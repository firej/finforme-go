package handlers

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
