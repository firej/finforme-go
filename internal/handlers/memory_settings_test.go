package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

func TestFinanceMemorySettings(t *testing.T) {
	h := financeTestHandler(t)
	ctx := context.Background()
	note := "Моя заметка\n</textarea><script>alert(1)</script>"
	if _, err := h.saveMemory(ctx, 2, saveMemoryIn{Key: "own", Content: note}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.saveMemory(ctx, 1, saveMemoryIn{Key: "private", Content: "Other user's private note"}); err != nil {
		t.Fatal(err)
	}
	router := mux.NewRouter()
	router.HandleFunc("/finance/settings/memory", h.RequireAuth(h.MemorySettings)).Methods("GET", "POST")
	protected := http.NewCrossOriginProtection().Handler(router)
	cookie := authCookie(t, h, 2)
	request := func(method, path string, values url.Values, auth *http.Cookie, origin string) *httptest.ResponseRecorder {
		t.Helper()
		r := authRequest(method, path, values, auth)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		protected.ServeHTTP(w, r)
		return w
	}
	for _, method := range []string{"GET", "POST"} {
		if w := request(method, "/finance/settings/memory", nil, nil, ""); w.Code != 302 && w.Code != 303 {
			t.Fatalf("anonymous %s: %d", method, w.Code)
		}
	}
	w := request("GET", "/finance/settings/memory", nil, cookie, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), ">own</a>") || strings.Contains(w.Body.String(), "private") {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	w = request("GET", "/finance/settings/memory?edit=own", nil, cookie, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "&lt;/textarea&gt;&lt;script&gt;") || strings.Contains(w.Body.String(), "<script>alert(1)</script>") {
		t.Fatalf("edit escaping: %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("memory page cacheable")
	}
	if w := request("GET", "/finance/settings/memory?edit=private", nil, cookie, ""); w.Code != 404 {
		t.Fatalf("foreign note: %d", w.Code)
	}
	values := url.Values{"action": {"save"}, "key": {"own"}, "content": {"Changed in settings"}, "user_id": {"1"}, "editing": {"1"}}
	if w := request("POST", "/finance/settings/memory", values, cookie, "https://evil.example"); w.Code != 403 {
		t.Fatalf("cross origin: %d", w.Code)
	}
	current, err := h.getMemory(ctx, 2, "own")
	if err != nil || current.Content != note {
		t.Fatal("blocked request changed note")
	}
	if w := request("POST", "/finance/settings/memory", values, cookie, ""); w.Code != 303 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	client := memoryClient(t, h, 2)
	if out := client("get_memory", map[string]any{"key": "own"}, false); out["content"] != "Changed in settings" {
		t.Fatalf("MCP read after UI edit: %v", out)
	}
	values.Set("content", strings.Repeat("я", 16001))
	w = request("POST", "/finance/settings/memory", values, cookie, "")
	if w.Code != 400 || !strings.Contains(w.Body.String(), values.Get("content")) {
		t.Fatalf("validation lost form: %d", w.Code)
	}
	values.Set("action", "delete")
	values.Set("key", "private")
	if w := request("POST", "/finance/settings/memory", values, cookie, ""); w.Code != 303 {
		t.Fatalf("delete missing: %d", w.Code)
	}
	other, err := h.getMemory(ctx, 1, "private")
	if err != nil || other.Content != "Other user's private note" {
		t.Fatal("deleted another user's note")
	}
	values.Set("key", "own")
	if w := request("POST", "/finance/settings/memory", values, cookie, ""); w.Code != 303 {
		t.Fatalf("delete: %d", w.Code)
	}
	client("get_memory", map[string]any{"key": "own"}, true)
	for _, path := range []string{"/finance/settings/memory?offset=-1", "/finance/settings/memory?offset=no", "/finance/settings/memory?edit=BAD", "/finance/settings/memory?prefix=%25"} {
		if w := request("GET", path, nil, cookie, ""); w.Code != 400 {
			t.Fatalf("invalid query %s: %d", path, w.Code)
		}
	}
	values = url.Values{"key": {"new"}, "content": {"New note"}, "action": {"save"}}
	if w := request("POST", "/finance/settings/memory", values, cookie, ""); w.Code != 303 {
		t.Fatalf("create: %d", w.Code)
	}
	w = request("GET", "/finance/settings/memory?prefix=absent", nil, cookie, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "По этому префиксу заметок нет") {
		t.Fatalf("empty search: %d", w.Code)
	}
}
