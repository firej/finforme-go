package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
)

// NotFound renders a common page without querying financial data.
func (h *Handler) NotFound(w http.ResponseWriter, r *http.Request) {
	var body strings.Builder
	data := map[string]interface{}{"Title": "Страница не найдена", "Authenticated": false, "ActivePage": ""}
	if err := h.templates.ExecuteTemplate(&body, "not_found.html", data); err != nil {
		http.Error(w, "Страница не найдена. Вернуться: /finance/", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	w.Write([]byte(body.String()))
}

func (h *Handler) requirePageAccount(w http.ResponseWriter, r *http.Request, userID, id int64) bool {
	var found int64
	err := h.db.QueryRow(`SELECT id FROM accounts WHERE id=? AND user_id=?`, id, userID).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		h.NotFound(w, r)
		return false
	}
	if err != nil {
		http.Error(w, "Не удалось загрузить счёт", http.StatusInternalServerError)
		return false
	}
	return true
}
