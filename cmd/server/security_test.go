package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCrossOriginProtection(t *testing.T) {
	handler := http.NewCrossOriginProtection().Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for _, tc := range []struct {
		name, origin, site string
		want               int
	}{
		{"same origin", "https://finfor.me", "same-origin", 204},
		{"foreign origin", "https://example.com", "cross-site", 403},
		{"sibling origin", "https://other.finfor.me", "same-site", 403},
		{"API without browser headers", "", "", 204},
		{"origin fallback blocked", "https://example.com", "", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "https://finfor.me/accounts/password_change/", nil)
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if tc.site != "" {
				r.Header.Set("Sec-Fetch-Site", tc.site)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status %d", w.Code)
			}
		})
	}
}
