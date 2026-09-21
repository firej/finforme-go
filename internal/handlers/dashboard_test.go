package handlers

import (
	"context"
	"math/big"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDashboardMonthlyReport(t *testing.T) {
	h := currencyFixture(t)
	// A previous-month expense, a current-month refund, and a bank transfer.
	for _, in := range []txSaveInput{
		{PostDate: time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC), CreditAccountID: 1, DebitAccountID: 2, Value: 80},
		{PostDate: time.Date(2026, 9, 30, 23, 59, 59, 0, time.UTC), CreditAccountID: 2, DebitAccountID: 1, Value: 25},
		{PostDate: time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC), CreditAccountID: 1, DebitAccountID: 3, Value: 90, ValueTarget: moneyPointer(1)},
		{PostDate: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), CreditAccountID: 1, DebitAccountID: 2, Value: 999},
	} {
		if _, err := h.saveTransaction(2, in); err != nil {
			t.Fatal(err)
		}
	}
	month := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	report, err := h.dashboardMonthlyReport(2, month, month, url.Values{"currency": {"USD"}, "rate_RUB_USD": {"USD/RUB@cbr"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Currencies) != 2 {
		t.Fatalf("currencies: %+v", report)
	}
	rub := report.Currencies[0]
	if rub.Currency != "RUB" || rub.Income != 500 || rub.Expense != 75 || rub.Net != 425 || rub.PreviousExpense != 80 {
		t.Fatalf("refund, boundary or transfer miscounted: %+v", rub)
	}
	if len(rub.Expenses) != 1 || rub.Expenses[0].Amount != 75 || rub.Expenses[0].Previous != 80 || rub.Expenses[0].URL != "/finance/account/2?month=2026-09" {
		t.Fatalf("categories: %+v", rub.Expenses)
	}
	if report.Currencies[1].Expense != 100 {
		t.Fatal("currencies mixed")
	}
	if !strings.Contains(report.PreviousURL, "month=2026-08") || !strings.Contains(report.NextURL, "currency=USD") || !strings.Contains(report.NextURL, "rate_RUB_USD=") {
		t.Fatal("navigation loses selections")
	}
	other, err := h.dashboardMonthlyReport(1, month, month, nil)
	if err != nil || len(other.Currencies) != 0 {
		t.Fatalf("other user: %+v %v", other, err)
	}
}

func TestDashboardMonthBoundariesAndComparison(t *testing.T) {
	now := time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)
	for _, invalid := range []string{"2024-13", "2024-2", "oops", "2024-02-01", "1899-12", "9999-01"} {
		if _, err := reportMonth(invalid, now); err == nil {
			t.Fatalf("accepted %s", invalid)
		}
	}
	month, err := reportMonth("", now)
	if err != nil || month.Format("2006-01-02") != "2024-02-01" || month.AddDate(0, 1, -1).Day() != 29 {
		t.Fatalf("leap month: %v %v", month, err)
	}
	previous := &PeriodReport{Totals: []CurrencyPeriodTotal{{Currency: "USD", TotalExpense: 12, Net: -12}}, Expense: []CategoryAmount{{AccountID: 9, AccountName: "Travel", Amount: 12, Currency: "USD"}}}
	report := buildDashboardMonth(month, now, &PeriodReport{}, previous, nil)
	if !report.Current || report.Currencies[0].Expense != 0 || report.Currencies[0].Expenses[0].Previous != 12 || report.Currencies[0].Expenses[0].Amount != 0 {
		t.Fatalf("disappearing category lost: %+v", report)
	}
	if amountChange(50, 0) != "+50,00" || amountChange(0, 0) != "Без изменений" {
		t.Fatal("zero baseline creates bogus percentages")
	}
	january, _ := reportMonth("2026-01", now)
	report = buildDashboardMonth(january, now, &PeriodReport{}, &PeriodReport{}, nil)
	if !strings.Contains(report.PreviousURL, "2025-12") {
		t.Fatal("year boundary")
	}
}

func TestDashboardCapitalHoldings(t *testing.T) {
	h := currencyFixture(t)
	in := validFinanceInput()
	in.PostDate = in.PostDate.AddDate(0, 1, 0)
	if _, err := h.saveTransaction(2, in); err != nil {
		t.Fatal(err)
	}
	holdings, err := h.capitalHoldings(context.Background(), 2, validFinanceInput().PostDate)
	want := []capitalHolding{{"RUB", 40000, 0}, {"USD", 25000, 5000}}
	if err != nil || !reflect.DeepEqual(holdings, want) {
		t.Fatalf("hidden/container/debt/future: %+v %v", holdings, err)
	}
	foreign, err := h.capitalHoldings(context.Background(), 1, validFinanceInput().PostDate)
	if err != nil || len(foreign) != 1 || foreign[0].Assets != 0 || foreign[0].Debt != 0 {
		t.Fatalf("leaked holdings: %+v %v", foreign, err)
	}
}

func TestDashboardCapitalConversion(t *testing.T) {
	quotes := []capitalQuote{
		{Base: "USD", Quote: "RUB", Code: "USD/RUB", Source: "cbr", Rate: "90", Date: "2026-09-04"},
		{Base: "USD", Quote: "RUB", Code: "USD/RUB", Source: "other", Rate: "100", Date: "2026-09-05"},
		{Base: "USD", Quote: "ARS", Code: "USD/ARS", Source: "bluedollar_sell", Rate: "1400", Date: "2026-09-04"},
		{Base: "EUR", Quote: "USD", Code: "EUR/USD", Source: "test", Rate: "1.2", Date: "2026-09-04"},
	}
	holdings := []capitalHolding{{"RUB", 10000, 0}, {"USD", 10000, 2000}, {"ARS", 140000, 0}, {"EUR", 0, 1000}}
	day := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	report := buildDashboardCapital(holdings, quotes, "RUB", day, nil)
	if report.Assets != "9\u00a0190,00" || report.Debt != "2\u00a0880,00" || report.Net != "6\u00a0310,00" || len(report.Missing) != 0 {
		t.Fatalf("direct/inverse/cross/debt: %+v", report)
	}
	if report.Rows[2].Route != "ARS → USD → RUB" || report.Rows[3].Converted != "−1\u00a0080,00" {
		t.Fatalf("paths: %+v", report.Rows)
	}
	usd := buildDashboardCapital(holdings, quotes, "USD", day, nil)
	if usd.Net != "70,11" {
		t.Fatalf("base currency: %+v", usd)
	}
	custom := buildDashboardCapital(holdings, quotes, "RUB", day, url.Values{"rate_RUB_USD": {"USD/RUB@other"}})
	if custom.Net != "7\u00a0000,00" {
		t.Fatalf("source ignored: %+v", custom)
	}
	fallback := buildDashboardCapital(holdings, quotes, "RUB", day, url.Values{"rate_RUB_USD": {"USD/RUB@expired"}})
	if len(fallback.Warnings) != 1 || fallback.Net != report.Net {
		t.Fatalf("silent source fallback: %+v", fallback)
	}
	rounded := buildDashboardCapital([]capitalHolding{{"USD", 2, 1}}, []capitalQuote{{Base: "USD", Quote: "RUB", Code: "USD/RUB", Source: "test", Rate: "0.6"}}, "RUB", day, nil)
	if rounded.Assets != "0,01" || rounded.Debt != "0,01" || rounded.Net != "0,00" || rounded.Rows[0].Converted != "0,00" {
		t.Fatalf("displayed totals do not add up: %+v", rounded)
	}
	// Equal assets and debts in an unconvertible currency still mean incomplete coverage.
	holdings = append(holdings, capitalHolding{"GBP", 300, 300}, capitalHolding{"CHF", 0, 0})
	partial := buildDashboardCapital(holdings, quotes, "RUB", day, nil)
	if !reflect.DeepEqual(partial.Missing, []string{"GBP"}) || partial.Net != report.Net {
		t.Fatalf("partial: %+v", partial)
	}
	if capitalMoney(big.NewRat(9007199254740993, 1)) != "90\u00a0071\u00a0992\u00a0547\u00a0409,93" || capitalMoney(big.NewRat(-1, 2)) != "−0,01" {
		t.Fatal("conversion lost precision or half-up rounding")
	}
}

func TestDashboardCapitalQuotes(t *testing.T) {
	h := rateBindingsTestHandler(t)
	for _, source := range []string{"cbr", "other"} {
		if err := h.saveRateBinding(currencyRateBinding{Code: "USD/RUB", Source: source, BaseID: 2, QuoteID: 1}, "save"); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`INSERT INTO currency_rates(code,source,name,rate,rate_date) VALUES('USD/RUB','cbr','Dollar',92.123456,'2026-09-04')`,
		`INSERT INTO currency_rates(code,source,name,rate,rate_date) VALUES('USD/RUB','cbr','Dollar',999,'2026-09-07')`,
		`INSERT INTO currency_rates(code,source,name,rate,rate_date) VALUES('USD/RUB','unbound','Dollar',200,'2026-09-04')`,
	} {
		if _, err := h.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	day := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	quotes, err := h.capitalQuotes(context.Background(), day)
	if err != nil || len(quotes) != 2 || quotes[0].Rate != "92.123456" || quotes[0].Date != "2026-09-04" {
		t.Fatalf("latest quotes: %+v %v", quotes, err)
	}
	for _, date := range []time.Time{day.AddDate(0, 0, -10), day.AddDate(0, 0, 20)} {
		quotes, err = h.capitalQuotes(context.Background(), date)
		if err != nil || len(quotes) != 0 {
			t.Fatalf("future/stale quotes: %+v %v", quotes, err)
		}
	}
	if _, err := h.db.Exec(`UPDATE currency_rates SET rate=0 WHERE source='cbr' AND rate_date='2026-09-04'`); err != nil {
		t.Fatal(err)
	}
	quotes, err = h.capitalQuotes(context.Background(), day)
	if err != nil || len(quotes) != 1 || quotes[0].Source != "other" {
		t.Fatalf("invalid rate: %+v %v", quotes, err)
	}
}

func TestDashboardPage(t *testing.T) {
	h := rateBindingsTestHandler(t)
	if _, err := h.saveTransaction(2, validFinanceInput()); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.renderDashboard(w, httptest.NewRequest("GET", "/?month=2026-09&currency=USD", nil), 2)
	body := w.Body.String()
	for _, want := range []string{"Общий капитал", "Отчёт за месяц", "Сентябрь 2026", "Август 2026", "Неполная оценка", "Расходы по категориям", `/finance/account/2?month=2026-09`, "Новости"} {
		if w.Code != 200 || !strings.Contains(body, want) {
			t.Fatalf("missing %q, status=%d: %s", want, w.Code, body)
		}
	}
	invalid := httptest.NewRecorder()
	h.renderDashboard(invalid, httptest.NewRequest("GET", "/?month=invalid", nil), 2)
	if invalid.Code != 400 {
		t.Fatalf("invalid month: %d", invalid.Code)
	}
}
