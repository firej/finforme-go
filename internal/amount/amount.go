// Package amount converts decimal amounts to exact integer ledger units.
package amount

import (
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

const MaxUnits int64 = 1<<53 - 1

var decimalPattern = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]{1,3})?$`)

func Digits(fraction int64) (int, error) {
	for n, d := int64(1), 0; d <= 8; n, d = n*10, d+1 {
		if fraction == n {
			return d, nil
		}
	}
	return 0, fmt.Errorf("точность валюты должна быть от 0 до 8 знаков")
}

// Parse never rounds. Decimal and scientific notation from JSON numbers are accepted.
func Parse(text string, fraction int64) (int64, error) {
	digits, err := Digits(fraction)
	if err != nil {
		return 0, err
	}
	text = strings.TrimSpace(text)
	if text == "" || len(text) > 64 || !decimalPattern.MatchString(text) {
		return 0, fmt.Errorf("некорректная сумма")
	}
	r, ok := new(big.Rat).SetString(text)
	if !ok {
		return 0, fmt.Errorf("некорректная сумма")
	}
	r.Mul(r, new(big.Rat).SetInt64(fraction))
	if !r.IsInt() {
		return 0, fmt.Errorf("укажите сумму с точностью до %d знаков после запятой", digits)
	}
	if !r.Num().IsInt64() {
		return 0, fmt.Errorf("сумма слишком велика")
	}
	n := r.Num().Int64()
	if n > MaxUnits || n < -MaxUnits {
		return 0, fmt.Errorf("сумма слишком велика для точности валюты")
	}
	return n, nil
}

func Float(value float64, fraction int64) (int64, error) {
	return Parse(strconv.FormatFloat(value, 'f', -1, 64), fraction)
}

// Normalize preserves exact sub-cent values and keeps the historical denominator
// 100 whenever possible, so old backups and ordinary currencies stay compatible.
func Normalize(num, denom int64) (int64, int64, error) {
	if denom <= 0 {
		return 0, 0, fmt.Errorf("знаменатель суммы должен быть положительным")
	}
	r := new(big.Rat).SetFrac(big.NewInt(num), big.NewInt(denom))
	for d := int64(100); d <= 100000000; d *= 10 {
		units := new(big.Rat).Mul(r, new(big.Rat).SetInt64(d))
		if units.IsInt() && units.Num().IsInt64() {
			n := units.Num().Int64()
			if n >= -MaxUnits && n <= MaxUnits {
				return n, d, nil
			}
		}
	}
	if _, err := Digits(denom); err == nil && num >= -MaxUnits && num <= MaxUnits {
		return num, denom, nil
	}
	return 0, 0, fmt.Errorf("сумма не представима точно с 8 знаками или слишком велика")
}

func Decimal(num, denom int64) string {
	if denom <= 0 {
		return ""
	}
	s := new(big.Rat).SetFrac(big.NewInt(num), big.NewInt(denom)).FloatString(8)
	return strings.TrimRight(strings.TrimRight(s, "0"), ".")
}
