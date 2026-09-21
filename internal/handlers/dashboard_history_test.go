package handlers

import (
	"context"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func historyDay(value string) time.Time {
	day, err := time.Parse("2006-01-02", value)
	if err != nil {
		panic(err)
	}
	return day
}

func TestCapitalHistoryDates(t *testing.T) {
	dates := historyDates(historyDay("2024-03-15"))
	if len(dates) != 13 || dates[0].Format("2006-01-02") != "2023-03-31" || dates[11].Format("2006-01-02") != "2024-02-29" || dates[12].Format("2006-01-02") != "2024-03-15" {
		t.Fatalf("calendar boundaries: %v", dates)
	}
	january := historyDates(historyDay("2026-01-01"))
	if january[0].Format("2006-01-02") != "2025-01-31" || january[11].Format("2006-01-02") != "2025-12-31" {
		t.Fatalf("year rollover: %v", january)
	}
}
func TestCapitalHistorySnapshots(t *testing.T) {
	h := currencyFixture(t)
	// Opening data predates the displayed year; future spending must be excluded.
	for _, in := range []txSaveInput{
		{PostDate: historyDay("2025-09-30"), CreditAccountID: 8, DebitAccountID: 1, Value: 1000},
		{PostDate: historyDay("2025-10-01"), CreditAccountID: 1, DebitAccountID: 2, Value: 50},
		{PostDate: historyDay("2026-09-20"), CreditAccountID: 1, DebitAccountID: 2, Value: 999},
		{PostDate: historyDay("2026-09-06"), CreditAccountID: 10, DebitAccountID: 3, Value: 200},
	} {
		if _, err := h.saveTransaction(2, in); err != nil {
			t.Fatal(err)
		}
	}
	dates := historyDates(historyDay("2026-09-19"))
	snapshots, hasData, err := h.capitalSnapshots(context.Background(), 2, dates)
	if err != nil || !hasData || len(snapshots) != 13 {
		t.Fatalf("snapshots: %v %v %v", snapshots, hasData, err)
	}
	if !reflect.DeepEqual(snapshots[0].Holdings, []capitalHolding{{"RUB", 100000, 0}}) || snapshots[1].Holdings[0].Assets != 95000 {
		t.Fatalf("opening/first-day boundary: %+v", snapshots[:2])
	}
	current, err := h.capitalHoldings(context.Background(), 2, dates[12])
	if err != nil || !reflect.DeepEqual(snapshots[12].Holdings, current) {
		t.Fatalf("history and headline disagree: %+v != %+v: %v", snapshots[12].Holdings, current, err)
	}
	foreign, hasData, err := h.capitalSnapshots(context.Background(), 1, dates)
	if err != nil || hasData || len(foreign[12].Holdings) != 0 {
		t.Fatalf("other user's history leaked: %+v %v", foreign, err)
	}
}
func TestCapitalHistoryGrowthAndFX(t *testing.T) {
	snapshots := []capitalSnapshot{
		{historyDay("2026-01-31"), []capitalHolding{{"USD", 10000, 2000}}},
		{historyDay("2026-02-28"), []capitalHolding{{"USD", 12000, 2000}}},
		// Paying a debt with cash changes both balances, leaving net capital intact.
		{historyDay("2026-03-15"), []capitalHolding{{"USD", 10000, 0}}},
	}
	quotes := []capitalQuote{
		{Base: "USD", Quote: "RUB", Code: "USD/RUB", Source: "cbr", Rate: "90", Date: "2026-01-30"},
		{Base: "USD", Quote: "RUB", Code: "USD/RUB", Source: "cbr", Rate: "100", Date: "2026-02-28"},
		{Base: "USD", Quote: "RUB", Code: "USD/RUB", Source: "cbr", Rate: "80", Date: "2026-03-14"},
	}
	result := buildCapitalHistory(snapshots, quotes, "RUB", "2026-02", url.Values{"report_currency": {"USD"}})
	feb, march := result.Rows[0], result.Rows[1]
	if !result.Known || result.Start != "7\u00a0200,00" || result.End != "8\u00a0000,00" || result.Change != "+800,00" {
		t.Fatalf("period: %+v", result)
	}
	if !feb.Known || feb.Change != "+2\u00a0800,00" || feb.BalanceChange != "+1\u00a0800,00" || feb.FXChange != "+1\u00a0000,00" || feb.Sign != "positive" || feb.Height != 44 {
		t.Fatalf("positive month: %+v", feb)
	}
	if !march.Known || march.Change != "−2\u00a0000,00" || march.BalanceChange != "0,00" || march.FXChange != "−2\u00a0000,00" || march.Sign != "negative" || march.Height <= 0 || march.Height >= 44 {
		t.Fatalf("debt payment/FX loss: %+v", march)
	}
	if !feb.Selected || feb.Current || !march.Current || !strings.Contains(feb.URL, "month=2026-02") || !strings.Contains(feb.URL, "currency=RUB") || !strings.Contains(feb.URL, "report_currency=USD") || !strings.HasSuffix(feb.URL, "#monthly-report") {
		t.Fatalf("drilldown: %+v", feb)
	}
	// Valuing in USD removes the currency effect without changing cash movements.
	usd := buildCapitalHistory(snapshots, quotes, "USD", "", nil)
	if usd.Rows[0].Change != "+20,00" || usd.Rows[0].FXChange != "0,00" || usd.Rows[1].Change != "0,00" {
		t.Fatalf("base currency: %+v", usd)
	}
}
func TestCapitalHistoryMissingQuotes(t *testing.T) {
	snapshots := []capitalSnapshot{
		{historyDay("2026-01-31"), []capitalHolding{{"USD", 10000, 0}}},
		{historyDay("2026-02-28"), []capitalHolding{{"USD", 10000, 0}}},
		{historyDay("2026-03-15"), []capitalHolding{{"USD", 10000, 0}}},
	}
	quotes := []capitalQuote{
		{Base: "USD", Quote: "RUB", Code: "USD/RUB", Source: "cbr", Rate: "90", Date: "2026-01-31"},
		{Base: "USD", Quote: "RUB", Code: "USD/RUB", Source: "cbr", Rate: "100", Date: "2026-03-15"},
	}
	result := buildCapitalHistory(snapshots, quotes, "RUB", "", nil)
	for _, row := range result.Rows {
		if row.Known || row.Change != "—" || row.Sign != "missing" || !strings.Contains(row.Status, "28.02.2026: USD") {
			t.Fatalf("missing quote became profit/loss: %+v", row)
		}
	}
	if result.Rows[0].End != "—" || result.Rows[1].End != "10\u00a0000,00" || result.Change != "+1\u00a0000,00" {
		t.Fatalf("endpoint estimates: %+v", result)
	}
	// Even when no historical graph mentions RUB, never silently switch to USD.
	noRates := buildCapitalHistory(snapshots, nil, "RUB", "", nil)
	if noRates.Known || noRates.Start != "—" || noRates.End != "—" {
		t.Fatalf("unit switched: %+v", noRates)
	}
	// A newly funded currency can have a known growth but unknown decomposition.
	snapshots[0].Holdings = nil
	result = buildCapitalHistory(snapshots[:2], []capitalQuote{{Base: "USD", Quote: "RUB", Code: "USD/RUB", Source: "cbr", Rate: "90", Date: "2026-02-28"}}, "RUB", "", nil)
	if !result.Rows[0].Known || result.Rows[0].BreakdownKnown || result.Rows[0].BalanceChange != "—" || result.Rows[0].Change != "+9\u00a0000,00" {
		t.Fatalf("new holding: %+v", result)
	}
}
func TestCapitalHistoricalQuoteSelection(t *testing.T) {
	history := []capitalQuote{
		{Base: "USD", Quote: "RUB", Code: "USD/RUB", Source: "cbr", Rate: "88", Date: "2026-01-01"},
		{Base: "USD", Quote: "RUB", Code: "USD/RUB", Source: "cbr", Rate: "90", Date: "2026-01-30"},
		{Base: "USD", Quote: "RUB", Code: "USD/RUB", Source: "cbr", Rate: "200", Date: "2026-02-01"},
		{Base: "USD", Quote: "RUB", Code: "USD/RUB", Source: "other", Rate: "92", Date: "2026-01-31"},
	}
	query := url.Values{"rate_RUB_USD": {"USD/RUB@cbr"}}
	quotes := historyQuotesAt(history, historyDay("2026-01-31"), query)
	if len(quotes) != 1 || quotes[0].Rate != "90" {
		t.Fatalf("future/wrong source: %+v", quotes)
	}
	history = append(history, capitalQuote{Base: "USD", Quote: "RUB", Code: "USD/RUB", Source: "cbr", Rate: "0", Date: "2026-01-31"})
	if quotes = historyQuotesAt(history, historyDay("2026-01-31"), query); len(quotes) != 0 {
		t.Fatalf("invalid latest quote revived old data: %+v", quotes)
	}
	if quotes = historyQuotesAt(history, historyDay("2026-02-28"), nil); len(quotes) != 0 {
		t.Fatalf("stale quote: %+v", quotes)
	}
	query.Set("rate_RUB_USD", "USD/RUB@missing")
	if quotes = historyQuotesAt(history, historyDay("2026-01-31"), query); len(quotes) != 0 {
		t.Fatalf("silently changed source: %+v", quotes)
	}
}
func TestCapitalHistoryHandlerAndTemplate(t *testing.T) {
	h := rateBindingsTestHandler(t)
	if err := h.saveRateBinding(currencyRateBinding{Code: "USD/RUB", Source: "cbr", BaseID: 2, QuoteID: 1}, "save"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.saveTransaction(2, validFinanceInput()); err != nil {
		t.Fatal(err)
	}
	day := historyDay("2026-09-19")
	report, err := h.dashboardCapitalHistory(context.Background(), 2, day, "RUB", "2026-09", nil)
	if err != nil || !report.HasData || len(report.Rows) != 12 || report.Rows[11].Change != "−100,00" {
		t.Fatalf("history: %+v %v", report, err)
	}
	quotes, err := h.capitalQuoteHistory(context.Background(), historyDates(day))
	if err != nil || len(quotes) != 1 || quotes[0].Source != "cbr" {
		t.Fatalf("unbound series used: %+v %v", quotes, err)
	}
	w := httptest.NewRecorder()
	h.renderDashboard(w, httptest.NewRequest("GET", "/?month=2026-09", nil), 2)
	body := w.Body.String()
	for _, want := range []string{"Прирост капитала по месяцам", "history-negative", "Суммы по месяцам и влияние курсов", "data-history-link", "#monthly-report"} {
		if w.Code != 200 || !strings.Contains(body, want) {
			t.Fatalf("missing %q: status %d: %s", want, w.Code, body)
		}
	}
	if strings.Contains(body, "ZgotmplZ") {
		t.Fatal("chart geometry rejected by template escaping")
	}
}

func TestCapitalHistoryPinsDefaultSource(t *testing.T) {
	snapshots := []capitalSnapshot{
		{historyDay("2026-01-31"), []capitalHolding{{"ARS", 140000, 0}}},
		{historyDay("2026-02-28"), []capitalHolding{{"ARS", 140000, 0}}},
	}
	quotes := []capitalQuote{
		{Base: "USD", Quote: "ARS", Code: "USD/ARS", Source: "cbr", Rate: "1000", Date: "2026-01-31"},
		{Base: "USD", Quote: "ARS", Code: "USD/ARS", Source: "cbr", Rate: "1000", Date: "2026-02-28"},
		{Base: "USD", Quote: "ARS", Code: "USD/ARS", Source: "bluedollar_sell", Rate: "1400", Date: "2026-02-28"},
	}
	result := buildCapitalHistory(snapshots, quotes, "USD", "", nil)
	if result.Known || result.Rows[0].Known || result.Rows[0].Change != "—" || !strings.Contains(result.Rows[0].URL, "bluedollar_sell") {
		t.Fatalf("source switch produced fictitious growth: %+v", result)
	}
	official := buildCapitalHistory(snapshots, quotes, "USD", "", url.Values{"rate_ARS_USD": {"USD/ARS@cbr"}})
	if !official.Known || official.Rows[0].Change != "0,00" {
		t.Fatalf("explicit source ignored: %+v", official)
	}
}
