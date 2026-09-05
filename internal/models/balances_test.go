package models

import (
	"reflect"
	"testing"
)

func TestGetBalancesKeepsCurrenciesAndOwnBalance(t *testing.T) {
	parent := &Account{Currency: "RUB", Balance: 10, Childs: []*Account{
		{Currency: "RUB", Balance: 100},
		{Currency: "USD", Childs: []*Account{{Currency: "USD", Balance: 100, Hidden: 1}, {Currency: "USD", Balance: -20}}},
	}}
	want := []CurrencyBalance{{Currency: "RUB", Amount: 110}, {Currency: "USD", Amount: 80}}
	for i := 0; i < 2; i++ {
		if got := parent.GetBalances(); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v", got)
		}
	}
	if parent.Balance != 10 || parent.GetBalance() != 10 {
		t.Fatal("aggregation overwrote own balance")
	}
}
