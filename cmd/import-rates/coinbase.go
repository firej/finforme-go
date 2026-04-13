package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// importBitcoin импортирует цену биткойна из API Coinbase.
func importBitcoin(db *sql.DB) error {
	client := &http.Client{Timeout: 15 * time.Second}
	url := "https://api.coinbase.com/v2/prices/BTC-USD/spot"

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; finforme-import/1.0)")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Coinbase returned status %d", resp.StatusCode)
	}

	var result struct {
		Data struct {
			Base     string `json:"base"`
			Currency string `json:"currency"`
			Amount   string `json:"amount"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("json parse failed: %w", err)
	}

	var price float64
	if _, err := fmt.Sscanf(result.Data.Amount, "%f", &price); err != nil {
		return fmt.Errorf("parse price failed: %w", err)
	}

	now := time.Now().Format("2006-01-02")

	_, err = db.Exec(`
		INSERT INTO currency_rates (code, name, rate, source, rate_date)
		VALUES ('BTC/USD', 'Bitcoin', ?, 'coinbase', ?)
		ON DUPLICATE KEY UPDATE rate = VALUES(rate), name = VALUES(name), created_at = CURRENT_TIMESTAMP
	`, price, now)
	if err != nil {
		return fmt.Errorf("db insert failed: %w", err)
	}

	log.Printf("Coinbase: BTC/USD = %.2f (date: %s)", price, now)
	return nil
}
