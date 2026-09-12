package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/evbogdanov/finforme/internal/database"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func memoryClient(t *testing.T, h *Handler, userID int64) func(string, map[string]any, bool) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	ct, st := mcp.NewInMemoryTransports()
	ss, err := h.buildMCPServer(userID).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return func(name string, args map[string]any, wantError bool) map[string]any {
		t.Helper()
		r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if r.IsError != wantError {
			t.Fatalf("%s error=%v: %+v", name, r.IsError, r.Content)
		}
		if wantError {
			return nil
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
}

func TestFinanceMCPMemory(t *testing.T) {
	h := financeTestHandler(t)
	first := memoryClient(t, h, 1)
	second := memoryClient(t, h, 2)
	key := map[string]any{"key": "preferences.currency"}
	empty := first("list_memory", map[string]any{}, false)
	if len(empty["memories"].([]any)) != 0 || empty["has_more"] != false {
		t.Fatalf("empty: %v", empty)
	}
	first("get_memory", key, true)
	first("save_memory", map[string]any{"key": key["key"], "content": "Рубли\nКурс ЦБ"}, false)
	second("get_memory", key, true)
	if second("delete_memory", key, false)["deleted"] != false {
		t.Fatal("deleted another user's note")
	}
	if len(second("list_memory", map[string]any{}, false)["memories"].([]any)) != 0 {
		t.Fatal("list leaked notes")
	}
	second("save_memory", map[string]any{"key": key["key"], "content": "Доллары"}, false)
	first("save_memory", map[string]any{"key": key["key"], "content": "Рубли, обновлено"}, false)
	// A fresh MCP session reads persisted data, not session state.
	fresh := memoryClient(t, h, 1)
	out := fresh("get_memory", key, false)
	if out["content"] != "Рубли, обновлено" {
		t.Fatalf("read: %v", out)
	}
	if _, err := time.Parse(time.RFC3339, out["updated_at"].(string)); err != nil {
		t.Fatal(err)
	}
	if second("get_memory", key, false)["content"] != "Доллары" {
		t.Fatal("update crossed users")
	}
	if fresh("delete_memory", key, false)["deleted"] != true {
		t.Fatal("delete failed")
	}
	fresh("get_memory", key, true)
	if second("get_memory", key, false)["content"] != "Доллары" {
		t.Fatal("delete crossed users")
	}
	for _, bad := range []string{"", "UPPER", "a b", "../bad", "ключ", strings.Repeat("a", 129)} {
		first("save_memory", map[string]any{"key": bad, "content": "note"}, true)
		first("get_memory", map[string]any{"key": bad}, true)
		first("delete_memory", map[string]any{"key": bad}, true)
	}
	for _, bad := range []string{"", "  \n", strings.Repeat("я", 16001)} {
		second("save_memory", map[string]any{"key": key["key"], "content": bad}, true)
	}
	if second("get_memory", key, false)["content"] != "Доллары" {
		t.Fatal("invalid update lost note")
	}
}

func TestFinanceMemoryPaginationAndConcurrentSave(t *testing.T) {
	h := financeTestHandler(t)
	ctx := context.Background()
	for _, key := range []string{"a_one", "a_two", "abother"} {
		if _, err := h.saveMemory(ctx, 2, saveMemoryIn{Key: key, Content: "note"}); err != nil {
			t.Fatal(err)
		}
	}
	out, err := h.listMemory(ctx, 2, listMemoryIn{Prefix: "a_", Limit: 1})
	if err != nil || len(out.Memories) != 1 || out.Memories[0].Key != "a_one" || !out.HasMore {
		t.Fatalf("page1: %+v %v", out, err)
	}
	out, err = h.listMemory(ctx, 2, listMemoryIn{Prefix: "a_", Limit: 1, Offset: 1})
	if err != nil || len(out.Memories) != 1 || out.Memories[0].Key != "a_two" || out.HasMore {
		t.Fatalf("page2: %+v %v", out, err)
	}
	for _, in := range []listMemoryIn{{Limit: -1}, {Limit: 101}, {Offset: -1}, {Prefix: "%"}} {
		if _, err := h.listMemory(ctx, 2, in); err == nil {
			t.Fatal("accepted invalid pagination")
		}
	}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := h.saveMemory(ctx, 2, saveMemoryIn{Key: "same", Content: fmt.Sprint(i)})
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM user_memory WHERE user_id=2 AND memory_key='same'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("duplicates: %d %v", n, err)
	}
	// Memory is independent of finance restore and cannot be erased by it.
	backup, err := h.exportBackup(2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.restoreBackup(2, backup, "replace"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.getMemory(ctx, 2, "same"); err != nil {
		t.Fatal(err)
	}
}

func TestOAuthMemoryScope(t *testing.T) {
	f := newOAuthFixture(t)
	read := f.token(f.exchangeForm(f.code(false)), 200)["access_token"].(string)
	list := f.mcp(read, "tools/list", map[string]any{})
	for _, name := range []string{"list_memory", "get_memory"} {
		if !strings.Contains(list, `"name":"`+name+`"`) {
			t.Fatalf("read tool missing: %s", name)
		}
	}
	for _, name := range []string{"save_memory", "delete_memory"} {
		if strings.Contains(list, `"name":"`+name+`"`) {
			t.Fatalf("write tool exposed: %s", name)
		}
		denied := f.mcp(read, "tools/call", map[string]any{"name": name, "arguments": map[string]any{"key": "scope", "content": "wrong"}})
		if !strings.Contains(denied, "error") {
			t.Fatal("read token wrote memory", denied)
		}
	}
	write := f.token(f.exchangeForm(f.code(true)), 200)["access_token"].(string)
	saved := f.mcp(write, "tools/call", map[string]any{"name": "save_memory", "arguments": map[string]any{"key": "scope", "content": "Scope note"}})
	if strings.Contains(saved, `"isError":true`) || strings.Contains(saved, `"error":`) {
		t.Fatal("write denied", saved)
	}
	got := f.mcp(read, "tools/call", map[string]any{"name": "get_memory", "arguments": map[string]any{"key": "scope"}})
	if !strings.Contains(got, "Scope note") {
		t.Fatal("read denied", got)
	}
}

func TestFinanceMemoryUserDeletionAndReinitialization(t *testing.T) {
	h := financeTestHandler(t)
	ctx := context.Background()
	if _, err := h.db.Exec(`INSERT INTO users(id,username,email,password_hash) VALUES(3,'memory-only','memory@example.test','unused')`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.saveMemory(ctx, 3, saveMemoryIn{Key: "context", Content: "Persistent note"}); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("FINFORME_TEST_MYSQL_DSN") != "" {
		if err := database.InitDB(h.db); err != nil {
			t.Fatal(err)
		}
		got, err := h.getMemory(ctx, 3, "context")
		if err != nil || got.Content != "Persistent note" {
			t.Fatalf("reinitialization: %+v %v", got, err)
		}
	}
	if _, err := h.db.Exec(`DELETE FROM users WHERE id=3`); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM user_memory WHERE user_id=3`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cascade: %d %v", count, err)
	}
}
