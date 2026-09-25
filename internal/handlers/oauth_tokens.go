package handlers

import (
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// oauthAudit deliberately excludes request headers, bodies, tokens and token hashes.
func oauthAudit(event, reason, kind, clientID, grantID string) {
	slog.Info("oauth", "event", event, "reason", reason, "grant_type", kind, "client_id", clientID, "grant_id", grantID)
}

func oauthGrantFailure(tx *sql.Tx, id string, now int64) string {
	var revoked, active, passwordChange int
	var expires, version, currentVersion int64
	err := tx.QueryRow(`SELECT g.revoked,g.expires_at,g.session_version,u.session_version,u.is_active,u.password_change_required
 FROM oauth_grants g JOIN users u ON u.id=g.user_id WHERE g.id=?`, id).
		Scan(&revoked, &expires, &version, &currentVersion, &active, &passwordChange)
	if err == sql.ErrNoRows {
		return "grant_not_found"
	}
	if err != nil {
		return "grant_lookup_failed"
	}
	switch {
	case revoked != 0:
		return "grant_revoked"
	case expires <= now:
		return "grant_expired"
	case active != 1:
		return "user_inactive"
	case passwordChange != 0:
		return "password_change_required"
	case version != currentVersion:
		return "session_version_changed"
	default:
		return "grant_lookup_failed"
	}
}

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
	ExpiresAt                     int64
}

func loadOAuthGrant(tx *sql.Tx, id string, now int64) (oauthGrant, error) {
	var g oauthGrant
	err := tx.QueryRow(`SELECT g.id,g.user_id,g.client_id,g.scope,g.resource,g.expires_at FROM oauth_grants g JOIN users u ON u.id=g.user_id
	 WHERE g.id=? AND g.revoked=0 AND g.expires_at>? AND u.is_active=1 AND u.password_change_required=0 AND u.session_version=g.session_version`, id, now).
		Scan(&g.ID, &g.UserID, &g.ClientID, &g.Scope, &g.Resource, &g.ExpiresAt)
	return g, err
}

func (h *Handler) oauthToken(w http.ResponseWriter, r *http.Request) {
	if !oauthForm(w, r) {
		return
	}
	f := r.PostForm
	var grantID string
	deny := func(status int, code, reason string) {
		oauthAudit("token_rejected", reason, f.Get("grant_type"), f.Get("client_id"), grantID)
		oauthError(w, status, code)
	}
	if r.Header.Get("Authorization") != "" || f.Get("client_secret") != "" {
		deny(400, "invalid_client", "client_auth_not_supported")
		return
	}
	kind := f.Get("grant_type")
	if kind != "authorization_code" {
		deny(400, "unsupported_grant_type", "grant_type_not_supported")
		return
	}
	client, err := h.oauthClient(f.Get("client_id"))
	if err != nil {
		deny(400, "invalid_client", "client_not_found")
		return
	}
	secret := f.Get("code")
	if secret == "" {
		deny(400, "invalid_grant", "credential_missing")
		return
	}
	hash := hashAPIToken(secret)
	var user int64
	err = h.db.QueryRow(`SELECT g.user_id,g.id FROM oauth_codes t JOIN oauth_grants g ON g.id=t.grant_id WHERE t.hash=?`, hash).Scan(&user, &grantID)
	if err != nil {
		deny(400, "invalid_grant", "credential_lookup_failed")
		return
	}
	tx, err := h.beginFinanceWrite(user)
	if err != nil {
		deny(500, "server_error", "transaction_failed")
		return
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	g, err := loadOAuthGrant(tx, grantID, now)
	if err != nil {
		deny(400, "invalid_grant", oauthGrantFailure(tx, grantID, now))
		return
	}
	if g.ClientID != client.ID || g.Resource != h.oauth.issuer+"/mcp" {
		deny(400, "invalid_grant", "grant_binding_mismatch")
		return
	}
	resource := f.Get("resource")
	if resource != g.Resource {
		deny(400, "invalid_target", "resource_mismatch")
		return
	}
	var used int
	var expires int64
	var redirect, challenge string
	err = tx.QueryRow(`SELECT redirect_uri,challenge,expires_at,used FROM oauth_codes WHERE hash=?`, hash).Scan(&redirect, &challenge, &expires, &used)
	if err != nil || redirect != f.Get("redirect_uri") || !verifyPKCE(f.Get("code_verifier"), challenge) {
		deny(400, "invalid_grant", "code_validation_failed")
		return
	}
	if used != 0 {
		// A valid authorization code was replayed. Invalidate the entire family.
		if _, err = tx.Exec(`UPDATE oauth_grants SET revoked=1 WHERE id=?`, g.ID); err == nil {
			err = tx.Commit()
		}
		if err != nil {
			deny(500, "server_error", "replay_revocation_failed")
			return
		}
		oauthAudit("grant_revoked", "credential_reused", kind, client.ID, g.ID)
		deny(400, "invalid_grant", "credential_reused")
		return
	}
	if expires <= now {
		deny(400, "invalid_grant", "credential_expired")
		return
	}
	if scope := f.Get("scope"); scope != "" {
		normalized, ok := oauthScopes(scope)
		if !ok || (strings.Contains(normalized, oauthWrite) && !strings.Contains(g.Scope, oauthWrite)) {
			deny(400, "invalid_scope", "scope_invalid")
			return
		}
		g.Scope = normalized
		if _, err = tx.Exec(`UPDATE oauth_grants SET scope=? WHERE id=?`, g.Scope, g.ID); err != nil {
			deny(500, "server_error", "scope_update_failed")
			return
		}
	}
	result, err := tx.Exec(`UPDATE oauth_codes SET used=1 WHERE hash=? AND used=0`, hash)
	if err != nil {
		deny(500, "server_error", "credential_consume_failed")
		return
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		deny(400, "invalid_grant", "credential_already_consumed")
		return
	}
	access, err := oauthSecret("ffoa_")
	if err != nil {
		deny(500, "server_error", "access_generation_failed")
		return
	}
	if _, err = tx.Exec(`INSERT INTO oauth_tokens(hash,grant_id,kind,expires_at,used) VALUES(?,?,?,?,0)`, hashAPIToken(access), g.ID, "access", g.ExpiresAt); err != nil {
		deny(500, "server_error", "token_insert_failed")
		return
	}
	if err = tx.Commit(); err != nil {
		deny(500, "server_error", "commit_failed")
		return
	}
	oauthAudit("token_issued", "success", kind, client.ID, g.ID)
	oauthJSON(w, 200, map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": g.ExpiresAt - now, "scope": g.Scope})
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
	result, err := h.db.Exec(`UPDATE oauth_grants SET revoked=1 WHERE client_id=? AND id IN (SELECT grant_id FROM oauth_tokens WHERE hash=?)`, r.PostForm.Get("client_id"), hashAPIToken(r.PostForm.Get("token")))
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	if rows, err := result.RowsAffected(); err == nil && rows > 0 {
		oauthAudit("grant_revoked", "revocation_endpoint", "", r.PostForm.Get("client_id"), "")
	}
	oauthJSON(w, 200, map[string]any{})
}
