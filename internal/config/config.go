package config

import (
	"os"
)

// Config содержит конфигурацию приложения
type Config struct {
	Port          string
	DatabasePath  string
	SessionSecret string
	SecureCookie  bool
}

// Load загружает конфигурацию из переменных окружения
func Load() *Config {
	return &Config{
		Port:          getEnv("PORT", "8000"),
		DatabasePath:  getEnv("DATABASE_PATH", "finforme.db"),
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
