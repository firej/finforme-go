package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"time"

	"github.com/evbogdanov/finforme/internal/exchangerates"
)

type ratesClient struct {
	endpoint string
	client   *http.Client
}

func newRatesClient(endpoint string) (*ratesClient, error) {
	if endpoint == "" {
		endpoint = "https://finfor.me/api/currency/rates"
	}
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Fragment != "" {
		return nil, fmt.Errorf("CURRENCY_RATES_URL должен быть абсолютным HTTP(S) URL без логина и пароля")
	}
	return &ratesClient{endpoint: endpoint, client: &http.Client{Timeout: 5 * time.Second}}, nil
}

var quoteSources = map[string]string{
	"BTC/USD": "coinbase", "USD/RUB": "cbr", "EUR/RUB": "cbr",
	"NVDA/USD": "yahoo", "NBIS/USD": "yahoo", "YDEX/RUB": "moex",
}

func (c *ratesClient) latest(ctx context.Context) (map[string]exchangerates.CurrencyRate, error) {
	var data exchangerates.LatestRates
	if err := c.getJSON(ctx, c.endpoint, &data); err != nil {
		return nil, err
	}
	if data.Rates == nil {
		return nil, fmt.Errorf("rates API response has no rates array")
	}
	result := make(map[string]exchangerates.CurrencyRate)
	for _, rate := range data.Rates {
		if source, ok := quoteSources[rate.Code]; !ok || rate.Source != source {
			continue
		}
		if rate.Rate <= 0 || math.IsNaN(rate.Rate) || math.IsInf(rate.Rate, 0) {
			return nil, fmt.Errorf("invalid rate for %s/%s", rate.Code, rate.Source)
		}
		if _, err := time.Parse(time.DateOnly, rate.RateDate); err != nil {
			return nil, fmt.Errorf("invalid quote date for %s/%s", rate.Code, rate.Source)
		}
		if rate.QuotedAt != "" {
			if _, err := time.Parse(time.RFC3339, rate.QuotedAt); err != nil {
				return nil, fmt.Errorf("invalid quote timestamp for %s/%s", rate.Code, rate.Source)
			}
		}
		if _, exists := result[rate.Code]; exists {
			return nil, fmt.Errorf("duplicate quote for %s/%s", rate.Code, rate.Source)
		}
		result[rate.Code] = rate
	}
	return result, nil
}

func (c *ratesClient) getJSON(ctx context.Context, endpoint string, data any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "finforme-telegram-bot/1.0")
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("rates API request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("rates API returned HTTP %d", resp.StatusCode)
	}
	const maxBody = 1 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return fmt.Errorf("read rates API response: %w", err)
	}
	if len(body) > maxBody {
		return fmt.Errorf("rates API response exceeds 1 MiB")
	}
	if err := json.Unmarshal(body, data); err != nil {
		return fmt.Errorf("decode rates API response: %w", err)
	}
	return nil
}
