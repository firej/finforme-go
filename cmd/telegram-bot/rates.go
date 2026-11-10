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

func (c *ratesClient) latest(ctx context.Context) (map[string]float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "finforme-telegram-bot/1.0")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rates API request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rates API returned HTTP %d", resp.StatusCode)
	}
	const maxBody = 1 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("read rates API response: %w", err)
	}
	if len(body) > maxBody {
		return nil, fmt.Errorf("rates API response exceeds 1 MiB")
	}
	var data exchangerates.LatestRates
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("decode rates API response: %w", err)
	}
	if data.Rates == nil {
		return nil, fmt.Errorf("rates API response has no rates array")
	}
	sources := map[string]string{"BTC/USD": "coinbase", "USD/RUB": "cbr", "EUR/RUB": "cbr"}
	result := make(map[string]float64)
	for _, rate := range data.Rates {
		if source, ok := sources[rate.Code]; !ok || rate.Source != source {
			continue
		}
		if rate.Rate <= 0 || math.IsNaN(rate.Rate) || math.IsInf(rate.Rate, 0) {
			return nil, fmt.Errorf("invalid rate for %s/%s", rate.Code, rate.Source)
		}
		if _, err := time.Parse(time.DateOnly, rate.RateDate); err != nil {
			return nil, fmt.Errorf("invalid quote date for %s/%s", rate.Code, rate.Source)
		}
		if _, exists := result[rate.Code]; exists {
			return nil, fmt.Errorf("duplicate quote for %s/%s", rate.Code, rate.Source)
		}
		result[rate.Code] = rate.Rate
	}
	return result, nil
}
