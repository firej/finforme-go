package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func TestBlueImportMariaDB(t *testing.T) {
	dsn := os.Getenv("FINFORME_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("requires isolated MariaDB")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := fmt.Sprintf("finforme_blue_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec("DROP DATABASE " + name)
	cfg.DBName = name
	t.Setenv("DATABASE_DSN", cfg.FormatDSN())
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`CREATE TABLE currency_rates(code VARCHAR(20),name VARCHAR(255),rate DECIMAL(18,6),source VARCHAR(50),rate_date DATE,PRIMARY KEY(code,source,rate_date))`,
		`INSERT INTO currency_rates VALUES('USD/RUB','USD',80,'cbr','2023-05-31'),('USD/ARS','Official',250,'bcra','2023-06-01'),('USD/ARS','Blue',490,'bluedollar_sell','2023-06-01'),('RUB/ARS','Cross',6.125,'cross_blue_sell','2023-06-01')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	html := filepath.Join(t.TempDir(), "blue.html")
	if err := os.WriteFile(html, []byte(`var chartData=[{"date":"2023-06-01","value":"495"},{"date":"2023-06-02","value":"500"}];`), 0600); err != nil {
		t.Fatal(err)
	}
	oldFlags, oldArgs := flag.CommandLine, os.Args
	defer func() { flag.CommandLine = oldFlags; os.Args = oldArgs }()
	execute := func(extra ...string) error {
		flag.CommandLine = flag.NewFlagSet("blue-test", flag.ContinueOnError)
		os.Args = append([]string{"blue-test", "-from", "2023-06-01", "-to", "2023-06-02", "-html", html}, extra...)
		return run()
	}
	check := func(code, source, date, want string) {
		t.Helper()
		var value string
		if err := db.QueryRow(`SELECT CAST(rate AS CHAR) FROM currency_rates WHERE code=? AND source=? AND rate_date=?`, code, source, date).Scan(&value); err != nil || value != want {
			t.Fatalf("%s %s %s: %s %v", code, source, date, value, err)
		}
	}
	if err := execute(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM currency_rates`).Scan(&count); err != nil || count != 4 {
		t.Fatal("preview wrote data", count, err)
	}
	if err := execute("-apply", "-missing-only"); err != nil {
		t.Fatal(err)
	}
	check("USD/ARS", "bluedollar_sell", "2023-06-01", "490.000000")
	check("RUB/ARS", "cross_blue_sell", "2023-06-01", "6.125000")
	check("USD/ARS", "bluedollar_sell", "2023-06-02", "500.000000")
	check("RUB/ARS", "cross_blue_sell", "2023-06-02", "6.250000")
	if err := execute("-apply"); err != nil {
		t.Fatal(err)
	}
	check("USD/ARS", "bluedollar_sell", "2023-06-01", "495.000000")
	check("RUB/ARS", "cross_blue_sell", "2023-06-01", "6.187500")
	check("USD/ARS", "bcra", "2023-06-01", "250.000000")
	if _, err := db.Exec(`CREATE TRIGGER reject_cross BEFORE INSERT ON currency_rates FOR EACH ROW BEGIN IF NEW.source='cross_blue_sell' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='test rollback'; END IF; END`); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(html, []byte(`var chartData=[{"date":"2023-06-01","value":"600"},{"date":"2023-06-02","value":"600"}];`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := execute("-apply"); err == nil {
		t.Fatal("cross failure did not abort import")
	}
	check("USD/ARS", "bluedollar_sell", "2023-06-01", "495.000000")
	check("USD/ARS", "bluedollar_sell", "2023-06-02", "500.000000")
}
