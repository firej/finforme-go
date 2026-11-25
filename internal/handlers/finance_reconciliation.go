package handlers

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"time"

	"github.com/evbogdanov/finforme/internal/amount"
)

type reconciliationResult struct {
	ID         int64  `json:"id"`
	AccountID  int64  `json:"account_id"`
	Date       string `json:"date"`
	Currency   string `json:"currency"`
	Actual     string `json:"actual"`
	Expected   string `json:"expected"`
	Difference string `json:"difference"`
	Matches    bool   `json:"matches"`
	Stale      bool   `json:"stale"`
	hash       string
	fraction   int64
}

// Check accounting balances, not rounded display values. The fingerprint changes
// even when two later edits happen to cancel each other out financially.
func reconciliationState(tx *sql.Tx, userID, accountID int64, date time.Time, expected string, historicalFraction ...int64) (reconciliationResult, error) {
	out := reconciliationResult{AccountID: accountID, Date: date.Format("2006-01-02")}
	var kind string
	var placeholder int
	err := tx.QueryRow(`SELECT a.account_type,a.placeholder,c.mnemonic,c.fraction FROM accounts a JOIN commodities c ON c.id=a.commodity_id WHERE a.user_id=? AND a.id=?`, userID, accountID).Scan(&kind, &placeholder, &out.Currency, &out.fraction)
	if err == sql.ErrNoRows {
		return out, validationError("Счёт не найден")
	}
	if err != nil {
		return out, err
	}
	if placeholder != 0 && len(historicalFraction) == 0 {
		return out, validationError("Сверка доступна только для конечного счёта")
	}
	expectedFraction := out.fraction
	if len(historicalFraction) > 0 {
		expectedFraction = historicalFraction[0]
	}
	n, err := amount.Parse(expected, expectedFraction)
	if err != nil {
		return out, validationError(err.Error())
	}
	total := new(big.Rat)
	hash := sha256.New()
	fmt.Fprintf(hash, "%d|%s|%s|%d|", accountID, kind, out.Currency, placeholder)
	rows, err := tx.Query(`SELECT s.id,s.tx_id,t.post_date,s.value_num,s.value_denom FROM splits s JOIN transactions t ON t.id=s.tx_id AND t.user_id=s.user_id WHERE s.user_id=? AND s.account_id=? AND t.post_date<? ORDER BY s.id`, userID, accountID, date.AddDate(0, 0, 1))
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var id, tid, num, denom int64
		var posted time.Time
		if err := rows.Scan(&id, &tid, &posted, &num, &denom); err != nil {
			rows.Close()
			return out, err
		}
		if denom <= 0 {
			rows.Close()
			return out, fmt.Errorf("invalid split denominator")
		}
		total.Add(total, new(big.Rat).SetFrac(big.NewInt(num), big.NewInt(denom)))
		fmt.Fprintf(hash, "%d/%d/%s/%d/%d;", id, tid, posted.Format("2006-01-02T15:04:05"), num, denom)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	// Match the positive income balance used in the account UI; liabilities retain
	// their accounting sign, so credit-card debt is entered as a negative balance.
	if kind == "INCOME" {
		total.Neg(total)
	}
	target := new(big.Rat).SetFrac(big.NewInt(n), big.NewInt(expectedFraction))
	delta := new(big.Rat).Sub(total, target)
	out.Actual = total.FloatString(8)
	out.Expected = target.FloatString(8)
	out.Difference = delta.FloatString(8)
	out.Matches = delta.Sign() == 0
	out.hash = hex.EncodeToString(hash.Sum(nil))
	return out, nil
}
func (h *Handler) reconcileAccount(userID, accountID int64, date time.Time, expected string, save bool) (reconciliationResult, error) {
	if date.IsZero() || date.Year() < 1000 || date.Year() > 9999 {
		return reconciliationResult{}, validationError("Некорректная дата")
	}
	tx, err := h.beginFinanceWrite(userID)
	if err != nil {
		return reconciliationResult{}, err
	}
	defer tx.Rollback()
	out, err := reconciliationState(tx, userID, accountID, date, expected)
	if err != nil {
		return out, err
	}
	if save {
		if !out.Matches {
			return out, validationError("Остатки не совпадают. Проверьте пропущенные или повторные операции")
		}
		n, _ := amount.Parse(expected, out.fraction)
		res, err := tx.Exec(`INSERT INTO account_reconciliations(user_id,account_id,as_of,expected_num,expected_denom,ledger_hash,created_at) VALUES(?,?,?,?,?,?,?)`, userID, accountID, date, n, out.fraction, out.hash, time.Now().UTC())
		if err != nil {
			return out, err
		}
		out.ID, err = res.LastInsertId()
		if err != nil {
			return out, err
		}
		// Checking a transaction is explicit and independent; reconciling a closing
		// balance does not silently mark individual transactions as reviewed.
	}
	return out, tx.Commit()
}
func (h *Handler) latestReconciliation(userID, accountID int64) (*reconciliationResult, error) {
	tx, err := h.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var id, num, denom int64
	var date time.Time
	var hash string
	err = tx.QueryRow(`SELECT id,as_of,expected_num,expected_denom,ledger_hash FROM account_reconciliations WHERE user_id=? AND account_id=? ORDER BY as_of DESC,id DESC LIMIT 1`, userID, accountID).Scan(&id, &date, &num, &denom, &hash)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// Existing precision may be higher than the currency's current input setting.
	out, err := reconciliationState(tx, userID, accountID, date, amount.Decimal(num, denom), denom)
	if err != nil {
		return nil, err
	}
	out.ID = id
	out.Stale = out.hash != hash
	return &out, tx.Commit()
}
func (h *Handler) setTransactionChecked(userID, id int64, checked bool) error {
	tx, err := h.beginFinanceWrite(userID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := requireTransaction(tx, userID, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM transaction_checks WHERE user_id=? AND transaction_id=?`, userID, id); err != nil {
		return err
	}
	if checked {
		if _, err := tx.Exec(`INSERT INTO transaction_checks(user_id,transaction_id,checked_at) VALUES(?,?,?)`, userID, id, time.Now().UTC()); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (h *Handler) APIReconcileAccount(w http.ResponseWriter, r *http.Request) {
	userID, _ := h.getUserID(r)
	id, err := strconv.ParseInt(r.FormValue("account_id"), 10, 64)
	if err != nil {
		writeFinanceError(w, validationError("Некорректный счёт"))
		return
	}
	date, err := time.Parse("2006-01-02", r.FormValue("date"))
	if err != nil {
		writeFinanceError(w, validationError("Некорректная дата"))
		return
	}
	out, err := h.reconcileAccount(userID, id, date, r.FormValue("expected"), r.FormValue("save") == "true")
	if err != nil {
		writeFinanceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
func (h *Handler) APITransactionCheck(w http.ResponseWriter, r *http.Request) {
	userID, _ := h.getUserID(r)
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		writeFinanceError(w, validationError("Некорректная операция"))
		return
	}
	checked, err := strconv.ParseBool(r.FormValue("checked"))
	if err != nil {
		writeFinanceError(w, validationError("Некорректная отметка"))
		return
	}
	if err := h.setTransactionChecked(userID, id, checked); err != nil {
		writeFinanceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
