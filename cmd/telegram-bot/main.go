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
	log.Printf("Авторизован как @%s (id=%d)", bot.Self.UserName, bot.Self.ID)

	// Регистрируем команды бота в Telegram — они появятся в меню "/"
	// и будут корректно доставляться в групповых чатах
	commands := tgbotapi.NewSetMyCommands(
		tgbotapi.BotCommand{Command: "rate", Description: "Показать последние курсы валют"},
		tgbotapi.BotCommand{Command: "rates", Description: "Показать последние курсы валют"},
		tgbotapi.BotCommand{Command: "help", Description: "Показать справку"},
		tgbotapi.BotCommand{Command: "start", Description: "Начать работу с ботом"},
	)
	if _, err := bot.Request(commands); err != nil {
		log.Printf("Ошибка регистрации команд: %v", err)
	} else {
		log.Printf("Команды бота зарегистрированы")
	}

	// Настраиваем получение обновлений
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	// Обрабатываем входящие обновления
	for update := range updates {
		// Каждое обновление обрабатываем с защитой от паники,
		// чтобы одно плохое сообщение не убило весь цикл
		handleUpdate(bot, db, update)
	}
}

// handleUpdate обрабатывает одно обновление от Telegram.
// Паника внутри перехватывается, чтобы бот продолжал работу.
func handleUpdate(bot *tgbotapi.BotAPI, db *sql.DB, update tgbotapi.Update) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("ПАНИКА при обработке обновления update_id=%d: %v", update.UpdateID, r)
		}
	}()

	if update.Message == nil {
		// Игнорируем не-сообщения (callback, inline и т.д.)
		return
	}

	msg := update.Message

	// Защита от системных сообщений без отправителя
	senderName := "<system>"
	if msg.From != nil {
		senderName = "@" + msg.From.UserName
	}

	// Логируем входящее сообщение с информацией о чате
	log.Printf("update_id=%d chat_id=%d chat_type=%s from=%s text=%q",
		update.UpdateID, msg.Chat.ID, msg.Chat.Type, senderName, msg.Text)

	// Обрабатываем только команды (сообщения начинающиеся с "/")
	// В групповых чатах команды приходят как /cmd@botname — msg.Command() обрезает суффикс
	if !msg.IsCommand() {
		return
	}

	// Получаем имя команды без префикса "/" и без "@botname"
	command := msg.Command()
	log.Printf("Команда: /%s от %s в чате %d (%s)", command, senderName, msg.Chat.ID, msg.Chat.Type)

	switch command {
	case "start":
		reply := tgbotapi.NewMessage(msg.Chat.ID,
			"Привет! Я бот для просмотра курсов валют.\n\n"+
				"Доступные команды:\n"+
				"/rate — показать последние курсы валют\n"+
				"/help — показать справку")
		if _, err := bot.Send(reply); err != nil {
			log.Printf("Ошибка отправки ответа на /start: %v", err)
		}

	case "help":
		reply := tgbotapi.NewMessage(msg.Chat.ID,
			"Доступные команды:\n"+
				"/start — начать работу с ботом\n"+
				"/rate — показать последние курсы валют\n"+
				"/help — показать эту справку")
		if _, err := bot.Send(reply); err != nil {
			log.Printf("Ошибка отправки ответа на /help: %v", err)
		}

	case "rate", "rates":
		rates, err := getLatestCurrenciesRate(db)
		if err != nil {
			log.Printf("Ошибка получения курсов: %v", err)
			reply := tgbotapi.NewMessage(msg.Chat.ID, "Не удалось получить курсы валют")
			if _, sendErr := bot.Send(reply); sendErr != nil {
				log.Printf("Ошибка отправки сообщения об ошибке: %v", sendErr)
			}
		} else {
			var parts []string
			if btc, ok := rates["BTC/USD"]; ok && btc > 0 {
				parts = append(parts, fmt.Sprintf("₿ $%.0f", btc))
			}
			if usd, ok := rates["USD/RUB"]; ok && usd > 0 {
				parts = append(parts, fmt.Sprintf("💵 %.2f₽", usd))
			}
			if eur, ok := rates["EUR/RUB"]; ok && eur > 0 {
				parts = append(parts, fmt.Sprintf("💶 %.2f₽", eur))
			}
			text := "Котировки: " + strings.Join(parts, "  ")
			if len(parts) == 0 {
				text = "Нет данных о курсах валют"
			}
			reply := tgbotapi.NewMessage(msg.Chat.ID, text)
			if _, err := bot.Send(reply); err != nil {
				log.Printf("Ошибка отправки курсов: %v", err)
			}
		}

	default:
		// Неизвестная команда — молча игнорируем
		log.Printf("Неизвестная команда: /%s от %s", command, senderName)
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
