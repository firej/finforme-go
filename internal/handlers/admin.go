package handlers

import (
	"net/http"
	"strconv"

	"github.com/evbogdanov/finforme/internal/models"
	"github.com/gorilla/mux"
	"golang.org/x/crypto/bcrypt"
)

// RequireAdmin — middleware для проверки прав администратора
func (h *Handler) RequireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := h.getUserID(r); !ok {
			http.Redirect(w, r, "/accounts/login/?next="+r.URL.Path, http.StatusSeeOther)
			return
		}
		userID, _ := h.getUserID(r)
		if !h.getIsAdmin(userID) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// adminPageData формирует общие данные для страниц админки
func (h *Handler) adminPageData(r *http.Request, title string) map[string]interface{} {
	userID, _ := h.getUserID(r)
	var user models.User
	h.db.QueryRow(`SELECT id, username FROM users WHERE id = ?`, userID).
		Scan(&user.ID, &user.Username)

	data := map[string]interface{}{
		"Title":         title,
		"Authenticated": true,
		"IsAdmin":       true,
		"User":          user,
	}

	// Передаём сообщения из query-параметров
	if msg := r.URL.Query().Get("msg"); msg != "" {
		data["Msg"] = msg
	}
	if errMsg := r.URL.Query().Get("error"); errMsg != "" {
		data["Error"] = errMsg
	}

	return data
}

// AdminIndex — главная страница админки (редирект на пользователей)
func (h *Handler) AdminIndex(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/admin/users/", http.StatusSeeOther)
}

// AdminUsers — вкладка управления пользователями
func (h *Handler) AdminUsers(w http.ResponseWriter, r *http.Request) {
	data := h.adminPageData(r, "Админка — Пользователи")
	data["Tab"] = "users"

	rows, err := h.db.Query(`
		SELECT id, username, email, first_name, last_name, is_active, is_admin, created_at
		FROM users
		ORDER BY id ASC
	`)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var users []models.User
	for rows.Next() {
		var u models.User
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.FirstName, &u.LastName,
			&u.IsActive, &u.IsAdmin, &u.CreatedAt); err != nil {
			continue
		}
		users = append(users, u)
	}
	data["Users"] = users

	h.renderTemplate(w, "admin.html", data)
}

// AdminUserRequestPasswordChange — запросить смену пароля (сбросить пароль на временный)
func (h *Handler) AdminUserRequestPasswordChange(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID, err := strconv.ParseInt(vars["id"], 10, 64)
	if err != nil {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	// Генерируем временный пароль
	tempPassword := "ChangeMe123!"
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(tempPassword), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	_, err = h.db.Exec("UPDATE users SET password_hash = ? WHERE id = ?", string(hashedPassword), userID)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/users/?msg=password_reset&uid="+vars["id"], http.StatusSeeOther)
}

// AdminUserDelete — удалить пользователя
func (h *Handler) AdminUserDelete(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID, err := strconv.ParseInt(vars["id"], 10, 64)
	if err != nil {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	// Нельзя удалить самого себя
	currentUserID, _ := h.getUserID(r)
	if userID == currentUserID {
		http.Redirect(w, r, "/admin/users/?error=self_delete", http.StatusSeeOther)
		return
	}

	_, err = h.db.Exec("DELETE FROM users WHERE id = ?", userID)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/users/?msg=deleted", http.StatusSeeOther)
}

// AdminRates — вкладка управления курсами валют
func (h *Handler) AdminRates(w http.ResponseWriter, r *http.Request) {
	data := h.adminPageData(r, "Админка — Курсы валют")
	data["Tab"] = "rates"

	// Параметры фильтрации
	filterCode := r.URL.Query().Get("code")
	filterSource := r.URL.Query().Get("source")
	data["FilterCode"] = filterCode
	data["FilterSource"] = filterSource

	query := `
		SELECT code, name, CAST(rate AS DOUBLE), source,
		       DATE_FORMAT(rate_date, '%Y-%m-%d'),
		       DATE_FORMAT(created_at, '%d.%m.%Y %H:%i')
		FROM currency_rates
		WHERE 1=1
	`
	args := []interface{}{}
	if filterCode != "" {
		query += " AND code = ?"
		args = append(args, filterCode)
	}
	if filterSource != "" {
		query += " AND source = ?"
		args = append(args, filterSource)
	}
	query += " ORDER BY rate_date DESC, code ASC, source ASC LIMIT 500"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var rates []models.CurrencyRate
	for rows.Next() {
		var cr models.CurrencyRate
		if err := rows.Scan(&cr.Code, &cr.Name, &cr.Rate, &cr.Source, &cr.RateDate, &cr.CreatedAt); err != nil {
			continue
		}
		rates = append(rates, cr)
	}
	data["Rates"] = rates

	// Список уникальных кодов и источников для фильтров
	codeRows, _ := h.db.Query("SELECT DISTINCT code FROM currency_rates ORDER BY code")
	var codes []string
	if codeRows != nil {
		defer codeRows.Close()
		for codeRows.Next() {
			var c string
			codeRows.Scan(&c)
			codes = append(codes, c)
		}
	}
	data["Codes"] = codes

	sourceRows, _ := h.db.Query("SELECT DISTINCT source FROM currency_rates ORDER BY source")
	var sources []string
	if sourceRows != nil {
		defer sourceRows.Close()
		for sourceRows.Next() {
			var s string
			sourceRows.Scan(&s)
			sources = append(sources, s)
		}
	}
	data["Sources"] = sources

	h.renderTemplate(w, "admin.html", data)
}

// AdminRateDelete — удалить запись курса валюты
func (h *Handler) AdminRateDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	code := r.FormValue("code")
	source := r.FormValue("source")
	rateDate := r.FormValue("rate_date")

	if code == "" || source == "" || rateDate == "" {
		http.Error(w, "Missing parameters", http.StatusBadRequest)
		return
	}

	_, err := h.db.Exec(
		"DELETE FROM currency_rates WHERE code = ? AND source = ? AND rate_date = ?",
		code, source, rateDate,
	)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Возвращаемся с теми же фильтрами
	redirect := "/admin/rates/?msg=deleted"
	if code != "" {
		redirect += "&code=" + code
	}
	if source != "" {
		redirect += "&source=" + source
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

// AdminRateDeleteBulk — удалить все записи по коду и/или источнику
func (h *Handler) AdminRateDeleteBulk(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	code := r.FormValue("code")
	source := r.FormValue("source")

	if code == "" && source == "" {
		http.Redirect(w, r, "/admin/rates/?error=empty_filter", http.StatusSeeOther)
		return
	}

	query := "DELETE FROM currency_rates WHERE 1=1"
	args := []interface{}{}
	if code != "" {
		query += " AND code = ?"
		args = append(args, code)
	}
	if source != "" {
		query += " AND source = ?"
		args = append(args, source)
	}

	_, err := h.db.Exec(query, args...)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/rates/?msg=bulk_deleted", http.StatusSeeOther)
}

// AdminRateEdit — редактировать запись курса валюты (GET — форма, POST — сохранение)
func (h *Handler) AdminRateEdit(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	source := r.URL.Query().Get("source")
	rateDate := r.URL.Query().Get("rate_date")

	if r.Method == "POST" {
		code = r.FormValue("code")
		source = r.FormValue("source")
		rateDate = r.FormValue("rate_date")
		newRate := r.FormValue("rate")
		newName := r.FormValue("name")

		if code == "" || source == "" || rateDate == "" || newRate == "" {
			http.Error(w, "Missing parameters", http.StatusBadRequest)
			return
		}

		rateVal, err := strconv.ParseFloat(newRate, 64)
		if err != nil {
			http.Error(w, "Invalid rate value", http.StatusBadRequest)
			return
		}

		_, err = h.db.Exec(
			"UPDATE currency_rates SET rate = ?, name = ? WHERE code = ? AND source = ? AND rate_date = ?",
			rateVal, newName, code, source, rateDate,
		)
		if err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, "/admin/rates/?msg=updated&code="+code+"&source="+source, http.StatusSeeOther)
		return
	}

	// GET — показываем форму редактирования
	data := h.adminPageData(r, "Редактировать курс")
	data["Tab"] = "rates"

	if code == "" || source == "" || rateDate == "" {
		http.Error(w, "Missing parameters", http.StatusBadRequest)
		return
	}

	var cr models.CurrencyRate
	err := h.db.QueryRow(
		`SELECT code, name, CAST(rate AS DOUBLE), source, DATE_FORMAT(rate_date, '%Y-%m-%d')
		 FROM currency_rates WHERE code = ? AND source = ? AND rate_date = ?`,
		code, source, rateDate,
	).Scan(&cr.Code, &cr.Name, &cr.Rate, &cr.Source, &cr.RateDate)
	if err != nil {
		http.Error(w, "Record not found", http.StatusNotFound)
		return
	}

	data["EditRate"] = cr
	h.renderTemplate(w, "admin.html", data)
}
