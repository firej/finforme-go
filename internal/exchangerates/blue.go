// Package exchangerates provides historical, source-specific valuations.
package exchangerates

import (
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strings"
	"time"
)

const BlueURL = "https://bluedollar.net/informal-rate/"
const BlueSource = "bluedollar_sell"
const CrossSource = "cross_blue_sell"
const FirstDate = "2023-06-01"

type Point struct {
	Date string `json:"date"`
	Rate string `json:"rate"`
}

type Quote struct {
	Date   string `json:"date"`
	Blue   Point  `json:"blue"`
	CBR    Point  `json:"cbr"`
	RUBARS string `json:"rub_ars"` // ARS per RUB, for the existing chart convention.
}

var decimalPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]{1,6})?$`)
var chartPattern = regexp.MustCompile(`\bvar\s+chartData\s*=\s*`)

func rate(value string) (*big.Rat, error) {
	if !decimalPattern.MatchString(value) {
		return nil, fmt.Errorf("invalid decimal rate %q", value)
	}
	r, ok := new(big.Rat).SetString(value)
	if !ok || r.Sign() <= 0 || r.Cmp(big.NewRat(1000000000000, 1)) >= 0 {
		return nil, fmt.Errorf("invalid rate %q", value)
	}
	return r, nil
}

func ParseBlueHTML(body []byte, from, to string) ([]Point, error) {
	if err := ValidateRange(from, to); err != nil {
		return nil, err
	}
	if len(body) > 8<<20 {
		return nil, fmt.Errorf("Blue Dollar response too large")
	}
	locations := chartPattern.FindAllIndex(body, -1)
	if len(locations) != 1 {
		return nil, fmt.Errorf("expected one chartData series, got %d", len(locations))
	}
	var points []struct {
		Date  string `json:"date"`
		Value string `json:"value"`
	}
	// Decode only the JSON array. Never evaluate the surrounding JavaScript.
	if err := json.NewDecoder(strings.NewReader(string(body[locations[0][1]:]))).Decode(&points); err != nil {
		return nil, fmt.Errorf("chartData: %w", err)
	}
	unique := map[string]string{}
	for _, p := range points {
		if _, err := time.Parse(time.DateOnly, p.Date); err != nil {
			return nil, fmt.Errorf("invalid blue date %q", p.Date)
		}
		if p.Date < from || p.Date > to {
			continue
		}
		value, err := rate(p.Value)
		if err != nil {
			return nil, err
		}
		normal := value.FloatString(6)
		if previous, ok := unique[p.Date]; ok && previous != normal {
			return nil, fmt.Errorf("conflicting blue rates on %s", p.Date)
		}
		unique[p.Date] = normal
	}
	result := make([]Point, 0, len(unique))
	for date, value := range unique {
		result = append(result, Point{date, value})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Date < result[j].Date })
	if len(result) == 0 {
		return nil, fmt.Errorf("no blue rates in requested range")
	}
	return result, nil
}

func ValidateRange(from, to string) error {
	for _, s := range []string{from, to} {
		if _, err := time.Parse(time.DateOnly, s); err != nil {
			return err
		}
	}
	if from < FirstDate || to < from {
		return fmt.Errorf("range must start on/after %s and end on/after start", FirstDate)
	}
	return nil
}

// Quotes uses the last available quote on/before each date, independently
// for Argentina and Russia. Large gaps are errors, not silently filled months.
func Quotes(blue, cbr []Point, from, to string) ([]Quote, error) {
	if err := ValidateRange(from, to); err != nil {
		return nil, err
	}
	for _, series := range [][]Point{blue, cbr} {
		for i, p := range series {
			if _, err := time.Parse(time.DateOnly, p.Date); err != nil {
				return nil, err
			}
			if _, err := rate(p.Rate); err != nil {
				return nil, err
			}
			if i > 0 && series[i-1].Date >= p.Date {
				return nil, fmt.Errorf("rates must have unique increasing dates")
			}
		}
	}
	start, _ := time.Parse(time.DateOnly, from)
	end, _ := time.Parse(time.DateOnly, to)
	var result []Quote
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		date := d.Format(time.DateOnly)
		latest := func(points []Point) (Point, error) {
			i := sort.Search(len(points), func(i int) bool { return points[i].Date > date }) - 1
			if i < 0 {
				return Point{}, fmt.Errorf("no preceding rate for %s", date)
			}
			pd, _ := time.Parse(time.DateOnly, points[i].Date)
			if d.Sub(pd) > 14*24*time.Hour {
				return Point{}, fmt.Errorf("rate for %s is stale (%s)", date, points[i].Date)
			}
			return points[i], nil
		}
		b, err := latest(blue)
		if err != nil {
			return nil, fmt.Errorf("blue: %w", err)
		}
		c, err := latest(cbr)
		if err != nil {
			return nil, fmt.Errorf("cbr: %w", err)
		}
		br, _ := rate(b.Rate)
		cr, _ := rate(c.Rate)
		result = append(result, Quote{date, b, c, new(big.Rat).Quo(br, cr).FloatString(6)})
	}
	return result, nil
}

// RUBCents rounds only the final value; never use the rounded chart cross-rate.
func (q Quote) RUBCents(arsCents int64) (int64, error) {
	if arsCents <= 0 || arsCents > (1<<53)-1 {
		return 0, fmt.Errorf("invalid amount")
	}
	b, err := rate(q.Blue.Rate)
	if err != nil {
		return 0, err
	}
	c, err := rate(q.CBR.Rate)
	if err != nil {
		return 0, err
	}
	v := new(big.Rat).Mul(new(big.Rat).SetInt64(arsCents), c)
	v.Quo(v, b)
	n := new(big.Int).Mul(v.Num(), big.NewInt(2))
	n.Add(n, v.Denom())
	n.Quo(n, new(big.Int).Mul(v.Denom(), big.NewInt(2)))
	if !n.IsInt64() || n.Sign() <= 0 || n.Int64() > (1<<53)-1 {
		return 0, fmt.Errorf("converted amount out of range")
	}
	return n.Int64(), nil
}
