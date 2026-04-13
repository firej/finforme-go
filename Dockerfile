# Этап сборки
FROM golang:1.25-bookworm AS builder

WORKDIR /app

# Копируем файлы зависимостей
COPY go.mod go.sum ./

# Загружаем зависимости
RUN go mod download

# Копируем исходный код
COPY . .

# Собираем основное приложение и скрипты импорта
RUN CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-w -s' -o finforme ./cmd/server/main.go
RUN CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-w -s' -o import-rates ./cmd/import-rates/
RUN CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-w -s' -o import-rates-history ./cmd/import-rates-history/
RUN CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-w -s' -o telegram-bot ./cmd/telegram-bot/

# Финальный образ
FROM debian:bookworm-slim

# Установка необходимых runtime зависимостей
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    tzdata \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Копируем бинарники из этапа сборки
COPY --from=builder /app/finforme .
COPY --from=builder /app/import-rates .
COPY --from=builder /app/import-rates-history .
COPY --from=builder /app/telegram-bot .

# Копируем статические файлы и шаблоны
COPY static/ ./static/
COPY templates/ ./templates/

# Переменные окружения по умолчанию
ENV PORT=8080
ENV DATABASE_DSN=finforme:finforme@tcp(mariadb:3306)/finforme?parseTime=true&charset=utf8mb4
ENV SESSION_SECRET=change-me-in-production
ENV SECURE_COOKIE=false

# Открываем порт
EXPOSE 8080

# Запускаем приложение
CMD ["./finforme"]
