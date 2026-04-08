package main

import (
	"encoding/xml"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/transform"
)

func TestParseCBRXML(t *testing.T) {
	f, err := os.Open("testdata_cbr.xml")
	if err != nil {
		t.Fatalf("cannot open testdata_cbr.xml: %v", err)
	}
	defer f.Close()

	utf8Reader := transform.NewReader(f, charmap.Windows1251.NewDecoder())
	bodyBytes, err := io.ReadAll(utf8Reader)
	if err != nil {
		t.Fatal("read error:", err)
	}

	bodyStr := xmlDeclRe.ReplaceAllString(string(bodyBytes), "")

	var valCurs cbrValCurs
	if err := xml.Unmarshal([]byte(bodyStr), &valCurs); err != nil {
		t.Fatalf("xml.Unmarshal error: %v", err)
	}

	t.Logf("Parsed date: %s, valutes count: %d", valCurs.Date, len(valCurs.Valutes))

	wantedCodes := map[string]bool{"USD": true, "EUR": true}

	found := 0
	for _, v := range valCurs.Valutes {
		if !wantedCodes[v.CharCode] {
			continue
		}
		t.Logf("%s: VunitRate=%q Nominal=%q Name=%q", v.CharCode, v.VunitRate, v.Nominal, v.Name)

		valStr := strings.ReplaceAll(v.VunitRate, ",", ".")
		nomStr := strings.ReplaceAll(v.Nominal, ",", ".")
		val, err1 := strconv.ParseFloat(valStr, 64)
		nom, err2 := strconv.ParseFloat(nomStr, 64)
		if err1 != nil || err2 != nil || nom == 0 {
			t.Errorf("%s: parse error val=%v nom=%v", v.CharCode, err1, err2)
			continue
		}
		rate := val / nom
		t.Logf("%s/RUB = %.4f", v.CharCode, rate)
		if rate <= 0 {
			t.Errorf("%s: rate must be positive, got %f", v.CharCode, rate)
		}
		found++
	}

	if found != 2 {
		t.Errorf("expected 2 currencies (USD, EUR), got %d", found)
	}
}
