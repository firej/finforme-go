package handlers

import (
	"database/sql"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestAuthLastLogin(t *testing.T) {
	h := authTestHandler(t)
	read := func() sql.NullTime {
		t.Helper()
		var date sql.NullTime
		if err := h.db.QueryRow(`SELECT last_login_at FROM users WHERE id=2`).Scan(&date); err != nil {
			t.Fatal(err)
		}
		return date
	}
	if read().Valid {
		t.Fatal("existing user should start without a login date")
	}
	authLogin(h, "wrong-password")
	if read().Valid {
		t.Fatal("failed login changed date")
	}
	before := time.Now().UTC().Truncate(time.Second)
	w := authLogin(h, "original-password")
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	got := read()
	if !got.Valid || got.Time.Before(before) || got.Time.After(time.Now().UTC()) {
		t.Fatal("successful login not recorded", got)
	}
	// Reissuing a session (e.g. after a password change) is not a login.
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if _, err := h.db.Exec(`UPDATE users SET last_login_at=? WHERE id=2`, old); err != nil {
		t.Fatal(err)
	}
	if err := h.writeSession(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), 2, 1); err != nil {
		t.Fatal(err)
	}
	if !read().Time.Equal(old) {
		t.Fatal("session refresh changed login date")
	}
	if _, err := h.db.Exec(`UPDATE users SET is_active=0 WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	authLogin(h, "original-password")
	if !read().Time.Equal(old) {
		t.Fatal("inactive user changed login date")
	}
	if _, err := h.db.Exec(`UPDATE users SET is_active=1 WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	h.demoUserID = 2
	w = httptest.NewRecorder()
	h.LoginDemo(w, httptest.NewRequest("POST", "/accounts/login-demo/", nil))
	if w.Code != 303 || !read().Time.After(old) {
		t.Fatal("demo login not recorded")
	}
	h.demoUserID = 0
	temporary := authReset(t, h)
	if w := authLogin(h, temporary); w.Code != 303 {
		t.Fatal("temporary login failed", w.Body.String())
	}
	date := read()
	authLogin(h, temporary)
	if !read().Time.Equal(date.Time) {
		t.Fatal("reused temporary credential changed date")
	}

	w = httptest.NewRecorder()
	h.Register(w, authRequest("POST", "/accounts/register/", url.Values{"username": {"new-user"}, "password": {"new-user-password"}, "email": {"new@example.com"}}, nil))
	if w.Code != 303 {
		t.Fatal("registration failed", w.Body.String())
	}
	var registered sql.NullTime
	if err := h.db.QueryRow(`SELECT last_login_at FROM users WHERE username='new-user'`).Scan(&registered); err != nil || !registered.Valid {
		t.Fatal("registration login not recorded", registered, err)
	}
}

func TestAdminLastLoginColumn(t *testing.T) {
	h := financeTestHandler(t)
	stamp := time.Date(2026, 10, 3, 12, 34, 56, 0, time.UTC)
	if _, err := h.db.Exec(`UPDATE users SET first_name=NULL,last_name=NULL,last_login_at=? WHERE id=2`, stamp); err != nil {
		t.Fatal(err)
	}
	cookie := authCookie(t, h, 1)
	render := func() string {
		t.Helper()
		w := httptest.NewRecorder()
		h.RequireAdmin(h.AdminUsers)(w, authRequest("GET", "/admin/users/", nil, cookie))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	body := render()
	if !strings.Contains(body, "Последний логин (UTC)") || !strings.Contains(body, "03.10.2026 12:34:56") {
		t.Fatal("missing login column or expected date")
	}
	if _, err := h.db.Exec(`UPDATE users SET last_login_at=NULL WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(render(), "Нет данных") {
		t.Fatal("missing unknown date placeholder")
	}
}
