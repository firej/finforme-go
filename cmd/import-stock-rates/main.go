// import-stock-rates stores NVDA, NBIS and YDEX independently of currency imports.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/evbogdanov/finforme/internal/config"
	"github.com/evbogdanov/finforme/internal/exchangerates"
	"github.com/evbogdanov/finforme/internal/stockquotes"
	_ "github.com/go-sql-driver/mysql"
)

func main() {
	preview := flag.Bool("preview", false, "Получить котировки и вывести JSON без подключения к базе")
	flag.Parse()
	if err := run(*preview); err != nil {
		log.Fatal(err)
	}
}

func run(preview bool) error {
	client, err := stockquotes.NewClient()
	if err != nil {
		return err
	}
	var db *sql.DB
	if !preview {
		db, err = sql.Open("mysql", config.Load().DatabaseDSN)
		if err != nil {
			return err
		}
		defer db.Close()
	}
	var failures []error
	out := exchangerates.LatestRates{Rates: []exchangerates.CurrencyRate{}}
	for _, instrument := range stockquotes.Instruments {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		q, err := client.Fetch(ctx, instrument)
		if err == nil && !preview {
			err = saveQuote(ctx, db, q)
		}
		cancel()
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", instrument.Symbol, err))
			continue
		}
		row := exchangerates.CurrencyRate{Code: q.Code(), Name: q.Name, Rate: q.Price, Source: q.Source, RateDate: q.Date}
		if !q.Time.IsZero() {
			row.QuotedAt = q.Time.UTC().Format(time.RFC3339)
		}
		out.Rates = append(out.Rates, row)
		if !preview {
			log.Printf("%s: %s = %.6f (date: %s, quoted_at: %s)", q.Source, q.Code(), q.Price, q.Date, row.QuotedAt)
		}
	}
	if preview {
		if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
			return err
		}
	}
	return errors.Join(failures...)
}

func saveQuote(ctx context.Context, db *sql.DB, q stockquotes.Quote) error {
	var timestamp any
	if !q.Time.IsZero() {
		timestamp = q.Time.Unix()
	}
	// A delayed response (or previous-close fallback) must not overwrite a
	// more recent observation already stored for the same trading date.
	_, err := db.ExecContext(ctx, `
		INSERT INTO currency_rates (code, name, rate, source, rate_date, quoted_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			rate = IF(COALESCE(VALUES(quoted_at), 0) >= COALESCE(quoted_at, 0), VALUES(rate), rate),
			name = VALUES(name),
			created_at = IF(COALESCE(VALUES(quoted_at), 0) >= COALESCE(quoted_at, 0), CURRENT_TIMESTAMP, created_at),
			quoted_at = IF(COALESCE(VALUES(quoted_at), 0) >= COALESCE(quoted_at, 0), VALUES(quoted_at), quoted_at)
	`, q.Code(), q.Name, q.Price, q.Source, q.Date, timestamp)
	return err
}
