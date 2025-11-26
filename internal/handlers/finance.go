package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/evbogdanov/finforme/internal/models"
	"github.com/gorilla/mux"
)

// FinanceIndex - главная страница финансов
func (h *Handler) FinanceIndex(w http.ResponseWriter, r *http.Request) {
	userID, _ := h.getUserID(r)

	// Получаем все счета пользователя
	rows, err := h.db.Query(`
		SELECT a.id, a.name, a.account_type, a.commodity_id, a.commodity_scu,
		       a.non_std_scu, a.parent_id, a.code, a.description, a.hidden, a.placeholder,
		       COALESCE(SUM(s.value_num), 0) as balance
		FROM accounts a
		LEFT JOIN splits s ON a.id = s.account_id
		WHERE a.user_id = ?
		GROUP BY a.id
	`, userID)

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	accounts := make([]*models.Account, 0)
	accountsMap := make(map[int64]*models.Account)

	for rows.Next() {
		var acc models.Account
		var balance int64
		var parentID sql.NullInt64
		var code, description sql.NullString

		err := rows.Scan(&acc.ID, &acc.Name, &acc.AccountType, &acc.CommodityID,
			&acc.CommoditySCU, &acc.NonStdSCU, &parentID, &code, &description,
			&acc.Hidden, &acc.Placeholder, &balance)

		if err != nil {
			fmt.Printf("ERROR scanning account: %v\n", err)
			continue
		}

		if parentID.Valid {
			acc.ParentID = &parentID.Int64
		}
		if code.Valid {
			acc.Code = code.String
		}
		if description.Valid {
			acc.Description = description.String
		}

		// Вычисляем баланс с учетом типа счета
		fraction := 100.0
		acc.Balance = float64(balance) / fraction
		if acc.IsNegativeBalance() {
			acc.Balance = -acc.Balance
		}

		accounts = append(accounts, &acc)
		accountsMap[acc.ID] = &acc
	}

	// Если нет счетов, показываем welcome страницу
	if len(accounts) == 0 {
		data := map[string]interface{}{
			"Title":         "finfor.me",
			"Authenticated": true,
		}
		h.renderTemplate(w, "finance_welcome.html", data)
		return
	}

	// Получаем валюты
	commodities, _ := h.getCommodities()

	data := map[string]interface{}{
		"Title":         "finfor.me",
		"Accounts":      accounts,
		"Commodities":   commodities,
		"Authenticated": true,
	}

	h.renderTemplate(w, "finance.html", data)
}

// FinanceAccountView - просмотр транзакций счета
func (h *Handler) FinanceAccountView(w http.ResponseWriter, r *http.Request) {
	userID, _ := h.getUserID(r)
	vars := mux.Vars(r)
	accountIDStr := vars["id"]

	accountID, err := strconv.ParseInt(accountIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid account ID", http.StatusBadRequest)
		return
	}

	// Получаем информацию о счете
	var account models.Account
	var parentID sql.NullInt64
	var code, description sql.NullString
	err = h.db.QueryRow(`
		SELECT id, name, account_type, commodity_id, commodity_scu, non_std_scu,
		       parent_id, code, description, hidden, placeholder
		FROM accounts WHERE id = ? AND user_id = ?
	`, accountID, userID).Scan(&account.ID, &account.Name, &account.AccountType,
		&account.CommodityID, &account.CommoditySCU, &account.NonStdSCU,
		&parentID, &code, &description, &account.Hidden, &account.Placeholder)

	if err != nil {
		fmt.Printf("ERROR loading account %d: %v\n", accountID, err)
		http.Error(w, "Account not found", http.StatusNotFound)
		return
	}

	if parentID.Valid {
		account.ParentID = &parentID.Int64
	}
	if code.Valid {
		account.Code = code.String
	}
	if description.Valid {
		account.Description = description.String
	}

	// Получаем транзакции счета
	transactions := h.getAccountTransactions(userID, accountID)
	accounts, _ := h.getAccounts(userID)
	commodities, _ := h.getCommodities()

	data := map[string]interface{}{
		"Title":         account.Name,
		"Account":       account,
		"Transactions":  transactions,
		"Accounts":      accounts,
		"Commodities":   commodities,
		"Authenticated": true,
	}

	h.renderTemplate(w, "finance_transactions.html", data)
}

// FinanceAccountEdit - создание/редактирование счета
func (h *Handler) FinanceAccountEdit(w http.ResponseWriter, r *http.Request) {
	userID, _ := h.getUserID(r)
	vars := mux.Vars(r)
	accountIDStr := vars["id"]

	accounts, err := h.getAccounts(userID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get accounts: %v", err), http.StatusInternalServerError)
		return
	}

	commodities, err := h.getCommodities()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get commodities: %v", err), http.StatusInternalServerError)
		return
	}

	if accountIDStr == "" {
		// Создание нового счета
		data := map[string]interface{}{
			"Title":         "Новый счет",
			"Accounts":      accounts,
			"Commodities":   commodities,
			"Authenticated": true,
		}
		h.renderTemplate(w, "finance_account.html", data)
		return
	}

	// Редактирование существующего счета
	accountID, err := strconv.ParseInt(accountIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid account ID", http.StatusBadRequest)
		return
	}

	var account models.Account
	var parentID sql.NullInt64
	var code, description sql.NullString
	err = h.db.QueryRow(`
		SELECT id, name, account_type, commodity_id, commodity_scu, non_std_scu,
		       parent_id, code, description, hidden, placeholder
		FROM accounts WHERE id = ? AND user_id = ?
	`, accountID, userID).Scan(&account.ID, &account.Name, &account.AccountType,
		&account.CommodityID, &account.CommoditySCU, &account.NonStdSCU,
		&parentID, &code, &description, &account.Hidden, &account.Placeholder)

	if err != nil {
		fmt.Printf("ERROR loading account for edit %d: %v\n", accountID, err)
		http.Error(w, "Account not found", http.StatusNotFound)
		return
	}

	if parentID.Valid {
		account.ParentID = &parentID.Int64
	}
	if code.Valid {
		account.Code = code.String
	}
	if description.Valid {
		account.Description = description.String
	}

	data := map[string]interface{}{
		"Title":         "Редактирование счета",
		"Account":       account,
		"Accounts":      accounts,
		"Commodities":   commodities,
		"Authenticated": true,
	}

	h.renderTemplate(w, "finance_account.html", data)
}

// FinanceTransaction - просмотр/редактирование транзакции
func (h *Handler) FinanceTransaction(w http.ResponseWriter, r *http.Request) {
	userID, _ := h.getUserID(r)
	vars := mux.Vars(r)
	accountIDStr := vars["account_id"]
	txIDStr := vars["tx_id"]

	accountID, _ := strconv.ParseInt(accountIDStr, 10, 64)

	var transaction *models.Transaction
	var debit, credit []map[string]interface{}

	if txIDStr != "" {
		txID, _ := strconv.ParseInt(txIDStr, 10, 64)
		transaction, debit, credit = h.getTransaction(userID, txID)
	}

	accounts, _ := h.getAccounts(userID)

	data := map[string]interface{}{
		"Title":         "Транзакция",
		"Transaction":   transaction,
		"Debit":         debit,
		"Credit":        credit,
		"AccountID":     accountID,
		"Accounts":      accounts,
		"Authenticated": true,
	}

	h.renderTemplate(w, "finance_transaction.html", data)
}

// FinanceTransactionsByTag - транзакции по тегу
func (h *Handler) FinanceTransactionsByTag(w http.ResponseWriter, r *http.Request) {
	userID, _ := h.getUserID(r)
	vars := mux.Vars(r)
	tag := vars["tag"]

	rows, err := h.db.Query(`
		SELECT t.id, t.description, t.post_date, t.tags,
		       a.id, a.name, a.account_type, a.commodity_id,
		       s.id, s.value_num, s.value_denom
		FROM transactions t
		LEFT JOIN splits s ON t.id = s.tx_id
		LEFT JOIN accounts a ON s.account_id = a.id
		WHERE t.user_id = ? AND t.tags LIKE ?
		ORDER BY t.post_date DESC
	`, userID, "%"+tag+"%")

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	transactionsMap := make(map[int64]map[string]interface{})

	for rows.Next() {
		var txID, accountID, splitID, valueNum, valueDenom int64
		var description, tags, accountName, accountType string
		var postDate time.Time
		var commodityID int64

		rows.Scan(&txID, &description, &postDate, &tags, &accountID, &accountName,
			&accountType, &commodityID, &splitID, &valueNum, &valueDenom)

		if _, exists := transactionsMap[txID]; !exists {
			transactionsMap[txID] = map[string]interface{}{
				"id":          txID,
				"description": description,
				"post_date":   postDate.Format("02.01.2006"),
				"tags":        strings.Split(tags, ","),
				"splits":      []map[string]interface{}{},
			}
		}

		split := map[string]interface{}{
			"account_id":   accountID,
			"account_name": accountName,
			"value":        float64(valueNum) / float64(valueDenom),
		}

		splits := transactionsMap[txID]["splits"].([]map[string]interface{})
		transactionsMap[txID]["splits"] = append(splits, split)
	}

	transactions := make([]map[string]interface{}, 0, len(transactionsMap))
	for _, tx := range transactionsMap {
		transactions = append(transactions, tx)
	}

	data := map[string]interface{}{
		"Title":         fmt.Sprintf("Транзакции с тегом: %s", tag),
		"Tag":           tag,
		"Transactions":  transactions,
		"Authenticated": true,
	}

	h.renderTemplate(w, "finance_transactions_by_tag.html", data)
}

// FinanceSettings - настройки
func (h *Handler) FinanceSettings(w http.ResponseWriter, r *http.Request) {
	data := map[string]interface{}{
		"Title":         "Настройки",
		"Authenticated": true,
	}

	h.renderTemplate(w, "finance_settings.html", data)
}

// Вспомогательные функции

func (h *Handler) getAccounts(userID int64) ([]*models.Account, error) {
	rows, err := h.db.Query(`
		SELECT id, name, account_type, commodity_id, commodity_scu, non_std_scu,
		       parent_id, code, description, hidden, placeholder
		FROM accounts WHERE user_id = ?
		ORDER BY name
	`, userID)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	accounts := make([]*models.Account, 0)

	for rows.Next() {
		var acc models.Account
		var parentID sql.NullInt64
		var code, description sql.NullString

		err := rows.Scan(&acc.ID, &acc.Name, &acc.AccountType, &acc.CommodityID,
			&acc.CommoditySCU, &acc.NonStdSCU, &parentID, &code, &description,
			&acc.Hidden, &acc.Placeholder)

		if err != nil {
			fmt.Printf("ERROR scanning account in getAccounts: %v\n", err)
			continue
		}

		if parentID.Valid {
			acc.ParentID = &parentID.Int64
		}
		if code.Valid {
			acc.Code = code.String
		}
		if description.Valid {
			acc.Description = description.String
		}

		accounts = append(accounts, &acc)
	}

	return accounts, nil
}

func (h *Handler) getCommodities() ([]*models.Commodity, error) {
	rows, err := h.db.Query(`
		SELECT id, namespace, mnemonic, fullname, cusip, fraction, quote_source, quote_tz, sign
		FROM commodities
	`)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	commodities := make([]*models.Commodity, 0)

	for rows.Next() {
		var c models.Commodity
		var cusip, quoteSource, quoteTZ sql.NullString

		err := rows.Scan(&c.ID, &c.Namespace, &c.Mnemonic, &c.Fullname, &cusip,
			&c.Fraction, &quoteSource, &quoteTZ, &c.Sign)

		if err != nil {
			fmt.Printf("ERROR scanning commodity: %v\n", err)
			continue
		}

		// Преобразуем NullString в обычные строки
		if cusip.Valid {
			c.Cusip = cusip.String
		}
		if quoteSource.Valid {
			c.QuoteSource = quoteSource.String
		}
		if quoteTZ.Valid {
			c.QuoteTZ = quoteTZ.String
		}

		commodities = append(commodities, &c)
	}

	return commodities, nil
}

func (h *Handler) getAccountTransactions(userID, accountID int64) []map[string]interface{} {
	rows, err := h.db.Query(`
		SELECT t.id, t.description, t.post_date, t.tags,
		       s.id, s.account_id, s.value_num, s.value_denom,
		       a.name
		FROM transactions t
		JOIN splits s ON t.id = s.tx_id
		LEFT JOIN accounts a ON s.account_id = a.id
		WHERE t.user_id = ? AND t.id IN (
			SELECT tx_id FROM splits WHERE account_id = ? AND user_id = ?
		)
		ORDER BY t.post_date DESC
	`, userID, accountID, userID)

	if err != nil {
		return []map[string]interface{}{}
	}
	defer rows.Close()

	transactionsMap := make(map[int64]map[string]interface{})

	for rows.Next() {
		var txID, splitID, splitAccountID, valueNum, valueDenom int64
		var description, tags, accountName string
		var postDate time.Time

		rows.Scan(&txID, &description, &postDate, &tags, &splitID, &splitAccountID,
			&valueNum, &valueDenom, &accountName)

		if _, exists := transactionsMap[txID]; !exists {
			transactionsMap[txID] = map[string]interface{}{
				"id":          txID,
				"description": description,
				"post_date":   postDate.Format("02.01.2006"),
				"tags":        strings.Split(tags, ","),
			}
		}

		if splitAccountID == accountID {
			value := float64(valueNum) / float64(valueDenom)
			transactionsMap[txID]["balance_changing"] = value
		} else {
			transactionsMap[txID]["account_name"] = accountName
			transactionsMap[txID]["account_id"] = splitAccountID
		}
	}

	transactions := make([]map[string]interface{}, 0, len(transactionsMap))
	for _, tx := range transactionsMap {
		transactions = append(transactions, tx)
	}

	return transactions
}

func (h *Handler) getTransaction(userID, txID int64) (*models.Transaction, []map[string]interface{}, []map[string]interface{}) {
	var tx models.Transaction
	err := h.db.QueryRow(`
		SELECT id, description, post_date, enter_date, tags, currency_id
		FROM transactions WHERE id = ? AND user_id = ?
	`, txID, userID).Scan(&tx.ID, &tx.Description, &tx.PostDate, &tx.EnterDate, &tx.Tags, &tx.CurrencyID)

	if err != nil {
		return nil, nil, nil
	}

	rows, err := h.db.Query(`
		SELECT s.id, s.account_id, s.value_num, s.value_denom, a.name
		FROM splits s
		LEFT JOIN accounts a ON s.account_id = a.id
		WHERE s.tx_id = ? AND s.user_id = ?
	`, txID, userID)

	if err != nil {
		return &tx, nil, nil
	}
	defer rows.Close()

	debit := []map[string]interface{}{}
	credit := []map[string]interface{}{}

	for rows.Next() {
		var splitID, accountID, valueNum, valueDenom int64
		var accountName string

		rows.Scan(&splitID, &accountID, &valueNum, &valueDenom, &accountName)

		value := float64(valueNum) / float64(valueDenom)
		split := map[string]interface{}{
			"id":           splitID,
			"account_id":   accountID,
			"account_name": accountName,
			"value":        value,
		}

		if value < 0 {
			split["value"] = -value
			credit = append(credit, split)
		} else {
			debit = append(debit, split)
		}
	}

	return &tx, debit, credit
}

// APIAccountsGet - получение списка счетов (API)
func (h *Handler) APIAccountsGet(w http.ResponseWriter, r *http.Request) {
	userID, _ := h.getUserID(r)
	accounts, err := h.getAccounts(userID)

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(accounts)
}

// APIAccountSave - сохранение счета (создание или обновление)
func (h *Handler) APIAccountSave(w http.ResponseWriter, r *http.Request) {
	userID, _ := h.getUserID(r)

	// Парсим форму
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	// Получаем данные из формы
	idStr := r.FormValue("id")
	name := r.FormValue("account_name")
	description := r.FormValue("account_description")
	accountType := r.FormValue("account_type")
	commodityIDStr := r.FormValue("commodity_id")
	parentIDStr := r.FormValue("account_parent")

	if name == "" || accountType == "" || commodityIDStr == "" {
		http.Error(w, "Missing required fields", http.StatusBadRequest)
		return
	}

	commodityID, err := strconv.ParseInt(commodityIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid commodity ID", http.StatusBadRequest)
		return
	}

	var parentID sql.NullInt64
	if parentIDStr != "" {
		pid, err := strconv.ParseInt(parentIDStr, 10, 64)
		if err == nil {
			parentID = sql.NullInt64{Int64: pid, Valid: true}
		}
	}

	w.Header().Set("Content-Type", "application/json")

	if idStr == "" {
		// Создание нового счета
		result, err := h.db.Exec(`
			INSERT INTO accounts (user_id, name, account_type, commodity_id, commodity_scu,
			                      non_std_scu, parent_id, description, hidden, placeholder)
			VALUES (?, ?, ?, ?, 100, 0, ?, ?, 0, 0)
		`, userID, name, accountType, commodityID, parentID, description)

		if err != nil {
			fmt.Printf("ERROR creating account: %v\n", err)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		accountID, _ := result.LastInsertId()
		json.NewEncoder(w).Encode(map[string]interface{}{
			"result": "ok",
			"id":     accountID,
		})
	} else {
		// Обновление существующего счета
		accountID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			http.Error(w, "Invalid account ID", http.StatusBadRequest)
			return
		}

		_, err = h.db.Exec(`
			UPDATE accounts
			SET name = ?, account_type = ?, commodity_id = ?, parent_id = ?, description = ?
			WHERE id = ? AND user_id = ?
		`, name, accountType, commodityID, parentID, description, accountID, userID)

		if err != nil {
			fmt.Printf("ERROR updating account: %v\n", err)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"result": "ok",
			"id":     accountID,
		})
	}
}

func (h *Handler) APIAccountDelete(w http.ResponseWriter, r *http.Request) {
	userID, _ := h.getUserID(r)

	// Получаем ID счета из параметров запроса
	accountIDStr := r.URL.Query().Get("id")
	if accountIDStr == "" {
		http.Error(w, "Missing account ID", http.StatusBadRequest)
		return
	}

	accountID, err := strconv.ParseInt(accountIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid account ID", http.StatusBadRequest)
		return
	}

	// Начинаем транзакцию
	tx, err := h.db.Begin()
	if err != nil {
		fmt.Printf("ERROR starting transaction: %v\n", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	// Сначала удаляем все splits, связанные со счетом
	_, err = tx.Exec("DELETE FROM splits WHERE account_id = ? AND user_id = ?", accountID, userID)
	if err != nil {
		fmt.Printf("ERROR deleting splits: %v\n", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Затем удаляем сам счет
	result, err := tx.Exec("DELETE FROM accounts WHERE id = ? AND user_id = ?", accountID, userID)
	if err != nil {
		fmt.Printf("ERROR deleting account: %v\n", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Проверяем, был ли удален счет
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		http.Error(w, "Account not found", http.StatusNotFound)
		return
	}

	// Коммитим транзакцию
	if err := tx.Commit(); err != nil {
		fmt.Printf("ERROR committing transaction: %v\n", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Возвращаем пустой ответ для HTMX
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) APITransactionsGet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode([]interface{}{})
}

func (h *Handler) APITransactionSave(w http.ResponseWriter, r *http.Request) {
	userID, _ := h.getUserID(r)

	// Парсим форму
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	// Получаем данные из формы
	description := r.FormValue("description")
	postDateStr := r.FormValue("post_date")
	tags := r.FormValue("tags")
	valueStr := r.FormValue("value")
	debitAccountStr := r.FormValue("debit_account")
	creditAccountStr := r.FormValue("credit_account")

	if description == "" || postDateStr == "" || valueStr == "" || debitAccountStr == "" || creditAccountStr == "" {
		http.Error(w, "Missing required fields", http.StatusBadRequest)
		return
	}

	// Парсим дату
	postDate, err := time.Parse("2006-01-02", postDateStr)
	if err != nil {
		http.Error(w, "Invalid date format", http.StatusBadRequest)
		return
	}

	// Парсим сумму
	value, err := strconv.ParseFloat(valueStr, 64)
	if err != nil {
		http.Error(w, "Invalid value", http.StatusBadRequest)
		return
	}

	// Парсим ID счетов
	debitAccountID, err := strconv.ParseInt(debitAccountStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid debit account ID", http.StatusBadRequest)
		return
	}

	creditAccountID, err := strconv.ParseInt(creditAccountStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid credit account ID", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	// Начинаем транзакцию
	tx, err := h.db.Begin()
	if err != nil {
		fmt.Printf("ERROR starting transaction: %v\n", err)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	defer tx.Rollback()

	// Создаем транзакцию
	result, err := tx.Exec(`
		INSERT INTO transactions (user_id, currency_id, post_date, enter_date, description, tags)
		VALUES (?, 1, ?, ?, ?, ?)
	`, userID, postDate, time.Now(), description, tags)

	if err != nil {
		fmt.Printf("ERROR creating transaction: %v\n", err)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	txID, _ := result.LastInsertId()

	// Конвертируем сумму в целые числа (value_num / value_denom)
	valueNum := int64(value * 100)
	valueDenom := int64(100)

	// Создаем split для дебета (положительное значение)
	_, err = tx.Exec(`
		INSERT INTO splits (user_id, tx_id, account_id, value_num, value_denom)
		VALUES (?, ?, ?, ?, ?)
	`, userID, txID, debitAccountID, valueNum, valueDenom)

	if err != nil {
		fmt.Printf("ERROR creating debit split: %v\n", err)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Создаем split для кредита (отрицательное значение)
	_, err = tx.Exec(`
		INSERT INTO splits (user_id, tx_id, account_id, value_num, value_denom)
		VALUES (?, ?, ?, ?, ?)
	`, userID, txID, creditAccountID, -valueNum, valueDenom)

	if err != nil {
		fmt.Printf("ERROR creating credit split: %v\n", err)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Коммитим транзакцию
	if err := tx.Commit(); err != nil {
		fmt.Printf("ERROR committing transaction: %v\n", err)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"result": "ok",
		"id":     txID,
	})
}

func (h *Handler) APITransactionDelete(w http.ResponseWriter, r *http.Request) {
	userID, _ := h.getUserID(r)

	// Получаем ID транзакции из параметров запроса
	txIDStr := r.URL.Query().Get("id")
	if txIDStr == "" {
		http.Error(w, "Missing transaction ID", http.StatusBadRequest)
		return
	}

	txID, err := strconv.ParseInt(txIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid transaction ID", http.StatusBadRequest)
		return
	}

	// Начинаем транзакцию
	tx, err := h.db.Begin()
	if err != nil {
		fmt.Printf("ERROR starting transaction: %v\n", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	// Сначала удаляем все splits, связанные с транзакцией
	_, err = tx.Exec("DELETE FROM splits WHERE tx_id = ? AND user_id = ?", txID, userID)
	if err != nil {
		fmt.Printf("ERROR deleting splits: %v\n", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Затем удаляем саму транзакцию
	result, err := tx.Exec("DELETE FROM transactions WHERE id = ? AND user_id = ?", txID, userID)
	if err != nil {
		fmt.Printf("ERROR deleting transaction: %v\n", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Проверяем, была ли удалена транзакция
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		http.Error(w, "Transaction not found", http.StatusNotFound)
		return
	}

	// Коммитим транзакцию
	if err := tx.Commit(); err != nil {
		fmt.Printf("ERROR committing transaction: %v\n", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Возвращаем пустой ответ для HTMX
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) APIExportJSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{})
}

func (h *Handler) APIDataDelete(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"result": "ok"})
}

func (h *Handler) APIWelcomeCreateEmpty(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"result": "ok"})
}

func (h *Handler) APIWelcomeCreateBase(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"result": "ok"})
}

func (h *Handler) APIImportJSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"result": "ok"})
}

// APIImportGnuCash handles GnuCash SQLite database import
func (h *Handler) APIImportGnuCash(w http.ResponseWriter, r *http.Request) {
	userID, authenticated := h.getUserID(r)
	if !authenticated {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"result": "error", "message": "Not authenticated"})
		return
	}

	if r.Method != http.MethodPost {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"result": "error", "message": "Method not allowed"})
		return
	}

	// Parse multipart form (max 32MB)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"result": "error", "message": "Failed to parse form"})
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"result": "error", "message": "Failed to get file"})
		return
	}
	defer file.Close()

	// Create temp directory if not exists
	if err := os.MkdirAll("temp", 0755); err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"result": "error", "message": "Failed to create temp directory"})
		return
	}

	// Save uploaded file
	tempFile := fmt.Sprintf("temp/%d_gnucash.sqlite", userID)
	dst, err := os.Create(tempFile)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"result": "error", "message": "Failed to create temp file"})
		return
	}
	defer dst.Close()
	defer os.Remove(tempFile)

	if _, err := io.Copy(dst, file); err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"result": "error", "message": "Failed to save file"})
		return
	}

	// Import from GnuCash database
	if err := h.importFromGnuCash(userID, tempFile); err != nil {
		log.Printf("Error importing GnuCash data: %v", err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"result": "error", "message": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"result": "ok"})
}

// importFromGnuCash imports data from GnuCash SQLite database
func (h *Handler) importFromGnuCash(userID int64, filename string) error {
	// Open GnuCash database
	importDB, err := sql.Open("sqlite3", filename)
	if err != nil {
		return fmt.Errorf("failed to open GnuCash database: %w", err)
	}
	defer importDB.Close()

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	// Map old IDs to new IDs
	accountMap := make(map[string]int64)
	commodityMap := make(map[string]int64)

	// Get existing commodities from our database
	existingCommodities := make(map[string]int64)
	commodityRows, err := h.db.Query("SELECT id, mnemonic FROM commodities")
	if err != nil {
		return fmt.Errorf("failed to query existing commodities: %w", err)
	}
	defer commodityRows.Close()

	for commodityRows.Next() {
		var id int64
		var mnemonic string
		if err := commodityRows.Scan(&id, &mnemonic); err != nil {
			return fmt.Errorf("failed to scan commodity: %w", err)
		}
		existingCommodities[mnemonic] = id
	}

	// Import commodities mapping from GnuCash
	// First, let's check what columns exist in the commodities table
	columnsRows, err := importDB.Query("PRAGMA table_info(commodities)")
	if err != nil {
		return fmt.Errorf("failed to get commodities table info: %w", err)
	}

	var columns []string
	for columnsRows.Next() {
		var cid int
		var name, ctype string
		var notnull, dfltValue, pk interface{}
		if err := columnsRows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			columnsRows.Close()
			return fmt.Errorf("failed to scan column info: %w", err)
		}
		columns = append(columns, name)
	}
	columnsRows.Close()

	if len(columns) == 0 {
		return fmt.Errorf("commodities table has no columns or doesn't exist")
	}

	// Try to find guid-like and mnemonic-like columns
	guidCol := ""
	mnemonicCol := ""
	for _, col := range columns {
		colLower := strings.ToLower(col)
		// Look for id or guid columns
		if colLower == "id" || colLower == "guid" || strings.Contains(colLower, "guid") {
			guidCol = col
		}
		// Look for mnemonic, code, or symbol columns
		if colLower == "mnemonic" || colLower == "code" || colLower == "symbol" {
			mnemonicCol = col
		}
	}

	if guidCol == "" || mnemonicCol == "" {
		return fmt.Errorf("could not find required columns in commodities table. Available columns: %v (looking for id/guid and mnemonic/code/symbol)", columns)
	}

	// Now query with the correct column names
	query := fmt.Sprintf("SELECT %s, %s FROM commodities", guidCol, mnemonicCol)
	rows, err := importDB.Query(query)
	if err != nil {
		return fmt.Errorf("failed to query commodities: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var guid, mnemonic string

		if err := rows.Scan(&guid, &mnemonic); err != nil {
			return fmt.Errorf("failed to scan import commodity: %w", err)
		}

		if id, ok := existingCommodities[mnemonic]; ok {
			commodityMap[guid] = id
		} else {
			commodityMap[guid] = 1 // Default to first commodity
		}
	}

	// Import accounts (with hierarchy support)
	accountRows, err := importDB.Query(`
		SELECT guid, name, account_type, commodity_guid, commodity_scu, non_std_scu,
		       parent_guid, code, description, hidden, placeholder
		FROM accounts
	`)
	if err != nil {
		return fmt.Errorf("failed to query accounts: %w", err)
	}
	defer accountRows.Close()

	type gnucashAccount struct {
		guid          string
		name          string
		accountType   string
		commodityGuid sql.NullString
		commodityScu  sql.NullInt64
		nonStdScu     sql.NullInt64
		parentGuid    sql.NullString
		code          sql.NullString
		description   sql.NullString
		hidden        sql.NullInt64
		placeholder   sql.NullInt64
	}

	var accounts []gnucashAccount
	for accountRows.Next() {
		var acc gnucashAccount
		if err := accountRows.Scan(&acc.guid, &acc.name, &acc.accountType,
			&acc.commodityGuid, &acc.commodityScu, &acc.nonStdScu,
			&acc.parentGuid, &acc.code, &acc.description,
			&acc.hidden, &acc.placeholder); err != nil {
			return fmt.Errorf("failed to scan account: %w", err)
		}
		accounts = append(accounts, acc)
	}

	// Import accounts in multiple passes to handle hierarchy
	maxPasses := 10
	for pass := 0; pass < maxPasses && len(accounts) > 0; pass++ {
		remaining := []gnucashAccount{}
		for _, acc := range accounts {
			// Check if parent exists or is null
			canImport := !acc.parentGuid.Valid || accountMap[acc.parentGuid.String] != 0

			if canImport {
				commodityID := int64(1)
				if acc.commodityGuid.Valid {
					if id, ok := commodityMap[acc.commodityGuid.String]; ok {
						commodityID = id
					}
				}

				var parentID sql.NullInt64
				if acc.parentGuid.Valid && accountMap[acc.parentGuid.String] != 0 {
					parentID.Valid = true
					parentID.Int64 = accountMap[acc.parentGuid.String]
				}

				result, err := tx.Exec(`
					INSERT INTO accounts (user_id, name, type, commodity_id, commodity_scu,
					                     non_std_scu, parent_id, code, description, hidden, placeholder)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				`, userID, acc.name, acc.accountType, commodityID,
					acc.commodityScu, acc.nonStdScu, parentID,
					acc.code, acc.description, acc.hidden, acc.placeholder)

				if err != nil {
					return fmt.Errorf("failed to insert account: %w", err)
				}

				newID, _ := result.LastInsertId()
				accountMap[acc.guid] = newID
			} else {
				remaining = append(remaining, acc)
			}
		}
		accounts = remaining
	}

	if len(accounts) > 0 {
		return fmt.Errorf("failed to import %d accounts due to unresolved parent references", len(accounts))
	}

	// Import transactions
	txRows, err := importDB.Query(`
		SELECT guid, currency_guid, num, post_date, enter_date, description
		FROM transactions
	`)
	if err != nil {
		return fmt.Errorf("failed to query transactions: %w", err)
	}
	defer txRows.Close()

	transactionMap := make(map[string]int64)
	for txRows.Next() {
		var guid, currencyGuid, num, postDate, enterDate, description string
		if err := txRows.Scan(&guid, &currencyGuid, &num, &postDate, &enterDate, &description); err != nil {
			return fmt.Errorf("failed to scan transaction: %w", err)
		}

		// Skip transactions without description
		if description == "" {
			continue
		}

		// Parse dates (GnuCash format: YYYYMMDDHHMMSS or YYYY-MM-DD HH:MM:SS)
		var postTime, enterTime time.Time
		if strings.Contains(postDate, "-") {
			postTime, _ = time.Parse("2006-01-02 15:04:05", postDate)
			enterTime, _ = time.Parse("2006-01-02 15:04:05", enterDate)
		} else {
			postTime, _ = time.Parse("20060102150405", postDate)
			enterTime, _ = time.Parse("20060102150405", enterDate)
		}

		result, err := tx.Exec(`
			INSERT INTO transactions (user_id, description, post_date, enter_date)
			VALUES (?, ?, ?, ?)
		`, userID, description, postTime, enterTime)

		if err != nil {
			return fmt.Errorf("failed to insert transaction: %w", err)
		}

		newTxID, _ := result.LastInsertId()
		transactionMap[guid] = newTxID
	}

	// Import splits
	splitRows, err := importDB.Query(`
		SELECT guid, tx_guid, account_guid, memo, action, reconcile_state, reconcile_date,
		       value_num, value_denom, quantity_num, quantity_denom, lot_guid
		FROM splits
	`)
	if err != nil {
		return fmt.Errorf("failed to query splits: %w", err)
	}
	defer splitRows.Close()

	for splitRows.Next() {
		var guid, txGuid, accountGuid string
		var memo, action, reconcileState, reconcileDate, lotGuid sql.NullString
		var valueNum, valueDenom, quantityNum, quantityDenom int64

		if err := splitRows.Scan(&guid, &txGuid, &accountGuid, &memo, &action, &reconcileState, &reconcileDate, &valueNum, &valueDenom, &quantityNum, &quantityDenom, &lotGuid); err != nil {
			return fmt.Errorf("failed to scan split: %w", err)
		}

		// Check if transaction and account exist in maps
		txID, txExists := transactionMap[txGuid]
		accountID, accExists := accountMap[accountGuid]

		if txExists && accExists {
			_, err := tx.Exec(`
				INSERT INTO splits (user_id, tx_id, account_id, value_num, value_denom)
				VALUES (?, ?, ?, ?, ?)
			`, userID, txID, accountID, valueNum, valueDenom)

			if err != nil {
				return fmt.Errorf("failed to insert split: %w", err)
			}
		}
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}
