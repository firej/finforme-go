package database

import (
	"database/sql"
	"fmt"
)

// InitDB инициализирует базу данных и создает таблицы
func InitDB(db *sql.DB) error {
	// Создаём таблицы по одной (MariaDB не поддерживает несколько CREATE TABLE в одном Exec)
	tables := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id BIGINT PRIMARY KEY AUTO_INCREMENT,
			username VARCHAR(255) UNIQUE NOT NULL,
			email VARCHAR(255) NOT NULL,
			password_hash VARCHAR(255) NOT NULL,
			first_name VARCHAR(255),
			last_name VARCHAR(255),
			is_active TINYINT DEFAULT 1,
			is_admin TINYINT DEFAULT 0,
 session_version BIGINT NOT NULL DEFAULT 1,
 password_change_required TINYINT NOT NULL DEFAULT 0,
 password_expires_at DATETIME NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,

		`CREATE TABLE IF NOT EXISTS commodities (
			id BIGINT PRIMARY KEY AUTO_INCREMENT,
			namespace VARCHAR(255),
			mnemonic VARCHAR(255) NOT NULL,
			fullname VARCHAR(255),
			cusip VARCHAR(255),
			fraction INT NOT NULL,
			quote_source VARCHAR(255),
			quote_tz VARCHAR(255),
			sign VARCHAR(10)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,

		`CREATE TABLE IF NOT EXISTS accounts (
			id BIGINT PRIMARY KEY AUTO_INCREMENT,
			user_id BIGINT NOT NULL,
			name VARCHAR(255) NOT NULL,
			account_type VARCHAR(255) NOT NULL,
			commodity_id BIGINT DEFAULT 1,
			commodity_scu INT NOT NULL,
			non_std_scu INT NOT NULL,
			parent_id BIGINT,
			code VARCHAR(255),
			description TEXT,
			hidden TINYINT DEFAULT 0,
			placeholder TINYINT DEFAULT 0,
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
			FOREIGN KEY (commodity_id) REFERENCES commodities(id),
			FOREIGN KEY (parent_id) REFERENCES accounts(id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,

		`CREATE TABLE IF NOT EXISTS books (
			id BIGINT PRIMARY KEY AUTO_INCREMENT,
			user_id BIGINT NOT NULL,
			root_account_id BIGINT NOT NULL,
			root_template_id VARCHAR(255),
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
			FOREIGN KEY (root_account_id) REFERENCES accounts(id) ON DELETE CASCADE
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,

		`CREATE TABLE IF NOT EXISTS transactions (
			id BIGINT PRIMARY KEY AUTO_INCREMENT,
			user_id BIGINT NOT NULL,
			num VARCHAR(255),
			post_date DATETIME NOT NULL,
			enter_date DATETIME NOT NULL,
			description TEXT,
			tags TEXT,
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,

		`CREATE TABLE IF NOT EXISTS splits (
			id BIGINT PRIMARY KEY AUTO_INCREMENT,
			user_id BIGINT NOT NULL,
			tx_id BIGINT NOT NULL,
			account_id BIGINT NOT NULL,
			value_num BIGINT NOT NULL,
			value_denom INT DEFAULT 100,
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
			FOREIGN KEY (tx_id) REFERENCES transactions(id) ON DELETE CASCADE,
			FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,

		`CREATE TABLE IF NOT EXISTS api_tokens (
			id BIGINT PRIMARY KEY AUTO_INCREMENT,
			user_id BIGINT NOT NULL,
			name VARCHAR(255) NOT NULL,
			token_hash CHAR(64) NOT NULL UNIQUE,
			prefix VARCHAR(20) NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			last_used_at DATETIME NULL,
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,

		`CREATE TABLE IF NOT EXISTS currency_rates (
			code VARCHAR(20) NOT NULL COMMENT 'Например: USD/RUB, EUR/RUB, USDT/RUB',
			name VARCHAR(255) NOT NULL COMMENT 'Название валюты',
			rate DECIMAL(18,6) NOT NULL COMMENT 'Курс к рублю',
			source VARCHAR(50) NOT NULL COMMENT 'Источник: cbr, binance, bybit и т.д.',
			rate_date DATE NOT NULL COMMENT 'Дата курса',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			PRIMARY KEY (code, source, rate_date)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	}

	for _, table := range tables {
		if _, err := db.Exec(table); err != nil {
			return fmt.Errorf("failed to create table: %w", err)
		}
	}

	// Миграции: добавляем новые колонки если их нет (для существующих БД)
	migrations := []string{
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS is_admin TINYINT DEFAULT 0`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS session_version BIGINT NOT NULL DEFAULT 1`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS password_change_required TINYINT NOT NULL DEFAULT 0`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS password_expires_at DATETIME NULL`,
	}
	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil {
			return fmt.Errorf("failed to migrate users: %w", err)
		}
	}

	// Миграция: убираем transactions.currency_id (валюта теперь определяется
	// через accounts.commodity_id каждого сплита). Сначала FK, потом колонку.
	var fkName string
	err := db.QueryRow(`
		SELECT CONSTRAINT_NAME
		FROM information_schema.KEY_COLUMN_USAGE
		WHERE TABLE_SCHEMA = DATABASE()
		  AND TABLE_NAME = 'transactions'
		  AND COLUMN_NAME = 'currency_id'
		  AND REFERENCED_TABLE_NAME IS NOT NULL
		LIMIT 1
	`).Scan(&fkName)
	if err == nil && fkName != "" {
		db.Exec(fmt.Sprintf("ALTER TABLE transactions DROP FOREIGN KEY %s", fkName))
	}
	db.Exec(`ALTER TABLE transactions DROP COLUMN IF EXISTS currency_id`)

	// Создаём индексы для ускорения запросов
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_splits_account_user ON splits (account_id, user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_splits_tx_id ON splits (tx_id)`,
		`CREATE INDEX IF NOT EXISTS idx_splits_user_id ON splits (user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_transactions_user_id ON transactions (user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_transactions_post_date ON transactions (user_id, post_date)`,
		`CREATE INDEX IF NOT EXISTS idx_accounts_user_id ON accounts (user_id)`,
	}

	for _, idx := range indexes {
		// Игнорируем ошибки если индекс уже существует (для совместимости с разными версиями MariaDB)
		db.Exec(idx)
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
