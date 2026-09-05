package handlers

import (
	"strings"
	"testing"
	"time"

	"github.com/evbogdanov/finforme/internal/gnucash"
)

func importFixture() *gnucash.ParsedData {
	date := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	return &gnucash.ParsedData{
		Commodities:  []gnucash.ParsedCommodity{{GUID: "rub", Space: "CURRENCY", Mnemonic: "RUB", Fraction: 100}, {GUID: "chf", Space: "ISO4217", Mnemonic: "CHF", Fullname: "Swiss Franc", Fraction: 100, Cusip: "756"}},
		Accounts:     []gnucash.ParsedAccount{{GUID: "a", Name: "Imported RUB", AccountType: "BANK", CommodityRef: "rub", ParentGUID: "root"}, {GUID: "b", Name: "Imported CHF", AccountType: "BANK", CommodityRef: "chf", ParentGUID: "root"}, {GUID: "root", Name: "Root", AccountType: "ROOT"}},
		Transactions: []gnucash.ParsedTransaction{{GUID: "t", CurrencyRef: "rub", Num: "42", PostDate: date, EnterDate: date, Splits: []gnucash.ParsedSplit{{GUID: "s1", AccountGUID: "a", ValueNum: -10000, ValueDenom: 100, QuantityNum: -10000, QuantityDenom: 100}, {GUID: "s2", AccountGUID: "b", ValueNum: 10000, ValueDenom: 100, QuantityNum: 1, QuantityDenom: 1}}}},
	}
}
func TestFinanceGnuCashCurrenciesAndEmptyDescription(t *testing.T) {
	h := financeTestHandler(t)
	if err := h.importFromGnuCashXML(2, importFixture()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.getAccounts(2); err != nil {
		t.Fatalf("imported tree cannot be read: %v", err)
	}
	var sum int64
	var description, num string
	if err := h.db.QueryRow(`SELECT s.value_num,t.description,t.num FROM splits s JOIN accounts a ON a.id=s.account_id JOIN transactions t ON t.id=s.tx_id WHERE a.name='Imported CHF'`).Scan(&sum, &description, &num); err != nil {
		t.Fatal(err)
	}
	if sum != 100 || description != "" || num != "42" {
		t.Fatalf("incorrect import: %d %q %q", sum, description, num)
	}
	var namespace, name, cusip string
	var fraction int
	if err := h.db.QueryRow(`SELECT namespace,fullname,cusip,fraction FROM commodities WHERE mnemonic='CHF'`).Scan(&namespace, &name, &cusip, &fraction); err != nil {
		t.Fatal(err)
	}
	if namespace != "CURRENCY" || name != "Swiss Franc" || cusip != "756" || fraction != 100 {
		t.Fatal("currency metadata lost")
	}
}
func TestFinanceGnuCashRollback(t *testing.T) {
	for name, change := range map[string]func(*gnucash.ParsedData){
		"missing parent":            func(d *gnucash.ParsedData) { d.Accounts[0].ParentGUID = "missing" },
		"cycle":                     func(d *gnucash.ParsedData) { d.Accounts[2].ParentGUID = "a" },
		"missing account":           func(d *gnucash.ParsedData) { d.Transactions[0].Splits[1].AccountGUID = "missing" },
		"duplicate account":         func(d *gnucash.ParsedData) { d.Accounts = append(d.Accounts, d.Accounts[0]) },
		"duplicate split":           func(d *gnucash.ParsedData) { d.Transactions[0].Splits[1].GUID = "s1" },
		"missing currency":          func(d *gnucash.ParsedData) { d.Transactions[0].CurrencyRef = "missing" },
		"unbalanced":                func(d *gnucash.ParsedData) { d.Transactions[0].Splits[1].ValueNum++ },
		"one sided":                 func(d *gnucash.ParsedData) { d.Transactions[0].Splits = d.Transactions[0].Splits[:1] },
		"zero denominator":          func(d *gnucash.ParsedData) { d.Transactions[0].Splits[1].QuantityDenom = 0 },
		"precision":                 func(d *gnucash.ParsedData) { d.Transactions[0].Splits[1].QuantityDenom = 1000 },
		"date":                      func(d *gnucash.ParsedData) { d.Transactions[0].PostDate = time.Time{} },
		"same currency discrepancy": func(d *gnucash.ParsedData) { d.Transactions[0].Splits[0].QuantityNum = -50 },
	} {
		t.Run(name, func(t *testing.T) {
			h := financeTestHandler(t)
			d := importFixture()
			change(d)
			var before int
			h.db.QueryRow(`SELECT COUNT(*) FROM commodities`).Scan(&before)
			if err := h.importFromGnuCashXML(2, d); err == nil {
				t.Fatal("invalid import accepted")
			}
			for query, want := range map[string]int{`SELECT COUNT(*) FROM accounts`: 6, `SELECT COUNT(*) FROM transactions`: 0, `SELECT COUNT(*) FROM splits`: 0, `SELECT COUNT(*) FROM commodities`: before} {
				var got int
				if err := h.db.QueryRow(query).Scan(&got); err != nil {
					t.Fatal(err)
				}
				if got != want {
					t.Fatalf("partial import: %s = %d want %d", query, got, want)
				}
			}
		})
	}
}
func TestFinanceGnuCashDeepHierarchy(t *testing.T) {
	h := financeTestHandler(t)
	d := importFixture()
	parent := "root"
	for i := 0; i < 20; i++ {
		id := strings.Repeat("x", i+1)
		d.Accounts = append([]gnucash.ParsedAccount{{GUID: id, Name: id, AccountType: "ASSET", CommodityRef: "rub", ParentGUID: parent}}, d.Accounts...)
		parent = id
	}
	d.Accounts[20].ParentGUID = parent
	if err := h.importFromGnuCashXML(2, d); err != nil {
		t.Fatal(err)
	}
}
