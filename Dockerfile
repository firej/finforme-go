# Этап сборки
FROM golang:1.21-bookworm AS builder

# Установка необходимых пакетов для сборки
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc \
    libc6-dev \
    libsqlite3-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Копируем файлы зависимостей
COPY go.mod go.sum ./

# Загружаем зависимости
RUN go mod download

# Копируем исходный код
COPY . .

# Собираем приложение
RUN CGO_ENABLED=1 GOOS=linux go build -a -ldflags '-w -s' -o finforme ./cmd/server/main.go

# Финальный образ
FROM debian:bookworm-slim

# Установка необходимых runtime зависимостей
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    tzdata \
    libsqlite3-0 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Копируем бинарник из этапа сборки
COPY --from=builder /app/finforme .

# Копируем статические файлы и шаблоны
COPY static/ ./static/
COPY templates/ ./templates/

# Создаём директорию для данных
RUN mkdir -p /app/data

# Переменные окружения по умолчанию
ENV PORT=8080
ENV DATABASE_PATH=/app/data/finforme.db
ENV SESSION_SECRET=change-me-in-production
ENV SECURE_COOKIE=false

# Открываем порт
EXPOSE 8080

# Запускаем приложение
CMD ["./finforme"]
