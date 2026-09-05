package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// APIToken — токен для программного доступа к API и MCP.
type APIToken struct {
	ID         int64
	Name       string
	Prefix     string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

// generateAPIToken создает новый токен вида finforme_<32 hex символа>.
func generateAPIToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "finforme_" + hex.EncodeToString(buf), nil
}

// hashAPIToken возвращает SHA-256 хеш токена в hex.
// В базе хранится только хеш — сам токен показывается один раз при создании.
func hashAPIToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// userIDFromBearer проверяет заголовок Authorization: Bearer <token>
// и возвращает ID владельца токена.
func (h *Handler) userIDFromBearer(r *http.Request) (int64, bool) {
	auth := r.Header.Get("Authorization")
	const scheme = "Bearer "
	if !strings.HasPrefix(auth, scheme) {
		return 0, false
	}
	token := strings.TrimSpace(auth[len(scheme):])
	if token == "" {
		return 0, false
	}

	var tokenID, userID int64
	err := h.db.QueryRow(`
		SELECT t.id, t.user_id
		FROM api_tokens t
		JOIN users u ON u.id = t.user_id
		WHERE t.token_hash = ? AND u.is_active = 1 AND u.password_change_required = 0
	`, hashAPIToken(token)).Scan(&tokenID, &userID)
	if err != nil {
		return 0, false
	}

	// Отмечаем использование не чаще раза в минуту, чтобы не спамить UPDATE-ами
	h.db.Exec(`
		UPDATE api_tokens SET last_used_at = NOW()
		WHERE id = ? AND (last_used_at IS NULL OR last_used_at < NOW() - INTERVAL 1 MINUTE)
	`, tokenID)

	return userID, true
}

// getAPITokens возвращает токены пользователя.
func (h *Handler) getAPITokens(userID int64) ([]APIToken, error) {
	rows, err := h.db.Query(`
		SELECT id, name, prefix, created_at, last_used_at
		FROM api_tokens WHERE user_id = ? ORDER BY id
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tokens := make([]APIToken, 0)
	for rows.Next() {
		var t APIToken
		var lastUsed *time.Time
		if err := rows.Scan(&t.ID, &t.Name, &t.Prefix, &t.CreatedAt, &lastUsed); err != nil {
			continue
		}
		t.LastUsedAt = lastUsed
		tokens = append(tokens, t)
	}
	return tokens, nil
}

// renderAPITokensSection рендерит фрагмент секции токенов для htmx.
func (h *Handler) renderAPITokensSection(w http.ResponseWriter, userID int64, newToken string, errMsg string) {
	tokens, err := h.getAPITokens(userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderTemplate(w, "api_tokens_section", map[string]interface{}{
		"Tokens":   tokens,
		"NewToken": newToken,
		"Error":    errMsg,
	})
}

// APITokenCreate — создание нового API-токена (из настроек, htmx).
func (h *Handler) APITokenCreate(w http.ResponseWriter, r *http.Request) {
	userID, _ := h.getUserID(r)

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("token_name"))
	if name == "" {
		name = "API token"
	}

	token, err := generateAPIToken()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// prefix — первые символы токена для узнаваемости в списке
	prefix := token[:14] + "…"
	_, err = h.db.Exec(`
		INSERT INTO api_tokens (user_id, name, token_hash, prefix)
		VALUES (?, ?, ?, ?)
	`, userID, name, hashAPIToken(token), prefix)
	if err != nil {
		h.renderAPITokensSection(w, userID, "", "Не удалось создать токен: "+err.Error())
		return
	}

	h.renderAPITokensSection(w, userID, token, "")
}

// APITokenDelete — отзыв токена (из настроек, htmx).
func (h *Handler) APITokenDelete(w http.ResponseWriter, r *http.Request) {
	userID, _ := h.getUserID(r)

	tokenID, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid token ID", http.StatusBadRequest)
		return
	}

	if _, err := h.db.Exec(`DELETE FROM api_tokens WHERE id = ? AND user_id = ?`, tokenID, userID); err != nil {
		h.renderAPITokensSection(w, userID, "", "Не удалось удалить токен: "+err.Error())
		return
	}

	h.renderAPITokensSection(w, userID, "", "")
}
