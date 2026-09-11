package database

import "database/sql"

// InitOAuthDB uses portable DDL for MariaDB and SQLite authorization tests.
// Secrets are SHA-256 hashes; timestamps are UTC Unix seconds.
func InitOAuthDB(db *sql.DB) error {
	for _, query := range []string{
		`CREATE TABLE IF NOT EXISTS oauth_clients (
		 id VARCHAR(64) PRIMARY KEY, name VARCHAR(255) NOT NULL, redirects TEXT NOT NULL,
		 created_at BIGINT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS oauth_grants (
		 id VARCHAR(64) PRIMARY KEY, user_id BIGINT NOT NULL, client_id VARCHAR(64) NOT NULL,
		 scope VARCHAR(64) NOT NULL, resource VARCHAR(512) NOT NULL, session_version BIGINT NOT NULL,
		 created_at BIGINT NOT NULL, expires_at BIGINT NOT NULL, revoked INTEGER NOT NULL DEFAULT 0,
		 FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE,
		 FOREIGN KEY(client_id) REFERENCES oauth_clients(id))`,
		`CREATE TABLE IF NOT EXISTS oauth_codes (
		 hash VARCHAR(64) PRIMARY KEY, grant_id VARCHAR(64) NOT NULL,
		 redirect_uri VARCHAR(1024) NOT NULL, challenge VARCHAR(128) NOT NULL,
		 expires_at BIGINT NOT NULL, used INTEGER NOT NULL DEFAULT 0,
		 FOREIGN KEY(grant_id) REFERENCES oauth_grants(id) ON DELETE CASCADE)`,
		`CREATE TABLE IF NOT EXISTS oauth_tokens (
		 hash VARCHAR(64) PRIMARY KEY, grant_id VARCHAR(64) NOT NULL, kind VARCHAR(16) NOT NULL,
		 expires_at BIGINT NOT NULL, used INTEGER NOT NULL DEFAULT 0,
		 FOREIGN KEY(grant_id) REFERENCES oauth_grants(id) ON DELETE CASCADE)`,
	} {
		if _, err := db.Exec(query); err != nil {
			return err
		}
	}
	return nil
}
