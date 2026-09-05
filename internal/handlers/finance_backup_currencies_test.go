package handlers

import (
	"reflect"
	"testing"
)

func TestFinanceBackupCurrencyMetadataTransfer(t *testing.T) {
	source := financeTestHandler(t)
	target := financeTestHandler(t)
	if err := source.importFromGnuCashXML(2, importFixture()); err != nil {
		t.Fatal(err)
	}
	if _, err := source.db.Exec(`UPDATE commodities SET fullname='Swiss franc',cusip='756',fraction=1000,quote_source='currency',quote_tz='Europe/Zurich',sign='Fr' WHERE mnemonic='CHF'`); err != nil {
		t.Fatal(err)
	}
	backup, err := source.exportBackup(2)
	if err != nil {
		t.Fatal(err)
	}
	if backup.Version != 2 {
		t.Fatal("wrong backup version")
	}
	if _, err := target.restoreBackup(2, backup, "replace"); err != nil {
		t.Fatal(err)
	}
	restored, err := target.exportBackup(2)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(backup.Commodities, restored.Commodities) {
		t.Fatalf("metadata lost: %+v versus %+v", backup.Commodities, restored.Commodities)
	}
	if len(restored.GnuCashImportIDs) != 4 {
		t.Fatal("import history lost")
	}
}
func TestFinanceBackupCurrencyValidationKeepsData(t *testing.T) {
	for _, kind := range []string{"missing", "duplicate", "precision", "namespace", "conflict"} {
		t.Run(kind, func(t *testing.T) {
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
			switch kind {
			case "missing":
				input.Commodities = nil
			case "duplicate":
				input.Commodities = append(input.Commodities, input.Commodities[0])
			case "precision":
				input.Commodities[0].Fraction = 0
			case "namespace":
				input.Commodities[0].Namespace = "STOCK"
			case "conflict":
				input.Commodities[0].Fullname = "Different currency name"
			}
			if _, err := h.restoreBackup(2, input, "replace"); err == nil {
				t.Fatal("invalid metadata accepted")
			}
			after, err := h.exportBackup(2)
			if err != nil {
				t.Fatal(err)
			}
			after.ExportedAt = before.ExportedAt
			if !reflect.DeepEqual(before, after) {
				t.Fatal("rejected restore changed data")
			}
		})
	}
}
func TestFinanceBackupLegacyCurrencyCompatibility(t *testing.T) {
	h := financeTestHandler(t)
	backup, err := h.exportBackup(2)
	if err != nil {
		t.Fatal(err)
	}
	backup.Version = 1
	backup.Commodities = nil
	backup.Accounts[0].Currency = "CAD"
	if _, err := h.restoreBackup(2, backup, "replace"); err != nil {
		t.Fatal(err)
	}
	var fraction int
	if err := h.db.QueryRow(`SELECT fraction FROM commodities WHERE mnemonic='CAD'`).Scan(&fraction); err != nil {
		t.Fatal(err)
	}
	if fraction != 100 {
		t.Fatal("legacy default changed")
	}
}
