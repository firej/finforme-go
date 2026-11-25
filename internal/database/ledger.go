package database

import (
	"database/sql"
	"fmt"
)

// InitLedgerDB also runs against the isolated SQLite databases used by tests.
func InitLedgerDB(db *sql.DB, sqlite bool) error {
	id := "BIGINT PRIMARY KEY AUTO_INCREMENT"
	if sqlite {
		id = "INTEGER PRIMARY KEY AUTOINCREMENT"
	}
	tables := []string{
		`CREATE TABLE IF NOT EXISTS transaction_history (
   id ` + id + `, user_id BIGINT NOT NULL, transaction_id BIGINT NOT NULL,
   action VARCHAR(16) NOT NULL, source VARCHAR(16) NOT NULL, created_at DATETIME NOT NULL,
   before_json MEDIUMTEXT NOT NULL, after_json MEDIUMTEXT NOT NULL, restored_id BIGINT NOT NULL DEFAULT 0,
   FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE)`,
		`CREATE TABLE IF NOT EXISTS csv_import_batches (
   user_id BIGINT NOT NULL, request_key VARCHAR(64) NOT NULL, payload_hash VARCHAR(64) NOT NULL,
   saved_count INTEGER NOT NULL, created_at DATETIME NOT NULL,
   PRIMARY KEY(user_id,request_key), FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE)`,
		`CREATE TABLE IF NOT EXISTS transaction_checks (
   user_id BIGINT NOT NULL, transaction_id BIGINT NOT NULL, checked_at DATETIME NOT NULL,
   PRIMARY KEY(user_id,transaction_id), FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE,
   FOREIGN KEY(transaction_id) REFERENCES transactions(id) ON DELETE CASCADE)`,
		`CREATE TABLE IF NOT EXISTS account_reconciliations (
   id ` + id + `, user_id BIGINT NOT NULL, account_id BIGINT NOT NULL, as_of DATE NOT NULL,
   expected_num BIGINT NOT NULL, expected_denom BIGINT NOT NULL, ledger_hash VARCHAR(64) NOT NULL,
   created_at DATETIME NOT NULL,
   FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE,
   FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE)`,
	}
	for _, q := range tables {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("ledger migration: %w", err)
		}
	}
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_history_user_id ON transaction_history(user_id,id)`,
		`CREATE INDEX IF NOT EXISTS idx_history_transaction ON transaction_history(user_id,transaction_id,id)`,
		`CREATE INDEX IF NOT EXISTS idx_reconciliation_account ON account_reconciliations(user_id,account_id,id)`,
	}
	for _, q := range indexes {
		if _, err := db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}
