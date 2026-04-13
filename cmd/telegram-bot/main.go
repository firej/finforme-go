package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func main() {
	// Получаем DSN базы данных из переменной окружения
	dsn := os.Getenv("DATABASE_DSN")
	if dsn == "" {
		log.Fatal("DATABASE_DSN не задан")
	}

	// Подключаемся к базе данных
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("Ошибка подключения к базе данных: %s", err)
	}
	defer db.Close()

	// Проверяем подключение
	if err := db.Ping(); err != nil {
		log.Fatalf("Ошибка проверки подключения к базе данных: %s", err)
	}

	// Получаем токен бота из переменной окружения
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN не задан")
	}

	// Создаем нового бота
	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		log.Fatalf("Ошибка при создании бота: %s", err)
	}

	// Включаем режим отладки
	bot.Debug = true
	log.Printf("Авторизован как %s", bot.Self.UserName)

	// Настраиваем получение обновлений
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	// Обрабатываем входящие обновления
	for update := range updates {
		if update.Message == nil { // Игнорируем не-сообщения
			continue
		}

		// Логируем входящее сообщение
		log.Printf("[%s] %s", update.Message.From.UserName, update.Message.Text)

		// Обрабатываем команды
		command := update.Message.Text
		switch {
		case strings.HasPrefix(command, "/start"):
			msg := tgbotapi.NewMessage(update.Message.Chat.ID,
				"Привет! Я бот для просмотра курсов валют.\n\n"+
					"Доступные команды:\n"+
					"/rate - показать последние курсы валют\n"+
					"/help - показать справку")
			bot.Send(msg)
		case strings.HasPrefix(command, "/help"):
			msg := tgbotapi.NewMessage(update.Message.Chat.ID,
				"Доступные команды:\n"+
					"/start - начать работу с ботом\n"+
					"/rate - показать последние курсы валют\n"+
					"/help - показать эту справку")
			bot.Send(msg)
		case strings.HasPrefix(command, "/rate"):
			rates, err := getLatestCurrenciesRate(db)
			if err != nil {
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Не удалось получить курсы валют")
				bot.Send(msg)
				log.Printf("Ошибка получения курсов: %v", err)
			} else {
				message := "Котировки: "
				btc := 0.0
				usd := 0.0
				eur := 0.0
				for code, rate := range rates {
					if code == "BTC" {
						btc = rate
					} else if code == "USD" {
						usd = rate
					} else if code == "EUR" {
						eur = rate
					}
				}
				message += fmt.Sprintf("₿ $%.2f  💵 %.2f₽  💶 %.2f₽", btc, usd, eur)
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, message)
				bot.Send(msg)
			}
		default:
			msg := tgbotapi.NewMessage(update.Message.Chat.ID,
				"Извините, я вас не понимаю. Попробуйте /start или /help.")
			bot.Send(msg)
		}
	}
}

// getLatestCurrenciesRate возвращает список всех последних значений курсов валют
func getLatestCurrenciesRate(db *sql.DB) (map[string]float64, error) {
	// Карта для хранения результатов
	results := make(map[string]float64)

	// SQL-запрос для получения последних курсов
	query := `
		SELECT code, rate
		FROM currency_rates
		WHERE (code, rate_date) IN (
			SELECT code, MAX(rate_date)
			FROM currency_rates
			GROUP BY code
		)
		ORDER BY code;
	`

	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Проходим по всем строкам результата
	for rows.Next() {
		var code string
		var rate float64

		// Считываем данные строки
		err := rows.Scan(&code, &rate)
		if err != nil {
			return nil, err
		}

		// Сохраняем в результат
		results[code] = rate
	}

	return results, nil
}
