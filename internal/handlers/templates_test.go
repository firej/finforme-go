package handlers

// Интеграционные тесты шаблонов.
//
// Не требуют реальной БД — каждый шаблон рендерится с минимальным набором
// данных, который имитирует то, что передают хендлеры. Тесты ловят:
//   - ошибки парсинга (неизвестные функции, синтаксис)
//   - ошибки рантайма (обращение к несуществующим полям, неверный тип)
//
// Запуск:  go test ./internal/handlers/ -v -run TestTemplates

import (
	"database/sql"
	"fmt"
	"html/template"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/evbogdanov/finforme/internal/models"
)

// buildTestTemplates загружает все шаблоны с той же funcMap, что и основной код.
func buildTestTemplates(t *testing.T) *template.Template {
	t.Helper()

	funcMap := template.FuncMap{
		"dict": func(values ...interface{}) map[string]interface{} {
			if len(values)%2 != 0 {
				return nil
			}
			d := make(map[string]interface{}, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				k, ok := values[i].(string)
				if !ok {
					return nil
				}
				d[k] = values[i+1]
			}
			return d
		},
		"add":      func(a, b int) int { return a + b },
		"mul":      func(a, b int) int { return a * b },
		"iterate":  func(n int) []int { s := make([]int, n); for i := range s { s[i] = i }; return s },
		"upper":    strings.ToUpper,
		"eqStr":    func(a, b string) bool { return a == b },
		"derefInt64": func(p *int64) int64 {
			if p != nil {
				return *p
			}
			return 0
		},
		"formatMoney": func(v float64) string {
			if math.Abs(v) < 0.005 {
				return "0.00"
			}
			return fmt.Sprintf("%.2f", v)
		},
		"formatMoneyShort": func(v float64) string {
			abs := math.Abs(v)
			sign := ""
			if v < 0 {
				sign = "-"
			}
			switch {
			case abs >= 1_000_000:
				return fmt.Sprintf("%s%.1fM", sign, abs/1_000_000)
			case abs >= 1_000:
				return fmt.Sprintf("%s%.1fK", sign, abs/1_000)
			default:
				return fmt.Sprintf("%s%.0f", sign, abs)
			}
		},
		"slice": func(s string, i, j int) string {
			r := []rune(s)
			if i < 0 {
				i = 0
			}
			if j > len(r) {
				j = len(r)
			}
			if i >= j {
				return ""
			}
			return string(r[i:j])
		},
		"formatDateGroup": func(dateStr string) string {
			t2, err := time.Parse("2006-01-02", dateStr)
			if err != nil {
				return dateStr
			}
			now := time.Now()
			today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
			d := time.Date(t2.Year(), t2.Month(), t2.Day(), 0, 0, 0, 0, now.Location())
			months := []string{
				"января", "февраля", "марта", "апреля", "мая", "июня",
				"июля", "августа", "сентября", "октября", "ноября", "декабря",
			}
			switch {
			case d.Equal(today):
				return "Сегодня"
			case d.Equal(today.AddDate(0, 0, -1)):
				return "Вчера"
			default:
				if t2.Year() == now.Year() {
					return fmt.Sprintf("%d %s", t2.Day(), months[t2.Month()-1])
				}
				return fmt.Sprintf("%d %s %d", t2.Day(), months[t2.Month()-1], t2.Year())
			}
		},
	}

	tmpl, err := template.New("").Funcs(funcMap).ParseGlob("../../templates/*.html")
	if err != nil {
		t.Fatalf("template parse error: %v", err)
	}
	return tmpl
}

// render выполняет шаблон с данными и возвращает ошибку.
func render(tmpl *template.Template, name string, data interface{}) error {
	var buf strings.Builder
	return tmpl.ExecuteTemplate(&buf, name, data)
}

// ─── Тестовые данные ────────────────────────────────────────────────────────

func testUser() models.User {
	return models.User{
		ID:        1,
		Username:  "testuser",
		Email:     "test@example.com",
		FirstName: "Test",
		LastName:  "User",
	}
}

func testAccount(id int64, typ string) *models.Account {
	return &models.Account{
		ID:          id,
		Name:        "Test Account " + fmt.Sprint(id),
		AccountType: typ,
		Balance:     1234.56,
	}
}

func testAccountTree() []*models.Account {
	root := testAccount(1, models.AccountTypeAsset)
	child := testAccount(2, models.AccountTypeBank)
	root.Childs = []*models.Account{child}
	return []*models.Account{root}
}

func testTransactions() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"id":                   int64(1),
			"post_date":            "2024-03-15",
			"description":          "Test transaction",
			"account_id":           int64(2),
			"account_name":         "Bank",
			"plus_balance_changing": 500.0,
			"balance_changing":      0.0,
			"account_balance":       1234.56,
			"tags":                 []string{"food"},
		},
		{
			"id":                   int64(2),
			"post_date":            "2024-03-14",
			"description":          "Expense",
			"account_id":           int64(3),
			"account_name":         "Cash",
			"plus_balance_changing": 0.0,
			"balance_changing":      200.0,
			"account_balance":       734.56,
			"tags":                 []string{},
		},
	}
}

func baseData(user models.User, tree []*models.Account) map[string]interface{} {
	return map[string]interface{}{
		"Authenticated": true,
		"IsAdmin":       false,
		"ActivePage":    "",
		"User":          user,
		"AccountTree":   tree,
	}
}

// ─── Тесты ──────────────────────────────────────────────────────────────────

func TestTemplates_Parse(t *testing.T) {
	// Если parse упал, buildTestTemplates сам вызовет t.Fatalf.
	buildTestTemplates(t)
}

func TestTemplates_Login(t *testing.T) {
	tmpl := buildTestTemplates(t)
	data := map[string]interface{}{
		"Title": "Вход", "Error": "", "Next": "/finance/",
		"Authenticated": false, "AccountTree": nil, "User": nil, "IsAdmin": false, "ActivePage": "",
	}
	if err := render(tmpl, "login.html", data); err != nil {
		t.Errorf("login.html: %v", err)
	}
}

func TestTemplates_LoginWithError(t *testing.T) {
	tmpl := buildTestTemplates(t)
	data := map[string]interface{}{
		"Title": "Вход", "Error": "Неверный пароль", "Next": "",
		"Authenticated": false, "AccountTree": nil, "User": nil, "IsAdmin": false, "ActivePage": "",
	}
	if err := render(tmpl, "login.html", data); err != nil {
		t.Errorf("login.html with error: %v", err)
	}
}

func TestTemplates_Register(t *testing.T) {
	tmpl := buildTestTemplates(t)
	data := map[string]interface{}{
		"Title": "Регистрация", "Error": "",
		"Authenticated": false, "AccountTree": nil, "User": nil, "IsAdmin": false, "ActivePage": "",
	}
	if err := render(tmpl, "account_register.html", data); err != nil {
		t.Errorf("account_register.html: %v", err)
	}
}

func TestTemplates_Index(t *testing.T) {
	tmpl := buildTestTemplates(t)
	u := testUser()
	data := baseData(u, testAccountTree())
	data["Title"] = "Дашборд"
	data["TotalAssets"] = 150000.0
	data["TotalLiabilities"] = 30000.0
	data["NetWorth"] = 120000.0
	data["TotalIncome"] = 50000.0
	data["TotalExpense"] = 20000.0
	data["TopAccounts"] = []map[string]interface{}{
		{"ID": int64(1), "Name": "Сбербанк", "AccountType": "BANK", "Balance": 100000.0},
	}
	data["RecentTransactions"] = []map[string]interface{}{
		{"ID": int64(1), "Description": "Зарплата", "PostDate": "2024-03-15", "Amount": 50000.0, "AccountName": "Сбербанк", "AccountType": "BANK"},
	}
	if err := render(tmpl, "index.html", data); err != nil {
		t.Errorf("index.html: %v", err)
	}
}

func TestTemplates_AccountInfo(t *testing.T) {
	tmpl := buildTestTemplates(t)
	u := testUser()
	data := baseData(u, testAccountTree())
	data["Title"] = "Аккаунт"
	if err := render(tmpl, "account_info.html", data); err != nil {
		t.Errorf("account_info.html: %v", err)
	}
}

func TestTemplates_ChangeInfoForm(t *testing.T) {
	tmpl := buildTestTemplates(t)
	u := testUser()
	data := baseData(u, testAccountTree())
	data["Title"] = "Изменение информации"
	if err := render(tmpl, "change_info_form.html", data); err != nil {
		t.Errorf("change_info_form.html: %v", err)
	}
}

func TestTemplates_PasswordChangeForm(t *testing.T) {
	tmpl := buildTestTemplates(t)
	u := testUser()
	data := baseData(u, testAccountTree())
	data["Title"] = "Смена пароля"
	if err := render(tmpl, "password_change_form.html", data); err != nil {
		t.Errorf("password_change_form.html: %v", err)
	}
}

func TestTemplates_FinanceWelcome(t *testing.T) {
	tmpl := buildTestTemplates(t)
	u := testUser()
	data := baseData(u, nil)
	data["Title"] = "finfor.me"
	if err := render(tmpl, "finance_welcome.html", data); err != nil {
		t.Errorf("finance_welcome.html: %v", err)
	}
}

func TestTemplates_Finance(t *testing.T) {
	tmpl := buildTestTemplates(t)
	u := testUser()
	tree := testAccountTree()
	data := baseData(u, tree)
	data["Title"] = "finfor.me"
	data["AccountTree"] = tree
	data["Commodities"] = []*models.Commodity{}
	data["ActivePage"] = "finance"
	if err := render(tmpl, "finance.html", data); err != nil {
		t.Errorf("finance.html: %v", err)
	}
}

func TestTemplates_FinanceTransactions(t *testing.T) {
	tmpl := buildTestTemplates(t)
	u := testUser()
	acc := testAccount(2, models.AccountTypeBank)
	txs := testTransactions()

	data := baseData(u, testAccountTree())
	data["Title"] = acc.Name
	data["Account"] = acc
	data["Transactions"] = txs
	data["SortOrder"] = "desc"
	data["OppositeSortOrder"] = "asc"
	data["ActiveAccountID"] = int64(2)
	data["ActivePage"] = "transactions"
	data["TotalIncome"] = 500.0
	data["TotalExpense"] = 200.0
	data["TotalCount"] = 2
	data["IncomeCount"] = 1
	data["ExpenseCount"] = 1
	data["AvailableMonths"] = []map[string]string{
		{"Value": "2024-03", "Label": "Март 2024"},
	}
	data["Accounts"] = []*models.Account{acc}
	data["Commodities"] = []*models.Commodity{}
	if err := render(tmpl, "finance_transactions.html", data); err != nil {
		t.Errorf("finance_transactions.html: %v", err)
	}
}

func TestTemplates_FinanceTransactionsTbody(t *testing.T) {
	tmpl := buildTestTemplates(t)
	acc := testAccount(2, models.AccountTypeBank)
	data := map[string]interface{}{
		"Transactions": testTransactions(),
		"Account":      acc,
	}
	if err := render(tmpl, "finance_transactions_tbody.html", data); err != nil {
		t.Errorf("finance_transactions_tbody.html: %v", err)
	}
}

func TestTemplates_FinanceAccount_New(t *testing.T) {
	tmpl := buildTestTemplates(t)
	u := testUser()
	data := baseData(u, testAccountTree())
	data["Title"] = "Новый счёт"
	data["Account"] = nil
	data["Accounts"] = []*models.Account{testAccount(1, models.AccountTypeAsset)}
	data["Commodities"] = []*models.Commodity{
		{ID: 1, Fullname: "Russian Ruble", Sign: "₽"},
	}
	data["ActivePage"] = "finance"
	if err := render(tmpl, "finance_account.html", data); err != nil {
		t.Errorf("finance_account.html (new): %v", err)
	}
}

func TestTemplates_FinanceAccount_Edit(t *testing.T) {
	tmpl := buildTestTemplates(t)
	u := testUser()
	acc := testAccount(2, models.AccountTypeBank)
	parentID := int64(1)
	acc.ParentID = &parentID

	data := baseData(u, testAccountTree())
	data["Title"] = "Редактирование счёта"
	data["Account"] = acc
	data["Accounts"] = []*models.Account{testAccount(1, models.AccountTypeAsset), acc}
	data["Commodities"] = []*models.Commodity{
		{ID: 1, Fullname: "Russian Ruble", Sign: "₽"},
	}
	data["ActivePage"] = "finance"
	if err := render(tmpl, "finance_account.html", data); err != nil {
		t.Errorf("finance_account.html (edit): %v", err)
	}
}

func TestTemplates_FinanceAccountDrawerForm(t *testing.T) {
	tmpl := buildTestTemplates(t)
	data := map[string]interface{}{
		"Account":     nil,
		"Accounts":    []*models.Account{testAccount(1, models.AccountTypeAsset)},
		"Commodities": []*models.Commodity{{ID: 1, Fullname: "Ruble", Sign: "₽"}},
	}
	if err := render(tmpl, "finance_account_drawer_form.html", data); err != nil {
		t.Errorf("finance_account_drawer_form.html: %v", err)
	}
}

func TestTemplates_FinanceTransactionModalForm(t *testing.T) {
	tmpl := buildTestTemplates(t)
	acc := testAccount(2, models.AccountTypeBank)
	data := map[string]interface{}{
		"Transaction": nil,
		"Account":     acc,
		"Accounts":    []*models.Account{testAccount(1, models.AccountTypeAsset), acc},
		"Debit":       []map[string]interface{}{},
		"Credit":      []map[string]interface{}{},
	}
	if err := render(tmpl, "finance_transaction_modal_form.html", data); err != nil {
		t.Errorf("finance_transaction_modal_form.html: %v", err)
	}
}

func TestTemplates_FinanceTransaction(t *testing.T) {
	tmpl := buildTestTemplates(t)
	u := testUser()
	acc := testAccount(2, models.AccountTypeBank)
	data := baseData(u, testAccountTree())
	data["Title"] = "Транзакция"
	data["Transaction"] = nil
	data["Debit"] = []map[string]interface{}{}
	data["Credit"] = []map[string]interface{}{}
	data["AccountID"] = int64(2)
	data["Accounts"] = []*models.Account{testAccount(1, models.AccountTypeAsset), acc}
	data["ActivePage"] = "transactions"
	if err := render(tmpl, "finance_transaction.html", data); err != nil {
		t.Errorf("finance_transaction.html: %v", err)
	}
}

func TestTemplates_FinanceTransactionsByTag(t *testing.T) {
	tmpl := buildTestTemplates(t)
	u := testUser()
	data := baseData(u, testAccountTree())
	data["Title"] = "Транзакции с тегом: food"
	data["Tag"] = "food"
	data["Transactions"] = []map[string]interface{}{
		{
			"id": int64(1), "post_date": "2024-03-15",
			"description": "Test", "value": 100.0,
			"splits": []map[string]interface{}{
				{"account_name": "Bank", "value": 100.0},
			},
		},
	}
	data["ActivePage"] = "transactions"
	if err := render(tmpl, "finance_transactions_by_tag.html", data); err != nil {
		t.Errorf("finance_transactions_by_tag.html: %v", err)
	}
}

func TestTemplates_FinanceSettings(t *testing.T) {
	tmpl := buildTestTemplates(t)
	u := testUser()
	data := baseData(u, testAccountTree())
	data["Title"] = "Настройки"
	data["ActivePage"] = "settings"
	if err := render(tmpl, "finance_settings.html", data); err != nil {
		t.Errorf("finance_settings.html: %v", err)
	}
}

func TestTemplates_Currency(t *testing.T) {
	tmpl := buildTestTemplates(t)
	u := testUser()
	data := baseData(u, testAccountTree())
	data["Title"] = "Курсы валют"
	data["ActivePage"] = "currency"
	data["UpdatedAt"] = time.Now().Format("15:04")
	data["Rates"] = nil
	data["ChartsJSON"] = template.JS("[]")
	if err := render(tmpl, "currency.html", data); err != nil {
		t.Errorf("currency.html (no rates): %v", err)
	}
}

func TestTemplates_MortgageCalculator(t *testing.T) {
	tmpl := buildTestTemplates(t)
	u := testUser()
	data := baseData(u, testAccountTree())
	data["Title"] = "Ипотечный калькулятор"
	data["ActivePage"] = "mortgage"
	if err := render(tmpl, "mortgage_calculator.html", data); err != nil {
		t.Errorf("mortgage_calculator.html: %v", err)
	}
}

func TestTemplates_Admin_Users(t *testing.T) {
	tmpl := buildTestTemplates(t)
	u := testUser()
	data := baseData(u, testAccountTree())
	data["Title"] = "Админка"
	data["Tab"] = "users"
	data["Users"] = []models.User{u}
	data["Msg"] = ""
	data["Error"] = ""
	if err := render(tmpl, "admin.html", data); err != nil {
		t.Errorf("admin.html (users): %v", err)
	}
}

func TestTemplates_Admin_Rates(t *testing.T) {
	tmpl := buildTestTemplates(t)
	u := testUser()
	data := baseData(u, testAccountTree())
	data["Title"] = "Админка"
	data["Tab"] = "rates"
	data["Rates"] = nil
	data["Codes"] = []string{"USD/RUB"}
	data["Sources"] = []string{"cbr"}
	data["FilterCode"] = ""
	data["FilterSource"] = ""
	data["EditRate"] = nil
	data["Msg"] = ""
	data["Error"] = ""
	if err := render(tmpl, "admin.html", data); err != nil {
		t.Errorf("admin.html (rates): %v", err)
	}
}

// Проверяем что models.User и models.Commodity имеют нужные поля —
// ловим опечатки при рефакторинге.
func TestModels_FieldsExist(t *testing.T) {
	u := models.User{}
	_ = u.ID
	_ = u.Username
	_ = u.Email
	_ = u.FirstName
	_ = u.LastName

	c := models.Commodity{}
	_ = c.ID
	_ = c.Fullname
	_ = c.Sign

	a := models.Account{}
	_ = a.ID
	_ = a.Name
	_ = a.AccountType
	_ = a.Balance
	_ = a.Childs
	_ = a.Hidden
	_ = a.ParentID
}


// Заглушка — чтобы компилятор не ругался на неиспользуемый импорт sql.
var _ = sql.ErrNoRows
