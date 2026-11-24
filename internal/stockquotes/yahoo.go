package stockquotes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"
)

func (c *Client) fetchYahoo(ctx context.Context, instrument Instrument) (Quote, error) {
	q := Quote{Instrument: instrument}
	var response struct {
		Chart struct {
			Error  json.RawMessage `json:"error"`
			Result []struct {
				Meta struct {
					Symbol, Currency, ExchangeTimezoneName string
					RegularMarketPrice                     float64
					RegularMarketTime                      int64
				} `json:"meta"`
			} `json:"result"`
		} `json:"chart"`
	}
	if err := getJSON(ctx, c.yahoo, c.yahooURL+url.PathEscape(instrument.Symbol)+"?interval=1d&range=5d", &response); err != nil {
		return q, err
	}
	if (len(response.Chart.Error) > 0 && string(response.Chart.Error) != "null") || len(response.Chart.Result) != 1 {
		return q, fmt.Errorf("Yahoo: missing or ambiguous quote")
	}
	m := response.Chart.Result[0].Meta
	if m.Symbol != instrument.Symbol || m.Currency != instrument.Currency || m.RegularMarketTime <= 0 {
		return q, fmt.Errorf("Yahoo: invalid symbol, currency or quote timestamp")
	}
	zone, err := time.LoadLocation(m.ExchangeTimezoneName)
	if err != nil || m.ExchangeTimezoneName == "" {
		return q, fmt.Errorf("Yahoo: invalid exchange timezone %q", m.ExchangeTimezoneName)
	}
	q.Price = m.RegularMarketPrice
	q.Time = time.Unix(m.RegularMarketTime, 0).UTC()
	q.Date = q.Time.In(zone).Format(time.DateOnly)
	return q, nil
}
