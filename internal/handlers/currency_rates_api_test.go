package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/evbogdanov/finforme/internal/exchangerates"
)

func ratesAPIHandler(t *testing.T) *Handler {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE TABLE currency_rates (
		code TEXT, name TEXT, rate REAL, source TEXT, rate_date TEXT,
		PRIMARY KEY (code, source, rate_date))`)
	if err != nil {
		t.Fatal(err)
	}
	return &Handler{db: db}
}

func TestCurrencyRatesAPIPublicLatestPerSource(t *testing.T) {
	h := ratesAPIHandler(t)
	_, err := h.db.Exec(`INSERT INTO currency_rates VALUES
		('USD/RUB', 'Dollar', 80, 'cbr', '2026-11-10'),
		('USD/RUB', 'Dollar', 82, 'cbr', '2026-11-11'),
		('USD/RUB', 'Dollar', 90, 'other', '2026-11-09'),
		('BTC/USD', 'Bitcoin', 73000, 'coinbase', '2026-11-10')`)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	// No session or user is needed, and the handler has no session store.
	h.CurrencyRatesAPI(w, httptest.NewRequest(http.MethodGet, "/api/currency/rates", nil))
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("response: %d %s", w.Code, w.Body.String())
	}
	var data exchangerates.LatestRates
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Rates) != 3 {
		t.Fatalf("expected latest quote per source: %+v", data)
	}
	bySource := make(map[string]exchangerates.CurrencyRate)
	for _, rate := range data.Rates {
		bySource[rate.Source] = rate
	}
	if bySource["cbr"].Rate != 82 || bySource["cbr"].RateDate != "2026-11-11" || bySource["other"].Rate != 90 || bySource["other"].RateDate != "2026-11-09" {
		t.Fatalf("sources or quote dates mixed: %+v", data)
	}
}

func TestCurrencyRatesAPIEmptyAndUnavailable(t *testing.T) {
	h := ratesAPIHandler(t)
	w := httptest.NewRecorder()
	h.CurrencyRatesAPI(w, httptest.NewRequest(http.MethodGet, "/api/currency/rates", nil))
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"rates":[]}` {
		t.Fatalf("empty response: %d %s", w.Code, w.Body.String())
	}
	h.db.Close()
	w = httptest.NewRecorder()
	h.CurrencyRatesAPI(w, httptest.NewRequest(http.MethodGet, "/api/currency/rates", nil))
	if w.Code != http.StatusServiceUnavailable || strings.TrimSpace(w.Body.String()) != "currency rates unavailable" {
		t.Fatalf("database error exposed or hidden: %d %s", w.Code, w.Body.String())
	}
}
