package handlers

import (
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestFinanceBackupRejectsInvalidReplacement(t *testing.T) {
	for _, name := range []string{"mode", "precision", "zero denominator", "overflow", "date", "duplicate"} {
		t.Run(name, func(t *testing.T) {
			h := financeTestHandler(t)
			if _, err := h.saveTransaction(2, validFinanceInput()); err != nil {
				t.Fatal(err)
			}
			before, err := h.exportBackup(2)
			if err != nil {
				t.Fatal(err)
			}
			input, err := h.exportBackup(2)
			if err != nil {
				t.Fatal(err)
			}
			mode := "replace"
			switch name {
			case "mode":
				mode = "replcae"
			case "precision":
				input.Transactions[0].Splits[0].ValueNum = 1
				input.Transactions[0].Splits[0].ValueDenom = 1000
			case "zero denominator":
				input.Transactions[0].Splits[0].ValueDenom = 0
			case "overflow":
				input.Transactions[0].Splits[0].ValueNum = 1 << 62
				input.Transactions[0].Splits[0].ValueDenom = 1
			case "date":
				input.Transactions[0].EnterDate = "bad"
			case "duplicate":
				input.Transactions = append(input.Transactions, input.Transactions[0])
			}
			if _, err := h.restoreBackup(2, input, mode); err == nil {
				t.Fatal("invalid replacement accepted")
			}
			after, err := h.exportBackup(2)
			if err != nil {
				t.Fatal(err)
			}
			after.ExportedAt = before.ExportedAt
			if !reflect.DeepEqual(before, after) {
				t.Fatal("existing data changed after failed replacement")
			}
		})
	}
}
func TestFinanceBackupRoundTrip(t *testing.T) {
	h := financeTestHandler(t)
	if _, err := h.saveTransaction(2, validFinanceInput()); err != nil {
		t.Fatal(err)
	}
	data, err := h.exportBackup(2)
	if err != nil {
		t.Fatal(err)
	}
	result, err := h.restoreBackup(2, data, "replace")
	if err != nil {
		t.Fatal(err)
	}
	if result.Accounts != 5 || result.Transactions != 1 || result.Splits != 2 {
		t.Fatalf("wrong result: %+v", result)
	}
	restored, err := h.exportBackup(2)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Transactions[0].Description != data.Transactions[0].Description || restored.Transactions[0].Splits[0].ValueNum != data.Transactions[0].Splits[0].ValueNum {
		t.Fatal("round trip lost values")
	}
}
func TestBackupRequestRejectsUnknownMode(t *testing.T) {
	r := httptest.NewRequest("POST", "/import?mode=replcae", strings.NewReader(`{}`))
	if _, _, err := readBackupRequest(r); err == nil {
		t.Fatal("unknown mode accepted")
	}
}
