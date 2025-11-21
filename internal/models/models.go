package models

import (
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
	CommoditySCU int        `json:"commodity_scu"`
	NonStdSCU    int        `json:"non_std_scu"`
	ParentID     *int64     `json:"parent_id"`
	Code         string     `json:"code"`
	Description  string     `json:"description"`
	Hidden       int        `json:"hidden"`
	Placeholder  int        `json:"placeholder"`
	Balance      float64    `json:"balance,omitempty"`
	Childs       []*Account `json:"childs,omitempty"`
}

// Transaction представляет транзакцию
type Transaction struct {
	ID          int64     `json:"id"`
	UserID      int64     `json:"user_id"`
	CurrencyID  int64     `json:"currency_id"`
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
	CreatedAt    time.Time `json:"created_at"`
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

// GetBalance вычисляет баланс счета
func (a *Account) GetBalance() float64 {
	if a.Balance != 0 {
		return a.Balance
	}

	if len(a.Childs) > 0 {
		balance := 0.0
		for _, child := range a.Childs {
			balance += child.GetBalance()
		}
		a.Balance = balance
		return balance
	}

	return a.Balance
}

// IsNegativeBalance проверяет, нужно ли инвертировать баланс для данного типа счета
func (a *Account) IsNegativeBalance() bool {
	return a.AccountType == AccountTypeIncome ||
		a.AccountType == AccountTypeEquity ||
		a.AccountType == AccountTypeLiability
}
