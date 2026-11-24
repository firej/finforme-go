package stockquotes

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"time"
)

// History returns daily closes in the preceding days (calendar days in Moscow).
// Today's unfinished candle in either Moscow or the exchange timezone is omitted.
// Candle timestamps describe intervals, not trades, so Quote.Time stays zero.
func (c *Client) History(ctx context.Context, instrument Instrument, days int, now time.Time) ([]Quote, error) {
	if days < 1 || days > 365 {
		return nil, fmt.Errorf("history days must be between 1 and 365")
	}
	msk := now.In(time.FixedZone("MSK", 3*60*60))
	end := time.Date(msk.Year(), msk.Month(), msk.Day(), 0, 0, 0, 0, time.UTC)
	start := end.AddDate(0, 0, -days)
	var quotes []Quote
	var err error
	switch instrument.Source {
	case "yahoo":
		quotes, err = c.yahooHistory(ctx, instrument, start, end, now)
	case "moex":
		quotes, err = c.moexHistory(ctx, instrument, start, end)
	default:
		return nil, fmt.Errorf("unknown source %q", instrument.Source)
	}
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	for _, q := range quotes {
		date, err := time.Parse(time.DateOnly, q.Date)
		if err != nil || date.Before(start) || !date.Before(end) || seen[q.Date] ||
			q.Price <= 0 || math.IsNaN(q.Price) || math.IsInf(q.Price, 0) {
			return nil, fmt.Errorf("invalid or duplicate historical quote for %s on %s", q.Code(), q.Date)
		}
		seen[q.Date] = true
	}
	if len(quotes) == 0 {
		return nil, fmt.Errorf("no completed daily candles for %s", instrument.Symbol)
	}
	sort.Slice(quotes, func(i, j int) bool { return quotes[i].Date < quotes[j].Date })
	return quotes, nil
}

func (c *Client) yahooHistory(ctx context.Context, instrument Instrument, start, end, now time.Time) ([]Quote, error) {
	var response struct {
		Chart struct {
			Error  json.RawMessage
			Result []struct {
				Meta       struct{ Symbol, Currency, ExchangeTimezoneName string }
				Timestamp  []int64
				Indicators struct{ Quote []struct{ Close []*float64 } }
			}
		}
	}
	params := url.Values{"interval": {"1d"}, "period1": {strconv.FormatInt(start.Unix(), 10)},
		"period2": {strconv.FormatInt(end.Unix(), 10)}}
	if err := getJSON(ctx, c.yahoo, c.yahooURL+url.PathEscape(instrument.Symbol)+"?"+params.Encode(), &response); err != nil {
		return nil, err
	}
	if (len(response.Chart.Error) > 0 && string(response.Chart.Error) != "null") || len(response.Chart.Result) != 1 {
		return nil, fmt.Errorf("Yahoo: missing or ambiguous history")
	}
	r := response.Chart.Result[0]
	if r.Meta.Symbol != instrument.Symbol || r.Meta.Currency != instrument.Currency || len(r.Indicators.Quote) != 1 {
		return nil, fmt.Errorf("Yahoo: unexpected history symbol, currency or price series")
	}
	zone, err := time.LoadLocation(r.Meta.ExchangeTimezoneName)
	if err != nil || r.Meta.ExchangeTimezoneName == "" {
		return nil, fmt.Errorf("Yahoo: invalid exchange timezone")
	}
	closes := r.Indicators.Quote[0].Close // Raw close, not dividend-adjusted adjclose.
	if len(closes) != len(r.Timestamp) {
		return nil, fmt.Errorf("Yahoo: mismatched candle dates and closes")
	}
	var quotes []Quote
	for i, stamp := range r.Timestamp {
		if stamp <= 0 {
			return nil, fmt.Errorf("Yahoo: invalid candle timestamp")
		}
		date := time.Unix(stamp, 0).In(zone).Format(time.DateOnly)
		if date >= end.Format(time.DateOnly) || date >= now.In(zone).Format(time.DateOnly) {
			continue
		}
		if closes[i] == nil {
			continue
		} // A missing trading observation is not a zero price.
		quotes = append(quotes, Quote{Instrument: instrument, Date: date, Price: *closes[i]})
	}
	return quotes, nil
}

func (c *Client) moexHistory(ctx context.Context, instrument Instrument, start, end time.Time) ([]Quote, error) {
	if instrument.Symbol != "YDEX" || instrument.Currency != "RUB" {
		return nil, fmt.Errorf("unsupported MOEX history instrument")
	}
	params := url.Values{"interval": {"24"}, "from": {start.Format(time.DateOnly)},
		"till": {end.AddDate(0, 0, -1).Format(time.DateOnly)}, "iss.meta": {"off"},
		"iss.only": {"candles"}, "candles.columns": {"begin,close"}}
	var quotes []Quote
	for offset := 0; offset <= 366; {
		params.Set("start", strconv.Itoa(offset))
		var response struct{ Candles issBlock }
		if err := getJSON(ctx, c.moex, c.moexURL+url.PathEscape(instrument.Symbol)+"/candles.json?"+params.Encode(), &response); err != nil {
			return nil, err
		}
		// An absent block is an invalid response, not the end of pagination.
		begin, closeIndex := -1, -1
		for i, column := range response.Candles.Columns {
			if column == "begin" {
				begin = i
			}
			if column == "close" {
				closeIndex = i
			}
		}
		if begin < 0 || closeIndex < 0 {
			return nil, fmt.Errorf("MOEX: missing candle columns")
		}
		if len(response.Candles.Data) == 0 {
			return quotes, nil
		}
		for _, row := range response.Candles.Data {
			if len(row) != len(response.Candles.Columns) {
				return nil, fmt.Errorf("MOEX: invalid candle row")
			}
			var timestamp string
			if err := json.Unmarshal(row[begin], &timestamp); err != nil {
				return nil, err
			}
			date, err := time.Parse(time.DateTime, timestamp)
			if err != nil {
				return nil, fmt.Errorf("MOEX: invalid candle date: %w", err)
			}
			if !date.Before(end) {
				continue
			}
			var price *float64
			if err := json.Unmarshal(row[closeIndex], &price); err != nil {
				return nil, err
			}
			if price == nil {
				continue
			}
			quotes = append(quotes, Quote{Instrument: instrument, Date: date.Format(time.DateOnly), Price: *price})
		}
		offset += len(response.Candles.Data)
	}
	return nil, fmt.Errorf("MOEX: too many daily candles or repeated pages")
}
