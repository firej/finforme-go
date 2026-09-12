package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"reflect"

	"github.com/evbogdanov/finforme/internal/exchangerates"
)

type savedRate struct {
	Code    string
	Name    string
	Rate    string
	Source  string
	Date    string
	Created string
}

func replacementTarget(r savedRate, from, to string) bool {
	return r.Date >= from && r.Date <= to && ((r.Code == "USD/ARS" && (r.Source == "bcra" || r.Source == exchangerates.BlueSource)) || (r.Code == "RUB/ARS" && (r.Source == "cross" || r.Source == exchangerates.CrossSource)))
}

func replaceOfficial(ctx context.Context, db *sql.DB, quotes []exchangerates.Quote, from, to string, apply bool, backup string) error {
	if err := exchangerates.ValidateRange(from, to); err != nil {
		return err
	}
	if to >= "2025-09-01" || len(quotes) == 0 {
		return fmt.Errorf("invalid replacement range")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	snapshot := func() ([]savedRate, error) {
		rows, err := tx.QueryContext(ctx, `SELECT code,name,CAST(rate AS CHAR),source,DATE_FORMAT(rate_date,'%Y-%m-%d'),CAST(created_at AS CHAR) FROM currency_rates ORDER BY code,source,rate_date FOR UPDATE`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		result := []savedRate{}
		for rows.Next() {
			var r savedRate
			if err = rows.Scan(&r.Code, &r.Name, &r.Rate, &r.Source, &r.Date, &r.Created); err != nil {
				return nil, err
			}
			result = append(result, r)
		}
		return result, rows.Err()
	}
	before, err := snapshot()
	if err != nil {
		return err
	}
	affected, protected := []savedRate{}, []savedRate{}
	for _, r := range before {
		if replacementTarget(r, from, to) {
			affected = append(affected, r)
		} else {
			protected = append(protected, r)
		}
	}
	if backup != "" {
		f, err := os.OpenFile(backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		err = json.NewEncoder(f).Encode(struct {
			From   string
			To     string
			Rates  []savedRate
			Quotes []exchangerates.Quote
		}{from, to, before, quotes})
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	var deleted int64
	if apply {
		if backup == "" {
			return fmt.Errorf("backup required")
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM currency_rates WHERE rate_date BETWEEN ? AND ? AND ((code='USD/ARS' AND source='bcra') OR (code='RUB/ARS' AND source='cross'))`, from, to)
		if err != nil {
			return err
		}
		deleted, err = res.RowsAffected()
		if err != nil {
			return err
		}
		for _, q := range quotes {
			for _, r := range []savedRate{{Code: "USD/ARS", Name: "Blue Dollar — Sell", Rate: q.Blue.Rate, Source: exchangerates.BlueSource, Date: q.Date}, {Code: "RUB/ARS", Name: "Blue Dollar Sell × ЦБ РФ", Rate: q.RUBARS, Source: exchangerates.CrossSource, Date: q.Date}} {
				if _, err = tx.ExecContext(ctx, `INSERT INTO currency_rates(code,name,rate,source,rate_date) VALUES(?,?,?,?,?) ON DUPLICATE KEY UPDATE rate=VALUES(rate),name=VALUES(name)`, r.Code, r.Name, r.Rate, r.Source, r.Date); err != nil {
					return err
				}
			}
		}
		after, err := snapshot()
		if err != nil {
			return err
		}
		protectedAfter := []savedRate{}
		actual := map[string]string{}
		for _, r := range after {
			if !replacementTarget(r, from, to) {
				protectedAfter = append(protectedAfter, r)
			} else {
				actual[r.Code+"|"+r.Source+"|"+r.Date] = r.Rate
			}
		}
		if !reflect.DeepEqual(protected, protectedAfter) {
			return fmt.Errorf("protected rates changed: rolling back")
		}
		if len(actual) != len(quotes)*2 {
			return fmt.Errorf("replacement count mismatch: rolling back")
		}
		for _, q := range quotes {
			if actual["USD/ARS|"+exchangerates.BlueSource+"|"+q.Date] != q.Blue.Rate || actual["RUB/ARS|"+exchangerates.CrossSource+"|"+q.Date] != q.RUBARS {
				return fmt.Errorf("replacement value mismatch: rolling back")
			}
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		Applied         bool
		From            string
		To              string
		PreviousRows    int
		DeletedOfficial int64
		BlueDays        int
		CrossDays       int
		ProtectedRows   int
		Backup          string
	}{apply, from, to, len(affected), deleted, len(quotes), len(quotes), len(protected), backup})
}
