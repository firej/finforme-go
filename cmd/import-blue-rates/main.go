// Blue Dollar Sell backfill and scheduled refresh. Preview by default; -apply writes rates,
// never transactions. Official rates are preserved unless -replace-official
// explicitly requests a backed-up historical ARS replacement.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/evbogdanov/finforme/internal/config"
	"github.com/evbogdanov/finforme/internal/exchangerates"
	_ "github.com/go-sql-driver/mysql"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	from := flag.String("from", exchangerates.FirstDate, "First date, not before 2023-06-01")
	to := flag.String("to", time.Now().UTC().Format(time.DateOnly), "Last date, inclusive")
	recentDays := flag.Int("recent-days", 0, "Refresh the last N calendar days through -to (default: today UTC); cannot combine with -from")
	missingOnly := flag.Bool("missing-only", false, "Insert missing historical rates without changing existing quotes")
	apply := flag.Bool("apply", false, "Write blue and derived cross rates atomically")
	auditOnly := flag.Bool("audit", false, "Read database identity and rate coverage, without downloading or writing")
	htmlFile := flag.String("html", "", "Optional saved bluedollar.net/informal-rate HTML")
	quoteDates := flag.String("quotes", "", "Comma-separated dates to include as valuation quotes in JSON output")
	replace := flag.Bool("replace-official", false, "Replace ARS official rates only within the selected range before September 2025; requires -backup when applying")
	backup := flag.String("backup", "", "Exclusive JSON backup file for replacement")
	flag.Parse()
	if *auditOnly {
		return audit()
	}
	explicitFrom := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "from" {
			explicitFrom = true
		}
	})
	if *recentDays != 0 && (explicitFrom || *replace) {
		return fmt.Errorf("-recent-days cannot be combined with -from or -replace-official")
	}
	if *missingOnly && *replace {
		return fmt.Errorf("-missing-only cannot be combined with -replace-official")
	}
	start, err := importStart(*from, *to, *recentDays)
	if err != nil {
		return err
	}
	*from = start
	if *to > time.Now().UTC().Format(time.DateOnly) {
		return fmt.Errorf("future end date is not allowed")
	}
	if *replace && (*to >= "2025-09-01" || (*apply && *backup == "")) {
		return fmt.Errorf("replacement must end before 2025-09-01 and requires -backup when applying")
	}
	var body []byte
	if *htmlFile != "" {
		f, err := os.Open(*htmlFile)
		if err != nil {
			return err
		}
		defer f.Close()
		body, err = io.ReadAll(io.LimitReader(f, (8<<20)+1))
		if err != nil {
			return err
		}
	} else {
		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Get(exchangerates.BlueURL)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("Blue Dollar HTTP %d", resp.StatusCode)
		}
		body, err = io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
		if err != nil {
			return err
		}
	}
	// Keep prior quotes available when a custom start date is a weekend.
	blue, err := exchangerates.ParseBlueHTML(body, exchangerates.FirstDate, *to)
	if err != nil {
		return err
	}
	db, err := sql.Open("mysql", config.Load().DatabaseDSN)
	if err != nil {
		return fmt.Errorf("database setup failed")
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err = db.PingContext(ctx); err != nil {
		return fmt.Errorf("database unavailable")
	}
	rows, err := db.QueryContext(ctx, `SELECT DATE_FORMAT(rate_date,'%Y-%m-%d'),CAST(rate AS CHAR) FROM currency_rates WHERE code='USD/RUB' AND source='cbr' AND rate_date<=? ORDER BY rate_date`, *to)
	if err != nil {
		return fmt.Errorf("read CBR rates: %w", err)
	}
	var cbr []exchangerates.Point
	for rows.Next() {
		var p exchangerates.Point
		if err = rows.Scan(&p.Date, &p.Rate); err != nil {
			rows.Close()
			return err
		}
		cbr = append(cbr, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	quotes, err := exchangerates.Quotes(blue, cbr, *from, *to)
	if err != nil {
		return err
	}
	first := 0
	for first < len(blue) && blue[first].Date < *from {
		first++
	}
	blue = blue[first:]
	if *replace {
		return replaceOfficial(ctx, db, quotes, *from, *to, *apply, *backup)
	}
	selected := map[string]bool{}
	if *quoteDates != "" {
		for _, d := range strings.Split(*quoteDates, ",") {
			if d < *from || d > *to {
				return fmt.Errorf("quote date outside range: %s", d)
			}
			if _, err := time.Parse(time.DateOnly, d); err != nil {
				return err
			}
			selected[d] = true
		}
	}
	if *apply {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		save := func(code, name, value, source, date string) error {
			query := `INSERT INTO currency_rates(code,name,rate,source,rate_date) VALUES(?,?,?,?,?) ON DUPLICATE KEY UPDATE rate=VALUES(rate),name=VALUES(name)`
			if *missingOnly {
				query = `INSERT INTO currency_rates(code,name,rate,source,rate_date) VALUES(?,?,?,?,?) ON DUPLICATE KEY UPDATE code=VALUES(code)`
			}
			_, err := tx.ExecContext(ctx, query, code, name, value, source, date)
			return err
		}
		for _, p := range blue {
			if err = save("USD/ARS", "Blue Dollar — Sell", p.Rate, exchangerates.BlueSource, p.Date); err != nil {
				return err
			}
		}
		for _, q := range quotes {
			if err = save("RUB/ARS", "Blue Dollar Sell × ЦБ РФ", q.RUBARS, exchangerates.CrossSource, q.Date); err != nil {
				return err
			}
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	var output []exchangerates.Quote
	for _, q := range quotes {
		if selected[q.Date] {
			output = append(output, q)
		}
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		Applied    bool                  `json:"applied"`
		From       string                `json:"from"`
		To         string                `json:"to"`
		Source     string                `json:"source"`
		BlueCount  int                   `json:"blue_count"`
		CrossCount int                   `json:"cross_count"`
		Quotes     []exchangerates.Quote `json:"quotes"`
	}{*apply, *from, *to, exchangerates.BlueURL, len(blue), len(quotes), output})
}

// importStart keeps the periodic window deterministic and inclusive of both ends.
func importStart(from, to string, recentDays int) (string, error) {
	if recentDays < 0 || recentDays > 366 {
		return "", fmt.Errorf("-recent-days must be between 1 and 366 (or 0 to use -from)")
	}
	if recentDays > 0 {
		end, err := time.Parse(time.DateOnly, to)
		if err != nil {
			return "", err
		}
		from = end.AddDate(0, 0, 1-recentDays).Format(time.DateOnly)
		if from < exchangerates.FirstDate {
			from = exchangerates.FirstDate
		}
	}
	return from, exchangerates.ValidateRange(from, to)
}
