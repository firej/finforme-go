package handlers

import (
	"fmt"
	"math"
	"sort"
)

const (
	cashFlowBandHeight = 104
	cashFlowLabelGap   = 42
	cashFlowPadding    = 23
	cashFlowCategories = 4
)

type cashFlowNode struct {
	Name, URL, Kind, Amount string
	Cents                   int64
	Color                   int
	LabelY, Top, Height     float64
	Ribbon                  string
}

type cashFlowChart struct {
	Sources, Uses []cashFlowNode
	Height        int
	CenterTop     float64
	CenterHeight  int
	Balanced      bool
}

func buildCashFlowChart(incomes, expenses []monthCategory) cashFlowChart {
	var sources, uses []cashFlowNode
	for _, income := range incomes {
		cents := int64(math.Round(income.Amount * 100))
		switch {
		case cents > 0:
			sources = append(sources, cashFlowNode{Name: income.Name, URL: income.URL, Kind: "income", Cents: cents})
		case cents < 0:
			uses = append(uses, cashFlowNode{Name: "Возврат дохода: " + income.Name, URL: income.URL, Kind: "refund", Cents: -cents})
		}
	}
	color := 0
	for _, expense := range expenses {
		cents := int64(math.Round(expense.Amount * 100))
		switch {
		case cents > 0:
			uses = append(uses, cashFlowNode{Name: expense.Name, URL: expense.URL, Kind: "expense", Cents: cents, Color: color % 5})
			color++
		case cents < 0:
			sources = append(sources, cashFlowNode{Name: "Возврат: " + expense.Name, URL: expense.URL, Kind: "refund", Cents: -cents})
		}
	}
	if len(sources) == 0 && len(uses) == 0 {
		return cashFlowChart{}
	}

	sources, incoming := groupCashFlowNodes(sources, "Прочие поступления", "income")
	uses, outgoing := groupCashFlowNodes(uses, "Прочие выбытия", "other")
	if incoming > outgoing {
		uses = append(uses, cashFlowNode{Name: "Осталось", Kind: "surplus", Cents: incoming - outgoing})
	} else if outgoing > incoming {
		sources = append(sources, cashFlowNode{Name: "Дефицит", Kind: "deficit", Cents: outgoing - incoming})
	}

	height := 2*cashFlowPadding + cashFlowBandHeight + (max(len(sources), len(uses))-1)*cashFlowLabelGap
	centerTop := float64(height-cashFlowBandHeight) / 2
	total := max(incoming, outgoing)
	layoutCashFlowNodes(sources, total, centerTop)
	layoutCashFlowNodes(uses, total, centerTop)
	return cashFlowChart{Sources: sources, Uses: uses, Height: height, CenterTop: centerTop, CenterHeight: cashFlowBandHeight, Balanced: incoming == outgoing}
}

func groupCashFlowNodes(nodes []cashFlowNode, otherName, otherKind string) ([]cashFlowNode, int64) {
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].Cents > nodes[j].Cents })
	var total int64
	for _, node := range nodes {
		total += node.Cents
	}
	if len(nodes) <= cashFlowCategories+1 {
		return nodes, total
	}
	var other int64
	for _, node := range nodes[cashFlowCategories:] {
		other += node.Cents
	}
	return append(nodes[:cashFlowCategories], cashFlowNode{Name: otherName, Kind: otherKind, Cents: other}), total
}

func layoutCashFlowNodes(nodes []cashFlowNode, total int64, centerTop float64) {
	outerY := centerTop - float64((len(nodes)-1)*cashFlowLabelGap)/2
	centerY := centerTop
	for i := range nodes {
		node := &nodes[i]
		node.Amount = formatMoney(float64(node.Cents) / 100)
		node.Top = outerY
		node.Height = cashFlowBandHeight * float64(node.Cents) / float64(total)
		node.LabelY = outerY + node.Height/2
		outerBottom, centerBottom := outerY+node.Height, centerY+node.Height
		node.Ribbon = fmt.Sprintf("M3 %.3f C25 %.3f 27 %.3f 49 %.3f V%.3f C27 %.3f 25 %.3f 3 %.3f Z",
			outerY, outerY, centerY, centerY, centerBottom, centerBottom, outerBottom, outerBottom)
		outerY = outerBottom + cashFlowLabelGap
		centerY = centerBottom
	}
}
