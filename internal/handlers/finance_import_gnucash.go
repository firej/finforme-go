package handlers

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/evbogdanov/finforme/internal/gnucash"
)

func currencyNamespace(s string) string {
	if s == "ISO4217" {
		return "CURRENCY"
	}
	return s
}

// Both source formats share one transaction and the same validation rules.
func (h *Handler) importFromGnuCashXML(userID int64, data *gnucash.ParsedData) error {
	if data == nil || len(data.Accounts) == 0 {
		return fmt.Errorf("файл не содержит счетов")
	}
	accounts := map[string]gnucash.ParsedAccount{}
	for _, a := range data.Accounts {
		if a.GUID == "" {
			return fmt.Errorf("счёт без идентификатора")
		}
		if _, ok := accounts[a.GUID]; ok {
			return fmt.Errorf("повторный идентификатор счёта %s", a.GUID)
		}
		accounts[a.GUID] = a
	}
	seenTx, seenSplit := map[string]bool{}, map[string]bool{}
	for _, t := range data.Transactions {
		if t.GUID == "" || seenTx[t.GUID] {
			return fmt.Errorf("пустой или повторный идентификатор операции")
		}
		seenTx[t.GUID] = true
		if t.PostDate.IsZero() || t.EnterDate.IsZero() || t.PostDate.Year() < 1000 || t.EnterDate.Year() < 1000 || t.PostDate.Year() > 9999 || t.EnterDate.Year() > 9999 {
			return fmt.Errorf("операция %s: неверная дата", t.GUID)
		}
		if len(t.Splits) < 2 {
			return fmt.Errorf("операция %s содержит менее двух проводок", t.GUID)
		}
		total := new(big.Rat)
		for _, s := range t.Splits {
			if s.GUID == "" || seenSplit[s.GUID] {
				return fmt.Errorf("пустой или повторный идентификатор проводки")
			}
			seenSplit[s.GUID] = true
			a, ok := accounts[s.AccountGUID]
			if !ok || a.AccountType == "ROOT" || a.Placeholder {
				return fmt.Errorf("проводка %s: отсутствующий счёт или счёт-контейнер", s.GUID)
			}
			if s.ValueDenom <= 0 {
				return fmt.Errorf("проводка %s: неверный знаменатель value", s.GUID)
			}
			if _, err := gnucash.Cents(s.QuantityNum, s.QuantityDenom); err != nil {
				return fmt.Errorf("проводка %s: %w", s.GUID, err)
			}
			total.Add(total, new(big.Rat).SetFrac(big.NewInt(s.ValueNum), big.NewInt(s.ValueDenom)))
		}
		if total.Sign() != 0 {
			return fmt.Errorf("операция %s не сбалансирована в исходной валюте", t.GUID)
		}
	}
	tx, err := h.beginFinanceWrite(userID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Commodities are shared by all users. Serialize discovery/creation using
	// the seeded commodity row, after taking the user's finance lock.
	if _, err := tx.Exec(`UPDATE commodities SET id=id WHERE id=1`); err != nil {
		return err
	}
	// No second connection while holding the user's write lock.
	commodities := map[string]int64{}
	rows, err := tx.Query(`SELECT id,COALESCE(namespace,'CURRENCY'),mnemonic FROM commodities`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		var ns, code string
		if err = rows.Scan(&id, &ns, &code); err != nil {
			rows.Close()
			return err
		}
		commodities[currencyNamespace(ns)+":"+code] = id
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	refs := map[string]int64{}
	for k, v := range commodities {
		refs[k] = v
		if strings.HasPrefix(k, "CURRENCY:") {
			refs["ISO4217:"+strings.TrimPrefix(k, "CURRENCY:")] = v
		}
	}
	seenCommodity := map[string]bool{}
	for _, c := range data.Commodities {
		if c.GUID == "" || seenCommodity[c.GUID] || c.Mnemonic == "" {
			return fmt.Errorf("неверный или повторный идентификатор валюты")
		}
		seenCommodity[c.GUID] = true
		ns := currencyNamespace(c.Space)
		if ns != "CURRENCY" {
			return fmt.Errorf("импорт ценных бумаг и товаров пока не поддерживается: %s", c.Mnemonic)
		}
		key := ns + ":" + c.Mnemonic
		id := commodities[key]
		if id == 0 {
			if c.Fraction <= 0 {
				return fmt.Errorf("валюта %s: отсутствует точность", c.Mnemonic)
			}
			result, err := tx.Exec(`INSERT INTO commodities(namespace,mnemonic,fullname,cusip,fraction,quote_source,quote_tz,sign) VALUES(?,?,?,?,?,?,?,?)`, ns, c.Mnemonic, c.Fullname, c.Cusip, c.Fraction, c.QuoteSource, c.QuoteTZ, c.Mnemonic)
			if err != nil {
				return err
			}
			id, err = result.LastInsertId()
			if err != nil {
				return err
			}
			commodities[key] = id
		}
		refs[c.GUID] = id
		refs[key] = id
		refs["ISO4217:"+c.Mnemonic] = id
	}
	accountIDs := map[string]int64{}
	accountCurrencies := map[string]int64{}
	remaining := append([]gnucash.ParsedAccount(nil), data.Accounts...)
	for len(remaining) > 0 {
		next := make([]gnucash.ParsedAccount, 0)
		for _, a := range remaining {
			if a.ParentGUID != "" && accountIDs[a.ParentGUID] == 0 {
				next = append(next, a)
				continue
			}
			var currency, parent interface{}
			id := refs[a.CommodityRef]
			if id == 0 && a.AccountType != "ROOT" {
				return fmt.Errorf("счёт %s: неизвестная валюта %s", a.GUID, a.CommodityRef)
			}
			// ROOT has no monetary balance, but existing account readers require a currency ID.
			if id == 0 && a.AccountType == "ROOT" {
				for _, known := range commodities {
					if id == 0 || known < id {
						id = known
					}
				}
			}
			if id == 0 {
				return fmt.Errorf("не найдена валюта для дерева счетов")
			}
			currency = id
			if a.ParentGUID != "" {
				parent = accountIDs[a.ParentGUID]
			}
			scu := a.CommoditySCU
			if scu == 0 {
				scu = 100
			}
			result, err := tx.Exec(`INSERT INTO accounts(user_id,name,account_type,commodity_id,commodity_scu,non_std_scu,parent_id,code,description,hidden,placeholder) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, userID, a.Name, a.AccountType, currency, scu, a.NonStdSCU, parent, a.Code, a.Description, a.Hidden, a.Placeholder)
			if err != nil {
				return err
			}
			newID, err := result.LastInsertId()
			if err != nil {
				return err
			}
			accountIDs[a.GUID] = newID
			accountCurrencies[a.GUID] = id
		}
		if len(next) == len(remaining) {
			return fmt.Errorf("не найдены родители счетов или обнаружен цикл: %d счетов", len(next))
		}
		remaining = next
	}
	for _, t := range data.Transactions {
		currency := refs[t.CurrencyRef]
		if currency == 0 {
			return fmt.Errorf("операция %s: неизвестная валюта", t.GUID)
		}
		result, err := tx.Exec(`INSERT INTO transactions(user_id,num,post_date,enter_date,description,tags) VALUES(?,?,?,?,?,?)`, userID, t.Num, t.PostDate, t.EnterDate, t.Description, "")
		if err != nil {
			return err
		}
		id, err := result.LastInsertId()
		if err != nil {
			return err
		}
		for _, s := range t.Splits {
			if accountCurrencies[s.AccountGUID] == currency {
				v := new(big.Rat).SetFrac(big.NewInt(s.ValueNum), big.NewInt(s.ValueDenom))
				q := new(big.Rat).SetFrac(big.NewInt(s.QuantityNum), big.NewInt(s.QuantityDenom))
				if v.Cmp(q) != 0 {
					return fmt.Errorf("проводка %s: value и quantity различаются при одинаковой валюте", s.GUID)
				}
			}
			cents, err := gnucash.Cents(s.QuantityNum, s.QuantityDenom)
			if err != nil {
				return err
			}
			if _, err = tx.Exec(`INSERT INTO splits(user_id,tx_id,account_id,value_num,value_denom) VALUES(?,?,?,?,100)`, userID, id, accountIDs[s.AccountGUID], cents); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
