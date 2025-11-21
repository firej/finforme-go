package database

import (
	"database/sql"
	"fmt"
)

// InitDB инициализирует базу данных и создает таблицы
func InitDB(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT UNIQUE NOT NULL,
		email TEXT NOT NULL,
		password_hash TEXT NOT NULL,
		first_name TEXT,
		last_name TEXT,
		is_active INTEGER DEFAULT 1,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS commodities (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		namespace TEXT,
		mnemonic TEXT NOT NULL,
		fullname TEXT,
		cusip TEXT,
		fraction INTEGER NOT NULL,
		quote_source TEXT,
		quote_tz TEXT,
		sign TEXT
	);

	CREATE TABLE IF NOT EXISTS accounts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		name TEXT NOT NULL,
		account_type TEXT NOT NULL,
		commodity_id INTEGER DEFAULT 1,
		commodity_scu INTEGER NOT NULL,
		non_std_scu INTEGER NOT NULL,
		parent_id INTEGER,
		code TEXT,
		description TEXT,
		hidden INTEGER DEFAULT 0,
		placeholder INTEGER DEFAULT 0,
		FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
		FOREIGN KEY (commodity_id) REFERENCES commodities(id),
		FOREIGN KEY (parent_id) REFERENCES accounts(id)
	);

	CREATE TABLE IF NOT EXISTS books (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		root_account_id INTEGER NOT NULL,
		root_template_id TEXT,
		FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
		FOREIGN KEY (root_account_id) REFERENCES accounts(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS transactions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		currency_id INTEGER DEFAULT 1,
		num TEXT,
		post_date DATETIME NOT NULL,
		enter_date DATETIME NOT NULL,
		description TEXT,
		tags TEXT DEFAULT '',
		FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
		FOREIGN KEY (currency_id) REFERENCES commodities(id)
	);

	CREATE INDEX IF NOT EXISTS idx_transactions_tags ON transactions(tags);
	CREATE INDEX IF NOT EXISTS idx_transactions_user_id ON transactions(user_id);
	CREATE INDEX IF NOT EXISTS idx_transactions_post_date ON transactions(post_date);

	CREATE TABLE IF NOT EXISTS splits (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		tx_id INTEGER NOT NULL,
		account_id INTEGER NOT NULL,
		value_num INTEGER NOT NULL,
		value_denom INTEGER DEFAULT 100,
		FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
		FOREIGN KEY (tx_id) REFERENCES transactions(id) ON DELETE CASCADE,
		FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_splits_tx_id ON splits(tx_id);
	CREATE INDEX IF NOT EXISTS idx_splits_account_id ON splits(account_id);
	CREATE INDEX IF NOT EXISTS idx_splits_user_id ON splits(user_id);
	`

	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}

	// Добавляем базовые валюты, если их нет
	if err := seedCommodities(db); err != nil {
		return fmt.Errorf("failed to seed commodities: %w", err)
	}

	return nil
}

// seedCommodities добавляет базовые валюты
func seedCommodities(db *sql.DB) error {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM commodities").Scan(&count)
	if err != nil {
		return err
	}

	if count > 0 {
		return nil // Валюты уже добавлены
	}

	commodities := []struct {
		mnemonic string
		fullname string
		fraction int
		sign     string
	}{
		{"RUB", "Российский рубль", 100, "₽"},
		{"USD", "US Dollar", 100, "$"},
		{"EUR", "Euro", 100, "€"},
		{"GBP", "British Pound", 100, "£"},
		{"CNY", "Chinese Yuan", 100, "¥"},
	}

	for _, c := range commodities {
		_, err := db.Exec(`
			INSERT INTO commodities (namespace, mnemonic, fullname, fraction, sign)
			VALUES ('CURRENCY', ?, ?, ?, ?)
		`, c.mnemonic, c.fullname, c.fraction, c.sign)
		if err != nil {
			return err
		}
	}

	return nil
}
