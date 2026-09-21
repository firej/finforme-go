package handlers

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"net/url"
	"sort"
	"strings"
	"time"
)

type capitalHistoryRow struct {
	Month, Label, ShortLabel, Year, URL, Date                string
	Start, End, Change, BalanceChange, FXChange, ShortChange string
	Status, StartRates, EndRates, Sign                       string
	Known, BreakdownKnown, Current, Selected                 bool
	Height                                                   float64
	change                                                   *big.Rat
}
type dashboardHistory struct {
	Currency, From, To, Start, End, Change string
	Known, HasData                         bool
	Rows                                   []capitalHistoryRow
}
type capitalSnapshot struct {
	Date     time.Time
	Holdings []capitalHolding
}

func historyDates(now time.Time) []time.Time {
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	first := month.AddDate(0, -11, 0)
	dates := []time.Time{first.AddDate(0, 0, -1)}
	for i := 0; i < 12; i++ {
		dates = append(dates, first.AddDate(0, i+1, -1))
	}
	dates[12] = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return dates
}

// One aggregation over the ledger supplies the opening balance and twelve
// monthly changes. Do not rescan the entire transaction history for each month.
func (h *Handler) capitalSnapshots(ctx context.Context, userID int64, dates []time.Time) ([]capitalSnapshot, bool, error) {
	first := dates[0].AddDate(0, 0, 1).Format("2006-01-02")
	rows, err := h.db.QueryContext(ctx, `SELECT
 CASE WHEN t.post_date<? THEN '' ELSE SUBSTR(CAST(t.post_date AS CHAR),1,7) END AS month_key,
 c.mnemonic,
 SUM(CASE WHEN a.account_type<>'LIABILITY' THEN s.value_num ELSE 0 END),
 SUM(CASE WHEN a.account_type='LIABILITY' THEN -s.value_num ELSE 0 END)
 FROM splits s JOIN accounts a ON a.id=s.account_id AND a.user_id=s.user_id
 JOIN transactions t ON t.id=s.tx_id AND t.user_id=s.user_id
 JOIN commodities c ON c.id=a.commodity_id
 WHERE s.user_id=? AND a.account_type IN ('ASSET','BANK','CASH','LIABILITY') AND t.post_date<?
 GROUP BY month_key,c.mnemonic ORDER BY month_key,c.mnemonic`, first, userID, dates[len(dates)-1].AddDate(0, 0, 1).Format("2006-01-02"))
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	periods := map[string][]capitalHolding{}
	hasData := false
	for rows.Next() {
		var period string
		var holding capitalHolding
		if err := rows.Scan(&period, &holding.Currency, &holding.Assets, &holding.Debt); err != nil {
			return nil, false, err
		}
		periods[period] = append(periods[period], holding)
		hasData = true
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	balances := map[string]capitalHolding{}
	snapshots := make([]capitalSnapshot, 0, len(dates))
	add := func(a, b int64) (int64, error) {
		if (b > 0 && a > math.MaxInt64-b) || (b < 0 && a < math.MinInt64-b) {
			return 0, fmt.Errorf("capital history balance overflow")
		}
		return a + b, nil
	}
	for i, date := range dates {
		period := ""
		if i > 0 {
			period = date.Format("2006-01")
		}
		for _, change := range periods[period] {
			holding := balances[change.Currency]
			holding.Currency = change.Currency
			holding.Assets, err = add(holding.Assets, change.Assets)
			if err != nil {
				return nil, false, err
			}
			holding.Debt, err = add(holding.Debt, change.Debt)
			if err != nil {
				return nil, false, err
			}
			balances[change.Currency] = holding
		}
		snapshot := capitalSnapshot{Date: date}
		for _, holding := range balances {
			snapshot.Holdings = append(snapshot.Holdings, holding)
		}
		sort.Slice(snapshot.Holdings, func(i, j int) bool { return currencyLess(snapshot.Holdings[i].Currency, snapshot.Holdings[j].Currency) })
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, hasData, nil
}

func (h *Handler) capitalQuoteHistory(ctx context.Context, dates []time.Time) ([]capitalQuote, error) {
	rows, err := h.db.QueryContext(ctx, `SELECT base.mnemonic,quote.mnemonic,r.code,r.source,r.rate,r.rate_date
 FROM currency_rate_bindings b JOIN commodities base ON base.id=b.base_commodity_id
 JOIN commodities quote ON quote.id=b.quote_commodity_id
 JOIN currency_rates r ON r.code=b.code AND r.source=b.source
 WHERE r.rate_date>=? AND r.rate_date<=? ORDER BY r.rate_date,r.code,r.source`, dates[0].AddDate(0, 0, -14).Format("2006-01-02"), dates[len(dates)-1].Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	quotes := []capitalQuote{}
	for rows.Next() {
		var q capitalQuote
		var date time.Time
		if err := rows.Scan(&q.Base, &q.Quote, &q.Code, &q.Source, &q.Rate, &date); err != nil {
			return nil, err
		}
		q.Date = date.Format("2006-01-02")
		quotes = append(quotes, q)
	}
	return quotes, rows.Err()
}

// Keep each source fixed across all months. Otherwise the arrival of a Blue
// quote after an official quote could masquerade as capital growth.
func pinHistorySources(history []capitalQuote, end time.Time, query url.Values) url.Values {
	pinned := url.Values{}
	for key, values := range query {
		pinned[key] = append([]string(nil), values...)
	}
	preferred := map[string]capitalQuote{}
	for _, quote := range history {
		if quote.Date > end.Format("2006-01-02") {
			continue
		}
		old, found := preferred[quote.pair()]
		if !found || quotePriority(quote) < quotePriority(old) ||
			(quotePriority(quote) == quotePriority(old) && (quote.Date > old.Date || (quote.Date == old.Date && quote.key() < old.key()))) {
			preferred[quote.pair()] = quote
		}
	}
	for pair, quote := range preferred {
		if pinned.Get("rate_"+pair) == "" {
			pinned.Set("rate_"+pair, quote.key())
		}
	}
	return pinned
}

func historyQuotesAt(history []capitalQuote, date time.Time, query url.Values) []capitalQuote {
	latest := map[string]capitalQuote{}
	end, start := date.Format("2006-01-02"), date.AddDate(0, 0, -14).Format("2006-01-02")
	for _, q := range history {
		if q.Date > end || q.Date < start {
			continue
		}
		if previous, ok := latest[q.key()]; !ok || q.Date > previous.Date {
			latest[q.key()] = q
		}
	}
	result := []capitalQuote{}
	for _, q := range latest {
		// A missing/invalid quote must not revive an older point or silently use
		// another source selected on a different date.
		rate, ok := new(big.Rat).SetString(q.Rate)
		if !ok || rate.Sign() <= 0 || q.Base == q.Quote {
			continue
		}
		if selected := query.Get("rate_" + q.pair()); selected != "" && selected != q.key() {
			continue
		}
		result = append(result, q)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].key() < result[j].key() })
	return result
}
func historyValuation(holdings []capitalHolding, quotes []capitalQuote, base string, date time.Time, query url.Values) dashboardCapital {
	// Pin the unit even if the historical quote graph no longer contains it.
	copy := append([]capitalHolding(nil), holdings...)
	found := false
	for _, h := range copy {
		if h.Currency == base {
			found = true
			break
		}
	}
	if !found {
		copy = append(copy, capitalHolding{Currency: base})
	}
	return buildDashboardCapital(copy, quotes, base, date, query)
}
func signedCapitalMoney(value *big.Rat) string {
	prefix := ""
	if value.Sign() > 0 {
		prefix = "+"
	}
	return prefix + capitalMoney(value)
}
func historicalRates(capital dashboardCapital) string {
	labels := []string{}
	for _, choice := range capital.Rates {
		for _, option := range choice.Options {
			if option.Selected {
				labels = append(labels, option.Label)
			}
		}
	}
	if len(labels) == 0 {
		if len(capital.Missing) > 0 {
			return "Нет подходящих курсов."
		}
		return "Пересчёт не нужен."
	}
	return strings.Join(labels, "; ")
}
func buildCapitalHistory(snapshots []capitalSnapshot, history []capitalQuote, base, selectedMonth string, query url.Values) dashboardHistory {
	result := dashboardHistory{Currency: base, Start: "—", End: "—", Change: "—"}
	if len(snapshots) < 2 {
		return result
	}
	query = pinHistorySources(history, snapshots[len(snapshots)-1].Date, query)
	result.From = snapshots[0].Date.AddDate(0, 0, 1).Format("02.01.2006")
	result.To = snapshots[len(snapshots)-1].Date.Format("02.01.2006")
	values := make([]dashboardCapital, len(snapshots))
	quotes := make([][]capitalQuote, len(snapshots))
	for i, snapshot := range snapshots {
		quotes[i] = historyQuotesAt(history, snapshot.Date, query)
		values[i] = historyValuation(snapshot.Holdings, quotes[i], base, snapshot.Date, query)
	}
	first, last := values[0], values[len(values)-1]
	if len(first.Missing) == 0 {
		result.Start = first.Net
	}
	if len(last.Missing) == 0 {
		result.End = last.Net
	}
	result.Known = len(first.Missing) == 0 && len(last.Missing) == 0
	if result.Known {
		result.Change = signedCapitalMoney(new(big.Rat).Sub(last.netCents, first.netCents))
	}
	max := new(big.Rat)
	names := []string{"янв", "фев", "мар", "апр", "май", "июн", "июл", "авг", "сен", "окт", "ноя", "дек"}
	for i := 1; i < len(snapshots); i++ {
		opening, closing := values[i-1], values[i]
		date := snapshots[i].Date
		month := date.Format("2006-01")
		params := url.Values{}
		for key, items := range query {
			params[key] = append([]string(nil), items...)
		}
		params.Set("month", month)
		params.Set("currency", base)
		row := capitalHistoryRow{Month: month, Label: monthLabel(date), ShortLabel: names[date.Month()-1], Year: date.Format("2006"), Date: date.Format("02.01.2006"), URL: "/?" + params.Encode() + "#monthly-report", Selected: month == selectedMonth, Current: i == len(snapshots)-1, Start: "—", End: "—", Change: "—", ShortChange: "—", BalanceChange: "—", FXChange: "—", Sign: "missing"}
		if len(opening.Missing) == 0 {
			row.Start = opening.Net
		} else {
			row.Status = "Нет курса на " + snapshots[i-1].Date.Format("02.01.2006") + ": " + strings.Join(opening.Missing, ", ") + ". "
		}
		if len(closing.Missing) == 0 {
			row.End = closing.Net
		} else {
			row.Status += "Нет курса на " + row.Date + ": " + strings.Join(closing.Missing, ", ") + "."
		}
		row.Known = len(opening.Missing) == 0 && len(closing.Missing) == 0
		row.StartRates = historicalRates(opening)
		row.EndRates = historicalRates(closing)
		if row.Known {
			row.change = new(big.Rat).Sub(closing.netCents, opening.netCents)
			row.Change = signedCapitalMoney(row.change)
			row.Sign = "zero"
			switch row.change.Sign() {
			case 1:
				row.Sign = "positive"
			case -1:
				row.Sign = "negative"
			}
			approximate, _ := new(big.Rat).Quo(row.change, big.NewRat(100, 1)).Float64()
			row.ShortChange = formatMoneyShort(approximate)
			if approximate != 0 && math.Abs(approximate) < 1 {
				row.ShortChange = capitalMoney(row.change)
			}
			if row.change.Sign() > 0 {
				row.ShortChange = "+" + row.ShortChange
			}
			abs := new(big.Rat).Abs(row.change)
			if abs.Cmp(max) > 0 {
				max.Set(abs)
			}
			// Separate balance movements valued at opening rates from revaluation of
			// the closing balances. These are endpoint estimates, not trading returns.
			fixed := historyValuation(snapshots[i].Holdings, quotes[i-1], base, snapshots[i-1].Date, query)
			row.BreakdownKnown = len(fixed.Missing) == 0
			if row.BreakdownKnown {
				row.BalanceChange = signedCapitalMoney(new(big.Rat).Sub(fixed.netCents, opening.netCents))
				row.FXChange = signedCapitalMoney(new(big.Rat).Sub(closing.netCents, fixed.netCents))
			} else {
				row.Status = "Для разделения прироста не хватает курса на начало месяца: " + strings.Join(fixed.Missing, ", ") + "."
			}
		}
		result.Rows = append(result.Rows, row)
	}
	if max.Sign() > 0 {
		for i := range result.Rows {
			row := &result.Rows[i]
			if row.Known {
				ratio, _ := new(big.Rat).Quo(new(big.Rat).Abs(row.change), max).Float64()
				row.Height = ratio * 44
			}
		}
	}
	return result
}
func (h *Handler) dashboardCapitalHistory(ctx context.Context, userID int64, now time.Time, base, selectedMonth string, query url.Values) (dashboardHistory, error) {
	dates := historyDates(now)
	snapshots, hasData, err := h.capitalSnapshots(ctx, userID, dates)
	if err != nil {
		return dashboardHistory{}, err
	}
	quotes, err := h.capitalQuoteHistory(ctx, dates)
	if err != nil {
		return dashboardHistory{}, err
	}
	report := buildCapitalHistory(snapshots, quotes, base, selectedMonth, query)
	report.HasData = hasData
	return report, nil
}
