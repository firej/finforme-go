package stockquotes

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const yahooHistoryFixture = `{"chart":{"error":null,"result":[{
	"meta":{"symbol":"NVDA","currency":"USD","exchangeTimezoneName":"America/New_York"},
	"timestamp":[1739457000,1739543400,1739629800],
	"indicators":{"quote":[{"close":[150,null,160]}],"adjclose":[{"adjclose":[99,99,99]}]}
}]}}`

func historyClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	return &Client{yahoo: s.Client(), moex: s.Client(), yahooURL: s.URL + "/", moexURL: s.URL + "/"}
}

func TestYahooHistoryClosesAndCompletedDates(t *testing.T) {
	c := historyClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("interval") != "1d" || r.URL.Query().Get("period1") == "" || r.URL.Query().Get("period2") == "" {
			t.Error("missing daily window")
		}
		fmt.Fprint(w, yahooHistoryFixture)
	})
	q, err := c.History(context.Background(), Instruments[0], 31, time.Date(2025, 2, 15, 20, 0, 0, 0, time.UTC))
	if err != nil || len(q) != 1 || q[0].Date != "2025-02-13" || q[0].Price != 150 || !q[0].Time.IsZero() {
		t.Fatalf("should use raw close, omit missing and unfinished candles: %+v %v", q, err)
	}
	// Already Feb 15 in Moscow, still Feb 14 in New York: omit NY's current date.
	c = historyClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, strings.Replace(yahooHistoryFixture, "150,null,160", "150,155,160", 1))
	})
	q, err = c.History(context.Background(), Instruments[0], 31, time.Date(2025, 2, 14, 22, 0, 0, 0, time.UTC))
	if err != nil || len(q) != 1 || q[0].Date != "2025-02-13" {
		t.Fatalf("unfinished NY day: %+v %v", q, err)
	}
}

func TestMOEXHistoryPaginationAndColumnOrder(t *testing.T) {
	var offsets []string
	c := historyClient(t, func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Query()
		if r.URL.Path != "/YDEX/candles.json" || p.Get("interval") != "24" || p.Get("from") != "2025-01-15" || p.Get("till") != "2025-02-14" {
			t.Errorf("wrong request: %s", r.URL)
		}
		offsets = append(offsets, p.Get("start"))
		switch p.Get("start") {
		case "0":
			fmt.Fprint(w, `{"candles":{"columns":["close","begin"],"data":[[3200,"2025-02-13 00:00:00"],[null,"2025-02-12 00:00:00"]]}}`)
		case "2":
			fmt.Fprint(w, `{"candles":{"columns":["close","begin"],"data":[[3210,"2025-02-14 00:00:00"]]}}`)
		default:
			fmt.Fprint(w, `{"candles":{"columns":["close","begin"],"data":[]}}`)
		}
	})
	q, err := c.History(context.Background(), Instruments[2], 31, time.Date(2025, 2, 15, 20, 0, 0, 0, time.UTC))
	if err != nil || len(q) != 2 || q[0].Price != 3200 || q[1].Price != 3210 || !q[1].Time.IsZero() || strings.Join(offsets, ",") != "0,2,3" {
		t.Fatalf("quotes=%+v offsets=%v err=%v", q, offsets, err)
	}
}

func TestHistoryRejectsBadData(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		instrument int
	}{
		{"wrong ticker", strings.ReplaceAll(yahooHistoryFixture, "NVDA", "NBIS"), 0},
		{"wrong currency", strings.ReplaceAll(yahooHistoryFixture, "USD", "RUB"), 0},
		{"unequal arrays", strings.ReplaceAll(yahooHistoryFixture, "150,null,160", "150"), 0},
		{"negative close", strings.ReplaceAll(yahooHistoryFixture, "150,null,160", "-1,null,160"), 0},
		{"duplicate date", strings.ReplaceAll(strings.ReplaceAll(yahooHistoryFixture, "1739543400", "1739457000"), "150,null,160", "150,151,160"), 0},
		{"source error", strings.ReplaceAll(yahooHistoryFixture, `"error":null`, `"error":{"code":"Denied"}`), 0},
		{"empty MOEX", `{}`, 2},
		{"bad MOEX date", `{"candles":{"columns":["begin","close"],"data":[["bad",3200]]}}`, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := historyClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls > 1 && tc.instrument == 2 {
					fmt.Fprint(w, `{"candles":{"columns":["begin","close"],"data":[]}}`)
					return
				}
				fmt.Fprint(w, tc.body)
			})
			if _, err := c.History(context.Background(), Instruments[tc.instrument], 31, time.Date(2025, 2, 15, 20, 0, 0, 0, time.UTC)); err == nil {
				t.Fatal("accepted invalid history")
			}
		})
	}
}
