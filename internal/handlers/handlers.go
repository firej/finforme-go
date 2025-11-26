package handlers

import (
	"database/sql"
	"html/template"
	"net/http"
	"path/filepath"

	"github.com/gorilla/sessions"
)

// Handler содержит зависимости для обработчиков
type Handler struct {
	db        *sql.DB
	store     *sessions.CookieStore
	templates *template.Template
}

// New создает новый экземпляр Handler
func New(db *sql.DB, store *sessions.CookieStore) *Handler {
	// Создаем функции для шаблонов
	funcMap := template.FuncMap{
		"dict": func(values ...interface{}) map[string]interface{} {
			if len(values)%2 != 0 {
				return nil
			}
			dict := make(map[string]interface{}, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				key, ok := values[i].(string)
				if !ok {
					return nil
				}
				dict[key] = values[i+1]
			}
			return dict
		},
		"add": func(a, b int) int {
			return a + b
		},
		"mul": func(a, b int) int {
			return a * b
		},
		"iterate": func(count int) []int {
			result := make([]int, count)
			for i := 0; i < count; i++ {
				result[i] = i
			}
			return result
		},
	}

	// Загружаем все шаблоны с функциями
	templates := template.Must(template.New("").Funcs(funcMap).ParseGlob(filepath.Join("templates", "*.html")))

	return &Handler{
		db:        db,
		store:     store,
		templates: templates,
	}
}

// getUserID получает ID пользователя из сессии
func (h *Handler) getUserID(r *http.Request) (int64, bool) {
	session, err := h.store.Get(r, "session")
	if err != nil {
		return 0, false
	}

	userID, ok := session.Values["user_id"].(int64)
	if !ok {
		return 0, false
	}

	return userID, true
}

// setUserID устанавливает ID пользователя в сессию
func (h *Handler) setUserID(w http.ResponseWriter, r *http.Request, userID int64) error {
	session, err := h.store.Get(r, "session")
	if err != nil {
		return err
	}

	session.Values["user_id"] = userID
	return session.Save(r, w)
}

// clearSession очищает сессию
func (h *Handler) clearSession(w http.ResponseWriter, r *http.Request) error {
	session, err := h.store.Get(r, "session")
	if err != nil {
		return err
	}

	session.Values = make(map[interface{}]interface{})
	session.Options.MaxAge = -1
	return session.Save(r, w)
}

// RequireAuth - middleware для проверки аутентификации
func (h *Handler) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := h.getUserID(r); !ok {
			http.Redirect(w, r, "/accounts/login/?next="+r.URL.Path, http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

// RequireAuthMiddleware - middleware для API
func (h *Handler) RequireAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := h.getUserID(r); !ok {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// renderTemplate рендерит шаблон с данными
func (h *Handler) renderTemplate(w http.ResponseWriter, name string, data interface{}) {
	err := h.templates.ExecuteTemplate(w, name, data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
