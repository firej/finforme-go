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

	"github.com/evbogdanov/finforme/internal/gnucash"
	"github.com/evbogdanov/finforme/internal/models"
	"github.com/gorilla/mux"
)

// MortgageCalculator - страница ипотечного калькулятора
func (h *Handler) MortgageCalculator(w http.ResponseWriter, r *http.Request) {
	userID, authenticated := h.getUserID(r)

	var data map[string]interface{}
	if authenticated {
		data = h.pageData(userID, "mortgage")
	} else {
		data = map[string]interface{}{"Authenticated": false}
	}
	data["Title"] = "Ипотечный калькулятор"

	h.renderTemplate(w, "mortgage_calculator.html", data)
}

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
		data := h.pageData(userID, "finance")
		data["Title"] = "finfor.me"
		h.renderTemplate(w, "finance_welcome.html", data)
		return
	}

	// Строим дерево счетов
	accountTree := h.buildAccountTree(accounts, accountsMap)

	// Получаем валюты
	commodities, _ := h.getCommodities()

	data := h.pageData(userID, "finance")
	data["Title"] = "finfor.me"
	data["AccountTree"] = accountTree
	data["Commodities"] = commodities

	h.renderTemplate(w, "finance.html", data)
}

// buildAccountTree строит древовидную структуру счетов
func (h *Handler) buildAccountTree(accounts []*models.Account, accountsMap map[int64]*models.Account) []*models.Account {
	// Находим корневые счета (без родителя или с ROOT типом)
	rootAccounts := make([]*models.Account, 0)

	for _, acc := range accounts {
		if acc.ParentID == nil || acc.AccountType == models.AccountTypeRoot {
			rootAccounts = append(rootAccounts, acc)
		} else if _, parentExists := accountsMap[*acc.ParentID]; !parentExists {
			// Родитель не в карте (например ROOT был исключён из списка) — считаем корневым
			rootAccounts = append(rootAccounts, acc)
		}
	}

	// Сортируем корневые счета по ID для стабильности
	h.sortAccountsByID(rootAccounts)

	// Рекурсивно строим дерево для каждого корневого счета
	for _, root := range rootAccounts {
		h.buildAccountChildren(root, accountsMap, []string{}, 0)
	}

	return rootAccounts
}

// buildAccountChildren рекурсивно строит дочерние счета и вычисляет TreeLines
func (h *Handler) buildAccountChildren(parent *models.Account, accountsMap map[int64]*models.Account, parentLines []string, level int) {
	parent.Childs = make([]*models.Account, 0)

	for _, acc := range accountsMap {
		if acc.ParentID != nil && *acc.ParentID == parent.ID {
			parent.Childs = append(parent.Childs, acc)
		}
	}

	// Сортируем дочерние счета по ID для стабильности
	h.sortAccountsByID(parent.Childs)

	// Считаем видимые дочерние счета для определения последнего
	visibleChilds := make([]*models.Account, 0)
	for _, child := range parent.Childs {
		if child.AccountType != models.AccountTypeRoot && child.Hidden == 0 {
			visibleChilds = append(visibleChilds, child)
		}
	}

	// Рекурсивно обрабатываем дочерние счета с вычислением TreeLines
	visibleIdx := 0
	for _, child := range parent.Childs {
		isVisible := child.AccountType != models.AccountTypeRoot && child.Hidden == 0
		if isVisible {
			isLast := visibleIdx == len(visibleChilds)-1
			child.IsLast = isLast
			child.Level = level

			// Строим TreeLines для этого дочернего элемента
			child.TreeLines = make([]string, len(parentLines)+1)
			copy(child.TreeLines, parentLines)

			if isLast {
				child.TreeLines[len(parentLines)] = "corner" // └
			} else {
				child.TreeLines[len(parentLines)] = "tee" // ├
			}

			// Для дочерних элементов этого узла: если текущий не последний, рисуем вертикальную линию
			childParentLines := make([]string, len(parentLines)+1)
			copy(childParentLines, parentLines)
			if isLast {
				childParentLines[len(parentLines)] = "blank" // пустое место
			} else {
				childParentLines[len(parentLines)] = "pipe" // │
			}

			h.buildAccountChildren(child, accountsMap, childParentLines, level+1)
			visibleIdx++
		} else {
			// Для скрытых/ROOT счетов тоже строим дерево (на случай если у них есть видимые потомки)
			h.buildAccountChildren(child, accountsMap, parentLines, level)
		}
	}
}

// sortAccountsByID сортирует счета по ID
func (h *Handler) sortAccountsByID(accounts []*models.Account) {
	for i := 0; i < len(accounts)-1; i++ {
		for j := i + 1; j < len(accounts); j++ {
			if accounts[i].ID > accounts[j].ID {
				accounts[i], accounts[j] = accounts[j], accounts[i]
			}
		}
	}
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

	// Получаем параметр сортировки из URL
	sortOrder := r.URL.Query().Get("sort")
	if sortOrder != "asc" && sortOrder != "desc" {
		sortOrder = "desc" // По умолчанию - от новых к старым
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

	// Получаем транзакции счета с учетом сортировки
	transactions := h.getAccountTransactions(userID, accountID, sortOrder)
	accounts, _ := h.getAccounts(userID)
	commodities, _ := h.getCommodities()

	// Определяем противоположный порядок сортировки для ссылки
	oppositeSortOrder := "asc"
	if sortOrder == "asc" {
		oppositeSortOrder = "desc"
	}

	// Статистика и список доступных месяцев
	var totalIncome, totalExpense float64
	var incomeCount, expenseCount, totalCount int
	monthsSet := make(map[string]bool)

	for _, tx := range transactions {
		totalCount++
		if v, ok := tx["plus_balance_changing"].(float64); ok && v > 0 {
			totalIncome += v
			incomeCount++
		}
		if v, ok := tx["balance_changing"].(float64); ok && v > 0 {
			totalExpense += v
			expenseCount++
		}
		if d, ok := tx["post_date"].(string); ok && len(d) >= 7 {
			monthsSet[d[:7]] = true
		}
	}

	monthNames := []string{
		"Январь", "Февраль", "Март", "Апрель", "Май", "Июнь",
		"Июль", "Август", "Сентябрь", "Октябрь", "Ноябрь", "Декабрь",
	}
	availableMonths := make([]map[string]string, 0, len(monthsSet))
	for ym := range monthsSet {
		parts := strings.SplitN(ym, "-", 2)
		if len(parts) != 2 {
			continue
		}
		month, _ := strconv.Atoi(parts[1])
		if month < 1 || month > 12 {
			continue
		}
		availableMonths = append(availableMonths, map[string]string{
			"Value": ym,
			"Label": fmt.Sprintf("%s %s", monthNames[month-1], parts[0]),
		})
	}
	// Сортируем месяцы по убыванию
	for i := 0; i < len(availableMonths)-1; i++ {
		for j := i + 1; j < len(availableMonths); j++ {
			if availableMonths[i]["Value"] < availableMonths[j]["Value"] {
				availableMonths[i], availableMonths[j] = availableMonths[j], availableMonths[i]
			}
		}
	}

	data := h.pageData(userID, "transactions")
	data["Title"] = account.Name
	data["Account"] = account
	data["Transactions"] = transactions
	data["Accounts"] = accounts
	data["Commodities"] = commodities
	data["SortOrder"] = sortOrder
	data["OppositeSortOrder"] = oppositeSortOrder
	data["ActiveAccountID"] = accountID
	data["TotalIncome"] = totalIncome
	data["TotalExpense"] = totalExpense
	data["TotalCount"] = totalCount
	data["IncomeCount"] = incomeCount
	data["ExpenseCount"] = expenseCount
	data["AvailableMonths"] = availableMonths

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
		data := h.pageData(userID, "finance")
		data["Title"] = "Новый счет"
		data["Accounts"] = accounts
		data["Commodities"] = commodities
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

	data := h.pageData(userID, "finance")
	data["Title"] = "Редактирование счета"
	data["Account"] = account
	data["Accounts"] = accounts
	data["Commodities"] = commodities
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

	data := h.pageData(userID, "transactions")
	data["Title"] = "Транзакция"
	data["Transaction"] = transaction
	data["Debit"] = debit
	data["Credit"] = credit
	data["AccountID"] = accountID
	data["Accounts"] = accounts
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

	data := h.pageData(userID, "transactions")
	data["Title"] = fmt.Sprintf("Транзакции с тегом: %s", tag)
	data["Tag"] = tag
	data["Transactions"] = transactions
	h.renderTemplate(w, "finance_transactions_by_tag.html", data)
}

// FinanceSettings - настройки
func (h *Handler) FinanceSettings(w http.ResponseWriter, r *http.Request) {
	userID, _ := h.getUserID(r)
	data := h.pageData(userID, "settings")
	data["Title"] = "Настройки"
	h.renderTemplate(w, "finance_settings.html", data)
}

// Вспомогательные функции

// getAccountsWithBalance загружает счета пользователя вместе с балансом (для сайдбара).
func (h *Handler) getAccountsWithBalance(userID int64) ([]*models.Account, error) {
	rows, err := h.db.Query(`
		SELECT a.id, a.name, a.account_type, a.commodity_id, a.commodity_scu,
		       a.non_std_scu, a.parent_id, a.code, a.description, a.hidden, a.placeholder,
		       COALESCE(SUM(s.value_num), 0) AS balance
		FROM accounts a
		LEFT JOIN splits s ON a.id = s.account_id
		WHERE a.user_id = ?
		GROUP BY a.id
		ORDER BY a.name
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	allAccounts := make([]*models.Account, 0)
	accountsMap := make(map[int64]*models.Account)

	for rows.Next() {
		var acc models.Account
		var parentID sql.NullInt64
		var code, description sql.NullString
		var balanceRaw int64

		err := rows.Scan(&acc.ID, &acc.Name, &acc.AccountType, &acc.CommodityID,
			&acc.CommoditySCU, &acc.NonStdSCU, &parentID, &code, &description,
			&acc.Hidden, &acc.Placeholder, &balanceRaw)
		if err != nil {
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

		acc.Balance = float64(balanceRaw) / 100.0
		if acc.IsNegativeBalance() {
			acc.Balance = -acc.Balance
		}

		allAccounts = append(allAccounts, &acc)
		accountsMap[acc.ID] = &acc
	}

	result := h.flattenAccountsHierarchy(allAccounts, accountsMap)
	return result, nil
}

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

	allAccounts := make([]*models.Account, 0)
	accountsMap := make(map[int64]*models.Account)

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

		allAccounts = append(allAccounts, &acc)
		accountsMap[acc.ID] = &acc
	}

	// Строим дерево и получаем плоский список в правильном порядке
	result := h.flattenAccountsHierarchy(allAccounts, accountsMap)

	return result, nil
}

// flattenAccountsHierarchy возвращает плоский список счетов с правильной иерархией
func (h *Handler) flattenAccountsHierarchy(accounts []*models.Account, accountsMap map[int64]*models.Account) []*models.Account {
	// Находим ID корневого ROOT счета (если есть)
	var rootID *int64
	for _, acc := range accounts {
		if acc.AccountType == models.AccountTypeRoot {
			rootID = &acc.ID
			break
		}
	}

	// Находим счета верхнего уровня (дочерние ROOT или без родителя, исключая сам ROOT)
	topLevelAccounts := make([]*models.Account, 0)
	for _, acc := range accounts {
		// Пропускаем ROOT счет
		if acc.AccountType == models.AccountTypeRoot {
			continue
		}
		// Счет верхнего уровня: без родителя или родитель - ROOT
		isTopLevel := acc.ParentID == nil ||
			(rootID != nil && *acc.ParentID == *rootID)
		if isTopLevel {
			topLevelAccounts = append(topLevelAccounts, acc)
		}
	}

	// Сортируем счета верхнего уровня по имени
	h.sortAccountsByName(topLevelAccounts)

	// Рекурсивно добавляем счета в плоский список
	result := make([]*models.Account, 0, len(accounts))
	var addAccountWithChildren func(acc *models.Account, level int)
	addAccountWithChildren = func(acc *models.Account, level int) {
		// Пропускаем ROOT счета
		if acc.AccountType == models.AccountTypeRoot {
			return
		}

		acc.Level = level
		// Создаем отступы с помощью неразрывных пробелов (4 пробела на уровень)
		indent := strings.Repeat("    ", level)
		acc.DisplayName = indent + acc.Name
		result = append(result, acc)

		// Находим дочерние счета
		children := make([]*models.Account, 0)
		for _, child := range accounts {
			if child.ParentID != nil && *child.ParentID == acc.ID && child.AccountType != models.AccountTypeRoot {
				children = append(children, child)
			}
		}

		// Сортируем дочерние счета по имени
		h.sortAccountsByName(children)

		// Рекурсивно добавляем дочерние счета
		for _, child := range children {
			addAccountWithChildren(child, level+1)
		}
	}

	for _, topLevel := range topLevelAccounts {
		addAccountWithChildren(topLevel, 0)
	}

	return result
}

// sortAccountsByName сортирует счета по имени
func (h *Handler) sortAccountsByName(accounts []*models.Account) {
	for i := 0; i < len(accounts)-1; i++ {
		for j := i + 1; j < len(accounts); j++ {
			if accounts[i].Name > accounts[j].Name {
				accounts[i], accounts[j] = accounts[j], accounts[i]
			}
		}
	}
}

// isPlaceholderAccount возвращает true, если счёт принадлежит userID и помечен как контейнерный (placeholder=1).
// При ошибках/отсутствии возвращает false (валидация ID идёт отдельно).
func (h *Handler) isPlaceholderAccount(userID, accountID int64) bool {
	var ph int
	err := h.db.QueryRow(
		`SELECT placeholder FROM accounts WHERE id = ? AND user_id = ?`,
		accountID, userID,
	).Scan(&ph)
	if err != nil {
		return false
	}
	return ph == 1
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

// Dashboard — страница дашборда с обзором финансов
// Dashboard — редирект на главную страницу (дашборд перенесён на /)
func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/", http.StatusFound)
}

// renderDashboard — общая логика рендеринга дашборда (используется Index и Dashboard)
func (h *Handler) renderDashboard(w http.ResponseWriter, r *http.Request, userID int64) {
	data := h.pageData(userID, "dashboard")
	data["Title"] = "Дашборд"

	// Загружаем балансы счетов, сгруппированные по типу
	rows, err := h.db.Query(`
		SELECT a.account_type, SUM(s.value_num) / 100.0 AS balance
		FROM accounts a
		LEFT JOIN splits s ON a.id = s.account_id
		WHERE a.user_id = ? AND a.account_type IN ('ASSET','BANK','CASH','LIABILITY','INCOME','EXPENSE','EQUITY')
		GROUP BY a.account_type
	`, userID)

	var totalAssets, totalLiabilities, totalIncome, totalExpense float64
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var accountType string
			var balance float64
			if err := rows.Scan(&accountType, &balance); err != nil {
				continue
			}
			switch accountType {
			case "ASSET", "BANK", "CASH":
				totalAssets += balance
			case "LIABILITY":
				// LIABILITY в GnuCash хранится отрицательным — инвертируем
				totalLiabilities += -balance
			case "INCOME":
				totalIncome += -balance
			case "EXPENSE":
				totalExpense += balance
			}
		}
	}
	data["TotalAssets"] = totalAssets
	data["TotalLiabilities"] = totalLiabilities
	data["NetWorth"] = totalAssets - totalLiabilities
	data["TotalIncome"] = totalIncome
	data["TotalExpense"] = totalExpense

	// Топ-7 счетов по абсолютному балансу (не ROOT, не INCOME/EXPENSE)
	topRows, err := h.db.Query(`
		SELECT a.id, a.name, a.account_type,
		       COALESCE(SUM(s.value_num), 0) / 100.0 AS balance
		FROM accounts a
		LEFT JOIN splits s ON a.id = s.account_id
		WHERE a.user_id = ?
		  AND a.account_type IN ('ASSET','BANK','CASH','LIABILITY','EQUITY')
		  AND a.hidden = 0
		GROUP BY a.id, a.name, a.account_type
		HAVING ABS(balance) > 0
		ORDER BY ABS(balance) DESC
		LIMIT 7
	`, userID)
	var topAccounts []map[string]interface{}
	if err == nil {
		defer topRows.Close()
		for topRows.Next() {
			var id int64
			var name, accountType string
			var balance float64
			if err := topRows.Scan(&id, &name, &accountType, &balance); err != nil {
				continue
			}
			// LIABILITY инвертируем
			if accountType == "LIABILITY" {
				balance = -balance
			}
			topAccounts = append(topAccounts, map[string]interface{}{
				"ID":          id,
				"Name":        name,
				"AccountType": accountType,
				"Balance":     balance,
			})
		}
	}
	data["TopAccounts"] = topAccounts

	// Последние 8 транзакций пользователя
	recentRows, err := h.db.Query(`
		SELECT DISTINCT t.id, t.description, DATE_FORMAT(t.post_date, '%Y-%m-%d') AS post_date,
		       s.value_num / 100.0 AS amount,
		       a.name AS account_name, a.account_type
		FROM transactions t
		JOIN splits s ON s.tx_id = t.id
		JOIN accounts a ON a.id = s.account_id
		WHERE t.user_id = ?
		  AND a.account_type NOT IN ('ROOT','EQUITY')
		  AND s.value_num > 0
		ORDER BY t.post_date DESC, t.id DESC
		LIMIT 8
	`, userID)
	var recentTx []map[string]interface{}
	if err == nil {
		defer recentRows.Close()
		for recentRows.Next() {
			var txID int64
			var description, postDate, accountName, accountType string
			var amount float64
			if err := recentRows.Scan(&txID, &description, &postDate, &amount, &accountName, &accountType); err != nil {
				continue
			}
			if description == "" {
				description = "—"
			}
			recentTx = append(recentTx, map[string]interface{}{
				"ID":          txID,
				"Description": description,
				"PostDate":    postDate,
				"Amount":      amount,
				"AccountName": accountName,
				"AccountType": accountType,
			})
		}
	}
	data["RecentTransactions"] = recentTx

	h.renderTemplate(w, "index.html", data)
}

func (h *Handler) getAccountTransactions(userID, accountID int64, sortOrder string) []map[string]interface{} {
	// Всегда получаем транзакции в хронологическом порядке для расчета баланса
	// Используем JOIN вместо IN (SELECT ...) для лучшей производительности
	rows, err := h.db.Query(`
		SELECT t.id, t.description, t.post_date, t.tags,
		       s.id, s.account_id, s.value_num, s.value_denom,
		       a.name
		FROM transactions t
		JOIN splits acc_split ON t.id = acc_split.tx_id AND acc_split.account_id = ? AND acc_split.user_id = ?
		JOIN splits s ON t.id = s.tx_id
		LEFT JOIN accounts a ON s.account_id = a.id
		WHERE t.user_id = ?
		ORDER BY t.post_date ASC, t.id ASC
	`, accountID, userID, userID)

	if err != nil {
		return []map[string]interface{}{}
	}
	defer rows.Close()

	transactionsMap := make(map[int64]map[string]interface{})
	transactionOrder := make([]int64, 0) // Сохраняем порядок транзакций

	for rows.Next() {
		var txID, splitID, splitAccountID, valueNum, valueDenom int64
		var description, tags, accountName string
		var postDate time.Time

		rows.Scan(&txID, &description, &postDate, &tags, &splitID, &splitAccountID,
			&valueNum, &valueDenom, &accountName)

		if _, exists := transactionsMap[txID]; !exists {
			transactionsMap[txID] = map[string]interface{}{
				"id":            txID,
				"description":   description,
				"post_date":     postDate.Format("02.01.2006"),
				"post_date_raw": postDate,
				"tags":          strings.Split(tags, ","),
			}
			transactionOrder = append(transactionOrder, txID)
		}

		if splitAccountID == accountID {
			value := float64(valueNum) / float64(valueDenom)
			// Разделяем на приход (положительное) и расход (отрицательное)
			if value > 0 {
				transactionsMap[txID]["plus_balance_changing"] = value
			} else {
				transactionsMap[txID]["balance_changing"] = -value // Показываем расход как положительное число
			}
			transactionsMap[txID]["value_change"] = value // Сохраняем оригинальное значение для расчета баланса
		} else {
			transactionsMap[txID]["account_name"] = accountName
			transactionsMap[txID]["account_id"] = splitAccountID
		}
	}

	// Рассчитываем накопительный баланс (в хронологическом порядке)
	var runningBalance float64 = 0
	for _, txID := range transactionOrder {
		if valueChange, ok := transactionsMap[txID]["value_change"].(float64); ok {
			runningBalance += valueChange
		}
		transactionsMap[txID]["account_balance"] = runningBalance
	}

	// Формируем результат в нужном порядке
	transactions := make([]map[string]interface{}, 0, len(transactionOrder))
	if sortOrder == "desc" {
		// Обратный порядок (от новых к старым)
		for i := len(transactionOrder) - 1; i >= 0; i-- {
			transactions = append(transactions, transactionsMap[transactionOrder[i]])
		}
	} else {
		// Прямой порядок (от старых к новым)
		for _, txID := range transactionOrder {
			transactions = append(transactions, transactionsMap[txID])
		}
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
	hidden := 0
	if r.FormValue("hidden") == "1" {
		hidden = 1
	}
	placeholder := 0
	if r.FormValue("placeholder") == "1" {
		placeholder = 1
	}

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
			VALUES (?, ?, ?, ?, 100, 0, ?, ?, ?, ?)
		`, userID, name, accountType, commodityID, parentID, description, hidden, placeholder)

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

		// Защита от противоречивых состояний placeholder
		if placeholder == 1 {
			var splits int
			h.db.QueryRow(`SELECT COUNT(*) FROM splits WHERE account_id = ? AND user_id = ?`,
				accountID, userID).Scan(&splits)
			if splits > 0 {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{
					"error": "Нельзя сделать счёт контейнерным: к нему уже привязаны транзакции",
				})
				return
			}
		} else {
			var kids int
			h.db.QueryRow(`SELECT COUNT(*) FROM accounts WHERE parent_id = ? AND user_id = ?`,
				accountID, userID).Scan(&kids)
			if kids > 0 {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{
					"error": "Нельзя убрать признак контейнера: у счёта есть дочерние счета",
				})
				return
			}
		}

		_, err = h.db.Exec(`
			UPDATE accounts
			SET name = ?, account_type = ?, commodity_id = ?, parent_id = ?, description = ?,
			    hidden = ?, placeholder = ?
			WHERE id = ? AND user_id = ?
		`, name, accountType, commodityID, parentID, description, hidden, placeholder, accountID, userID)

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
	idStr := r.FormValue("id")
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

	// Контейнерные (placeholder) счета не могут участвовать в транзакциях
	if h.isPlaceholderAccount(userID, debitAccountID) || h.isPlaceholderAccount(userID, creditAccountID) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Контейнерный счёт не может участвовать в транзакции — выберите конечный счёт",
		})
		return
	}

	// Конвертируем сумму в целые числа (value_num / value_denom)
	valueNum := int64(value * 100)
	valueDenom := int64(100)

	// Проверяем, это обновление или создание
	if idStr != "" && idStr != "0" {
		// Обновление существующей транзакции
		txID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			json.NewEncoder(w).Encode(map[string]string{"error": "Invalid transaction ID"})
			return
		}

		// Начинаем транзакцию БД
		tx, err := h.db.Begin()
		if err != nil {
			fmt.Printf("ERROR starting transaction: %v\n", err)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		defer tx.Rollback()

		// Обновляем транзакцию
		_, err = tx.Exec(`
			UPDATE transactions SET description = ?, post_date = ?, tags = ?
			WHERE id = ? AND user_id = ?
		`, description, postDate, tags, txID, userID)

		if err != nil {
			fmt.Printf("ERROR updating transaction: %v\n", err)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		// Удаляем старые splits
		_, err = tx.Exec("DELETE FROM splits WHERE tx_id = ? AND user_id = ?", txID, userID)
		if err != nil {
			fmt.Printf("ERROR deleting old splits: %v\n", err)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		// Создаем новые splits
		_, err = tx.Exec(`
			INSERT INTO splits (user_id, tx_id, account_id, value_num, value_denom)
			VALUES (?, ?, ?, ?, ?)
		`, userID, txID, debitAccountID, valueNum, valueDenom)

		if err != nil {
			fmt.Printf("ERROR creating debit split: %v\n", err)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		_, err = tx.Exec(`
			INSERT INTO splits (user_id, tx_id, account_id, value_num, value_denom)
			VALUES (?, ?, ?, ?, ?)
		`, userID, txID, creditAccountID, -valueNum, valueDenom)

		if err != nil {
			fmt.Printf("ERROR creating credit split: %v\n", err)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		if err := tx.Commit(); err != nil {
			fmt.Printf("ERROR committing transaction: %v\n", err)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"result": "ok",
			"id":     txID,
		})
	} else {
		// Создание новой транзакции
		tx, err := h.db.Begin()
		if err != nil {
			fmt.Printf("ERROR starting transaction: %v\n", err)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		defer tx.Rollback()

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

		_, err = tx.Exec(`
			INSERT INTO splits (user_id, tx_id, account_id, value_num, value_denom)
			VALUES (?, ?, ?, ?, ?)
		`, userID, txID, debitAccountID, valueNum, valueDenom)

		if err != nil {
			fmt.Printf("ERROR creating debit split: %v\n", err)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		_, err = tx.Exec(`
			INSERT INTO splits (user_id, tx_id, account_id, value_num, value_denom)
			VALUES (?, ?, ?, ?, ?)
		`, userID, txID, creditAccountID, -valueNum, valueDenom)

		if err != nil {
			fmt.Printf("ERROR creating credit split: %v\n", err)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

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
	userID, authenticated := h.getUserID(r)
	if !authenticated {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"result": "error", "message": "Not authenticated"})
		return
	}

	// Начинаем транзакцию
	tx, err := h.db.Begin()
	if err != nil {
		fmt.Printf("ERROR starting transaction: %v\n", err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"result": "error", "message": err.Error()})
		return
	}
	defer tx.Rollback()

	// Удаляем все splits пользователя
	_, err = tx.Exec("DELETE FROM splits WHERE user_id = ?", userID)
	if err != nil {
		fmt.Printf("ERROR deleting splits: %v\n", err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"result": "error", "message": err.Error()})
		return
	}

	// Удаляем все транзакции пользователя
	_, err = tx.Exec("DELETE FROM transactions WHERE user_id = ?", userID)
	if err != nil {
		fmt.Printf("ERROR deleting transactions: %v\n", err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"result": "error", "message": err.Error()})
		return
	}

	// Сначала обнуляем parent_id у всех счетов, чтобы избежать ошибки foreign key
	_, err = tx.Exec("UPDATE accounts SET parent_id = NULL WHERE user_id = ?", userID)
	if err != nil {
		fmt.Printf("ERROR clearing parent_id: %v\n", err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"result": "error", "message": err.Error()})
		return
	}

	// Удаляем все счета пользователя
	_, err = tx.Exec("DELETE FROM accounts WHERE user_id = ?", userID)
	if err != nil {
		fmt.Printf("ERROR deleting accounts: %v\n", err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"result": "error", "message": err.Error()})
		return
	}

	// Коммитим транзакцию
	if err := tx.Commit(); err != nil {
		fmt.Printf("ERROR committing transaction: %v\n", err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"result": "error", "message": err.Error()})
		return
	}

	log.Printf("User %d deleted all their data", userID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"result": "ok"})
}

func (h *Handler) APIWelcomeCreateEmpty(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"result": "ok"})
}

// APIWelcomeCreateBase создает базовый набор счетов как в GnuCash
func (h *Handler) APIWelcomeCreateBase(w http.ResponseWriter, r *http.Request) {
	userID, authenticated := h.getUserID(r)
	if !authenticated {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"result": "error", "message": "Not authenticated"})
		return
	}

	// Проверяем, есть ли уже счета у пользователя
	var count int
	err := h.db.QueryRow("SELECT COUNT(*) FROM accounts WHERE user_id = ?", userID).Scan(&count)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"result": "error", "message": err.Error()})
		return
	}

	if count > 0 {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"result": "error", "message": "У вас уже есть счета"})
		return
	}

	// Создаем базовый набор счетов
	if err := h.createBaseAccounts(userID); err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"result": "error", "message": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"result": "ok"})
}

// createBaseAccounts создает базовый набор счетов для пользователя
func (h *Handler) createBaseAccounts(userID int64) error {
	// Начинаем транзакцию
	tx, err := h.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	// Получаем ID валюты RUB (по умолчанию 1)
	commodityID := int64(1)

	// Структура для описания счета
	type accountDef struct {
		name        string
		accountType string
		description string
		placeholder int
		children    []accountDef
	}

	// Базовая структура счетов как в GnuCash
	baseAccounts := []accountDef{
		{
			name:        "Активы",
			accountType: models.AccountTypeAsset,
			description: "Все активы",
			placeholder: 1,
			children: []accountDef{
				{
					name:        "Текущие активы",
					accountType: models.AccountTypeAsset,
					description: "Текущие активы",
					placeholder: 1,
					children: []accountDef{
						{
							name:        "Наличные",
							accountType: models.AccountTypeCash,
							description: "Наличные деньги",
							placeholder: 0,
						},
						{
							name:        "Расчетный счет",
							accountType: models.AccountTypeBank,
							description: "Основной банковский счет",
							placeholder: 0,
						},
						{
							name:        "Сберегательный счет",
							accountType: models.AccountTypeBank,
							description: "Сберегательный счет",
							placeholder: 0,
						},
					},
				},
			},
		},
		{
			name:        "Обязательства",
			accountType: models.AccountTypeLiability,
			description: "Все обязательства",
			placeholder: 1,
			children: []accountDef{
				{
					name:        "Кредитная карта",
					accountType: models.AccountTypeLiability,
					description: "Задолженность по кредитной карте",
					placeholder: 0,
				},
				{
					name:        "Займы",
					accountType: models.AccountTypeLiability,
					description: "Займы и кредиты",
					placeholder: 0,
				},
			},
		},
		{
			name:        "Доходы",
			accountType: models.AccountTypeIncome,
			description: "Все доходы",
			placeholder: 1,
			children: []accountDef{
				{
					name:        "Зарплата",
					accountType: models.AccountTypeIncome,
					description: "Заработная плата",
					placeholder: 0,
				},
				{
					name:        "Проценты",
					accountType: models.AccountTypeIncome,
					description: "Процентные доходы",
					placeholder: 0,
				},
				{
					name:        "Прочие доходы",
					accountType: models.AccountTypeIncome,
					description: "Прочие доходы",
					placeholder: 0,
				},
			},
		},
		{
			name:        "Расходы",
			accountType: models.AccountTypeExpense,
			description: "Все расходы",
			placeholder: 1,
			children: []accountDef{
				{
					name:        "Продукты",
					accountType: models.AccountTypeExpense,
					description: "Продукты питания",
					placeholder: 0,
				},
				{
					name:        "Транспорт",
					accountType: models.AccountTypeExpense,
					description: "Транспортные расходы",
					placeholder: 0,
				},
				{
					name:        "Коммунальные услуги",
					accountType: models.AccountTypeExpense,
					description: "Коммунальные платежи",
					placeholder: 1,
					children: []accountDef{
						{
							name:        "Электричество",
							accountType: models.AccountTypeExpense,
							description: "Оплата электроэнергии",
							placeholder: 0,
						},
						{
							name:        "Газ",
							accountType: models.AccountTypeExpense,
							description: "Оплата газа",
							placeholder: 0,
						},
						{
							name:        "Вода",
							accountType: models.AccountTypeExpense,
							description: "Оплата воды",
							placeholder: 0,
						},
						{
							name:        "Интернет",
							accountType: models.AccountTypeExpense,
							description: "Оплата интернета",
							placeholder: 0,
						},
						{
							name:        "Телефон",
							accountType: models.AccountTypeExpense,
							description: "Оплата телефона",
							placeholder: 0,
						},
					},
				},
				{
					name:        "Развлечения",
					accountType: models.AccountTypeExpense,
					description: "Развлечения и отдых",
					placeholder: 0,
				},
				{
					name:        "Одежда",
					accountType: models.AccountTypeExpense,
					description: "Одежда и обувь",
					placeholder: 0,
				},
				{
					name:        "Здоровье",
					accountType: models.AccountTypeExpense,
					description: "Медицина и здоровье",
					placeholder: 0,
				},
				{
					name:        "Образование",
					accountType: models.AccountTypeExpense,
					description: "Образование и обучение",
					placeholder: 0,
				},
				{
					name:        "Подарки",
					accountType: models.AccountTypeExpense,
					description: "Подарки",
					placeholder: 0,
				},
				{
					name:        "Прочие расходы",
					accountType: models.AccountTypeExpense,
					description: "Прочие расходы",
					placeholder: 0,
				},
			},
		},
		{
			name:        "Капитал",
			accountType: models.AccountTypeEquity,
			description: "Собственный капитал",
			placeholder: 1,
			children: []accountDef{
				{
					name:        "Начальный баланс",
					accountType: models.AccountTypeEquity,
					description: "Начальные остатки",
					placeholder: 0,
				},
			},
		},
	}

	// Рекурсивная функция для создания счетов
	var createAccount func(acc accountDef, parentID *int64) error
	createAccount = func(acc accountDef, parentID *int64) error {
		result, err := tx.Exec(`
			INSERT INTO accounts (user_id, name, account_type, commodity_id, commodity_scu,
			                      non_std_scu, parent_id, description, hidden, placeholder)
			VALUES (?, ?, ?, ?, 100, 0, ?, ?, 0, ?)
		`, userID, acc.name, acc.accountType, commodityID, parentID, acc.description, acc.placeholder)

		if err != nil {
			return fmt.Errorf("failed to create account %s: %w", acc.name, err)
		}

		accountID, err := result.LastInsertId()
		if err != nil {
			return fmt.Errorf("failed to get account ID for %s: %w", acc.name, err)
		}

		// Создаем дочерние счета
		for _, child := range acc.children {
			if err := createAccount(child, &accountID); err != nil {
				return err
			}
		}

		return nil
	}

	// Создаем все счета
	for _, acc := range baseAccounts {
		if err := createAccount(acc, nil); err != nil {
			return err
		}
	}

	// Коммитим транзакцию
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
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

// APIImportGnuCashXML handles GnuCash XML file import (compressed gzip)
func (h *Handler) APIImportGnuCashXML(w http.ResponseWriter, r *http.Request) {
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

	file, header, err := r.FormFile("file")
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"result": "error", "message": "Failed to get file"})
		return
	}
	defer file.Close()

	log.Printf("Importing GnuCash XML file: %s", header.Filename)

	// Read file content
	fileData, err := io.ReadAll(file)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"result": "error", "message": "Failed to read file"})
		return
	}

	// Parse GnuCash XML
	parsedData, err := gnucash.ParseReaderWithFallback(fileData)
	if err != nil {
		log.Printf("Error parsing GnuCash XML: %v", err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"result": "error", "message": fmt.Sprintf("Failed to parse GnuCash file: %v", err)})
		return
	}

	// Import parsed data
	if err := h.importFromGnuCashXML(userID, parsedData); err != nil {
		log.Printf("Error importing GnuCash XML data: %v", err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"result": "error", "message": err.Error()})
		return
	}

	log.Printf("Successfully imported GnuCash XML: %d accounts, %d transactions",
		len(parsedData.Accounts), len(parsedData.Transactions))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"result":       "ok",
		"accounts":     len(parsedData.Accounts),
		"transactions": len(parsedData.Transactions),
	})
}

// importFromGnuCashXML imports data from parsed GnuCash XML
func (h *Handler) importFromGnuCashXML(userID int64, data *gnucash.ParsedData) error {
	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	// Map old GUIDs to new IDs
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

	// Map commodities from GnuCash to our database
	for _, c := range data.Commodities {
		if id, ok := existingCommodities[c.Mnemonic]; ok {
			commodityMap[c.GUID] = id
			commodityMap[c.Space+":"+c.Mnemonic] = id
		} else {
			// Default to first commodity (usually RUB)
			commodityMap[c.GUID] = 1
			commodityMap[c.Space+":"+c.Mnemonic] = 1
		}
	}

	// Import accounts in multiple passes to handle hierarchy
	remainingAccounts := make([]gnucash.ParsedAccount, len(data.Accounts))
	copy(remainingAccounts, data.Accounts)

	maxPasses := 10
	for pass := 0; pass < maxPasses && len(remainingAccounts) > 0; pass++ {
		var stillRemaining []gnucash.ParsedAccount

		for _, acc := range remainingAccounts {
			// Check if parent exists or is empty (root account)
			canImport := acc.ParentGUID == "" || accountMap[acc.ParentGUID] != 0

			if canImport {
				// Get commodity ID
				commodityID := int64(1)
				if id, ok := commodityMap[acc.CommodityRef]; ok {
					commodityID = id
				}

				// Get parent ID
				var parentID sql.NullInt64
				if acc.ParentGUID != "" && accountMap[acc.ParentGUID] != 0 {
					parentID.Valid = true
					parentID.Int64 = accountMap[acc.ParentGUID]
				}

				// Convert hidden/placeholder to int
				hidden := 0
				if acc.Hidden {
					hidden = 1
				}
				placeholder := 0
				if acc.Placeholder {
					placeholder = 1
				}

				// Get commodity SCU (default to 100)
				commoditySCU := acc.CommoditySCU
				if commoditySCU == 0 {
					commoditySCU = 100
				}

				result, err := tx.Exec(`
					INSERT INTO accounts (user_id, name, account_type, commodity_id, commodity_scu,
					                      non_std_scu, parent_id, code, description, hidden, placeholder)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				`, userID, acc.Name, acc.AccountType, commodityID,
					commoditySCU, acc.NonStdSCU, parentID,
					acc.Code, acc.Description, hidden, placeholder)

				if err != nil {
					return fmt.Errorf("failed to insert account %s: %w", acc.Name, err)
				}

				newID, _ := result.LastInsertId()
				accountMap[acc.GUID] = newID
			} else {
				stillRemaining = append(stillRemaining, acc)
			}
		}

		remainingAccounts = stillRemaining
	}

	if len(remainingAccounts) > 0 {
		log.Printf("Warning: %d accounts could not be imported due to unresolved parent references", len(remainingAccounts))
	}

	// Import transactions
	transactionMap := make(map[string]int64)
	for _, t := range data.Transactions {
		// Get currency ID
		currencyID := int64(1)
		if id, ok := commodityMap[t.CurrencyRef]; ok {
			currencyID = id
		}

		result, err := tx.Exec(`
			INSERT INTO transactions (user_id, currency_id, num, post_date, enter_date, description, tags)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, userID, currencyID, t.Num, t.PostDate, t.EnterDate, t.Description, "")

		if err != nil {
			return fmt.Errorf("failed to insert transaction: %w", err)
		}

		newTxID, _ := result.LastInsertId()
		transactionMap[t.GUID] = newTxID

		// Import splits for this transaction
		for _, s := range t.Splits {
			accountID, accExists := accountMap[s.AccountGUID]
			if !accExists {
				log.Printf("Warning: skipping split with unknown account GUID: %s", s.AccountGUID)
				continue
			}

			// Normalize value_denom to 100 if needed
			valueNum := s.ValueNum
			valueDenom := s.ValueDenom
			if valueDenom == 0 {
				valueDenom = 100
			}

			// Convert to standard denom (100)
			if valueDenom != 100 {
				valueNum = valueNum * 100 / valueDenom
				valueDenom = 100
			}

			_, err := tx.Exec(`
				INSERT INTO splits (user_id, tx_id, account_id, value_num, value_denom)
				VALUES (?, ?, ?, ?, ?)
			`, userID, newTxID, accountID, valueNum, valueDenom)

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

// APITransactionFormGet - возвращает HTML-фрагмент формы для модального окна редактирования/создания транзакции
func (h *Handler) APITransactionFormGet(w http.ResponseWriter, r *http.Request) {
	userID, _ := h.getUserID(r)

	txIDStr := r.URL.Query().Get("tx_id")
	accountIDStr := r.URL.Query().Get("account_id")

	accountID, _ := strconv.ParseInt(accountIDStr, 10, 64)

	var transaction *models.Transaction
	var debit, credit []map[string]interface{}

	if txIDStr != "" && txIDStr != "0" {
		txID, _ := strconv.ParseInt(txIDStr, 10, 64)
		transaction, debit, credit = h.getTransaction(userID, txID)
	}

	accounts, _ := h.getAccounts(userID)

	data := map[string]interface{}{
		"Transaction": transaction,
		"Debit":       debit,
		"Credit":      credit,
		"AccountID":   accountID,
		"Accounts":    accounts,
		"Today":       time.Now().Format("2006-01-02"),
	}

	h.renderTemplate(w, "finance_transaction_modal_form.html", data)
}

// APITransactionTableGet - возвращает HTML-фрагмент тела таблицы транзакций для обновления без перезагрузки
func (h *Handler) APITransactionTableGet(w http.ResponseWriter, r *http.Request) {
	userID, _ := h.getUserID(r)

	accountIDStr := r.URL.Query().Get("account_id")
	sortOrder := r.URL.Query().Get("sort")

	accountID, err := strconv.ParseInt(accountIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid account ID", http.StatusBadRequest)
		return
	}

	if sortOrder != "asc" && sortOrder != "desc" {
		sortOrder = "desc"
	}

	transactions := h.getAccountTransactions(userID, accountID, sortOrder)

	data := map[string]interface{}{
		"Transactions": transactions,
		"Account":      map[string]interface{}{"ID": accountID},
	}

	h.renderTemplate(w, "finance_transactions_tbody.html", data)
}

// APIAccountFormGet - возвращает HTML-фрагмент формы для drawer-а редактирования/создания счёта
func (h *Handler) APIAccountFormGet(w http.ResponseWriter, r *http.Request) {
	userID, _ := h.getUserID(r)

	accountIDStr := r.URL.Query().Get("account_id")

	var account *models.Account

	if accountIDStr != "" && accountIDStr != "0" {
		accountID, err := strconv.ParseInt(accountIDStr, 10, 64)
		if err == nil {
			var acc models.Account
			var parentID sql.NullInt64
			var code, description sql.NullString
			err = h.db.QueryRow(`
				SELECT id, name, account_type, commodity_id, commodity_scu, non_std_scu,
				       parent_id, code, description, hidden, placeholder
				FROM accounts WHERE id = ? AND user_id = ?
			`, accountID, userID).Scan(&acc.ID, &acc.Name, &acc.AccountType,
				&acc.CommodityID, &acc.CommoditySCU, &acc.NonStdSCU,
				&parentID, &code, &description, &acc.Hidden, &acc.Placeholder)

			if err == nil {
				if parentID.Valid {
					acc.ParentID = &parentID.Int64
				}
				if code.Valid {
					acc.Code = code.String
				}
				if description.Valid {
					acc.Description = description.String
				}
				account = &acc
			}
		}
	}

	accounts, _ := h.getAccounts(userID)
	commodities, _ := h.getCommodities()

	data := map[string]interface{}{
		"Account":     account,
		"Accounts":    accounts,
		"Commodities": commodities,
	}

	h.renderTemplate(w, "finance_account_drawer_form.html", data)
}
