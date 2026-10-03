package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"time"
)

// transactionDate preserves the stored wall-clock time when older clients omit it.
// This runs under the same finance write lock as the subsequent save.
func transactionDate(tx *sql.Tx, userID, id int64, date time.Time, clock *string) (time.Time, error) {
	if clock != nil {
		var parsed time.Time
		var err error
		switch len(*clock) {
		case 5:
			parsed, err = time.Parse("15:04", *clock)
		case 8:
			parsed, err = time.Parse("15:04:05", *clock)
		default:
			return time.Time{}, validationError("Укажите время в формате HH:MM или HH:MM:SS")
		}
		if err != nil {
			return time.Time{}, validationError("Некорректное время транзакции")
		}
		return time.Date(date.Year(), date.Month(), date.Day(), parsed.Hour(), parsed.Minute(), parsed.Second(), 0, date.Location()), nil
	}
	if id != 0 {
		var old time.Time
		if err := tx.QueryRow(`SELECT post_date FROM transactions WHERE id=? AND user_id=?`, id, userID).Scan(&old); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return time.Time{}, validationError("Транзакция не найдена")
			}
			return time.Time{}, err
		}
		return time.Date(date.Year(), date.Month(), date.Day(), old.Hour(), old.Minute(), old.Second(), old.Nanosecond(), date.Location()), nil
	}
	return date, nil
}

func optionalFormTime(r *http.Request) *string {
	if !r.PostForm.Has("post_time") {
		return nil
	}
	value := r.PostForm.Get("post_time")
	return &value
}
