# Telegram Bot для курсов валют

Этот Telegram бот показывает курсы валют из базы данных проекта informe.

## Функциональность

Бот поддерживает следующие команды:

- `/start` - начать работу с ботом
- `/help` - показать справку
- `/rate` - показать последние курсы валют

## Установка и запуск

### Требования

- Docker
- Docker Compose
- Токен Telegram бота (получить у [@BotFather](https://t.me/botfather))

### Настройка

1. Создайте файл `.env` в корне проекта и добавьте токен бота:

```env
TELEGRAM_BOT_TOKEN=ваш_токен_здесь
```

2. Убедитесь, что база данных содержит курсы валют в таблице `currency_rates`.

### Запуск

Запустите все сервисы с помощью Docker Compose:

```bash
docker-compose up -d
```

Бот будет автоматически подключен к базе данных и начнет обрабатывать команды.

## Структура проекта

```
cmd/telegram-bot/
├── Dockerfile       # Dockerfile для сборки контейнера бота
└── main.go          # Основной файл бота
```

## Разработка

### Локальный запуск

Для локального запуска бота без Docker:

1. Установите зависимости:
```bash
go mod download
```

2. Установите переменные окружения:
```bash
export DATABASE_DSN="finforme:finforme@tcp(localhost:3306)/finforme?parseTime=true&charset=utf8mb4"
export TELEGRAM_BOT_TOKEN="ваш_токен_здесь"
```

3. Запустите бота:
```bash
go run cmd/telegram-bot/main.go
```

## Архитектура

Бот подключается к той же базе данных, что и основное приложение, и читает курсы валют из таблицы `currency_rates`. Это позволяет избежать дублирования данных и обеспечивает согласованность информации между веб-интерфейсом и ботом.

### Запрос к базе данных

Бот использует следующий SQL-запрос для получения последних курсов:

```sql
SELECT code, rate
FROM currency_rates
WHERE (code, rate_date) IN (
    SELECT code, MAX(rate_date)
    FROM currency_rates
    GROUP BY code
)
ORDER BY code;
```

Этот запрос возвращает последние курсы для каждой валюты.
