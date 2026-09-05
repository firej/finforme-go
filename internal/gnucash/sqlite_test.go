package gnucash

import (
	"database/sql"
	_ "modernc.org/sqlite"
	"path/filepath"
	"testing"
)

func TestParseSQLitePreservesQuantityAndRejectsOrphans(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`CREATE TABLE commodities(guid,namespace,mnemonic,fullname,cusip,fraction,quote_source,quote_tz)`,
		`INSERT INTO commodities VALUES('c','CURRENCY','RUB','Ruble',NULL,100,NULL,NULL)`,
		`CREATE TABLE accounts(guid,name,account_type,commodity_guid,commodity_scu,non_std_scu,parent_guid,code,description,hidden,placeholder)`,
		`INSERT INTO accounts VALUES('a','Bank','BANK','c',100,0,NULL,NULL,NULL,0,0)`,
		`CREATE TABLE transactions(guid,currency_guid,num,post_date,enter_date,description)`,
		`INSERT INTO transactions VALUES('t','c','42','20260905120000','2026-09-05 12:00:00',NULL)`,
		`CREATE TABLE splits(guid,tx_guid,account_guid,value_num,value_denom,quantity_num,quantity_denom,memo,action)`,
		`INSERT INTO splits VALUES('s','t','a',10000,100,1,1,NULL,NULL)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	data, err := ParseSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	tx := data.Transactions[0]
	if tx.Description != "" || tx.PostDate.IsZero() || tx.EnterDate.IsZero() || tx.Num != "42" || tx.Splits[0].QuantityNum != 1 || tx.Splits[0].ValueNum != 10000 {
		t.Fatalf("lost source data: %+v", tx)
	}
	if _, err := db.Exec(`UPDATE splits SET tx_guid='missing'`); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseSQLite(path); err == nil {
		t.Fatal("orphan split accepted")
	}
}
