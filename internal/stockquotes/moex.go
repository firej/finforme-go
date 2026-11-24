package stockquotes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"
)

// ISS uses positional rows with a separate columns array. Never rely on order.
type issBlock struct {
	Columns []string            `json:"columns"`
	Data    [][]json.RawMessage `json:"data"`
}

func (b issBlock) row() (map[string]json.RawMessage, error) {
	if len(b.Data) != 1 || len(b.Data[0]) != len(b.Columns) {
		return nil, fmt.Errorf("MOEX: missing or ambiguous row")
	}
	r := make(map[string]json.RawMessage, len(b.Columns))
	for i, column := range b.Columns {
		r[column] = b.Data[0][i]
	}
	return r, nil
}

func issString(row map[string]json.RawMessage, column string) string {
	var value string
	_ = json.Unmarshal(row[column], &value)
	return value
}

func (c *Client) fetchMOEX(ctx context.Context, instrument Instrument) (Quote, error) {
	q := Quote{Instrument: instrument}
	var response struct {
		Securities, Marketdata, Dataversion issBlock
	}
	params := url.Values{
		"iss.meta": {"off"}, "iss.only": {"securities,marketdata,dataversion"},
		"securities.columns":  {"SECID,BOARDID,CURRENCYID,PREVPRICE,PREVDATE"},
		"marketdata.columns":  {"SECID,BOARDID,LAST,TIME"},
		"dataversion.columns": {"trade_date"},
	}
	if err := getJSON(ctx, c.moex, c.moexURL+url.PathEscape(instrument.Symbol)+".json?"+params.Encode(), &response); err != nil {
		return q, err
	}
	security, err := response.Securities.row()
	if err != nil {
		return q, err
	}
	market, err := response.Marketdata.row()
	if err != nil {
		return q, err
	}
	currency := issString(security, "CURRENCYID")
	if instrument.Currency != "RUB" || (currency != "SUR" && currency != "RUB") ||
		issString(security, "SECID") != instrument.Symbol || issString(market, "SECID") != instrument.Symbol ||
		issString(security, "BOARDID") != "TQBR" || issString(market, "BOARDID") != "TQBR" {
		return q, fmt.Errorf("MOEX: unexpected symbol, board or currency")
	}
	last, ok := market["LAST"]
	if !ok {
		return q, fmt.Errorf("MOEX: missing LAST column")
	}
	if string(last) == "null" {
		// No trades in this session. Keep the actual previous date; do not
		// invent a new observation for today or a timestamp for the close.
		q.Date = issString(security, "PREVDATE")
		if err := json.Unmarshal(security["PREVPRICE"], &q.Price); err != nil {
			return q, err
		}
		return q, nil
	}
	if err := json.Unmarshal(last, &q.Price); err != nil {
		return q, err
	}
	version, err := response.Dataversion.row()
	if err != nil {
		return q, err
	}
	q.Date = issString(version, "trade_date")
	// SYSTIME is the response generation time, not the last trade time.
	q.Time, err = time.ParseInLocation(time.DateTime, q.Date+" "+issString(market, "TIME"), time.FixedZone("MSK", 3*60*60))
	if err != nil {
		return q, fmt.Errorf("MOEX: invalid trade date/time: %w", err)
	}
	q.Time = q.Time.UTC()
	return q, nil
}
