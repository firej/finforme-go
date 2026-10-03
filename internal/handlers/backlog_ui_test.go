package handlers

import (
	"strings"
	"testing"
)

func TestTransactionAmountInputsUseFullDecimal(t *testing.T) {
	tmpl := buildTestTemplates(t)
	for _, name := range []string{"finance_transaction.html", "finance_transaction_modal_form.html"} {
		for _, amount := range []struct {
			value float64
			text  string
		}{
			{12000000, "12000000"},
			{12000000.01, "12000000.01"},
			{0.01, "0.01"},
			{1234567890.12, "1234567890.12"},
		} {
			t.Run(name+"/"+amount.text, func(t *testing.T) {
				data := baseData(testUser(), nil)
				data["AccountID"] = int64(1)
				data["CrossCurrency"] = true
				data["Credit"] = []map[string]interface{}{{"account_id": int64(1), "value": amount.value}}
				data["Debit"] = []map[string]interface{}{{"account_id": int64(2), "value": amount.value}}
				var out strings.Builder
				if err := tmpl.ExecuteTemplate(&out, name, data); err != nil {
					t.Fatal(err)
				}
				if got := strings.Count(out.String(), `value="`+amount.text+`"`); got != 2 {
					t.Fatalf("expected both amount fields to contain %s; got %d matches", amount.text, got)
				}
			})
		}
	}
}

func TestTransactionTagExactMatch(t *testing.T) {
	for _, tc := range []struct {
		tags, wanted string
		match        bool
	}{
		{"победа,еда дома", "еда", false},
		{"победа, еда ,дом", "еда", true},
		{`100%, x_y, путь\дом, еда/кафе, a&b`, `путь\дом`, true},
		{"100%, x_y", "%", false},
		{"100%, x_y", "x_y", true},
		{"еда", "", false},
	} {
		if got := hasTransactionTag(tc.tags, tc.wanted); got != tc.match {
			t.Errorf("tags %q, wanted %q: got %v", tc.tags, tc.wanted, got)
		}
	}
}

func TestTransactionTagLinkEscaping(t *testing.T) {
	tmpl := buildTestTemplates(t)
	txs := testTransactions()
	txs[0]["tags"] = []string{" еда/кафе & 100% "}
	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "finance_transactions_tbody.html", map[string]interface{}{
		"Transactions": txs, "Account": testAccount(2, "BANK"),
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `href="/finance/tag?tag=%d0%b5%d0%b4%d0%b0%2f%d0%ba%d0%b0%d1%84%d0%b5%20%26%20100%25"`) {
		t.Fatalf("tag link is not safely query-encoded: %s", out.String())
	}
}
