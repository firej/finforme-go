package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/mux"
)

func rateBindingsTestHandler(t *testing.T) *Handler {
	t.Helper()
	h := financeTestHandler(t)
	if os.Getenv("FINFORME_TEST_MYSQL_DSN") == "" {
		for _, q := range []string{
			`CREATE TABLE currency_rates(code TEXT,source TEXT,name TEXT,rate DECIMAL(18,6),rate_date DATE,PRIMARY KEY(code,source,rate_date))`,
			`CREATE TABLE currency_rate_bindings(code TEXT,source TEXT,base_commodity_id INTEGER REFERENCES commodities(id),quote_commodity_id INTEGER REFERENCES commodities(id),PRIMARY KEY(code,source),CHECK(base_commodity_id<>quote_commodity_id))`,
		} {
			if _, err := h.db.Exec(q); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, source := range []string{"cbr", "other"} {
		if _, err := h.db.Exec(`INSERT INTO currency_rates(code,source,name,rate,rate_date) VALUES('USD/RUB',?,'Dollar',90,'2026-09-01')`, source); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

func TestFinanceRateBindings(t *testing.T) {
	h := rateBindingsTestHandler(t)
	router := mux.NewRouter()
	router.HandleFunc("/admin/rate-bindings/", h.RequireAdmin(h.AdminRateBindings)).Methods("GET", "POST")
	protected := http.NewCrossOriginProtection().Handler(router)
	admin, user := authCookie(t, h, 1), authCookie(t, h, 2)
	values := url.Values{"code": {"USD/RUB"}, "source": {"cbr"}, "base_id": {"2"}, "quote_id": {"1"}, "action": {"save"}}
	request := func(method string, cookie *http.Cookie, origin string) *httptest.ResponseRecorder {
		r := authRequest(method, "/admin/rate-bindings/", values, cookie)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		protected.ServeHTTP(w, r)
		return w
	}
	for _, method := range []string{"GET", "POST"} {
		if w := request(method, nil, ""); w.Code != 302 && w.Code != 303 {
			t.Fatalf("anonymous: %d", w.Code)
		}
		if w := request(method, user, ""); w.Code != 403 {
			t.Fatalf("user: %d", w.Code)
		}
	}
	if w := request("POST", admin, "https://evil.example"); w.Code != 403 {
		t.Fatalf("cross origin: %d", w.Code)
	}
	if w := request("GET", admin, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "не привязано") {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	for i := 0; i < 2; i++ {
		if w := request("POST", admin, ""); w.Code != 303 {
			t.Fatalf("save: %d %s", w.Code, w.Body.String())
		}
	}
	assertBinding := func(source string, want int) {
		t.Helper()
		var count int
		if err := h.db.QueryRow(`SELECT COUNT(*) FROM currency_rate_bindings WHERE code='USD/RUB' AND source=? AND base_commodity_id=2 AND quote_commodity_id=1`, source).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Fatalf("%s binding count: %d, want %d", source, count, want)
		}
	}
	assertBinding("cbr", 1)
	assertBinding("other", 0)
	if w := request("GET", admin, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `value="2" selected`) || !strings.Contains(w.Body.String(), "Снять привязку") {
		t.Fatalf("saved list: %d %s", w.Code, w.Body.String())
	}
	for _, tc := range []struct{ field, value string }{
		{"base_id", "1"}, {"base_id", "999"}, {"base_id", "invalid"}, {"quote_id", "-1"}, {"quote_id", "0"},
		{"source", "missing"}, {"code", "RUB/USD"}, {"action", "unknown"}, {"code", ""},
	} {
		original := values.Get(tc.field)
		values.Set(tc.field, tc.value)
		if w := request("POST", admin, ""); w.Code != 400 {
			t.Fatalf("%s=%s: %d %s", tc.field, tc.value, w.Code, w.Body.String())
		}
		values.Set(tc.field, original)
		assertBinding("cbr", 1)
	}
	// The second source gets its own binding; incoming dates do not need rebinding.
	values.Set("source", "other")
	if w := request("POST", admin, ""); w.Code != 303 {
		t.Fatalf("other source: %d", w.Code)
	}
	if _, err := h.db.Exec(`INSERT INTO currency_rates(code,source,name,rate,rate_date) VALUES('USD/RUB','cbr','Dollar',91,'2026-09-02')`); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM currency_rates r JOIN currency_rate_bindings b ON b.code=r.code AND b.source=r.source WHERE b.base_commodity_id=2 AND b.quote_commodity_id=1 AND b.source='cbr'`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("new quote binding: %d %v", count, err)
	}
	w := request("GET", admin, "")
	for _, want := range []string{"data-example-amount=\"9\u00a0100,00\"", "02.09.2026", "data-example-amount=\"9\u00a0000,00\"", "/static/admin-rate-bindings.js"} {
		if w.Code != 200 || !strings.Contains(w.Body.String(), want) {
			t.Fatalf("missing latest per-source example %q: status %d", want, w.Code)
		}
	}
	values.Set("source", "cbr")
	values.Set("action", "delete")
	if w := request("POST", admin, ""); w.Code != 303 {
		t.Fatalf("delete: %d", w.Code)
	}
	assertBinding("cbr", 0)
	assertBinding("other", 1)
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM currency_rates`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("quotes changed: %d %v", count, err)
	}
}

func TestFinanceRateBindingsConcurrentSave(t *testing.T) {
	h := rateBindingsTestHandler(t)
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- h.saveRateBinding(currencyRateBinding{Code: "USD/RUB", Source: "cbr", BaseID: 2, QuoteID: 1}, "save")
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM currency_rate_bindings`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("bindings: %d %v", count, err)
	}
}
