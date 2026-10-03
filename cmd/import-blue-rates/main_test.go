package main

import "testing"

func TestImportStart(t *testing.T) {
	for _, tc := range []struct {
		from, to string
		days     int
		want     string
	}{
		{"2023-06-01", "2026-10-03", 14, "2026-09-20"},
		{"2023-06-01", "2024-03-01", 3, "2024-02-28"},
		{"2023-06-01", "2026-01-02", 4, "2025-12-30"},
		{"2023-06-01", "2023-06-02", 14, "2023-06-01"},
		{"2025-09-01", "2026-10-03", 0, "2025-09-01"},
		{"2023-06-01", "2026-10-03", 1, "2026-10-03"},
	} {
		got, err := importStart(tc.from, tc.to, tc.days)
		if err != nil || got != tc.want {
			t.Fatalf("%+v: %s %v", tc, got, err)
		}
	}
	for _, tc := range []struct {
		from, to string
		days     int
	}{
		{"2023-06-01", "bad", 14}, {"2023-06-01", "2026-10-03", -1},
		{"2023-06-01", "2026-10-03", 367}, {"2023-06-01", "2023-05-31", 14},
	} {
		if _, err := importStart(tc.from, tc.to, tc.days); err == nil {
			t.Fatalf("accepted invalid window: %+v", tc)
		}
	}
}
