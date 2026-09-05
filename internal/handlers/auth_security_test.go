package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/evbogdanov/finforme/internal/database"
	"github.com/go-sql-driver/mysql"
	"github.com/gorilla/mux"
	"github.com/gorilla/sessions"
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

// FINFORME_TEST_MYSQL_DSN must point to a disposable server with CREATE DATABASE
// permissions. Each test creates and drops its own database, never existing data.
func authTestHandler(t *testing.T) *Handler {
	t.Helper()
	var db *sql.DB
	var err error
	if dsn := os.Getenv("FINFORME_TEST_MYSQL_DSN"); dsn != "" {
		cfg, e := mysql.ParseDSN(dsn)
		if e != nil {
			t.Fatal(e)
		}
		cfg.DBName = ""
		admin, e := sql.Open("mysql", cfg.FormatDSN())
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { admin.Close() })
		name := fmt.Sprintf("finforme_auth_test_%d", time.Now().UnixNano())
		if _, e = admin.Exec("CREATE DATABASE " + name); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			if _, e := admin.Exec("DROP DATABASE " + name); e != nil {
				t.Error(e)
			}
		})
		cfg.DBName = name
		cfg.ParseTime = true
		db, err = sql.Open("mysql", cfg.FormatDSN())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		if err = database.InitDB(db); err != nil {
			t.Fatal(err)
		}
		// Exercise idempotent migrations on an already initialized database.
		if err = database.InitDB(db); err != nil {
			t.Fatal(err)
		}
	} else {
		db, err = sql.Open("sqlite", ":memory:")
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		t.Cleanup(func() { db.Close() })
		for _, q := range []string{
			`CREATE TABLE users (id INTEGER PRIMARY KEY, username TEXT, email TEXT DEFAULT '', first_name TEXT DEFAULT '', last_name TEXT DEFAULT '', password_hash TEXT, is_active INTEGER DEFAULT 1, is_admin INTEGER DEFAULT 0, session_version INTEGER NOT NULL DEFAULT 1, password_change_required INTEGER NOT NULL DEFAULT 0, password_expires_at DATETIME)`,
			`CREATE TABLE api_tokens (id INTEGER PRIMARY KEY, user_id INTEGER, token_hash TEXT, name TEXT, prefix TEXT, last_used_at DATETIME)`,
		} {
			if _, err = db.Exec(q); err != nil {
				t.Fatal(err)
			}
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("original-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	for id := 1; id <= 2; id++ {
		if _, err = db.Exec(`INSERT INTO users(id,username,email,password_hash,is_admin) VALUES(?,?,?,?,?)`, id, fmt.Sprintf("user%d", id), "test@example.com", string(hash), id == 1); err != nil {
			t.Fatal(err)
		}
	}
	store := sessions.NewCookieStore([]byte("a-test-secret-that-is-at-least-32-bytes"))
	return &Handler{db: db, store: store, templates: buildTestTemplates(t)}
}

func authCookie(t *testing.T, h *Handler, id int64) *http.Cookie {
	t.Helper()
	w := httptest.NewRecorder()
	if err := h.setUserID(w, httptest.NewRequest("GET", "/", nil), id); err != nil {
		t.Fatal(err)
	}
	return w.Result().Cookies()[0]
}
func authRequest(method, path string, values url.Values, cookie *http.Cookie) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	return r
}
func authLogin(h *Handler, password string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.Login(w, authRequest("POST", "/accounts/login/", url.Values{"username": {"user2"}, "password": {password}}, nil))
	return w
}
func authReset(t *testing.T, h *Handler) string {
	t.Helper()
	r := authRequest("POST", "/admin/users/2/reset-password/", nil, authCookie(t, h, 1))
	r = mux.SetURLVars(r, map[string]string{"id": "2"})
	w := httptest.NewRecorder()
	h.RequireAdmin(h.AdminUserRequestPasswordChange)(w, r)
	if w.Code != 200 {
		t.Fatalf("reset status %d: %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("credential response may be cached")
	}
	m := regexp.MustCompile(`<code id="temporary-password">([0-9a-f]{32})</code>`).FindStringSubmatch(w.Body.String())
	if len(m) != 2 {
		t.Fatalf("missing credential: %s", w.Body.String())
	}
	return m[1]
}
func requireRejected(t *testing.T, h *Handler, cookie *http.Cookie) {
	t.Helper()
	if _, ok := h.getUserID(authRequest("GET", "/finance/", nil, cookie)); ok {
		t.Fatal("revoked/restricted session accepted")
	}
}

func TestAuthRecoveryLifecycle(t *testing.T) {
	h := authTestHandler(t)
	old := authCookie(t, h, 2)
	if _, err := h.db.Exec(`INSERT INTO api_tokens(id,user_id,token_hash,name,prefix) VALUES(1,2,?,'test','test')`, hashAPIToken("old-token")); err != nil {
		t.Fatal(err)
	}
	password := authReset(t, h)
	requireRejected(t, h, old)
	var n int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM api_tokens WHERE user_id=2`).Scan(&n); err != nil || n != 0 {
		t.Fatal("reset did not revoke API tokens", err)
	}
	if w := authLogin(h, "original-password"); w.Code == 303 {
		t.Fatal("old password accepted")
	}
	w := authLogin(h, password)
	if w.Code != 303 || w.Header().Get("Location") != "/accounts/password_change/" {
		t.Fatalf("login status %d: %s", w.Code, w.Body.String())
	}
	restricted := w.Result().Cookies()[0]
	requireRejected(t, h, restricted)
	w = httptest.NewRecorder()
	h.RequireAuth(h.AccountInfo)(w, authRequest("GET", "/accounts/info/", nil, restricted))
	if w.Header().Get("Location") != "/accounts/password_change/" {
		t.Fatal("recovery escaped required change")
	}
	w = httptest.NewRecorder()
	h.RequireAuthMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("API allowed recovery session") })).ServeHTTP(w, authRequest("GET", "/api/v1/finance/accounts/get", nil, restricted))
	if w.Code != 401 {
		t.Fatalf("API status %d", w.Code)
	}
	if w := authLogin(h, password); w.Code == 303 {
		t.Fatal("temporary password reused")
	}
	w = httptest.NewRecorder()
	h.RequireAuth(h.PasswordChange)(w, authRequest("GET", "/accounts/password_change/", nil, restricted))
	if w.Code != 200 || strings.Contains(w.Body.String(), `name="old_password"`) {
		t.Fatal("recovery form requires consumed password")
	}
	w = httptest.NewRecorder()
	h.RequireAuth(h.PasswordChange)(w, authRequest("POST", "/accounts/password_change/", url.Values{"new_password": {"new-password"}}, restricted))
	if w.Code != 303 {
		t.Fatalf("change status %d: %s", w.Code, w.Body.String())
	}
	fresh := w.Result().Cookies()[0]
	if _, ok := h.getUserID(authRequest("GET", "/finance/", nil, fresh)); !ok {
		t.Fatal("new session rejected")
	}
	requireRejected(t, h, restricted)
	if w := authLogin(h, password); w.Code == 303 {
		t.Fatal("temporary password works after recovery")
	}
	if w := authLogin(h, "new-password"); w.Code != 303 {
		t.Fatal("new password rejected")
	}
}

func TestAuthSessionRevocation(t *testing.T) {
	h := authTestHandler(t)
	old := authCookie(t, h, 2)
	w := httptest.NewRecorder()
	h.RequireAuth(h.PasswordChange)(w, authRequest("POST", "/accounts/password_change/", url.Values{"old_password": {"original-password"}, "new_password": {"changed-password"}}, old))
	if w.Code != 303 {
		t.Fatalf("change: %d %s", w.Code, w.Body.String())
	}
	requireRejected(t, h, old)
	fresh := w.Result().Cookies()[0]
	if _, ok := h.getUserID(authRequest("GET", "/finance/", nil, fresh)); !ok {
		t.Fatal("current session revoked")
	}
	if _, err := h.db.Exec(`UPDATE users SET is_active=0 WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	requireRejected(t, h, fresh)
}

func TestAuthRecoveryExpiryAndReplacement(t *testing.T) {
	h := authTestHandler(t)
	first := authReset(t, h)
	second := authReset(t, h)
	if first == second {
		t.Fatal("repeated reset returned same password")
	}
	if w := authLogin(h, first); w.Code == 303 {
		t.Fatal("replaced password accepted")
	}
	w := authLogin(h, second)
	if w.Code != 303 {
		t.Fatal("temporary login failed")
	}
	cookie := w.Result().Cookies()[0]
	if _, err := h.db.Exec(`UPDATE users SET password_expires_at=? WHERE id=2`, time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	requireRejected(t, h, cookie)
	w = httptest.NewRecorder()
	h.RequireAuth(h.PasswordChange)(w, authRequest("POST", "/accounts/password_change/", url.Values{"new_password": {"new-password"}}, cookie))
	if w.Header().Get("Location") == "/accounts/info/" {
		t.Fatal("expired recovery completed")
	}
	third := authReset(t, h)
	if _, err := h.db.Exec(`UPDATE users SET password_expires_at=? WHERE id=2`, time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if w := authLogin(h, third); w.Code == 303 {
		t.Fatal("expired password accepted")
	}
}

func TestAuthInvalidCookiesAndLegacySessions(t *testing.T) {
	h := authTestHandler(t)
	requireRejected(t, h, &http.Cookie{Name: "session", Value: "invalid"})
	r := httptest.NewRequest("GET", "/", nil)
	s, _ := h.store.New(r, "session")
	s.Values["user_id"] = int64(2)
	w := httptest.NewRecorder()
	if err := s.Save(r, w); err != nil {
		t.Fatal(err)
	}
	requireRejected(t, h, w.Result().Cookies()[0])
	w = httptest.NewRecorder()
	h.Login(w, authRequest("POST", "/accounts/login/", url.Values{"username": {"user2"}, "password": {"original-password"}}, &http.Cookie{Name: "session", Value: "broken"}))
	if w.Code != 303 {
		t.Fatal("bad cookie prevents legitimate login")
	}
}

func TestAuthInvalidPasswordChangePreservesAccess(t *testing.T) {
	h := authTestHandler(t)
	cookie := authCookie(t, h, 2)
	for _, v := range []url.Values{
		{"old_password": {"wrong-password"}, "new_password": {"new-password"}},
		{"old_password": {"original-password"}, "new_password": {""}},
		{"old_password": {"original-password"}, "new_password": {"яяяя"}},
		{"old_password": {"original-password"}, "new_password": {strings.Repeat("x", 73)}},
	} {
		w := httptest.NewRecorder()
		h.RequireAuth(h.PasswordChange)(w, authRequest("POST", "/accounts/password_change/", v, cookie))
		if w.Code != 200 || w.Header().Get("Location") != "" {
			t.Fatalf("invalid change: %d", w.Code)
		}
		if _, ok := h.getUserID(authRequest("GET", "/finance/", nil, cookie)); !ok {
			t.Fatal("failed change revoked session")
		}
	}
	if w := authLogin(h, "original-password"); w.Code != 303 {
		t.Fatal("failed change altered password")
	}
}

func TestAuthResetGuards(t *testing.T) {
	for _, tc := range []struct {
		name          string
		actor, target int64
		demo          bool
		want          int
	}{
		{"not admin", 2, 1, false, 403}, {"self", 1, 1, false, 400}, {"demo", 1, 2, true, 400}, {"missing", 1, 999, false, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := authTestHandler(t)
			if tc.demo {
				h.demoUserID = 2
			}
			r := authRequest("POST", "/admin/users/reset-password/", nil, authCookie(t, h, tc.actor))
			r = mux.SetURLVars(r, map[string]string{"id": fmt.Sprint(tc.target)})
			w := httptest.NewRecorder()
			h.RequireAdmin(h.AdminUserRequestPasswordChange)(w, r)
			if w.Code != tc.want {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestAuthTemporaryPasswordConcurrentLogin(t *testing.T) {
	h := authTestHandler(t)
	password := authReset(t, h)
	results := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- authLogin(h, password).Code }()
	}
	successes := 0
	for i := 0; i < 2; i++ {
		if <-results == 303 {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("temporary password authenticated %d concurrent requests", successes)
	}
}

func TestAuthBearerRestrictions(t *testing.T) {
	h := authTestHandler(t)
	if _, err := h.db.Exec(`INSERT INTO api_tokens(id,user_id,token_hash,name,prefix) VALUES(1,2,?,'test','test')`, hashAPIToken("test-token")); err != nil {
		t.Fatal(err)
	}
	r := authRequest("GET", "/api/v1/finance/accounts/get", nil, nil)
	r.Header.Set("Authorization", "Bearer test-token")
	if id, ok := h.getUserID(r); !ok || id != 2 {
		t.Fatal("valid token rejected")
	}
	if _, err := h.db.Exec(`UPDATE users SET password_change_required=1 WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.getUserID(r); ok {
		t.Fatal("token bypassed required password change")
	}
	if _, err := h.db.Exec(`UPDATE users SET password_change_required=0, is_active=0 WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.getUserID(r); ok {
		t.Fatal("inactive user's token accepted")
	}
}

func TestAuthMariaDBUpgrade(t *testing.T) {
	if os.Getenv("FINFORME_TEST_MYSQL_DSN") == "" {
		t.Skip("requires disposable MariaDB")
	}
	h := authTestHandler(t)
	if _, err := h.db.Exec(`ALTER TABLE users DROP COLUMN session_version, DROP COLUMN password_change_required, DROP COLUMN password_expires_at`); err != nil {
		t.Fatal(err)
	}
	if err := database.InitDB(h.db); err != nil {
		t.Fatal(err)
	}
	if w := authLogin(h, "original-password"); w.Code != 303 {
		t.Fatal("existing user cannot log in after migration")
	}
}
