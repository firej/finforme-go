package handlers

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/evbogdanov/finforme/internal/amount"
)

type backupReconciliation struct {
	AccountID int64     `json:"account_id"`
	Date      time.Time `json:"date"`
	Num       int64     `json:"numerator"`
	Denom     int64     `json:"denominator"`
	Stale     bool      `json:"stale"`
	CreatedAt time.Time `json:"created_at"`
}

func exportLedgerBackup(tx *sql.Tx, userID int64, data *backupData) error {
	rows, err := tx.Query(`SELECT id,transaction_id,action,source,created_at,before_json,after_json,restored_id FROM transaction_history WHERE user_id=? ORDER BY id`, userID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var item transactionHistory
		var b, a string
		if err := rows.Scan(&item.ID, &item.TransactionID, &item.Action, &item.Source, &item.CreatedAt, &b, &a, &item.RestoredID); err != nil {
			rows.Close()
			return err
		}
		if err := json.Unmarshal([]byte(b), &item.Before); err != nil {
			rows.Close()
			return err
		}
		if err := json.Unmarshal([]byte(a), &item.After); err != nil {
			rows.Close()
			return err
		}
		data.History = append(data.History, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = tx.Query(`SELECT transaction_id FROM transaction_checks WHERE user_id=? ORDER BY transaction_id`, userID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		data.Checked = append(data.Checked, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = tx.Query(`SELECT account_id,as_of,expected_num,expected_denom,ledger_hash,created_at FROM account_reconciliations WHERE user_id=? ORDER BY id`, userID)
	if err != nil {
		return err
	}
	hashes := []string{}
	for rows.Next() {
		var item backupReconciliation
		var hash string
		if err := rows.Scan(&item.AccountID, &item.Date, &item.Num, &item.Denom, &hash, &item.CreatedAt); err != nil {
			rows.Close()
			return err
		}
		data.Reconciliations = append(data.Reconciliations, item)
		hashes = append(hashes, hash)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for i, item := range data.Reconciliations {
		out, err := reconciliationState(tx, userID, item.AccountID, item.Date, amount.Decimal(item.Num, item.Denom), item.Denom)
		if err != nil {
			return err
		}
		data.Reconciliations[i].Stale = out.hash != hashes[i]
	}
	return nil
}
func restoreLedgerBackup(tx *sql.Tx, userID int64, data *backupData, accounts, transactions, commodities map[int64]int64) error {
	// All references are mapped through this backup's own entities, never through
	// live IDs supplied by a file. Missing historical accounts remain unrestorable.
	remap := func(original *transactionSnapshot) (*transactionSnapshot, error) {
		if original == nil {
			return nil, nil
		}
		s := *original
		s.Splits = append([]historySplit(nil), original.Splits...)
		if s.Date.IsZero() || s.Entered.IsZero() || s.Date.Year() < 1000 || s.Date.Year() > 9999 || s.Entered.Year() < 1000 || s.Entered.Year() > 9999 {
			return nil, validationError("Некорректная дата в истории")
		}
		if err := validateTransactionComment(&s.Comment); err != nil {
			return nil, err
		}
		for i, p := range s.Splits {
			if _, _, err := amount.Normalize(p.Num, p.Denom); err != nil {
				return nil, validationError(err.Error())
			}
			s.Splits[i].AccountID = accounts[p.AccountID]
			s.Splits[i].CommodityID = commodities[p.CommodityID]
			s.Splits[i].Amount = amount.Decimal(p.Num, p.Denom)
		}
		return &s, nil
	}
	for _, item := range data.History {
		switch item.Action {
		case "create", "update", "delete", "restore":
		default:
			return validationError("Неизвестное действие в истории")
		}
		switch item.Source {
		case "ui", "api", "mcp", "import":
		default:
			return validationError("Неизвестный источник истории")
		}
		if item.CreatedAt.IsZero() {
			return validationError("Отсутствует дата истории")
		}
		before, err := remap(item.Before)
		if err != nil {
			return err
		}
		after, err := remap(item.After)
		if err != nil {
			return err
		}
		if item.Action == "delete" && (before == nil || len(before.Splits) == 0) {
			return validationError("Отсутствуют проводки удалённой операции")
		}
		restored := transactions[item.RestoredID]
		if item.RestoredID != 0 && restored == 0 {
			restored = -1
		}
		b, _ := json.Marshal(before)
		a, _ := json.Marshal(after)
		if _, err := tx.Exec(`INSERT INTO transaction_history(user_id,transaction_id,action,source,created_at,before_json,after_json,restored_id) VALUES(?,?,?,?,?,?,?,?)`, userID, transactions[item.TransactionID], item.Action, item.Source, item.CreatedAt, string(b), string(a), restored); err != nil {
			return err
		}
	}
	seen := map[int64]bool{}
	for _, oldID := range data.Checked {
		id := transactions[oldID]
		if id == 0 || seen[id] {
			return validationError("Некорректная отметка проверки в копии")
		}
		seen[id] = true
		if _, err := tx.Exec(`INSERT INTO transaction_checks(user_id,transaction_id,checked_at) VALUES(?,?,?)`, userID, id, time.Now().UTC()); err != nil {
			return err
		}
	}
	for _, item := range data.Reconciliations {
		id := accounts[item.AccountID]
		if id == 0 || item.Date.IsZero() || item.Date.Year() < 1000 || item.Date.Year() > 9999 || item.CreatedAt.IsZero() {
			return validationError("Некорректная сверка в копии")
		}
		if _, err := amount.Digits(item.Denom); err != nil {
			return validationError(err.Error())
		}
		out, err := reconciliationState(tx, userID, id, item.Date, amount.Decimal(item.Num, item.Denom), item.Denom)
		if err != nil {
			return err
		}
		hash := out.hash
		if item.Stale || !out.Matches {
			hash = strings.Repeat("0", 64)
		}
		if _, err := tx.Exec(`INSERT INTO account_reconciliations(user_id,account_id,as_of,expected_num,expected_denom,ledger_hash,created_at) VALUES(?,?,?,?,?,?,?)`, userID, id, item.Date, item.Num, item.Denom, hash, item.CreatedAt); err != nil {
			return err
		}
	}
	return nil
}
