package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/evbogdanov/finforme/internal/models"
)

func currencyFixture(t *testing.T) *Handler {
	t.Helper()
	h := financeTestHandler(t)
	for _, a := range []struct {
		id, commodity int
		kind, name    string
	}{
		{7, 2, "EXPENSE", "USD expenses"}, {8, 1, "INCOME", "RUB income"}, {9, 2, "INCOME", "USD income"}, {10, 2, "LIABILITY", "USD debt"}, {11, 2, "ASSET", "Nested USD"},
	} {
		if _, err := h.db.Exec(`INSERT INTO accounts(id,user_id,name,account_type,commodity_id,commodity_scu,non_std_scu,placeholder) VALUES(?,2,?,?,?,100,0,?)`, a.id, a.name, a.kind, a.commodity, a.id == 11); err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range []struct {
		from, to int64
		amount   float64
	}{{8, 1, 500}, {9, 3, 300}, {1, 2, 100}, {3, 7, 100}, {10, 3, 50}} {
		in := validFinanceInput()
		in.CreditAccountID = v.from
		in.DebitAccountID = v.to
		in.Value = v.amount
		if _, err := h.saveTransaction(2, in); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{`UPDATE accounts SET parent_id=6 WHERE id IN (1,11)`, `UPDATE accounts SET parent_id=11, hidden=1 WHERE id=3`} {
		if _, err := h.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

func TestFinanceCurrencyReport(t *testing.T) {
	h := currencyFixture(t)
	date := validFinanceInput().PostDate
	report, err := h.periodReport(2, date, date)
	if err != nil {
		t.Fatal(err)
	}
	want := []CurrencyPeriodTotal{{Currency: "RUB", TotalIncome: 500, TotalExpense: 100, Net: 400}, {Currency: "USD", TotalIncome: 300, TotalExpense: 100, Net: 200}}
	if !reflect.DeepEqual(report.Totals, want) {
		t.Fatalf("mixed report: %+v", report)
	}
	for _, category := range append(report.Income, report.Expense...) {
		if category.Currency == "" {
			t.Fatal("category has no currency")
		}
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["total_expense"]; ok {
		t.Fatal("ambiguous scalar total still present")
	}
	empty, err := h.periodReport(2, date.AddDate(0, 0, 1), date.AddDate(0, 0, 1))
	if err != nil || len(empty.Totals) != 0 {
		t.Fatal("period end is not inclusive only for the selected day", err)
	}
	foreign, err := h.periodReport(1, date, date)
	if err != nil || len(foreign.Totals) != 0 {
		t.Fatal("report leaks another user", err)
	}
	if _, err := h.periodReport(2, date.AddDate(0, 0, 1), date); err == nil {
		t.Fatal("reversed dates accepted")
	}
}

func TestFinanceCurrencyDashboardAndTree(t *testing.T) {
	h := currencyFixture(t)
	totals, err := h.dashboardTotals(2)
	if err != nil {
		t.Fatal(err)
	}
	want := []DashboardCurrencyTotal{{Currency: "RUB", TotalAssets: 400, NetWorth: 400, TotalIncome: 500, TotalExpense: 100}, {Currency: "USD", TotalAssets: 250, TotalLiabilities: 50, NetWorth: 200, TotalIncome: 300, TotalExpense: 100}}
	if !reflect.DeepEqual(totals, want) {
		t.Fatalf("mixed dashboard totals: %+v", totals)
	}
	accounts, err := h.accountsWithBalances(2)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range accounts {
		if a.ID == 6 {
			found = true
			expected := []models.CurrencyBalance{{Currency: "RUB", Amount: 400}, {Currency: "USD", Amount: 250}}
			if !reflect.DeepEqual(a.Balances, expected) || a.Balance != 0 {
				t.Fatalf("wrong parent totals: %+v", a)
			}
		}
	}
	if !found {
		t.Fatal("parent missing")
	}
	w := httptest.NewRecorder()
	h.FinanceIndex(w, authRequest("GET", "/finance/", nil, authCookie(t, h, 2)))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "400.00 RUB") || !strings.Contains(w.Body.String(), "250.00 USD") {
		t.Fatalf("tree currencies missing: %d %s", w.Code, w.Body.String())
	}
	if os.Getenv("FINFORME_TEST_MYSQL_DSN") != "" {
		w = httptest.NewRecorder()
		h.renderDashboard(w, httptest.NewRequest("GET", "/", nil), 2)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `data-currency="RUB"`) || !strings.Contains(w.Body.String(), `data-currency="USD"`) {
			t.Fatalf("dashboard failed: %d %s", w.Code, w.Body.String())
		}
	}
}

func currencyPreview(t *testing.T, h *Handler, source int64, rows []csvImportRow) map[string]json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(csvImportPreviewReq{SourceAccountID: source, Rows: rows})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/v1/finance/import/csv/preview", strings.NewReader(string(raw)))
	r.AddCookie(authCookie(t, h, 2))
	w := httptest.NewRecorder()
	h.APIImportCSVPreview(w, r)
	if w.Code != 200 {
		t.Fatalf("preview %d: %s", w.Code, w.Body.String())
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func currencySave(t *testing.T, h *Handler, source int64, items []csvImportSaveItem) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(csvImportSaveReq{SourceAccountID: source, Items: items})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/v1/finance/import/csv/save", strings.NewReader(string(raw)))
	r.AddCookie(authCookie(t, h, 2))
	w := httptest.NewRecorder()
	h.APIImportCSVSave(w, r)
	return w
}

func TestFinanceCurrencyCSVImbalance(t *testing.T) {
	h := financeTestHandler(t)
	if _, err := h.db.Exec(`UPDATE accounts SET name='Дисбаланс-RUB',account_type='EQUITY' WHERE id=4`); err != nil {
		t.Fatal(err)
	}
	// Previous cross-currency transaction must not suggest its RUB counter-account.
	in := validFinanceInput()
	in.CreditAccountID = 3
	in.DebitAccountID = 2
	in.Value = 1
	in.ValueTarget = moneyPointer(100)
	in.Description = "exchange"
	if _, err := h.saveTransaction(2, in); err != nil {
		t.Fatal(err)
	}
	result := currencyPreview(t, h, 3, []csvImportRow{
		{Date: "2026-09-06", Amount: -100, Currency: "USD", Description: "exchange"},
		{Date: "2026-09-06", Amount: -100, Currency: "USD", OtherID: 2},
		{Date: "2026-09-06", Amount: -100, Currency: "RUB"},
	})
	var id int64
	var items []csvImportPreviewItem
	if err := json.Unmarshal(result["imbalance_account_id"], &id); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(result["items"], &items); err != nil {
		t.Fatal(err)
	}
	if id == 4 || len(items) != 3 || items[0].OtherAccountID != id || items[0].Error != "" || items[1].Error == "" || items[2].Error == "" {
		t.Fatalf("unsafe CSV preview: %s", result)
	}
	account, err := h.getAccountByID(2, id)
	if err != nil || account.CommodityID != 2 {
		t.Fatal("imbalance uses wrong currency", err)
	}
	again, _, err := h.getOrCreateImbalanceAccount(2, 2)
	if err != nil || again != id {
		t.Fatal("duplicate imbalance", err)
	}
	rub, _, err := h.getOrCreateImbalanceAccount(2, 1)
	if err != nil || rub != 4 {
		t.Fatal("existing RUB imbalance not reused", err)
	}
}

func TestFinanceCurrencyCSVSaveIsAtomic(t *testing.T) {
	h := financeTestHandler(t)
	usd, _, err := h.getOrCreateImbalanceAccount(2, 2)
	if err != nil {
		t.Fatal(err)
	}
	valid := csvImportSaveItem{Date: "2026-09-05", Amount: -100, Currency: "USD", OtherAccountID: usd, Description: "USD expense"}
	for _, tc := range []struct {
		name   string
		change func(*csvImportSaveItem)
	}{
		{"different account currency", func(i *csvImportSaveItem) { i.OtherAccountID = 2 }},
		{"different row currency", func(i *csvImportSaveItem) { i.Currency = "RUB" }},
		{"zero", func(i *csvImportSaveItem) { i.Amount = 0 }},
		{"excess precision", func(i *csvImportSaveItem) { i.Amount = 1.001 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := valid
			tc.change(&bad)
			w := currencySave(t, h, 3, []csvImportSaveItem{valid, bad})
			if w.Code != 400 {
				t.Fatalf("invalid CSV accepted: %d %s", w.Code, w.Body.String())
			}
			var count int
			if err := h.db.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&count); err != nil || count != 0 {
				t.Fatal("CSV partially saved", err)
			}
		})
	}
	valid.Currency = " usd "
	w := currencySave(t, h, 3, []csvImportSaveItem{valid})
	if w.Code != 200 {
		t.Fatal("valid USD CSV rejected", w.Body.String())
	}
	var sum int64
	var count int
	if err := h.db.QueryRow(`SELECT COUNT(*),SUM(value_num) FROM splits`).Scan(&count, &sum); err != nil || count != 2 || sum != 0 {
		t.Fatal("CSV not balanced", err)
	}
}

func TestFinanceCurrencyConcurrentImbalance(t *testing.T) {
	if os.Getenv("FINFORME_TEST_MYSQL_DSN") == "" {
		t.Skip("requires InnoDB row locks")
	}
	h := financeTestHandler(t)
	type result struct {
		id  int64
		err error
	}
	done := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() { id, _, err := h.getOrCreateImbalanceAccount(2, 2); done <- result{id, err} }()
	}
	first, second := <-done, <-done
	if first.err != nil || second.err != nil || first.id != second.id {
		t.Fatalf("concurrent imbalance: %+v %+v", first, second)
	}
}

func TestFinanceCurrencyEmptyDashboard(t *testing.T) {
	h := financeTestHandler(t)
	if _, err := h.db.Exec(`DELETE FROM accounts WHERE user_id=1`); err != nil {
		t.Fatal(err)
	}
	totals, err := h.dashboardTotals(1)
	if err != nil || len(totals) != 0 {
		t.Fatal("empty dashboard invents a currency", err)
	}
	var html strings.Builder
	if err := h.templates.ExecuteTemplate(&html, "index.html", map[string]interface{}{"CurrencyTotals": totals}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html.String(), "Пока нет счетов") {
		t.Fatal("empty state missing")
	}
}
