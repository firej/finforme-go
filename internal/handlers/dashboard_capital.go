package handlers

import (
	"context"
	"fmt"
	"math/big"
	"net/url"
	"sort"
	"strings"
	"time"
)

type capitalHolding struct {
	Currency     string
	Assets, Debt int64 // minor units, never floating point
}
type capitalQuote struct {
	Base, Quote, Code, Source, Rate, Date string
}

func (q capitalQuote) key() string { return q.Code + "@" + q.Source }
func (q capitalQuote) pair() string {
	if q.Base < q.Quote {
		return q.Base + "_" + q.Quote
	}
	return q.Quote + "_" + q.Base
}
func (q capitalQuote) label() string {
	source := q.Source
	switch source {
	case "cbr":
		source = "ЦБ РФ"
	case "bluedollar_sell":
		source = "Blue · продажа"
	case "cross_blue_sell":
		source = "Blue · кросс-курс"
	}
	return fmt.Sprintf("%s · %s · %s · %s", q.Code, source, q.Rate, q.Date)
}

type capitalRateOption struct {
	Value, Label string
	Selected     bool
}
type capitalRateChoice struct {
	Name, Pair string
	Options    []capitalRateOption
}
type capitalRow struct {
	Currency, Assets, Debt, Net, Converted, Route string
	Included                                      bool
}
type dashboardCapital struct {
	netCents                          *big.Rat
	Currency, Date, Assets, Debt, Net string
	Currencies                        []string
	Rows                              []capitalRow
	Rates                             []capitalRateChoice
	Warnings                          []string
	Missing                           []string
}

func (h *Handler) capitalHoldings(ctx context.Context, userID int64, date time.Time) ([]capitalHolding, error) {
	// Sum postings once, without rolling up container accounts; include hidden accounts.
	// Future-dated operations do not belong to today's capital.
	rows, err := h.db.QueryContext(ctx, `SELECT c.mnemonic,
 COALESCE(SUM(CASE WHEN a.account_type<>'LIABILITY' AND t.id IS NOT NULL THEN s.value_num ELSE 0 END),0),
 COALESCE(SUM(CASE WHEN a.account_type='LIABILITY' AND t.id IS NOT NULL THEN -s.value_num ELSE 0 END),0)
 FROM accounts a JOIN commodities c ON c.id=a.commodity_id
 LEFT JOIN splits s ON s.account_id=a.id AND s.user_id=a.user_id
 LEFT JOIN transactions t ON t.id=s.tx_id AND t.user_id=s.user_id AND t.post_date<?
 WHERE a.user_id=? AND a.account_type IN ('ASSET','BANK','CASH','LIABILITY')
 GROUP BY c.mnemonic`, date.AddDate(0, 0, 1).Format("2006-01-02"), userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	holdings := []capitalHolding{}
	for rows.Next() {
		var row capitalHolding
		if err := rows.Scan(&row.Currency, &row.Assets, &row.Debt); err != nil {
			return nil, err
		}
		holdings = append(holdings, row)
	}
	sort.Slice(holdings, func(i, j int) bool { return currencyLess(holdings[i].Currency, holdings[j].Currency) })
	return holdings, rows.Err()
}
func (h *Handler) capitalQuotes(ctx context.Context, date time.Time) ([]capitalQuote, error) {
	// Only explicitly bound series are eligible, at their latest non-future date.
	rows, err := h.db.QueryContext(ctx, `SELECT base.mnemonic,quote.mnemonic,r.code,r.source,r.rate,r.rate_date
 FROM currency_rate_bindings b
 JOIN commodities base ON base.id=b.base_commodity_id JOIN commodities quote ON quote.id=b.quote_commodity_id
 JOIN currency_rates r ON r.code=b.code AND r.source=b.source
 WHERE r.rate_date=(SELECT MAX(p.rate_date) FROM currency_rates p WHERE p.code=r.code AND p.source=r.source AND p.rate_date<=?)
 AND r.rate_date>=? ORDER BY r.code,r.source`, date.Format("2006-01-02"), date.AddDate(0, 0, -14).Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	quotes := []capitalQuote{}
	for rows.Next() {
		var q capitalQuote
		var day time.Time
		if err := rows.Scan(&q.Base, &q.Quote, &q.Code, &q.Source, &q.Rate, &day); err != nil {
			return nil, err
		}
		rate, ok := new(big.Rat).SetString(q.Rate)
		if !ok || rate.Sign() <= 0 || q.Base == q.Quote {
			continue
		}
		q.Date = day.Format("2006-01-02")
		quotes = append(quotes, q)
	}
	return quotes, rows.Err()
}

// Round only at the display boundary; cross and inverse rates stay exact.
func capitalMoney(cents *big.Rat) string {
	text := new(big.Rat).Quo(cents, big.NewRat(100, 1)).FloatString(2)
	negative := strings.HasPrefix(text, "-")
	text = strings.TrimPrefix(text, "-")
	parts := strings.Split(text, ".")
	whole := parts[0]
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + "\u00a0" + whole[i:]
	}
	if negative && text != "0.00" {
		whole = "−" + whole
	}
	return whole + "," + parts[1]
}
func roundCapitalCents(value *big.Rat) *big.Rat {
	rounded, _ := new(big.Rat).SetString(value.FloatString(0))
	return rounded
}

func quotePriority(q capitalQuote) int {
	if (q.Base == "ARS" || q.Quote == "ARS") && (q.Source == "bluedollar_sell" || q.Source == "cross_blue_sell") {
		return 0
	}
	if q.Source == "cbr" {
		return 1
	}
	return 2
}

type capitalEdge struct {
	To    string
	Rate  *big.Rat
	Quote capitalQuote
}

func capitalPath(graph map[string][]capitalEdge, from, to string) ([]capitalEdge, bool) {
	if from == to {
		return nil, true
	}
	type step struct {
		Currency string
		Path     []capitalEdge
	}
	queue := []step{{Currency: from}}
	seen := map[string]bool{from: true}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, edge := range graph[current.Currency] {
			if seen[edge.To] {
				continue
			}
			seen[edge.To] = true
			path := append(append([]capitalEdge(nil), current.Path...), edge)
			if edge.To == to {
				return path, true
			}
			queue = append(queue, step{edge.To, path})
		}
	}
	return nil, false
}
func buildDashboardCapital(holdings []capitalHolding, quotes []capitalQuote, base string, date time.Time, query url.Values) dashboardCapital {
	result := dashboardCapital{Date: date.Format("02.01.2006")}
	available := map[string]bool{}
	for _, h := range holdings {
		available[h.Currency] = true
	}
	groups := map[string][]capitalQuote{}
	for _, q := range quotes {
		available[q.Base] = true
		available[q.Quote] = true
		groups[q.pair()] = append(groups[q.pair()], q)
	}
	for c := range available {
		result.Currencies = append(result.Currencies, c)
	}
	sort.Slice(result.Currencies, func(i, j int) bool { return currencyLess(result.Currencies[i], result.Currencies[j]) })
	if !available[base] {
		if len(result.Currencies) > 0 {
			base = result.Currencies[0]
		} else {
			base = "RUB"
		}
	}
	result.Currency = base
	graph := map[string][]capitalEdge{}
	choices := map[string]capitalRateChoice{}
	for pair, options := range groups {
		sort.Slice(options, func(i, j int) bool {
			if quotePriority(options[i]) != quotePriority(options[j]) {
				return quotePriority(options[i]) < quotePriority(options[j])
			}
			if options[i].Date != options[j].Date {
				return options[i].Date > options[j].Date
			}
			return options[i].key() < options[j].key()
		})
		selected := options[0]
		name := "rate_" + pair
		requested := query.Get(name)
		found := requested == ""
		for _, q := range options {
			if requested == q.key() {
				selected = q
				found = true
			}
		}
		if !found {
			result.Warnings = append(result.Warnings, "Выбранный источник для "+strings.ReplaceAll(pair, "_", " / ")+" недоступен. Используется "+selected.label()+".")
		}
		choice := capitalRateChoice{Name: name, Pair: strings.ReplaceAll(pair, "_", " / ")}
		for _, q := range options {
			choice.Options = append(choice.Options, capitalRateOption{q.key(), q.label(), q.key() == selected.key()})
		}
		choices[pair] = choice
		rate, _ := new(big.Rat).SetString(selected.Rate)
		graph[selected.Base] = append(graph[selected.Base], capitalEdge{selected.Quote, rate, selected})
		graph[selected.Quote] = append(graph[selected.Quote], capitalEdge{selected.Base, new(big.Rat).Inv(rate), selected})
	}
	for c := range graph {
		sort.Slice(graph[c], func(i, j int) bool { return currencyLess(graph[c][i].To, graph[c][j].To) })
	}
	assets, debt := new(big.Rat), new(big.Rat)
	used := map[string]bool{}
	for _, h := range holdings {
		a, d := big.NewRat(h.Assets, 1), big.NewRat(h.Debt, 1)
		n := new(big.Rat).Sub(a, d)
		row := capitalRow{Currency: h.Currency, Assets: capitalMoney(a), Debt: capitalMoney(d), Net: capitalMoney(n)}
		path, found := capitalPath(graph, h.Currency, base)
		row.Included = found || (h.Assets == 0 && h.Debt == 0)
		if row.Included {
			factor := big.NewRat(1, 1)
			route := []string{h.Currency}
			for _, edge := range path {
				factor.Mul(factor, edge.Rate)
				route = append(route, edge.To)
				used[edge.Quote.pair()] = true
			}
			row.Route = strings.Join(route, " → ")
			// Round each currency's assets and debt after the entire conversion path.
			// The displayed breakdown then adds up to the headline exactly.
			convertedAssets := roundCapitalCents(new(big.Rat).Mul(a, factor))
			convertedDebt := roundCapitalCents(new(big.Rat).Mul(d, factor))
			row.Converted = capitalMoney(new(big.Rat).Sub(convertedAssets, convertedDebt))
			assets.Add(assets, convertedAssets)
			debt.Add(debt, convertedDebt)
		} else {
			result.Missing = append(result.Missing, h.Currency)
		}
		result.Rows = append(result.Rows, row)
	}
	sort.Strings(result.Warnings)
	for pair := range used {
		result.Rates = append(result.Rates, choices[pair])
	}
	sort.Slice(result.Rates, func(i, j int) bool { return result.Rates[i].Name < result.Rates[j].Name })
	result.Assets = capitalMoney(assets)
	result.Debt = capitalMoney(debt)
	result.netCents = new(big.Rat).Sub(assets, debt)
	result.Net = capitalMoney(result.netCents)
	return result
}
