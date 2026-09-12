package exchangerates

import (
	"strings"
	"testing"
)

func TestParseBlueHTML(t *testing.T) {
	body := []byte(`<script>var chartData = [{"date":"2023-05-31","value":"490"},{"date":"2023-06-02","value":"495"},{"date":"2023-06-01","value":"490"},{"date":"2023-06-02","value":"495.0"}]; doNotExecute();</script>`)
	p, err := ParseBlueHTML(body, FirstDate, "2023-06-04")
	if err != nil || len(p) != 2 || p[0] != (Point{FirstDate, "490.000000"}) || p[1].Rate != "495.000000" {
		t.Fatalf("points=%v err=%v", p, err)
	}
}

func TestParseBlueRejectsInvalidSource(t *testing.T) {
	for _, body := range []string{
		`nothing`,
		`var chartData=[]; var chartData=[];`,
		`var chartData=alert(1);`,
		`var chartData=[];`,
		`var chartData=[{"date":"2023-06-01","value":"490"},{"date":"2023-06-01","value":"491"}];`,
		`var chartData=[{"date":"2023-06-01","value":"0"}];`,
		`var chartData=[{"date":"2023-06-01","value":"NaN"}];`,
		`var chartData=[{"date":"2023-06-99","value":"490"}];`,
		strings.Repeat("x", (8<<20)+1),
	} {
		if _, err := ParseBlueHTML([]byte(body), FirstDate, "2023-06-04"); err == nil {
			t.Fatal("expected invalid source error")
		}
	}
}

func TestQuotesIndependentPriorDates(t *testing.T) {
	blue := []Point{{"2023-06-01", "490"}, {"2023-06-02", "495"}, {"2023-06-05", "500"}}
	cbr := []Point{{"2023-05-31", "80"}, {"2023-06-03", "82"}, {"2023-06-06", "90"}}
	q, err := Quotes(blue, cbr, FirstDate, "2023-06-05")
	if err != nil || len(q) != 5 {
		t.Fatalf("quotes=%v err=%v", q, err)
	}
	if q[0].CBR.Date != "2023-05-31" || q[3].Blue.Date != "2023-06-02" || q[3].CBR.Date != "2023-06-03" || q[4].CBR.Rate != "82" {
		t.Fatalf("wrong historical selection: %+v", q)
	}
	if q[0].RUBARS != "6.125000" {
		t.Fatal(q[0])
	}
}

func TestQuotesRejectMissingStaleUnsorted(t *testing.T) {
	for _, series := range [][]Point{
		nil,
		{{"2023-06-02", "490"}},
		{{"2023-05-01", "490"}},
		{{"2023-06-02", "490"}, {"2023-06-01", "490"}},
		{{"2023-06-01", "490"}, {"2023-06-01", "490"}},
		{{"2023-06-01", "-1"}},
	} {
		if _, err := Quotes(series, []Point{{FirstDate, "80"}}, FirstDate, "2023-06-03"); err == nil {
			t.Fatal("expected invalid quotes")
		}
	}
	if _, err := Quotes([]Point{{FirstDate, "490"}}, nil, FirstDate, FirstDate); err == nil {
		t.Fatal("missing CBR accepted")
	}
}

func TestRUBCentsExactFinalRounding(t *testing.T) {
	for _, tc := range []struct {
		blue, cbr string
		ars, want int64
	}{
		{"490", "80", 49000, 8000},
		{"3", "1", 100, 33},
		{"2", "1", 101, 51},
		{"1545", "91.2345", 33390001, 1971728},
	} {
		q := Quote{Blue: Point{FirstDate, tc.blue}, CBR: Point{FirstDate, tc.cbr}, RUBARS: "deliberately unused"}
		got, err := q.RUBCents(tc.ars)
		if err != nil || got != tc.want {
			t.Fatalf("%+v: got %d, err %v", tc, got, err)
		}
	}
	for _, amount := range []int64{0, -1, 1 << 53} {
		if _, err := (Quote{Blue: Point{FirstDate, "490"}, CBR: Point{FirstDate, "80"}}).RUBCents(amount); err == nil {
			t.Fatal("invalid amount accepted")
		}
	}
}

func TestRange(t *testing.T) {
	for _, pair := range [][2]string{{"2023-05-31", "2023-06-01"}, {"2023-06-02", "2023-06-01"}, {"bad", "2023-06-01"}} {
		if ValidateRange(pair[0], pair[1]) == nil {
			t.Fatal("invalid range accepted")
		}
	}
}
