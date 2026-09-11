package handlers

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPAccountLifecycle(t *testing.T) {
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
	call := func(name string, args map[string]any, wantError bool) map[string]any {
		t.Helper()
		r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if r.IsError != wantError {
			t.Fatalf("%s error=%v, result=%+v", name, r.IsError, r.Content)
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
	call("list_commodities", map[string]any{}, false)
	r := call("create_account", map[string]any{"name": "Savings", "account_type": "BANK", "commodity_id": 2, "parent_id": 6, "description": "Keep me"}, false)
	id := r["id"]
	call("update_account", map[string]any{"id": id, "hidden": true}, false)
	a := call("get_account", map[string]any{"id": id}, false)
	if a["name"] != "Savings" || a["description"] != "Keep me" || a["parent_id"] != float64(6) || a["hidden"] != float64(1) {
		t.Fatalf("partial update lost fields: %v", a)
	}
	call("update_account", map[string]any{"id": id, "hidden": false, "parent_id": 0, "description": "", "name": "Renamed"}, false)
	a = call("get_account", map[string]any{"id": id}, false)
	if a["hidden"] != float64(0) || a["parent_id"] != nil || a["description"] != "" || a["name"] != "Renamed" {
		t.Fatalf("explicit zero values ignored: %v", a)
	}
	for _, tool := range []string{"get_account", "update_account", "delete_account"} {
		call(tool, map[string]any{"id": 5}, true)
	}
	for _, args := range []map[string]any{
		{"name": "", "account_type": "BANK", "commodity_id": 1},
		{"name": "Bad", "account_type": "ROOT", "commodity_id": 1},
		{"name": "Bad", "account_type": "BANK", "commodity_id": 999},
		{"name": "Bad", "account_type": "BANK", "commodity_id": 1, "parent_id": 5},
		{"name": "Bad", "account_type": "BANK", "commodity_id": 1, "parent_id": 1},
	} {
		call("create_account", args, true)
	}
	call("update_account", map[string]any{"id": 6, "parent_id": 6}, true)
	call("update_account", map[string]any{"id": id, "placeholder": true, "parent_id": 6}, false)
	call("update_account", map[string]any{"id": 6, "parent_id": id}, true)
	call("update_account", map[string]any{"id": 6, "placeholder": false}, true)
	call("delete_account", map[string]any{"id": 6}, true)
	txID, err := h.saveTransaction(2, validFinanceInput())
	if err != nil {
		t.Fatal(err)
	}
	before := splitSnapshot(t, h, txID)
	call("delete_account", map[string]any{"id": 1}, true)
	call("update_account", map[string]any{"id": 1, "commodity_id": 2}, true)
	call("update_account", map[string]any{"id": 1, "placeholder": true}, true)
	call("update_account", map[string]any{"id": 1, "hidden": true}, false)
	after := splitSnapshot(t, h, txID)
	if len(before) != len(after) || before[0] != after[0] || before[1] != after[1] {
		t.Fatal("history changed")
	}
	call("delete_account", map[string]any{"id": id}, false)
	call("get_account", map[string]any{"id": id}, true)
	call("update_account", map[string]any{"id": id, "name": "Gone"}, true)
	call("delete_account", map[string]any{"id": id}, true)
	if _, err := h.db.Exec(`UPDATE accounts SET account_type='ROOT' WHERE id=6`); err != nil {
		t.Fatal(err)
	}
	call("update_account", map[string]any{"id": 6, "name": "Root"}, true)
	call("delete_account", map[string]any{"id": 6}, true)
}
