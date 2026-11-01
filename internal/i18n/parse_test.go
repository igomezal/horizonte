package i18n

import "testing"

func TestParseDecimal(t *testing.T) {
	cases := []struct {
		lang Lang
		in   string
		want float64
	}{
		{ES, "1.234,56", 1234.56},
		{ES, "1234,56", 1234.56},
		{ES, "1234.56", 1234.56}, // en-style decimal typed in es
		{ES, "1.234", 1234},
		{ES, "1.234.567", 1234567},
		{ES, "12,5 %", 12.5},
		{ES, "1.500 €", 1500},
		{ES, "-3,5", -3.5},
		{ES, "0,5", 0.5},
		{EN, "1,234.56", 1234.56},
		{EN, "1,234", 1234},
		{EN, "12.5%", 12.5},
		{EN, "€1,500", 1500},
		{EN, "3,5", 3.5}, // es-style decimal typed in en
	}
	for _, c := range cases {
		got, err := ParseDecimal(c.lang, c.in)
		if err != nil || got != c.want {
			t.Errorf("ParseDecimal(%s, %q) = %v, %v; want %v", c.lang, c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "abc", "1,2,3", "€", "1.2.3"} {
		if _, err := ParseDecimal(ES, bad); err == nil {
			t.Errorf("ParseDecimal(es, %q) should fail", bad)
		}
	}
}

func TestFormatInputRoundTrip(t *testing.T) {
	for _, lang := range Supported {
		for _, v := range []float64{0, 1234.5, 1234, 0.035, 150000.25} {
			s := FormatInput(lang, v, 4)
			back, err := ParseDecimal(lang, s)
			if err != nil || back != v {
				t.Errorf("%s: FormatInput(%v) = %q → %v, %v", lang, v, s, back, err)
			}
		}
	}
	if got := FormatInput(ES, 1234.5, 2); got != "1234,5" {
		t.Errorf("FormatInput = %q", got)
	}
}

func TestFormatMonth(t *testing.T) {
	if got := FormatMonth(ES, 2041, 3); got != "mar 2041" {
		t.Errorf("es = %q", got)
	}
	if got := FormatMonth(EN, 2041, 12); got != "Dec 2041" {
		t.Errorf("en = %q", got)
	}
}
