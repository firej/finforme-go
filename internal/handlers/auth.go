package handlers

import (
	"database/sql"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/evbogdanov/finforme/internal/models"
	"golang.org/x/crypto/bcrypt"
)

// safeNextURL возвращает true, если next — внутренний путь сайта
// (защита от open redirect на внешние домены после логина)
func safeNextURL(next string) bool {
	return strings.HasPrefix(next, "/") && !strings.HasPrefix(next, "//") && !strings.HasPrefix(next, "/\\")
}

// Index - главная страница
func (h *Handler) Index(w http.ResponseWriter, r *http.Request) {
	userID, authenticated := h.getUserID(r)
	if !authenticated {
		data := map[string]interface{}{
			"Title": "Добро пожаловать",
			"News":  h.latestNews(),
		}
		h.renderTemplate(w, "welcome.html", data)
		return
	}
	h.renderDashboard(w, r, userID)
}

// Login - страница входа
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		next := r.URL.Query().Get("next")
		data := map[string]interface{}{
			"Title": "Вход",
			"Next":  next,
		}
		h.renderTemplate(w, "login.html", data)
		return
	}

	// POST
	username := r.FormValue("username")
	password := r.FormValue("password")
	next := r.FormValue("next")

	var id, version int64
	var hash string
	var active, required bool
	var expires sql.NullTime
	err := h.db.QueryRow(`SELECT id, password_hash, is_active, session_version,
 password_change_required, password_expires_at FROM users WHERE username = ?`, username).
		Scan(&id, &hash, &active, &version, &required, &expires)
	invalid := func() {
		h.renderTemplate(w, "login.html", map[string]interface{}{"Title": "Вход", "Next": next,
			"Error": "Неверный логин или пароль. Временный пароль мог истечь или уже использован."})
	}
	if err != nil || !active || (required && (!expires.Valid || !time.Now().Before(expires.Time))) ||
		bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		invalid()
		return
	}
	if required {
		// Consume the temporary credential atomically. Only the newly issued restricted
		// session can now finish recovery; a second login with this password fails.
		res, err := h.db.Exec(`UPDATE users SET password_hash = '', session_version = session_version + 1
   WHERE id = ? AND session_version = ? AND password_hash = ? AND is_active = 1
   AND password_change_required = 1 AND password_expires_at > ?`, id, version, hash, time.Now().UTC())
		if err != nil {
			http.Error(w, "Internal server error", 500)
			return
		}
		n, err := res.RowsAffected()
		if err != nil || n != 1 {
			invalid()
			return
		}
		version++
	}
	if err := h.writeSession(w, r, id, version); err != nil {
		http.Error(w, "Internal server error", 500)
		return
	}
	if required {
		http.Redirect(w, r, "/accounts/password_change/", http.StatusSeeOther)
		return
	}

	if safeNextURL(next) {
		http.Redirect(w, r, next, http.StatusSeeOther)
	} else {
		http.Redirect(w, r, "/accounts/info/", http.StatusSeeOther)
	}
}

// Logout - выход
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	h.clearSession(w, r)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// Register - регистрация
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		data := map[string]interface{}{
			"Title": "Регистрация",
		}
		h.renderTemplate(w, "account_register.html", data)
		return
	}

	// POST
	username := r.FormValue("username")
	password := r.FormValue("password")
	email := r.FormValue("email")
	firstName := r.FormValue("firstname")
	lastName := r.FormValue("lastname")

	// Проверка обязательных полей
	if username == "" || password == "" || email == "" {
		data := map[string]interface{}{
			"Title": "Регистрация",
			"Error": "Заполните все обязательные поля",
		}
		h.renderTemplate(w, "account_register.html", data)
		return
	}

	// Проверка существования пользователя
	var exists bool
	err := h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM users WHERE username = ?)", username).Scan(&exists)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if exists {
		data := map[string]interface{}{
			"Title": "Регистрация",
			"Error": "Пользователь с таким логином уже существует",
		}
		h.renderTemplate(w, "account_register.html", data)
		return
	}

	// Хеширование пароля
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Создание пользователя
	result, err := h.db.Exec(`
		INSERT INTO users (username, email, password_hash, first_name, last_name, is_active)
		VALUES (?, ?, ?, ?, ?, 1)
	`, username, email, string(hashedPassword), firstName, lastName)

	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	userID, _ := result.LastInsertId()

	// Автоматический вход
	if err := h.setUserID(w, r, userID); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/accounts/info/", http.StatusSeeOther)
}

// AccountInfo - информация об аккаунте
func (h *Handler) AccountInfo(w http.ResponseWriter, r *http.Request) {
	userID, _ := h.getUserID(r)

	var user models.User
	err := h.db.QueryRow(`
		SELECT id, username, email, first_name, last_name
		FROM users WHERE id = ?
	`, userID).Scan(&user.ID, &user.Username, &user.Email, &user.FirstName, &user.LastName)

	if err != nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	data := h.pageData(userID, "")
	data["Title"] = "Информация об аккаунте"
	data["User"] = user
	h.renderTemplate(w, "account_info.html", data)
}

// PasswordChange - смена пароля
func (h *Handler) PasswordChange(w http.ResponseWriter, r *http.Request) {
	// Password changes require a browser session, including the restricted recovery session.
	userID, version, required, ok := h.sessionUser(r)
	if !ok {
		h.redirectToLogin(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	data := map[string]interface{}{"Title": "Смена пароля", "Required": required}
	if !required {
		data = h.pageData(userID, "")
		data["Title"] = "Смена пароля"
	}
	if r.Method == http.MethodGet {
		h.renderTemplate(w, "password_change_form.html", data)
		return
	}
	newPassword := r.FormValue("new_password")
	if utf8.RuneCountInString(newPassword) < 8 || len(newPassword) > 72 {
		data["Error"] = "Введите не меньше 8 символов. Если пароль очень длинный, сократите его."
		h.renderTemplate(w, "password_change_form.html", data)
		return
	}
	if !required {
		var hash string
		err := h.db.QueryRow(`SELECT password_hash FROM users WHERE id = ? AND session_version = ?`, userID, version).Scan(&hash)
		if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(r.FormValue("old_password"))) != nil {
			data["Error"] = "Неверный текущий пароль"
			h.renderTemplate(w, "password_change_form.html", data)
			return
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "Internal server error", 500)
		return
	}
	res, err := h.db.Exec(`UPDATE users SET password_hash = ?, session_version = session_version + 1,
  password_change_required = 0, password_expires_at = NULL
  WHERE id = ? AND session_version = ? AND is_active = 1
  AND (password_change_required = 0 OR password_expires_at > ?)`, string(hash), userID, version, time.Now().UTC())
	if err != nil {
		http.Error(w, "Internal server error", 500)
		return
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		http.Error(w, "Сессия истекла. Войдите заново.", http.StatusUnauthorized)
		return
	}
	if err := h.writeSession(w, r, userID, version+1); err != nil {
		http.Error(w, "Internal server error", 500)
		return
	}
	http.Redirect(w, r, "/accounts/info/", http.StatusSeeOther)
}

// ChangeInfo - изменение информации об аккаунте
func (h *Handler) ChangeInfo(w http.ResponseWriter, r *http.Request) {
	userID, _ := h.getUserID(r)

	if r.Method == "GET" {
		var user models.User
		err := h.db.QueryRow(`
			SELECT id, username, email, first_name, last_name
			FROM users WHERE id = ?
		`, userID).Scan(&user.ID, &user.Username, &user.Email, &user.FirstName, &user.LastName)

		if err != nil {
			http.Error(w, "User not found", http.StatusNotFound)
			return
		}

		data := h.pageData(userID, "")
		data["Title"] = "Изменение информации"
		data["User"] = user
		h.renderTemplate(w, "change_info_form.html", data)
		return
	}

	// POST
	email := r.FormValue("email")
	firstName := r.FormValue("first_name")
	lastName := r.FormValue("last_name")

	_, err := h.db.Exec(`
		UPDATE users SET email = ?, first_name = ?, last_name = ?
		WHERE id = ?
	`, email, firstName, lastName, userID)

	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/accounts/info/", http.StatusSeeOther)
}
