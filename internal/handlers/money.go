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
