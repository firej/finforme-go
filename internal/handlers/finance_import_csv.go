package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/evbogdanov/finforme/internal/models"
)

// FinanceImportCSV - страница импорта транзакций из CSV (GET)
func (h *Handler) FinanceImportCSV(w http.ResponseWriter, r *http.Request) {
	userID, _ := h.getUserID(r)
	accounts, err := h.getAccountsWithBalance(userID)
	if err != nil {
		http.Error(w, "Не удалось загрузить счета", 500)
		return
	}

	data := h.pageData(userID, "import_csv")
	data["Title"] = "Импорт транзакций из CSV"
	data["Accounts"] = accounts
	h.renderTemplate(w, "finance_import_csv.html", data)
}

// csvImportRow - одна строка для превью/сохранения
type csvImportRow struct {
	Currency    string  `json:"currency,omitempty"`
	Date        string  `json:"date"`   // YYYY-MM-DD
	Amount      float64 `json:"amount"` // знаковая сумма со стороны source_account
	Description string  `json:"description"`
	OtherID     int64   `json:"other_account_id,omitempty"`
}

// csvImportPreviewReq - запрос для превью
type csvImportPreviewReq struct {
	SourceAccountID int64          `json:"source_account_id"`
	Rows            []csvImportRow `json:"rows"`
}

// csvImportPreviewItem - результат превью одной строки
type csvImportPreviewItem struct {
	Currency         string  `json:"currency,omitempty"`
	Index            int     `json:"index"`
	Date             string  `json:"date"`
	Amount           float64 `json:"amount"`
	Description      string  `json:"description"`
	OtherAccountID   int64   `json:"other_account_id"`
	OtherAccountName string  `json:"other_account_name"`
	IsDuplicate      bool    `json:"is_duplicate"`
	ExistingTxID     int64   `json:"existing_tx_id,omitempty"`
	Error            string  `json:"error,omitempty"`
}

// APIImportCSVPreview - получает строки от клиента, возвращает превью с матчингом
func (h *Handler) APIImportCSVPreview(w http.ResponseWriter, r *http.Request) {
	userID, _ := h.getUserID(r)
	w.Header().Set("Content-Type", "application/json")

	var req csvImportPreviewReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Некорректный JSON: " + err.Error()})
		return
	}

	// Проверяем, что source account принадлежит пользователю и не контейнерный
	srcAcc, err := h.getAccountByID(userID, req.SourceAccountID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Исходный счёт не найден"})
		return
	}
	if srcAcc.Placeholder == 1 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Исходный счёт — контейнерный, выберите конечный счёт"})
		return
	}

	var sourceCurrency string
	if err := h.db.QueryRow(`SELECT mnemonic FROM commodities WHERE id=?`, srcAcc.CommodityID).Scan(&sourceCurrency); err != nil {
		writeFinanceError(w, err)
		return
	}
	// Получаем/создаём счёт «Дисбаланс»
	imbID, imbName, err := h.getOrCreateImbalanceAccount(userID, srcAcc.CommodityID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Не удалось создать счёт Дисбаланс: " + err.Error()})
		return
	}

	// Загружаем имена всех счетов один раз для отображения
	accountsByID, err := h.getAccountsMap(userID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	items := make([]csvImportPreviewItem, 0, len(req.Rows))
	for i, row := range req.Rows {
		item := csvImportPreviewItem{
			Index:       i,
			Date:        row.Date,
			Amount:      row.Amount,
			Description: strings.TrimSpace(row.Description),
			Currency:    row.Currency,
		}

		// Валидируем дату
		if _, err := time.Parse("2006-01-02", row.Date); err != nil {
			item.Error = "Неверный формат даты"
			items = append(items, item)
			continue
		}

		// Проверка на дубликат на исходном счёте: та же дата + та же сумма
		valueNum, err := csvAmountCents(row.Amount)
		if err != nil {
			item.Error = err.Error()
			items = append(items, item)
			continue
		}
		if err := validateCSVCurrency(row.Currency, sourceCurrency); err != nil {
			item.Error = err.Error()
			items = append(items, item)
			continue
		}
		dupTxID := h.findDuplicateTransaction(userID, req.SourceAccountID, row.Date, valueNum)
		if dupTxID > 0 {
			item.IsDuplicate = true
			item.ExistingTxID = dupTxID
		}

		// Подбор «второго» счёта
		otherID := int64(0)
		if row.OtherID > 0 {
			otherID = row.OtherID
		} else {
			otherID = h.guessOtherAccount(userID, req.SourceAccountID, item.Description, row.Amount)
		}
		if row.OtherID == 0 {
			acc, ok := accountsByID[otherID]
			if !ok || acc.CommodityID != srcAcc.CommodityID || acc.Placeholder != 0 || acc.ID == req.SourceAccountID {
				otherID = imbID
			}
		}
		item.OtherAccountID = otherID
		if acc, ok := accountsByID[otherID]; ok {
			item.OtherAccountName = acc.Name
			if acc.CommodityID != srcAcc.CommodityID {
				item.Error = "В CSV второй счёт должен быть в валюте исходного счёта"
			}
			if acc.Placeholder != 0 || acc.ID == req.SourceAccountID {
				item.Error = "Выберите другой конечный счёт"
			}
		} else {
			item.Error = "Второй счёт не найден"
		}

		items = append(items, item)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"items":                items,
		"imbalance_account_id": imbID,
		"imbalance_account_nm": imbName,
		"source_account_id":    req.SourceAccountID,
		"source_account_name":  srcAcc.Name,
		"source_commodity_id":  srcAcc.CommodityID,
		"source_currency":      sourceCurrency,
	})
}

// csvImportSaveReq - запрос сохранения
type csvImportSaveReq struct {
	SourceAccountID int64               `json:"source_account_id"`
	Items           []csvImportSaveItem `json:"items"`
}

type csvImportSaveItem struct {
	Currency       string  `json:"currency,omitempty"`
	Date           string  `json:"date"`
	Amount         float64 `json:"amount"`
	Description    string  `json:"description"`
	OtherAccountID int64   `json:"other_account_id"`
}

// APIImportCSVSave - сохраняет импортированные транзакции
func (h *Handler) APIImportCSVSave(w http.ResponseWriter, r *http.Request) {
	userID, _ := h.getUserID(r)
	w.Header().Set("Content-Type", "application/json")

	var req csvImportSaveReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Некорректный JSON: " + err.Error()})
		return
	}

	tx, err := h.beginFinanceWrite(userID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	defer tx.Rollback()

	// Проверяем источник
	srcAcc, err := getAccountByIDFrom(tx, userID, req.SourceAccountID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Исходный счёт не найден"})
		return
	}
	if srcAcc.Placeholder == 1 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Исходный счёт — контейнерный"})
		return
	}

	if len(req.Items) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Нет строк для импорта"})
		return
	}

	var sourceCurrency string
	if err := tx.QueryRow(`SELECT mnemonic FROM commodities WHERE id=?`, srcAcc.CommodityID).Scan(&sourceCurrency); err != nil {
		writeFinanceError(w, err)
		return
	}
	saved := 0
	for i, it := range req.Items {
		postDate, err := time.Parse("2006-01-02", it.Date)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Строка %d: неверная дата", i+1)})
			return
		}
		if it.OtherAccountID == 0 || it.OtherAccountID == req.SourceAccountID {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Строка %d: не выбран второй счёт", i+1)})
			return
		}
		// Проверяем, что other принадлежит юзеру и не контейнерный
		otherAcc, err := getAccountByIDFrom(tx, userID, it.OtherAccountID)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Строка %d: счёт-контрагент не найден", i+1)})
			return
		}
		if otherAcc.Placeholder == 1 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Строка %d: счёт-контрагент контейнерный", i+1)})
			return
		}

		if otherAcc.CommodityID != srcAcc.CommodityID {
			writeFinanceError(w, validationError(fmt.Sprintf("Строка %d: второй счёт должен быть в валюте исходного счёта", i+1)))
			return
		}
		if err := validateCSVCurrency(it.Currency, sourceCurrency); err != nil {
			writeFinanceError(w, validationError(fmt.Sprintf("Строка %d: %s", i+1, err)))
			return
		}
		valueNum, err := csvAmountCents(it.Amount)
		if err != nil {
			writeFinanceError(w, validationError(fmt.Sprintf("Строка %d: %s", i+1, err)))
			return
		}

		res, err := tx.Exec(`
			INSERT INTO transactions (user_id, post_date, enter_date, description, tags)
			VALUES (?, ?, ?, ?, '')
		`, userID, postDate, time.Now(), it.Description)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		txID, _ := res.LastInsertId()

		// Сплит на исходный счёт (со знаком как в CSV)
		_, err = tx.Exec(`
			INSERT INTO splits (user_id, tx_id, account_id, value_num, value_denom)
			VALUES (?, ?, ?, ?, 100)
		`, userID, txID, req.SourceAccountID, valueNum)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		// Парный сплит
		_, err = tx.Exec(`
			INSERT INTO splits (user_id, tx_id, account_id, value_num, value_denom)
			VALUES (?, ?, ?, ?, 100)
		`, userID, txID, it.OtherAccountID, -valueNum)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		saved++
	}

	if err := tx.Commit(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"result": "ok",
		"saved":  saved,
	})
}

// findDuplicateTransaction - ищет существующую транзакцию с такой же датой и суммой
// на указанном счёте (для пользователя). Возвращает 0, если не найдено.
func (h *Handler) findDuplicateTransaction(userID, accountID int64, date string, valueNum int64) int64 {
	var txID int64
	err := h.db.QueryRow(`
		SELECT t.id FROM transactions t
		JOIN splits s ON s.tx_id = t.id AND s.user_id = t.user_id
		WHERE t.user_id = ?
		  AND DATE(t.post_date) = ?
		  AND s.account_id = ?
		  AND s.value_num = ?
		LIMIT 1
	`, userID, date, accountID, valueNum).Scan(&txID)
	if err != nil {
		return 0
	}
	return txID
}

// guessOtherAccount - пытается угадать «второй» счёт по предыдущим транзакциям
// с похожим описанием на этом же исходном счёте.
func (h *Handler) guessOtherAccount(userID, sourceAccountID int64, description string, amount float64) int64 {
	if description == "" {
		return 0
	}
	// Ищем последнюю транзакцию на этом счёте с таким же описанием
	var otherID int64
	err := h.db.QueryRow(`
		SELECT s2.account_id
		FROM transactions t
		JOIN splits s1 ON s1.tx_id = t.id AND s1.account_id = ?
		JOIN splits s2 ON s2.tx_id = t.id AND s2.account_id != s1.account_id
		WHERE t.user_id = ? AND t.description = ?
		ORDER BY t.post_date DESC, t.id DESC
		LIMIT 1
	`, sourceAccountID, userID, description).Scan(&otherID)
	if err != nil {
		return 0
	}
	return otherID
}

// getOrCreateImbalanceAccount - находит или создаёт счёт «Дисбаланс»
// для размещения транзакций, у которых не определён второй счёт.
func (h *Handler) getOrCreateImbalanceAccount(userID, commodityID int64) (int64, string, error) {
	tx, err := h.beginFinanceWrite(userID)
	if err != nil {
		return 0, "", err
	}
	defer tx.Rollback()
	var id int64
	var name string
	err = tx.QueryRow(`SELECT id,name FROM accounts WHERE user_id=? AND commodity_id=? AND placeholder=0
 AND (name='Дисбаланс' OR name LIKE 'Дисбаланс-%' OR name='Imbalance' OR name LIKE 'Imbalance-%') ORDER BY id LIMIT 1`, userID, commodityID).Scan(&id, &name)
	if err == nil {
		return id, name, tx.Commit()
	}
	if err != sql.ErrNoRows {
		return 0, "", err
	}
	var currency string
	if err := tx.QueryRow(`SELECT mnemonic FROM commodities WHERE id=?`, commodityID).Scan(&currency); err != nil {
		return 0, "", err
	}
	name = "Дисбаланс-" + currency
	res, err := tx.Exec(`INSERT INTO accounts(user_id,name,account_type,commodity_id,commodity_scu,non_std_scu,description,hidden,placeholder)
 VALUES(?,?,'EQUITY',?,100,0,'Счёт для несбалансированных импортированных транзакций',0,0)`, userID, name, commodityID)
	if err != nil {
		return 0, "", err
	}
	id, err = res.LastInsertId()
	if err != nil {
		return 0, "", err
	}
	return id, name, tx.Commit()
}

func csvAmountCents(amount float64) (int64, error) {
	cents, err := transactionCents(math.Abs(amount))
	if err != nil {
		return 0, err
	}
	if amount < 0 {
		cents = -cents
	}
	return cents, nil
}

func validateCSVCurrency(currency, source string) error {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency != "" && currency != strings.ToUpper(source) {
		return validationError("Валюта строки CSV не совпадает с валютой исходного счёта")
	}
	return nil
}

// getAccountByID - возвращает счёт пользователя по ID
func (h *Handler) getAccountByID(userID, accountID int64) (*models.Account, error) {
	return getAccountByIDFrom(h.db, userID, accountID)
}

func getAccountByIDFrom(q interface {
	QueryRow(string, ...interface{}) *sql.Row
}, userID, accountID int64) (*models.Account, error) {
	var acc models.Account
	var parentID sql.NullInt64
	var code, description sql.NullString
	err := q.QueryRow(`
		SELECT id, name, account_type, commodity_id, commodity_scu, non_std_scu,
		       parent_id, code, description, hidden, placeholder
		FROM accounts WHERE id = ? AND user_id = ?
	`, accountID, userID).Scan(&acc.ID, &acc.Name, &acc.AccountType,
		&acc.CommodityID, &acc.CommoditySCU, &acc.NonStdSCU,
		&parentID, &code, &description, &acc.Hidden, &acc.Placeholder)
	if err != nil {
		return nil, err
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
	return &acc, nil
}

// getAccountsMap - карта id -> *Account для всех счетов пользователя
func (h *Handler) getAccountsMap(userID int64) (map[int64]*models.Account, error) {
	accounts, err := h.getAccounts(userID)
	if err != nil {
		return nil, err
	}
	result := make(map[int64]*models.Account, len(accounts))
	for _, acc := range accounts {
		result[acc.ID] = acc
	}
	return result, nil
}
