package handlers

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/evbogdanov/finforme/internal/models"
)

var commodityCodePattern = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,11}$`)

// AdminCommodities manages the shared account-currency registry, not exchange rates.
func (h *Handler) AdminCommodities(w http.ResponseWriter, r *http.Request) {
	form := models.Commodity{Fraction: 100}
	message := ""
	status := http.StatusOK
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Не удалось прочитать форму", http.StatusBadRequest)
			return
		}
		form.Mnemonic = strings.ToUpper(strings.TrimSpace(r.PostForm.Get("mnemonic")))
		form.Fullname = strings.TrimSpace(r.PostForm.Get("fullname"))
		form.Sign = strings.TrimSpace(r.PostForm.Get("sign"))
		decimals, err := strconv.Atoi(r.PostForm.Get("decimals"))
		if err != nil || decimals < 0 || decimals > 8 {
			message = "Точность должна быть от 0 до 8 знаков после запятой"
		} else {
			form.Fraction = 1
			for i := 0; i < decimals; i++ {
				form.Fraction *= 10
			}
			if err = h.createAdminCommodity(form); err != nil {
				var invalid validationError
				if !errors.As(err, &invalid) {
					http.Error(w, "Не удалось добавить валюту. Попробуйте ещё раз.", http.StatusInternalServerError)
					return
				}
				message = err.Error()
			} else {
				http.Redirect(w, r, "/admin/commodities/?msg=currency_created", http.StatusSeeOther)
				return
			}
		}
		status = http.StatusBadRequest
	}
	rows, err := h.db.Query(`SELECT id, COALESCE(namespace,'CURRENCY'), mnemonic,
		COALESCE(fullname,''), fraction, COALESCE(sign,'') FROM commodities ORDER BY mnemonic,id`)
	if err != nil {
		http.Error(w, "Не удалось загрузить валюты", http.StatusInternalServerError)
		return
	}
	var items []models.Commodity
	for rows.Next() {
		var c models.Commodity
		if err = rows.Scan(&c.ID, &c.Namespace, &c.Mnemonic, &c.Fullname, &c.Fraction, &c.Sign); err != nil {
			break
		}
		items = append(items, c)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil || rowErr != nil {
		http.Error(w, "Не удалось загрузить валюты", http.StatusInternalServerError)
		return
	}
	data := h.adminPageData(r, "Админка — Валюты")
	data["Tab"] = "commodities"
	data["Commodities"] = items
	data["CurrencyForm"] = form
	if message != "" {
		data["Error"] = message
	}
	var body strings.Builder
	if err := h.templates.ExecuteTemplate(&body, "admin.html", data); err != nil {
		http.Error(w, "Не удалось загрузить страницу", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	w.Write([]byte(body.String()))
}

func (h *Handler) createAdminCommodity(c models.Commodity) error {
	if !commodityCodePattern.MatchString(c.Mnemonic) {
		return validationError("Код: от 2 до 12 латинских букв или цифр, начиная с буквы (например, ARS или USDT)")
	}
	if c.Fullname == "" || utf8.RuneCountInString(c.Fullname) > 255 {
		return validationError("Название должно содержать от 1 до 255 символов")
	}
	if c.Sign == "" || utf8.RuneCountInString(c.Sign) > 10 {
		return validationError("Обозначение должно содержать от 1 до 10 символов")
	}
	validFraction := false
	for n := 1; n <= 100000000; n *= 10 {
		validFraction = validFraction || c.Fraction == n
	}
	if !validFraction {
		return validationError("Некорректная точность валюты")
	}
	tx, err := h.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Match the registry lock used by backup restore and GnuCash import so
	// concurrent requests cannot insert the same code after checking it.
	if _, err = tx.Exec(`UPDATE commodities SET id=id WHERE id=1`); err != nil {
		return err
	}
	var count int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM commodities WHERE UPPER(TRIM(mnemonic))=?`, c.Mnemonic).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return validationError("Валюта с таким кодом уже существует")
	}
	if _, err = tx.Exec(`INSERT INTO commodities(namespace,mnemonic,fullname,fraction,sign) VALUES('CURRENCY',?,?,?,?)`, c.Mnemonic, c.Fullname, c.Fraction, c.Sign); err != nil {
		return err
	}
	return tx.Commit()
}
