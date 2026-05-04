package handlers

import (
	"database/sql"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/evbogdanov/finforme/internal/models"
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
		"formatMoney": func(value float64) string {
			if math.Abs(value) < 0.005 {
				return "0.00"
			}
			return fmt.Sprintf("%.2f", value)
		},
		"derefInt64": func(ptr *int64) int64 {
			if ptr != nil {
				return *ptr
			}
			return 0
		},
		"eqStr": func(a, b string) bool {
			return a == b
		},
		"upper": strings.ToUpper,
		"slice": func(s string, i, j int) string {
			r := []rune(s)
			if i < 0 {
				i = 0
			}
			if j > len(r) {
				j = len(r)
			}
			if i >= j {
				return ""
			}
			return string(r[i:j])
		},
		"formatMoneyShort": func(value float64) string {
			abs := math.Abs(value)
			sign := ""
			if value < 0 {
				sign = "-"
			}
			switch {
			case abs >= 1_000_000:
				return fmt.Sprintf("%s%.1fM", sign, abs/1_000_000)
			case abs >= 1_000:
				return fmt.Sprintf("%s%.1fK", sign, abs/1_000)
			default:
				return fmt.Sprintf("%s%.0f", sign, abs)
			}
		},
		"formatDateGroup": func(dateStr string) string {
			// dateStr ожидается в формате "2006-01-02"
			t, err := time.Parse("2006-01-02", dateStr)
			if err != nil {
				return dateStr
			}
			now := time.Now()
			today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
			d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, now.Location())
			switch {
			case d.Equal(today):
				return "Сегодня"
			case d.Equal(today.AddDate(0, 0, -1)):
				return "Вчера"
			default:
				months := []string{
					"января", "февраля", "марта", "апреля", "мая", "июня",
					"июля", "августа", "сентября", "октября", "ноября", "декабря",
				}
				if t.Year() == now.Year() {
					return fmt.Sprintf("%d %s", t.Day(), months[t.Month()-1])
				}
				return fmt.Sprintf("%d %s %d", t.Day(), months[t.Month()-1], t.Year())
			}
		},
	}

	// Загружаем все шаблоны с функциями
	templates := template.Must(template.New("").Funcs(funcMap).ParseGlob("templates/*.html"))

	return &Handler{
		db:        db,
		store:     store,
		templates: templates,
	}
}

// getIsAdmin проверяет флаг is_admin для пользователя по ID
func (h *Handler) getIsAdmin(userID int64) bool {
	var isAdmin bool
	err := h.db.QueryRow("SELECT is_admin FROM users WHERE id = ?", userID).Scan(&isAdmin)
	if err != nil {
		return false
	}
	return isAdmin
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

// pageData возвращает базовые данные для страниц: user, accountTree, activePage
func (h *Handler) pageData(userID int64, activePage string) map[string]interface{} {
	data := map[string]interface{}{
		"Authenticated": true,
		"IsAdmin":       h.getIsAdmin(userID),
		"ActivePage":    activePage,
	}

	// Пользователь
	var user models.User
	err := h.db.QueryRow(
		`SELECT id, username, email, first_name, last_name FROM users WHERE id = ?`, userID,
	).Scan(&user.ID, &user.Username, &user.Email, &user.FirstName, &user.LastName)
	if err == nil {
		data["User"] = user
	}

	// Дерево счетов для сайдбара (с балансами)
	sidebarAccounts, err := h.getAccountsWithBalance(userID)
	if err == nil && len(sidebarAccounts) > 0 {
		accountsMap := make(map[int64]*models.Account, len(sidebarAccounts))
		for _, acc := range sidebarAccounts {
			accountsMap[acc.ID] = acc
		}
		data["AccountTree"] = h.buildAccountTree(sidebarAccounts, accountsMap)
	}

	return data
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

// renderTemplate рендерит шаблон с данными через буфер,
// чтобы при ошибке можно было вернуть 500 не после начала записи тела.
func (h *Handler) renderTemplate(w http.ResponseWriter, name string, data interface{}) {
	var buf strings.Builder
	err := h.templates.ExecuteTemplate(&buf, name, data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, buf.String())
}
