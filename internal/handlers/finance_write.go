package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/evbogdanov/finforme/internal/amount"
	"github.com/evbogdanov/finforme/internal/models"
	"math"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"
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
	if err := validateTransactionComment(in.Comment); err != nil {
		return 0, err
	}
	if in.TxID < 0 || in.PostDate.IsZero() {
		return 0, validationError("Укажите корректные ID и дату транзакции")
	}
	if in.DebitAccountID == in.CreditAccountID {
		return 0, validationError("Выберите разные счета списания и зачисления")
	}
	tx, err := h.beginFinanceWrite(userID)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var debitCurrency, creditCurrency, debitFraction, creditFraction int64
	for _, account := range []struct {
		id       int64
		currency *int64
		fraction *int64
	}{
		{in.DebitAccountID, &debitCurrency, &debitFraction}, {in.CreditAccountID, &creditCurrency, &creditFraction},
	} {
		var placeholder int
		err := tx.QueryRow(`SELECT a.commodity_id, a.placeholder, c.fraction FROM accounts a JOIN commodities c ON c.id=a.commodity_id WHERE a.id = ? AND a.user_id = ?`, account.id, userID).Scan(account.currency, &placeholder, account.fraction)
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
	valueNum, err := amount.Float(in.Value, creditFraction)
	if in.ValueText != "" {
		valueNum, err = amount.Parse(in.ValueText, creditFraction)
	}
	if err != nil {
		return 0, validationError(err.Error())
	}
	if valueNum <= 0 {
		return 0, validationError("Сумма должна быть положительной")
	}
	target := in.Value
	if in.ValueTarget != nil {
		target = *in.ValueTarget
	}
	targetNum, err := amount.Float(target, debitFraction)
	targetText := in.TargetText
	if in.ValueTarget == nil && targetText == "" {
		targetText = in.ValueText
	}
	if targetText != "" {
		targetNum, err = amount.Parse(targetText, debitFraction)
	}
	if err != nil {
		return 0, validationError(err.Error())
	}
	if targetNum <= 0 {
		return 0, validationError("Сумма должна быть положительной")
	}
	if debitCurrency == creditCurrency && valueNum != targetNum {
		return 0, validationError("Суммы списания и зачисления в одной валюте должны совпадать")
	}
	if debitCurrency != creditCurrency && in.ValueTarget == nil && in.TargetText == "" {
		return 0, validationError("Для разных валют укажите сумму зачисления")
	}
	before, err := snapshotTransaction(tx, userID, in.TxID)
	if err != nil {
		return 0, err
	}
	id := in.TxID
	in.PostDate, err = transactionDate(tx, userID, id, in.PostDate, in.PostTime)
	if err != nil {
		return 0, err
	}
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
			return 0, validationError("У этой операции нестандартное распределение сумм. Можно изменить только описание, комментарий, дату и теги")
		}
		if _, err := tx.Exec(`UPDATE transactions SET description = ?, post_date = ?, tags = ?, comment = COALESCE(?,comment) WHERE id = ? AND user_id = ?`, in.Description, in.PostDate, in.Tags, in.Comment, id, userID); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`DELETE FROM splits WHERE tx_id = ? AND user_id = ?`, id, userID); err != nil {
			return 0, err
		}
	} else {
		res, err := tx.Exec(`INSERT INTO transactions (user_id,post_date,enter_date,description,tags,comment) VALUES(?,?,?,?,?,COALESCE(?,''))`, userID, in.PostDate, time.Now(), in.Description, in.Tags, in.Comment)
		if err != nil {
			return 0, err
		}
		id, err = res.LastInsertId()
		if err != nil {
			return 0, err
		}
	}
	for _, split := range []struct{ account, amount, denom int64 }{{in.DebitAccountID, targetNum, debitFraction}, {in.CreditAccountID, -valueNum, creditFraction}} {
		if _, err := tx.Exec(`INSERT INTO splits (user_id,tx_id,account_id,value_num,value_denom) VALUES(?,?,?,?,?)`, userID, id, split.account, split.amount, split.denom); err != nil {
			return 0, err
		}
	}
	action := "create"
	if in.TxID != 0 {
		action = "update"
	}
	if err := recordTransactionHistory(tx, userID, id, action, in.Source, before); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func (h *Handler) updateTransactionMetadata(userID, id int64, date time.Time, description, tags string, comment *string, clocks ...*string) error {
	return h.updateTransactionMetadataFrom(userID, id, date, description, tags, comment, "api", clocks...)
}
func (h *Handler) updateTransactionMetadataFrom(userID, id int64, date time.Time, description, tags string, comment *string, source string, clocks ...*string) error {
	if err := validateTransactionComment(comment); err != nil {
		return err
	}
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
	before, err := snapshotTransaction(tx, userID, id)
	if err != nil {
		return err
	}
	var clock *string
	if len(clocks) > 0 {
		clock = clocks[0]
	}
	date, err = transactionDate(tx, userID, id, date, clock)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE transactions SET post_date = ?, description = ?, tags = ?, comment = COALESCE(?,comment) WHERE id = ? AND user_id = ?`, date, description, tags, comment, id, userID); err != nil {
		return err
	}
	if err := recordTransactionHistory(tx, userID, id, "update", source, before); err != nil {
		return err
	}
	return tx.Commit()
}

func (h *Handler) APITransactionMetadataSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeFinanceError(w, validationError("Не удалось прочитать форму"))
		return
	}
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
	if err := h.updateTransactionMetadataFrom(userID, id, date, r.FormValue("description"), r.FormValue("tags"), optionalFormComment(r), financeSource(r), optionalFormTime(r)); err != nil {
		writeFinanceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"result": "ok", "id": id})
}

// Omitted fields from older clients preserve comments; an explicit empty value clears them.
func optionalFormComment(r *http.Request) *string {
	if !r.PostForm.Has("comment") {
		return nil
	}
	value := r.PostForm.Get("comment")
	return &value
}

func validateTransactionComment(comment *string) error {
	if comment != nil && (!utf8.ValidString(*comment) || utf8.RuneCountInString(*comment) > 16000) {
		return validationError("Комментарий должен содержать не более 16000 символов UTF-8")
	}
	return nil
}

// Old postings can be more precise than today's currency settings. Keep them
// editable through metadata only rather than forcing a rounded replacement.
func transactionMetadataOnly(debit, credit []map[string]interface{}, currencies map[int64]*models.Commodity) bool {
	if len(debit) != 1 || len(credit) != 1 {
		return true
	}
	for _, split := range []map[string]interface{}{debit[0], credit[0]} {
		id, _ := split["commodity_id"].(int64)
		currency := currencies[id]
		if currency == nil {
			continue
		}
		text, _ := split["value_exact"].(string)
		if text == "" {
			value, _ := split["value"].(float64)
			text = strconv.FormatFloat(value, 'f', -1, 64)
		}
		if _, err := amount.Parse(text, int64(currency.Fraction)); err != nil {
			return true
		}
	}
	return false
}
