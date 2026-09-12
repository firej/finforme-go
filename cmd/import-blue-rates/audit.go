package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"time"

	"github.com/evbogdanov/finforme/internal/config"
)

func audit() error {
	db, err := sql.Open("mysql", config.Load().DatabaseDSN)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var database string
	if err = db.QueryRowContext(ctx, `SELECT DATABASE()`).Scan(&database); err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, `SELECT code,source,YEAR(rate_date),COUNT(*),DATE_FORMAT(MIN(rate_date),'%Y-%m-%d'),DATE_FORMAT(MAX(rate_date),'%Y-%m-%d') FROM currency_rates WHERE code IN ('USD/ARS','RUB/ARS','USD/RUB') GROUP BY code,source,YEAR(rate_date) ORDER BY code,source,YEAR(rate_date)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type coverage struct {
		Code   string
		Source string
		Year   int
		Count  int
		First  string
		Last   string
	}
	result := []coverage{}
	for rows.Next() {
		var r coverage
		if err = rows.Scan(&r.Code, &r.Source, &r.Year, &r.Count, &r.First, &r.Last); err != nil {
			return err
		}
		result = append(result, r)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	rows.Close()
	from := "2023-06-01"
	to := time.Now().UTC().Format(time.DateOnly)
	dates, err := db.QueryContext(ctx, `SELECT code,DATE_FORMAT(rate_date,'%Y-%m-%d') FROM currency_rates WHERE code IN ('USD/ARS','RUB/ARS') AND rate_date BETWEEN ? AND ?`, from, to)
	if err != nil {
		return err
	}
	defer dates.Close()
	seen := map[string]map[string]bool{"USD/ARS": {}, "RUB/ARS": {}}
	for dates.Next() {
		var code, date string
		if err = dates.Scan(&code, &date); err != nil {
			return err
		}
		seen[code][date] = true
	}
	if err = dates.Err(); err != nil {
		return err
	}
	type gap struct {
		Code     string
		Missing  int
		Weekend  int
		Weekday  int
		Examples []string
	}
	gaps := []gap{}
	start, _ := time.Parse(time.DateOnly, from)
	end, _ := time.Parse(time.DateOnly, to)
	for _, code := range []string{"USD/ARS", "RUB/ARS"} {
		g := gap{Code: code}
		for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
			date := d.Format(time.DateOnly)
			if seen[code][date] {
				continue
			}
			g.Missing++
			if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
				g.Weekend++
			} else {
				g.Weekday++
			}
			if len(g.Examples) < 10 {
				g.Examples = append(g.Examples, date)
			}
		}
		gaps = append(gaps, g)
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		Database string
		Coverage []coverage
		From     string
		To       string
		Gaps     []gap
	}{database, result, from, to, gaps})
}
