package handlers

import (
	"database/sql"
	"fmt"
	"math/rand"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	DemoUsername  = "demo"
	DemoEmail     = "demo@finfor.me"
	DemoFirstName = "Demo"
	DemoLastName  = "User"
)

// SeedDemoUser создаёт демо-пользователя со счетами и транзакциями, если его нет.
// Кэширует ID найденного/созданного пользователя в Handler для быстрой проверки демо-доступа.
func (h *Handler) SeedDemoUser() error {
	var id int64
	err := h.db.QueryRow("SELECT id FROM users WHERE username = ?", DemoUsername).Scan(&id)
	if err == nil {
		h.demoUserID = id
		return nil
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("lookup demo user: %w", err)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte("demo"), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash demo password: %w", err)
	}

	result, err := h.db.Exec(`
		INSERT INTO users (username, email, password_hash, first_name, last_name, is_active)
		VALUES (?, ?, ?, ?, ?, 1)
	`, DemoUsername, DemoEmail, string(hash), DemoFirstName, DemoLastName)
	if err != nil {
		return fmt.Errorf("insert demo user: %w", err)
	}
	id, _ = result.LastInsertId()
	h.demoUserID = id

	if err := h.createBaseAccounts(id); err != nil {
		return fmt.Errorf("seed demo accounts: %w", err)
	}
	if err := h.seedDemoTransactions(id); err != nil {
		return fmt.Errorf("seed demo transactions: %w", err)
	}
	return nil
}

// LoginDemo логинит пользователя как демо без пароля.
func (h *Handler) LoginDemo(w http.ResponseWriter, r *http.Request) {
	if h.demoUserID == 0 {
		http.Error(w, "Демо-аккаунт не настроен", http.StatusServiceUnavailable)
		return
	}
	if err := h.setUserID(w, r, h.demoUserID); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/finance/dashboard", http.StatusSeeOther)
}

// isDemo возвращает true, если userID совпадает с кэшированным демо-пользователем.
func (h *Handler) isDemo(userID int64) bool {
	return h.demoUserID != 0 && userID == h.demoUserID
}

// seedDemoTransactions создаёт реалистичный набор транзакций за последние ~90 дней.
func (h *Handler) seedDemoTransactions(userID int64) error {
	accounts, err := h.demoAccountIDs(userID)
	if err != nil {
		return err
	}

	// Стартовый баланс: с "Начального баланса" на банковский счёт
	// Затем регулярные доходы и расходы
	rng := rand.New(rand.NewSource(42)) // детерминированно для повторяемости
	now := time.Now()
	startDate := now.AddDate(0, 0, -90)

	type entry struct {
		date  time.Time
		desc  string
		tags  string
		debit string  // куда деньги пришли (увеличивается)
		credit string // откуда ушли (уменьшается)
		amount float64
	}
	var entries []entry

	// Начальный баланс на текущем счёте
	entries = append(entries, entry{
		date: startDate, desc: "Начальный баланс", tags: "",
		debit: "Расчетный счет", credit: "Начальный баланс", amount: 50000,
	})
	entries = append(entries, entry{
		date: startDate, desc: "Начальный баланс", tags: "",
		debit: "Наличные", credit: "Начальный баланс", amount: 8000,
	})

	// Зарплата 1 и 15 числа каждого месяца за последние 3 месяца
	for m := 3; m >= 0; m-- {
		d := time.Date(now.Year(), now.Month(), 1, 9, 30, 0, 0, now.Location()).AddDate(0, -m, 0)
		if d.After(startDate) && d.Before(now) {
			entries = append(entries, entry{
				date: d, desc: "Аванс", tags: "salary",
				debit: "Расчетный счет", credit: "Зарплата", amount: 60000,
			})
		}
		d2 := d.AddDate(0, 0, 14)
		if d2.After(startDate) && d2.Before(now) {
			entries = append(entries, entry{
				date: d2, desc: "Зарплата", tags: "salary",
				debit: "Расчетный счет", credit: "Зарплата", amount: 90000,
			})
		}
	}

	// Продукты — раз в 2-3 дня
	groceries := []string{"Перекрёсток", "Пятёрочка", "Магнит", "ВкусВилл", "Лента", "Ашан"}
	for d := startDate; d.Before(now); d = d.AddDate(0, 0, 2+rng.Intn(2)) {
		amt := 1500 + float64(rng.Intn(4500))
		entries = append(entries, entry{
			date: d, desc: groceries[rng.Intn(len(groceries))], tags: "food",
			debit: "Продукты", credit: "Расчетный счет", amount: amt,
		})
	}

	// Транспорт — почти каждый день
	for d := startDate; d.Before(now); d = d.AddDate(0, 0, 1+rng.Intn(2)) {
		if rng.Intn(3) == 0 {
			continue
		}
		amt := 50 + float64(rng.Intn(450))
		desc := "Метро"
		if rng.Intn(4) == 0 {
			desc = "Такси"
			amt = 250 + float64(rng.Intn(800))
		}
		entries = append(entries, entry{
			date: d, desc: desc, tags: "transport",
			debit: "Транспорт", credit: "Расчетный счет", amount: amt,
		})
	}

	// Коммуналка — раз в месяц
	for m := 2; m >= 0; m-- {
		d := time.Date(now.Year(), now.Month(), 10, 12, 0, 0, 0, now.Location()).AddDate(0, -m, 0)
		if d.Before(startDate) || d.After(now) {
			continue
		}
		entries = append(entries,
			entry{date: d, desc: "Электричество", tags: "utilities", debit: "Электричество", credit: "Расчетный счет", amount: 1200 + float64(rng.Intn(800))},
			entry{date: d, desc: "Интернет", tags: "utilities", debit: "Интернет", credit: "Расчетный счет", amount: 600},
			entry{date: d, desc: "Мобильная связь", tags: "utilities", debit: "Телефон", credit: "Расчетный счет", amount: 500},
			entry{date: d, desc: "Вода", tags: "utilities", debit: "Вода", credit: "Расчетный счет", amount: 800 + float64(rng.Intn(400))},
		)
	}

	// Развлечения, одежда, здоровье — несколько раз в месяц
	misc := []struct {
		debit string
		desc  string
		tag   string
		min, max float64
	}{
		{"Развлечения", "Кино", "fun", 400, 1200},
		{"Развлечения", "Кафе", "fun", 800, 3500},
		{"Развлечения", "Концерт", "fun", 2500, 6000},
		{"Одежда", "Покупка одежды", "shopping", 2000, 8000},
		{"Здоровье", "Аптека", "health", 300, 2500},
		{"Подарки", "Подарок", "gifts", 1500, 5000},
	}
	for d := startDate; d.Before(now); d = d.AddDate(0, 0, 5+rng.Intn(4)) {
		m := misc[rng.Intn(len(misc))]
		amt := m.min + rng.Float64()*(m.max-m.min)
		entries = append(entries, entry{
			date: d, desc: m.desc, tags: m.tag,
			debit: m.debit, credit: "Расчетный счет", amount: amt,
		})
	}

	// Переводы между счетами — снятие наличных
	for m := 2; m >= 0; m-- {
		d := time.Date(now.Year(), now.Month(), 5, 14, 0, 0, 0, now.Location()).AddDate(0, -m, 0)
		if d.Before(startDate) || d.After(now) {
			continue
		}
		entries = append(entries, entry{
			date: d, desc: "Снятие наличных", tags: "transfer",
			debit: "Наличные", credit: "Расчетный счет", amount: 5000,
		})
	}

	// Вставляем
	tx, err := h.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, e := range entries {
		debitID, ok1 := accounts[e.debit]
		creditID, ok2 := accounts[e.credit]
		if !ok1 || !ok2 {
			continue
		}
		valueNum := int64(e.amount * 100)
		res, err := tx.Exec(`
			INSERT INTO transactions (user_id, currency_id, post_date, enter_date, description, tags)
			VALUES (?, 1, ?, ?, ?, ?)
		`, userID, e.date, e.date, e.desc, e.tags)
		if err != nil {
			return err
		}
		txID, _ := res.LastInsertId()
		if _, err := tx.Exec(`
			INSERT INTO splits (user_id, tx_id, account_id, value_num, value_denom)
			VALUES (?, ?, ?, ?, 100)
		`, userID, txID, debitID, valueNum); err != nil {
			return err
		}
		if _, err := tx.Exec(`
			INSERT INTO splits (user_id, tx_id, account_id, value_num, value_denom)
			VALUES (?, ?, ?, ?, 100)
		`, userID, txID, creditID, -valueNum); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// demoAccountIDs возвращает map "имя счёта" → ID для счетов демо-пользователя.
func (h *Handler) demoAccountIDs(userID int64) (map[string]int64, error) {
	rows, err := h.db.Query("SELECT id, name FROM accounts WHERE user_id = ?", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]int64)
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[name] = id
	}
	return out, rows.Err()
}
