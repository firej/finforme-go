package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/evbogdanov/finforme/internal/config"
	"github.com/evbogdanov/finforme/internal/stockquotes"
)

func runHistory(days int, preview bool) error {
	if days < 1 || days > 365 {
		return fmt.Errorf("history-days must be between 1 and 365")
	}
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
	now := time.Now()
	var failures []error
	for _, instrument := range stockquotes.Instruments {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		quotes, err := client.History(ctx, instrument, days, now)
		var inserted int64
		if err == nil && !preview {
			inserted, err = saveHistory(ctx, db, quotes)
		}
		cancel()
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", instrument.Symbol, err))
			continue
		}
		if preview {
			if err := json.NewEncoder(os.Stdout).Encode(quotes); err != nil {
				return err
			}
		}
		log.Printf("%s: daily closes %s — %s, fetched=%d inserted=%d (preview=%t)",
			instrument.Symbol, quotes[0].Date, quotes[len(quotes)-1].Date, len(quotes), inserted, preview)
	}
	return errors.Join(failures...)
}

// Fill only missing dates, atomically per instrument. Live quotes and repeated
// imports stay unchanged, including their source timestamps and import times.
func saveHistory(ctx context.Context, db *sql.DB, quotes []stockquotes.Quote) (int64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var inserted int64
	for _, q := range quotes {
		result, err := tx.ExecContext(ctx, `INSERT INTO currency_rates (code,name,rate,source,rate_date,quoted_at)
			VALUES (?,?,?,?,?,NULL) ON DUPLICATE KEY UPDATE code=VALUES(code)`, q.Code(), q.Name, q.Price, q.Source, q.Date)
		if err != nil {
			return 0, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		inserted += n
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return inserted, nil
}
