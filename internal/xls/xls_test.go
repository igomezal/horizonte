package xls

import "testing"

func TestRK(t *testing.T) {
	neg := int32(-5)
	cases := []struct {
		v    uint32
		want float64
	}{
		{uint32(1871<<2) | 2, 1871},      // integer
		{uint32(187101<<2) | 3, 1871.01}, // integer / 100
		{0x3FF00000, 1},                  // float 1.0 (high 30 bits)
		{0x3FF00000 | 1, 0.01},           // float 1.0 / 100
		{uint32(neg<<2) | 2, -5},         // negative integer
	}
	for _, c := range cases {
		if got := rk(c.v); got != c.want {
			t.Errorf("rk(%#x) = %v, want %v", c.v, got, c.want)
		}
	}
}

func TestRejectsNonXLS(t *testing.T) {
	if _, err := Read([]byte("this is not a spreadsheet, just some text that is long enough")); err == nil {
		t.Error("expected an error for non-OLE2 data")
	}
	if _, err := Read(make([]byte, 600)); err == nil {
		t.Error("expected an error for zeroed data")
	}
}
