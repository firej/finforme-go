package handlers

import (
	"database/sql"
	"fmt"
	"strings"
	"unicode/utf8"
)

type backupCommodity struct {
	Namespace   string `json:"namespace"`
	Mnemonic    string `json:"mnemonic"`
	Fullname    string `json:"fullname"`
	Cusip       string `json:"cusip"`
	Fraction    int    `json:"fraction"`
	QuoteSource string `json:"quote_source"`
	QuoteTZ     string `json:"quote_tz"`
	Sign        string `json:"sign"`
}

const backupCurrencyColumns = `COALESCE(namespace,'CURRENCY'),mnemonic,COALESCE(fullname,''),COALESCE(cusip,''),fraction,COALESCE(quote_source,''),COALESCE(quote_tz,''),COALESCE(sign,'')`

func scanBackupCommodity(rows *sql.Rows, c *backupCommodity) error {
	return rows.Scan(&c.Namespace, &c.Mnemonic, &c.Fullname, &c.Cusip, &c.Fraction, &c.QuoteSource, &c.QuoteTZ, &c.Sign)
}
func exportBackupCommodities(tx *sql.Tx, userID int64) ([]backupCommodity, error) {
	rows, err := tx.Query(`SELECT `+backupCurrencyColumns+` FROM commodities WHERE id IN (SELECT commodity_id FROM accounts WHERE user_id=?) ORDER BY mnemonic,id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []backupCommodity{}
	seen := map[string]backupCommodity{}
	for rows.Next() {
		var c backupCommodity
		if err := scanBackupCommodity(rows, &c); err != nil {
			return nil, err
		}
		if prior, ok := seen[c.Mnemonic]; ok {
			if prior != c {
				return nil, fmt.Errorf("неоднозначные свойства валюты %s", c.Mnemonic)
			}
			continue
		}
		seen[c.Mnemonic] = c
		result = append(result, c)
	}
	return result, rows.Err()
}
func validateBackupCommodities(data *backupData) (map[string]backupCommodity, error) {
	result := map[string]backupCommodity{}
	for _, c := range data.Commodities {
		c.Mnemonic = strings.ToUpper(strings.TrimSpace(c.Mnemonic))
		c.Namespace = currencyNamespace(c.Namespace)
		if c.Mnemonic == "" || c.Namespace != "CURRENCY" || c.Fraction <= 0 || int64(c.Fraction) > 2147483647 {
			return nil, validationError("Некорректные свойства валюты в резервной копии")
		}
		for _, v := range []string{c.Namespace, c.Mnemonic, c.Fullname, c.Cusip, c.QuoteSource, c.QuoteTZ} {
			if utf8.RuneCountInString(v) > 255 {
				return nil, validationError("Слишком длинное свойство валюты")
			}
		}
		if utf8.RuneCountInString(c.Sign) > 10 {
			return nil, validationError("Слишком длинный символ валюты")
		}
		if _, ok := result[c.Mnemonic]; ok {
			return nil, validationError("Повторное описание валюты в резервной копии")
		}
		result[c.Mnemonic] = c
	}
	for _, a := range data.Accounts {
		code := strings.ToUpper(strings.TrimSpace(a.Currency))
		if data.Version >= 2 {
			if _, ok := result[code]; !ok {
				return nil, validationError(fmt.Sprintf("Нет описания валюты счёта %s", a.Name))
			}
		}
	}
	return result, nil
}
func restoreBackupCommodities(tx *sql.Tx, data *backupData, metadata map[string]backupCommodity) (map[string]int64, int64, error) {
	// Same lock order as GnuCash import: user, then shared currency registry.
	if _, err := tx.Exec(`UPDATE commodities SET id=id WHERE id=1`); err != nil {
		return nil, 0, err
	}
	rows, err := tx.Query(`SELECT id,` + backupCurrencyColumns + ` FROM commodities ORDER BY id`)
	if err != nil {
		return nil, 0, err
	}
	ids := map[string]int64{}
	existing := map[string]backupCommodity{}
	var defaultID int64
	for rows.Next() {
		var id int64
		var c backupCommodity
		if err := rows.Scan(&id, &c.Namespace, &c.Mnemonic, &c.Fullname, &c.Cusip, &c.Fraction, &c.QuoteSource, &c.QuoteTZ, &c.Sign); err != nil {
			rows.Close()
			return nil, 0, err
		}
		if defaultID == 0 {
			defaultID = id
		}
		c.Namespace = currencyNamespace(c.Namespace)
		c.Mnemonic = strings.ToUpper(strings.TrimSpace(c.Mnemonic))
		if c.Namespace != "CURRENCY" {
			continue
		}
		if prior, ok := existing[c.Mnemonic]; ok && prior != c {
			rows.Close()
			return nil, 0, validationError("Неоднозначная валюта в справочнике: " + c.Mnemonic)
		}
		ids[c.Mnemonic] = id
		existing[c.Mnemonic] = c
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	for _, a := range data.Accounts {
		code := strings.ToUpper(strings.TrimSpace(a.Currency))
		c, hasMetadata := metadata[code]
		if code == "" {
			continue
		} // Legacy backups could omit the root currency.
		if ids[code] != 0 {
			if hasMetadata && existing[code] != c {
				return nil, 0, validationError("Свойства валюты " + code + " отличаются от справочника. Восстановление отменено, чтобы не изменить валюту других пользователей.")
			}
			continue
		}
		if !hasMetadata {
			c = backupCommodity{Namespace: "CURRENCY", Mnemonic: code, Fullname: code, Fraction: 100}
		}
		res, err := tx.Exec(`INSERT INTO commodities(namespace,mnemonic,fullname,cusip,fraction,quote_source,quote_tz,sign) VALUES(?,?,?,?,?,?,?,?)`, c.Namespace, c.Mnemonic, c.Fullname, c.Cusip, c.Fraction, c.QuoteSource, c.QuoteTZ, c.Sign)
		if err != nil {
			return nil, 0, err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return nil, 0, err
		}
		ids[code] = id
		existing[code] = c
		if defaultID == 0 {
			defaultID = id
		}
	}
	if defaultID == 0 {
		return nil, 0, validationError("В справочнике нет валют")
	}
	return ids, defaultID, nil
}
