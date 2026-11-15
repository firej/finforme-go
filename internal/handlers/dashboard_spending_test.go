package handlers

import (
	"strings"
	"testing"
)

func TestDashboardSpendingTrendUsesElapsedDaysAndKeepsRefunds(t *testing.T) {
	h := financeTestHandler(t)
	for _, in := range []txSaveInput{
		{PostDate: historyDay("2024-02-01"), CreditAccountID: 1, DebitAccountID: 2, Value: 30},
		{PostDate: historyDay("2024-02-29"), CreditAccountID: 1, DebitAccountID: 2, Value: 70},
		{PostDate: historyDay("2024-03-01"), CreditAccountID: 1, DebitAccountID: 2, Value: 50},
		{PostDate: historyDay("2024-03-03"), CreditAccountID: 2, DebitAccountID: 1, Value: 50},
		{PostDate: historyDay("2024-03-04"), CreditAccountID: 1, DebitAccountID: 2, Value: 1000},
	} {
		if _, err := h.saveTransaction(2, in); err != nil {
			t.Fatal(err)
		}
	}
	trends, err := h.dashboardSpendingTrends(2, historyDay("2024-03-01"), historyDay("2024-03-03"))
	if err != nil {
		t.Fatal(err)
	}
	trend := trends["RUB"]
	if trend.CurrentTotal != "0,00" || trend.PreviousTotal != "30,00" || trend.CurrentThrough != 3 || trend.PreviousThrough != 3 || trend.ChangeSign != -1 {
		t.Fatalf("unexpected comparison: %+v", trend)
	}
	if len(trend.Days) != 29 || trend.Days[1].Current != "50,00" || trend.Days[2].Current != "0,00" || trend.Days[3].Current != "—" || trend.Days[28].Previous != "100,00" || strings.Count(trend.CurrentLine, "L") != 3 {
		t.Fatalf("unexpected daily points: %+v", trend)
	}
}

func TestDashboardSpendingTrendShowsFullyRefundedCurrency(t *testing.T) {
	h := financeTestHandler(t)
	for _, in := range []txSaveInput{
		{PostDate: historyDay("2026-08-03"), CreditAccountID: 1, DebitAccountID: 2, Value: 10},
		{PostDate: historyDay("2026-08-06"), CreditAccountID: 2, DebitAccountID: 1, Value: 10},
	} {
		if _, err := h.saveTransaction(2, in); err != nil {
			t.Fatal(err)
		}
	}
	report, err := h.dashboardMonthlyReport(2, historyDay("2026-08-01"), historyDay("2026-09-01"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Currencies) != 1 || report.Currencies[0].Currency != "RUB" || report.Currencies[0].SpendingTrend.Days[2].Current != "10,00" || report.Currencies[0].Expense != 0 {
		t.Fatalf("fully refunded currency disappeared: %+v", report)
	}
}
