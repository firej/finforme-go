package main

import "testing"

func TestReplacementScope(t *testing.T) {
	for _, tc := range []struct {
		r    savedRate
		want bool
	}{
		{savedRate{Code: "USD/ARS", Source: "bcra", Date: "2023-06-01"}, true},
		{savedRate{Code: "RUB/ARS", Source: "cross", Date: "2025-08-31"}, true},
		{savedRate{Code: "USD/ARS", Source: "bcra", Date: "2023-05-31"}, false},
		{savedRate{Code: "USD/ARS", Source: "bcra", Date: "2025-09-01"}, false},
		{savedRate{Code: "USD/RUB", Source: "cbr", Date: "2024-01-01"}, false},
		{savedRate{Code: "USD/ARS", Source: "another", Date: "2024-01-01"}, false},
	} {
		if got := replacementTarget(tc.r, "2023-06-01", "2025-08-31"); got != tc.want {
			t.Fatalf("%+v: got %v", tc, got)
		}
	}
}
