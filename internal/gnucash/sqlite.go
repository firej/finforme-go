package gnucash

import (
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
)

// ParseSQLite reads an existing database without modifying the source file.
func ParseSQLite(filename string) (*ParsedData, error) {
	path, err := filepath.Abs(filename)
	if err != nil {
		return nil, err
	}
	uri := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	source, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer source.Rollback()
	data := &ParsedData{}
	rows, err := source.Query(`SELECT guid,namespace,mnemonic,COALESCE(fullname,''),COALESCE(cusip,''),fraction,COALESCE(quote_source,''),COALESCE(quote_tz,'') FROM commodities`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c ParsedCommodity
		if err = rows.Scan(&c.GUID, &c.Space, &c.Mnemonic, &c.Fullname, &c.Cusip, &c.Fraction, &c.QuoteSource, &c.QuoteTZ); err != nil {
			rows.Close()
			return nil, err
		}
		data.Commodities = append(data.Commodities, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = source.Query(`SELECT guid,name,account_type,COALESCE(commodity_guid,''),COALESCE(commodity_scu,0),COALESCE(non_std_scu,0),COALESCE(parent_guid,''),COALESCE(code,''),COALESCE(description,''),COALESCE(hidden,0),COALESCE(placeholder,0) FROM accounts`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var a ParsedAccount
		if err = rows.Scan(&a.GUID, &a.Name, &a.AccountType, &a.CommodityRef, &a.CommoditySCU, &a.NonStdSCU, &a.ParentGUID, &a.Code, &a.Description, &a.Hidden, &a.Placeholder); err != nil {
			rows.Close()
			return nil, err
		}
		data.Accounts = append(data.Accounts, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = source.Query(`SELECT guid,currency_guid,COALESCE(num,''),post_date,enter_date,COALESCE(description,'') FROM transactions`)
	if err != nil {
		return nil, err
	}
	index := map[string]int{}
	for rows.Next() {
		var t ParsedTransaction
		var post, enter string
		if err = rows.Scan(&t.GUID, &t.CurrencyRef, &t.Num, &post, &enter, &t.Description); err != nil {
			rows.Close()
			return nil, err
		}
		t.PostDate = parseGnuCashDate(post)
		t.EnterDate = parseGnuCashDate(enter)
		if _, ok := index[t.GUID]; ok {
			rows.Close()
			return nil, fmt.Errorf("повторный идентификатор операции")
		}
		index[t.GUID] = len(data.Transactions)
		data.Transactions = append(data.Transactions, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = source.Query(`SELECT guid,tx_guid,account_guid,value_num,value_denom,quantity_num,quantity_denom,COALESCE(memo,''),COALESCE(action,'') FROM splits`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var s ParsedSplit
		var txID string
		if err = rows.Scan(&s.GUID, &txID, &s.AccountGUID, &s.ValueNum, &s.ValueDenom, &s.QuantityNum, &s.QuantityDenom, &s.Memo, &s.Action); err != nil {
			rows.Close()
			return nil, err
		}
		i, ok := index[txID]
		if !ok {
			rows.Close()
			return nil, fmt.Errorf("проводка ссылается на отсутствующую операцию %s", txID)
		}
		data.Transactions[i].Splits = append(data.Transactions[i].Splits, s)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return data, nil
}
