package handlers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFinanceTransactionRates(t *testing.T) {
	h := rateBindingsTestHandler(t)
	ctx := context.Background()
	if err := h.saveRateBinding(currencyRateBinding{Code: "USD/RUB", Source: "cbr", BaseID: 2, QuoteID: 1}, "save"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO currency_rates(code,source,name,rate,rate_date) VALUES('USD/RUB','cbr','Dollar',92.123456,'2026-09-04')`,
		`INSERT INTO currency_rates(code,source,name,rate,rate_date) VALUES('USD/RUB','cbr','Dollar',99,'2026-09-07')`,
	} {
		if _, err := h.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	day := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	out, err := h.transactionRates(ctx, 2, 3, 2, day)
	if err != nil || len(out.Rates) != 1 {
		t.Fatalf("quotes: %+v %v", out, err)
	}
	rate := out.Rates[0]
	if out.FromCurrency != "USD" || out.ToCurrency != "RUB" || rate.Inverse || rate.Date != "2026-09-04" || rate.Rate != "92.123456" {
		t.Fatalf("wrong direct quote: %+v", out)
	}
	inverse, err := h.transactionRates(ctx, 2, 1, 3, day)
	if err != nil || len(inverse.Rates) != 1 || !inverse.Rates[0].Inverse || inverse.Rates[0].Rate != rate.Rate {
		t.Fatalf("inverse: %+v %v", inverse, err)
	}
	// Unbound sources are ignored; binding another source returns it separately.
	if err := h.saveRateBinding(currencyRateBinding{Code: "USD/RUB", Source: "other", BaseID: 2, QuoteID: 1}, "save"); err != nil {
		t.Fatal(err)
	}
	out, err = h.transactionRates(ctx, 2, 3, 2, day)
	if err != nil || len(out.Rates) != 2 {
		t.Fatalf("sources: %+v %v", out, err)
	}
	for _, date := range []time.Time{day.AddDate(0, 0, -10), day.AddDate(0, 0, 20)} {
		out, err = h.transactionRates(ctx, 2, 3, 2, date)
		if err != nil || len(out.Rates) != 0 {
			t.Fatalf("future/stale quote: %+v %v", out, err)
		}
	}
	out, err = h.transactionRates(ctx, 2, 1, 2, day)
	if err != nil || len(out.Rates) != 0 {
		t.Fatalf("same currency: %+v %v", out, err)
	}
	for _, accounts := range [][2]int64{{5, 3}, {3, 5}, {999, 3}, {6, 3}} {
		if _, err := h.transactionRates(ctx, 2, accounts[0], accounts[1], day); err == nil {
			t.Fatalf("accepted accounts: %v", accounts)
		}
	}
	if _, err := h.db.Exec(`UPDATE currency_rates SET rate=0 WHERE code='USD/RUB' AND source='cbr' AND rate_date='2026-09-04'`); err != nil {
		t.Fatal(err)
	}
	out, err = h.transactionRates(ctx, 2, 3, 2, day)
	if err != nil || len(out.Rates) != 1 || out.Rates[0].Source != "other" {
		t.Fatalf("invalid rate accepted: %+v %v", out, err)
	}
}

func TestFinanceTransactionRatesAPI(t *testing.T) {
	h := rateBindingsTestHandler(t)
	handler := h.RequireAuth(h.APITransactionRates)
	path := "/api/v1/finance/transaction/rates?credit_account=3&debit_account=2&post_date=2026-09-06"
	w := httptest.NewRecorder()
	handler(w, authRequest("GET", path, nil, nil))
	if w.Code != 302 && w.Code != 303 {
		t.Fatalf("anonymous: %d", w.Code)
	}
	w = httptest.NewRecorder()
	handler(w, authRequest("GET", path, nil, authCookie(t, h, 2)))
	if w.Code != 200 {
		t.Fatalf("get: %d %s", w.Code, w.Body.String())
	}
	var out transactionRatesOut
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.Rates == nil {
		t.Fatalf("response: %+v %v", out, err)
	}
	for _, query := range []string{"credit_account=bad", "credit_account=3&debit_account=5&post_date=2026-09-06", "credit_account=3&debit_account=2&post_date=invalid"} {
		w = httptest.NewRecorder()
		handler(w, authRequest("GET", "/api/v1/finance/transaction/rates?"+query, nil, authCookie(t, h, 2)))
		if w.Code != 400 {
			t.Fatalf("bad request: %d %s", w.Code, w.Body.String())
		}
	}
}
