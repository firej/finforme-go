package main

import (
	"encoding/json"
	"io"
	"os"
	"testing"
)

func TestParseBCRAJSON(t *testing.T) {
	f, err := os.Open("testdata/bcra.json")
	if err != nil {
		t.Fatalf("cannot open testdata/bcra.json: %v", err)
	}
	defer f.Close()

	body, err := io.ReadAll(f)
	if err != nil {
		t.Fatal("read error:", err)
	}

	var resp bcraResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}

	t.Logf("BCRA results count: %d", len(resp.Results))

	if len(resp.Results) == 0 {
		t.Fatal("expected at least one result")
	}

	latest := resp.Results[0]
	t.Logf("Latest fecha: %s", latest.Fecha)

	if len(latest.Detalle) == 0 {
		t.Fatal("expected detalle to be non-empty")
	}

	cotizacion := latest.Detalle[0].TipoCotizacion
	t.Logf("tipoCotizacion: %f", cotizacion)

	if cotizacion <= 0 {
		t.Errorf("tipoCotizacion must be positive, got %f", cotizacion)
	}

	// USD/ARS = tipoCotizacion (сколько песо за 1 доллар, например 1392.5)
	usdARS := cotizacion
	t.Logf("USD/ARS = %.2f (1 USD = %.2f ARS)", usdARS, usdARS)

	if usdARS <= 0 {
		t.Errorf("USD/ARS must be positive, got %f", usdARS)
	}

	// Проверяем обрезку даты (BCRA возвращает "2026-04-07T00:00:00Z")
	dateStr := latest.Fecha
	if len(dateStr) > 10 {
		dateStr = dateStr[:10]
	}
	t.Logf("date (trimmed): %s", dateStr)
	if len(dateStr) != 10 {
		t.Errorf("expected date length 10, got %d: %q", len(dateStr), dateStr)
	}
}
