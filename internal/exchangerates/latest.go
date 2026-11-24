package exchangerates

// CurrencyRate is a published quote, identified by both pair and source.
type CurrencyRate struct {
	Code     string  `json:"code"`
	Name     string  `json:"name"`
	Rate     float64 `json:"rate"`
	Source   string  `json:"source"`
	RateDate string  `json:"rate_date"`
	QuotedAt string  `json:"quoted_at,omitempty"` // Source timestamp, RFC3339; absent for date-only quotes.
}

// LatestRates is the public GET /api/currency/rates response.
type LatestRates struct {
	Rates []CurrencyRate `json:"rates"`
}
