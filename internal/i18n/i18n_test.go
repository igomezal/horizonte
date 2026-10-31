package i18n

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
)

func TestCatalogParity(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := c.Keys(Default)
	sort.Strings(want)
	for _, l := range Supported {
		got := map[string]bool{}
		for _, k := range c.Keys(l) {
			got[k] = true
		}
		for _, k := range want {
			if !got[k] {
				t.Errorf("%s catalog missing key %q", l, k)
			}
		}
		if len(got) != len(want) {
			t.Errorf("%s catalog has %d keys, default has %d", l, len(got), len(want))
		}
	}
}

func TestTFallbacks(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := c.T(EN, "nav.plan"); got != "Plan" {
		t.Errorf("T(en, nav.plan) = %q", got)
	}
	if got := c.T(ES, "no.such.key"); got != "no.such.key" {
		t.Errorf("missing key should return key, got %q", got)
	}
	if got := c.T(ES, "demo.desc", "1 €", "2 €", "5 %", 30); got == "" || got == "demo.desc" {
		t.Errorf("formatted message not rendered: %q", got)
	}
}

func TestFormat(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"es money", FormatMoney(ES, 1234567.891, 2), "1.234.567,89 €"},
		{"en money", FormatMoney(EN, 1234567.891, 2), "€1,234,567.89"},
		{"es negative", FormatMoney(ES, -1500, 0), "-1.500 €"},
		{"en negative", FormatMoney(EN, -1500, 0), "-€1,500"},
		{"small", FormatNumber(ES, 999, 0), "999"},
		{"zero", FormatNumber(ES, 0, 2), "0,00"},
		{"negative zero", FormatNumber(EN, -0.001, 2), "0.00"},
		{"es percent", FormatPercent(ES, 0.075, 1), "7,5 %"},
		{"en percent", FormatPercent(EN, 0.075, 1), "7.5%"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestMiddleware(t *testing.T) {
	var seen Lang
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = FromContext(r.Context())
	}))

	req := httptest.NewRequest("GET", "/", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if seen != ES {
		t.Errorf("default lang = %q, want es", seen)
	}

	req = httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: "en"})
	h.ServeHTTP(httptest.NewRecorder(), req)
	if seen != EN {
		t.Errorf("cookie lang = %q, want en", seen)
	}

	req = httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: "fr"})
	h.ServeHTTP(httptest.NewRecorder(), req)
	if seen != ES {
		t.Errorf("unsupported lang = %q, want es", seen)
	}
}
