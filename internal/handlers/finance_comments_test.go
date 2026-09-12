package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/evbogdanov/finforme/internal/database"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestFinanceTransactionComments(t *testing.T) {
	h := financeTestHandler(t)
	comment := "Курс: 90 RUB/USD\nДанные выписки: <script>alert(1)</script>"
	values := url.Values{"description": {"Покупка"}, "comment": {comment}, "post_date": {"2026-09-13"}, "value": {"100"}, "debit_account": {"2"}, "credit_account": {"1"}}
	cookie := authCookie(t, h, 2)
	w := httptest.NewRecorder()
	h.APITransactionSave(w, authRequest("POST", "/api/v1/finance/transaction/save", values, cookie))
	if w.Code != 200 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var result struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	id := result.ID
	assertComment := func(want string) {
		t.Helper()
		tx, _, _ := h.getTransaction(2, id)
		if tx == nil || tx.Comment != want || tx.Description != "Покупка" {
			t.Fatalf("transaction: %+v", tx)
		}
	}
	assertComment(comment)
	for _, name := range []string{"finance_transaction.html", "finance_transaction_modal_form.html"} {
		tx, debit, credit := h.getTransaction(2, id)
		var out strings.Builder
		data := map[string]any{"Transaction": tx, "Debit": debit, "Credit": credit, "MetadataOnly": true, "AccountID": int64(1)}
		if err := h.templates.ExecuteTemplate(&out, name, data); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), `name="comment"`) || !strings.Contains(out.String(), "&lt;script&gt;") || strings.Contains(out.String(), "<script>alert(1)</script>") {
			t.Fatalf("comment missing or unescaped in %s", name)
		}
	}
	txs, err := h.listTransactions(2, txListFilter{})
	if err != nil || len(txs) != 1 || txs[0]["comment"] != comment {
		t.Fatalf("list: %v %v", txs, err)
	}
	// Old full-update clients do not know about comments.
	values.Set("id", strconv.FormatInt(id, 10))
	values.Del("comment")
	w = httptest.NewRecorder()
	h.APITransactionSave(w, authRequest("POST", "/api/v1/finance/transaction/save", values, cookie))
	if w.Code != 200 {
		t.Fatalf("legacy save: %d %s", w.Code, w.Body.String())
	}
	assertComment(comment)
	before := splitSnapshot(t, h, id)
	metadata := url.Values{"id": {strconv.FormatInt(id, 10)}, "description": {"Покупка"}, "post_date": {"2026-09-13"}, "tags": {"тег"}}
	for _, tc := range []struct {
		present     bool
		value, want string
	}{{false, "", comment}, {true, "Новое уточнение", "Новое уточнение"}, {true, "", ""}} {
		if tc.present {
			metadata.Set("comment", tc.value)
		} else {
			metadata.Del("comment")
		}
		w = httptest.NewRecorder()
		h.APITransactionMetadataSave(w, authRequest("POST", "/api/v1/finance/transaction/metadata", metadata, cookie))
		if w.Code != 200 {
			t.Fatalf("metadata: %d %s", w.Code, w.Body.String())
		}
		assertComment(tc.want)
		if !reflect.DeepEqual(before, splitSnapshot(t, h, id)) {
			t.Fatal("metadata changed splits")
		}
	}
	metadata.Set("comment", "Чужой комментарий")
	w = httptest.NewRecorder()
	h.APITransactionMetadataSave(w, authRequest("POST", "/api/v1/finance/transaction/metadata", metadata, authCookie(t, h, 1)))
	if w.Code != 400 {
		t.Fatalf("other user: %d", w.Code)
	}
	assertComment("")
	long := strings.Repeat("я", 16001)
	in := validFinanceInput()
	in.TxID = id
	in.Comment = &long
	if _, err := h.saveTransaction(2, in); err == nil {
		t.Fatal("accepted long comment")
	}
	if err := h.updateTransactionMetadata(2, id, in.PostDate, "changed", "", &long); err == nil {
		t.Fatal("accepted long metadata comment")
	}
	assertComment("")
}

func TestFinanceCommentBackup(t *testing.T) {
	h := financeTestHandler(t)
	note := "Источники и расчёты\n90 × 2 = 180"
	in := validFinanceInput()
	in.Comment = &note
	if _, err := h.saveTransaction(2, in); err != nil {
		t.Fatal(err)
	}
	backup, err := h.exportBackup(2)
	if err != nil {
		t.Fatal(err)
	}
	if backup.Version != 3 || backup.Transactions[0].Comment != note {
		t.Fatalf("export: %+v", backup)
	}
	raw, err := json.Marshal(backup)
	if err != nil {
		t.Fatal(err)
	}
	var decoded backupData
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, err = h.restoreBackup(2, &decoded, "replace"); err != nil {
		t.Fatal(err)
	}
	restored, err := h.exportBackup(2)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Transactions[0].Comment != note {
		t.Fatal("round trip lost comment")
	}
	decoded.Transactions[0].Comment = strings.Repeat("😀", 16001)
	if _, err = h.restoreBackup(2, &decoded, "replace"); err == nil {
		t.Fatal("accepted oversized backup comment")
	}
	after, err := h.exportBackup(2)
	if err != nil {
		t.Fatal(err)
	}
	if after.Transactions[0].Comment != note {
		t.Fatal("failed restore lost existing comment")
	}
	// Older JSON backups omit the field and restore to an empty comment.
	decoded.Version = 2
	decoded.Transactions[0].Comment = ""
	raw, err = json.Marshal(&decoded)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.ReplaceAll(string(raw), `,"comment":""`, ""))
	decoded = backupData{}
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, err = h.restoreBackup(2, &decoded, "replace"); err != nil {
		t.Fatal(err)
	}
	after, err = h.exportBackup(2)
	if err != nil || after.Transactions[0].Comment != "" {
		t.Fatalf("old backup: %v %v", after, err)
	}
}

func TestFinanceMCPComments(t *testing.T) {
	h := financeTestHandler(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := h.buildMCPServer(2).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if r.IsError {
			t.Fatalf("%s: %+v", name, r.Content)
		}
		var out map[string]any
		if err = json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	args := map[string]any{"date": "2026-09-13", "description": "Покупка", "comment": "Расчёт курса", "amount": 100, "from_account_id": 1, "to_account_id": 2}
	id := call("create_transaction", args)["id"]
	assert := func(want string) {
		t.Helper()
		out := call("get_transaction", map[string]any{"id": id})
		if out["comment"] != want {
			t.Fatalf("get: %v", out)
		}
	}
	assert("Расчёт курса")
	args["id"] = id
	delete(args, "comment")
	call("update_transaction", args)
	assert("Расчёт курса")
	args["comment"] = "Уточнение"
	call("update_transaction", args)
	assert("Уточнение")
	meta := map[string]any{"id": id, "date": "2026-09-13", "description": "Покупка", "tags": ""}
	call("update_transaction_metadata", meta)
	assert("Уточнение")
	meta["comment"] = ""
	call("update_transaction_metadata", meta)
	assert("")
	// Metadata edits are also allowed for a transaction with more than two splits.
	complexID := complexFinanceTransaction(t, h)
	before := splitSnapshot(t, h, complexID)
	meta["id"] = complexID
	meta["comment"] = "Пояснение к распределению"
	call("update_transaction_metadata", meta)
	out := call("get_transaction", map[string]any{"id": complexID})
	if out["comment"] != meta["comment"] || !reflect.DeepEqual(before, splitSnapshot(t, h, complexID)) {
		t.Fatal(fmt.Sprintf("complex metadata: %v", out))
	}
}

func TestFinanceCommentMigration(t *testing.T) {
	if os.Getenv("FINFORME_TEST_MYSQL_DSN") == "" {
		t.Skip("requires disposable MariaDB")
	}
	h := financeTestHandler(t)
	id, err := h.saveTransaction(2, validFinanceInput())
	if err != nil {
		t.Fatal(err)
	}
	before := splitSnapshot(t, h, id)
	if _, err := h.db.Exec(`ALTER TABLE transactions DROP COLUMN comment`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := database.InitDB(h.db); err != nil {
			t.Fatal(err)
		}
	}
	tx, _, _ := h.getTransaction(2, id)
	if tx == nil || tx.Description != "Purchase" || tx.Comment != "" || !reflect.DeepEqual(before, splitSnapshot(t, h, id)) {
		t.Fatalf("migration changed operation: %+v", tx)
	}
}
