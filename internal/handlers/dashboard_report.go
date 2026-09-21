package handlers

import (
	"fmt"
	"math"
	"net/url"
	"sort"
	"time"
)

type monthCategory struct {
	AccountID        int64
	Name, URL        string
	Amount, Previous float64
	Change           string
	Bar              float64
}
type monthCurrencyReport struct {
	Currency                                     string
	Income, Expense, Net                         float64
	PreviousIncome, PreviousExpense, PreviousNet float64
	IncomeChange, ExpenseChange, NetChange       string
	Expenses, Incomes                            []monthCategory
}
type dashboardMonth struct {
	Value, Label, PreviousLabel, PreviousURL, NextURL string
	Current                                           bool
	Currencies                                        []monthCurrencyReport
}

func reportMonth(value string, now time.Time) (time.Time, error) {
	if value == "" {
		value = now.Format("2006-01")
	}
	month, err := time.Parse("2006-01", value)
	if err != nil || month.Year() < 1900 || month.Year() > 9998 {
		return time.Time{}, validationError("Выберите месяц в формате ГГГГ-ММ")
	}
	return month, nil
}
func monthLabel(month time.Time) string {
	names := []string{"Январь", "Февраль", "Март", "Апрель", "Май", "Июнь", "Июль", "Август", "Сентябрь", "Октябрь", "Ноябрь", "Декабрь"}
	return fmt.Sprintf("%s %d", names[month.Month()-1], month.Year())
}
func currencyLess(a, b string) bool {
	priority := func(c string) int {
		switch c {
		case "RUB":
			return 0
		case "USD":
			return 1
		case "EUR":
			return 2
		case "ARS":
			return 3
		}
		return 4
	}
	if priority(a) != priority(b) {
		return priority(a) < priority(b)
	}
	return a < b
}
func amountChange(current, previous float64) string {
	delta := current - previous
	if math.Abs(delta) < .005 {
		return "Без изменений"
	}
	sign := ""
	if delta > 0 {
		sign = "+"
	}
	if previous <= 0 {
		return sign + formatMoney(delta)
	}
	return fmt.Sprintf("%s%s · %+.0f%%", sign, formatMoney(delta), delta/previous*100)
}
func monthCategories(current, previous []CategoryAmount, currency, month string) []monthCategory {
	rows := map[int64]*monthCategory{}
	for _, c := range previous {
		if c.Currency == currency {
			rows[c.AccountID] = &monthCategory{AccountID: c.AccountID, Name: c.AccountName, Previous: c.Amount}
		}
	}
	for _, c := range current {
		if c.Currency != currency {
			continue
		}
		if rows[c.AccountID] == nil {
			rows[c.AccountID] = &monthCategory{AccountID: c.AccountID, Name: c.AccountName}
		}
		rows[c.AccountID].Amount = c.Amount
	}
	max := 0.0
	for _, row := range rows {
		if row.Amount > max {
			max = row.Amount
		}
	}
	result := make([]monthCategory, 0, len(rows))
	for _, row := range rows {
		row.URL = fmt.Sprintf("/finance/account/%d?month=%s", row.AccountID, month)
		row.Change = amountChange(row.Amount, row.Previous)
		if max > 0 && row.Amount > 0 {
			row.Bar = row.Amount / max
		}
		result = append(result, *row)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Amount != result[j].Amount {
			return result[i].Amount > result[j].Amount
		}
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		return result[i].AccountID < result[j].AccountID
	})
	return result
}
func buildDashboardMonth(month, now time.Time, current, previous *PeriodReport, query url.Values) dashboardMonth {
	result := dashboardMonth{Value: month.Format("2006-01"), Label: monthLabel(month), PreviousLabel: monthLabel(month.AddDate(0, -1, 0)), Current: month.Format("2006-01") == now.Format("2006-01")}
	link := func(offset int) string {
		copy := url.Values{}
		for key, values := range query {
			copy[key] = append([]string(nil), values...)
		}
		copy.Set("month", month.AddDate(0, offset, 0).Format("2006-01"))
		return "/?" + copy.Encode() + "#monthly-report"
	}
	if month.Year() > 1900 || month.Month() > 1 {
		result.PreviousURL = link(-1)
	}
	if month.Year() < 9998 || month.Month() < 12 {
		result.NextURL = link(1)
	}
	currencies := map[string]*monthCurrencyReport{}
	for _, total := range previous.Totals {
		currencies[total.Currency] = &monthCurrencyReport{Currency: total.Currency, PreviousIncome: total.TotalIncome, PreviousExpense: total.TotalExpense, PreviousNet: total.Net}
	}
	for _, total := range current.Totals {
		if currencies[total.Currency] == nil {
			currencies[total.Currency] = &monthCurrencyReport{Currency: total.Currency}
		}
		c := currencies[total.Currency]
		c.Income = total.TotalIncome
		c.Expense = total.TotalExpense
		c.Net = total.Net
	}
	for _, c := range currencies {
		c.IncomeChange = amountChange(c.Income, c.PreviousIncome)
		c.ExpenseChange = amountChange(c.Expense, c.PreviousExpense)
		c.NetChange = amountChange(c.Net, c.PreviousNet)
		c.Expenses = monthCategories(current.Expense, previous.Expense, c.Currency, result.Value)
		c.Incomes = monthCategories(current.Income, previous.Income, c.Currency, result.Value)
		result.Currencies = append(result.Currencies, *c)
	}
	sort.Slice(result.Currencies, func(i, j int) bool { return currencyLess(result.Currencies[i].Currency, result.Currencies[j].Currency) })
	return result
}

func (h *Handler) dashboardMonthlyReport(userID int64, month, now time.Time, query url.Values) (dashboardMonth, error) {
	current, err := h.periodReport(userID, month, month.AddDate(0, 1, -1))
	if err != nil {
		return dashboardMonth{}, err
	}
	previous, err := h.periodReport(userID, month.AddDate(0, -1, 0), month.AddDate(0, 0, -1))
	if err != nil {
		return dashboardMonth{}, err
	}
	return buildDashboardMonth(month, now, current, previous, query), nil
}
