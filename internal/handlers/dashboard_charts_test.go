package handlers

import (
	"math/big"
	"strings"
	"testing"
)

func TestCapitalTrendLeavesGapBetweenKnownValuations(t *testing.T) {
	values := []*big.Rat{big.NewRat(-500, 1), nil, big.NewRat(200, 1), big.NewRat(500, 1)}
	chart := buildCapitalTrend(values, make([]capitalHistoryRow, len(values)))
	if !chart.HasValues || chart.Points[1].Available || !chart.Points[2].Available {
		t.Fatalf("unexpected points: %+v", chart.Points)
	}
	if strings.Count(chart.Line, "M") != 2 || strings.Count(chart.Line, "L") != 1 || strings.Count(chart.Area, "Z") != 2 {
		t.Fatalf("chart bridges missing valuation: %q, %q", chart.Line, chart.Area)
	}
	if chart.Points[0].Y <= chart.Points[2].Y || chart.Points[2].Y <= chart.Points[3].Y {
		t.Fatalf("positions should rise with the value: %+v", chart.Points)
	}
}

func TestExpenseChartGroupsSmallCategoriesAndExcludesRefunds(t *testing.T) {
	chart := buildExpenseChart([]monthCategory{
		{Name: "Жильё", Amount: 50, URL: "/finance/account/1"},
		{Amount: 15}, {Amount: 10}, {Amount: 10}, {Amount: 10},
		{Amount: 3}, {Amount: 2}, {Amount: 0, Previous: 100}, {Amount: -25},
	})
	if chart.Total != 100 || !chart.HasRefund || len(chart.Segments) != 6 {
		t.Fatalf("unexpected chart: %+v", chart)
	}
	first, other := chart.Segments[0], chart.Segments[5]
	if first.Share != "50,0%" || first.URL != "/finance/account/1" || other.Name != "Остальные" || other.Amount != 5 || other.Share != "5,0%" {
		t.Fatalf("unexpected segments: %+v", chart.Segments)
	}
}
