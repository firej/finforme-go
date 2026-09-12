package handlers

import (
	"database/sql"
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
	if r.Method == http.MethodGet && r.URL.Query().Has("edit") {
		id, err := strconv.ParseInt(r.URL.Query().Get("edit"), 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "Некорректный ID валюты", http.StatusBadRequest)
			return
		}
		err = h.db.QueryRow(`SELECT id,mnemonic,COALESCE(fullname,''),fraction,COALESCE(sign,'') FROM commodities WHERE id=?`, id).Scan(&form.ID, &form.Mnemonic, &form.Fullname, &form.Fraction, &form.Sign)
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "Не удалось загрузить валюту", http.StatusInternalServerError)
			return
		}
	}
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Не удалось прочитать форму", http.StatusBadRequest)
			return
		}
		if rawID := r.PostForm.Get("id"); rawID != "" {
			id, err := strconv.ParseInt(rawID, 10, 64)
			if err != nil || id <= 0 {
				http.Error(w, "Некорректный ID валюты", http.StatusBadRequest)
				return
			}
			form.ID = id
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
			if err = h.saveAdminCommodity(form); err != nil {
				var invalid validationError
				if !errors.As(err, &invalid) {
					http.Error(w, "Не удалось сохранить валюту. Попробуйте ещё раз.", http.StatusInternalServerError)
					return
				}
				message = err.Error()
			} else {
				msg := "currency_created"
				if form.ID != 0 {
					msg = "currency_updated"
				}
				http.Redirect(w, r, "/admin/commodities/?msg="+msg, http.StatusSeeOther)
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
	c.ID = 0
	return h.saveAdminCommodity(c)
}

func (h *Handler) saveAdminCommodity(c models.Commodity) error {
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
	if c.ID != 0 {
		var code string
		var fraction int
		err = tx.QueryRow(`SELECT mnemonic,fraction FROM commodities WHERE id=?`, c.ID).Scan(&code, &fraction)
		if errors.Is(err, sql.ErrNoRows) {
			return validationError("Валюта не найдена")
		}
		if err != nil {
			return err
		}
		if code != c.Mnemonic || fraction != c.Fraction {
			return validationError("Код и точность существующей валюты менять нельзя: создайте новую валюту")
		}
		if _, err = tx.Exec(`UPDATE commodities SET fullname=?,sign=? WHERE id=?`, c.Fullname, c.Sign, c.ID); err != nil {
			return err
		}
		return tx.Commit()
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
