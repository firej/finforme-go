package handlers

import (
	"fmt"
	"math"
	"math/big"
	"strings"
)

type capitalTrendPoint struct {
	Date, End, URL      string
	Available, Selected bool
	Y                   float64
}

type capitalTrend struct {
	Points                []capitalTrendPoint
	Line, Area            string
	FirstLabel, LastLabel string
	HasValues             bool
}

func buildCapitalTrend(values []*big.Rat, rows []capitalHistoryRow) capitalTrend {
	var chart capitalTrend
	var low, high *big.Rat
	for _, value := range values {
		if value == nil {
			continue
		}
		if low == nil || value.Cmp(low) < 0 {
			low = value
		}
		if high == nil || value.Cmp(high) > 0 {
			high = value
		}
	}
	if low == nil {
		return chart
	}
	chart.HasValues = true
	chart.FirstLabel = rows[0].ShortLabel + " " + rows[0].Year
	last := rows[len(rows)-1]
	chart.LastLabel = last.ShortLabel + " " + last.Year
	span := new(big.Rat).Sub(high, low)
	var line, area strings.Builder
	var firstX, lastX float64
	connected := false
	closeArea := func() {
		fmt.Fprintf(&area, " L %.3f 100 L %.3f 100 Z", lastX, firstX)
	}
	for i, value := range values {
		row := rows[i]
		point := capitalTrendPoint{Date: row.Date, End: row.End, URL: row.URL, Available: value != nil, Selected: row.Selected}
		if value == nil {
			if connected {
				closeArea()
			}
			connected = false
		} else {
			point.Y = 50
			if span.Sign() > 0 {
				relative := new(big.Rat).Quo(new(big.Rat).Sub(high, value), span)
				fraction, _ := relative.Float64()
				point.Y = 10 + 80*fraction
			}
			x := 100 * (float64(i) + .5) / float64(len(values))
			command := " L"
			if !connected {
				command = " M"
				firstX = x
			}
			fmt.Fprintf(&line, "%s %.3f %.3f", command, x, point.Y)
			fmt.Fprintf(&area, "%s %.3f %.3f", command, x, point.Y)
			lastX = x
			connected = true
		}
		chart.Points = append(chart.Points, point)
	}
	if connected {
		closeArea()
	}
	chart.Line, chart.Area = line.String(), area.String()
	return chart
}

type expenseSegment struct {
	Name, URL             string
	Amount, Start, Length float64
	Share                 string
}

type expenseChart struct {
	Segments  []expenseSegment
	Total     float64
	Label     string
	HasRefund bool
}

// Categories are sorted by descending amount. Keep the legend short while the
// full report below still lists every category, including net refunds.
func buildExpenseChart(categories []monthCategory) expenseChart {
	const namedCategories = 5
	chart := expenseChart{}
	var other float64
	for _, category := range categories {
		if category.Amount < 0 {
			chart.HasRefund = true
		}
		if category.Amount <= 0 {
			continue
		}
		chart.Total += category.Amount
		if len(chart.Segments) < namedCategories {
			chart.Segments = append(chart.Segments, expenseSegment{Name: category.Name, URL: category.URL, Amount: category.Amount})
		} else {
			other += category.Amount
		}
	}
	if other > 0 {
		chart.Segments = append(chart.Segments, expenseSegment{Name: "Остальные", Amount: other})
	}
	chart.Label = formatMoneyShort(chart.Total)
	if chart.Total < 999.5 {
		chart.Label = formatMoney(chart.Total)
	}
	var start float64
	for i := range chart.Segments {
		segment := &chart.Segments[i]
		share := segment.Amount / chart.Total * 100
		gap := 0.0
		if len(chart.Segments) > 1 {
			gap = math.Min(.8, share/4)
		}
		segment.Start = -(start + gap/2)
		segment.Length = share - gap
		if share < .05 {
			segment.Share = "< 0,1%"
		} else {
			segment.Share = strings.Replace(fmt.Sprintf("%.1f%%", share), ".", ",", 1)
		}
		start += share
	}
	return chart
}
