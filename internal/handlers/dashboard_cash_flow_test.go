package handlers

import "testing"

func TestCashFlowKeepsReversalsAndBalancesDeficit(t *testing.T) {
	chart := buildCashFlowChart(
		[]monthCategory{{Name: "Зарплата", Amount: 100}, {Name: "Премия", Amount: -20}},
		[]monthCategory{{Name: "Жильё", Amount: 150}, {Name: "Покупки", Amount: -10}},
	)
	if len(chart.Sources) != 3 || chart.Sources[1].Name != "Возврат: Покупки" || chart.Sources[1].Cents != 1000 || chart.Sources[2].Kind != "deficit" || chart.Sources[2].Cents != 6000 {
		t.Fatalf("unexpected sources: %+v", chart.Sources)
	}
	if len(chart.Uses) != 2 || chart.Uses[1].Name != "Возврат дохода: Премия" || chart.Uses[1].Cents != 2000 {
		t.Fatalf("unexpected uses: %+v", chart.Uses)
	}
}

func TestCashFlowGroupsSmallCategoriesAndUsesCurrentMonthOnly(t *testing.T) {
	chart := buildCashFlowChart([]monthCategory{{Amount: 121}}, []monthCategory{
		{Amount: 50}, {Amount: 25}, {Amount: 20}, {Amount: 15}, {Amount: 6}, {Amount: 5}, {Previous: 1000},
	})
	if len(chart.Uses) != 5 || !chart.Balanced || chart.Uses[4].Name != "Прочие выбытия" || chart.Uses[4].Cents != 1100 {
		t.Fatalf("unexpected grouping or totals: %+v", chart)
	}
	if empty := buildCashFlowChart(nil, []monthCategory{{Previous: 10}}); empty.Height != 0 {
		t.Fatalf("previous month triggered a chart: %+v", empty)
	}
}
