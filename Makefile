.PHONY: build run clean test help up down logs shell docker docker-run docker-stop deploy deploy-bot deploy-all db-shell bot-build bot-run bot-dev

# Переменные
BINARY_NAME=finforme
BUILD_DIR=bin
MAIN_PATH=cmd/server/main.go
BOT_BINARY_NAME=telegram-bot
BOT_MAIN_PATH=./cmd/telegram-bot

# Цвета для вывода
GREEN=\033[0;32m
NC=\033[0m # No Color

help: ## Показать справку
	@echo "Доступные команды:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  $(GREEN)%-15s$(NC) %s\n", $$1, $$2}'

build: ## Собрать приложение
	@echo "$(GREEN)Сборка приложения...$(NC)"
	@mkdir -p $(BUILD_DIR)
	@CGO_ENABLED=0 go build -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)
	@echo "$(GREEN)✓ Сборка завершена: $(BUILD_DIR)/$(BINARY_NAME)$(NC)"

run: ## Запустить приложение (требуется запущенная MariaDB)
	@echo "$(GREEN)Запуск приложения...$(NC)"
	@go run $(MAIN_PATH)

clean: ## Очистить собранные файлы
	@echo "$(GREEN)Очистка...$(NC)"
	@rm -rf $(BUILD_DIR)
	@echo "$(GREEN)✓ Очистка завершена$(NC)"

test: ## Запустить тесты
	@echo "$(GREEN)Запуск тестов...$(NC)"
	@go test -v ./...
	@node --test tests/*.test.cjs

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

all: clean deps build ## Полная пересборка проекта

# Docker Compose команды
COMPOSE_ENGINE ?= $(shell command -v podman-compose 2>/dev/null || command -v docker-compose 2>/dev/null || echo "docker compose")

up: ## Запустить приложение с MariaDB через docker-compose
	@echo "$(GREEN)Запуск через docker-compose...$(NC)"
	@$(COMPOSE_ENGINE) up -d
	@echo "$(GREEN)✓ Приложение запущено: http://localhost:8080$(NC)"

down: ## Остановить docker-compose
	@echo "$(GREEN)Остановка docker-compose...$(NC)"
	@$(COMPOSE_ENGINE) down
	@echo "$(GREEN)✓ Остановлено$(NC)"

logs: ## Показать логи docker-compose
	@$(COMPOSE_ENGINE) logs -f

shell: ## Открыть shell в контейнере приложения
	@$(COMPOSE_ENGINE) exec app /bin/sh

db-shell: ## Открыть shell MariaDB
	@$(COMPOSE_ENGINE) exec mariadb mariadb -u finforme -pfinforme finforme

db-only: ## Запустить только MariaDB (для локальной разработки)
	@echo "$(GREEN)Запуск MariaDB...$(NC)"
	@$(COMPOSE_ENGINE) up -d mariadb
	@echo "$(GREEN)Ожидание готовности MariaDB...$(NC)"
	@sleep 3
	@echo "$(GREEN)✓ MariaDB запущена на localhost:3306$(NC)"

dev: db-only run ## Запустить MariaDB и приложение локально

# Telegram Bot команды
bot-build: ## Собрать Telegram бот
	@echo "$(GREEN)Сборка Telegram бота...$(NC)"
	@mkdir -p $(BUILD_DIR)
	@CGO_ENABLED=0 go build -o $(BUILD_DIR)/$(BOT_BINARY_NAME) $(BOT_MAIN_PATH)
	@echo "$(GREEN)✓ Сборка завершена: $(BUILD_DIR)/$(BOT_BINARY_NAME)$(NC)"

bot-run: ## Запустить Telegram бот (нужен токен; котировки через HTTPS API)
	@echo "$(GREEN)Запуск Telegram бота...$(NC)"
	@if [ -z "$$TELEGRAM_BOT_TOKEN" ]; then \
		echo "$(GREEN)⚠️  TELEGRAM_BOT_TOKEN не задан. Установите переменную окружения:$(NC)"; \
		echo "export TELEGRAM_BOT_TOKEN=ваш_токен"; \
		exit 1; \
	fi
	@go run $(BOT_MAIN_PATH)

bot-dev: bot-run ## Запустить бота локально с котировками через API

rebuild: ## Пересобрать и перезапустить контейнеры
	@echo "$(GREEN)Пересборка контейнеров...$(NC)"
	@$(COMPOSE_ENGINE) up -d --build
	@echo "$(GREEN)✓ Контейнеры пересобраны$(NC)"

# Container команды (поддержка docker и podman)
CONTAINER_ENGINE ?= $(shell command -v podman 2>/dev/null || echo docker)
IMAGE_NAME=finforme
IMAGE_TAG=latest
CONTAINER_NAME=finforme-app

docker: ## Собрать образ (podman/docker)
	@echo "$(GREEN)Сборка образа с $(CONTAINER_ENGINE)...$(NC)"
	@$(CONTAINER_ENGINE) build -t $(IMAGE_NAME):$(IMAGE_TAG) .
	@echo "$(GREEN)✓ Образ собран: $(IMAGE_NAME):$(IMAGE_TAG)$(NC)"

docker-run: up ## Алиас для up
docker-stop: down ## Алиас для down

# Деплой на сервер
DEPLOY_HOST ?= firej@firej.org
BOT_DEPLOY_HOST ?= firej@45.144.233.69

deploy: ## Задеплоить на сервер finfor.me
	@DEPLOY_HOST="$(DEPLOY_HOST)" bash scripts/deploy-prod.sh

deploy-bot: ## Задеплоить Telegram-бот на отдельный сервер в Германии
	@BOT_DEPLOY_HOST="$(BOT_DEPLOY_HOST)" bash scripts/deploy-bot.sh

deploy-all: deploy ## Задеплоить сайт, затем бот в Германии
	@$(MAKE) deploy-bot
