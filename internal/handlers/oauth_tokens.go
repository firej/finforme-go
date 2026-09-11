package handlers

import (
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"net/http"
	"strings"
	"time"
)

func validPKCEChallenge(s string) bool {
	b, err := base64.RawURLEncoding.DecodeString(s)
	return err == nil && len(b) == 32 && base64.RawURLEncoding.EncodeToString(b) == s
}

func verifyPKCE(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	for _, c := range verifier {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~", c)) {
			return false
		}
	}
	sum := sha256.Sum256([]byte(verifier))
	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) == 1
}

type oauthGrant struct {
	ID, ClientID, Scope, Resource string
	UserID                        int64
}

func loadOAuthGrant(tx *sql.Tx, id string, now int64) (oauthGrant, error) {
	var g oauthGrant
	err := tx.QueryRow(`SELECT g.id,g.user_id,g.client_id,g.scope,g.resource FROM oauth_grants g JOIN users u ON u.id=g.user_id
	 WHERE g.id=? AND g.revoked=0 AND g.expires_at>? AND u.is_active=1 AND u.password_change_required=0 AND u.session_version=g.session_version`, id, now).
		Scan(&g.ID, &g.UserID, &g.ClientID, &g.Scope, &g.Resource)
	return g, err
}

func (h *Handler) oauthToken(w http.ResponseWriter, r *http.Request) {
	if !oauthForm(w, r) {
		return
	}
	f := r.PostForm
	if r.Header.Get("Authorization") != "" || f.Get("client_secret") != "" {
		oauthError(w, 400, "invalid_client")
		return
	}
	kind := f.Get("grant_type")
	if kind != "authorization_code" && kind != "refresh_token" {
		oauthError(w, 400, "unsupported_grant_type")
		return
	}
	client, err := h.oauthClient(f.Get("client_id"))
	if err != nil {
		oauthError(w, 400, "invalid_client")
		return
	}
	secret := f.Get("code")
	table := "oauth_codes"
	if kind == "refresh_token" {
		secret = f.Get("refresh_token")
		table = "oauth_tokens"
	}
	if secret == "" {
		oauthError(w, 400, "invalid_grant")
		return
	}
	hash := hashAPIToken(secret)
	// table is a fixed internal identifier, not supplied by the request.
	var user int64
	var grantID string
	err = h.db.QueryRow(`SELECT g.user_id,g.id FROM `+table+` t JOIN oauth_grants g ON g.id=t.grant_id WHERE t.hash=?`, hash).Scan(&user, &grantID)
	if err != nil {
		oauthError(w, 400, "invalid_grant")
		return
	}
	tx, err := h.beginFinanceWrite(user)
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	g, err := loadOAuthGrant(tx, grantID, now)
	if err != nil || g.ClientID != client.ID || g.Resource != h.oauth.issuer+"/mcp" {
		oauthError(w, 400, "invalid_grant")
		return
	}
	resource := f.Get("resource")
	if (kind == "authorization_code" && resource != g.Resource) || (resource != "" && resource != g.Resource) {
		oauthError(w, 400, "invalid_target")
		return
	}
	var used int
	var expires int64
	if kind == "authorization_code" {
		var redirect, challenge string
		err = tx.QueryRow(`SELECT redirect_uri,challenge,expires_at,used FROM oauth_codes WHERE hash=?`, hash).Scan(&redirect, &challenge, &expires, &used)
		if err != nil || redirect != f.Get("redirect_uri") || !verifyPKCE(f.Get("code_verifier"), challenge) {
			oauthError(w, 400, "invalid_grant")
			return
		}
	} else {
		var tokenKind string
		err = tx.QueryRow(`SELECT kind,expires_at,used FROM oauth_tokens WHERE hash=?`, hash).Scan(&tokenKind, &expires, &used)
		if err != nil || tokenKind != "refresh" {
			oauthError(w, 400, "invalid_grant")
			return
		}
	}
	if used != 0 {
		// A valid code/refresh token was replayed. Invalidate the entire family.
		if _, err = tx.Exec(`UPDATE oauth_grants SET revoked=1 WHERE id=?`, g.ID); err == nil {
			err = tx.Commit()
		}
		if err != nil {
			oauthError(w, 500, "server_error")
			return
		}
		oauthError(w, 400, "invalid_grant")
		return
	}
	if expires <= now {
		oauthError(w, 400, "invalid_grant")
		return
	}
	if scope := f.Get("scope"); scope != "" {
		normalized, ok := oauthScopes(scope)
		if !ok || (strings.Contains(normalized, oauthWrite) && !strings.Contains(g.Scope, oauthWrite)) {
			oauthError(w, 400, "invalid_scope")
			return
		}
		g.Scope = normalized
		if _, err = tx.Exec(`UPDATE oauth_grants SET scope=? WHERE id=?`, g.Scope, g.ID); err != nil {
			oauthError(w, 500, "server_error")
			return
		}
	}
	result, err := tx.Exec(`UPDATE `+table+` SET used=1 WHERE hash=? AND used=0`, hash)
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		oauthError(w, 400, "invalid_grant")
		return
	}
	access, err := oauthSecret("ffoa_")
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	refresh, err := oauthSecret("ffor_")
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	for _, item := range []struct {
		token, kind string
		lifetime    int64
	}{{access, "access", 3600}, {refresh, "refresh", 30 * 86400}} {
		if _, err = tx.Exec(`INSERT INTO oauth_tokens(hash,grant_id,kind,expires_at,used) VALUES(?,?,?,?,0)`, hashAPIToken(item.token), g.ID, item.kind, now+item.lifetime); err != nil {
			oauthError(w, 500, "server_error")
			return
		}
	}
	if err = tx.Commit(); err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	oauthJSON(w, 200, map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": 3600, "refresh_token": refresh, "scope": g.Scope})
}

func (h *Handler) oauthBearer(r *http.Request) (int64, string, bool) {
	if h.oauth == nil {
		return 0, "", false
	}
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || !strings.HasPrefix(parts[1], "ffoa_") {
		return 0, "", false
	}
	var user int64
	var scope string
	now := time.Now().Unix()
	err := h.db.QueryRow(`SELECT g.user_id,g.scope FROM oauth_tokens t JOIN oauth_grants g ON g.id=t.grant_id JOIN users u ON u.id=g.user_id
	 WHERE t.hash=? AND t.kind='access' AND t.used=0 AND t.expires_at>? AND g.expires_at>? AND g.revoked=0 AND g.resource=?
	 AND u.is_active=1 AND u.password_change_required=0 AND u.session_version=g.session_version`, hashAPIToken(parts[1]), now, now, h.oauth.issuer+"/mcp").Scan(&user, &scope)
	return user, scope, err == nil
}

func (h *Handler) oauthRevoke(w http.ResponseWriter, r *http.Request) {
	if !oauthForm(w, r) {
		return
	}
	// Possession of a token allows revocation, but never grants access to data.
	_, err := h.db.Exec(`UPDATE oauth_grants SET revoked=1 WHERE client_id=? AND id IN (SELECT grant_id FROM oauth_tokens WHERE hash=?)`, r.PostForm.Get("client_id"), hashAPIToken(r.PostForm.Get("token")))
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	oauthJSON(w, 200, map[string]any{})
}
