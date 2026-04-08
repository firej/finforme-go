// Скрипт для однократного импорта исторических курсов валют с сайта ЦБ РФ и BCRA.
// Загружает данные по месяцам за указанный период.
//
// Использование:
//
//	go run ./cmd/import-rates-history/ -from 2020-01-01 -to 2026-04-04
//
// По умолчанию импортирует последние 3 года.
// Использует те же переменные окружения, что и основной сервер (DATABASE_DSN).
package main

import (
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/evbogdanov/finforme/internal/config"
	_ "github.com/go-sql-driver/mysql"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/transform"
)

// Коды валют на сайте ЦБ РФ
var cbrCurrencies = []struct {
	code    string // код в нашей БД, например USD/RUB
	name    string
	cbrCode string // код на сайте ЦБ РФ
}{
	{"USD/RUB", "Доллар США", "R01235"},
	{"EUR/RUB", "Евро", "R01239"},
}

var xmlDeclRe = regexp.MustCompile(`<\?xml[^?]*\?>`)

func main() {
	fromStr := flag.String("from", "", "Начальная дата (YYYY-MM-DD), по умолчанию 3 года назад")
	toStr := flag.String("to", "", "Конечная дата (YYYY-MM-DD), по умолчанию сегодня")
	flag.Parse()

	now := time.Now()

	var fromDate, toDate time.Time
	var err error

	if *fromStr != "" {
		fromDate, err = time.Parse("2006-01-02", *fromStr)
		if err != nil {
			log.Fatalf("Invalid -from date: %v", err)
		}
	} else {
		fromDate = now.AddDate(-3, 0, 0)
	}

	if *toStr != "" {
		toDate, err = time.Parse("2006-01-02", *toStr)
		if err != nil {
			log.Fatalf("Invalid -to date: %v", err)
		}
	} else {
		toDate = now
	}

	log.Printf("Importing historical rates from %s to %s",
		fromDate.Format("2006-01-02"), toDate.Format("2006-01-02"))

	cfg := config.Load()
	db, err := sql.Open("mysql", cfg.DatabaseDSN)
	if err != nil {
		log.Fatal("Failed to open database:", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatal("Failed to connect to database:", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}

	totalImported := 0
	totalErrors := 0

	// --- ЦБ РФ: USD/RUB и EUR/RUB ---
	for _, cur := range cbrCurrencies {
		log.Printf("--- Importing %s (CBR code: %s) ---", cur.code, cur.cbrCode)

		// Разбиваем на месячные чанки — ЦБ РФ лучше работает с небольшими диапазонами
		chunkStart := fromDate
		for chunkStart.Before(toDate) {
			chunkEnd := chunkStart.AddDate(0, 1, 0)
			if chunkEnd.After(toDate) {
				chunkEnd = toDate
			}

			imported, err := importCBRHistoricalChunk(db, client, cur.code, cur.name, cur.cbrCode, chunkStart, chunkEnd)
			if err != nil {
				log.Printf("ERROR %s [%s - %s]: %v",
					cur.code,
					chunkStart.Format("2006-01-02"),
					chunkEnd.Format("2006-01-02"),
					err)
				totalErrors++
			} else {
				totalImported += imported
			}

			// Небольшая пауза чтобы не перегружать ЦБ РФ
			time.Sleep(200 * time.Millisecond)

			chunkStart = chunkEnd.AddDate(0, 0, 1)
		}
	}

	// --- BCRA: USD/ARS ---
	log.Printf("--- Importing USD/ARS from BCRA ---")
	chunkStart := fromDate
	for chunkStart.Before(toDate) {
		chunkEnd := chunkStart.AddDate(0, 1, 0)
		if chunkEnd.After(toDate) {
			chunkEnd = toDate
		}

		imported, err := importBCRAHistoricalChunk(db, client, chunkStart, chunkEnd)
		if err != nil {
			log.Printf("ERROR USD/ARS [%s - %s]: %v",
				chunkStart.Format("2006-01-02"),
				chunkEnd.Format("2006-01-02"),
				err)
			totalErrors++
		} else {
			totalImported += imported
		}

		time.Sleep(200 * time.Millisecond)
		chunkStart = chunkEnd.AddDate(0, 0, 1)
	}

	// --- Кросс-курс RUB/ARS = USD/ARS / USD/RUB ---
	log.Printf("--- Computing cross-rate RUB/ARS ---")
	crossImported, err := importCrossRateRUBARS(db, fromDate, toDate)
	if err != nil {
		log.Printf("ERROR computing RUB/ARS cross-rate: %v", err)
		totalErrors++
	} else {
		log.Printf("RUB/ARS cross-rate: %d records", crossImported)
		totalImported += crossImported
	}

	log.Printf("Done: imported %d records, errors: %d", totalImported, totalErrors)
}

// --- ЦБ РФ ---

// cbrDynamicValCurs — структура XML-ответа динамики курсов ЦБ РФ
type cbrDynamicValCurs struct {
	XMLName xml.Name           `xml:"ValCurs"`
	Records []cbrDynamicRecord `xml:"Record"`
}

type cbrDynamicRecord struct {
	Date      string `xml:"Date,attr"`
	Nominal   string `xml:"Nominal"`
	VunitRate string `xml:"VunitRate"`
}

func importCBRHistoricalChunk(
	db *sql.DB,
	client *http.Client,
	code, name, cbrCode string,
	from, to time.Time,
) (int, error) {
	fromStr := from.Format("02.01.2006")
	toStr := to.Format("02.01.2006")

	url := fmt.Sprintf(
		"https://www.cbr.ru/scripts/XML_dynamic.asp?date_req1=%s&date_req2=%s&VAL_NM_RQ=%s",
		fromStr, toStr, cbrCode,
	)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; finforme-history/1.0)")

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("CBR returned status %d", resp.StatusCode)
	}

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("read body: %w", err)
	}

	utf8Bytes, _, err := transform.Bytes(charmap.Windows1251.NewDecoder(), rawBody)
	if err != nil {
		return 0, fmt.Errorf("decode win1251: %w", err)
	}

	bodyStr := xmlDeclRe.ReplaceAllString(string(utf8Bytes), "")

	var valCurs cbrDynamicValCurs
	if err := xml.Unmarshal([]byte(bodyStr), &valCurs); err != nil {
		return 0, fmt.Errorf("xml parse: %w", err)
	}

	if len(valCurs.Records) == 0 {
		// Нет данных за период (праздники и т.д.) — не ошибка
		return 0, nil
	}

	imported := 0
	for _, rec := range valCurs.Records {
		// Дата в формате "10.01.2024" → "2024-01-10"
		t, err := time.Parse("02.01.2006", rec.Date)
		if err != nil {
			log.Printf("  skip: bad date %q: %v", rec.Date, err)
			continue
		}
		dateStr := t.Format("2006-01-02")

		valStr := strings.ReplaceAll(rec.VunitRate, ",", ".")
		nomStr := strings.ReplaceAll(rec.Nominal, ",", ".")

		val, err1 := strconv.ParseFloat(valStr, 64)
		nom, err2 := strconv.ParseFloat(nomStr, 64)
		if err1 != nil || err2 != nil || nom == 0 {
			log.Printf("  skip: bad rate %q nominal %q", rec.VunitRate, rec.Nominal)
			continue
		}

		rate := val / nom

		_, err = db.Exec(`
			INSERT INTO currency_rates (code, name, rate, source, rate_date)
			VALUES (?, ?, ?, 'cbr', ?)
			ON DUPLICATE KEY UPDATE rate = VALUES(rate), name = VALUES(name), created_at = CURRENT_TIMESTAMP
		`, code, name, rate, dateStr)
		if err != nil {
			log.Printf("  db insert failed for %s %s: %v", code, dateStr, err)
			continue
		}
		imported++
	}

	log.Printf("  %s [%s - %s]: %d records", code, fromStr, toStr, imported)
	return imported, nil
}

// --- BCRA (Banco Central de la República Argentina) ---

// bcraHistoryResponse — ответ API BCRA с историческими данными
type bcraHistoryResponse struct {
	Status  int `json:"status"`
	Results []struct {
		Fecha   string `json:"fecha"`
		Detalle []struct {
			CodigoMoneda   string  `json:"codigoMoneda"`
			TipoCotizacion float64 `json:"tipoCotizacion"`
		} `json:"detalle"`
	} `json:"results"`
}

// importBCRAHistoricalChunk загружает исторические курсы ARS/USD из BCRA за период [from, to].
// tipoCotizacion — количество песо за 1 USD, поэтому ARS/USD = 1 / tipoCotizacion.
func importBCRAHistoricalChunk(
	db *sql.DB,
	client *http.Client,
	from, to time.Time,
) (int, error) {
	fromStr := from.Format("2006-01-02")
	toStr := to.Format("2006-01-02")

	url := fmt.Sprintf(
		"https://api.bcra.gob.ar/estadisticascambiarias/v1.0/Cotizaciones/USD?fechadesde=%s&fechahasta=%s",
		fromStr, toStr,
	)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; finforme-history/1.0)")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("BCRA returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("read body: %w", err)
	}

	var bcra bcraHistoryResponse
	if err := json.Unmarshal(body, &bcra); err != nil {
		return 0, fmt.Errorf("json parse: %w", err)
	}

	if len(bcra.Results) == 0 {
		return 0, nil
	}

	imported := 0
	for _, day := range bcra.Results {
		if len(day.Detalle) == 0 {
			continue
		}
		cotizacion := day.Detalle[0].TipoCotizacion
		if cotizacion == 0 {
			continue
		}
		// USD/ARS = tipoCotizacion (сколько песо за 1 доллар, например 1392.5)
		usdARS := cotizacion

		// BCRA возвращает дату в формате "2026-04-07T00:00:00Z" или "2026-04-07"
		// Обрезаем до первых 10 символов: "2026-04-07"
		dateStr := day.Fecha
		if len(dateStr) > 10 {
			dateStr = dateStr[:10]
		}

		_, err := db.Exec(`
			INSERT INTO currency_rates (code, name, rate, source, rate_date)
			VALUES ('USD/ARS', 'Аргентинский песо', ?, 'bcra', ?)
			ON DUPLICATE KEY UPDATE rate = VALUES(rate), name = VALUES(name), created_at = CURRENT_TIMESTAMP
		`, usdARS, dateStr)
		if err != nil {
			log.Printf("  BCRA: db insert failed for %s: %v", dateStr, err)
			continue
		}
		imported++
	}

	log.Printf("  USD/ARS [%s - %s]: %d records", fromStr, toStr, imported)
	return imported, nil
}

// importCrossRateRUBARS вычисляет кросс-курс RUB/ARS = USD/ARS / USD/RUB
// (сколько песо за 1 рубль) по всем датам в диапазоне [from, to].
func importCrossRateRUBARS(db *sql.DB, from, to time.Time) (int, error) {
	rows, err := db.Query(`
		SELECT usdars.rate_date, CAST(usdars.rate AS DOUBLE), CAST(usdrub.rate AS DOUBLE)
		FROM currency_rates usdars
		INNER JOIN currency_rates usdrub
			ON usdrub.code = 'USD/RUB' AND usdrub.source = 'cbr' AND usdrub.rate_date = usdars.rate_date
		WHERE usdars.code = 'USD/ARS' AND usdars.source = 'bcra'
		  AND usdars.rate_date >= ? AND usdars.rate_date <= ?
		ORDER BY usdars.rate_date
	`, from.Format("2006-01-02"), to.Format("2006-01-02"))
	if err != nil {
		return 0, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	imported := 0
	for rows.Next() {
		var dateStr string
		var usdARS, usdRUB float64
		if err := rows.Scan(&dateStr, &usdARS, &usdRUB); err != nil {
			log.Printf("  RUB/ARS: scan error: %v", err)
			continue
		}
		if usdRUB == 0 {
			continue
		}
		// MariaDB может вернуть дату как "2026-03-03T00:00:00Z" — обрезаем до YYYY-MM-DD
		if len(dateStr) > 10 {
			dateStr = dateStr[:10]
		}
		// RUB/ARS = USD/ARS / USD/RUB (сколько песо за 1 рубль)
		rubARS := usdARS / usdRUB

		_, err := db.Exec(`
			INSERT INTO currency_rates (code, name, rate, source, rate_date)
			VALUES ('RUB/ARS', 'Аргентинский песо', ?, 'cross', ?)
			ON DUPLICATE KEY UPDATE rate = VALUES(rate), created_at = CURRENT_TIMESTAMP
		`, rubARS, dateStr)
		if err != nil {
			log.Printf("  RUB/ARS: db insert failed for %s: %v", dateStr, err)
			continue
		}
		imported++
	}
	return imported, rows.Err()
}
