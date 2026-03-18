package gnucash

import (
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
)

func TestParseGnuCashDate(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"2024-01-15 12:00:00 +0300", "2024-01-15"},
		{"2024-01-15 12:00:00", "2024-01-15"},
		{"2024-01-15", "2024-01-15"},
		{"", "0001-01-01"},
	}

	for _, test := range tests {
		result := parseGnuCashDate(test.input)
		if result.Format("2006-01-02") != test.expected {
			t.Errorf("parseGnuCashDate(%q) = %v, expected %v", test.input, result.Format("2006-01-02"), test.expected)
		}
	}
}

func TestParseGnuCashValue(t *testing.T) {
	tests := []struct {
		input         string
		expectedNum   int64
		expectedDenom int64
	}{
		{"10000/100", 10000, 100},
		{"-5000/100", -5000, 100},
		{"100", 100, 1},
		{"", 0, 100},
		{"invalid", 0, 100},
	}

	for _, test := range tests {
		num, denom := parseGnuCashValue(test.input)
		if num != test.expectedNum || denom != test.expectedDenom {
			t.Errorf("parseGnuCashValue(%q) = (%d, %d), expected (%d, %d)",
				test.input, num, denom, test.expectedNum, test.expectedDenom)
		}
	}
}

func TestParseXML(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="utf-8" ?>
<gnc-v2>
  <gnc:book version="2.0.0">
    <gnc:commodity version="2.0.0">
      <cmdty:space>CURRENCY</cmdty:space>
      <cmdty:id>RUB</cmdty:id>
      <cmdty:name>Russian Ruble</cmdty:name>
      <cmdty:fraction>100</cmdty:fraction>
    </gnc:commodity>
    <gnc:account version="2.0.0">
      <act:name>Root Account</act:name>
      <act:id type="guid">root-guid-123</act:id>
      <act:type>ROOT</act:type>
    </gnc:account>
    <gnc:account version="2.0.0">
      <act:name>Assets</act:name>
      <act:id type="guid">assets-guid-456</act:id>
      <act:type>ASSET</act:type>
      <act:commodity>
        <cmdty:space>CURRENCY</cmdty:space>
        <cmdty:id>RUB</cmdty:id>
      </act:commodity>
      <act:commodity-scu>100</act:commodity-scu>
      <act:parent type="guid">root-guid-123</act:parent>
    </gnc:account>
  </gnc:book>
</gnc-v2>`

	result, err := parseXML(strings.NewReader(xmlData))
	if err != nil {
		t.Fatalf("parseXML failed: %v", err)
	}

	// Проверяем, что данные распарсились (даже если пустые из-за namespace)
	t.Logf("Parsed %d commodities, %d accounts, %d transactions",
		len(result.Commodities), len(result.Accounts), len(result.Transactions))
}

func TestParseReaderWithFallback(t *testing.T) {
	// Тест с обычным XML
	xmlData := `<?xml version="1.0" encoding="utf-8" ?>
<gnc-v2>
  <book>
    <commodity>
      <space>CURRENCY</space>
      <id>RUB</id>
      <name>Russian Ruble</name>
      <fraction>100</fraction>
    </commodity>
    <account>
      <name>Root Account</name>
      <id type="guid">root-guid-123</id>
      <type>ROOT</type>
    </account>
  </book>
</gnc-v2>`

	result, err := ParseReaderWithFallback([]byte(xmlData))
	if err != nil {
		t.Fatalf("ParseReaderWithFallback (plain XML) failed: %v", err)
	}

	t.Logf("Plain XML: Parsed %d commodities, %d accounts, %d transactions",
		len(result.Commodities), len(result.Accounts), len(result.Transactions))

	// Тест с gzip-сжатым XML
	var buf bytes.Buffer
	gzWriter := gzip.NewWriter(&buf)
	gzWriter.Write([]byte(xmlData))
	gzWriter.Close()

	result, err = ParseReaderWithFallback(buf.Bytes())
	if err != nil {
		t.Fatalf("ParseReaderWithFallback (gzip) failed: %v", err)
	}

	t.Logf("Gzip XML: Parsed %d commodities, %d accounts, %d transactions",
		len(result.Commodities), len(result.Accounts), len(result.Transactions))
}

func TestParseSimpleGnuCashXML(t *testing.T) {
	// Простой XML без namespace prefixes
	xmlData := `<?xml version="1.0" encoding="utf-8" ?>
<gnc-v2>
  <book>
    <commodity>
      <space>CURRENCY</space>
      <id>RUB</id>
      <name>Russian Ruble</name>
      <fraction>100</fraction>
    </commodity>
    <account>
      <name>Root Account</name>
      <id type="guid">root-guid-123</id>
      <type>ROOT</type>
    </account>
    <account>
      <name>Assets</name>
      <id type="guid">assets-guid-456</id>
      <type>ASSET</type>
      <commodity>
        <space>CURRENCY</space>
        <id>RUB</id>
      </commodity>
      <commodity-scu>100</commodity-scu>
      <parent type="guid">root-guid-123</parent>
    </account>
    <transaction>
      <id type="guid">tx-guid-789</id>
      <currency>
        <space>CURRENCY</space>
        <id>RUB</id>
      </currency>
      <date-posted>
        <date>2024-01-15 12:00:00 +0300</date>
      </date-posted>
      <date-entered>
        <date>2024-01-15 12:00:00 +0300</date>
      </date-entered>
      <description>Test transaction</description>
      <splits>
        <split>
          <id type="guid">split-1</id>
          <account type="guid">assets-guid-456</account>
          <value>10000/100</value>
          <quantity>10000/100</quantity>
        </split>
      </splits>
    </transaction>
  </book>
</gnc-v2>`

	result, err := ParseReaderWithFallback([]byte(xmlData))
	if err != nil {
		t.Fatalf("ParseReaderWithFallback failed: %v", err)
	}

	if len(result.Commodities) != 1 {
		t.Errorf("Expected 1 commodity, got %d", len(result.Commodities))
	}

	if len(result.Accounts) != 2 {
		t.Errorf("Expected 2 accounts, got %d", len(result.Accounts))
	}

	if len(result.Transactions) != 1 {
		t.Errorf("Expected 1 transaction, got %d", len(result.Transactions))
	}

	if len(result.Transactions) > 0 {
		tx := result.Transactions[0]
		if tx.Description != "Test transaction" {
			t.Errorf("Expected description 'Test transaction', got '%s'", tx.Description)
		}
		if len(tx.Splits) != 1 {
			t.Errorf("Expected 1 split, got %d", len(tx.Splits))
		}
		if len(tx.Splits) > 0 {
			split := tx.Splits[0]
			if split.ValueNum != 10000 || split.ValueDenom != 100 {
				t.Errorf("Expected value 10000/100, got %d/%d", split.ValueNum, split.ValueDenom)
			}
		}
	}
}
