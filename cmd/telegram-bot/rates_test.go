package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const testRatesJSON = `{"rates":[
	{"code":"USD/RUB","source":"other","rate":999,"rate_date":"2026-11-11"},
	{"code":"USD/RUB","source":"cbr","rate":82.5486,"rate_date":"2026-11-11"},
	{"code":"EUR/RUB","source":"cbr","rate":92.3966,"rate_date":"2026-11-11"},
	{"code":"BTC/USD","source":"coinbase","rate":73848.71,"rate_date":"2026-11-10"},
	{"code":"NVDA/USD","source":"yahoo","rate":153.21,"rate_date":"2025-02-14","quoted_at":"2025-02-14T21:00:00Z"},
	{"code":"NBIS/USD","source":"yahoo","rate":86.94,"rate_date":"2025-02-14","quoted_at":"2025-02-14T20:30:00Z"},
	{"code":"YDEX/RUB","source":"moex","rate":3282,"rate_date":"2025-02-13"}
]}`

func TestRatesClientHTTPSAndSources(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/currency/rates" || r.Header.Get("Accept") != "application/json" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(testRatesJSON))
	}))
	defer server.Close()
	client, err := newRatesClient(server.URL + "/api/currency/rates")
	if err != nil {
		t.Fatal(err)
	}
	client.client = server.Client()
	rates, err := client.latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := formatRates(rates); got != "Котировки: ₿ $73849  💵 82.55₽  💶 92.40₽" {
		t.Fatalf("unexpected rates reply: %s", got)
	}
}

func TestRatesClientRejectsUnavailableOrInvalidData(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"upstream failure", 503, `unavailable`},
		{"login page", 200, `<html>login</html>`},
		{"missing data", 200, `{}`},
		{"null rates", 200, `{"rates":null}`},
		{"negative rate", 200, `{"rates":[{"code":"USD/RUB","source":"cbr","rate":-1,"rate_date":"2026-11-11"}]}`},
		{"invalid date", 200, `{"rates":[{"code":"USD/RUB","source":"cbr","rate":82,"rate_date":"11.11.2026"}]}`},
		{"invalid stock timestamp", 200, `{"rates":[{"code":"NVDA/USD","source":"yahoo","rate":153,"rate_date":"2025-02-14","quoted_at":"yesterday"}]}`},
		{"duplicate source", 200, `{"rates":[{"code":"USD/RUB","source":"cbr","rate":82,"rate_date":"2026-11-11"},{"code":"USD/RUB","source":"cbr","rate":81,"rate_date":"2026-11-10"}]}`},
		{"oversized response", 200, strings.Repeat(" ", (1<<20)+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client, err := newRatesClient(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.latest(context.Background()); err == nil {
				t.Fatal("invalid upstream response accepted")
			}
		})
	}
}

func TestStockRatesSourcesAndTimestamps(t *testing.T) {
	const body = `{"rates":[
		{"code":"NVDA/USD","source":"other","rate":999,"rate_date":"2025-02-14"},
		{"code":"NVDA/USD","source":"yahoo","rate":153.21,"rate_date":"2025-02-14","quoted_at":"2025-02-14T21:00:00Z"},
		{"code":"NBIS/USD","source":"yahoo","rate":86.94,"rate_date":"2025-02-14","quoted_at":"2025-02-14T20:30:00Z"},
		{"code":"YDEX/RUB","source":"moex","rate":3282,"rate_date":"2025-02-13"}
	]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
	defer server.Close()
	client, _ := newRatesClient(server.URL)
	rates, err := client.latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := "NVDA 153.21 $ · 15.02.2025 00:00 МСК · Yahoo Finance\nNBIS 86.94 $ · 14.02.2025 23:30 МСК · Yahoo Finance\nYDEX 3282.00 ₽ · 13.02.2025 · MOEX · задержка 15 мин"
	var lines []string
	for _, stock := range stockRates {
		lines = append(lines, formatStockRate(rates, stock))
	}
	if got := strings.Join(lines, "\n"); got != want {
		t.Fatalf("got=%q want=%q", got, want)
	}
}

func TestRatesClientEmptyAndCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"rates":[]}`))
	}))
	defer server.Close()
	client, _ := newRatesClient(server.URL)
	rates, err := client.latest(context.Background())
	if err != nil || formatRates(rates) != "Нет данных о курсах валют" {
		t.Fatalf("empty rates: %v %v", rates, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.latest(ctx); err == nil {
		t.Fatal("cancelled request was not cancelled")
	}
}

func TestRateCommandsUseAPIAndReportOutage(t *testing.T) {
	for _, tc := range []struct{ command, args, want string }{
		{"rate", "", "Котировки: ₿ $73849  💵 82.55₽  💶 92.40₽"},
		{"rates", "", "Котировки: ₿ $73849  💵 82.55₽  💶 92.40₽"},
		{"rate", "NVDA", "NVDA 153.21 $ · 15.02.2025 00:00 МСК · Yahoo Finance"},
		{"rate@test_bot", " nvda ", "NVDA 153.21 $ · 15.02.2025 00:00 МСК · Yahoo Finance"},
		{"rate", "NBIS/USD", "NBIS 86.94 $ · 14.02.2025 23:30 МСК · Yahoo Finance"},
		{"rate", "YDEX", "YDEX 3282.00 ₽ · 13.02.2025 · MOEX · задержка 15 мин"},
	} {
		command := tc.command
		for _, available := range []bool{true, false} {
			t.Run(command+tc.args+map[bool]string{true: "/ok", false: "/outage"}[available], func(t *testing.T) {
				ratesServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if !available {
						http.Error(w, "unavailable", 503)
						return
					}
					w.Write([]byte(testRatesJSON))
				}))
				defer ratesServer.Close()
				client, _ := newRatesClient(ratesServer.URL)
				replies := make(chan string, 1)
				telegram := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if strings.HasSuffix(r.URL.Path, "/getMe") {
						w.Write([]byte(`{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"Test","username":"test_bot"}}`))
						return
					}
					if !strings.HasSuffix(r.URL.Path, "/sendMessage") {
						t.Errorf("unexpected Telegram method: %s", r.URL.Path)
					}
					r.ParseForm()
					replies <- r.Form.Get("text")
					json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": 1}})
				}))
				defer telegram.Close()
				bot, err := tgbotapi.NewBotAPIWithClient("test-token", telegram.URL+"/bot%s/%s", telegram.Client())
				if err != nil {
					t.Fatal(err)
				}
				handleUpdate(bot, client, tgbotapi.Update{Message: &tgbotapi.Message{
					Chat: &tgbotapi.Chat{ID: 1, Type: "private"}, Text: "/" + command + " " + tc.args,
					Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: len(command) + 1}},
				}})
				want := "Не удалось получить курсы валют"
				if available {
					want = tc.want
				}
				select {
				case got := <-replies:
					if got != want {
						t.Fatalf("reply=%q, want=%q", got, want)
					}
				default:
					t.Fatal("command did not produce a reply")
				}
			})
		}
	}
}

func TestStockRateMissingAndInvalidTicker(t *testing.T) {
	stock, ok := findStockRate("NVDA")
	if !ok {
		t.Fatal("NVDA not recognized")
	}
	if got := formatStockRate(nil, stock); got != "Нет данных о котировке NVDA" {
		t.Fatalf("missing quote: %s", got)
	}
	for _, args := range []string{"AAPL", "NVDA NBIS", "NVDA/RUB"} {
		if _, ok := findStockRate(args); ok {
			t.Errorf("accepted unsupported request %q", args)
		}
	}
}
