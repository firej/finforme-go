package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

type bcraResponse struct {
	Status  int `json:"status"`
	Results []struct {
		Fecha   string `json:"fecha"`
		Detalle []struct {
			CodigoMoneda   string  `json:"codigoMoneda"`
			Descripcion    string  `json:"descripcion"`
			TipoCotizacion float64 `json:"tipoCotizacion"`
		} `json:"detalle"`
	} `json:"results"`
}

// importBCRA импортирует курс из официального API Центрального банка Аргентины.
// tipoCotizacion — количество песо за 1 USD.
// Сохраняет USD/ARS = tipoCotizacion (сколько песо за 1 доллар).
// Возвращает (usdARS, dateStr, error) для последующего вычисления кросс-курсов.
func importBCRA(db *sql.DB) (usdARS float64, dateStr string, err error) {
	client := &http.Client{Timeout: 15 * time.Second}
	url := "https://api.bcra.gob.ar/estadisticascambiarias/v1.0/Cotizaciones/USD"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, "", fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; finforme-import/1.0)")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("BCRA returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, "", fmt.Errorf("read body failed: %w", err)
	}

	var bcra bcraResponse
	if err := json.Unmarshal(body, &bcra); err != nil {
		return 0, "", fmt.Errorf("json parse failed: %w", err)
	}

	if len(bcra.Results) == 0 || len(bcra.Results[0].Detalle) == 0 {
		return 0, "", fmt.Errorf("BCRA: empty results")
	}

	latest := bcra.Results[0]
	cotizacion := latest.Detalle[0].TipoCotizacion
	if cotizacion == 0 {
		return 0, "", fmt.Errorf("BCRA: tipoCotizacion is zero")
	}

	// USD/ARS = tipoCotizacion (сколько песо за 1 доллар, например 1392.5)
	usdARS = cotizacion

	// BCRA возвращает дату в формате "2026-04-07T00:00:00Z" или "2026-04-07"
	// Обрезаем до первых 10 символов: "2026-04-07"
	dateStr = latest.Fecha
	if len(dateStr) > 10 {
		dateStr = dateStr[:10]
	}

	_, err = db.Exec(`
		INSERT INTO currency_rates (code, name, rate, source, rate_date)
		VALUES ('USD/ARS', 'Аргентинский песо', ?, 'bcra', ?)
		ON DUPLICATE KEY UPDATE rate = VALUES(rate), name = VALUES(name), created_at = CURRENT_TIMESTAMP
	`, usdARS, dateStr)
	if err != nil {
		return 0, "", fmt.Errorf("BCRA: db insert failed: %w", err)
	}

	log.Printf("BCRA: USD/ARS = %.2f (date: %s)", usdARS, dateStr)
	return usdARS, dateStr, nil
}
