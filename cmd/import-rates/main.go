// Скрипт для импорта курсов валют в базу данных.
// Запускается по крону, например:
//
//	0 12 * * * /path/to/import-rates
//
// Использует переменные окружения (те же, что и основной сервер):
//
//	DATABASE_DSN — строка подключения к MariaDB
//
// Источники:
//   - ЦБ РФ (cbr.ru) — USD/RUB, EUR/RUB
package main

import (
	"database/sql"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/evbogdanov/finforme/internal/config"
	_ "github.com/go-sql-driver/mysql"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/transform"
)

// xmlDeclRe удаляет XML-декларацию вида <?xml ... ?>
var xmlDeclRe = regexp.MustCompile(`<\?xml[^?]*\?>`)

func main() {
	cfg := config.Load()

	db, err := sql.Open("mysql", cfg.DatabaseDSN)
	if err != nil {
		log.Fatal("Failed to open database:", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatal("Failed to connect to database:", err)
	}

	log.Println("Importing currency rates from CBR...")

	if err := importCBR(db); err != nil {
		log.Printf("ERROR importing CBR rates: %v", err)
		os.Exit(1)
	}

	log.Println("Done successfully")
}

// --- ЦБ РФ ---

type cbrValCurs struct {
	XMLName xml.Name    `xml:"ValCurs"`
	Date    string      `xml:"Date,attr"`
	Valutes []cbrValute `xml:"Valute"`
}

type cbrValute struct {
	CharCode  string `xml:"CharCode"`
	Nominal   string `xml:"Nominal"`
	Name      string `xml:"Name"`
	VunitRate string `xml:"VunitRate"`
}

func importCBR(db *sql.DB) error {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", "https://www.cbr.ru/scripts/XML_daily.asp", nil)
	if err != nil {
		return fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; finforme-import/1.0)")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("CBR returned status %d", resp.StatusCode)
	}

	// Читаем сырые байты (Windows-1251)
	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body failed: %w", err)
	}

	// Декодируем из Windows-1251 в UTF-8
	utf8Bytes, _, err := transform.Bytes(charmap.Windows1251.NewDecoder(), rawBody)
	if err != nil {
		return fmt.Errorf("decode win1251 failed: %w", err)
	}

	// Go XML-парсер не поддерживает encoding="windows-1251" —
	// удаляем XML-декларацию целиком, содержимое уже в UTF-8
	bodyStr := xmlDeclRe.ReplaceAllString(string(utf8Bytes), "")

	var valCurs cbrValCurs
	if err := xml.Unmarshal([]byte(bodyStr), &valCurs); err != nil {
		return fmt.Errorf("xml parse failed: %w", err)
	}

	// Дата в ответе ЦБ РФ: DD.MM.YYYY → конвертируем в YYYY-MM-DD
	cbrDate, err := time.Parse("02.01.2006", valCurs.Date)
	if err != nil {
		return fmt.Errorf("CBR: failed to parse date %q: %w", valCurs.Date, err)
	}
	dateStr := cbrDate.Format("2006-01-02")
	log.Printf("CBR date from response: %s", dateStr)

	imported := 0
	for _, v := range valCurs.Valutes {
		if v.CharCode != "USD" && v.CharCode != "EUR" {
			continue
		}

		// Значения в формате "85,1234" — заменяем запятую на точку
		valStr := strings.ReplaceAll(v.VunitRate, ",", ".")
		nomStr := strings.ReplaceAll(v.Nominal, ",", ".")

		val, err1 := strconv.ParseFloat(valStr, 64)
		nom, err2 := strconv.ParseFloat(nomStr, 64)
		if err1 != nil || err2 != nil || nom == 0 {
			log.Printf("CBR: failed to parse rate for %s: VunitRate=%q Nominal=%q", v.CharCode, v.VunitRate, v.Nominal)
			continue
		}

		rate := val / nom
		code := v.CharCode + "/RUB"

		_, err := db.Exec(`
			INSERT INTO currency_rates (code, name, rate, source, rate_date)
			VALUES (?, ?, ?, 'cbr', ?)
			ON DUPLICATE KEY UPDATE rate = VALUES(rate), name = VALUES(name), created_at = CURRENT_TIMESTAMP
		`, code, v.Name, rate, dateStr)
		if err != nil {
			log.Printf("CBR: db insert failed for %s: %v", code, err)
			continue
		}

		log.Printf("CBR: %s = %.4f RUB", code, rate)
		imported++
	}

	if imported == 0 {
		return fmt.Errorf("no rates imported from CBR")
	}
	return nil
}
