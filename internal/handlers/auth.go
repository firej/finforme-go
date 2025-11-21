package handlers

import (
	"net/http"

	"github.com/evbogdanov/finforme/internal/models"
	"golang.org/x/crypto/bcrypt"
)

// Index - главная страница
func (h *Handler) Index(w http.ResponseWriter, r *http.Request) {
	userID, authenticated := h.getUserID(r)

	data := map[string]interface{}{
		"Title":         "finfor.me",
		"Authenticated": authenticated,
	}

	if authenticated {
		var user models.User
		err := h.db.QueryRow(`
			SELECT id, username, email, first_name, last_name
			FROM users WHERE id = ?
		`, userID).Scan(&user.ID, &user.Username, &user.Email, &user.FirstName, &user.LastName)

		if err == nil {
			data["User"] = user
		}
	}

	h.renderTemplate(w, "index.html", data)
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

	var user models.User
	err := h.db.QueryRow(`
		SELECT id, username, email, password_hash, is_active
		FROM users WHERE username = ?
	`, username).Scan(&user.ID, &user.Username, &user.Email, &user.PasswordHash, &user.IsActive)

	if err != nil {
		data := map[string]interface{}{
			"Title": "Вход",
			"Error": "Неверный логин или пароль",
			"Next":  next,
		}
		h.renderTemplate(w, "login.html", data)
		return
	}

	if !user.IsActive {
		data := map[string]interface{}{
			"Title": "Вход",
			"Error": "Аккаунт отключен",
			"Next":  next,
		}
		h.renderTemplate(w, "login.html", data)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		data := map[string]interface{}{
			"Title": "Вход",
			"Error": "Неверный логин или пароль",
			"Next":  next,
		}
		h.renderTemplate(w, "login.html", data)
		return
	}

	// Успешная аутентификация
	if err := h.setUserID(w, r, user.ID); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if next != "" && next != "False" {
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

	data := map[string]interface{}{
		"Title":         "Информация об аккаунте",
		"User":          user,
		"Authenticated": true,
	}

	h.renderTemplate(w, "account_info.html", data)
}

// PasswordChange - смена пароля
func (h *Handler) PasswordChange(w http.ResponseWriter, r *http.Request) {
	userID, _ := h.getUserID(r)

	if r.Method == "GET" {
		data := map[string]interface{}{
			"Title":         "Смена пароля",
			"Authenticated": true,
		}
		h.renderTemplate(w, "password_change_form.html", data)
		return
	}

	// POST
	oldPassword := r.FormValue("old_password")
	newPassword := r.FormValue("new_password")

	var passwordHash string
	err := h.db.QueryRow("SELECT password_hash FROM users WHERE id = ?", userID).Scan(&passwordHash)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(oldPassword)); err != nil {
		data := map[string]interface{}{
			"Title":         "Смена пароля",
			"Error":         "Неверный текущий пароль",
			"Authenticated": true,
		}
		h.renderTemplate(w, "password_change_form.html", data)
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	_, err = h.db.Exec("UPDATE users SET password_hash = ? WHERE id = ?", string(hashedPassword), userID)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
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

		data := map[string]interface{}{
			"Title":         "Изменение информации",
			"User":          user,
			"Authenticated": true,
		}
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
