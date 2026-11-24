// Package stockquotes fetches stock prices without API keys.
package stockquotes

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"
	_ "time/tzdata"
)

// Downloaded over verified HTTPS from
// https://gu-st.ru/content/Other/doc/russian_trusted_root_ca.cer
// SHA-256 (DER): D26D2D0231B7C39F92CC738512BA54103519E4405D68B5BD703E9788CA8ECF31.
// This root is used only by the MOEX client, never installed system-wide.
//
//go:embed moex_root.pem
var moexRoot []byte

type Instrument struct {
	Symbol, Currency, Name, Source string
}

var Instruments = []Instrument{
	{"NVDA", "USD", "NVIDIA", "yahoo"},
	{"NBIS", "USD", "Nebius Group", "yahoo"},
	{"YDEX", "RUB", "Яндекс", "moex"},
}

type Quote struct {
	Instrument
	Price float64
	Date  string
	Time  time.Time // Zero when the source provides only a previous closing date.
}

func (q Quote) Code() string { return q.Symbol + "/" + q.Currency }

type Client struct {
	yahoo, moex       *http.Client
	yahooURL, moexURL string
}

func NewClient() (*Client, error) {
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(moexRoot) {
		return nil, fmt.Errorf("invalid bundled MOEX root certificate")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	return &Client{
		yahoo: &http.Client{Timeout: 15 * time.Second},
		moex: &http.Client{Timeout: 15 * time.Second, Transport: transport,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return fmt.Errorf("unexpected MOEX redirect")
			}},
		yahooURL: "https://query1.finance.yahoo.com/v8/finance/chart/",
		moexURL:  "https://iss.moex.com/iss/engines/stock/markets/shares/boards/TQBR/securities/",
	}, nil
}

func (c *Client) Fetch(ctx context.Context, instrument Instrument) (Quote, error) {
	var q Quote
	var err error
	switch instrument.Source {
	case "yahoo":
		q, err = c.fetchYahoo(ctx, instrument)
	case "moex":
		q, err = c.fetchMOEX(ctx, instrument)
	default:
		return q, fmt.Errorf("unknown source %q", instrument.Source)
	}
	if err != nil {
		return q, err
	}
	if q.Price <= 0 || math.IsNaN(q.Price) || math.IsInf(q.Price, 0) {
		return q, fmt.Errorf("invalid price for %s", q.Code())
	}
	date, err := time.Parse(time.DateOnly, q.Date)
	if err != nil || date.After(time.Now().UTC().Add(24*time.Hour)) || q.Time.After(time.Now().Add(5*time.Minute)) {
		return q, fmt.Errorf("invalid quote date/time for %s", q.Code())
	}
	return q, nil
}

func getJSON(ctx context.Context, client *http.Client, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; finforme-import/1.0)")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("quote source returned HTTP %d", resp.StatusCode)
	}
	const maxBody = 1 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return err
	}
	if len(body) > maxBody {
		return fmt.Errorf("quote response exceeds 1 MiB")
	}
	return json.Unmarshal(body, out)
}
