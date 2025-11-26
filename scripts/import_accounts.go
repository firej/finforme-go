package main

import (
	"database/sql"
	"fmt"
	"log"

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

	// Получаем все счета из GnuCash
	rows, err := sourceDB.Query(`
		SELECT id, name, account_type, commodity_id, commodity_scu,
		       non_std_scu, parent_id, code, description, hidden, placeholder, user_id
		FROM accounts
		ORDER BY id
	`)
	if err != nil {
		log.Fatal("Ошибка чтения счетов из GnuCash:", err)
	}
	defer rows.Close()

	// Мапа для отслеживания соответствия старых и новых ID
	idMap := make(map[int64]int64)

	// Начинаем транзакцию
	tx, err := targetDB.Begin()
	if err != nil {
		log.Fatal("Ошибка начала транзакции:", err)
	}

	importedCount := 0
	skippedCount := 0

	for rows.Next() {
		var (
			id, commodityID, commoditySCU, nonStdSCU, userID int64
			name, accountType, code, description             string
			parentID                                         sql.NullInt64
			hidden, placeholder                              sql.NullInt64
		)

		err := rows.Scan(&id, &name, &accountType, &commodityID, &commoditySCU,
			&nonStdSCU, &parentID, &code, &description, &hidden, &placeholder, &userID)
		if err != nil {
			log.Printf("Ошибка чтения строки: %v", err)
			continue
		}

		// Проверяем, существует ли уже счет с таким именем для данного пользователя
		var existingID int64
		err = tx.QueryRow(`
			SELECT id FROM accounts
			WHERE user_id = ? AND name = ? AND account_type = ?
		`, userID, name, accountType).Scan(&existingID)

		if err == nil {
			// Счет уже существует
			idMap[id] = existingID
			skippedCount++
			log.Printf("Пропущен существующий счет: %s (ID: %d -> %d)", name, id, existingID)
			continue
		} else if err != sql.ErrNoRows {
			log.Printf("Ошибка проверки существования счета: %v", err)
			continue
		}

		// Определяем parent_id в новой базе
		var newParentID sql.NullInt64
		if parentID.Valid {
			if mappedParentID, ok := idMap[parentID.Int64]; ok {
				newParentID = sql.NullInt64{Int64: mappedParentID, Valid: true}
			} else {
				// Родительский счет еще не импортирован, пропускаем пока
				log.Printf("Предупреждение: родительский счет %d для '%s' еще не импортирован", parentID.Int64, name)
				newParentID = sql.NullInt64{Valid: false}
			}
		}

		// Вставляем счет в целевую базу
		result, err := tx.Exec(`
			INSERT INTO accounts (user_id, name, account_type, commodity_id, commodity_scu,
			                     non_std_scu, parent_id, code, description, hidden, placeholder)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, userID, name, accountType, commodityID, commoditySCU, nonStdSCU,
			newParentID, code, description, hidden, placeholder)

		if err != nil {
			log.Printf("Ошибка вставки счета '%s': %v", name, err)
			continue
		}

		newID, err := result.LastInsertId()
		if err != nil {
			log.Printf("Ошибка получения нового ID для '%s': %v", name, err)
			continue
		}

		// Сохраняем соответствие старого и нового ID
		idMap[id] = newID
		importedCount++
		log.Printf("Импортирован счет: %s (ID: %d -> %d)", name, id, newID)
	}

	if err = rows.Err(); err != nil {
		tx.Rollback()
		log.Fatal("Ошибка при чтении строк:", err)
	}

	// Второй проход: обновляем parent_id для счетов, у которых родители не были импортированы в первом проходе
	rows2, err := sourceDB.Query(`
		SELECT id, parent_id
		FROM accounts
		WHERE parent_id IS NOT NULL
		ORDER BY id
	`)
	if err != nil {
		tx.Rollback()
		log.Fatal("Ошибка второго прохода:", err)
	}
	defer rows2.Close()

	updatedCount := 0
	for rows2.Next() {
		var oldID int64
		var oldParentID sql.NullInt64

		if err := rows2.Scan(&oldID, &oldParentID); err != nil {
			log.Printf("Ошибка чтения во втором проходе: %v", err)
			continue
		}

		if !oldParentID.Valid {
			continue
		}

		newID, ok1 := idMap[oldID]
		newParentID, ok2 := idMap[oldParentID.Int64]

		if !ok1 || !ok2 {
			continue
		}

		// Проверяем, нужно ли обновить parent_id
		var currentParentID sql.NullInt64
		err := tx.QueryRow("SELECT parent_id FROM accounts WHERE id = ?", newID).Scan(&currentParentID)
		if err != nil {
			log.Printf("Ошибка проверки parent_id: %v", err)
			continue
		}

		// Обновляем только если parent_id не установлен или отличается
		if !currentParentID.Valid || currentParentID.Int64 != newParentID {
			_, err = tx.Exec("UPDATE accounts SET parent_id = ? WHERE id = ?", newParentID, newID)
			if err != nil {
				log.Printf("Ошибка обновления parent_id для счета ID %d: %v", newID, err)
				continue
			}
			updatedCount++
		}
	}

	// Фиксируем транзакцию
	if err = tx.Commit(); err != nil {
		log.Fatal("Ошибка фиксации транзакции:", err)
	}

	fmt.Println("\n=== Результаты импорта ===")
	fmt.Printf("Импортировано новых счетов: %d\n", importedCount)
	fmt.Printf("Пропущено существующих счетов: %d\n", skippedCount)
	fmt.Printf("Обновлено parent_id: %d\n", updatedCount)
	fmt.Printf("Всего счетов в GnuCash: %d\n", importedCount+skippedCount)
}
