package handlers

import (
	"reflect"
	"strings"
	"testing"
)

func TestFinanceGnuCashDuplicateAndChangedBook(t *testing.T) {
	for _, kind := range []string{"repeat", "changed", "transaction overlap"} {
		t.Run(kind, func(t *testing.T) {
			h := financeTestHandler(t)
			if err := h.importFromGnuCashXML(2, importFixture()); err != nil {
				t.Fatal(err)
			}
			before, err := h.exportBackup(2)
			if err != nil {
				t.Fatal(err)
			}
			data := importFixture()
			if kind == "changed" {
				data.Transactions[0].Description = "Updated"
				data.Transactions[0].GUID = "new transaction"
			}
			if kind == "transaction overlap" {
				for i := range data.Accounts {
					data.Accounts[i].GUID += "new"
					if data.Accounts[i].ParentGUID != "" {
						data.Accounts[i].ParentGUID += "new"
					}
				}
				for i := range data.Transactions[0].Splits {
					data.Transactions[0].Splits[i].AccountGUID += "new"
				}
			}
			if err := h.importFromGnuCashXML(2, data); err == nil || !strings.Contains(err.Error(), "ранее импортированные") {
				t.Fatalf("expected duplicate explanation: %v", err)
			}
			after, err := h.exportBackup(2)
			if err != nil {
				t.Fatal(err)
			}
			after.ExportedAt = before.ExportedAt
			if !reflect.DeepEqual(before, after) {
				t.Fatal("rejected import changed data")
			}
		})
	}
}
func TestFinanceGnuCashConcurrentDuplicate(t *testing.T) {
	h := financeTestHandler(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { <-start; results <- h.importFromGnuCashXML(2, importFixture()) }()
	}
	close(start)
	success := 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			success++
		} else if !strings.Contains(err.Error(), "ранее импортированные") {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("successful imports: %d", success)
	}
	var count int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM transactions WHERE user_id=2`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("transactions: %d", count)
	}
}
func TestFinanceGnuCashHistoryIsAtomicAndPerUser(t *testing.T) {
	h := financeTestHandler(t)
	bad := importFixture()
	bad.Accounts[0].ParentGUID = "missing"
	if err := h.importFromGnuCashXML(2, bad); err == nil {
		t.Fatal("bad hierarchy accepted")
	}
	var count int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM gnucash_import_ids`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed import left identity history")
	}
	for _, user := range []int64{1, 2} {
		if err := h.importFromGnuCashXML(user, importFixture()); err != nil {
			t.Fatal(err)
		}
	}
}
func TestFinanceGnuCashHistorySurvivesBackup(t *testing.T) {
	h := financeTestHandler(t)
	if err := h.importFromGnuCashXML(2, importFixture()); err != nil {
		t.Fatal(err)
	}
	backup, err := h.exportBackup(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(backup.GnuCashImportIDs) != 4 {
		t.Fatalf("missing history: %d", len(backup.GnuCashImportIDs))
	}
	// Restoring into another user exercises transfer with newly assigned account IDs.
	if _, err := h.restoreBackup(1, backup, "replace"); err != nil {
		t.Fatal(err)
	}
	if err := h.importFromGnuCashXML(1, importFixture()); err == nil {
		t.Fatal("restore lost duplicate protection")
	}
	// Removing local transactions does not silently authorize reimporting the book.
	if _, err := h.db.Exec(`DELETE FROM transactions WHERE user_id=2`); err != nil {
		t.Fatal(err)
	}
	if err := h.importFromGnuCashXML(2, importFixture()); err == nil {
		t.Fatal("deletion lost duplicate protection")
	}
}
