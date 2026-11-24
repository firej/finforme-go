package stockquotes

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const yahooFixture = `{"chart":{"error":null,"result":[{"meta":{"symbol":"NVDA","currency":"USD","exchangeTimezoneName":"America/New_York","regularMarketPrice":153.21,"regularMarketTime":1739572200}}]}}`
const moexFixture = `{
	"securities":{"columns":["PREVDATE","PREVPRICE","CURRENCYID","SECID","BOARDID"],"data":[["2025-02-13",3282,"SUR","YDEX","TQBR"]]},
	"marketdata":{"columns":["TIME","LAST","BOARDID","SECID"],"data":[["20:33:45",3252,"TQBR","YDEX"]]},
	"dataversion":{"columns":["trade_date"],"data":[["2025-02-14"]]}
}`

func fixtureClient(t *testing.T, body string, status int) *Client {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Accept") != "application/json" {
			t.Errorf("unexpected request: %s %v", r.Method, r.Header)
		}
		if strings.HasSuffix(r.URL.Path, ".json") && r.URL.Query().Get("dataversion.columns") != "trade_date" {
			t.Error("MOEX request must include the actual trading date")
		}
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(server.Close)
	return &Client{yahoo: server.Client(), moex: server.Client(), yahooURL: server.URL + "/", moexURL: server.URL + "/"}
}

func TestYahooRegularSessionPriceAndExchangeDate(t *testing.T) {
	// 2025-02-15 00:30 UTC is still Feb 14 in New York. Ignore any postmarket price.
	body := strings.Replace(yahooFixture, "1739572200", "1739579400", 1)
	body = strings.Replace(body, `"regularMarketPrice":153.21`, `"postMarketPrice":999,"regularMarketPrice":153.21`, 1)
	q, err := fixtureClient(t, body, 200).Fetch(context.Background(), Instruments[0])
	if err != nil || q.Code() != "NVDA/USD" || q.Price != 153.21 || q.Date != "2025-02-14" || q.Time.Format(time.RFC3339) != "2025-02-15T00:30:00Z" {
		t.Fatalf("quote=%+v err=%v", q, err)
	}
	nbis := strings.ReplaceAll(yahooFixture, "NVDA", "NBIS")
	q, err = fixtureClient(t, nbis, 200).Fetch(context.Background(), Instruments[1])
	if err != nil || q.Code() != "NBIS/USD" {
		t.Fatalf("NBIS: %+v %v", q, err)
	}
}

func TestMOEXColumnMappingAndPreviousClose(t *testing.T) {
	q, err := fixtureClient(t, moexFixture, 200).Fetch(context.Background(), Instruments[2])
	if err != nil || q.Price != 3252 || q.Date != "2025-02-14" || q.Time.Format(time.RFC3339) != "2025-02-14T17:33:45Z" {
		t.Fatalf("quote=%+v err=%v", q, err)
	}
	closed := strings.Replace(moexFixture, `"20:33:45",3252`, `null,null`, 1)
	q, err = fixtureClient(t, closed, 200).Fetch(context.Background(), Instruments[2])
	if err != nil || q.Price != 3282 || q.Date != "2025-02-13" || !q.Time.IsZero() {
		t.Fatalf("previous close must keep its date and omit time: %+v %v", q, err)
	}
}

func TestRejectMalformedOrUnavailableQuotes(t *testing.T) {
	for _, tc := range []struct {
		name, body         string
		status, instrument int
	}{
		{"Yahoo limit", "limited", 429, 0},
		{"Yahoo missing", `{"chart":{"result":[],"error":null}}`, 200, 0},
		{"Yahoo error", strings.Replace(yahooFixture, `"error":null`, `"error":{"code":"Not Found"}`, 1), 200, 0},
		{"Yahoo wrong ticker", strings.ReplaceAll(yahooFixture, "NVDA", "NBIS"), 200, 0},
		{"Yahoo wrong currency", strings.ReplaceAll(yahooFixture, "USD", "EUR"), 200, 0},
		{"Yahoo zero price", strings.ReplaceAll(yahooFixture, "153.21", "0"), 200, 0},
		{"Yahoo null price", strings.ReplaceAll(yahooFixture, "153.21", "null"), 200, 0},
		{"Yahoo negative price", strings.ReplaceAll(yahooFixture, "153.21", "-1"), 200, 0},
		{"Yahoo missing timestamp", strings.ReplaceAll(yahooFixture, "1739572200", "0"), 200, 0},
		{"Yahoo future timestamp", strings.ReplaceAll(yahooFixture, "1739572200", "4102444800"), 200, 0},
		{"Yahoo timezone", strings.ReplaceAll(yahooFixture, "America/New_York", "Mars"), 200, 0},
		{"MOEX wrong board", strings.ReplaceAll(moexFixture, "TQBR", "TQTF"), 200, 2},
		{"MOEX wrong currency", strings.ReplaceAll(moexFixture, "SUR", "USD"), 200, 2},
		{"MOEX wrong ticker", strings.ReplaceAll(moexFixture, "YDEX", "SBER"), 200, 2},
		{"MOEX missing LAST", strings.ReplaceAll(moexFixture, `"LAST"`, `"OTHER"`), 200, 2},
		{"MOEX malformed row", strings.ReplaceAll(moexFixture, `"TIME","LAST"`, `"LAST"`), 200, 2},
		{"MOEX zero price", strings.ReplaceAll(moexFixture, "3252", "0"), 200, 2},
		{"MOEX missing date", strings.ReplaceAll(moexFixture, "trade_date", "other_date"), 200, 2},
		{"MOEX missing time", strings.ReplaceAll(moexFixture, "20:33:45", ""), 200, 2},
		{"MOEX no previous close", strings.ReplaceAll(strings.ReplaceAll(moexFixture, "3252", "null"), "3282", "null"), 200, 2},
		{"HTML", "<html>error</html>", 200, 2},
		{"oversized", strings.Repeat(" ", (1<<20)+1), 200, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if q, err := fixtureClient(t, tc.body, tc.status).Fetch(context.Background(), Instruments[tc.instrument]); err == nil {
				t.Fatalf("accepted invalid quote: %+v", q)
			}
		})
	}
}

func TestMOEXTrustIsScopedAndVerified(t *testing.T) {
	c, err := NewClient()
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(moexRoot)
	if block == nil {
		t.Fatal("missing certificate")
	}
	if got := fmt.Sprintf("%X", sha256.Sum256(block.Bytes)); got != "D26D2D0231B7C39F92CC738512BA54103519E4405D68B5BD703E9788CA8ECF31" {
		t.Fatalf("unexpected root: %s", got)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil || !cert.IsCA {
		t.Fatalf("invalid CA: %v", err)
	}
	transport := c.moex.Transport.(*http.Transport)
	if transport.TLSClientConfig.InsecureSkipVerify || c.yahoo.Transport != nil {
		t.Fatal("TLS trust leaked or verification disabled")
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	if _, err := c.moex.Get(server.URL); err == nil {
		t.Fatal("accepted untrusted TLS certificate")
	}
}
