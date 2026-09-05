package handlers

import (
	"sort"
	"time"

	"github.com/evbogdanov/finforme/internal/models"
)

// CategoryAmount — сумма по одному счёту-категории
type CategoryAmount struct {
	AccountID   int64   `json:"account_id"`
	AccountName string  `json:"account_name"`
	Amount      float64 `json:"amount"`
	Currency    string  `json:"currency"`
}

// PeriodReport — отчёт по доходам и расходам за период
type PeriodReport struct {
	From    string                `json:"from"`
	To      string                `json:"to"`
	Totals  []CurrencyPeriodTotal `json:"totals"`
	Income  []CategoryAmount      `json:"income"`
	Expense []CategoryAmount      `json:"expense"`
}

type CurrencyPeriodTotal struct {
	Currency     string  `json:"currency"`
	TotalIncome  float64 `json:"total_income"`
	TotalExpense float64 `json:"total_expense"`
	Net          float64 `json:"net"`
}

// periodReport считает доходы и расходы пользователя за период по категориям.
// from/to — включительно (to трактуется как конец дня).
// Используется MCP-инструментом get_report.
func (h *Handler) periodReport(userID int64, from, to time.Time) (*PeriodReport, error) {
	if to.Before(from) {
		return nil, validationError("Конец периода раньше начала")
	}
	rows, err := h.db.Query(`
		SELECT a.id, a.name, a.account_type, c.mnemonic, SUM(s.value_num)
		FROM splits s
		JOIN transactions t ON t.id = s.tx_id AND t.user_id = s.user_id
		JOIN accounts a ON a.id = s.account_id AND a.user_id = s.user_id
		JOIN commodities c ON c.id = a.commodity_id
		WHERE s.user_id = ?
		  AND a.account_type IN ('INCOME', 'EXPENSE')
		  AND t.post_date >= ? AND t.post_date < ?
		GROUP BY a.id, a.name, a.account_type, c.mnemonic
		HAVING SUM(s.value_num) <> 0
		ORDER BY ABS(SUM(s.value_num)) DESC
	`, userID, from, to.AddDate(0, 0, 1))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	report := &PeriodReport{
		From:    from.Format("2006-01-02"),
		To:      to.Format("2006-01-02"),
		Income:  []CategoryAmount{},
		Expense: []CategoryAmount{},
		Totals:  []CurrencyPeriodTotal{},
	}

	totals := make(map[string]*CurrencyPeriodTotal)
	for rows.Next() {
		var id, valueNum int64
		var name, accountType, currency string
		if err := rows.Scan(&id, &name, &accountType, &currency, &valueNum); err != nil {
			return nil, err
		}
		amount := float64(valueNum) / 100.0
		if totals[currency] == nil {
			totals[currency] = &CurrencyPeriodTotal{Currency: currency}
		}
		total := totals[currency]

		switch accountType {
		case models.AccountTypeIncome:
			// Доход хранится отрицательным (кредит) — инвертируем в положительный
			amount = -amount
			report.Income = append(report.Income, CategoryAmount{
				AccountID: id, AccountName: name, Amount: amount, Currency: currency,
			})
			total.TotalIncome += amount
		case models.AccountTypeExpense:
			report.Expense = append(report.Expense, CategoryAmount{
				AccountID: id, AccountName: name, Amount: amount, Currency: currency,
			})
			total.TotalExpense += amount
		}
	}
	for _, total := range totals {
		total.Net = total.TotalIncome - total.TotalExpense
		report.Totals = append(report.Totals, *total)
	}
	sort.Slice(report.Totals, func(i, j int) bool { return report.Totals[i].Currency < report.Totals[j].Currency })

	return report, rows.Err()
}

// AccountBalance — счёт с балансом и валютой (для MCP list_accounts)
type AccountBalance struct {
	ID          int64                    `json:"id"`
	Name        string                   `json:"name"`
	AccountType string                   `json:"account_type"`
	Currency    string                   `json:"currency"`
	Balance     float64                  `json:"balance"`
	Placeholder bool                     `json:"placeholder"`
	Hidden      bool                     `json:"hidden"`
	Balances    []models.CurrencyBalance `json:"balances"`
}

// accountsWithBalances возвращает счета пользователя с балансами и валютой.
// Используется MCP-инструментом list_accounts.
func (h *Handler) accountsWithBalances(userID int64) ([]AccountBalance, error) {
	accounts, err := h.getAccountsWithBalance(userID)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]*models.Account, len(accounts))
	for _, a := range accounts {
		byID[a.ID] = a
	}
	h.buildAccountTree(accounts, byID)
	result := make([]AccountBalance, 0, len(accounts))
	for _, a := range accounts {
		result = append(result, AccountBalance{ID: a.ID, Name: a.Name, AccountType: a.AccountType, Currency: a.Currency,
			Balance: a.Balance, Placeholder: a.Placeholder == 1, Hidden: a.Hidden == 1, Balances: a.GetBalances()})
	}
	return result, nil
}

// DashboardCurrencyTotal contains independent totals for one account currency.
type DashboardCurrencyTotal struct {
	Currency                                                           string
	TotalAssets, TotalLiabilities, NetWorth, TotalIncome, TotalExpense float64
}

func (h *Handler) dashboardTotals(userID int64) ([]DashboardCurrencyTotal, error) {
	rows, err := h.db.Query(`SELECT c.mnemonic,a.account_type,COALESCE(SUM(s.value_num),0)
 FROM accounts a JOIN commodities c ON c.id=a.commodity_id
 LEFT JOIN splits s ON s.account_id=a.id AND s.user_id=a.user_id
 WHERE a.user_id=? AND a.account_type IN ('ASSET','BANK','CASH','LIABILITY','INCOME','EXPENSE')
 GROUP BY c.mnemonic,a.account_type ORDER BY c.mnemonic,a.account_type`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	totals := make(map[string]*DashboardCurrencyTotal)
	for rows.Next() {
		var currency, kind string
		var raw int64
		if err := rows.Scan(&currency, &kind, &raw); err != nil {
			return nil, err
		}
		if totals[currency] == nil {
			totals[currency] = &DashboardCurrencyTotal{Currency: currency}
		}
		total := totals[currency]
		amount := float64(raw) / 100
		switch kind {
		case "ASSET", "BANK", "CASH":
			total.TotalAssets += amount
		case "LIABILITY":
			total.TotalLiabilities -= amount
		case "INCOME":
			total.TotalIncome -= amount
		case "EXPENSE":
			total.TotalExpense += amount
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make([]DashboardCurrencyTotal, 0, len(totals))
	for _, total := range totals {
		total.NetWorth = total.TotalAssets - total.TotalLiabilities
		result = append(result, *total)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Currency < result[j].Currency })
	return result, nil
}
