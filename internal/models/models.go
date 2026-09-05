package models

import (
	"fmt"
	"sort"
	"time"
)

// Commodity представляет валюту
type Commodity struct {
	ID          int64  `json:"id"`
	Namespace   string `json:"namespace"`
	Mnemonic    string `json:"mnemonic"`
	Fullname    string `json:"fullname"`
	Cusip       string `json:"cusip"`
	Fraction    int    `json:"fraction"`
	QuoteSource string `json:"quote_source"`
	QuoteTZ     string `json:"quote_tz"`
	Sign        string `json:"sign"`
}

// Account представляет счет
type Account struct {
	ID           int64      `json:"id"`
	UserID       int64      `json:"user_id"`
	Name         string     `json:"name"`
	AccountType  string     `json:"account_type"`
	CommodityID  int64      `json:"commodity_id"`
	Currency     string     `json:"currency,omitempty"`
	CommoditySCU int        `json:"commodity_scu"`
	NonStdSCU    int        `json:"non_std_scu"`
	ParentID     *int64     `json:"parent_id"`
	Code         string     `json:"code"`
	Description  string     `json:"description"`
	Hidden       int        `json:"hidden"`
	Placeholder  int        `json:"placeholder"`
	Balance      float64    `json:"balance,omitempty"`
	Childs       []*Account `json:"childs,omitempty"`
	Level        int        `json:"level,omitempty"`        // Уровень вложенности для отображения
	DisplayName  string     `json:"display_name,omitempty"` // Имя с отступами для select
	IsLast       bool       `json:"-"`                      // Является ли последним дочерним элементом
	TreeLines    []string   `json:"-"`                      // Линии дерева для каждого уровня: "pipe", "tee", "corner", "blank"
}

// Transaction представляет транзакцию
type Transaction struct {
	ID          int64     `json:"id"`
	UserID      int64     `json:"user_id"`
	Num         string    `json:"num"`
	PostDate    time.Time `json:"post_date"`
	EnterDate   time.Time `json:"enter_date"`
	Description string    `json:"description"`
	Tags        string    `json:"tags"`
	Value       float64   `json:"value,omitempty"`
}

// Split представляет часть транзакции
type Split struct {
	ID         int64 `json:"id"`
	UserID     int64 `json:"user_id"`
	TxID       int64 `json:"tx_id"`
	AccountID  int64 `json:"account_id"`
	ValueNum   int64 `json:"value_num"`
	ValueDenom int64 `json:"value_denom"`
}

// User представляет пользователя
type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	FirstName    string    `json:"first_name"`
	LastName     string    `json:"last_name"`
	IsActive     bool      `json:"is_active"`
	IsAdmin      bool      `json:"is_admin"`
	CreatedAt    time.Time `json:"created_at"`
}

// CurrencyRate представляет запись курса валюты
type CurrencyRate struct {
	Code      string  `json:"code"`
	Name      string  `json:"name"`
	Rate      float64 `json:"rate"`
	Source    string  `json:"source"`
	RateDate  string  `json:"rate_date"`
	CreatedAt string  `json:"created_at"`
}

// Book представляет книгу учета
type Book struct {
	ID             int64  `json:"id"`
	UserID         int64  `json:"user_id"`
	RootAccountID  int64  `json:"root_account_id"`
	RootTemplateID string `json:"root_template_id"`
}

const (
	// DefaultDenom - знаменатель по умолчанию для денежных значений
	DefaultDenom = 100

	// Типы счетов
	AccountTypeRoot      = "ROOT"
	AccountTypeAsset     = "ASSET"
	AccountTypeCash      = "CASH"
	AccountTypeBank      = "BANK"
	AccountTypeLiability = "LIABILITY"
	AccountTypeIncome    = "INCOME"
	AccountTypeExpense   = "EXPENSE"
	AccountTypeEquity    = "EQUITY"
)

// CurrencyBalance is a sum in one currency; currencies are never converted implicitly.
type CurrencyBalance struct {
	Currency string  `json:"currency"`
	Amount   float64 `json:"amount"`
}

// GetBalance returns the account's own balance, without mixing descendant currencies.
func (a *Account) GetBalance() float64 { return a.Balance }

// GetBalances sums this account and its descendants by currency without caching
// or mutating Balance. Hidden accounts still contribute to financial totals.
func (a *Account) GetBalances() []CurrencyBalance {
	totals := make(map[string]float64)
	var visit func(*Account)
	visit = func(acc *Account) {
		currency := acc.Currency
		if currency == "" {
			currency = fmt.Sprintf("Валюта #%d", acc.CommodityID)
		}
		if len(acc.Childs) == 0 || acc.Balance != 0 {
			totals[currency] += acc.Balance
		}
		for _, child := range acc.Childs {
			visit(child)
		}
	}
	visit(a)
	keys := make([]string, 0, len(totals))
	for c := range totals {
		keys = append(keys, c)
	}
	sort.Strings(keys)
	result := make([]CurrencyBalance, 0, len(keys))
	for _, c := range keys {
		result = append(result, CurrencyBalance{Currency: c, Amount: totals[c]})
	}
	return result
}

// IsNegativeBalance проверяет, нужно ли инвертировать баланс для данного типа счета
func (a *Account) IsNegativeBalance() bool {
	return a.AccountType == AccountTypeIncome ||
		a.AccountType == AccountTypeEquity ||
		a.AccountType == AccountTypeLiability
}
