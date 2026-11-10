# Telegram-бот Finforme

`@finforme_bot` показывает BTC/USD (Coinbase), USD/RUB и EUR/RUB (ЦБ РФ).
Команды: `/start`, `/help`, `/rate`, `/rates`.

Бот получает котировки через публичный HTTPS API сайта. Подключение к MariaDB
и финансовый API-токен ему не нужны. Для каждой пары выбирается явный источник;
другие источники не заменяют котировки ЦБ или Coinbase.

## API котировок

`GET https://finfor.me/api/currency/rates` возвращает JSON:

```json
{
  "rates": [
    {
      "code": "USD/RUB",
      "name": "Доллар США",
      "rate": 82.5486,
      "source": "cbr",
      "rate_date": "2026-11-11"
    }
  ]
}
```

Это последняя сохранённая публикация для каждой пары и источника, включая
объявленный ЦБ курс на следующий день. `rate_date` сохраняет дату источника.
Пустая база возвращает `{"rates":[]}`; недоступность базы — HTTP 503.
В ответе нет счетов, операций или других пользовательских данных.
Кэширование ответа разрешено на 30 секунд.

Запрос бота ограничен пятью секундами и ответом в 1 MiB. При ошибке API бот
сообщает, что котировки получить не удалось; локальная база или устаревший
кэш вместо ответа не используются.

## Сервер в Германии

Бот работает на `firej@45.144.233.69` в отдельном Compose-проекте
`finforme-bot`, каталог `/opt/finforme-bot`. Он обращается к Telegram напрямую.
Сайт, база и импорт курсов остаются на `firej.org`.

Настройки `/opt/finforme-bot/.env` (владелец root, права `0600`):

```dotenv
TELEGRAM_BOT_TOKEN=<существующий токен бота>
CURRENCY_RATES_URL=https://finfor.me/api/currency/rates
```

Токен устанавливается отдельно и не попадает в исходники, образ или архив
деплоя. Копировать `.env` веб-сервера на сервер бота не нужно.

```bash
make deploy-bot                    # только бот на BOT_DEPLOY_HOST
make deploy-all                    # сначала сайт/API, затем бот
make deploy-bot BOT_DEPLOY_HOST=firej@45.144.233.69
```

`scripts/deploy-bot.sh` собирает Linux amd64-бинарник, загружает минимальный
образ и проверяет API котировок и Telegram перед запуском. `BOT_PREPARE_ONLY=1`
подготавливает и проверяет сервис без запуска polling — для переноса сначала
остановите старый экземпляр, затем запускайте новый. Одновременный polling
двух экземпляров одного бота недопустим.

Диагностика на сервере:

```bash
cd /opt/finforme-bot
sudo docker compose --env-file .env ps
sudo docker compose --env-file .env run --rm --no-deps finforme-bot -check
sudo docker logs --tail 20 finforme-bot
```

`-check` проверяет авторизацию Telegram и наличие трёх нужных котировок,
не забирая обновления и не отправляя сообщения. Эта же команда используется
в Docker healthcheck. Контейнер запускается от непривилегированного
пользователя, с файловой системой только для чтения и `restart: unless-stopped`.
Таймаут авторизации Telegram — 15 секунд, запросов polling — 75 секунд.
Токен скрывается в логах приложения и Telegram SDK, включая сетевые ошибки.

## Локальная разработка

Задайте `TELEGRAM_BOT_TOKEN` и выполните `make bot-run` или
`go run ./cmd/telegram-bot`. По умолчанию используется публичный API `finfor.me`.
Для локального сайта задайте
`CURRENCY_RATES_URL=http://localhost:8080/api/currency/rates`.

В локальном Compose бот включается явно: `docker compose --profile bot up -d`.
Он обращается к `http://app:8080/api/currency/rates`.
Не запускайте локальный polling с production-токеном одновременно с сервером.

Проверки: `go test -race ./cmd/telegram-bot`; тестовые ответы Telegram и API
обслуживаются локально, настоящие сообщения не отправляются.
