# Finforme - Personal Finance Manager

Веб-приложение для управления личными финансами с поддержкой двойной записи (double-entry bookkeeping).

## Технологии

- **Backend**: Go 1.21+
- **Frontend**: HTML + htmx для динамических обновлений
- **База данных**: MariaDB
- **Стиль**: Bootstrap 5
- **Контейнеризация**: Docker / Podman

## Возможности

- ✅ Управление счетами с иерархической структурой
- ✅ Транзакции с двойной записью (дебет/кредит)
- ✅ Поддержка нескольких валют
- ✅ Теги для категоризации транзакций
- ✅ Аутентификация пользователей
- ✅ Импорт данных из GnuCash SQLite
- ✅ Динамический интерфейс с htmx (без перезагрузки страниц)

## Быстрый старт

### Запуск через Docker Compose (рекомендуется)

```bash
# Запустить приложение с MariaDB
make up

# Или напрямую
docker-compose up -d
```

Приложение будет доступно по адресу: http://localhost:8000

### Локальная разработка

```bash
# Запустить только MariaDB, приложение запустить локально
make dev

# Или по шагам:
make db-only          # Запустить MariaDB
go run cmd/server/main.go  # Запустить приложение
```

### Полезные команды

```bash
make up        # Запустить всё через docker-compose
make down      # Остановить
make logs      # Посмотреть логи
make db-shell  # Открыть консоль MariaDB
make rebuild   # Пересобрать и перезапустить контейнеры
```

## Конфигурация

Приложение настраивается через переменные окружения:

| Переменная | Описание | По умолчанию |
|------------|----------|--------------|
| `PORT` | Порт приложения | `8000` |
| `DATABASE_DSN` | DSN для подключения к MariaDB | `finforme:finforme@tcp(localhost:3306)/finforme?parseTime=true&charset=utf8mb4` |
| `SESSION_SECRET` | Секрет для сессий | `change-me-in-production` |
| `SECURE_COOKIE` | Использовать secure cookies | `false` |

## Структура проекта

```
.
├── cmd/
│   └── server/          # Точка входа приложения
├── internal/
│   ├── config/          # Конфигурация
│   ├── database/        # Инициализация БД
│   ├── handlers/        # HTTP handlers
│   └── models/          # Модели данных
├── static/              # Статические файлы (CSS, JS)
├── templates/           # HTML шаблоны
├── docker-compose.yml   # Docker Compose конфигурация
├── Dockerfile           # Сборка образа
└── Makefile             # Команды для разработки
```

## База данных

Приложение использует MariaDB с автоматической инициализацией схемы при первом запуске.

### Основные таблицы

- `users` - пользователи системы
- `commodities` - валюты и товары
- `accounts` - счета пользователей
- `transactions` - финансовые транзакции
- `splits` - записи дебета/кредита для транзакций

## Импорт данных

### Из GnuCash

1. Экспортируйте данные из GnuCash в формат SQLite
2. Перейдите в раздел "Настройки" в приложении
3. Загрузите файл SQLite через форму импорта
4. Данные будут автоматически импортированы с сохранением структуры счетов и транзакций

## Разработка

### Требования

- Go 1.21 или выше
- Docker / Podman (для MariaDB)

### Сборка

```bash
# Собрать бинарник
make build

# Собрать Docker образ
make docker
```

## API Endpoints

### Аутентификация
- `GET /accounts/login/` - страница входа
- `POST /accounts/login/` - вход в систему
- `GET /accounts/logout/` - выход из системы
- `GET /accounts/register/` - страница регистрации
- `POST /accounts/register/` - регистрация нового пользователя

### Финансы
- `GET /finance/` - главная страница (список счетов)
- `GET /finance/account/{id}` - просмотр транзакций счета
- `GET /finance/account/{id}/edit` - редактирование счета
- `GET /finance/transaction/{account_id}/{tx_id}` - просмотр транзакции
- `GET /finance/settings` - настройки и импорт данных

### API
- `POST /api/v1/finance/account/save` - сохранение счета
- `POST /api/v1/finance/transaction/save` - сохранение транзакции
- `POST /api/v1/finance/welcome/import` - импорт из GnuCash

## Деплой

```bash
# Деплой на сервер finfor.me
make deploy
```

## Лицензия

MIT

## Автор

Evgeny Bogdanov
