package main

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func main() {
	// Открываем исходную базу GnuCash
	sourceDB, err := sql.Open("sqlite3", "mybase.gnucash.sqlite")
	if err != nil {
		log.Fatal("Ошибка открытия mybase.gnucash.sqlite:", err)
	}
	defer sourceDB.Close()

	// Открываем целевую базу finforme
	targetDB, err := sql.Open("sqlite3", "finforme.db")
	if err != nil {
		log.Fatal("Ошибка открытия finforme.db:", err)
	}
	defer targetDB.Close()

	// Создаем мапу соответствия старых ID счетов к новым
	accountMap := make(map[int64]int64)

	// Получаем соответствие по именам счетов
	rows, err := sourceDB.Query(`
		SELECT id, name, account_type
		FROM accounts
		ORDER BY id
	`)
	if err != nil {
		log.Fatal("Ошибка чтения счетов из GnuCash:", err)
	}

	for rows.Next() {
		var oldID int64
		var name, accountType string
		if err := rows.Scan(&oldID, &name, &accountType); err != nil {
			log.Printf("Ошибка чтения счета: %v", err)
			continue
		}

		// Ищем соответствующий счет в целевой базе
		var newID int64
		err := targetDB.QueryRow(`
			SELECT id FROM accounts
			WHERE user_id = 1 AND name = ? AND account_type = ?
		`, name, accountType).Scan(&newID)

		if err == nil {
			accountMap[oldID] = newID
		} else {
			log.Printf("Предупреждение: счет '%s' (ID: %d) не найден в целевой базе", name, oldID)
		}
	}
	rows.Close()

	log.Printf("Создана мапа соответствия для %d счетов", len(accountMap))

	// Получаем транзакции из GnuCash
	txRows, err := sourceDB.Query(`
		SELECT id, num, enter_date, description, currency_id, user_id, tags, post_date
		FROM transactions
		ORDER BY post_date, id
	`)
	if err != nil {
		log.Fatal("Ошибка чтения транзакций из GnuCash:", err)
	}
	defer txRows.Close()

	// Начинаем транзакцию
	tx, err := targetDB.Begin()
	if err != nil {
		log.Fatal("Ошибка начала транзакции:", err)
	}

	importedTxCount := 0
	skippedTxCount := 0
	importedSplitsCount := 0
	skippedSplitsCount := 0
	txMap := make(map[int64]int64) // старый ID транзакции -> новый ID

	for txRows.Next() {
		var oldTxID, currencyID, userID int64
		var num, description, tags string
		var enterDate, postDate time.Time

		err := txRows.Scan(&oldTxID, &num, &enterDate, &description, &currencyID, &userID, &tags, &postDate)
		if err != nil {
			log.Printf("Ошибка чтения транзакции: %v", err)
			continue
		}

		// Проверяем, существует ли уже транзакция с таким описанием и датой
		var existingTxID int64
		err = tx.QueryRow(`
			SELECT id FROM transactions
			WHERE user_id = ? AND description = ? AND post_date = ?
		`, userID, description, postDate).Scan(&existingTxID)

		if err == nil {
			// Транзакция уже существует
			txMap[oldTxID] = existingTxID
			skippedTxCount++
			continue
		} else if err != sql.ErrNoRows {
			log.Printf("Ошибка проверки существования транзакции: %v", err)
			continue
		}

		// Вставляем транзакцию
		result, err := tx.Exec(`
			INSERT INTO transactions (user_id, currency_id, num, post_date, enter_date, description, tags)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, userID, currencyID, num, postDate, enterDate, description, tags)

		if err != nil {
			log.Printf("Ошибка вставки транзакции '%s': %v", description, err)
			continue
		}

		newTxID, err := result.LastInsertId()
		if err != nil {
			log.Printf("Ошибка получения нового ID транзакции: %v", err)
			continue
		}

		txMap[oldTxID] = newTxID
		importedTxCount++

		if importedTxCount%100 == 0 {
			log.Printf("Импортировано транзакций: %d", importedTxCount)
		}
	}

	log.Printf("Завершен импорт транзакций. Импортировано: %d, пропущено: %d", importedTxCount, skippedTxCount)

	// Теперь импортируем splits
	splitRows, err := sourceDB.Query(`
		SELECT id, value_num, value_denom, account_id, tx_id, user_id
		FROM splits
		ORDER BY tx_id, id
	`)
	if err != nil {
		tx.Rollback()
		log.Fatal("Ошибка чтения splits из GnuCash:", err)
	}
	defer splitRows.Close()

	for splitRows.Next() {
		var oldSplitID, valueNum, valueDenom, oldAccountID, oldTxID, userID int64

		err := splitRows.Scan(&oldSplitID, &valueNum, &valueDenom, &oldAccountID, &oldTxID, &userID)
		if err != nil {
			log.Printf("Ошибка чтения split: %v", err)
			continue
		}

		// Проверяем, есть ли соответствующие транзакция и счет
		newTxID, txExists := txMap[oldTxID]
		newAccountID, accExists := accountMap[oldAccountID]

		if !txExists {
			skippedSplitsCount++
			continue
		}

		if !accExists {
			log.Printf("Предупреждение: счет ID %d не найден для split ID %d", oldAccountID, oldSplitID)
			skippedSplitsCount++
			continue
		}

		// Проверяем, существует ли уже такой split
		var existingSplitID int64
		err = tx.QueryRow(`
			SELECT id FROM splits
			WHERE user_id = ? AND tx_id = ? AND account_id = ? AND value_num = ? AND value_denom = ?
		`, userID, newTxID, newAccountID, valueNum, valueDenom).Scan(&existingSplitID)

		if err == nil {
			// Split уже существует
			skippedSplitsCount++
			continue
		} else if err != sql.ErrNoRows {
			log.Printf("Ошибка проверки существования split: %v", err)
			continue
		}

		// Вставляем split
		_, err = tx.Exec(`
			INSERT INTO splits (user_id, tx_id, account_id, value_num, value_denom)
			VALUES (?, ?, ?, ?, ?)
		`, userID, newTxID, newAccountID, valueNum, valueDenom)

		if err != nil {
			log.Printf("Ошибка вставки split для транзакции %d: %v", newTxID, err)
			continue
		}

		importedSplitsCount++

		if importedSplitsCount%500 == 0 {
			log.Printf("Импортировано splits: %d", importedSplitsCount)
		}
	}

	// Фиксируем транзакцию
	if err = tx.Commit(); err != nil {
		log.Fatal("Ошибка фиксации транзакции:", err)
	}

	fmt.Println("\n=== Результаты импорта транзакций ===")
	fmt.Printf("Импортировано новых транзакций: %d\n", importedTxCount)
	fmt.Printf("Пропущено существующих транзакций: %d\n", skippedTxCount)
	fmt.Printf("Импортировано новых splits: %d\n", importedSplitsCount)
	fmt.Printf("Пропущено splits: %d\n", skippedSplitsCount)
	fmt.Printf("Всего транзакций обработано: %d\n", importedTxCount+skippedTxCount)
}
