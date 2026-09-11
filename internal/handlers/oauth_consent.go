package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/sessions"
)

type oauthAuthorization struct {
	ClientID  string
	Redirect  string
	Challenge string
	Scope     string
	State     string
	Resource  string
}

// Chromium checks the callback redirect against the original form's policy.
// Call only after matching the redirect against the registered OAuth client.
func oauthConsentPolicy(redirect string) string {
	u, _ := url.Parse(redirect)
	return "default-src 'none'; style-src 'unsafe-inline'; form-action 'self' " + u.Scheme + "://" + u.Host + "; frame-ancestors 'none'; base-uri 'none'"
}

func (h *Handler) oauthBrowserUser(w http.ResponseWriter, r *http.Request) (int64, int64, bool) {
	id, version, required, ok := h.sessionUser(r)
	if required {
		http.Redirect(w, r, "/accounts/password_change/", 303)
		return 0, 0, false
	}
	if !ok {
		if r.Method != "GET" {
			http.Error(w, "Войдите заново и повторите подключение", 401)
			return 0, 0, false
		}
		http.Redirect(w, r, "/accounts/login/?next="+url.QueryEscape(r.URL.RequestURI()), 303)
		return 0, 0, false
	}
	if h.isDemo(id) {
		http.Error(w, "Подключение приложений недоступно в демо-аккаунте", 403)
		return 0, 0, false
	}
	return id, version, true
}

func (h *Handler) oauthBrowserCookie(name string) *sessions.Session {
	s := sessions.NewSession(h.store, name)
	s.Options = &sessions.Options{Path: "/", MaxAge: 600, HttpOnly: true, Secure: strings.HasPrefix(h.oauth.issuer, "https://"), SameSite: http.SameSiteLaxMode}
	return s
}

func (h *Handler) oauthCSRF(w http.ResponseWriter, r *http.Request, name string, user, version int64, payload string) (string, error) {
	nonce, err := oauthSecret("")
	if err != nil {
		return "", err
	}
	s := h.oauthBrowserCookie(name)
	s.Values["csrf"] = nonce
	s.Values["user"] = user
	s.Values["version"] = version
	s.Values["expires"] = time.Now().Add(10 * time.Minute).Unix()
	s.Values["payload"] = payload
	return nonce, s.Save(r, w)
}

func (h *Handler) oauthCheckCSRF(r *http.Request, name string, user, version int64) (string, bool) {
	s, err := h.store.Get(r, name)
	if err != nil {
		return "", false
	}
	nonce, _ := s.Values["csrf"].(string)
	expires, _ := s.Values["expires"].(int64)
	u, _ := s.Values["user"].(int64)
	v, _ := s.Values["version"].(int64)
	payload, _ := s.Values["payload"].(string)
	return payload, nonce != "" && subtle.ConstantTimeCompare([]byte(nonce), []byte(r.PostForm.Get("csrf"))) == 1 && u == user && v == version && expires > time.Now().Unix()
}

func (h *Handler) oauthAuthorize(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	if len(r.URL.RawQuery) > 2048 {
		oauthError(w, 400, "invalid_request")
		return
	}
	var a oauthAuthorization
	if r.Method == "GET" {
		q := r.URL.Query()
		for _, values := range q {
			if len(values) != 1 {
				oauthError(w, 400, "invalid_request")
				return
			}
		}
		a = oauthAuthorization{ClientID: q.Get("client_id"), Redirect: q.Get("redirect_uri"), Challenge: q.Get("code_challenge"), State: q.Get("state"), Resource: q.Get("resource")}
		var ok bool
		a.Scope, ok = oauthScopes(q.Get("scope"))
		if !ok {
			oauthError(w, 400, "invalid_scope")
			return
		}
		if q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || !validPKCEChallenge(a.Challenge) || len(a.State) > 256 || a.Resource != h.oauth.issuer+"/mcp" {
			oauthError(w, 400, "invalid_request")
			return
		}
		c, err := h.oauthClient(a.ClientID)
		if err != nil || !matchesOAuthRedirect(c, a.Redirect) {
			oauthError(w, 400, "invalid_request")
			return
		}
		w.Header().Set("Content-Security-Policy", oauthConsentPolicy(a.Redirect))
		user, version, ok := h.oauthBrowserUser(w, r)
		if !ok {
			return
		}
		payload, _ := json.Marshal(a)
		csrf, err := h.oauthCSRF(w, r, "oauth-consent", user, version, string(payload))
		if err != nil {
			oauthError(w, 500, "server_error")
			return
		}
		h.renderTemplate(w, "oauth_consent.html", map[string]any{"Client": c.Name, "Redirect": a.Redirect, "CSRF": csrf, "Write": strings.Contains(a.Scope, oauthWrite)})
		return
	}
	if !oauthForm(w, r) {
		return
	}
	user, version, ok := h.oauthBrowserUser(w, r)
	if !ok {
		return
	}
	payload, ok := h.oauthCheckCSRF(r, "oauth-consent", user, version)
	if !ok || json.Unmarshal([]byte(payload), &a) != nil {
		oauthError(w, 403, "invalid_request")
		return
	}
	c, err := h.oauthClient(a.ClientID)
	if err != nil || !matchesOAuthRedirect(c, a.Redirect) || a.Resource != h.oauth.issuer+"/mcp" {
		oauthError(w, 400, "invalid_request")
		return
	}
	w.Header().Set("Content-Security-Policy", oauthConsentPolicy(a.Redirect))
	redirect, _ := url.Parse(a.Redirect)
	q := redirect.Query()
	q.Set("state", a.State)
	q.Set("iss", h.oauth.issuer)
	if r.PostForm.Get("decision") == "deny" {
		q.Set("error", "access_denied")
	} else {
		if r.PostForm.Get("decision") != "allow" {
			oauthError(w, 400, "invalid_request")
			return
		}
		// Users may narrow the requested permission, never expand it.
		scope := oauthRead
		if r.PostForm.Get("write") == "1" && strings.Contains(a.Scope, oauthWrite) {
			scope += " " + oauthWrite
		}
		code, err := h.oauthCreateCode(user, version, a, scope)
		if err != nil {
			oauthError(w, 500, "server_error")
			return
		}
		q.Set("code", code)
	}
	s := h.oauthBrowserCookie("oauth-consent")
	s.Options.MaxAge = -1
	if err := s.Save(r, w); err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	redirect.RawQuery = q.Encode()
	http.Redirect(w, r, redirect.String(), 303)
}

func (h *Handler) oauthCreateCode(user, version int64, a oauthAuthorization, scope string) (string, error) {
	code, err := oauthSecret("code_")
	if err != nil {
		return "", err
	}
	grant, err := oauthSecret("grant_")
	if err != nil {
		return "", err
	}
	tx, err := h.beginFinanceWrite(user)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var id int64
	if err = tx.QueryRow(`SELECT id FROM users WHERE id=? AND session_version=? AND is_active=1 AND password_change_required=0`, user, version).Scan(&id); err != nil {
		return "", err
	}
	now := time.Now().Unix()
	if _, err = tx.Exec(`INSERT INTO oauth_grants(id,user_id,client_id,scope,resource,session_version,created_at,expires_at,revoked) VALUES(?,?,?,?,?,?,?,?,0)`, grant, user, a.ClientID, scope, a.Resource, version, now, now+90*86400); err != nil {
		return "", err
	}
	if _, err = tx.Exec(`INSERT INTO oauth_codes(hash,grant_id,redirect_uri,challenge,expires_at,used) VALUES(?,?,?,?,?,0)`, hashAPIToken(code), grant, a.Redirect, a.Challenge, now+300); err != nil {
		return "", err
	}
	return code, tx.Commit()
}

type oauthConnection struct{ ID, Name, Scope, Created string }

func (h *Handler) oauthConnections(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	user, version, ok := h.oauthBrowserUser(w, r)
	if !ok {
		return
	}
	if r.Method == "POST" {
		if !oauthForm(w, r) {
			return
		}
		if _, ok := h.oauthCheckCSRF(r, "oauth-connections", user, version); !ok {
			oauthError(w, 403, "invalid_request")
			return
		}
		if _, err := h.db.Exec(`UPDATE oauth_grants SET revoked=1 WHERE id=? AND user_id=?`, r.PostForm.Get("id"), user); err != nil {
			oauthError(w, 500, "server_error")
			return
		}
		http.Redirect(w, r, "/finance/settings/connections", 303)
		return
	}
	rows, err := h.db.Query(`SELECT g.id,c.name,g.scope,g.created_at FROM oauth_grants g JOIN oauth_clients c ON c.id=g.client_id WHERE g.user_id=? AND g.session_version=? AND g.revoked=0 AND g.expires_at>? ORDER BY g.created_at DESC`, user, version, time.Now().Unix())
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	var items []oauthConnection
	for rows.Next() {
		var item oauthConnection
		var created int64
		if err = rows.Scan(&item.ID, &item.Name, &item.Scope, &created); err != nil {
			break
		}
		item.Created = time.Unix(created, 0).UTC().Format("2006-01-02")
		if strings.Contains(item.Scope, oauthWrite) {
			item.Scope = "Чтение и запись"
		} else {
			item.Scope = "Только чтение"
		}
		items = append(items, item)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil || rowErr != nil {
		oauthError(w, 500, "server_error")
		return
	}
	csrf, err := h.oauthCSRF(w, r, "oauth-connections", user, version, "")
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	data := h.pageData(user, "settings")
	data["Title"] = "Подключённые приложения"
	data["Connections"] = items
	data["CSRF"] = csrf
	data["MCPURL"] = h.oauth.issuer + "/mcp"
	h.renderTemplate(w, "oauth_connections.html", data)
}
