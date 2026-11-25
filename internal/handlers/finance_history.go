package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/evbogdanov/finforme/internal/amount"
)

type historySplit struct {
	AccountID   int64  `json:"account_id"`
	CommodityID int64  `json:"commodity_id"`
	Account     string `json:"account"`
	Currency    string `json:"currency"`
	Num         int64  `json:"numerator"`
	Denom       int64  `json:"denominator"`
	Amount      string `json:"amount"`
}
type transactionSnapshot struct {
	Num         string         `json:"num"`
	Date        time.Time      `json:"date"`
	Entered     time.Time      `json:"entered"`
	Description string         `json:"description"`
	Comment     string         `json:"comment"`
	Tags        string         `json:"tags"`
	Splits      []historySplit `json:"splits"`
}
type transactionHistory struct {
	ID            int64                `json:"id"`
	TransactionID int64                `json:"transaction_id"`
	Action        string               `json:"action"`
	Source        string               `json:"source"`
	CreatedAt     time.Time            `json:"created_at"`
	Before        *transactionSnapshot `json:"before"`
	After         *transactionSnapshot `json:"after"`
	RestoredID    int64                `json:"restored_id"`
}

func financeSource(r *http.Request) string {
	if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		return "api"
	}
	return "ui"
}
func snapshotTransaction(tx *sql.Tx, userID, id int64) (*transactionSnapshot, error) {
	s := &transactionSnapshot{}
	err := tx.QueryRow(`SELECT COALESCE(num,''),post_date,enter_date,COALESCE(description,''),COALESCE(comment,''),COALESCE(tags,'') FROM transactions WHERE user_id=? AND id=?`, userID, id).Scan(&s.Num, &s.Date, &s.Entered, &s.Description, &s.Comment, &s.Tags)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(`SELECT a.id,a.commodity_id,a.name,c.mnemonic,s.value_num,s.value_denom FROM splits s JOIN accounts a ON a.id=s.account_id AND a.user_id=s.user_id JOIN commodities c ON c.id=a.commodity_id WHERE s.user_id=? AND s.tx_id=? ORDER BY s.id`, userID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	s.Splits = []historySplit{}
	for rows.Next() {
		var p historySplit
		if err := rows.Scan(&p.AccountID, &p.CommodityID, &p.Account, &p.Currency, &p.Num, &p.Denom); err != nil {
			return nil, err
		}
		p.Amount = amount.Decimal(p.Num, p.Denom)
		s.Splits = append(s.Splits, p)
	}
	return s, rows.Err()
}
func recordTransactionHistory(tx *sql.Tx, userID, id int64, action, source string, before *transactionSnapshot) error {
	after, err := snapshotTransaction(tx, userID, id)
	if err != nil {
		return err
	}
	if source == "" {
		source = "api"
	}
	b, err := json.Marshal(before)
	if err != nil {
		return err
	}
	a, err := json.Marshal(after)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO transaction_history(user_id,transaction_id,action,source,created_at,before_json,after_json) VALUES(?,?,?,?,?,?,?)`, userID, id, action, source, time.Now().UTC(), string(b), string(a)); err != nil {
		return err
	}
	_, err = tx.Exec(`DELETE FROM transaction_checks WHERE user_id=? AND transaction_id=?`, userID, id)
	return err
}
func (h *Handler) transactionHistory(userID, transactionID, beforeID int64) ([]transactionHistory, error) {
	q := `SELECT id,transaction_id,action,source,created_at,before_json,after_json,restored_id FROM transaction_history WHERE user_id=?`
	args := []any{userID}
	if transactionID > 0 {
		q += ` AND transaction_id=?`
		args = append(args, transactionID)
	}
	if beforeID > 0 {
		q += ` AND id<?`
		args = append(args, beforeID)
	}
	rows, err := h.db.Query(q+` ORDER BY id DESC LIMIT 50`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []transactionHistory{}
	for rows.Next() {
		var item transactionHistory
		var b, a string
		if err := rows.Scan(&item.ID, &item.TransactionID, &item.Action, &item.Source, &item.CreatedAt, &b, &a, &item.RestoredID); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(b), &item.Before); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(a), &item.After); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (h *Handler) restoreDeletedTransaction(userID, historyID int64, source string) (int64, error) {
	tx, err := h.beginFinanceWrite(userID)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var action, b string
	var restored int64
	if err = tx.QueryRow(`SELECT action,before_json,restored_id FROM transaction_history WHERE user_id=? AND id=?`, userID, historyID).Scan(&action, &b, &restored); errors.Is(err, sql.ErrNoRows) {
		return 0, validationError("Запись истории не найдена")
	} else if err != nil {
		return 0, err
	}
	if action != "delete" {
		return 0, validationError("Восстановить можно только удалённую операцию")
	}
	if restored != 0 {
		return restored, nil
	}
	var s transactionSnapshot
	if err = json.Unmarshal([]byte(b), &s); err != nil {
		return 0, err
	}
	if len(s.Splits) == 0 {
		return 0, validationError("Нет проводок для восстановления")
	}
	for _, p := range s.Splits {
		var commodity int64
		var placeholder int
		if err = tx.QueryRow(`SELECT commodity_id,placeholder FROM accounts WHERE user_id=? AND id=?`, userID, p.AccountID).Scan(&commodity, &placeholder); err != nil || commodity != p.CommodityID || placeholder != 0 {
			return 0, validationError("Счёт удалён, стал контейнером или сменил валюту; восстановление отменено")
		}
	}
	res, err := tx.Exec(`INSERT INTO transactions(user_id,num,post_date,enter_date,description,comment,tags) VALUES(?,?,?,?,?,?,?)`, userID, s.Num, s.Date, s.Entered, s.Description, s.Comment, s.Tags)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	for _, p := range s.Splits {
		if _, err = tx.Exec(`INSERT INTO splits(user_id,tx_id,account_id,value_num,value_denom) VALUES(?,?,?,?,?)`, userID, id, p.AccountID, p.Num, p.Denom); err != nil {
			return 0, err
		}
	}
	if err = recordTransactionHistory(tx, userID, id, "restore", source, nil); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(`UPDATE transaction_history SET restored_id=? WHERE user_id=? AND id=?`, id, userID, historyID); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}
func (h *Handler) FinanceHistory(w http.ResponseWriter, r *http.Request) {
	userID, _ := h.getUserID(r)
	txID, _ := strconv.ParseInt(r.URL.Query().Get("transaction_id"), 10, 64)
	beforeID, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	items, err := h.transactionHistory(userID, txID, beforeID)
	if err != nil {
		http.Error(w, "Не удалось загрузить историю", 500)
		return
	}
	data := h.pageData(userID, "history")
	data["Title"] = "История изменений"
	data["History"] = items
	data["TransactionID"] = txID
	if len(items) == 50 {
		data["NextHistoryID"] = items[len(items)-1].ID
	}
	h.renderTemplate(w, "finance_history.html", data)
}
func (h *Handler) APITransactionRestore(w http.ResponseWriter, r *http.Request) {
	userID, _ := h.getUserID(r)
	id, err := strconv.ParseInt(r.FormValue("history_id"), 10, 64)
	if err != nil {
		writeFinanceError(w, validationError("Некорректный ID истории"))
		return
	}
	restored, err := h.restoreDeletedTransaction(userID, id, financeSource(r))
	if err != nil {
		writeFinanceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"result": "ok", "id": restored})
}

func (h *Handler) APITransactionHistory(w http.ResponseWriter, r *http.Request) {
	userID, _ := h.getUserID(r)
	id, _ := strconv.ParseInt(r.URL.Query().Get("transaction_id"), 10, 64)
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	items, err := h.transactionHistory(userID, id, before)
	if err != nil {
		writeFinanceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"history": items})
}
