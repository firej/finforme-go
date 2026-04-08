package handlers

import (
	"database/sql"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/evbogdanov/finforme/internal/models"
)

// CurrencyRateRow — одна строка курса из БД (последний курс)
type CurrencyRateRow struct {
	Code     string
	Name     string
	Rate     float64
	Source   string
	RateDate string
	Change   float64 // изменение относительно предыдущей записи того же источника
}

// CurrencyHistoryPoint — одна точка истории курса
type CurrencyHistoryPoint struct {
	Date string  `json:"date"`
	Rate float64 `json:"rate"`
}

// CurrencyChartData — данные для одного графика
type CurrencyChartData struct {
	Code   string                 `json:"code"`
	Points []CurrencyHistoryPoint `json:"points"`
}

// CurrencyPage — страница курсов валют
func (h *Handler) CurrencyPage(w http.ResponseWriter, r *http.Request) {
	userID, authenticated := h.getUserID(r)

	data := map[string]interface{}{
		"Title":         "Курсы валют",
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
		data["IsAdmin"] = h.getIsAdmin(userID)
	}

	rates, updatedAt, err := loadCurrencyRates(h.db)
	if err != nil {
		log.Printf("loadCurrencyRates error: %v", err)
	}
	data["Rates"] = rates
	data["UpdatedAt"] = updatedAt

	// Загружаем исторические данные за 14 дней для графиков
	charts, err := loadCurrencyCharts(h.db, 14)
	if err != nil {
		log.Printf("loadCurrencyCharts error: %v", err)
	}
	// Сериализуем в JSON для передачи в шаблон.
	// Используем template.JS чтобы Go не экранировал JSON как HTML.
	chartsJSON, err := json.Marshal(charts)
	if err != nil {
		log.Printf("charts json marshal error: %v", err)
		chartsJSON = []byte("[]")
	}
	data["ChartsJSON"] = template.JS(chartsJSON)

	h.renderTemplate(w, "currency.html", data)
}

// CurrencyChartsAPI возвращает JSON с историческими данными за указанный период
// GET /api/currency/charts?days=N (публичный эндпоинт)
func (h *Handler) CurrencyChartsAPI(w http.ResponseWriter, r *http.Request) {
	daysStr := r.URL.Query().Get("days")
	days, err := strconv.Atoi(daysStr)
	if err != nil || days <= 0 || days > 366 {
		days = 14
	}

	charts, err := loadCurrencyCharts(h.db, days)
	if err != nil {
		log.Printf("CurrencyChartsAPI loadCurrencyCharts error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(charts); err != nil {
		log.Printf("CurrencyChartsAPI encode error: %v", err)
	}
}

// loadCurrencyRates загружает последние курсы из БД и считает изменение за день
func loadCurrencyRates(db *sql.DB) ([]CurrencyRateRow, string, error) {
	rows, err := db.Query(`
		SELECT
			cr.code,
			cr.name,
			CAST(cr.rate AS DOUBLE),
			cr.source,
			DATE_FORMAT(cr.rate_date, '%d.%m.%Y') AS rate_date,
			COALESCE(CAST(prev.rate AS DOUBLE), 0) AS prev_rate
		FROM currency_rates cr
		INNER JOIN (
			SELECT code, source, MAX(rate_date) AS max_date
			FROM currency_rates
			GROUP BY code, source
		) latest ON cr.code = latest.code AND cr.source = latest.source AND cr.rate_date = latest.max_date
		LEFT JOIN currency_rates prev ON prev.code = cr.code
			AND prev.source = cr.source
			AND prev.rate_date = (
				SELECT MAX(rate_date)
				FROM currency_rates
				WHERE code = cr.code AND source = cr.source AND rate_date < cr.rate_date
			)
		ORDER BY
			CASE cr.code
				WHEN 'USD/RUB'  THEN 1
				WHEN 'EUR/RUB'  THEN 2
				WHEN 'USDT/RUB' THEN 3
				WHEN 'USD/ARS'  THEN 4
				WHEN 'RUB/ARS'  THEN 5
				ELSE 6
			END,
			cr.source
	`)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var result []CurrencyRateRow
	for rows.Next() {
		var r CurrencyRateRow
		var prevRate float64
		if err := rows.Scan(&r.Code, &r.Name, &r.Rate, &r.Source, &r.RateDate, &prevRate); err != nil {
			log.Printf("scan error: %v", err)
			continue
		}
		if prevRate != 0 {
			r.Change = r.Rate - prevRate
		}
		result = append(result, r)
	}

	updatedAt := time.Now().In(time.FixedZone("MSK", 3*60*60)).Format("02.01.2006 15:04 MSK")
	return result, updatedAt, rows.Err()
}

// loadCurrencyCharts загружает исторические данные за последние N дней
// Группирует по (code, source) — отдельный график для каждой пары
func loadCurrencyCharts(db *sql.DB, days int) ([]CurrencyChartData, error) {
	since := time.Now().AddDate(0, 0, -days).Format("2006-01-02")

	rows, err := db.Query(`
		SELECT
			code,
			source,
			DATE_FORMAT(rate_date, '%Y-%m-%d') AS label,
			CAST(rate AS DOUBLE)
		FROM currency_rates
		WHERE rate_date >= ?
		ORDER BY
			CASE code
				WHEN 'USD/RUB'  THEN 1
				WHEN 'EUR/RUB'  THEN 2
				WHEN 'USDT/RUB' THEN 3
				WHEN 'USD/ARS'  THEN 4
				WHEN 'RUB/ARS'  THEN 5
				ELSE 6
			END,
			source,
			rate_date ASC
	`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Группируем по "code|source"
	type key struct{ code, source string }
	pointsMap := make(map[key][]CurrencyHistoryPoint)
	keyOrder := make([]key, 0)

	for rows.Next() {
		var code, source, label string
		var rate float64
		if err := rows.Scan(&code, &source, &label, &rate); err != nil {
			continue
		}
		k := key{code, source}
		if _, exists := pointsMap[k]; !exists {
			keyOrder = append(keyOrder, k)
		}
		pointsMap[k] = append(pointsMap[k], CurrencyHistoryPoint{Date: label, Rate: rate})
	}

	result := make([]CurrencyChartData, 0, len(keyOrder))
	for _, k := range keyOrder {
		// Показываем код с источником если источников несколько
		displayCode := k.code
		result = append(result, CurrencyChartData{
			Code:   displayCode,
			Points: pointsMap[k],
		})
	}

	return result, rows.Err()
}
