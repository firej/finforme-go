package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/evbogdanov/finforme/internal/gnucash"
	"github.com/evbogdanov/finforme/internal/models"
)

// Формат резервной копии: полный слепок данных пользователя (счета + транзакции
// со сплитами) в JSON. ID в файле — исходные ID пользователя, при восстановлении
// они переназначаются, а ссылки (parent_id, account_id) переписываются по карте.
const (
	backupFormat        = "finforme-backup"
	backupFormatVersion = 1
	backupMaxUploadSize = 64 << 20 // 64 МБ
)

type backupData struct {
	Format       string              `json:"format"`
	Version      int                 `json:"version"`
	ExportedAt   string              `json:"exported_at"`
	Accounts     []backupAccount     `json:"accounts"`
	Transactions []backupTransaction `json:"transactions"`
}

type backupAccount struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	AccountType  string `json:"account_type"`
	Currency     string `json:"currency"`
	CommoditySCU int    `json:"commodity_scu"`
	NonStdSCU    int    `json:"non_std_scu"`
	ParentID     *int64 `json:"parent_id"`
	Code         string `json:"code"`
	Description  string `json:"description"`
	Hidden       int    `json:"hidden"`
	Placeholder  int    `json:"placeholder"`
}

type backupTransaction struct {
	ID          int64         `json:"id"`
	Num         string        `json:"num"`
	PostDate    string        `json:"post_date"`
	EnterDate   string        `json:"enter_date"`
	Description string        `json:"description"`
	Tags        string        `json:"tags"`
	Splits      []backupSplit `json:"splits"`
}

type backupSplit struct {
	AccountID  int64 `json:"account_id"`
	ValueNum   int64 `json:"value_num"`
	ValueDenom int64 `json:"value_denom"`
}

// backupRestoreResult — сводка по восстановленным данным
type backupRestoreResult struct {
	Accounts     int `json:"accounts"`
	Transactions int `json:"transactions"`
	Splits       int `json:"splits"`
}

// APIExportJSON отдаёт полную резервную копию данных пользователя JSON-файлом.
func (h *Handler) APIExportJSON(w http.ResponseWriter, r *http.Request) {
	userID, authenticated := h.getUserID(r)
	if !authenticated {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	data, err := h.exportBackup(userID)
	if err != nil {
		log.Printf("ERROR building backup for user %d: %v", userID, err)
		http.Error(w, "Не удалось сформировать резервную копию", http.StatusInternalServerError)
		return
	}

	filename := fmt.Sprintf("finforme-backup-%s.json", time.Now().Format("2006-01-02"))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		// Заголовки уже отправлены — остаётся только залогировать
		log.Printf("ERROR writing backup for user %d: %v", userID, err)
	}
}

// exportBackup собирает все счета и транзакции пользователя в структуру резервной копии.
func (h *Handler) exportBackup(userID int64) (*backupData, error) {
	// Share the finance lock so every part belongs to the same snapshot.
	tx, err := h.beginFinanceWrite(userID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	data := &backupData{
		Format:       backupFormat,
		Version:      backupFormatVersion,
		ExportedAt:   time.Now().Format(time.RFC3339),
		Accounts:     []backupAccount{},
		Transactions: []backupTransaction{},
	}

	accountRows, err := tx.Query(`
		SELECT a.id, a.name, a.account_type, COALESCE(c.mnemonic, ''), a.commodity_scu,
		       a.non_std_scu, a.parent_id, a.code, a.description, a.hidden, a.placeholder
		FROM accounts a
		LEFT JOIN commodities c ON c.id = a.commodity_id
		WHERE a.user_id = ?
		ORDER BY a.id
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to query accounts: %w", err)
	}
	defer accountRows.Close()

	exportedAccounts := map[int64]bool{}
	for accountRows.Next() {
		var acc backupAccount
		var parentID sql.NullInt64
		var code, description sql.NullString

		if err := accountRows.Scan(&acc.ID, &acc.Name, &acc.AccountType, &acc.Currency,
			&acc.CommoditySCU, &acc.NonStdSCU, &parentID, &code, &description,
			&acc.Hidden, &acc.Placeholder); err != nil {
			return nil, fmt.Errorf("failed to scan account: %w", err)
		}

		if parentID.Valid {
			pid := parentID.Int64
			acc.ParentID = &pid
		}
		acc.Code = code.String
		acc.Description = description.String

		exportedAccounts[acc.ID] = true
		data.Accounts = append(data.Accounts, acc)
	}
	if err := accountRows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read accounts: %w", err)
	}

	txRows, err := tx.Query(`
		SELECT id, num, post_date, enter_date, description, tags
		FROM transactions
		WHERE user_id = ?
		ORDER BY post_date, id
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to query transactions: %w", err)
	}
	defer txRows.Close()

	txIndex := make(map[int64]int)
	for txRows.Next() {
		var t backupTransaction
		var num, description, tags sql.NullString
		var postDate, enterDate time.Time

		if err := txRows.Scan(&t.ID, &num, &postDate, &enterDate, &description, &tags); err != nil {
			return nil, fmt.Errorf("failed to scan transaction: %w", err)
		}

		t.Num = num.String
		t.Description = description.String
		t.Tags = tags.String
		t.PostDate = postDate.Format(time.RFC3339)
		t.EnterDate = enterDate.Format(time.RFC3339)
		t.Splits = []backupSplit{}

		txIndex[t.ID] = len(data.Transactions)
		data.Transactions = append(data.Transactions, t)
	}
	if err := txRows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read transactions: %w", err)
	}

	splitRows, err := tx.Query(`
		SELECT tx_id, account_id, value_num, value_denom
		FROM splits
		WHERE user_id = ?
		ORDER BY tx_id, id
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to query splits: %w", err)
	}
	defer splitRows.Close()

	for splitRows.Next() {
		var txID int64
		var s backupSplit
		if err := splitRows.Scan(&txID, &s.AccountID, &s.ValueNum, &s.ValueDenom); err != nil {
			return nil, fmt.Errorf("failed to scan split: %w", err)
		}
		if !exportedAccounts[s.AccountID] {
			return nil, fmt.Errorf("split references an account outside the backup")
		}
		idx, ok := txIndex[txID]
		if !ok {
			return nil, fmt.Errorf("orphan split in backup")
		}
		data.Transactions[idx].Splits = append(data.Transactions[idx].Splits, s)
	}
	if err := splitRows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read splits: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return data, nil
}

// APIImportJSON восстанавливает данные из резервной копии.
// Файл принимается либо как multipart-поле "file", либо как тело запроса.
// Параметр mode: "replace" (по умолчанию) — удалить текущие данные перед
// восстановлением, "merge" — добавить к существующим.
func (h *Handler) APIImportJSON(w http.ResponseWriter, r *http.Request) {
	userID, authenticated := h.getUserID(r)
	if !authenticated {
		writeBackupError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, backupMaxUploadSize+(1<<20))
	body, mode, err := readBackupRequest(r)
	if err != nil {
		writeBackupError(w, http.StatusBadRequest, err.Error())
		return
	}

	var data backupData
	if err := json.Unmarshal(body, &data); err != nil {
		writeBackupError(w, http.StatusBadRequest, "Не удалось разобрать JSON: "+err.Error())
		return
	}

	result, err := h.restoreBackup(userID, &data, mode)
	if err != nil {
		var vErr validationError
		if errors.As(err, &vErr) {
			writeBackupError(w, http.StatusBadRequest, vErr.Error())
			return
		}
		log.Printf("ERROR restoring backup for user %d: %v", userID, err)
		writeBackupError(w, http.StatusInternalServerError, "Не удалось восстановить данные")
		return
	}

	log.Printf("User %d restored backup (mode=%s): %d accounts, %d transactions, %d splits",
		userID, mode, result.Accounts, result.Transactions, result.Splits)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"result":       "ok",
		"mode":         mode,
		"accounts":     result.Accounts,
		"transactions": result.Transactions,
		"splits":       result.Splits,
	})
}

// readBackupRequest достаёт содержимое файла резервной копии и режим восстановления.
func readBackupRequest(r *http.Request) ([]byte, string, error) {
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	mode := "replace"

	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			return nil, "", validationError("Не удалось разобрать форму")
		}
		if m := r.FormValue("mode"); m != "" {
			mode = m
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			return nil, "", validationError("Файл резервной копии не выбран")
		}
		defer file.Close()
		if header.Size > backupMaxUploadSize {
			return nil, "", validationError("Файл слишком большой (максимум 64 МБ)")
		}
		body, err := io.ReadAll(io.LimitReader(file, backupMaxUploadSize+1))
		if err != nil {
			return nil, "", validationError("Не удалось прочитать файл")
		}
		if len(body) > backupMaxUploadSize {
			return nil, "", validationError("Файл слишком большой (максимум 64 МБ)")
		}
		mode = normalizeBackupMode(mode)
		if mode == "" {
			return nil, "", validationError("Неизвестный режим восстановления")
		}
		return body, mode, nil
	}

	if m := r.URL.Query().Get("mode"); m != "" {
		mode = m
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, backupMaxUploadSize+1))
	if err != nil {
		return nil, "", validationError("Не удалось прочитать запрос")
	}
	if len(body) > backupMaxUploadSize {
		return nil, "", validationError("Файл слишком большой (максимум 64 МБ)")
	}
	if len(body) == 0 {
		return nil, "", validationError("Пустой запрос: нужен файл резервной копии")
	}
	mode = normalizeBackupMode(mode)
	if mode == "" {
		return nil, "", validationError("Неизвестный режим восстановления")
	}
	return body, mode, nil
}

func normalizeBackupMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "replace":
		return "replace"
	case "merge":
		return "merge"
	default:
		return ""
	}
}

func writeBackupError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{"result": "error", "message": message})
}

// validAccountTypes — типы счетов, допустимые в резервной копии
var validAccountTypes = map[string]bool{
	models.AccountTypeRoot:      true,
	models.AccountTypeAsset:     true,
	models.AccountTypeCash:      true,
	models.AccountTypeBank:      true,
	models.AccountTypeLiability: true,
	models.AccountTypeIncome:    true,
	models.AccountTypeExpense:   true,
	models.AccountTypeEquity:    true,
}

// restoreBackup записывает данные из резервной копии в БД одной транзакцией.
func (h *Handler) restoreBackup(userID int64, data *backupData, mode string) (*backupRestoreResult, error) {
	mode = normalizeBackupMode(mode)
	if mode == "" {
		return nil, validationError("Неизвестный режим восстановления")
	}
	if data == nil {
		return nil, validationError("Пустая резервная копия")
	}
	if data.Format != "" && data.Format != backupFormat {
		return nil, validationError("Это не резервная копия Finforme")
	}
	if data.Version > backupFormatVersion {
		return nil, validationError(fmt.Sprintf(
			"Версия резервной копии (%d) новее поддерживаемой (%d)", data.Version, backupFormatVersion))
	}
	if len(data.Accounts) == 0 && len(data.Transactions) == 0 {
		return nil, validationError("В файле нет данных для восстановления")
	}

	// Предварительная проверка: типы счетов и ссылки внутри файла
	fileAccounts := make(map[int64]bool, len(data.Accounts))
	for _, acc := range data.Accounts {
		if acc.ID <= 0 {
			return nil, validationError("У счёта в файле отсутствует id")
		}
		if fileAccounts[acc.ID] {
			return nil, validationError(fmt.Sprintf("Дублирующийся id счёта в файле: %d", acc.ID))
		}
		if strings.TrimSpace(acc.Name) == "" {
			return nil, validationError(fmt.Sprintf("У счёта %d пустое название", acc.ID))
		}
		if !validAccountTypes[acc.AccountType] {
			return nil, validationError(fmt.Sprintf("Неизвестный тип счёта «%s» у счёта «%s»", acc.AccountType, acc.Name))
		}
		fileAccounts[acc.ID] = true
	}
	for _, acc := range data.Accounts {
		if acc.ParentID != nil && !fileAccounts[*acc.ParentID] {
			return nil, validationError(fmt.Sprintf("Счёт «%s» ссылается на отсутствующий родительский счёт %d", acc.Name, *acc.ParentID))
		}
	}
	seenTransactions := map[int64]bool{}
	for _, t := range data.Transactions {
		if t.ID <= 0 || seenTransactions[t.ID] {
			return nil, validationError("Пустой или повторный id операции")
		}
		seenTransactions[t.ID] = true
		if _, err := parseBackupTime(t.PostDate); err != nil {
			return nil, validationError("Некорректная дата операции")
		}
		if t.EnterDate != "" {
			if _, err := parseBackupTime(t.EnterDate); err != nil {
				return nil, validationError("Некорректная дата создания операции")
			}
		}
		for _, s := range t.Splits {
			if _, _, err := normalizeSplitValue(s.ValueNum, s.ValueDenom); err != nil {
				return nil, validationError(err.Error())
			}
			if !fileAccounts[s.AccountID] {
				return nil, validationError(fmt.Sprintf(
					"Транзакция «%s» ссылается на отсутствующий счёт %d", t.Description, s.AccountID))
			}
		}
	}

	commodityIDs, err := h.commodityIDsByMnemonic()
	if err != nil {
		return nil, err
	}
	defaultCommodityID, err := h.defaultCommodityID()
	if err != nil {
		return nil, err
	}

	tx, err := h.beginFinanceWrite(userID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if mode == "replace" {
		if err := deleteUserFinanceData(tx, userID); err != nil {
			return nil, err
		}
	}

	// Валюты, которых нет в справочнике, добавляем — иначе счета потеряют валюту
	for _, acc := range data.Accounts {
		mnemonic := strings.ToUpper(strings.TrimSpace(acc.Currency))
		if mnemonic == "" || commodityIDs[mnemonic] != 0 {
			continue
		}
		res, err := tx.Exec(`
			INSERT INTO commodities (namespace, mnemonic, fullname, fraction, sign)
			VALUES ('CURRENCY', ?, ?, 100, '')
		`, mnemonic, mnemonic)
		if err != nil {
			return nil, fmt.Errorf("failed to insert commodity %s: %w", mnemonic, err)
		}
		newID, err := res.LastInsertId()
		if err != nil {
			return nil, err
		}
		commodityIDs[mnemonic] = newID
	}

	// Счета вставляем послойно: сначала без родителя, затем те, чей родитель уже создан
	accountIDs := make(map[int64]int64, len(data.Accounts))
	pending := make([]backupAccount, len(data.Accounts))
	copy(pending, data.Accounts)

	for len(pending) > 0 {
		remaining := pending[:0:0]
		for _, acc := range pending {
			var parentID sql.NullInt64
			if acc.ParentID != nil {
				newParentID, ok := accountIDs[*acc.ParentID]
				if !ok {
					remaining = append(remaining, acc)
					continue
				}
				parentID = sql.NullInt64{Int64: newParentID, Valid: true}
			}

			commodityID := defaultCommodityID
			if id := commodityIDs[strings.ToUpper(strings.TrimSpace(acc.Currency))]; id != 0 {
				commodityID = id
			}
			commoditySCU := acc.CommoditySCU
			if commoditySCU <= 0 {
				commoditySCU = models.DefaultDenom
			}

			res, err := tx.Exec(`
				INSERT INTO accounts (user_id, name, account_type, commodity_id, commodity_scu,
				                      non_std_scu, parent_id, code, description, hidden, placeholder)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, userID, acc.Name, acc.AccountType, commodityID, commoditySCU, acc.NonStdSCU,
				parentID, acc.Code, acc.Description, acc.Hidden, acc.Placeholder)
			if err != nil {
				return nil, fmt.Errorf("failed to insert account %q: %w", acc.Name, err)
			}
			newID, err := res.LastInsertId()
			if err != nil {
				return nil, err
			}
			accountIDs[acc.ID] = newID
		}

		if len(remaining) == len(pending) {
			return nil, validationError("В файле обнаружена циклическая иерархия счетов")
		}
		pending = remaining
	}

	result := &backupRestoreResult{Accounts: len(accountIDs)}

	for _, t := range data.Transactions {
		postDate, err := parseBackupTime(t.PostDate)
		if err != nil {
			return nil, validationError(fmt.Sprintf("Некорректная дата «%s» у транзакции «%s»", t.PostDate, t.Description))
		}
		enterDate, err := parseBackupTime(t.EnterDate)
		if err != nil {
			enterDate = postDate
		}

		res, err := tx.Exec(`
			INSERT INTO transactions (user_id, num, post_date, enter_date, description, tags)
			VALUES (?, ?, ?, ?, ?, ?)
		`, userID, t.Num, postDate, enterDate, t.Description, t.Tags)
		if err != nil {
			return nil, fmt.Errorf("failed to insert transaction %q: %w", t.Description, err)
		}
		newTxID, err := res.LastInsertId()
		if err != nil {
			return nil, err
		}
		result.Transactions++

		for _, s := range t.Splits {
			valueNum, valueDenom, err := normalizeSplitValue(s.ValueNum, s.ValueDenom)
			if err != nil {
				return nil, validationError(err.Error())
			}
			if _, err := tx.Exec(`
				INSERT INTO splits (user_id, tx_id, account_id, value_num, value_denom)
				VALUES (?, ?, ?, ?, ?)
			`, userID, newTxID, accountIDs[s.AccountID], valueNum, valueDenom); err != nil {
				return nil, fmt.Errorf("failed to insert split: %w", err)
			}
			result.Splits++
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return result, nil
}

// normalizeSplitValue приводит сумму сплита к знаменателю 100 (балансы считаются как SUM(value_num)/100)
func normalizeSplitValue(valueNum, valueDenom int64) (int64, int64, error) {
	cents, err := gnucash.Cents(valueNum, valueDenom)
	return cents, models.DefaultDenom, err
}

// parseBackupTime разбирает дату из резервной копии в одном из поддерживаемых форматов
func parseBackupTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, fmt.Errorf("empty date")
	}
	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, value); err == nil && t.Year() >= 1000 && t.Year() <= 9999 {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported date format: %s", value)
}

// commodityIDsByMnemonic возвращает справочник валют: мнемоника (в верхнем регистре) -> id
func (h *Handler) commodityIDsByMnemonic() (map[string]int64, error) {
	rows, err := h.db.Query("SELECT id, mnemonic FROM commodities")
	if err != nil {
		return nil, fmt.Errorf("failed to query commodities: %w", err)
	}
	defer rows.Close()

	result := make(map[string]int64)
	for rows.Next() {
		var id int64
		var mnemonic string
		if err := rows.Scan(&id, &mnemonic); err != nil {
			return nil, fmt.Errorf("failed to scan commodity: %w", err)
		}
		result[strings.ToUpper(strings.TrimSpace(mnemonic))] = id
	}
	return result, rows.Err()
}

// defaultCommodityID возвращает валюту по умолчанию для счетов без указанной валюты
func (h *Handler) defaultCommodityID() (int64, error) {
	var id int64
	err := h.db.QueryRow("SELECT id FROM commodities ORDER BY id LIMIT 1").Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("failed to get default commodity: %w", err)
	}
	return id, nil
}

// deleteUserFinanceData удаляет все счета, транзакции и сплиты пользователя
func deleteUserFinanceData(tx *sql.Tx, userID int64) error {
	if _, err := tx.Exec("DELETE FROM splits WHERE user_id = ?", userID); err != nil {
		return fmt.Errorf("failed to delete splits: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM transactions WHERE user_id = ?", userID); err != nil {
		return fmt.Errorf("failed to delete transactions: %w", err)
	}
	// Обнуляем parent_id, иначе удаление упрётся в внешний ключ accounts.parent_id
	if _, err := tx.Exec("UPDATE accounts SET parent_id = NULL WHERE user_id = ?", userID); err != nil {
		return fmt.Errorf("failed to clear parent_id: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM accounts WHERE user_id = ?", userID); err != nil {
		return fmt.Errorf("failed to delete accounts: %w", err)
	}
	return nil
}
