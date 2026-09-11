package handlers

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gorilla/mux"
)

const oauthRead = "finforme:read"
const oauthWrite = "finforme:write"

type oauthServer struct {
	issuer             string
	mu                 sync.Mutex
	registrationWindow time.Time
	registrations      int
}

// Public origin is deployment configuration, never derived from Host or forwarded headers.
func (h *Handler) ConfigureOAuth(issuer string) error {
	u, err := url.Parse(issuer)
	if err != nil || len(issuer) > 500 || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") ||
		(u.Scheme != "https" && !(u.Scheme == "http" && loopbackHost(u.Hostname()))) {
		return fmt.Errorf("PUBLIC_URL must be an HTTPS origin (HTTP allowed only on localhost)")
	}
	h.oauth = &oauthServer{issuer: strings.TrimRight(issuer, "/")}
	return nil
}

func loopbackHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func oauthSecret(prefix string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b), nil
}

func oauthJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func oauthError(w http.ResponseWriter, status int, code string) {
	oauthJSON(w, status, map[string]string{"error": code})
}

func (h *Handler) RegisterOAuthRoutes(r *mux.Router) {
	r.HandleFunc("/.well-known/oauth-authorization-server", h.oauthMetadata).Methods("GET")
	r.HandleFunc("/.well-known/oauth-protected-resource", h.oauthResourceMetadata).Methods("GET")
	r.HandleFunc("/.well-known/oauth-protected-resource/mcp", h.oauthResourceMetadata).Methods("GET")
	r.HandleFunc("/oauth/register", h.oauthRegister).Methods("POST")
	r.HandleFunc("/oauth/authorize", h.oauthAuthorize).Methods("GET", "POST")
	r.HandleFunc("/oauth/token", h.oauthToken).Methods("POST")
	r.HandleFunc("/oauth/revoke", h.oauthRevoke).Methods("POST")
	r.HandleFunc("/finance/settings/connections", h.oauthConnections).Methods("GET", "POST")
}

func (h *Handler) oauthMetadata(w http.ResponseWriter, r *http.Request) {
	i := h.oauth.issuer
	oauthJSON(w, 200, map[string]any{
		"issuer": i, "authorization_endpoint": i + "/oauth/authorize", "token_endpoint": i + "/oauth/token",
		"registration_endpoint": i + "/oauth/register", "revocation_endpoint": i + "/oauth/revoke",
		"response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported": []string{"none"}, "revocation_endpoint_auth_methods_supported": []string{"none"},
		"code_challenge_methods_supported": []string{"S256"}, "scopes_supported": []string{oauthRead, oauthWrite},
		"authorization_response_iss_parameter_supported": true,
	})
}

func (h *Handler) oauthResourceMetadata(w http.ResponseWriter, r *http.Request) {
	oauthJSON(w, 200, map[string]any{"resource": h.oauth.issuer + "/mcp", "authorization_servers": []string{h.oauth.issuer},
		"scopes_supported": []string{oauthRead, oauthWrite}, "bearer_methods_supported": []string{"header"}})
}

type oauthClient struct {
	ID            string   `json:"client_id,omitempty"`
	Name          string   `json:"client_name"`
	Redirects     []string `json:"redirect_uris"`
	AuthMethod    string   `json:"token_endpoint_auth_method"`
	GrantTypes    []string `json:"grant_types,omitempty"`
	ResponseTypes []string `json:"response_types,omitempty"`
}

func validOAuthRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	// The validated authority is also used as a CSP source. Do not allow
	// whitespace, wildcards or directive delimiters to widen that policy.
	for _, c := range u.Host {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune(".-:[]", c)) {
			return false
		}
	}
	return err == nil && len(raw) <= 512 && u.Host != "" && u.User == nil && u.Fragment == "" &&
		(u.Scheme == "https" || (u.Scheme == "http" && loopbackHost(u.Hostname())))
}

func (h *Handler) oauthRegister(w http.ResponseWriter, r *http.Request) {
	// No outbound metadata fetches. Bound anonymous registration rate and storage.
	o := h.oauth
	o.mu.Lock()
	if time.Since(o.registrationWindow) >= time.Minute {
		o.registrationWindow = time.Now()
		o.registrations = 0
	}
	if o.registrations >= 30 {
		o.mu.Unlock()
		w.Header().Set("Retry-After", "60")
		oauthError(w, 429, "temporarily_unavailable")
		return
	}
	o.registrations++
	o.mu.Unlock()
	var count int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM oauth_clients`).Scan(&count); err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	if count >= 10000 {
		oauthError(w, 503, "temporarily_unavailable")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	d := json.NewDecoder(r.Body)
	var c oauthClient
	if err := d.Decode(&c); err != nil {
		oauthError(w, 400, "invalid_client_metadata")
		return
	}
	if d.Decode(new(any)) != io.EOF {
		oauthError(w, 400, "invalid_client_metadata")
		return
	}
	c.Name = strings.TrimSpace(c.Name)
	if c.ID != "" || c.Name == "" || utf8.RuneCountInString(c.Name) > 100 || len(c.Redirects) == 0 || len(c.Redirects) > 5 || c.AuthMethod != "none" {
		oauthError(w, 400, "invalid_client_metadata")
		return
	}
	for _, v := range c.Redirects {
		if !validOAuthRedirect(v) {
			oauthError(w, 400, "invalid_redirect_uri")
			return
		}
	}
	for _, v := range c.GrantTypes {
		if v != "authorization_code" && v != "refresh_token" {
			oauthError(w, 400, "invalid_client_metadata")
			return
		}
	}
	for _, v := range c.ResponseTypes {
		if v != "code" {
			oauthError(w, 400, "invalid_client_metadata")
			return
		}
	}
	var err error
	c.ID, err = oauthSecret("client_")
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	redirects, _ := json.Marshal(c.Redirects)
	if _, err = h.db.Exec(`INSERT INTO oauth_clients(id,name,redirects,created_at) VALUES(?,?,?,?)`, c.ID, c.Name, string(redirects), time.Now().Unix()); err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	c.GrantTypes = []string{"authorization_code", "refresh_token"}
	c.ResponseTypes = []string{"code"}
	oauthJSON(w, 201, c)
}

func (h *Handler) oauthClient(id string) (oauthClient, error) {
	var c oauthClient
	var redirects string
	err := h.db.QueryRow(`SELECT id,name,redirects FROM oauth_clients WHERE id=?`, id).Scan(&c.ID, &c.Name, &redirects)
	if err == nil && c.ID != id {
		return c, sql.ErrNoRows
	}
	if err == nil {
		err = json.Unmarshal([]byte(redirects), &c.Redirects)
	}
	return c, err
}

// Native clients may choose a loopback listener port at login (RFC 8252).
func matchesOAuthRedirect(c oauthClient, raw string) bool {
	if !validOAuthRedirect(raw) {
		return false
	}
	for _, registered := range c.Redirects {
		if raw == registered {
			return true
		}
		a, _ := url.Parse(registered)
		b, _ := url.Parse(raw)
		if a.Scheme == "http" && b.Scheme == "http" && (a.Hostname() == "127.0.0.1" || a.Hostname() == "::1") && a.Hostname() == b.Hostname() {
			a.Host = a.Hostname()
			b.Host = b.Hostname()
			if a.String() == b.String() {
				return true
			}
		}
	}
	return false
}

func oauthScopes(s string) (string, bool) {
	read, write := false, false
	for _, part := range strings.Fields(s) {
		switch part {
		case oauthRead:
			read = true
		case oauthWrite:
			write = true
		default:
			return "", false
		}
	}
	if !read && !write {
		return oauthRead, true
	}
	if write {
		return oauthRead + " " + oauthWrite, true
	}
	return oauthRead, true
}

func oauthForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := r.ParseForm(); err != nil {
		oauthError(w, 400, "invalid_request")
		return false
	}
	for _, values := range r.PostForm {
		if len(values) != 1 {
			oauthError(w, 400, "invalid_request")
			return false
		}
	}
	return true
}
