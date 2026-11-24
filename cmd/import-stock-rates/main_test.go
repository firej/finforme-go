package main

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/evbogdanov/finforme/internal/stockquotes"
)

// Optional integration test against MariaDB. Uses only a connection-local table.
func TestSaveQuoteMariaDB(t *testing.T) {
	dsn := os.Getenv("FINFORME_STOCK_TEST_DSN")
	if dsn == "" {
		t.Skip("set FINFORME_STOCK_TEST_DSN to test MariaDB upserts")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TEMPORARY TABLE currency_rates (
		code VARCHAR(20), name VARCHAR(255), rate DECIMAL(18,6), source VARCHAR(50),
		rate_date DATE, quoted_at BIGINT NULL, created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY(code, source, rate_date))`)
	if err != nil {
		t.Fatal(err)
	}
	q := stockquotes.Quote{Instrument: stockquotes.Instruments[0], Price: 150, Date: "2025-02-14", Time: time.Date(2025, 2, 14, 16, 0, 0, 0, time.UTC)}
	for _, step := range []struct {
		price    float64
		at       time.Time
		want     float64
		wantTime time.Time
	}{
		{150, q.Time, 150, q.Time},
		{151, q.Time.Add(time.Minute), 151, q.Time.Add(time.Minute)},
		{140, q.Time, 151, q.Time.Add(time.Minute)},
		{130, time.Time{}, 151, q.Time.Add(time.Minute)},
	} {
		update := q
		update.Price, update.Time = step.price, step.at
		if err := saveQuote(context.Background(), db, update); err != nil {
			t.Fatal(err)
		}
		var price float64
		var timestamp int64
		if err := db.QueryRow(`SELECT rate,quoted_at FROM currency_rates`).Scan(&price, &timestamp); err != nil {
			t.Fatal(err)
		}
		if price != step.want || timestamp != step.wantTime.Unix() {
			t.Fatalf("stale update accepted: price=%v timestamp=%v", price, timestamp)
		}
	}
	q.Instrument = stockquotes.Instruments[2]
	q.Price, q.Time = 3282, time.Time{}
	if err := saveQuote(context.Background(), db, q); err != nil {
		t.Fatal(err)
	}
	var timestamp sql.NullInt64
	if err := db.QueryRow(`SELECT quoted_at FROM currency_rates WHERE code='YDEX/RUB'`).Scan(&timestamp); err != nil || timestamp.Valid {
		t.Fatalf("previous close timestamp: %+v %v", timestamp, err)
	}
}
