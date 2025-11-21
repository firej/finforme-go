.PHONY: build run clean test help

# Переменные
BINARY_NAME=finforme
BUILD_DIR=bin
MAIN_PATH=cmd/server/main.go

# Цвета для вывода
GREEN=\033[0;32m
NC=\033[0m # No Color

help: ## Показать справку
	@echo "Доступные команды:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  $(GREEN)%-15s$(NC) %s\n", $$1, $$2}'

build: ## Собрать приложение
	@echo "$(GREEN)Сборка приложения...$(NC)"
	@mkdir -p $(BUILD_DIR)
	@go build -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)
	@echo "$(GREEN)✓ Сборка завершена: $(BUILD_DIR)/$(BINARY_NAME)$(NC)"

run: ## Запустить приложение в режиме разработки
	@echo "$(GREEN)Запуск приложения...$(NC)"
	@go run $(MAIN_PATH)

clean: ## Очистить собранные файлы
	@echo "$(GREEN)Очистка...$(NC)"
	@rm -rf $(BUILD_DIR)
	@rm -f finforme.db
	@echo "$(GREEN)✓ Очистка завершена$(NC)"

test: ## Запустить тесты
	@echo "$(GREEN)Запуск тестов...$(NC)"
	@go test -v ./...

deps: ## Установить зависимости
	@echo "$(GREEN)Установка зависимостей...$(NC)"
	@go mod download
	@go mod tidy
	@echo "$(GREEN)✓ Зависимости установлены$(NC)"

fmt: ## Форматировать код
	@echo "$(GREEN)Форматирование кода...$(NC)"
	@go fmt ./...
	@echo "$(GREEN)✓ Форматирование завершено$(NC)"

vet: ## Проверить код на ошибки
	@echo "$(GREEN)Проверка кода...$(NC)"
	@go vet ./...
	@echo "$(GREEN)✓ Проверка завершена$(NC)"

lint: fmt vet ## Запустить все проверки кода

dev: deps run ## Установить зависимости и запустить в режиме разработки

all: clean deps build ## Полная пересборка проекта
