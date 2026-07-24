package handlers

import (
	"time"

	"github.com/evbogdanov/finforme/internal/models"
)

// CategoryAmount — сумма по одному счёту-категории
type CategoryAmount struct {
	AccountID   int64   `json:"account_id"`
	AccountName string  `json:"account_name"`
	Amount      float64 `json:"amount"`
}

// PeriodReport — отчёт по доходам и расходам за период
type PeriodReport struct {
	From         string           `json:"from"`
	To           string           `json:"to"`
	TotalIncome  float64          `json:"total_income"`
	TotalExpense float64          `json:"total_expense"`
	Net          float64          `json:"net"`
	Income       []CategoryAmount `json:"income"`
	Expense      []CategoryAmount `json:"expense"`
}

// periodReport считает доходы и расходы пользователя за период по категориям.
// from/to — включительно (to трактуется как конец дня).
// Используется MCP-инструментом get_report.
func (h *Handler) periodReport(userID int64, from, to time.Time) (*PeriodReport, error) {
	rows, err := h.db.Query(`
		SELECT a.id, a.name, a.account_type, SUM(s.value_num)
		FROM splits s
		JOIN transactions t ON t.id = s.tx_id
		JOIN accounts a ON a.id = s.account_id
		WHERE s.user_id = ?
		  AND a.account_type IN ('INCOME', 'EXPENSE')
		  AND t.post_date >= ? AND t.post_date < ?
		GROUP BY a.id, a.name, a.account_type
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
	}

	for rows.Next() {
		var id, valueNum int64
		var name, accountType string
		if err := rows.Scan(&id, &name, &accountType, &valueNum); err != nil {
			continue
		}
		amount := float64(valueNum) / 100.0

		switch accountType {
		case models.AccountTypeIncome:
			// Доход хранится отрицательным (кредит) — инвертируем в положительный
			amount = -amount
			report.Income = append(report.Income, CategoryAmount{
				AccountID: id, AccountName: name, Amount: amount,
			})
			report.TotalIncome += amount
		case models.AccountTypeExpense:
			report.Expense = append(report.Expense, CategoryAmount{
				AccountID: id, AccountName: name, Amount: amount,
			})
			report.TotalExpense += amount
		}
	}
	report.Net = report.TotalIncome - report.TotalExpense

	return report, rows.Err()
}

// AccountBalance — счёт с балансом и валютой (для MCP list_accounts)
type AccountBalance struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	AccountType string  `json:"account_type"`
	Currency    string  `json:"currency"`
	Balance     float64 `json:"balance"`
	Placeholder bool    `json:"placeholder"`
	Hidden      bool    `json:"hidden"`
}

// accountsWithBalances возвращает счета пользователя с балансами и валютой.
// Используется MCP-инструментом list_accounts.
func (h *Handler) accountsWithBalances(userID int64) ([]AccountBalance, error) {
	rows, err := h.db.Query(`
		SELECT a.id, a.name, a.account_type, COALESCE(c.mnemonic, ''),
		       COALESCE(SUM(s.value_num), 0), a.placeholder, a.hidden
		FROM accounts a
		LEFT JOIN splits s ON s.account_id = a.id
		LEFT JOIN commodities c ON c.id = a.commodity_id
		WHERE a.user_id = ? AND a.account_type <> 'ROOT'
		GROUP BY a.id, a.name, a.account_type, c.mnemonic, a.placeholder, a.hidden
		ORDER BY a.name
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]AccountBalance, 0)
	for rows.Next() {
		var ab AccountBalance
		var balanceRaw int64
		var placeholder, hidden int
		if err := rows.Scan(&ab.ID, &ab.Name, &ab.AccountType, &ab.Currency,
			&balanceRaw, &placeholder, &hidden); err != nil {
			continue
		}
		ab.Balance = float64(balanceRaw) / 100.0
		// Для доходных/пассивных счетов баланс инвертируется (как в UI)
		acc := models.Account{AccountType: ab.AccountType}
		if acc.IsNegativeBalance() {
			ab.Balance = -ab.Balance
		}
		ab.Placeholder = placeholder == 1
		ab.Hidden = hidden == 1
		result = append(result, ab)
	}
	return result, rows.Err()
}
