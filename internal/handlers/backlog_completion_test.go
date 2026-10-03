package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/evbogdanov/finforme/internal/models"
	"github.com/gorilla/mux"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func backlogMCPCall(t *testing.T, h *Handler) func(string, map[string]any, bool) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	ct, st := mcp.NewInMemoryTransports()
	ss, err := h.buildMCPServer(2).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "backlog-test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return func(name string, args map[string]any, wantError bool) map[string]any {
		t.Helper()
		r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if r.IsError != wantError {
			t.Fatalf("%s: %+v", name, r.Content)
		}
		if wantError {
			return nil
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
}

func TestFinanceTransactionTime(t *testing.T) {
	h := financeTestHandler(t)
	call := backlogMCPCall(t, h)
	args := map[string]any{"date": "2026-10-03", "time": "23:59:42", "description": "Timed", "amount": 10.25, "from_account_id": 1, "to_account_id": 2}
	out := call("create_transaction", args, false)
	id := int64(out["id"].(float64))
	check := func(want string) {
		t.Helper()
		got := call("get_transaction", map[string]any{"id": id}, false)
		if got["time"] != want {
			t.Fatalf("time: %v; want %s", got, want)
		}
	}
	check("23:59:42")
	args["id"] = id
	delete(args, "time")
	args["date"] = "2026-10-04"
	call("update_transaction", args, false)
	check("23:59:42")
	args["time"] = "24:00"
	call("update_transaction", args, true)
	check("23:59:42")
	args["time"] = "00:00"
	call("update_transaction", args, false)
	check("00:00:00")
	cookie := authCookie(t, h, 2)
	values := url.Values{"id": {fmt.Sprint(id)}, "post_date": {"2026-10-05"}, "post_time": {"12:34:56"}, "description": {"UI time"}, "value": {"10.25"}, "credit_account": {"1"}, "debit_account": {"2"}}
	w := httptest.NewRecorder()
	h.APITransactionSave(w, authRequest("POST", "/api/v1/finance/transaction/save", values, cookie))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	check("12:34:56")
	w = httptest.NewRecorder()
	h.APITransactionFormGet(w, authRequest("GET", fmt.Sprintf("/api/v1/finance/transaction/form?account_id=1&tx_id=%d", id), nil, cookie))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `value="12:34:56"`) {
		t.Fatal("form lost time", w.Body.String())
	}
	// A complex operation still permits time changes through metadata only.
	complex := complexFinanceTransaction(t, h)
	before := splitSnapshot(t, h, complex)
	metadata := map[string]any{"id": complex, "date": "2026-10-06", "time": "08:09:10", "description": "Complex", "tags": ""}
	call("update_transaction_metadata", metadata, false)
	id = complex
	check("08:09:10")
	delete(metadata, "time")
	metadata["date"] = "2026-10-07"
	call("update_transaction_metadata", metadata, false)
	check("08:09:10")
	values = url.Values{"id": {fmt.Sprint(id)}, "post_date": {"2026-10-08"}, "post_time": {"09:10"}, "description": {"Metadata"}, "tags": {""}}
	w = httptest.NewRecorder()
	h.APITransactionMetadataSave(w, authRequest("POST", "/api/v1/finance/transaction/metadata", values, cookie))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	check("09:10:00")
	if !reflect.DeepEqual(before, splitSnapshot(t, h, id)) {
		t.Fatal("time edit changed splits")
	}
}

func TestFinanceHistoricalCurrencyRatesMCP(t *testing.T) {
	h := rateBindingsTestHandler(t)
	for _, q := range []string{
		`INSERT INTO currency_rates(code,source,name,rate,rate_date) VALUES('USD/RUB','cbr','Dollar',66.5,'2018-11-24')`,
		`INSERT INTO currency_rates(code,source,name,rate,rate_date) VALUES('USD/RUB','cbr','Dollar',67,'2018-11-28')`,
		`INSERT INTO currency_rates(code,source,name,rate,rate_date) VALUES('USD/RUB','other','Dollar',80,'2018-11-27')`,
		`INSERT INTO currency_rates(code,source,name,rate,rate_date) VALUES('EUR/RUB','cbr','Euro',100,'2026-10-03')`,
	} {
		if _, err := h.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	call := backlogMCPCall(t, h)
	out := call("get_currency_rates", map[string]any{"date": "2018-11-27"}, false)
	rates := out["rates"].([]any)
	if len(rates) != 1 {
		t.Fatal(out)
	}
	rate := rates[0].(map[string]any)
	if rate["rate"] != 66.5 || rate["rate_date"] != "2018-11-24" || rate["source"] != "cbr" || rate["code"] != "USD/RUB" {
		t.Fatal(out)
	}
	if !reflect.DeepEqual(out["missing_pairs"], []any{"EUR/RUB"}) {
		t.Fatal(out)
	}
	out = call("get_currency_rates", map[string]any{"date": "2010-01-01"}, false)
	if len(out["rates"].([]any)) != 0 || out["message"] == nil {
		t.Fatal("history fell back to current rate", out)
	}
	call("get_currency_rates", map[string]any{"date": "2018-02-30"}, true)
	if os.Getenv("FINFORME_TEST_MYSQL_DSN") != "" {
		latest := call("get_currency_rates", map[string]any{}, false)
		rates := latest["rates"].([]any)
		if len(rates) != 3 || latest["updated_at"] == "" {
			t.Fatal("latest rates behavior changed", latest)
		}
		for _, entry := range rates {
			rate := entry.(map[string]any)
			if rate["code"] == "USD/RUB" && rate["rate"] != 90.0 {
				t.Fatal("latest returned a historical rate", rate)
			}
		}
	}

}

func TestFinanceIncomePresentation(t *testing.T) {
	h := financeTestHandler(t)
	if _, err := h.db.Exec(`UPDATE accounts SET account_type='INCOME' WHERE id=4`); err != nil {
		t.Fatal(err)
	}
	in := validFinanceInput()
	in.CreditAccountID = 4
	in.DebitAccountID = 1
	id, err := h.saveTransaction(2, in)
	if err != nil {
		t.Fatal(err)
	}
	before := splitSnapshot(t, h, id)
	txs := h.getAccountTransactions(2, 4, "asc")
	if len(txs) != 1 || txs[0]["plus_balance_changing"] != 100.0 || txs[0]["account_balance"] != 100.0 {
		t.Fatal(txs)
	}
	refund := in
	refund.CreditAccountID = 1
	refund.DebitAccountID = 4
	refund.Value = 25
	if _, err := h.saveTransaction(2, refund); err != nil {
		t.Fatal(err)
	}
	txs = h.getAccountTransactions(2, 4, "asc")
	if txs[1]["balance_changing"] != 25.0 || txs[1]["account_balance"] != 75.0 {
		t.Fatal(txs)
	}
	if !reflect.DeepEqual(before, splitSnapshot(t, h, id)) {
		t.Fatal("presentation changed ledger")
	}
	a := models.Account{AccountType: models.AccountTypeIncome, Balance: -100, Currency: "RUB", Childs: []*models.Account{{AccountType: models.AccountTypeIncome, Balance: 25, Currency: "RUB"}}}
	if a.DisplayBalances()[0].Amount != 75 || a.GetBalances()[0].Amount != -75 || a.Balance != -100 {
		t.Fatal("display mutated balances")
	}
}

func TestFinanceNotFoundPages(t *testing.T) {
	h := financeTestHandler(t)
	router := mux.NewRouter()
	router.NotFoundHandler = http.HandlerFunc(h.NotFound)
	router.HandleFunc("/finance/account/{id}/edit", h.FinanceAccountEdit)
	router.HandleFunc("/finance/account/{id}", h.FinanceAccountView)
	router.HandleFunc("/finance/transaction/{account_id}/{tx_id}", h.FinanceTransaction)
	router.HandleFunc("/api/v1/finance/account/form", h.APIAccountFormGet)
	router.HandleFunc("/api/v1/finance/transaction/form", h.APITransactionFormGet)
	cookie := authCookie(t, h, 2)
	for _, path := range []string{"/missing", "/finance/account/9999", "/finance/account/5", "/finance/account/nope", "/finance/account/9999/edit", "/finance/transaction/1/9999", "/finance/transaction/9999/1", "/api/v1/finance/account/form?account_id=9999", "/api/v1/finance/account/form?account_id=5", "/api/v1/finance/transaction/form?tx_id=9999"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, authRequest("GET", path, nil, cookie))
		if w.Code != 404 || !strings.Contains(w.Body.String(), "Вернуться к счетам") || !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
}

func TestAdminCurrencyPrecisionPreservesSplits(t *testing.T) {
	h := financeTestHandler(t)
	id, err := h.saveTransaction(2, validFinanceInput())
	if err != nil {
		t.Fatal(err)
	}
	before := splitSnapshot(t, h, id)
	for _, fraction := range []int{1, 10, 1000, 100000000, 100} {
		c := models.Commodity{ID: 1, Mnemonic: "RUB", Fullname: "Рубль", Sign: "₽", Fraction: fraction}
		if err := h.saveAdminCommodity(c); err != nil {
			t.Fatal(err)
		}
		var saved int
		if err := h.db.QueryRow(`SELECT fraction FROM commodities WHERE id=1`).Scan(&saved); err != nil || saved != fraction {
			t.Fatal(saved, err)
		}
		if !reflect.DeepEqual(before, splitSnapshot(t, h, id)) {
			t.Fatal("precision edit changed splits")
		}
	}
}
