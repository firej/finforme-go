package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"
)

func TestParseCoinbaseJSON(t *testing.T) {
	f, err := os.Open("testdata/coinbase.json")
	if err != nil {
		t.Fatalf("cannot open testdata/coinbase.json: %v", err)
	}
	defer f.Close()

	body, err := io.ReadAll(f)
	if err != nil {
		t.Fatal("read error:", err)
	}

	var resp struct {
		Data struct {
			Base     string `json:"base"`
			Currency string `json:"currency"`
			Amount   string `json:"amount"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}

	t.Logf("Coinbase response: base=%s, currency=%s, amount=%s",
		resp.Data.Base, resp.Data.Currency, resp.Data.Amount)

	if resp.Data.Base != "BTC" {
		t.Errorf("expected base to be BTC, got %s", resp.Data.Base)
	}

	if resp.Data.Currency != "USD" {
		t.Errorf("expected currency to be USD, got %s", resp.Data.Currency)
	}

	if resp.Data.Amount == "" {
		t.Error("expected amount to be non-empty")
	}

	// Проверяем, что amount можно распарсить как число
	var price float64
	if _, err := fmt.Sscanf(resp.Data.Amount, "%f", &price); err != nil {
		t.Errorf("failed to parse amount as float: %v", err)
	}

	if price <= 0 {
		t.Errorf("price must be positive, got %f", price)
	}

	t.Logf("BTC price: %.2f USD", price)
}
