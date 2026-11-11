package main

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestParseChartRequest(t *testing.T) {
	for _, tc := range []struct {
		args, code string
		days       int
	}{
		{"", "USD/RUB", 14}, {"14", "USD/RUB", 14}, {"eur", "EUR/RUB", 14},
		{"btc/usd 2w", "BTC/USD", 14}, {"USD 2н", "USD/RUB", 14},
		{"  RUB/ARS  30д ", "RUB/ARS", 30}, {"USD/ARS 365", "USD/ARS", 365},
		{"BTC 2", "BTC/USD", 2}, {"EUR 52w", "EUR/RUB", 364},
	} {
		t.Run(tc.args, func(t *testing.T) {
			req, err := parseChartRequest(tc.args)
			if err != nil || req.Instrument.Code != tc.code || req.Days != tc.days {
				t.Fatalf("parse %q = %+v, %v", tc.args, req, err)
			}
		})
	}
	for _, args := range []string{"JPY 14", "USD 0", "USD 1", "-14", "366", "BTC 53w", "BTC 999999999999999999999w", "USD two", "BTC 2 weeks", "USD 2dд"} {
		if _, err := parseChartRequest(args); err == nil {
			t.Errorf("accepted invalid arguments %q", args)
		}
	}
}

func TestHistorySelectsSourceAndCalendarWindow(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/currency/charts" || r.URL.Query().Get("days") != "15" {
			t.Errorf("unexpected history request: %s %s", r.Method, r.URL)
		}
		io.WriteString(w, `[
			{"code":"USD/RUB","source":"other","points":[{"date":"2026-11-11","rate":999}]},
			{"code":"USD/RUB","source":"cbr","points":[
				{"date":"2026-11-11","rate":82},
				{"date":"2026-10-28","rate":79},
				{"date":"2026-11-12","rate":83},
				{"date":"2026-10-29","rate":80}
			]}
		]`)
	}))
	defer server.Close()
	client, _ := newRatesClient(server.URL + "/api/currency/rates")
	client.client = server.Client()
	req, _ := parseChartRequest("")
	// Already November 11 in Moscow, still November 10 in UTC.
	data, err := client.history(context.Background(), req, time.Date(2026, 11, 10, 22, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if data.From.Format(time.DateOnly) != "2026-10-29" || data.To.Format(time.DateOnly) != "2026-11-11" || len(data.Points) != 2 {
		t.Fatalf("incorrect calendar window: %+v", data)
	}
	if data.Points[0].Rate != 80 || data.Points[1].Rate != 82 {
		t.Fatalf("wrong source, date order, or invented points: %+v", data.Points)
	}
	if !strings.Contains(data.caption(), "+2.50%") || !strings.Contains(data.caption(), "точек: 2") {
		t.Fatalf("incorrect caption: %s", data.caption())
	}
}

func TestHistoryRejectsInvalidData(t *testing.T) {
	for _, body := range []string{
		`null`, `{}`, `<html>error</html>`,
		`[{"code":"USD/RUB","source":"cbr"},{"code":"USD/RUB","source":"cbr"}]`,
		`[{"code":"USD/RUB","source":"cbr","points":[{"date":"bad","rate":80}]}]`,
		`[{"code":"USD/RUB","source":"cbr","points":[{"date":"2026-11-11","rate":0}]}]`,
		`[{"code":"USD/RUB","source":"cbr","points":[{"date":"2026-11-11","rate":-1}]}]`,
		`[{"code":"USD/RUB","source":"cbr","points":[{"date":"2026-11-11","rate":80},{"date":"2026-11-11","rate":81}]}]`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, body)
		}))
		client, _ := newRatesClient(server.URL)
		req, _ := parseChartRequest("")
		_, err := client.history(context.Background(), req, time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC))
		server.Close()
		if err == nil {
			t.Errorf("accepted invalid history: %s", body)
		}
	}
}

func TestChartCommandsUploadPhotoOrExplainError(t *testing.T) {
	for _, tc := range []struct {
		name, command, args, code, source, wantText string
		points, status                              int
	}{
		{"default", "chart", "", "USD/RUB", "cbr", "", 2, 200},
		{"group and pair", "chart@test_bot", "BTC 2w", "BTC/USD", "coinbase", "", 2, 200},
		{"invalid period", "chart", "USD 999", "", "", "период должен быть", 0, 200},
		{"empty", "chart", "", "USD/RUB", "cbr", "недостаточно данных", 0, 200},
		{"single point", "chart", "", "USD/RUB", "cbr", "недостаточно данных", 1, 200},
		{"outage", "chart", "", "USD/RUB", "cbr", "Не удалось получить историю", 0, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ratesServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				var points []string
				for i := 0; i < tc.points; i++ {
					date := time.Now().UTC().AddDate(0, 0, -i).Format(time.DateOnly)
					points = append(points, fmt.Sprintf(`{"date":%q,"rate":%d}`, date, 80+i))
				}
				fmt.Fprintf(w, `[{"code":%q,"source":%q,"points":[%s]}]`, tc.code, tc.source, strings.Join(points, ","))
			}))
			defer ratesServer.Close()
			client, _ := newRatesClient(ratesServer.URL + "/api/currency/rates")
			type sentReply struct {
				method, caption, message, chat, replyTo, filename string
				image                                             []byte
			}
			replies := make(chan sentReply, 2)
			telegram := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(r.URL.Path, "/getMe") {
					io.WriteString(w, `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"Test","username":"test_bot"}}`)
					return
				}
				sent := sentReply{method: r.URL.Path}
				if strings.HasSuffix(r.URL.Path, "/sendPhoto") {
					if err := r.ParseMultipartForm(10 << 20); err != nil {
						t.Error(err)
						http.Error(w, "invalid multipart", 400)
						return
					}
					defer r.MultipartForm.RemoveAll()
					file, header, err := r.FormFile("photo")
					if err != nil {
						t.Error(err)
						http.Error(w, "missing photo", 400)
						return
					}
					defer file.Close()
					sent.image, _ = io.ReadAll(file)
					sent.filename = header.Filename
				} else {
					if err := r.ParseForm(); err != nil {
						t.Error(err)
					}
				}
				sent.caption, sent.message = r.Form.Get("caption"), r.Form.Get("text")
				sent.chat, sent.replyTo = r.Form.Get("chat_id"), r.Form.Get("reply_to_message_id")
				replies <- sent
				io.WriteString(w, `{"ok":true,"result":{"message_id":123}}`)
			}))
			defer telegram.Close()
			bot, err := tgbotapi.NewBotAPIWithClient("test-token", telegram.URL+"/bot%s/%s", telegram.Client())
			if err != nil {
				t.Fatal(err)
			}
			handleUpdate(bot, client, tgbotapi.Update{Message: &tgbotapi.Message{
				MessageID: 42, Chat: &tgbotapi.Chat{ID: 7, Type: "group"}, Text: strings.TrimSpace("/" + tc.command + " " + tc.args),
				Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: len(tc.command) + 1}},
			}})
			select {
			case sent := <-replies:
				if sent.chat != "7" || sent.replyTo != "42" {
					t.Fatalf("wrong reply destination: %+v", sent)
				}
				if tc.wantText != "" {
					if !strings.HasSuffix(sent.method, "/sendMessage") || !strings.Contains(sent.message, tc.wantText) {
						t.Fatalf("unexpected error reply: %+v", sent)
					}
					return
				}
				if !strings.HasSuffix(sent.method, "/sendPhoto") || sent.filename != "finforme-chart.png" || !strings.Contains(sent.caption, tc.code) || !strings.Contains(sent.caption, "точек: 2") {
					t.Fatalf("unexpected photo: method=%s filename=%s caption=%s", sent.method, sent.filename, sent.caption)
				}
				assertChartPNG(t, sent.image)
			default:
				t.Fatal("command did not produce a reply")
			}
		})
	}
}

func TestChartRendersFlatAndLongHistory(t *testing.T) {
	for _, args := range []string{"RUB/ARS 2", "BTC 365"} {
		req, _ := parseChartRequest(args)
		from := time.Date(2025, 11, 12, 0, 0, 0, 0, time.UTC)
		data := quoteChart{Request: req, From: from, To: from.AddDate(0, 0, req.Days-1)}
		for i := 0; i < req.Days; i++ {
			data.Points = append(data.Points, chartPoint{Date: from.AddDate(0, 0, i), Rate: 18.1234})
		}
		image, err := renderQuoteChart(data)
		if err != nil {
			t.Fatal(err)
		}
		assertChartPNG(t, image)
	}
	if _, err := renderQuoteChart(quoteChart{}); err == nil {
		t.Fatal("rendered empty history")
	}
}

func assertChartPNG(t *testing.T, data []byte) {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 1200 || img.Bounds().Dy() != 720 || len(data) > 10<<20 {
		t.Fatalf("unexpected PNG: bounds=%v bytes=%d", img.Bounds(), len(data))
	}
}
