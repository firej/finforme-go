package handlers

import (
	"fmt"
	"math"
	"strings"
)

// formatMoney is display-only: input values and API numbers remain unlocalized.
func formatMoney(value float64) string {
	if math.Abs(value) < 0.005 {
		value = 0
	}
	parts := strings.SplitN(fmt.Sprintf("%.2f", math.Abs(value)), ".", 2)
	whole := parts[0]
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + "\u00a0" + whole[i:]
	}
	if value < 0 {
		whole = "−" + whole
	}
	return whole + "," + parts[1]
}

// formatMoneyShort keeps sidebar balances compact; the exact amount is in the tooltip.
func formatMoneyShort(value float64) string {
	amount := math.Abs(value)
	sign := ""
	if value < 0 && amount >= 0.5 {
		sign = "−"
	}
	divisor, suffix := 1.0, ""
	switch {
	case amount >= 999_950_000:
		divisor, suffix = 1_000_000_000, " млрд"
	case amount >= 999_950:
		divisor, suffix = 1_000_000, " млн"
	case amount >= 999.5:
		divisor, suffix = 1_000, " тыс."
	default:
		return sign + fmt.Sprintf("%.0f", math.Round(amount))
	}
	rounded := math.Round(amount/divisor*10) / 10
	number := strings.TrimSuffix(fmt.Sprintf("%.1f", rounded), ".0")
	return sign + strings.ReplaceAll(number, ".", ",") + suffix
}
