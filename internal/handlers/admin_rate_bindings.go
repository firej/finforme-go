package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/evbogdanov/finforme/internal/models"
)

// A binding describes an entire (code, source) series, including future quotes.
// A rate is quote-currency units per one base-currency unit.
type currencyRateBinding struct {
	Code, Source    string
	BaseID, QuoteID int64
}

func (h *Handler) AdminRateBindings(w http.ResponseWriter, r *http.Request) {
	status := http.StatusOK
	message := ""
	var submitted currencyRateBinding
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Не удалось прочитать форму", http.StatusBadRequest)
			return
		}
		submitted.Code = r.PostForm.Get("code")
		submitted.Source = r.PostForm.Get("source")
		submitted.BaseID, _ = strconv.ParseInt(r.PostForm.Get("base_id"), 10, 64)
		submitted.QuoteID, _ = strconv.ParseInt(r.PostForm.Get("quote_id"), 10, 64)
		action := r.PostForm.Get("action")
		if err := h.saveRateBinding(submitted, action); err != nil {
			var invalid validationError
			if !errors.As(err, &invalid) {
				http.Error(w, "Не удалось сохранить привязку", http.StatusInternalServerError)
				return
			}
			message = err.Error()
			status = http.StatusBadRequest
		} else {
			http.Redirect(w, r, "/admin/rate-bindings/?msg=updated", http.StatusSeeOther)
			return
		}
	}
	rows, err := h.db.Query(`SELECT series.code,series.source,
  COALESCE(b.base_commodity_id,0),COALESCE(b.quote_commodity_id,0)
  FROM (SELECT code,source FROM currency_rates UNION SELECT code,source FROM currency_rate_bindings) series
  LEFT JOIN currency_rate_bindings b ON b.code=series.code AND b.source=series.source
  ORDER BY series.code,series.source`)
	if err != nil {
		http.Error(w, "Не удалось загрузить привязки", 500)
		return
	}
	var bindings []currencyRateBinding
	for rows.Next() {
		var b currencyRateBinding
		if err = rows.Scan(&b.Code, &b.Source, &b.BaseID, &b.QuoteID); err != nil {
			break
		}
		if status == http.StatusBadRequest && b.Code == submitted.Code && b.Source == submitted.Source {
			b = submitted
		}
		bindings = append(bindings, b)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil || rowErr != nil {
		http.Error(w, "Не удалось загрузить привязки", 500)
		return
	}
	rows, err = h.db.Query(`SELECT id,mnemonic,COALESCE(fullname,'') FROM commodities ORDER BY mnemonic,id`)
	if err != nil {
		http.Error(w, "Не удалось загрузить валюты", 500)
		return
	}
	var commodities []models.Commodity
	for rows.Next() {
		var c models.Commodity
		if err = rows.Scan(&c.ID, &c.Mnemonic, &c.Fullname); err != nil {
			break
		}
		commodities = append(commodities, c)
	}
	rowErr = rows.Err()
	rows.Close()
	if err != nil || rowErr != nil {
		http.Error(w, "Не удалось загрузить валюты", 500)
		return
	}
	data := h.adminPageData(r, "Админка — Привязка курсов")
	data["Tab"] = "rate-bindings"
	data["Bindings"] = bindings
	data["Commodities"] = commodities
	data["Error"] = message
	var body strings.Builder
	if err := h.templates.ExecuteTemplate(&body, "admin.html", data); err != nil {
		http.Error(w, "Не удалось загрузить страницу", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	w.Write([]byte(body.String()))
}

func (h *Handler) saveRateBinding(b currencyRateBinding, action string) error {
	if b.Code == "" || b.Source == "" || utf8.RuneCountInString(b.Code) > 20 || utf8.RuneCountInString(b.Source) > 50 {
		return validationError("Укажите пару и источник")
	}
	if action != "save" && action != "delete" {
		return validationError("Неизвестное действие")
	}
	tx, err := h.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Use the same registry lock as currency editing and imports.
	if _, err = tx.Exec(`UPDATE commodities SET id=id WHERE id=1`); err != nil {
		return err
	}
	if action == "delete" {
		if _, err = tx.Exec(`DELETE FROM currency_rate_bindings WHERE code=? AND source=?`, b.Code, b.Source); err != nil {
			return err
		}
		return tx.Commit()
	}
	if b.BaseID <= 0 || b.QuoteID <= 0 || b.BaseID == b.QuoteID {
		return validationError("Выберите две разные валюты")
	}
	var count int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM currency_rates WHERE code=? AND source=?`, b.Code, b.Source).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return validationError("Для этой пары и источника нет курсов")
	}
	var base, quote string
	err = tx.QueryRow(`SELECT b.mnemonic,q.mnemonic FROM commodities b CROSS JOIN commodities q WHERE b.id=? AND q.id=?`, b.BaseID, b.QuoteID).Scan(&base, &quote)
	if errors.Is(err, sql.ErrNoRows) {
		return validationError("Валюта не найдена в справочнике")
	}
	if err != nil {
		return err
	}
	// All importers store BASE/QUOTE. Reject reversed or unrelated currencies.
	if !strings.EqualFold(strings.TrimSpace(base)+"/"+strings.TrimSpace(quote), b.Code) {
		return validationError("Валюты и их порядок должны соответствовать коду пары")
	}
	if _, err = tx.Exec(`DELETE FROM currency_rate_bindings WHERE code=? AND source=?`, b.Code, b.Source); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO currency_rate_bindings(code,source,base_commodity_id,quote_commodity_id) VALUES(?,?,?,?)`, b.Code, b.Source, b.BaseID, b.QuoteID); err != nil {
		return err
	}
	return tx.Commit()
}
