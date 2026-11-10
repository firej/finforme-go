package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/evbogdanov/finforme/internal/exchangerates"
)

// CurrencyRatesAPI publishes the latest stored quote for each (code, source).
// Like /currency/, it contains only public market data and requires no login.
func (h *Handler) CurrencyRatesAPI(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	rates, err := h.latestCurrencyRates(ctx)
	if err != nil {
		log.Printf("CurrencyRatesAPI: %v", err)
		http.Error(w, "currency rates unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=30")
	if err := json.NewEncoder(w).Encode(rates); err != nil {
		log.Printf("CurrencyRatesAPI encode: %v", err)
	}
}

func (h *Handler) latestCurrencyRates(ctx context.Context) (exchangerates.LatestRates, error) {
	out := exchangerates.LatestRates{Rates: []exchangerates.CurrencyRate{}}
	rows, err := h.db.QueryContext(ctx, `
		SELECT cr.code, cr.name, cr.rate, cr.source, CAST(cr.rate_date AS CHAR)
		FROM currency_rates cr
		JOIN (
			SELECT code, source, MAX(rate_date) AS rate_date
			FROM currency_rates GROUP BY code, source
		) latest ON cr.code = latest.code AND cr.source = latest.source
			AND cr.rate_date = latest.rate_date
		ORDER BY cr.code, cr.source
	`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var rate exchangerates.CurrencyRate
		if err := rows.Scan(&rate.Code, &rate.Name, &rate.Rate, &rate.Source, &rate.RateDate); err != nil {
			return out, err
		}
		out.Rates = append(out.Rates, rate)
	}
	return out, rows.Err()
}
