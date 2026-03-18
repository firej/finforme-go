package gnucash

import (
	"compress/gzip"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// GnuCashBook представляет корневой элемент GnuCash XML
type GnuCashBook struct {
	XMLName      xml.Name         `xml:"gnc-v2"`
	CountData    []CountData      `xml:"count-data"`
	Book         Book             `xml:"book"`
	Commodities  []XMLCommodity   `xml:"commodity"`
	Accounts     []XMLAccount     `xml:"account"`
	Transactions []XMLTransaction `xml:"transaction"`
}

// Book представляет книгу GnuCash
type Book struct {
	XMLName      xml.Name         `xml:"book"`
	ID           XMLGUID          `xml:"id"`
	Commodities  []XMLCommodity   `xml:"commodity"`
	Accounts     []XMLAccount     `xml:"account"`
	Transactions []XMLTransaction `xml:"transaction"`
}

// CountData содержит количество элементов
type CountData struct {
	Type  string `xml:"type,attr"`
	Count int    `xml:",chardata"`
}

// XMLGUID представляет GUID в GnuCash
type XMLGUID struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

// XMLCommodity представляет валюту/товар в GnuCash XML
type XMLCommodity struct {
	XMLName     xml.Name `xml:"commodity"`
	Space       string   `xml:"space"`
	ID          string   `xml:"id"`
	Name        string   `xml:"name"`
	XCode       string   `xml:"xcode"`
	Fraction    int      `xml:"fraction"`
	GetQuotes   string   `xml:"get_quotes"`
	QuoteSource string   `xml:"quote_source"`
	QuoteTZ     string   `xml:"quote_tz"`
}

// XMLAccount представляет счет в GnuCash XML
type XMLAccount struct {
	XMLName      xml.Name        `xml:"account"`
	Version      string          `xml:"version,attr"`
	Name         string          `xml:"name"`
	ID           XMLGUID         `xml:"id"`
	Type         string          `xml:"type"`
	Commodity    XMLCommodityRef `xml:"commodity"`
	CommoditySCU int             `xml:"commodity-scu"`
	NonStdSCU    int             `xml:"non-std-scu"`
	Code         string          `xml:"code"`
	Description  string          `xml:"description"`
	Slots        XMLSlots        `xml:"slots"`
	Parent       XMLGUID         `xml:"parent"`
}

// XMLCommodityRef представляет ссылку на валюту
type XMLCommodityRef struct {
	Space string `xml:"space"`
	ID    string `xml:"id"`
}

// XMLSlots представляет слоты (дополнительные данные)
type XMLSlots struct {
	Slot []XMLSlot `xml:"slot"`
}

// XMLSlot представляет один слот
type XMLSlot struct {
	Key   string       `xml:"key"`
	Value XMLSlotValue `xml:"value"`
}

// XMLSlotValue представляет значение слота
type XMLSlotValue struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

// XMLTransaction представляет транзакцию в GnuCash XML
type XMLTransaction struct {
	XMLName     xml.Name        `xml:"transaction"`
	Version     string          `xml:"version,attr"`
	ID          XMLGUID         `xml:"id"`
	Currency    XMLCommodityRef `xml:"currency"`
	Num         string          `xml:"num"`
	DatePosted  XMLDate         `xml:"date-posted"`
	DateEntered XMLDate         `xml:"date-entered"`
	Description string          `xml:"description"`
	Slots       XMLSlots        `xml:"slots"`
	Splits      XMLSplits       `xml:"splits"`
}

// XMLDate представляет дату в GnuCash XML
type XMLDate struct {
	Date string `xml:"date"`
}

// XMLSplits представляет список сплитов
type XMLSplits struct {
	Split []XMLSplit `xml:"split"`
}

// XMLSplit представляет сплит (часть транзакции)
type XMLSplit struct {
	ID              XMLGUID `xml:"id"`
	ReconciledState string  `xml:"reconciled-state"`
	ReconciledDate  XMLDate `xml:"reconcile-date"`
	Value           string  `xml:"value"`
	Quantity        string  `xml:"quantity"`
	Account         XMLGUID `xml:"account"`
	Memo            string  `xml:"memo"`
	Action          string  `xml:"action"`
}

// ParsedData содержит распарсенные данные из GnuCash
type ParsedData struct {
	Commodities  []ParsedCommodity
	Accounts     []ParsedAccount
	Transactions []ParsedTransaction
}

// ParsedCommodity представляет распарсенную валюту
type ParsedCommodity struct {
	GUID        string
	Space       string
	Mnemonic    string
	Fullname    string
	Fraction    int
	QuoteSource string
	QuoteTZ     string
}

// ParsedAccount представляет распарсенный счет
type ParsedAccount struct {
	GUID         string
	Name         string
	AccountType  string
	CommodityRef string // Space:ID
	CommoditySCU int
	NonStdSCU    int
	ParentGUID   string
	Code         string
	Description  string
	Hidden       bool
	Placeholder  bool
}

// ParsedTransaction представляет распарсенную транзакцию
type ParsedTransaction struct {
	GUID        string
	CurrencyRef string // Space:ID
	Num         string
	PostDate    time.Time
	EnterDate   time.Time
	Description string
	Splits      []ParsedSplit
}

// ParsedSplit представляет распарсенный сплит
type ParsedSplit struct {
	GUID        string
	AccountGUID string
	ValueNum    int64
	ValueDenom  int64
	Memo        string
	Action      string
}

// ParseFile парсит .gnucash файл (сжатый gzip XML)
func ParseFile(filename string) (*ParsedData, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	return ParseReader(file)
}

// ParseReader парсит данные из io.Reader
func ParseReader(r io.Reader) (*ParsedData, error) {
	// Пробуем распаковать как gzip
	gzReader, err := gzip.NewReader(r)
	if err != nil {
		// Если не gzip, пробуем как обычный XML
		// Нужно перечитать файл
		return nil, fmt.Errorf("file is not gzip compressed: %w", err)
	}
	defer gzReader.Close()

	return parseXML(gzReader)
}

// ParseReaderWithFallback парсит данные, пробуя сначала gzip, потом обычный XML
func ParseReaderWithFallback(data []byte) (*ParsedData, error) {
	// Пробуем как gzip
	gzReader, err := gzip.NewReader(strings.NewReader(string(data)))
	if err == nil {
		defer gzReader.Close()
		return parseXML(gzReader)
	}

	// Пробуем как обычный XML
	return parseXML(strings.NewReader(string(data)))
}

// parseXML парсит XML данные
func parseXML(r io.Reader) (*ParsedData, error) {
	var gnucash GnuCashBook
	decoder := xml.NewDecoder(r)

	if err := decoder.Decode(&gnucash); err != nil {
		return nil, fmt.Errorf("failed to decode XML: %w", err)
	}

	result := &ParsedData{
		Commodities:  make([]ParsedCommodity, 0),
		Accounts:     make([]ParsedAccount, 0),
		Transactions: make([]ParsedTransaction, 0),
	}

	// Собираем данные из корневого уровня и из Book
	allCommodities := append(gnucash.Commodities, gnucash.Book.Commodities...)
	allAccounts := append(gnucash.Accounts, gnucash.Book.Accounts...)
	allTransactions := append(gnucash.Transactions, gnucash.Book.Transactions...)

	// Парсим валюты
	for _, c := range allCommodities {
		result.Commodities = append(result.Commodities, ParsedCommodity{
			GUID:        c.Space + ":" + c.ID,
			Space:       c.Space,
			Mnemonic:    c.ID,
			Fullname:    c.Name,
			Fraction:    c.Fraction,
			QuoteSource: c.QuoteSource,
			QuoteTZ:     c.QuoteTZ,
		})
	}

	// Парсим счета
	for _, a := range allAccounts {
		hidden := false
		placeholder := false

		// Проверяем слоты на hidden и placeholder
		for _, slot := range a.Slots.Slot {
			if slot.Key == "hidden" && slot.Value.Value == "true" {
				hidden = true
			}
			if slot.Key == "placeholder" && slot.Value.Value == "true" {
				placeholder = true
			}
		}

		result.Accounts = append(result.Accounts, ParsedAccount{
			GUID:         a.ID.Value,
			Name:         a.Name,
			AccountType:  a.Type,
			CommodityRef: a.Commodity.Space + ":" + a.Commodity.ID,
			CommoditySCU: a.CommoditySCU,
			NonStdSCU:    a.NonStdSCU,
			ParentGUID:   a.Parent.Value,
			Code:         a.Code,
			Description:  a.Description,
			Hidden:       hidden,
			Placeholder:  placeholder,
		})
	}

	// Парсим транзакции
	for _, t := range allTransactions {
		postDate := parseGnuCashDate(t.DatePosted.Date)
		enterDate := parseGnuCashDate(t.DateEntered.Date)

		parsedTx := ParsedTransaction{
			GUID:        t.ID.Value,
			CurrencyRef: t.Currency.Space + ":" + t.Currency.ID,
			Num:         t.Num,
			PostDate:    postDate,
			EnterDate:   enterDate,
			Description: t.Description,
			Splits:      make([]ParsedSplit, 0),
		}

		// Парсим сплиты
		for _, s := range t.Splits.Split {
			valueNum, valueDenom := parseGnuCashValue(s.Value)

			parsedTx.Splits = append(parsedTx.Splits, ParsedSplit{
				GUID:        s.ID.Value,
				AccountGUID: s.Account.Value,
				ValueNum:    valueNum,
				ValueDenom:  valueDenom,
				Memo:        s.Memo,
				Action:      s.Action,
			})
		}

		result.Transactions = append(result.Transactions, parsedTx)
	}

	return result, nil
}

// parseGnuCashDate парсит дату в формате GnuCash
// Формат: "2024-01-15 12:00:00 +0300" или "2024-01-15"
func parseGnuCashDate(dateStr string) time.Time {
	dateStr = strings.TrimSpace(dateStr)
	if dateStr == "" {
		return time.Time{}
	}

	// Пробуем разные форматы
	formats := []string{
		"2006-01-02 15:04:05 -0700",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, dateStr); err == nil {
			return t
		}
	}

	return time.Time{}
}

// parseGnuCashValue парсит значение в формате GnuCash (например, "10000/100")
func parseGnuCashValue(valueStr string) (int64, int64) {
	valueStr = strings.TrimSpace(valueStr)
	if valueStr == "" {
		return 0, 100
	}

	parts := strings.Split(valueStr, "/")
	if len(parts) != 2 {
		// Пробуем как целое число
		if num, err := strconv.ParseInt(valueStr, 10, 64); err == nil {
			return num, 1
		}
		return 0, 100
	}

	num, err1 := strconv.ParseInt(parts[0], 10, 64)
	denom, err2 := strconv.ParseInt(parts[1], 10, 64)

	if err1 != nil || err2 != nil {
		return 0, 100
	}

	if denom == 0 {
		denom = 100
	}

	return num, denom
}
