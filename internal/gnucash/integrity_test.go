package gnucash

import (
	"bytes"
	"compress/gzip"
	"testing"
)

func TestStrictValuesAndCents(t *testing.T) {
	for _, v := range []string{"", "bad", "1/0", "1/-1", "1/2/3", "9223372036854775808/1"} {
		if _, _, err := parseStrictValue(v); err == nil {
			t.Errorf("accepted %q", v)
		}
	}
	for _, v := range []struct{ n, d, want int64 }{{-1, 1, -100}, {123, 100, 123}, {10, 1000, 1}, {0, 100, 0}} {
		got, err := Cents(v.n, v.d)
		if err != nil || got != v.want {
			t.Errorf("%v: %d %v", v, got, err)
		}
	}
	for _, v := range [][2]int64{{1, 1000}, {1, 0}, {1 << 62, 1}, {1, -100}} {
		if _, err := Cents(v[0], v[1]); err == nil {
			t.Errorf("accepted %v", v)
		}
	}
}
func TestRejectCorruptXMLAndGzip(t *testing.T) {
	xml := []byte(`<gnc-v2><book></book></gnc-v2>`)
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	w.Write(xml)
	w.Close()
	bad := append([]byte(nil), b.Bytes()...)
	bad[len(bad)-8] ^= 1
	for _, data := range [][]byte{bad, append(xml, []byte(`<extra/>`)...), []byte(`<gnc-v2><book>`)} {
		if _, err := ParseReaderWithFallback(data); err == nil {
			t.Fatal("corrupt input accepted")
		}
	}
}
func TestXMLQuantityAndStrictFields(t *testing.T) {
	prefix := `<gnc-v2><book><transaction><id>t</id><date-posted><date>2026-09-05</date></date-posted><date-entered><date>2026-09-05</date></date-entered><splits><split><id>s</id><value>100/1</value>`
	suffix := `</split></splits></transaction></book></gnc-v2>`
	d, err := ParseReaderWithFallback([]byte(prefix + `<quantity>1/1</quantity>` + suffix))
	if err != nil {
		t.Fatal(err)
	}
	if d.Transactions[0].Splits[0].QuantityNum != 1 {
		t.Fatal("quantity lost")
	}
	for _, q := range []string{"", `<quantity>bad</quantity>`, `<quantity>1/0</quantity>`} {
		if _, err := ParseReaderWithFallback([]byte(prefix + q + suffix)); err == nil {
			t.Fatal("invalid quantity accepted")
		}
	}
}
