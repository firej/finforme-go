package amount

import "testing"

func TestExactAmounts(t *testing.T) {
	for _, c := range []struct {
		s    string
		d, n int64
	}{{"0.00000001", 100000000, 1}, {"0.29", 100, 29}, {"1e-8", 100000000, 1}, {"-1.23456789", 100000000, -123456789}, {"42", 1, 42}} {
		got, err := Parse(c.s, c.d)
		if err != nil || got != c.n {
			t.Fatalf("%+v: %d %v", c, got, err)
		}
	}
	for _, s := range []string{"1.001", "NaN", "Inf", "1/2", "90071992547410", "1e999", ""} {
		if _, err := Parse(s, 100); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
	n, d, err := Normalize(1, 100000000)
	if err != nil || n != 1 || d != 100000000 {
		t.Fatal(n, d, err)
	}
	if _, _, err := Normalize(1, 3); err == nil {
		t.Fatal("rounded repeating fraction")
	}
}

func TestNormalizeLargeWholeCurrency(t *testing.T) {
	n, d, err := Normalize(MaxUnits, 1)
	if err != nil || n != MaxUnits || d != 1 {
		t.Fatal(n, d, err)
	}
}
