package handlers

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestCurrencyChartsKeepSourceIdentity(t *testing.T) {
	charts := []CurrencyChartData{
		{Code: "USD/ARS", Source: "bcra", Points: []CurrencyHistoryPoint{{Date: "2023-06-01", Rate: 240}}},
		{Code: "USD/ARS", Source: "bluedollar_sell", Points: []CurrencyHistoryPoint{{Date: "2023-06-01", Rate: 490}}},
	}
	data, err := json.Marshal(charts)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []struct {
		Code   string `json:"code"`
		Source string `json:"source"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 2 || decoded[0].Source != "bcra" || decoded[1].Source != "bluedollar_sell" {
		t.Fatalf("source identity lost: %s", data)
	}
	// Both initial charts and period reloads must target one (pair, source)
	// canvas, never every canvas sharing the currency pair prefix.
	body, err := os.ReadFile("../../templates/currency.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{`id="chart-{{.Code}}-{{.Source}}"`, `document.getElementById('chart-' + item.code + '-' + item.source)`} {
		if !strings.Contains(string(body), required) {
			t.Fatalf("source-specific chart mapping missing: %s", required)
		}
	}
}
