package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const chartHelp = "График: /chart [пара] [дни]\n" +
	"/chart help — показать эту справку\n" +
	"/chart — USD/RUB за 14 дней\n" +
	"/chart EUR 30\n/chart BTC/USD 2w\n" +
	"Пары: USD/RUB, EUR/RUB, BTC/USD, USD/ARS, RUB/ARS.\n" +
	"Период: от 2 до 365 дней; 2w = 14 дней. ARS — Blue Sell."

type chartInstrument struct {
	Code, Source, SourceName string
	Decimals                 int
}

var chartInstruments = map[string]chartInstrument{
	"USD/RUB": {"USD/RUB", "cbr", "ЦБ РФ", 4},
	"EUR/RUB": {"EUR/RUB", "cbr", "ЦБ РФ", 4},
	"BTC/USD": {"BTC/USD", "coinbase", "Coinbase", 2},
	"USD/ARS": {"USD/ARS", "bluedollar_sell", "Blue Dollar Sell", 2},
	"RUB/ARS": {"RUB/ARS", "cross_blue_sell", "Blue Sell × ЦБ РФ", 4},
}

type chartRequest struct {
	Instrument chartInstrument
	Days       int
}

func parseChartRequest(args string) (chartRequest, error) {
	req := chartRequest{Instrument: chartInstruments["USD/RUB"], Days: 14}
	fields := strings.Fields(strings.ToUpper(args))
	if len(fields) > 2 {
		return req, fmt.Errorf("ожидаются пара и период")
	}
	if len(fields) == 0 {
		return req, nil
	}
	aliases := map[string]string{"USD": "USD/RUB", "EUR": "EUR/RUB", "BTC": "BTC/USD"}
	code := fields[0]
	if alias, ok := aliases[code]; ok {
		code = alias
	}
	if instrument, ok := chartInstruments[code]; ok {
		req.Instrument = instrument
		fields = fields[1:]
	} else if len(fields) != 1 {
		return req, fmt.Errorf("неизвестная валютная пара")
	}
	if len(fields) == 0 {
		return req, nil
	}
	period := fields[0]
	multiplier := 1
	if strings.HasSuffix(period, "W") || strings.HasSuffix(period, "Н") {
		multiplier = 7
	}
	for _, suffix := range []string{"W", "Н", "D", "Д"} {
		if strings.HasSuffix(period, suffix) {
			period = strings.TrimSuffix(period, suffix)
			break
		}
	}
	days, err := strconv.Atoi(period)
	if err != nil || days < 1 || days > 365/multiplier || days*multiplier < 2 {
		return req, fmt.Errorf("период должен быть от 2 до 365 дней")
	}
	req.Days = days * multiplier
	return req, nil
}

type chartPoint struct {
	Date time.Time
	Rate float64
}

type quoteChart struct {
	Request  chartRequest
	From, To time.Time
	Points   []chartPoint
}

// history keeps actual quote dates, without inventing missing observations.
func (c *ratesClient) history(ctx context.Context, req chartRequest, now time.Time) (quoteChart, error) {
	moscow := now.In(time.FixedZone("MSK", 3*60*60))
	to := time.Date(moscow.Year(), moscow.Month(), moscow.Day(), 0, 0, 0, 0, time.UTC)
	data := quoteChart{Request: req, From: to.AddDate(0, 0, 1-req.Days), To: to}
	base, err := url.Parse(c.endpoint)
	if err != nil {
		return data, err
	}
	endpoint := base.ResolveReference(&url.URL{Path: "charts"})
	query := endpoint.Query()
	// Request an extra day because the website's history cutoff uses UTC.
	query.Set("days", strconv.Itoa(req.Days+1))
	endpoint.RawQuery = query.Encode()
	var series []struct {
		Code, Source string
		Points       []struct {
			Date string
			Rate float64
		}
	}
	if err := c.getJSON(ctx, endpoint.String(), &series); err != nil {
		return data, err
	}
	if series == nil {
		return data, fmt.Errorf("history API response has no series array")
	}
	found := false
	seen := make(map[string]bool)
	for _, s := range series {
		if s.Code != req.Instrument.Code || s.Source != req.Instrument.Source {
			continue
		}
		if found {
			return data, fmt.Errorf("duplicate history series")
		}
		found = true
		for _, p := range s.Points {
			date, err := time.Parse(time.DateOnly, p.Date)
			if err != nil {
				return data, fmt.Errorf("invalid history date")
			}
			if date.Before(data.From) || date.After(data.To) {
				continue
			}
			if p.Rate <= 0 || math.IsNaN(p.Rate) || math.IsInf(p.Rate, 0) || seen[p.Date] {
				return data, fmt.Errorf("invalid or duplicate history point")
			}
			seen[p.Date] = true
			data.Points = append(data.Points, chartPoint{Date: date, Rate: p.Rate})
		}
	}
	sort.Slice(data.Points, func(i, j int) bool { return data.Points[i].Date.Before(data.Points[j].Date) })
	return data, nil
}

func (data quoteChart) caption() string {
	first, last := data.Points[0], data.Points[len(data.Points)-1]
	change := (last.Rate/first.Rate - 1) * 100
	return fmt.Sprintf("%s · %s\nПериод: %s — %s (%d дней, МСК)\nКотировки: %s — %s · точек: %d\nПоследняя: %.*f · изменение между первой и последней: %+.2f%%",
		data.Request.Instrument.Code, data.Request.Instrument.SourceName,
		data.From.Format("02.01.2006"), data.To.Format("02.01.2006"), data.Request.Days,
		first.Date.Format("02.01.2006"), last.Date.Format("02.01.2006"), len(data.Points),
		data.Request.Instrument.Decimals, last.Rate, change)
}

func handleChart(bot *tgbotapi.BotAPI, rates *ratesClient, msg *tgbotapi.Message) {
	reply := func(message string) {
		text := tgbotapi.NewMessage(msg.Chat.ID, message)
		text.ReplyToMessageID = msg.MessageID
		if _, err := bot.Send(text); err != nil {
			log.Printf("Ошибка отправки ответа на /chart: %v", err)
		}
	}
	args := strings.TrimSpace(msg.CommandArguments())
	if strings.EqualFold(args, "help") {
		reply(chartHelp)
		return
	}
	req, err := parseChartRequest(args)
	if err != nil {
		reply(err.Error() + "\n\n" + chartHelp)
		return
	}
	data, err := rates.history(context.Background(), req, time.Now())
	if err != nil {
		log.Printf("Ошибка получения истории котировок: %v", err)
		reply("Не удалось получить историю котировок. Попробуйте позже.")
		return
	}
	if len(data.Points) < 2 {
		message := fmt.Sprintf("Для %s за %d дней недостаточно данных: точек %d, нужно хотя бы две.", req.Instrument.Code, req.Days, len(data.Points))
		if req.Days < 365 {
			message += fmt.Sprintf(" Попробуйте больший период: /chart %s %d.", req.Instrument.Code, min(365, max(30, req.Days*2)))
		}
		reply(message)
		return
	}
	png, err := renderQuoteChart(data)
	if err != nil {
		log.Printf("Ошибка построения графика: %v", err)
		reply("Не удалось построить график. Попробуйте позже.")
		return
	}
	photo := tgbotapi.NewPhoto(msg.Chat.ID, tgbotapi.FileBytes{Name: "finforme-chart.png", Bytes: png})
	photo.Caption = data.caption()
	photo.ReplyToMessageID = msg.MessageID
	if _, err := bot.Send(photo); err != nil {
		log.Printf("Ошибка отправки графика: %v", err)
		reply("Не удалось отправить график. Попробуйте ещё раз.")
	}
}
