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
//   - BCRA (api.bcra.gob.ar) — ARS/USD (официальный курс ЦБ Аргентины)
//   - Кросс-курс — ARS/RUB = ARS/USD * USD/RUB
package main

import (
	"database/sql"
	"encoding/json"
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
	usdRUB, err := importCBR(db)
	if err != nil {
		log.Printf("ERROR importing CBR rates: %v", err)
		os.Exit(1)
	}

	log.Println("Importing USD/ARS from BCRA...")
	usdARS, bcraDate, err := importBCRA(db)
	if err != nil {
		log.Printf("ERROR importing BCRA rates: %v", err)
		os.Exit(1)
	}

	// Кросс-курс RUB/ARS = USD/ARS / USD/RUB (сколько песо за 1 рубль)
	if usdARS > 0 && usdRUB > 0 {
		rubARS := usdARS / usdRUB
		_, err := db.Exec(`
			INSERT INTO currency_rates (code, name, rate, source, rate_date)
			VALUES ('RUB/ARS', 'Аргентинский песо', ?, 'cross', ?)
			ON DUPLICATE KEY UPDATE rate = VALUES(rate), name = VALUES(name), created_at = CURRENT_TIMESTAMP
		`, rubARS, bcraDate)
		if err != nil {
			log.Printf("ERROR inserting RUB/ARS cross-rate: %v", err)
		} else {
			log.Printf("Cross: RUB/ARS = %.4f (1 RUB = %.4f ARS)", rubARS, rubARS)
		}
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

// importCBR импортирует USD/RUB и EUR/RUB из ЦБ РФ.
// Возвращает курс USD/RUB для последующего вычисления кросс-курсов.
func importCBR(db *sql.DB) (usdRUB float64, err error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", "https://www.cbr.ru/scripts/XML_daily.asp", nil)
	if err != nil {
		return 0, fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; finforme-import/1.0)")
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("CBR returned status %d", resp.StatusCode)
	}

	// Читаем сырые байты (Windows-1251)
	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("read body failed: %w", err)
	}

	// Декодируем из Windows-1251 в UTF-8
	utf8Bytes, _, err := transform.Bytes(charmap.Windows1251.NewDecoder(), rawBody)
	if err != nil {
		return 0, fmt.Errorf("decode win1251 failed: %w", err)
	}

	// Go XML-парсер не поддерживает encoding="windows-1251" —
	// удаляем XML-декларацию целиком, содержимое уже в UTF-8
	bodyStr := xmlDeclRe.ReplaceAllString(string(utf8Bytes), "")

	var valCurs cbrValCurs
	if err := xml.Unmarshal([]byte(bodyStr), &valCurs); err != nil {
		return 0, fmt.Errorf("xml parse failed: %w", err)
	}

	// Дата в ответе ЦБ РФ: DD.MM.YYYY → конвертируем в YYYY-MM-DD
	cbrDate, err := time.Parse("02.01.2006", valCurs.Date)
	if err != nil {
		return 0, fmt.Errorf("CBR: failed to parse date %q: %w", valCurs.Date, err)
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
		if v.CharCode == "USD" {
			usdRUB = rate
		}
		imported++
	}

	if imported == 0 {
		return 0, fmt.Errorf("no rates imported from CBR")
	}
	return usdRUB, nil
}

// --- BCRA (Banco Central de la República Argentina) ---

// bcraResponse — ответ API BCRA /estadisticascambiarias/v1.0/Cotizaciones/USD
type bcraResponse struct {
	Status  int `json:"status"`
	Results []struct {
		Fecha   string `json:"fecha"`
		Detalle []struct {
			CodigoMoneda   string  `json:"codigoMoneda"`
			Descripcion    string  `json:"descripcion"`
			TipoCotizacion float64 `json:"tipoCotizacion"`
		} `json:"detalle"`
	} `json:"results"`
}

// importBCRA импортирует курс из официального API Центрального банка Аргентины.
// tipoCotizacion — количество песо за 1 USD.
// Сохраняет USD/ARS = tipoCotizacion (сколько песо за 1 доллар).
// Возвращает (usdARS, dateStr, error) для последующего вычисления кросс-курсов.
func importBCRA(db *sql.DB) (usdARS float64, dateStr string, err error) {
	client := &http.Client{Timeout: 15 * time.Second}
	url := "https://api.bcra.gob.ar/estadisticascambiarias/v1.0/Cotizaciones/USD"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, "", fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; finforme-import/1.0)")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("BCRA returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, "", fmt.Errorf("read body failed: %w", err)
	}

	var bcra bcraResponse
	if err := json.Unmarshal(body, &bcra); err != nil {
		return 0, "", fmt.Errorf("json parse failed: %w", err)
	}

	if len(bcra.Results) == 0 || len(bcra.Results[0].Detalle) == 0 {
		return 0, "", fmt.Errorf("BCRA: empty results")
	}

	latest := bcra.Results[0]
	cotizacion := latest.Detalle[0].TipoCotizacion
	if cotizacion == 0 {
		return 0, "", fmt.Errorf("BCRA: tipoCotizacion is zero")
	}

	// USD/ARS = tipoCotizacion (сколько песо за 1 доллар, например 1392.5)
	usdARS = cotizacion

	// BCRA возвращает дату в формате "2026-04-07T00:00:00Z" или "2026-04-07"
	// Обрезаем до первых 10 символов: "2026-04-07"
	dateStr = latest.Fecha
	if len(dateStr) > 10 {
		dateStr = dateStr[:10]
	}

	_, err = db.Exec(`
		INSERT INTO currency_rates (code, name, rate, source, rate_date)
		VALUES ('USD/ARS', 'Аргентинский песо', ?, 'bcra', ?)
		ON DUPLICATE KEY UPDATE rate = VALUES(rate), name = VALUES(name), created_at = CURRENT_TIMESTAMP
	`, usdARS, dateStr)
	if err != nil {
		return 0, "", fmt.Errorf("BCRA: db insert failed: %w", err)
	}

	log.Printf("BCRA: USD/ARS = %.2f (date: %s)", usdARS, dateStr)
	return usdARS, dateStr, nil
}
