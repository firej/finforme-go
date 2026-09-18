package handlers

import (
	"github.com/gorilla/mux"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFormatMoney(t *testing.T) {
	for _, tc := range []struct {
		value float64
		want  string
	}{
		{0, "0,00"}, {-0.001, "0,00"}, {1234567.89, "1\u00a0234\u00a0567,89"}, {-1234.5, "−1\u00a0234,50"}, {999.999, "1\u00a0000,00"},
	} {
		if got := formatMoney(tc.value); got != tc.want {
			t.Errorf("%v: got %q, want %q", tc.value, got, tc.want)
		}
	}
}

func TestFinanceAccountViewShowsCurrentBalance(t *testing.T) {
	h := financeTestHandler(t)
	if _, err := h.saveTransaction(2, validFinanceInput()); err != nil {
		t.Fatal(err)
	}
	for _, order := range []string{"asc", "desc"} {
		r := authRequest("GET", "/finance/account/1?sort="+order, nil, authCookie(t, h, 2))
		r = mux.SetURLVars(r, map[string]string{"id": "1"})
		w := httptest.NewRecorder()
		h.FinanceAccountView(w, r)
		body := w.Body.String()
		if w.Code != 200 || !strings.Contains(body, `−100,00 <span class="stat-currency">RUB</span>`) {
			t.Fatalf("current balance missing for %s, status %d", order, w.Code)
		}
		if !strings.Contains(body, `data-date="2026-09-05"`) || !strings.Contains(body, `value="2026-09"`) {
			t.Fatal("period controls need ISO dates and available months")
		}
	}
}
