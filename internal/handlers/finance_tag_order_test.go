package handlers

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
)

func TestFinanceTransactionsByTagOrder(t *testing.T) {
	h := financeTestHandler(t)
	for _, tc := range []struct {
		name string
		date time.Time
	}{
		{"older", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)},
		{"same-day-first", time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)},
		{"same-day-second", time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)},
	} {
		in := validFinanceInput()
		in.Description, in.PostDate, in.Tags = tc.name, tc.date, "credit"
		if _, err := h.saveTransaction(2, in); err != nil {
			t.Fatal(err)
		}
	}
	cookie := authCookie(t, h, 2)
	for i := 0; i < 20; i++ {
		w := httptest.NewRecorder()
		h.FinanceTransactionsByTag(w, authRequest("GET", "/finance/tag?tag=credit", nil, cookie))
		body := w.Body.String()
		first, second, older := strings.Index(body, ">same-day-second</td>"), strings.Index(body, ">same-day-first</td>"), strings.Index(body, ">older</td>")
		if w.Code != 200 || first < 0 || second <= first || older <= second {
			t.Fatalf("unexpected order or status %d: positions %d, %d, %d", w.Code, first, second, older)
		}
	}
}

func TestFinanceTagTransactionLink(t *testing.T) {
	h := financeTestHandler(t)
	in := validFinanceInput()
	in.Description, in.Tags = "Tagged purchase", "credit"
	id, err := h.saveTransaction(2, in)
	if err != nil {
		t.Fatal(err)
	}
	router := mux.NewRouter()
	router.HandleFunc("/finance/tag", h.FinanceTransactionsByTag)
	router.HandleFunc("/finance/transaction/{account_id}/", h.FinanceTransaction)
	router.HandleFunc("/finance/transaction/{account_id}/{tx_id}", h.FinanceTransaction)
	cookie := authCookie(t, h, 2)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, authRequest("GET", "/finance/tag?tag=credit", nil, cookie))
	link := fmt.Sprintf("/finance/transaction/0/%d", id)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `href="`+link+`"`) {
		t.Fatalf("tag page missing transaction link: status %d", w.Code)
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, authRequest("GET", link, nil, cookie))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Tagged purchase") || !strings.Contains(w.Body.String(), `href="/finance/"`) || strings.Contains(w.Body.String(), "/finance/account/0") {
		t.Fatalf("transaction page status %d or incorrect content", w.Code)
	}
	for _, path := range []string{link, "/finance/transaction/0/999999", "/finance/transaction/0/"} {
		w = httptest.NewRecorder()
		router.ServeHTTP(w, authRequest("GET", path, nil, authCookie(t, h, 1)))
		if w.Code != 404 {
			t.Fatalf("%s: got status %d, want 404", path, w.Code)
		}
	}
}
