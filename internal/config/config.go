package config

import (
	"os"
)

// Config содержит конфигурацию приложения
type Config struct {
	Port          string
	DatabaseDSN   string // DSN для MariaDB: user:password@tcp(host:port)/dbname?parseTime=true
	SessionSecret string
	SecureCookie  bool
}

// Load загружает конфигурацию из переменных окружения
func Load() *Config {
	return &Config{
		Port:          getEnv("PORT", "8080"),
		DatabaseDSN:   getEnv("DATABASE_DSN", "finforme:finforme@tcp(localhost:3306)/finforme?parseTime=true&charset=utf8mb4"),
		SessionSecret: getEnv("SESSION_SECRET", "change-me-in-production"),
		SecureCookie:  getEnv("SECURE_COOKIE", "false") == "true",
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
