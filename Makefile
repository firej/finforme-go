.PHONY: build run clean test help container-build container-run container-stop container-logs container-shell docker docker-run docker-stop deploy

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

# Container команды (поддержка docker и podman)
CONTAINER_ENGINE ?= $(shell command -v podman 2>/dev/null || echo docker)
IMAGE_NAME=finforme
IMAGE_TAG=latest
CONTAINER_NAME=finforme-app

container-build: ## Собрать образ (podman/docker)
	@echo "$(GREEN)Сборка образа с $(CONTAINER_ENGINE)...$(NC)"
	@$(CONTAINER_ENGINE) build -t $(IMAGE_NAME):$(IMAGE_TAG) .
	@echo "$(GREEN)✓ Образ собран: $(IMAGE_NAME):$(IMAGE_TAG)$(NC)"

container-run: ## Запустить контейнер
	@echo "$(GREEN)Запуск контейнера...$(NC)"
	@$(CONTAINER_ENGINE) run -d --name $(CONTAINER_NAME) \
		-p 8080:8080 \
		-v finforme-data:/app/data \
		-e SESSION_SECRET=$${SESSION_SECRET:-super-secret-key} \
		$(IMAGE_NAME):$(IMAGE_TAG)
	@echo "$(GREEN)✓ Контейнер запущен: $(CONTAINER_NAME)$(NC)"
	@echo "$(GREEN)  Приложение доступно на http://localhost:8080$(NC)"

container-stop: ## Остановить и удалить контейнер
	@echo "$(GREEN)Остановка контейнера...$(NC)"
	@$(CONTAINER_ENGINE) stop $(CONTAINER_NAME) 2>/dev/null || true
	@$(CONTAINER_ENGINE) rm $(CONTAINER_NAME) 2>/dev/null || true
	@echo "$(GREEN)✓ Контейнер остановлен$(NC)"

container-logs: ## Показать логи контейнера
	@$(CONTAINER_ENGINE) logs -f $(CONTAINER_NAME)

container-shell: ## Открыть shell в контейнере
	@$(CONTAINER_ENGINE) exec -it $(CONTAINER_NAME) /bin/sh

# Алиасы для совместимости
docker: container-build
docker-run: container-run
docker-stop: container-stop

# Деплой на сервер
DEPLOY_HOST=firej@finfor.me
DEPLOY_PATH=/opt/finforme

deploy: ## Задеплоить на сервер finfor.me
	@echo "$(GREEN)Синхронизация исходников...$(NC)"
	@rsync -avz --exclude='.git' --exclude='bin' --exclude='*.db' --exclude='*.gnucash' --exclude='scripts' --exclude='.idea' --exclude='.vscode' . $(DEPLOY_HOST):$(DEPLOY_PATH)/
	@echo "$(GREEN)Сборка образа на сервере...$(NC)"
	@ssh $(DEPLOY_HOST) 'cd $(DEPLOY_PATH) && docker build -t finforme:latest .'
	@echo "$(GREEN)Перезапуск контейнера...$(NC)"
	@ssh $(DEPLOY_HOST) 'cd /opt/traefik && docker compose up -d --force-recreate finforme'
	@echo "$(GREEN)✓ Деплой завершён: https://finfor.me$(NC)"
