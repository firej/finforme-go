# Этап сборки
FROM golang:1.21-bookworm AS builder

WORKDIR /app

# Копируем файлы зависимостей
COPY go.mod go.sum ./

# Загружаем зависимости
RUN go mod download

# Копируем исходный код
COPY . .

# Собираем приложение (CGO отключен, так как не используем SQLite)
RUN CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-w -s' -o finforme ./cmd/server/main.go

# Финальный образ
FROM debian:bookworm-slim

# Установка необходимых runtime зависимостей
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    tzdata \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Копируем бинарник из этапа сборки
COPY --from=builder /app/finforme .

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
