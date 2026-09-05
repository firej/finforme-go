package handlers

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/evbogdanov/finforme/internal/models"
)

func financeTestHandler(t *testing.T) *Handler {
	t.Helper()
	h := authTestHandler(t)
	if os.Getenv("FINFORME_TEST_MYSQL_DSN") == "" {
		for _, q := range []string{
			`PRAGMA foreign_keys=ON`,
			`CREATE TABLE commodities(id INTEGER PRIMARY KEY, namespace TEXT, mnemonic TEXT, fullname TEXT, cusip TEXT, fraction INTEGER, quote_source TEXT, quote_tz TEXT, sign TEXT)`,
			`INSERT INTO commodities(id,namespace,mnemonic,fullname,fraction,sign) VALUES(1,'CURRENCY','RUB','Ruble',100,'RUB'),(2,'CURRENCY','USD','Dollar',100,'USD')`,
			`CREATE TABLE accounts(id INTEGER PRIMARY KEY, user_id INTEGER REFERENCES users(id), name TEXT, account_type TEXT, commodity_id INTEGER REFERENCES commodities(id), commodity_scu INTEGER, non_std_scu INTEGER, parent_id INTEGER REFERENCES accounts(id), code TEXT, description TEXT, hidden INTEGER DEFAULT 0, placeholder INTEGER DEFAULT 0)`,
			`CREATE TABLE transactions(id INTEGER PRIMARY KEY, user_id INTEGER REFERENCES users(id), num TEXT, post_date DATETIME, enter_date DATETIME, description TEXT, tags TEXT)`,
			`CREATE TABLE splits(id INTEGER PRIMARY KEY,user_id INTEGER REFERENCES users(id),tx_id INTEGER REFERENCES transactions(id) ON DELETE CASCADE,account_id INTEGER REFERENCES accounts(id) ON DELETE CASCADE,value_num BIGINT,value_denom INTEGER DEFAULT 100)`,
			`CREATE TABLE books(id INTEGER PRIMARY KEY,user_id INTEGER,root_account_id INTEGER REFERENCES accounts(id) ON DELETE CASCADE,root_template_id TEXT)`,
		} {
			if _, err := h.db.Exec(q); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, a := range []struct {
		id, user, currency, placeholder int
		name, kind                      string
	}{
		{1, 2, 1, 0, "Bank RUB", "BANK"}, {2, 2, 1, 0, "Food", "EXPENSE"}, {3, 2, 2, 0, "Bank USD", "BANK"},
		{4, 2, 1, 0, "Transport", "EXPENSE"}, {5, 1, 1, 0, "Other user's account", "BANK"}, {6, 2, 1, 1, "Container", "ASSET"},
	} {
		if _, err := h.db.Exec(`INSERT INTO accounts(id,user_id,name,account_type,commodity_id,commodity_scu,non_std_scu,placeholder) VALUES(?,?,?,?,?,100,0,?)`, a.id, a.user, a.name, a.kind, a.currency, a.placeholder); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

func validFinanceInput() txSaveInput {
	return txSaveInput{Description: "Purchase", PostDate: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC), Value: 100, CreditAccountID: 1, DebitAccountID: 2}
}
func moneyPointer(v float64) *float64 { return &v }
func splitSnapshot(t *testing.T, h *Handler, id int64) []models.Split {
	t.Helper()
	rows, err := h.db.Query(`SELECT id,user_id,tx_id,account_id,value_num,value_denom FROM splits WHERE tx_id=? ORDER BY id`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []models.Split
	for rows.Next() {
		var s models.Split
		if err := rows.Scan(&s.ID, &s.UserID, &s.TxID, &s.AccountID, &s.ValueNum, &s.ValueDenom); err != nil {
			t.Fatal(err)
		}
		result = append(result, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}
func financeDelete(h *Handler, id int64) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	// Direct test request gets a real versioned cookie without touching shared state.
	cw := httptest.NewRecorder()
	_ = h.writeSession(cw, httptest.NewRequest("GET", "/", nil), 2, 1)
	h.APIAccountDelete(w, authRequest("DELETE", fmt.Sprintf("/api/v1/finance/account/delete?id=%d", id), nil, cw.Result().Cookies()[0]))
	return w
}
func complexFinanceTransaction(t *testing.T, h *Handler) int64 {
	t.Helper()
	id, err := h.saveTransaction(2, validFinanceInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Exec(`UPDATE splits SET value_num=6000 WHERE tx_id=? AND account_id=2`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Exec(`INSERT INTO splits(user_id,tx_id,account_id,value_num,value_denom) VALUES(2,?,4,4000,100)`, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestFinanceDeleteAccountPreservesHistory(t *testing.T) {
	h := financeTestHandler(t)
	id, err := h.saveTransaction(2, validFinanceInput())
	if err != nil {
		t.Fatal(err)
	}
	before := splitSnapshot(t, h, id)
	w := financeDelete(h, 1)
	if w.Code != 400 {
		t.Fatalf("delete status %d: %s", w.Code, w.Body.String())
	}
	var result map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || !strings.Contains(result["error"], "Скройте") {
		t.Fatal("missing actionable JSON error", err)
	}
	if after := splitSnapshot(t, h, id); !reflect.DeepEqual(before, after) {
		t.Fatal("account deletion changed splits")
	}
	if w := financeDelete(h, 4); w.Code != 204 {
		t.Fatalf("empty account deletion: %d %s", w.Code, w.Body.String())
	}
	if w := financeDelete(h, 5); w.Code != 400 {
		t.Fatal("foreign account deleted")
	}
	if _, err := h.db.Exec(`UPDATE accounts SET parent_id=6 WHERE id=3`); err != nil {
		t.Fatal(err)
	}
	if w := financeDelete(h, 6); w.Code != 400 {
		t.Fatal("parent account deleted")
	}
}

func TestFinanceComplexMetadataPreservesAllSplits(t *testing.T) {
	h := financeTestHandler(t)
	id := complexFinanceTransaction(t, h)
	before := splitSnapshot(t, h, id)
	in := validFinanceInput()
	in.TxID = id
	if _, err := h.saveTransaction(2, in); err == nil {
		t.Fatal("complex operation accepted by two-split editor")
	}
	if err := h.updateTransactionMetadata(2, id, in.PostDate.AddDate(0, 0, 1), "Renamed", "tag"); err != nil {
		t.Fatal(err)
	}
	if after := splitSnapshot(t, h, id); !reflect.DeepEqual(before, after) {
		t.Fatal("metadata edit changed splits")
	}
	var desc, tags string
	if err := h.db.QueryRow(`SELECT description,tags FROM transactions WHERE id=?`, id).Scan(&desc, &tags); err != nil || desc != "Renamed" || tags != "tag" {
		t.Fatal("metadata not updated", err)
	}
	if err := h.updateTransactionMetadata(1, id, in.PostDate, "Hacked", ""); err == nil {
		t.Fatal("foreign transaction changed")
	}
	w := httptest.NewRecorder()
	h.APITransactionFormGet(w, authRequest("GET", fmt.Sprintf("/api/v1/finance/transaction/form?tx_id=%d&account_id=1", id), nil, authCookie(t, h, 2)))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `action="/api/v1/finance/transaction/metadata"`) || strings.Contains(w.Body.String(), `name="value"`) || !strings.Contains(w.Body.String(), "Transport") {
		t.Fatalf("unsafe complex editor: %d %s", w.Code, w.Body.String())
	}
}

func TestFinanceInvalidAmountsAreAtomic(t *testing.T) {
	h := financeTestHandler(t)
	id, err := h.saveTransaction(2, validFinanceInput())
	if err != nil {
		t.Fatal(err)
	}
	before := splitSnapshot(t, h, id)
	cases := []struct {
		name   string
		change func(*txSaveInput)
	}{
		{"zero", func(in *txSaveInput) { in.Value = 0 }}, {"negative", func(in *txSaveInput) { in.Value = -1 }},
		{"nan", func(in *txSaveInput) { in.Value = math.NaN() }}, {"infinite", func(in *txSaveInput) { in.Value = math.Inf(1) }},
		{"overflow", func(in *txSaveInput) { in.Value = 1e100 }}, {"subcent", func(in *txSaveInput) { in.Value = 1.001 }},
		{"same account", func(in *txSaveInput) { in.DebitAccountID = 1 }}, {"foreign account", func(in *txSaveInput) { in.DebitAccountID = 5 }},
		{"container", func(in *txSaveInput) { in.DebitAccountID = 6 }}, {"missing account", func(in *txSaveInput) { in.DebitAccountID = 999 }},
		{"unbalanced", func(in *txSaveInput) { in.ValueTarget = moneyPointer(200) }},
		{"missing FX amount", func(in *txSaveInput) { in.DebitAccountID = 3 }},
		{"explicit zero target", func(in *txSaveInput) { in.ValueTarget = moneyPointer(0) }},
		{"nan target", func(in *txSaveInput) { in.ValueTarget = moneyPointer(math.NaN()) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := validFinanceInput()
			in.TxID = id
			tc.change(&in)
			if _, err := h.saveTransaction(2, in); err == nil {
				t.Fatal("invalid operation accepted")
			}
			if after := splitSnapshot(t, h, id); !reflect.DeepEqual(before, after) {
				t.Fatal("failed update changed splits")
			}
			in.TxID = 0
			if _, err := h.saveTransaction(2, in); err == nil {
				t.Fatal("invalid new operation accepted")
			}
		})
	}
	var count int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&count); err != nil || count != 1 {
		t.Fatal("invalid operation left partial records", err)
	}
}

func TestFinanceValidAmountsAndFX(t *testing.T) {
	for _, v := range []float64{0.01, 0.29, 1.15, 100, 123456789.29} {
		if _, err := transactionCents(v); err != nil {
			t.Fatalf("%v: %v", v, err)
		}
	}
	h := financeTestHandler(t)
	in := validFinanceInput()
	in.DebitAccountID = 3
	in.ValueTarget = moneyPointer(1.25)
	id, err := h.saveTransaction(2, in)
	if err != nil {
		t.Fatal(err)
	}
	splits := splitSnapshot(t, h, id)
	if len(splits) != 2 || splits[0].ValueNum != 125 || splits[1].ValueNum != -10000 {
		t.Fatalf("bad FX splits: %+v", splits)
	}
	in.TxID = id
	in.Value = 200
	in.ValueTarget = moneyPointer(2.5)
	if _, err := h.saveTransaction(2, in); err != nil {
		t.Fatal(err)
	}
	if len(splitSnapshot(t, h, id)) != 2 {
		t.Fatal("simple edit changed split count")
	}
}

func TestFinanceAPIAndMCPValidation(t *testing.T) {
	h := financeTestHandler(t)
	cookie := authCookie(t, h, 2)
	v := url.Values{"description": {"test"}, "post_date": {"2026-09-05"}, "value": {"NaN"}, "debit_account": {"2"}, "credit_account": {"1"}}
	w := httptest.NewRecorder()
	h.APITransactionSave(w, authRequest("POST", "/api/v1/finance/transaction/save", v, cookie))
	if w.Code != 400 {
		t.Fatal("API accepts NaN")
	}
	in := writeTransactionIn{Date: "2026-09-05", Description: "FX", Amount: 100, FromAccountID: 1, ToAccountID: 3}
	if _, _, err := h.mcpSaveTransaction(2, 0, in); err == nil {
		t.Fatal("MCP assumes FX rate 1:1")
	}
	in.AmountTo = moneyPointer(0)
	if _, _, err := h.mcpSaveTransaction(2, 0, in); err == nil {
		t.Fatal("MCP treats explicit zero as missing")
	}
	in.AmountTo = moneyPointer(1.25)
	if _, _, err := h.mcpSaveTransaction(2, 0, in); err != nil {
		t.Fatal(err)
	}
	id := complexFinanceTransaction(t, h)
	w = httptest.NewRecorder()
	h.APITransactionMetadataSave(w, authRequest("POST", "/api/v1/finance/transaction/metadata", url.Values{"id": {fmt.Sprint(id)}, "post_date": {"2026-09-06"}, "description": {"Changed"}, "tags": {"tag"}}, cookie))
	if w.Code != 200 {
		t.Fatal("metadata API failed", w.Body.String())
	}
	if len(splitSnapshot(t, h, id)) != 3 {
		t.Fatal("metadata API dropped a split")
	}
}

func TestFinanceAccountChangesPreserveCurrencyAndHistory(t *testing.T) {
	h := financeTestHandler(t)
	_, err := h.saveTransaction(2, validFinanceInput())
	if err != nil {
		t.Fatal(err)
	}
	cookie := authCookie(t, h, 2)
	v := url.Values{"id": {"1"}, "account_name": {"Bank RUB"}, "account_type": {"BANK"}, "commodity_id": {"2"}}
	w := httptest.NewRecorder()
	h.APIAccountSave(w, authRequest("POST", "/api/v1/finance/account/save", v, cookie))
	if w.Code != 400 {
		t.Fatal("used account currency changed")
	}
	v.Set("commodity_id", "1")
	v.Set("placeholder", "1")
	w = httptest.NewRecorder()
	h.APIAccountSave(w, authRequest("POST", "/api/v1/finance/account/save", v, cookie))
	if w.Code != 400 {
		t.Fatal("used account became container")
	}
	v.Set("placeholder", "0")
	v.Set("hidden", "1")
	w = httptest.NewRecorder()
	h.APIAccountSave(w, authRequest("POST", "/api/v1/finance/account/save", v, cookie))
	if w.Code != 200 {
		t.Fatal("cannot hide used account", w.Body.String())
	}
}

func TestFinanceConcurrentDeleteAndSave(t *testing.T) {
	if os.Getenv("FINFORME_TEST_MYSQL_DSN") == "" {
		t.Skip("requires InnoDB row locks")
	}
	for i := 0; i < 5; i++ {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			h := financeTestHandler(t)
			start := make(chan struct{})
			saved := make(chan error, 1)
			deleted := make(chan int, 1)
			go func() { <-start; _, err := h.saveTransaction(2, validFinanceInput()); saved <- err }()
			go func() { <-start; deleted <- financeDelete(h, 1).Code }()
			close(start)
			saveErr, deleteStatus := <-saved, <-deleted
			if saveErr == nil && deleteStatus != 400 {
				t.Fatalf("saved operation lost account: delete %d", deleteStatus)
			}
			if saveErr != nil && deleteStatus != 204 {
				t.Fatalf("both operations failed: save %v delete %d", saveErr, deleteStatus)
			}
			var txs, splits int
			if err := h.db.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&txs); err != nil {
				t.Fatal(err)
			}
			if err := h.db.QueryRow(`SELECT COUNT(*) FROM splits`).Scan(&splits); err != nil {
				t.Fatal(err)
			}
			if (saveErr == nil && (txs != 1 || splits != 2)) || (saveErr != nil && (txs != 0 || splits != 0)) {
				t.Fatalf("partial data: tx=%d splits=%d", txs, splits)
			}
		})
	}
}

func TestFinanceFailedSplitInsertRollsBack(t *testing.T) {
	h := financeTestHandler(t)
	id, err := h.saveTransaction(2, validFinanceInput())
	if err != nil {
		t.Fatal(err)
	}
	before := splitSnapshot(t, h, id)
	trigger := `CREATE TRIGGER reject_credit BEFORE INSERT ON splits WHEN NEW.account_id=1 BEGIN SELECT RAISE(ABORT,'test credit failure'); END`
	if os.Getenv("FINFORME_TEST_MYSQL_DSN") != "" {
		trigger = `CREATE TRIGGER reject_credit BEFORE INSERT ON splits FOR EACH ROW BEGIN IF NEW.account_id=1 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='test credit failure'; END IF; END`
	}
	if _, err := h.db.Exec(trigger); err != nil {
		t.Fatal(err)
	}
	in := validFinanceInput()
	in.TxID = id
	in.Value = 200
	in.Description = "Must roll back"
	if _, err := h.saveTransaction(2, in); err == nil {
		t.Fatal("injected failure not observed")
	}
	if after := splitSnapshot(t, h, id); !reflect.DeepEqual(before, after) {
		t.Fatal("failed insert lost original splits")
	}
	var description string
	if err := h.db.QueryRow(`SELECT description FROM transactions WHERE id=?`, id).Scan(&description); err != nil || description != "Purchase" {
		t.Fatal("metadata did not roll back", err)
	}
	in.TxID = 0
	if _, err := h.saveTransaction(2, in); err == nil {
		t.Fatal("new transaction ignored injected failure")
	}
	var count int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&count); err != nil || count != 1 {
		t.Fatal("failed insert left a transaction", err)
	}
}

func TestFinanceCSVUsesTransactionConnection(t *testing.T) {
	h := financeTestHandler(t)
	cookie := authCookie(t, h, 2)
	r := httptest.NewRequest("POST", "/api/v1/finance/import/csv/save", strings.NewReader(`{"source_account_id":1,"items":[{"date":"2026-09-05","amount":-100,"description":"CSV","other_account_id":2}]}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.APIImportCSVSave(w, r)
	if w.Code != 200 {
		t.Fatalf("CSV status %d: %s", w.Code, w.Body.String())
	}
	var n int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM splits`).Scan(&n); err != nil || n != 2 {
		t.Fatal("CSV did not save both splits", err)
	}
}

func TestFinanceLegacyComplexEditor(t *testing.T) {
	h := financeTestHandler(t)
	id := complexFinanceTransaction(t, h)
	transaction, debit, credit := h.getTransaction(2, id)
	data := map[string]interface{}{"Transaction": transaction, "Debit": debit, "Credit": credit, "MetadataOnly": true, "AccountID": int64(1)}
	var html strings.Builder
	if err := h.templates.ExecuteTemplate(&html, "finance_transaction.html", data); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html.String(), `name="value"`) || !strings.Contains(html.String(), `hx-post="/api/v1/finance/transaction/metadata"`) || !strings.Contains(html.String(), "Transport") {
		t.Fatal("legacy form can overwrite complex splits")
	}
}
