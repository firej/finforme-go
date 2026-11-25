package handlers

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// formatMoney is display-only: input values and API numbers remain unlocalized.
func formatAmount(value float64) string {
	if math.Abs(value) < 0.000000005 {
		value = 0
	}
	precision := 2
	for precision < 8 && math.Abs(value-math.Round(value*math.Pow10(precision))/math.Pow10(precision)) > 0.0000000001 {
		precision++
	}
	parts := strings.SplitN(fmt.Sprintf("%.*f", precision, math.Abs(value)), ".", 2)
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

// formatMoneyInput preserves the float value without scientific notation or rounding.
func formatMoneyInput(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// exactCents is used only for valuation aggregates, with no binary float step.
type exactCents string

func (c exactCents) rat() *big.Rat {
	if c == "" {
		return new(big.Rat)
	}
	r, _ := new(big.Rat).SetString(string(c))
	return r
}
func (c *exactCents) Scan(value any) error {
	var text string
	switch v := value.(type) {
	case int64:
		text = strconv.FormatInt(v, 10)
	case float64:
		text = strconv.FormatFloat(v, 'f', -1, 64)
	case []byte:
		text = string(v)
	case string:
		text = v
	default:
		return fmt.Errorf("invalid monetary aggregate %T", value)
	}
	r, ok := new(big.Rat).SetString(text)
	if !ok {
		return fmt.Errorf("invalid monetary aggregate")
	}
	*c = exactCents(r.RatString())
	return nil
}

// Reports rounded to cents retain their existing formatting contract.
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
