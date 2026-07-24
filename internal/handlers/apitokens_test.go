package handlers

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGenerateAPIToken(t *testing.T) {
	token, err := generateAPIToken()
	if err != nil {
		t.Fatalf("generateAPIToken: %v", err)
	}
	if !strings.HasPrefix(token, "finforme_") {
		t.Errorf("токен должен начинаться с finforme_, получено: %q", token)
	}
	if len(token) != len("finforme_")+32 {
		t.Errorf("неожиданная длина токена: %d (%q)", len(token), token)
	}

	// Токены должны быть уникальными
	token2, _ := generateAPIToken()
	if token == token2 {
		t.Error("два вызова generateAPIToken вернули одинаковый токен")
	}
}

func TestHashAPIToken(t *testing.T) {
	// Хеш детерминированный, hex, 64 символа (SHA-256)
	h1 := hashAPIToken("finforme_abc")
	h2 := hashAPIToken("finforme_abc")
	if h1 != h2 {
		t.Error("хеш одного токена должен быть одинаковым")
	}
	if len(h1) != 64 {
		t.Errorf("ожидался hex SHA-256 (64 символа), получено %d", len(h1))
	}
	if h1 == hashAPIToken("finforme_abd") {
		t.Error("разные токены не должны давать одинаковый хеш")
	}
}

func TestUserIDFromBearer_NoHeader(t *testing.T) {
	// Без заголовка Authorization до БД дело не доходит — db может быть nil
	h := &Handler{}

	r := httptest.NewRequest("GET", "/api/v1/finance/accounts/get", nil)
	if _, ok := h.userIDFromBearer(r); ok {
		t.Error("запрос без Authorization не должен авторизоваться")
	}

	r.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	if _, ok := h.userIDFromBearer(r); ok {
		t.Error("запрос с Basic-авторизацией не должен приниматься")
	}

	r.Header.Set("Authorization", "Bearer ")
	if _, ok := h.userIDFromBearer(r); ok {
		t.Error("пустой Bearer-токен не должен приниматься")
	}
}

func TestMCPHandler_Unauthorized(t *testing.T) {
	h := &Handler{}
	handler := h.MCPHandler()

	r := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 401 {
		t.Errorf("без токена ожидается 401, получено %d", w.Code)
	}
	if got := w.Header().Get("WWW-Authenticate"); !strings.Contains(got, "Bearer") {
		t.Errorf("ожидался WWW-Authenticate: Bearer, получено %q", got)
	}
}

func TestBuildMCPServer(t *testing.T) {
	// Регистрация инструментов не обращается к БД — достаточно пустого Handler
	h := &Handler{}
	server := h.buildMCPServer(42)
	if server == nil {
		t.Fatal("buildMCPServer вернул nil")
	}
}

func TestParseOptionalDate(t *testing.T) {
	if d, err := parseOptionalDate(""); err != nil || !d.IsZero() {
		t.Errorf("пустая строка должна давать нулевое время без ошибки: %v %v", d, err)
	}
	d, err := parseOptionalDate("2026-07-24")
	if err != nil {
		t.Fatalf("валидная дата не распарсилась: %v", err)
	}
	want := time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)
	if !d.Equal(want) {
		t.Errorf("ожидалось %v, получено %v", want, d)
	}
	if _, err := parseOptionalDate("24.07.2026"); err == nil {
		t.Error("неверный формат даты должен давать ошибку")
	}
}
