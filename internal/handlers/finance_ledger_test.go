package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestFinanceLedgerMultipleSplitsAndPages(t *testing.T) {
	h := financeTestHandler(t)
	id, err := h.saveTransaction(2, validFinanceInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.db.Exec(`UPDATE splits SET value_num=-6000 WHERE tx_id=? AND account_id=1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err = h.db.Exec(`INSERT INTO splits(user_id,tx_id,account_id,value_num,value_denom) VALUES(2,?,1,-4000,100)`, id); err != nil {
		t.Fatal(err)
	}
	rows := h.getAccountTransactions(2, 1, "asc")
	if len(rows) != 1 || rows[0]["account_balance"] != float64(-100) {
		t.Fatalf("legacy rows: %+v", rows)
	}
	page, err := h.accountTransactionPage(context.Background(), 2, 1, accountPageFilter{Sort: "asc"})
	if err != nil || page.Expense != 100 || page.Transactions[0]["account_balance"] != float64(-100) {
		t.Fatalf("page %+v: %v", page, err)
	}
	// More than one page, including same-date transactions: stable ID tie breaking.
	var focus int64
	for i := 0; i < 105; i++ {
		in := validFinanceInput()
		in.Value = 1
		in.Description = fmt.Sprintf("row %d", i)
		focus, err = h.saveTransaction(2, in)
		if err != nil {
			t.Fatal(err)
		}
	}
	page, err = h.accountTransactionPage(context.Background(), 2, 1, accountPageFilter{Sort: "asc", Offset: 50})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 106 || len(page.Transactions) != 50 || page.Expense != 205 || page.Transactions[0]["account_balance"] != float64(-150) {
		t.Fatalf("page totals/running balance: %+v", page)
	}
	page, err = h.accountTransactionPage(context.Background(), 2, 1, accountPageFilter{Sort: "asc", Search: "row 104"})
	if err != nil || page.Total != 1 || page.Transactions[0]["account_balance"] != float64(-205) {
		t.Fatalf("search lost opening balance: %+v %v", page, err)
	}
	page, err = h.accountTransactionPage(context.Background(), 2, 1, accountPageFilter{Sort: "asc", Focus: focus})
	if err != nil || page.Offset != 100 || len(page.Transactions) != 6 {
		t.Fatalf("anchor page: %+v %v", page, err)
	}
	if _, err = h.accountTransactionPage(context.Background(), 1, 1, accountPageFilter{}); err == nil {
		t.Fatal("foreign account accepted")
	}
	// The real page and JSON HTML fragment must render successfully.
	cookie := authCookie(t, h, 2)
	w := httptest.NewRecorder()
	h.APITransactionPage(w, authRequest("GET", "/api/v1/finance/transaction/page?account_id=1&offset=50", nil, cookie))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "account_balance") && !strings.Contains(w.Body.String(), "html") {
		t.Fatalf("page API: %d %s", w.Code, w.Body.String())
	}
}
func TestFinanceLedgerDuplicateDenominators(t *testing.T) {
	h := financeTestHandler(t)
	id, err := h.saveTransaction(2, validFinanceInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.db.Exec(`UPDATE transactions SET post_date='2026-09-05 00:00:00' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err = h.db.Exec(`UPDATE splits SET value_num=-100,value_denom=1 WHERE tx_id=? AND account_id=1`, id); err != nil {
		t.Fatal(err)
	}
	if h.findDuplicateTransaction(2, 1, "2026-09-05", -10000) != id {
		t.Fatal("equal amount missed")
	}
	if h.findDuplicateTransaction(2, 1, "2026-09-05", -100) != 0 {
		t.Fatal("different amount considered duplicate")
	}
}
func TestFinanceLedgerCSVRetry(t *testing.T) {
	h := financeTestHandler(t)
	cookie := authCookie(t, h, 2)
	request := csvImportSaveReq{SourceAccountID: 1, RequestID: "retry-test", Items: []csvImportSaveItem{{Date: "2026-09-05", Amount: -1, Description: "CSV", OtherAccountID: 2}}}
	call := func(req csvImportSaveReq) *httptest.ResponseRecorder {
		body, _ := json.Marshal(req)
		r := httptest.NewRequest("POST", "/", strings.NewReader(string(body)))
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		h.APIImportCSVSave(w, r)
		return w
	}
	for i := 0; i < 2; i++ {
		w := call(request)
		if w.Code != 200 {
			t.Fatalf("save %d: %s", i, w.Body.String())
		}
	}
	var count int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM transactions WHERE user_id=2`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("retry duplicated import: %d %v", count, err)
	}
	request.Items[0].Amount = -2
	if w := call(request); w.Code != 400 {
		t.Fatal("reused identity accepted different payload", w.Body.String())
	}
	request.RequestID = ""
	for i := 0; i < 2; i++ {
		if w := call(request); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM transactions WHERE user_id=2`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("content retry duplicated import: %d %v", count, err)
	}
}
func TestFinanceLedgerHistoryRestoreAndIsolation(t *testing.T) {
	h := financeTestHandler(t)
	in := validFinanceInput()
	in.Source = "ui"
	id, err := h.saveTransaction(2, in)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.setTransactionChecked(2, id, true); err != nil {
		t.Fatal(err)
	}
	comment := "new comment"
	if err = h.updateTransactionMetadataFrom(2, id, in.PostDate, "changed", "tag", &comment, "mcp"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = h.db.QueryRow(`SELECT COUNT(*) FROM transaction_checks WHERE user_id=2`).Scan(&n); err != nil || n != 0 {
		t.Fatal("edit retained check", err)
	}
	if err = h.deleteTransaction(2, id, "ui"); err != nil {
		t.Fatal(err)
	}
	history, err := h.transactionHistory(2, id, 0)
	if err != nil || len(history) != 3 {
		t.Fatal(history, err)
	}
	if history[1].Source != "mcp" || history[1].Before.Description != "Purchase" || history[1].After.Comment != comment || len(history[0].Before.Splits) != 2 {
		t.Fatalf("incomplete audit: %+v", history)
	}
	if _, err = h.restoreDeletedTransaction(1, history[0].ID, "mcp"); err == nil {
		t.Fatal("foreign restore accepted")
	}
	restored, err := h.restoreDeletedTransaction(2, history[0].ID, "mcp")
	if err != nil {
		t.Fatal(err)
	}
	retry, err := h.restoreDeletedTransaction(2, history[0].ID, "mcp")
	if err != nil || retry != restored {
		t.Fatal("restore retry not idempotent", err)
	}
	if len(splitSnapshot(t, h, restored)) != 2 {
		t.Fatal("lost restored splits")
	}
	foreign, err := h.transactionHistory(1, 0, 0)
	if err != nil || len(foreign) != 0 {
		t.Fatal("history isolation", err)
	}
	w := httptest.NewRecorder()
	h.FinanceHistory(w, authRequest("GET", "/finance/history", nil, authCookie(t, h, 2)))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "new comment") {
		t.Fatal("history render", w.Code, w.Body.String())
	}
}
func TestFinanceLedgerReconciliation(t *testing.T) {
	h := financeTestHandler(t)
	in := validFinanceInput()
	id, err := h.saveTransaction(2, in)
	if err != nil {
		t.Fatal(err)
	}
	date := in.PostDate
	out, err := h.reconcileAccount(2, 1, date, "-99", false)
	if err != nil || out.Matches || out.Difference != "-1.00000000" {
		t.Fatal(out, err)
	}
	if _, err = h.reconcileAccount(2, 1, date, "-99", true); err == nil {
		t.Fatal("mismatched balance confirmed")
	}
	if _, err = h.reconcileAccount(2, 5, date, "0", true); err == nil {
		t.Fatal("foreign reconciliation accepted")
	}
	out, err = h.reconcileAccount(2, 1, date, "-100", true)
	if err != nil || out.ID == 0 {
		t.Fatal(out, err)
	}
	latest, err := h.latestReconciliation(2, 1)
	if err != nil || latest == nil || latest.Stale {
		t.Fatal(latest, err)
	}
	in.TxID = id
	in.Value = 99
	if _, err = h.saveTransaction(2, in); err != nil {
		t.Fatal(err)
	}
	latest, err = h.latestReconciliation(2, 1)
	if err != nil || !latest.Stale {
		t.Fatal("stale reconciliation hidden", latest, err)
	}
}
func TestFinanceLedgerEightDecimalsRoundTrip(t *testing.T) {
	h := financeTestHandler(t)
	if _, err := h.db.Exec(`UPDATE commodities SET fraction=100000000 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	in := validFinanceInput()
	in.Value = 0.00000001
	in.ValueText = "0.00000001"
	id, err := h.saveTransaction(2, in)
	if err != nil {
		t.Fatal(err)
	}
	splits := splitSnapshot(t, h, id)
	if len(splits) != 2 || splits[0].ValueNum != 1 || splits[0].ValueDenom != 100000000 {
		t.Fatal(splits)
	}
	accounts, err := h.getAccountsWithBalance(2)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range accounts {
		if a.ID == 1 && a.Balance != -0.00000001 {
			t.Fatalf("balance %v", a.Balance)
		}
	}
	report, err := h.periodReport(2, in.PostDate, in.PostDate)
	if err != nil || report.Expense[0].Amount != 0.00000001 {
		t.Fatal(report, err)
	}
	capital, err := h.capitalHoldings(context.Background(), 2, in.PostDate)
	if err != nil || capital[0].Assets.rat().RatString() != "-1/1000000" {
		t.Fatal(capital, err)
	}
	page, err := h.accountTransactionPage(context.Background(), 2, 1, accountPageFilter{})
	if err != nil || page.Expense != 0.00000001 {
		t.Fatal(page, err)
	}
	out, err := h.reconcileAccount(2, 1, in.PostDate, "-0.00000001", true)
	if err != nil || !out.Matches {
		t.Fatal(out, err)
	}
	backup, err := h.exportBackup(2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.restoreBackup(2, backup, "replace"); err != nil {
		t.Fatal(err)
	}
	again, err := h.exportBackup(2)
	if err != nil || again.Transactions[0].Splits[0].ValueNum != 1 || again.Transactions[0].Splits[0].ValueDenom != 100000000 {
		t.Fatal(again, err)
	}
	// Whole-unit currencies reject fractional input, through both HTTP and MCP.
	h = financeTestHandler(t)
	if _, err = h.db.Exec(`UPDATE commodities SET fraction=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	in = validFinanceInput()
	in.Value = 1.1
	if _, err = h.saveTransaction(2, in); err == nil {
		t.Fatal("whole-unit currency accepted decimals")
	}
	if _, _, err = h.mcpSaveTransaction(2, 0, writeTransactionIn{Date: "2026-09-05", Description: "exact", AmountExact: "1", FromAccountID: 1, ToAccountID: 2}); err != nil {
		t.Fatal(err)
	}
}
func TestFinanceLedgerPeriodBoundaries(t *testing.T) {
	h := financeTestHandler(t)
	in := validFinanceInput()
	in.PostDate = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if _, err := h.saveTransaction(2, in); err != nil {
		t.Fatal(err)
	}
	in.PostDate = in.PostDate.AddDate(0, 1, 0)
	if _, err := h.saveTransaction(2, in); err != nil {
		t.Fatal(err)
	}
	filter, err := parseAccountPageFilter(url.Values{"month": {"2026-09"}})
	if err != nil {
		t.Fatal(err)
	}
	page, err := h.accountTransactionPage(context.Background(), 2, 1, filter)
	if err != nil || page.Total != 1 {
		t.Fatal(page, err)
	}
}

func TestFinanceLedgerBackupPreservesHistoryAndReconciliation(t *testing.T) {
	h := financeTestHandler(t)
	in := validFinanceInput()
	id, err := h.saveTransaction(2, in)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.setTransactionChecked(2, id, true); err != nil {
		t.Fatal(err)
	}
	if _, err = h.reconcileAccount(2, 1, in.PostDate, "-100", true); err != nil {
		t.Fatal(err)
	}
	// A deleted second transaction must remain restorable after backup transfer.
	in.Description = "deleted"
	in.PostDate = in.PostDate.AddDate(0, 0, 1)
	deleted, err := h.saveTransaction(2, in)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.deleteTransaction(2, deleted); err != nil {
		t.Fatal(err)
	}
	backup, err := h.exportBackup(2)
	if err != nil {
		t.Fatal(err)
	}
	target := financeTestHandler(t)
	if _, err = target.restoreBackup(1, backup, "replace"); err != nil {
		t.Fatal(err)
	}
	transferred, err := target.exportBackup(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(transferred.History) != 3 || len(transferred.Checked) != 1 || len(transferred.Reconciliations) != 1 || transferred.Reconciliations[0].Stale {
		t.Fatalf("backup lost ledger features: %+v", transferred)
	}
	history, err := target.transactionHistory(1, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := target.restoreDeletedTransaction(1, history[0].ID, "ui")
	if err != nil || restored == 0 {
		t.Fatal("deleted snapshot not restorable after transfer", err)
	}
	// Input IDs must never allow backup snapshots to target another user's accounts.
	snaps := splitSnapshot(t, target, restored)
	for _, s := range snaps {
		var owner int64
		if err := target.db.QueryRow(`SELECT user_id FROM accounts WHERE id=?`, s.AccountID).Scan(&owner); err != nil || owner != 1 {
			t.Fatal("foreign restored split", s, err)
		}
	}
}

func TestFinanceLedgerFXPrecisionAndOlderPosting(t *testing.T) {
	h := financeTestHandler(t)
	if _, err := h.db.Exec(`UPDATE commodities SET fraction=100000000 WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	in := validFinanceInput()
	in.DebitAccountID = 3
	in.ValueTarget = moneyPointer(0.00000001)
	in.TargetText = "0.00000001"
	id, err := h.saveTransaction(2, in)
	if err != nil {
		t.Fatal(err)
	}
	postings := splitSnapshot(t, h, id)
	if postings[0].ValueDenom != 100000000 || postings[1].ValueDenom != 100 {
		t.Fatal(postings)
	}
	if _, err := h.db.Exec(`UPDATE commodities SET fraction=100 WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.APITransactionFormGet(w, authRequest("GET", fmt.Sprintf("/api/v1/finance/transaction/form?tx_id=%d&account_id=1", id), nil, authCookie(t, h, 2)))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "/finance/transaction/metadata") || !strings.Contains(w.Body.String(), "0,00000001") {
		t.Fatal("old precision cannot be edited safely", w.Body.String())
	}
	// Exact CSV input takes precedence over a rounded legacy numeric field.
	if got, err := csvAmountUnits(90071992.5474099, 100000000, "90071992.54740991"); err != nil || got != 9007199254740991 {
		t.Fatal(got, err)
	}
}

func TestFinanceLedgerConcurrentCSVAndRestore(t *testing.T) {
	h := financeTestHandler(t)
	cookie := authCookie(t, h, 2)
	body := `{"source_account_id":1,"request_id":"parallel","items":[{"date":"2026-09-05","amount":-1,"other_account_id":2}]}`
	done := make(chan int, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			r := httptest.NewRequest("POST", "/", strings.NewReader(body))
			r.AddCookie(cookie)
			w := httptest.NewRecorder()
			h.APIImportCSVSave(w, r)
			done <- w.Code
		}()
	}
	close(start)
	for i := 0; i < 2; i++ {
		if code := <-done; code != 200 {
			t.Fatalf("parallel import status %d", code)
		}
	}
	var count int
	var id int64
	if err := h.db.QueryRow(`SELECT COUNT(*),MAX(id) FROM transactions WHERE user_id=2`).Scan(&count, &id); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if err := h.deleteTransaction(2, id); err != nil {
		t.Fatal(err)
	}
	items, err := h.transactionHistory(2, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	restored := make(chan int64, 2)
	for i := 0; i < 2; i++ {
		go func() {
			id, err := h.restoreDeletedTransaction(2, items[0].ID, "api")
			if err != nil {
				restored <- 0
			} else {
				restored <- id
			}
		}()
	}
	one, two := <-restored, <-restored
	if one == 0 || one != two {
		t.Fatal("concurrent restore duplicated", one, two)
	}
}
