# Finforme - Personal Finance Manager

Веб-приложение для управления личными финансами с поддержкой двойной записи (double-entry bookkeeping).

## Технологии

- **Backend**: Go 1.25+
- **Frontend**: HTML + htmx + Chart.js для динамических обновлений
- **База данных**: MariaDB
- **Стиль**: Tailwind CSS
- **Контейнеризация**: Docker / Podman

## Возможности

- ✅ Управление счетами с иерархической структурой
- ✅ Транзакции с двойной записью (дебет/кредит)
- ✅ Поддержка нескольких валют
- ✅ Теги для категоризации транзакций
- ✅ Аутентификация пользователей
- ✅ Импорт данных из GnuCash SQLite
- ✅ Динамический интерфейс с htmx (без перезагрузки страниц)
- ✅ Курсы валют USD/RUB и EUR/RUB с графиками (данные ЦБ РФ)

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
│   ├── server/                # Точка входа основного приложения
│   ├── import-rates/          # Скрипт ежедневного импорта курсов валют
│   └── import-rates-history/  # Скрипт однократного импорта исторических курсов
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
- `currency_rates` - исторические курсы валют (ЦБ РФ)

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

## API-токены

Программный доступ к `/api/v1/…` и MCP-серверу работает по токенам.

1. Откройте **Настройки → API-токены** и создайте токен (показывается один раз,
   в БД хранится только SHA-256 хеш).
2. Передавайте токен в заголовке `Authorization: Bearer <токен>` — работает
   для всех эндпоинтов `/api/v1` наравне с cookie-сессией.

```bash
# Список счетов
curl https://finfor.me/api/v1/finance/accounts/get \
  -H "Authorization: Bearer finforme_XXXX"

# Транзакции с фильтрами: account_id, from, to, tag, search, limit, offset
curl "https://finfor.me/api/v1/finance/transactions/get?from=2026-07-01&tag=продукты" \
  -H "Authorization: Bearer finforme_XXXX"
```

## MCP-сервер (доступ из Claude)

Приложение отдаёт MCP-сервер (Streamable HTTP, stateless) на `/mcp`.
Авторизация — тем же Bearer-токеном. Это позволяет Claude просматривать счета,
добавлять транзакции и строить отчёты по вашим данным.

### Подключение

**Claude Code (CLI):**

```bash
claude mcp add --transport http finforme https://finfor.me/mcp \
  --header "Authorization: Bearer finforme_XXXX"
```

**Claude Desktop** — Settings → Connectors → Add custom connector,
URL: `https://finfor.me/mcp`, заголовок `Authorization: Bearer finforme_XXXX`.

**Локальная разработка** — тот же способ, но URL `http://localhost:8080/mcp`.

Проверить подключение можно запросом: «покажи мои счета в finforme».

### Инструменты

| Инструмент | Описание |
|------------|----------|
| `list_accounts` | Счета с балансами и валютой |
| `list_transactions` | Транзакции с фильтрами (счёт, даты, тег, поиск, пагинация) |
| `get_transaction` | Одна транзакция со сплитами (дебет/кредит) |
| `create_transaction` | Создать транзакцию: `from_account_id` → `to_account_id`, суммы в валюте счёта, поддержка кросс-валютных (`amount_to`) |
| `update_transaction` | Обновить транзакцию по id |
| `delete_transaction` | Удалить транзакцию (необратимо) |
| `get_report` | Доходы/расходы по категориям за период |
| `get_currency_rates` | Актуальные курсы валют с дневным изменением |

Все инструменты работают строго в рамках пользователя, которому принадлежит
токен. Отозвать доступ можно удалением токена в настройках.

## Курсы валют

Раздел `/currency/` показывает курсы USD/RUB и EUR/RUB с графиками за последние 14 дней.
Данные хранятся в таблице `currency_rates` и обновляются отдельными скриптами.

### Первоначальный импорт исторических данных

После деплоя нужно однократно загрузить исторические данные. Скрипт запускается
**внутри контейнера** `app`:

```bash
# Импорт за последние 3 года (по умолчанию)
docker exec finforme-app-1 ./import-rates-history

# Или за конкретный период
docker exec finforme-app-1 ./import-rates-history -from 2020-01-01 -to 2026-04-04
```

### Ежедневное обновление курсов (cron)

Скрипт `import-rates` запускается каждые 15 минут с 10:00 до 16:00 МСК (07:00–13:00 UTC).
Добавьте задачу в cron на сервере (`crontab -e`):

```cron
# Обновление курсов валют каждые 15 минут с 10:00 до 16:00 МСК (07:00-13:00 UTC)
*/15 7-13 * * * docker exec finforme-app-1 ./import-rates >> /var/log/import-rates.log 2>&1
```

> **Примечание:** ЦБ РФ публикует курсы в рабочие дни. В выходные и праздники
> скрипт завершится успешно, но новых данных не добавит (курс не меняется).
> Повторные запуски безопасны — `ON DUPLICATE KEY UPDATE` не создаёт дублей.

### Таблица `currency_rates`

| Поле | Тип | Описание |
|------|-----|----------|
| `code` | VARCHAR(20) | Код пары, например `USD/RUB` |
| `name` | VARCHAR(255) | Название валюты |
| `rate` | DECIMAL(18,6) | Курс к рублю |
| `source` | VARCHAR(50) | Источник: `cbr` |
| `rate_date` | DATE | Дата курса |

Первичный ключ: `(code, source, rate_date)` — позволяет хранить курс одной валюты
из разных источников одновременно.

## Деплой

```bash
# Деплой на сервер finfor.me
make deploy
```

## Лицензия

MIT

## Автор

Evgeny Bogdanov
