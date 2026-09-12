package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"strconv"
	"time"
)

// Preserve the stored decimal rate so the browser can calculate using integers.
type transactionRate struct {
	Code    string `json:"code"`
	Source  string `json:"source"`
	Rate    string `json:"rate"`
	Date    string `json:"date"`
	Inverse bool   `json:"inverse"`
}
type transactionRatesOut struct {
	FromCurrency string            `json:"from_currency"`
	ToCurrency   string            `json:"to_currency"`
	Rates        []transactionRate `json:"rates"`
}

func (h *Handler) APITransactionRates(w http.ResponseWriter, r *http.Request) {
	userID, _ := h.getUserID(r)
	from, err := strconv.ParseInt(r.URL.Query().Get("credit_account"), 10, 64)
	if err != nil || from <= 0 {
		writeFinanceError(w, validationError("Некорректный счёт списания"))
		return
	}
	to, err := strconv.ParseInt(r.URL.Query().Get("debit_account"), 10, 64)
	if err != nil || to <= 0 {
		writeFinanceError(w, validationError("Некорректный счёт зачисления"))
		return
	}
	date, err := time.Parse("2006-01-02", r.URL.Query().Get("post_date"))
	if err != nil {
		writeFinanceError(w, validationError("Некорректная дата"))
		return
	}
	out, err := h.transactionRates(r.Context(), userID, from, to, date)
	if err != nil {
		writeFinanceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(out)
}

func (h *Handler) transactionRates(ctx context.Context, userID, from, to int64, date time.Time) (transactionRatesOut, error) {
	out := transactionRatesOut{Rates: []transactionRate{}}
	var fromCurrency, toCurrency int64
	for _, item := range []struct {
		id       int64
		currency *int64
		code     *string
	}{{from, &fromCurrency, &out.FromCurrency}, {to, &toCurrency, &out.ToCurrency}} {
		var placeholder int
		err := h.db.QueryRowContext(ctx, `SELECT a.commodity_id,c.mnemonic,a.placeholder FROM accounts a JOIN commodities c ON c.id=a.commodity_id WHERE a.id=? AND a.user_id=?`, item.id, userID).Scan(item.currency, item.code, &placeholder)
		if errors.Is(err, sql.ErrNoRows) {
			return out, validationError("Счёт не найден")
		}
		if err != nil {
			return out, err
		}
		if placeholder != 0 {
			return out, validationError("Выберите конечный счёт")
		}
	}
	if fromCurrency == toCurrency {
		return out, nil
	}
	// A series is selected by its explicit registry binding, never by code text.
	// Each source independently uses its latest date, with no future quotes.
	rows, err := h.db.QueryContext(ctx, `SELECT r.code,r.source,r.rate,r.rate_date,b.base_commodity_id
 FROM currency_rate_bindings b JOIN currency_rates r ON r.code=b.code AND r.source=b.source
 WHERE ((b.base_commodity_id=? AND b.quote_commodity_id=?) OR (b.base_commodity_id=? AND b.quote_commodity_id=?))
 AND r.rate_date=(SELECT MAX(p.rate_date) FROM currency_rates p WHERE p.code=r.code AND p.source=r.source AND p.rate_date<=?)
 AND r.rate_date>=?
 ORDER BY r.code,r.source`, fromCurrency, toCurrency, toCurrency, fromCurrency, date.Format("2006-01-02"), date.AddDate(0, 0, -14).Format("2006-01-02"))
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var rate transactionRate
		var rateDate time.Time
		var base int64
		if err := rows.Scan(&rate.Code, &rate.Source, &rate.Rate, &rateDate, &base); err != nil {
			return out, err
		}
		value, ok := new(big.Rat).SetString(rate.Rate)
		if !ok || value.Sign() <= 0 {
			continue
		}
		rate.Date = rateDate.Format("2006-01-02")
		rate.Inverse = base != fromCurrency
		out.Rates = append(out.Rates, rate)
	}
	return out, rows.Err()
}
