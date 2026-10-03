package handlers

import (
	"fmt"
	"time"
)

type currencyRatesIn struct {
	Date string `json:"date,omitempty" jsonschema:"YYYY-MM-DD: курсы ЦБ, действовавшие на эту дату; без даты — последние курсы всех источников"`
}

func (h *Handler) historicalCurrencyRates(date string) (currencyRatesOut, error) {
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return currencyRatesOut{}, validationError("Дата курса должна быть в формате YYYY-MM-DD")
	}
	out := currencyRatesOut{RequestedDate: date, Rates: []currencyRateOut{}}
	rows, err := h.db.Query(`SELECT cr.code,cr.name,cr.rate,cr.source,CAST(cr.rate_date AS CHAR)
 FROM currency_rates cr
 JOIN (SELECT code,source,MAX(rate_date) AS effective_date FROM currency_rates
 WHERE source='cbr' AND rate_date<=? GROUP BY code,source) latest
 ON cr.code=latest.code AND cr.source=latest.source AND cr.rate_date=latest.effective_date
 ORDER BY cr.code`, date)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var rate currencyRateOut
		if err = rows.Scan(&rate.Code, &rate.Name, &rate.Rate, &rate.Source, &rate.RateDate); err != nil {
			rows.Close()
			return out, err
		}
		if len(rate.RateDate) >= 10 {
			rate.RateDate = rate.RateDate[:10]
		}
		out.Rates = append(out.Rates, rate)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = h.db.Query(`SELECT DISTINCT code FROM currency_rates WHERE source='cbr' ORDER BY code`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	available := make(map[string]bool)
	for _, rate := range out.Rates {
		available[rate.Code] = true
	}
	for rows.Next() {
		var code string
		if err = rows.Scan(&code); err != nil {
			return out, err
		}
		if !available[code] {
			out.MissingPairs = append(out.MissingPairs, code)
		}
	}
	if len(out.Rates) == 0 {
		out.Message = fmt.Sprintf("Исторические курсы ЦБ на %s отсутствуют в сервисе", date)
	} else if len(out.MissingPairs) > 0 {
		out.Message = "Для части валютных пар исторические данные отсутствуют: см. missing_pairs"
	}
	return out, rows.Err()
}
