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
//   - BCRA (api.bcra.gob.ar) — USD/ARS (официальный курс ЦБ Аргентины)
//   - Кросс-курс — RUB/ARS = USD/ARS / USD/RUB
package main

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

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
	if err := importCBR(db); err != nil {
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

	if err := importOfficialCrossRate(db, usdARS, bcraDate); err != nil {
		log.Fatalf("ERROR importing RUB/ARS cross-rate: %v", err)
	}

	log.Println("Done successfully")
}

func importOfficialCrossRate(db *sql.DB, usdARS float64, bcraDate string) error {
	date, err := time.Parse(time.DateOnly, bcraDate)
	if err != nil {
		return fmt.Errorf("invalid BCRA date: %w", err)
	}

	// BCRA and CBR publish independently. Use the CBR rate effective on the BCRA quote date.
	var usdRUB float64
	err = db.QueryRow(`
		SELECT rate FROM currency_rates
		WHERE code = 'USD/RUB' AND source = 'cbr' AND rate_date BETWEEN ? AND ? AND rate > 0
		ORDER BY rate_date DESC LIMIT 1
	`, date.AddDate(0, 0, -14).Format(time.DateOnly), bcraDate).Scan(&usdRUB)
	if errors.Is(err, sql.ErrNoRows) {
		log.Printf("Cross: no recent USD/RUB rate effective on %s", bcraDate)
		return nil
	}
	if err != nil {
		return err
	}

	rubARS := usdARS / usdRUB
	_, err = db.Exec(`
		INSERT INTO currency_rates (code, name, rate, source, rate_date)
		VALUES ('RUB/ARS', 'Аргентинский песо', ?, 'cross', ?)
		ON DUPLICATE KEY UPDATE rate = VALUES(rate), name = VALUES(name), created_at = CURRENT_TIMESTAMP
	`, rubARS, bcraDate)
	if err == nil {
		log.Printf("Cross: RUB/ARS = %.4f (date: %s)", rubARS, bcraDate)
	}
	return err
}
