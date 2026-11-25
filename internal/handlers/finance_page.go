package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const transactionPageSize = 50

type accountPageFilter struct {
	From, To     time.Time
	Search, Sort string
	Offset       int
	Focus        int64
}
type accountTransactionPage struct {
	Transactions                     []map[string]any `json:"-"`
	Total, IncomeCount, ExpenseCount int              // encoded explicitly by API below
	Income, Expense                  float64
	Offset                           int
	HasMore                          bool
}

func parseAccountPageFilter(q url.Values) (accountPageFilter, error) {
	f := accountPageFilter{Sort: q.Get("sort"), Search: q.Get("search")}
	if f.Sort != "asc" {
		f.Sort = "desc"
	}
	var err error
	for _, field := range []struct {
		name   string
		target *time.Time
	}{{"from", &f.From}, {"to", &f.To}} {
		if q.Get(field.name) != "" {
			*field.target, err = time.Parse("2006-01-02", q.Get(field.name))
			if err != nil {
				return f, validationError("Некорректный период")
			}
		}
	}
	if q.Get("month") != "" && f.From.IsZero() {
		f.From, err = time.Parse("2006-01", q.Get("month"))
		if err != nil {
			return f, validationError("Некорректный месяц")
		}
		f.To = f.From.AddDate(0, 1, 0)
	}
	if !f.From.IsZero() && !f.To.IsZero() && !f.To.After(f.From) {
		return f, validationError("Конец периода должен быть позже начала")
	}
	if q.Get("offset") != "" {
		f.Offset, err = strconv.Atoi(q.Get("offset"))
		if err != nil || f.Offset < 0 {
			return f, validationError("Некорректная страница")
		}
	}
	if q.Get("focus") != "" {
		f.Focus, err = strconv.ParseInt(q.Get("focus"), 10, 64)
		if err != nil || f.Focus <= 0 {
			return f, validationError("Некорректная операция")
		}
		f.From = time.Time{}
		f.To = time.Time{}
		f.Search = ""
	}
	if len(f.Search) > 1000 {
		return f, validationError("Слишком длинный поисковый запрос")
	}
	return f, nil
}

// Window balances are computed before applying search/period/pagination, so each
// visible row retains its actual historical balance, including hidden older rows.
const accountLedgerCTE = `WITH movements AS (
 SELECT t.id,t.post_date,COALESCE(t.description,'') AS description,COALESCE(t.comment,'') AS comment,COALESCE(t.tags,'') AS tags,
 SUM(CAST(s.value_num AS DECIMAL(65,16))*1.0/s.value_denom * CASE WHEN a.account_type='INCOME' THEN -1 ELSE 1 END) AS movement,
 SUM(CASE WHEN s.value_num * CASE WHEN a.account_type='INCOME' THEN -1 ELSE 1 END>0 THEN CAST(s.value_num AS DECIMAL(65,16))*1.0/s.value_denom * CASE WHEN a.account_type='INCOME' THEN -1 ELSE 1 END ELSE 0 END) AS income,
 SUM(CASE WHEN s.value_num * CASE WHEN a.account_type='INCOME' THEN -1 ELSE 1 END<0 THEN -CAST(s.value_num AS DECIMAL(65,16))*1.0/s.value_denom * CASE WHEN a.account_type='INCOME' THEN -1 ELSE 1 END ELSE 0 END) AS expense
 FROM transactions t JOIN splits s ON s.tx_id=t.id AND s.user_id=t.user_id JOIN accounts a ON a.id=s.account_id AND a.user_id=s.user_id
 WHERE t.user_id=? AND s.account_id=? GROUP BY t.id,t.post_date,t.description,t.comment,t.tags
), ledger AS (SELECT movements.*,SUM(movement) OVER (ORDER BY post_date,id ROWS UNBOUNDED PRECEDING) AS balance FROM movements) `

func (h *Handler) accountTransactionPage(ctx context.Context, userID, accountID int64, f accountPageFilter) (accountTransactionPage, error) {
	page := accountTransactionPage{Transactions: []map[string]any{}, Offset: f.Offset}
	// One read transaction keeps count, opening/running balances and rows consistent.
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	var owned int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts WHERE user_id=? AND id=?`, userID, accountID).Scan(&owned); err != nil {
		return page, err
	}
	if owned != 1 {
		return page, validationError("Счёт не найден")
	}
	where := " WHERE 1=1"
	args := []any{userID, accountID}
	if !f.From.IsZero() {
		where += " AND post_date>=?"
		args = append(args, f.From)
	}
	if !f.To.IsZero() {
		where += " AND post_date<?"
		args = append(args, f.To)
	}
	if f.Search != "" {
		where += ` AND (LOWER(description) LIKE LOWER(?) ESCAPE '!' OR LOWER(comment) LIKE LOWER(?) ESCAPE '!' OR LOWER(tags) LIKE LOWER(?) ESCAPE '!')`
		pattern := "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(f.Search) + "%"
		args = append(args, pattern, pattern, pattern)
	}
	err = tx.QueryRowContext(ctx, accountLedgerCTE+`SELECT COUNT(*),COALESCE(SUM(income),0),COALESCE(SUM(expense),0),COALESCE(SUM(CASE WHEN income>0 THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN expense>0 THEN 1 ELSE 0 END),0) FROM ledger`+where, args...).Scan(&page.Total, &page.Income, &page.Expense, &page.IncomeCount, &page.ExpenseCount)
	if err != nil {
		return page, err
	}
	order := "DESC"
	if f.Sort == "asc" {
		order = "ASC"
	}
	if f.Focus > 0 {
		var position int
		err = tx.QueryRowContext(ctx, accountLedgerCTE+`SELECT position FROM (SELECT id,ROW_NUMBER() OVER (ORDER BY post_date `+order+`,id `+order+`) - 1 AS position FROM ledger) ranked WHERE id=?`, userID, accountID, f.Focus).Scan(&position)
		if err != nil {
			return page, validationError("Операция не найдена на этом счёте")
		}
		page.Offset = position / transactionPageSize * transactionPageSize
	}
	if page.Offset >= page.Total && page.Total > 0 {
		page.Offset = (page.Total - 1) / transactionPageSize * transactionPageSize
	}
	query := accountLedgerCTE + `SELECT id,post_date,description,comment,tags,income,expense,balance FROM ledger` + where + ` ORDER BY post_date ` + order + `,id ` + order + ` LIMIT ? OFFSET ?`
	rows, err := tx.QueryContext(ctx, query, append(args, transactionPageSize, page.Offset)...)
	if err != nil {
		return page, err
	}
	byID := map[int64]map[string]any{}
	ids := []any{}
	for rows.Next() {
		var id int64
		var date time.Time
		var description, comment, tags string
		var income, expense, balance float64
		if err := rows.Scan(&id, &date, &description, &comment, &tags, &income, &expense, &balance); err != nil {
			rows.Close()
			return page, err
		}
		item := map[string]any{"id": id, "post_date": date.Format("02.01.2006"), "post_date_raw": date, "description": description, "comment": comment, "tags": strings.Split(tags, ","), "plus_balance_changing": income, "balance_changing": expense, "value_change": income - expense, "account_balance": balance, "account_name": "", "account_id": accountID}
		page.Transactions = append(page.Transactions, item)
		byID[id] = item
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, err
	}
	if len(ids) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
		extraArgs := []any{userID, accountID}
		extraArgs = append(extraArgs, ids...)
		others, err := tx.QueryContext(ctx, `SELECT DISTINCT s.tx_id,a.id,a.name FROM splits s JOIN accounts a ON a.id=s.account_id AND a.user_id=s.user_id WHERE s.user_id=? AND s.account_id<>? AND s.tx_id IN (`+placeholders+`) ORDER BY s.tx_id,a.id`, extraArgs...)
		if err != nil {
			return page, err
		}
		for others.Next() {
			var id, aid int64
			var name string
			if err := others.Scan(&id, &aid, &name); err != nil {
				others.Close()
				return page, err
			}
			item := byID[id]
			others, _ := item["other_accounts"].([]map[string]any)
			item["other_accounts"] = append(others, map[string]any{"id": aid, "name": name})
			if item["account_name"] == "" {
				item["account_id"] = aid
				item["account_name"] = name
			} else {
				item["account_name"] = fmt.Sprint(item["account_name"]) + ", " + name
			}
		}
		err = others.Err()
		others.Close()
		if err != nil {
			return page, err
		}
		checks, err := tx.QueryContext(ctx, `SELECT transaction_id FROM transaction_checks WHERE user_id=? AND transaction_id IN (`+placeholders+`)`, append([]any{userID}, ids...)...)
		if err != nil {
			return page, err
		}
		for checks.Next() {
			var id int64
			if err := checks.Scan(&id); err != nil {
				checks.Close()
				return page, err
			}
			byID[id]["checked"] = true
		}
		err = checks.Err()
		checks.Close()
		if err != nil {
			return page, err
		}
	}
	page.HasMore = page.Offset+len(page.Transactions) < page.Total
	return page, tx.Commit()
}

func (h *Handler) APITransactionPage(w http.ResponseWriter, r *http.Request) {
	userID, _ := h.getUserID(r)
	id, err := strconv.ParseInt(r.URL.Query().Get("account_id"), 10, 64)
	if err != nil {
		writeFinanceError(w, validationError("Некорректный счёт"))
		return
	}
	f, err := parseAccountPageFilter(r.URL.Query())
	if err != nil {
		writeFinanceError(w, err)
		return
	}
	page, err := h.accountTransactionPage(r.Context(), userID, id, f)
	if err != nil {
		writeFinanceError(w, err)
		return
	}
	var html bytes.Buffer
	if err := h.templates.ExecuteTemplate(&html, "finance_transactions_tbody.html", map[string]any{"Transactions": page.Transactions, "Account": map[string]any{"ID": id}}); err != nil {
		writeFinanceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"html": html.String(), "total": page.Total, "income": formatMoney(page.Income), "expense": formatMoney(page.Expense), "income_count": page.IncomeCount, "expense_count": page.ExpenseCount, "offset": page.Offset, "has_more": page.HasMore})
}
