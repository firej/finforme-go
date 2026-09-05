package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"
)

// Serialize financial writes for one user before reading any validation state.
// UPDATE takes an InnoDB row lock even if the value does not change. It also
// works with SQLite in tests; RowsAffected is intentionally not used here.
func (h *Handler) beginFinanceWrite(userID int64) (*sql.Tx, error) {
	tx, err := h.db.Begin()
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`UPDATE users SET id = id WHERE id = ?`, userID); err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func writeFinanceError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := "Не удалось сохранить изменения. Попробуйте ещё раз."
	var invalid validationError
	if errors.As(err, &invalid) {
		status = http.StatusBadRequest
		message = err.Error()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// Restrict to cents that float64 can represent exactly as integers. Reject
// non-finite, nonpositive, sub-cent and out-of-range amounts before conversion.
func transactionCents(value float64) (int64, error) {
	scaled := value * 100
	rounded := math.Round(scaled)
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 || rounded < 1 || rounded > (1<<53)-1 {
		return 0, validationError("Сумма должна быть положительной, не меньше 0,01 и не больше 90 071 992 547 409,90")
	}
	if value != rounded/100 {
		return 0, validationError("Укажите сумму с точностью до двух знаков после запятой")
	}
	return int64(rounded), nil
}

func requireTransaction(tx *sql.Tx, userID, id int64) error {
	if id <= 0 {
		return validationError("Транзакция не найдена")
	}
	var found int64
	if err := tx.QueryRow(`SELECT id FROM transactions WHERE id = ? AND user_id = ?`, id, userID).Scan(&found); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return validationError("Транзакция не найдена")
		}
		return err
	}
	return nil
}

func (h *Handler) saveTransaction(userID int64, in txSaveInput) (int64, error) {
	if in.TxID < 0 || in.PostDate.IsZero() {
		return 0, validationError("Укажите корректные ID и дату транзакции")
	}
	if in.DebitAccountID == in.CreditAccountID {
		return 0, validationError("Выберите разные счета списания и зачисления")
	}
	valueNum, err := transactionCents(in.Value)
	if err != nil {
		return 0, err
	}
	target := in.Value
	if in.ValueTarget != nil {
		target = *in.ValueTarget
	}
	targetNum, err := transactionCents(target)
	if err != nil {
		return 0, err
	}
	tx, err := h.beginFinanceWrite(userID)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var debitCurrency, creditCurrency int64
	for _, account := range []struct {
		id       int64
		currency *int64
	}{
		{in.DebitAccountID, &debitCurrency}, {in.CreditAccountID, &creditCurrency},
	} {
		var placeholder int
		err := tx.QueryRow(`SELECT commodity_id, placeholder FROM accounts WHERE id = ? AND user_id = ?`, account.id, userID).Scan(account.currency, &placeholder)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, validationError("Счёт не найден")
		}
		if err != nil {
			return 0, err
		}
		if placeholder != 0 {
			return 0, validationError("Контейнерный счёт не может участвовать в транзакции — выберите конечный счёт")
		}
	}
	if debitCurrency == creditCurrency && valueNum != targetNum {
		return 0, validationError("Суммы списания и зачисления в одной валюте должны совпадать")
	}
	if debitCurrency != creditCurrency && in.ValueTarget == nil {
		return 0, validationError("Для разных валют укажите сумму зачисления")
	}
	id := in.TxID
	if id != 0 {
		if err := requireTransaction(tx, userID, id); err != nil {
			return 0, err
		}
		var count, positive, negative int
		if err := tx.QueryRow(`SELECT COUNT(*), COALESCE(SUM(CASE WHEN value_num > 0 THEN 1 ELSE 0 END),0),
   COALESCE(SUM(CASE WHEN value_num < 0 THEN 1 ELSE 0 END),0) FROM splits WHERE tx_id = ?`, id).Scan(&count, &positive, &negative); err != nil {
			return 0, err
		}
		if count != 2 || positive != 1 || negative != 1 {
			return 0, validationError("У этой операции нестандартное распределение сумм. Можно изменить только описание, дату и теги")
		}
		if _, err := tx.Exec(`UPDATE transactions SET description = ?, post_date = ?, tags = ? WHERE id = ? AND user_id = ?`, in.Description, in.PostDate, in.Tags, id, userID); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`DELETE FROM splits WHERE tx_id = ? AND user_id = ?`, id, userID); err != nil {
			return 0, err
		}
	} else {
		res, err := tx.Exec(`INSERT INTO transactions (user_id,post_date,enter_date,description,tags) VALUES(?,?,?,?,?)`, userID, in.PostDate, time.Now(), in.Description, in.Tags)
		if err != nil {
			return 0, err
		}
		id, err = res.LastInsertId()
		if err != nil {
			return 0, err
		}
	}
	for _, split := range []struct{ account, amount int64 }{{in.DebitAccountID, targetNum}, {in.CreditAccountID, -valueNum}} {
		if _, err := tx.Exec(`INSERT INTO splits (user_id,tx_id,account_id,value_num,value_denom) VALUES(?,?,?,?,100)`, userID, id, split.account, split.amount); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func (h *Handler) updateTransactionMetadata(userID, id int64, date time.Time, description, tags string) error {
	if date.IsZero() {
		return validationError("Укажите дату транзакции")
	}
	tx, err := h.beginFinanceWrite(userID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := requireTransaction(tx, userID, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE transactions SET post_date = ?, description = ?, tags = ? WHERE id = ? AND user_id = ?`, date, description, tags, id, userID); err != nil {
		return err
	}
	return tx.Commit()
}

func (h *Handler) APITransactionMetadataSave(w http.ResponseWriter, r *http.Request) {
	userID, _ := h.getUserID(r)
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		writeFinanceError(w, validationError("Некорректный ID транзакции"))
		return
	}
	date, err := time.Parse("2006-01-02", r.FormValue("post_date"))
	if err != nil {
		writeFinanceError(w, validationError("Некорректная дата"))
		return
	}
	if err := h.updateTransactionMetadata(userID, id, date, r.FormValue("description"), r.FormValue("tags")); err != nil {
		writeFinanceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"result": "ok", "id": id})
}
