package handlers

import (
	"fmt"
	"strings"
	"time"
)

type spendingDay struct {
	Current, Previous string
}

type spendingTick struct {
	Label string
	Y     int
}

type spendingTrend struct {
	Days                            []spendingDay
	Ticks                           []spendingTick
	CurrentLine, PreviousLine       string
	CurrentArea                     string
	CurrentTotal, PreviousTotal     string
	CurrentThrough, PreviousThrough int
	Change                          string
	ChangeSign                      int
}

type dailySpending struct {
	current, previous []int64
}

func (h *Handler) dashboardSpendingTrends(userID int64, month, now time.Time) (map[string]spendingTrend, error) {
	previousMonth := month.AddDate(0, -1, 0)
	currentDays := month.AddDate(0, 1, -1).Day()
	previousDays := month.AddDate(0, 0, -1).Day()
	inProgress := month.Format("2006-01") == now.Format("2006-01")
	if inProgress {
		currentDays = now.Day()
	}
	end := month.AddDate(0, 0, currentDays)
	rows, err := h.db.Query(`
		SELECT SUBSTR(CAST(t.post_date AS CHAR), 1, 10), c.mnemonic, ROUND(SUM(CAST(s.value_num AS DECIMAL(65,16)) * 100.0 / s.value_denom),0)
		FROM splits s
		JOIN transactions t ON t.id = s.tx_id AND t.user_id = s.user_id
		JOIN accounts a ON a.id = s.account_id AND a.user_id = s.user_id
		JOIN commodities c ON c.id = a.commodity_id
		WHERE s.user_id = ? AND a.account_type = 'EXPENSE'
		  AND t.post_date >= ? AND t.post_date < ?
		GROUP BY SUBSTR(CAST(t.post_date AS CHAR), 1, 10), c.mnemonic
		HAVING ROUND(SUM(CAST(s.value_num AS DECIMAL(65,16)) * 100.0 / s.value_denom),0) <> 0
	`, userID, previousMonth, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byCurrency := make(map[string]dailySpending)
	for rows.Next() {
		var date, currency string
		var cents int64
		if err := rows.Scan(&date, &currency, &cents); err != nil {
			return nil, err
		}
		day, err := time.Parse("2006-01-02", date)
		if err != nil {
			return nil, err
		}
		series, ok := byCurrency[currency]
		if !ok {
			series = dailySpending{make([]int64, currentDays), make([]int64, previousDays)}
			byCurrency[currency] = series
		}
		if day.Before(month) {
			series.previous[day.Day()-1] = cents
		} else {
			series.current[day.Day()-1] = cents
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	trends := make(map[string]spendingTrend, len(byCurrency))
	for currency, series := range byCurrency {
		trends[currency] = buildSpendingTrend(series, inProgress)
	}
	return trends, nil
}

func buildSpendingTrend(series dailySpending, inProgress bool) spendingTrend {
	var low, high int64
	accumulate := func(daily []int64) {
		var total int64
		for i, cents := range daily {
			total += cents
			daily[i] = total
			low, high = min(low, total), max(high, total)
		}
	}
	accumulate(series.current)
	accumulate(series.previous)

	currentDays, previousDays := len(series.current), len(series.previous)
	through := previousDays
	if inProgress {
		through = min(currentDays, previousDays)
	}
	currentTotal, previousTotal := series.current[currentDays-1], series.previous[through-1]
	trend := spendingTrend{
		CurrentThrough: currentDays, PreviousThrough: through,
		CurrentTotal:  formatMoney(float64(currentTotal) / 100),
		PreviousTotal: formatMoney(float64(previousTotal) / 100),
		Change:        amountChange(float64(currentTotal)/100, float64(previousTotal)/100),
	}
	if currentTotal < previousTotal {
		trend.ChangeSign = -1
	} else if currentTotal > previousTotal {
		trend.ChangeSign = 1
	}
	dayCount := max(currentDays, previousDays)
	for i := 0; i < dayCount; i++ {
		day := spendingDay{Current: "—", Previous: "—"}
		if i < currentDays {
			day.Current = formatMoney(float64(series.current[i]) / 100)
		}
		if i < previousDays {
			day.Previous = formatMoney(float64(series.previous[i]) / 100)
		}
		trend.Days = append(trend.Days, day)
	}

	span := float64(high) - float64(low)
	y := func(cents int64) float64 { return 8 + 84*(float64(high)-float64(cents))/span }
	line := func(values []int64) string {
		var path strings.Builder
		fmt.Fprintf(&path, "M0 %.3f", y(0))
		for i, cents := range values {
			fmt.Fprintf(&path, " L%.3f %.3f", 100*float64(i+1)/float64(dayCount), y(cents))
		}
		return path.String()
	}
	trend.PreviousLine = line(series.previous)
	trend.CurrentLine = line(series.current)
	trend.CurrentArea = fmt.Sprintf("%s L%.3f %.3f H0 Z", trend.CurrentLine, 100*float64(currentDays)/float64(dayCount), y(0))
	for i, cents := range []float64{float64(high), float64(high) - span/2, float64(low)} {
		label := formatMoney(cents / 100)
		if cents <= -100_000 || cents >= 100_000 {
			label = formatMoneyShort(cents / 100)
		}
		trend.Ticks = append(trend.Ticks, spendingTick{Label: label, Y: 8 + 42*i})
	}
	return trend
}
