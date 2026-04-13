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
//   - Coinbase (api.coinbase.com) — BTC/USD
//   - BCRA (api.bcra.gob.ar) — ARS/USD (официальный курс ЦБ Аргентины)
//   - Кросс-курс — ARS/RUB = ARS/USD * USD/RUB
package main

import (
	"database/sql"
	"log"
	"os"

	"github.com/evbogdanov/finforme/internal/config"
	_ "github.com/go-sql-driver/mysql"
)

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

	log.Println("Importing Bitcoin price from Coinbase...")
	if err := importBitcoin(db); err != nil {
		log.Printf("ERROR importing Bitcoin price: %v", err)
		// Не выходим, продолжаем импорт других валют
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
