package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/evbogdanov/finforme/internal/models"
	"github.com/gorilla/mux"
)

func TestAdminCommodities(t *testing.T) {
	h := financeTestHandler(t)
	router := mux.NewRouter()
	router.HandleFunc("/admin/commodities/", h.RequireAdmin(h.AdminCommodities)).Methods("GET", "POST")
	protected := http.NewCrossOriginProtection().Handler(router)
	admin, user := authCookie(t, h, 1), authCookie(t, h, 2)
	form := url.Values{"mnemonic": {" ars "}, "fullname": {"Аргентинское песо"}, "sign": {"ARS"}, "decimals": {"2"}}
	request := func(method string, values url.Values, cookie *http.Cookie, origin string) *httptest.ResponseRecorder {
		t.Helper()
		r := authRequest(method, "/admin/commodities/", values, cookie)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		protected.ServeHTTP(w, r)
		return w
	}
	for _, method := range []string{"GET", "POST"} {
		if w := request(method, form, nil, ""); w.Code != http.StatusSeeOther && w.Code != http.StatusFound {
			t.Fatalf("anonymous %s: %d", method, w.Code)
		}
		if w := request(method, form, user, ""); w.Code != http.StatusForbidden {
			t.Fatalf("non-admin %s: %d", method, w.Code)
		}
	}
	if w := request("POST", form, admin, "https://evil.example"); w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin: %d", w.Code)
	}
	if w := request("POST", form, admin, ""); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/commodities/?msg=currency_created" {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var c models.Commodity
	if err := h.db.QueryRow(`SELECT namespace,mnemonic,fullname,fraction,sign FROM commodities WHERE mnemonic='ARS'`).Scan(&c.Namespace, &c.Mnemonic, &c.Fullname, &c.Fraction, &c.Sign); err != nil {
		t.Fatal(err)
	}
	if c.Namespace != "CURRENCY" || c.Fraction != 100 || c.Fullname != "Аргентинское песо" || c.Sign != "ARS" {
		t.Fatalf("stored: %+v", c)
	}
	items, err := h.getCommodities()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range items {
		found = found || item.Mnemonic == "ARS"
	}
	if !found {
		t.Fatal("new currency missing from account/MCP currency list")
	}
	if w := request("GET", nil, admin, ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Аргентинское песо") {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	if w := request("POST", form, admin, ""); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "уже существует") {
		t.Fatalf("duplicate: %d %s", w.Code, w.Body.String())
	}
	for _, tc := range []struct{ field, value string }{
		{"mnemonic", "A"}, {"mnemonic", "ARS/USD"}, {"mnemonic", "ПЕСО"}, {"mnemonic", strings.Repeat("A", 13)},
		{"fullname", " "}, {"fullname", strings.Repeat("я", 256)}, {"sign", ""}, {"sign", strings.Repeat("a", 11)},
		{"decimals", "9"}, {"decimals", "-1"}, {"decimals", "oops"}, {"decimals", ""},
	} {
		values := url.Values{"mnemonic": {"USDT"}, "fullname": {"Tether"}, "sign": {"USDT"}, "decimals": {"6"}}
		values.Set(tc.field, tc.value)
		if w := request("POST", values, admin, ""); w.Code != http.StatusBadRequest {
			t.Fatalf("invalid %s=%q: %d", tc.field, tc.value, w.Code)
		}
	}
	var n int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM commodities WHERE mnemonic='USDT'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("invalid request inserted a currency")
	}
	if err := h.createAdminCommodity(models.Commodity{Mnemonic: "USDT", Fullname: "Tether", Sign: "USDT", Fraction: 1000000}); err != nil {
		t.Fatal(err)
	}
	if err := h.createAdminCommodity(models.Commodity{Mnemonic: "JPY", Fullname: "Yen", Sign: "JPY", Fraction: 3}); err == nil {
		t.Fatal("accepted invalid fraction")
	}
}

func TestAdminCommodityConcurrentDuplicate(t *testing.T) {
	h := financeTestHandler(t)
	c := models.Commodity{Mnemonic: "ARS", Fullname: "Peso", Sign: "ARS", Fraction: 100}
	results := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- h.createAdminCommodity(c)
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if err.Error() != "Валюта с таким кодом уже существует" {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("created %d times", successes)
	}
}
