package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/evbogdanov/finforme/internal/database"
	"github.com/gorilla/mux"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

func TestOAuthSDKDiscoveryAndLogin(t *testing.T) {
	f := newOAuthFixture(t)
	s := httptest.NewServer(f.r)
	defer s.Close()
	if err := f.h.ConfigureOAuth(s.URL); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	handler, err := auth.NewAuthorizationCodeHandler(&auth.AuthorizationCodeHandlerConfig{
		RedirectURL: f.redirect,
		DynamicClientRegistrationConfig: &auth.DynamicClientRegistrationConfig{Metadata: &oauthex.ClientRegistrationMetadata{
			ClientName: "SDK client", RedirectURIs: []string{f.redirect}, TokenEndpointAuthMethod: "none",
		}},
		AuthorizationCodeFetcher: func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
			u, err := url.Parse(args.URL)
			if err != nil {
				return nil, err
			}
			csrf, cookies := f.consent(u.Query())
			w := f.request("POST", "/oauth/authorize", url.Values{"csrf": {csrf}, "decision": {"allow"}, "write": {"1"}}.Encode(), "application/x-www-form-urlencoded", cookies)
			if w.Code != 303 {
				t.Fatalf("SDK consent: %d %s", w.Code, w.Body.String())
			}
			redirect, err := url.Parse(w.Header().Get("Location"))
			if err != nil {
				return nil, err
			}
			return &auth.AuthorizationResult{Code: redirect.Query().Get("code"), State: redirect.Query().Get("state")}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "oauth-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: s.URL + "/mcp", OAuthHandler: handler}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_account", Arguments: map[string]any{"id": 1}})
	if err != nil || result.IsError {
		t.Fatalf("SDK tool call: %+v %v", result, err)
	}
}

type oauthFixture struct {
	t                             *testing.T
	h                             *Handler
	r                             http.Handler
	client                        oauthClient
	cookie                        *http.Cookie
	verifier, challenge, redirect string
}

func newOAuthFixture(t *testing.T) *oauthFixture {
	t.Helper()
	h := financeTestHandler(t)
	if err := database.InitOAuthDB(h.db); err != nil {
		t.Fatal(err)
	}
	if err := database.InitOAuthDB(h.db); err != nil {
		t.Fatal(err)
	}
	if err := h.ConfigureOAuth("https://finfor.me"); err != nil {
		t.Fatal(err)
	}
	r := mux.NewRouter()
	h.RegisterOAuthRoutes(r)
	r.Handle("/mcp", h.MCPHandler())
	f := &oauthFixture{t: t, h: h, r: http.NewCrossOriginProtection().Handler(r), cookie: authCookie(t, h, 2), verifier: strings.Repeat("x", 43), redirect: "http://127.0.0.1:49152/callback"}
	sum := sha256.Sum256([]byte(f.verifier))
	f.challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	b, _ := json.Marshal(oauthClient{Name: "Test Codex", Redirects: []string{"http://127.0.0.1/callback"}, AuthMethod: "none"})
	w := f.request("POST", "/oauth/register", string(b), "application/json", nil)
	if w.Code != 201 {
		t.Fatalf("registration: %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &f.client); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *oauthFixture) request(method, path, body, contentType string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	f.t.Helper()
	r := httptest.NewRequest(method, "https://finfor.me"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", contentType)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	f.r.ServeHTTP(w, r)
	return w
}

func (f *oauthFixture) authorizationQuery() url.Values {
	return url.Values{"response_type": {"code"}, "client_id": {f.client.ID}, "redirect_uri": {f.redirect}, "code_challenge": {f.challenge}, "code_challenge_method": {"S256"}, "resource": {"https://finfor.me/mcp"}, "scope": {oauthRead + " " + oauthWrite}, "state": {"state+with spaces"}}
}

func (f *oauthFixture) consent(q url.Values) (string, []*http.Cookie) {
	f.t.Helper()
	w := f.request("GET", "/oauth/authorize?"+q.Encode(), "", "", []*http.Cookie{f.cookie})
	if w.Code != 200 {
		f.t.Fatalf("consent: %d %s", w.Code, w.Body.String())
	}
	m := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(w.Body.String())
	if len(m) != 2 {
		f.t.Fatalf("missing CSRF: %s", w.Body.String())
	}
	return m[1], append(w.Result().Cookies(), f.cookie)
}

func (f *oauthFixture) code(write bool) string {
	f.t.Helper()
	csrf, cookies := f.consent(f.authorizationQuery())
	form := url.Values{"csrf": {csrf}, "decision": {"allow"}}
	if write {
		form.Set("write", "1")
	}
	w := f.request("POST", "/oauth/authorize", form.Encode(), "application/x-www-form-urlencoded", cookies)
	if w.Code != 303 {
		f.t.Fatalf("allow: %d %s", w.Code, w.Body.String())
	}
	u, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		f.t.Fatal(err)
	}
	if u.Query().Get("state") != "state+with spaces" || u.Query().Get("iss") != "https://finfor.me" {
		f.t.Fatal("missing state or issuer", u)
	}
	return u.Query().Get("code")
}

func (f *oauthFixture) exchangeForm(code string) url.Values {
	return url.Values{"grant_type": {"authorization_code"}, "client_id": {f.client.ID}, "code": {code}, "redirect_uri": {f.redirect}, "code_verifier": {f.verifier}, "resource": {"https://finfor.me/mcp"}}
}

func (f *oauthFixture) token(form url.Values, status int) map[string]any {
	f.t.Helper()
	w := f.request("POST", "/oauth/token", form.Encode(), "application/x-www-form-urlencoded", nil)
	if w.Code != status {
		f.t.Fatalf("token status %d want %d: %s", w.Code, status, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		f.t.Fatal("token response is cacheable")
	}
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		f.t.Fatal(err)
	}
	return result
}

func (f *oauthFixture) bearer(token string) bool {
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	_, _, ok := f.h.oauthBearer(r)
	return ok
}

func (f *oauthFixture) mcp(token, method string, params any) string {
	f.t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	r := httptest.NewRequest("POST", "https://finfor.me/mcp", strings.NewReader(string(body)))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	w := httptest.NewRecorder()
	f.r.ServeHTTP(w, r)
	if w.Code != 200 {
		f.t.Fatalf("MCP: %d %s", w.Code, w.Body.String())
	}
	return w.Body.String()
}

func TestOAuthBrowserFlowAndScope(t *testing.T) {
	f := newOAuthFixture(t)
	for _, path := range []string{"/.well-known/oauth-authorization-server", "/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		w := f.request("GET", path, "", "", nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "https://finfor.me") {
			t.Fatal(path, w.Body.String())
		}
	}
	w := f.request("POST", "/mcp", "{}", "application/json", nil)
	if w.Code != 401 || !strings.Contains(w.Header().Get("WWW-Authenticate"), "resource_metadata=") {
		t.Fatal("missing discovery challenge")
	}
	q := f.authorizationQuery()
	w = f.request("GET", "/oauth/authorize?"+q.Encode(), "", "", nil)
	u, _ := url.Parse(w.Header().Get("Location"))
	if w.Code != 303 || u.Query().Get("next") != "/oauth/authorize?"+q.Encode() {
		t.Fatal("login lost OAuth request")
	}
	read := f.token(f.exchangeForm(f.code(false)), 200)
	access := read["access_token"].(string)
	if read["scope"] != oauthRead || !f.bearer(access) {
		t.Fatal("invalid read token")
	}
	request := httptest.NewRequest("GET", "/api/v1/finance/accounts/get", nil)
	request.Header.Set("Authorization", "Bearer "+access)
	if _, ok := f.h.userIDFromBearer(request); ok {
		t.Fatal("OAuth token escaped MCP audience")
	}
	list := f.mcp(access, "tools/list", map[string]any{})
	if strings.Contains(list, `"name":"create_account"`) || !strings.Contains(list, `"name":"get_account"`) {
		t.Fatal("scope filtering failed", list)
	}
	get := f.mcp(access, "tools/call", map[string]any{"name": "get_account", "arguments": map[string]any{"id": 1}})
	if !strings.Contains(get, "Bank RUB") {
		t.Fatal("read denied", get)
	}
	denied := f.mcp(access, "tools/call", map[string]any{"name": "delete_account", "arguments": map[string]any{"id": 4}})
	if !strings.Contains(denied, "error") {
		t.Fatal("write allowed", denied)
	}
	write := f.token(f.exchangeForm(f.code(true)), 200)
	created := f.mcp(write["access_token"].(string), "tools/call", map[string]any{"name": "create_account", "arguments": map[string]any{"name": "OAuth account", "account_type": "BANK", "commodity_id": 1}})
	if strings.Contains(created, `"isError":true`) || strings.Contains(created, `"error":`) {
		t.Fatal("write failed", created)
	}
	var count int
	if err := f.h.db.QueryRow(`SELECT COUNT(*) FROM accounts WHERE name='OAuth account' AND user_id=2`).Scan(&count); err != nil || count != 1 {
		t.Fatal("account not created", err)
	}
}

func TestOAuthPKCEBindingAndReplay(t *testing.T) {
	f := newOAuthFixture(t)
	code := f.code(true)
	for _, change := range []struct{ key, value string }{{"code_verifier", strings.Repeat("z", 43)}, {"redirect_uri", "http://127.0.0.1:49153/callback"}, {"resource", "https://evil.example/mcp"}, {"client_id", "unknown"}} {
		form := f.exchangeForm(code)
		form.Set(change.key, change.value)
		f.token(form, 400)
	}
	tokens := f.token(f.exchangeForm(code), 200)
	access := tokens["access_token"].(string)
	refresh := tokens["refresh_token"].(string)
	rotated := f.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {f.client.ID}, "refresh_token": {refresh}}, 200)
	if rotated["refresh_token"] == refresh {
		t.Fatal("refresh did not rotate")
	}
	f.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {f.client.ID}, "refresh_token": {refresh}}, 400)
	if f.bearer(access) || f.bearer(rotated["access_token"].(string)) {
		t.Fatal("replayed family not revoked")
	}
	code = f.code(false)
	tokens = f.token(f.exchangeForm(code), 200)
	f.token(f.exchangeForm(code), 400)
	if f.bearer(tokens["access_token"].(string)) {
		t.Fatal("code replay not revoked")
	}
}

func TestOAuthConsentValidation(t *testing.T) {
	f := newOAuthFixture(t)
	for _, change := range []struct{ key, value string }{{"redirect_uri", "https://evil.example/callback"}, {"redirect_uri", "http://127.0.0.1:49152/other"}, {"code_challenge_method", "plain"}, {"code_challenge", "bad"}, {"scope", "admin"}, {"resource", "https://evil.example/mcp"}} {
		q := f.authorizationQuery()
		q.Set(change.key, change.value)
		w := f.request("GET", "/oauth/authorize?"+q.Encode(), "", "", []*http.Cookie{f.cookie})
		if w.Code != 400 || w.Header().Get("Location") != "" {
			t.Fatal("invalid authorization redirected", change, w.Code)
		}
	}
	csrf, cookies := f.consent(f.authorizationQuery())
	form := url.Values{"csrf": {"wrong"}, "decision": {"allow"}}
	w := f.request("POST", "/oauth/authorize", form.Encode(), "application/x-www-form-urlencoded", cookies)
	if w.Code != 403 {
		t.Fatal("missing CSRF protection")
	}
	form.Set("csrf", csrf)
	var other []*http.Cookie
	for _, c := range cookies {
		if c.Name != "session" {
			other = append(other, c)
		}
	}
	other = append(other, authCookie(t, f.h, 1))
	w = f.request("POST", "/oauth/authorize", form.Encode(), "application/x-www-form-urlencoded", other)
	if w.Code != 403 {
		t.Fatal("consent not bound to user")
	}
	form.Set("decision", "deny")
	w = f.request("POST", "/oauth/authorize", form.Encode(), "application/x-www-form-urlencoded", cookies)
	u, _ := url.Parse(w.Header().Get("Location"))
	if w.Code != 303 || u.Query().Get("error") != "access_denied" || u.Query().Get("code") != "" {
		t.Fatal("denial failed")
	}
	var count int
	if err := f.h.db.QueryRow(`SELECT COUNT(*) FROM oauth_codes`).Scan(&count); err != nil || count != 0 {
		t.Fatal("denial issued code")
	}
}

func TestOAuthRevocationExpiryAndPasswordChange(t *testing.T) {
	f := newOAuthFixture(t)
	issue := func() map[string]any { return f.token(f.exchangeForm(f.code(true)), 200) }
	tokens := issue()
	w := f.request("POST", "/oauth/revoke", url.Values{"token": {tokens["refresh_token"].(string)}, "client_id": {f.client.ID}}.Encode(), "application/x-www-form-urlencoded", nil)
	if w.Code != 200 || f.bearer(tokens["access_token"].(string)) {
		t.Fatal("revocation failed")
	}
	tokens = issue()
	if _, err := f.h.db.Exec(`UPDATE oauth_tokens SET expires_at=? WHERE hash=?`, time.Now().Unix()-1, hashAPIToken(tokens["access_token"].(string))); err != nil {
		t.Fatal(err)
	}
	if f.bearer(tokens["access_token"].(string)) {
		t.Fatal("expired token accepted")
	}
	tokens = issue()
	if _, err := f.h.db.Exec(`UPDATE users SET session_version=session_version+1 WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	if f.bearer(tokens["access_token"].(string)) {
		t.Fatal("password change did not invalidate access")
	}
	f.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {f.client.ID}, "refresh_token": {tokens["refresh_token"].(string)}}, 400)
}

func TestOAuthRegistrationAndOrigin(t *testing.T) {
	f := newOAuthFixture(t)
	for _, raw := range []string{"https://good.example/callback", "http://127.0.0.1:4000/callback", "http://[::1]/callback"} {
		if !validOAuthRedirect(raw) {
			t.Fatal("valid redirect rejected", raw)
		}
	}
	for _, raw := range []string{"http://evil.example/callback", "javascript:alert(1)", "https://user:pass@good.example/callback", "https://good.example/#fragment", "//evil.example"} {
		if validOAuthRedirect(raw) {
			t.Fatal("unsafe redirect", raw)
		}
	}
	for _, raw := range []string{"https://finfor.me/path", "http://example.com", "https://finfor.me/?x=1"} {
		if f.h.ConfigureOAuth(raw) == nil {
			t.Fatal("unsafe issuer", raw)
		}
	}
	b, _ := json.Marshal(oauthClient{Name: "Bad", Redirects: []string{"http://evil.example/callback"}, AuthMethod: "none"})
	w := f.request("POST", "/oauth/register", string(b), "application/json", nil)
	if w.Code != 400 {
		t.Fatal("unsafe DCR accepted")
	}
	r := httptest.NewRequest("POST", "https://finfor.me/oauth/authorize", strings.NewReader("decision=allow"))
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	f.r.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross origin consent accepted")
	}
	// Exercise discovery through a real HTTP listener as well as in-process routing.
	s := httptest.NewServer(f.r)
	defer s.Close()
	resp, err := http.Get(s.URL + "/.well-known/oauth-protected-resource/mcp")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"resource":"https://finfor.me/mcp"`) {
		t.Fatal("issuer derived from untrusted Host")
	}
}

func TestOAuthConnectionsRevokeAndOwnership(t *testing.T) {
	f := newOAuthFixture(t)
	tokens := f.token(f.exchangeForm(f.code(true)), 200)
	var grant string
	if err := f.h.db.QueryRow(`SELECT grant_id FROM oauth_tokens WHERE hash=?`, hashAPIToken(tokens["access_token"].(string))).Scan(&grant); err != nil {
		t.Fatal(err)
	}
	w := f.request("GET", "/finance/settings/connections", "", "", []*http.Cookie{f.cookie})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Test Codex") {
		t.Fatal("connections rendering", w.Code, w.Body.String())
	}
	csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(w.Body.String())[1]
	cookies := append(w.Result().Cookies(), f.cookie)
	form := url.Values{"id": {grant}, "csrf": {"wrong"}}
	w = f.request("POST", "/finance/settings/connections", form.Encode(), "application/x-www-form-urlencoded", cookies)
	if w.Code != 403 || !f.bearer(tokens["access_token"].(string)) {
		t.Fatal("revoke without CSRF")
	}
	// Another user with their own valid CSRF cannot revoke this grant.
	other := authCookie(t, f.h, 1)
	w = f.request("GET", "/finance/settings/connections", "", "", []*http.Cookie{other})
	if strings.Contains(w.Body.String(), grant) {
		t.Fatal("foreign grant disclosed")
	}
	// Empty connection lists have no form, but the signed CSRF cookie is still issued.
	r := httptest.NewRequest("GET", "/", nil)
	for _, c := range w.Result().Cookies() {
		r.AddCookie(c)
	}
	s, err := f.h.store.Get(r, "oauth-connections")
	if err != nil {
		t.Fatal(err)
	}
	form.Set("csrf", s.Values["csrf"].(string))
	w = f.request("POST", "/finance/settings/connections", form.Encode(), "application/x-www-form-urlencoded", append(w.Result().Cookies(), other))
	if w.Code != 303 || !f.bearer(tokens["access_token"].(string)) {
		t.Fatal("foreign grant revoked")
	}
	form.Set("csrf", csrf)
	w = f.request("POST", "/finance/settings/connections", form.Encode(), "application/x-www-form-urlencoded", cookies)
	if w.Code != 303 || f.bearer(tokens["access_token"].(string)) {
		t.Fatal("owner revoke failed")
	}
	f.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {f.client.ID}, "refresh_token": {tokens["refresh_token"].(string)}}, 400)
}

func TestOAuthExpiryAndScopeEscalation(t *testing.T) {
	f := newOAuthFixture(t)
	code := f.code(false)
	if _, err := f.h.db.Exec(`UPDATE oauth_codes SET expires_at=? WHERE hash=?`, time.Now().Unix()-1, hashAPIToken(code)); err != nil {
		t.Fatal(err)
	}
	f.token(f.exchangeForm(code), 400)
	tokens := f.token(f.exchangeForm(f.code(false)), 200)
	refresh := url.Values{"grant_type": {"refresh_token"}, "client_id": {f.client.ID}, "refresh_token": {tokens["refresh_token"].(string)}, "scope": {oauthRead + " " + oauthWrite}}
	f.token(refresh, 400)
	refresh.Del("scope")
	if _, err := f.h.db.Exec(`UPDATE oauth_tokens SET expires_at=? WHERE hash=?`, time.Now().Unix()-1, hashAPIToken(tokens["refresh_token"].(string))); err != nil {
		t.Fatal(err)
	}
	f.token(refresh, 400)
	if _, err := f.h.db.Exec(`UPDATE users SET is_active=0 WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	if f.bearer(tokens["access_token"].(string)) {
		t.Fatal("inactive user accepted")
	}
}

func TestOAuthConsentAllowsValidatedCallbackRedirect(t *testing.T) {
	f := newOAuthFixture(t)
	w := f.request("GET", "/oauth/authorize?"+f.authorizationQuery().Encode(), "", "", []*http.Cookie{f.cookie})
	policy := w.Header().Get("Content-Security-Policy")
	if w.Code != 200 || !strings.Contains(policy, "form-action 'self' http://127.0.0.1:49152;") {
		t.Fatalf("callback blocked by consent policy: %d %s", w.Code, policy)
	}
	csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(w.Body.String())[1]
	for _, decision := range []string{"allow", "deny"} {
		response := f.request("POST", "/oauth/authorize", url.Values{"csrf": {csrf}, "decision": {decision}}.Encode(), "application/x-www-form-urlencoded", append(w.Result().Cookies(), f.cookie))
		if response.Code != 303 || response.Header().Get("Content-Security-Policy") != policy {
			t.Fatalf("callback redirect policy: %d %s", response.Code, response.Header().Get("Content-Security-Policy"))
		}
	}
	for _, redirect := range []string{"https://*.example.com/callback", "https://example.com;script-src/callback", "https://example.com'unsafe-inline'/callback"} {
		if validOAuthRedirect(redirect) {
			t.Fatalf("accepted unsafe CSP authority %q", redirect)
		}
	}
	q := f.authorizationQuery()
	q.Set("redirect_uri", "https://other.example/callback")
	w = f.request("GET", "/oauth/authorize?"+q.Encode(), "", "", []*http.Cookie{f.cookie})
	if w.Code != 400 || strings.Contains(w.Header().Get("Content-Security-Policy"), "other.example") {
		t.Fatal("unregistered redirect added to CSP")
	}
}
