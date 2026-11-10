package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func main() {
	check := flag.Bool("check", false, "Проверить API котировок и авторизацию Telegram без получения или отправки сообщений")
	flag.Parse()

	// Получаем токен бота из переменной окружения
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN не задан")
	}
	// Telegram includes the token in request URLs, including network errors.
	log.SetOutput(tokenRedactingWriter{writer: os.Stderr, token: token})
	if err := tgbotapi.SetLogger(log.Default()); err != nil {
		log.Fatal(err)
	}
	rates, err := newRatesClient(os.Getenv("CURRENCY_RATES_URL"))
	if err != nil {
		log.Fatal(err)
	}

	// Создаем нового бота
	client := &http.Client{Timeout: 15 * time.Second}
	bot, err := tgbotapi.NewBotAPIWithClient(token, tgbotapi.APIEndpoint, client)
	if err != nil {
		log.Fatalf("Ошибка при создании бота: %s", err)
	}
	// Allow the 60-second long poll to finish, but bound stalled requests.
	client.Timeout = 75 * time.Second

	log.Printf("Авторизован как @%s (id=%d)", bot.Self.UserName, bot.Self.ID)
	if *check {
		quotes, err := rates.latest(context.Background())
		if err != nil {
			log.Fatalf("Ошибка проверки API котировок: %v", err)
		}
		if len(quotes) != 3 {
			log.Fatal("В API отсутствуют обязательные котировки BTC/USD, USD/RUB или EUR/RUB")
		}
		log.Printf("API котировок доступен: %s", formatRates(quotes))
		return
	}

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
	log.Println("Получение обновлений Telegram запущено")

	// Обрабатываем входящие обновления
	for update := range updates {
		// Каждое обновление обрабатываем с защитой от паники,
		// чтобы одно плохое сообщение не убило весь цикл
		handleUpdate(bot, rates, update)
	}
}

type tokenRedactingWriter struct {
	writer io.Writer
	token  string
}

func (w tokenRedactingWriter) Write(p []byte) (int, error) {
	redacted := p
	if w.token != "" {
		redacted = bytes.ReplaceAll(p, []byte(w.token), []byte("[REDACTED_TOKEN]"))
	}
	n, err := w.writer.Write(redacted)
	if err != nil {
		return 0, err
	}
	if n != len(redacted) {
		return 0, io.ErrShortWrite
	}
	return len(p), nil
}

// handleUpdate обрабатывает одно обновление от Telegram.
// Паника внутри перехватывается, чтобы бот продолжал работу.
func handleUpdate(bot *tgbotapi.BotAPI, ratesClient *ratesClient, update tgbotapi.Update) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("ПАНИКА при обработке обновления update_id=%d: %v", update.UpdateID, r)
		}
	}()

	if update.Message == nil || update.Message.Chat == nil {
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
		rates, err := ratesClient.latest(context.Background())
		if err != nil {
			log.Printf("Ошибка получения курсов: %v", err)
			reply := tgbotapi.NewMessage(msg.Chat.ID, "Не удалось получить курсы валют")
			if _, sendErr := bot.Send(reply); sendErr != nil {
				log.Printf("Ошибка отправки сообщения об ошибке: %v", sendErr)
			}
		} else {
			reply := tgbotapi.NewMessage(msg.Chat.ID, formatRates(rates))
			if _, err := bot.Send(reply); err != nil {
				log.Printf("Ошибка отправки курсов: %v", err)
			}
		}

	default:
		// Неизвестная команда — молча игнорируем
		log.Printf("Неизвестная команда: /%s от %s", command, senderName)
	}
}

func formatRates(rates map[string]float64) string {
	var parts []string
	if btc := rates["BTC/USD"]; btc > 0 {
		parts = append(parts, fmt.Sprintf("₿ $%.0f", btc))
	}
	if usd := rates["USD/RUB"]; usd > 0 {
		parts = append(parts, fmt.Sprintf("💵 %.2f₽", usd))
	}
	if eur := rates["EUR/RUB"]; eur > 0 {
		parts = append(parts, fmt.Sprintf("💶 %.2f₽", eur))
	}
	if len(parts) == 0 {
		return "Нет данных о курсах валют"
	}
	return "Котировки: " + strings.Join(parts, "  ")
}
