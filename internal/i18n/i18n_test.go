package i18n

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
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
	if got := c.T(ES, "demo.headline", "300 €", 30); got != "Lo que hacen 300 € al mes en 30 años" {
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

// Strings that take arguments go through fmt.Sprintf, so a literal percent
// sign must be written "%%" (otherwise "60 % bolsa" renders as "60 %!b(MISSING)olsa"),
// and both languages must use the same placeholders in the same order.
func TestFormatVerbs(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	verbs := regexp.MustCompile(`%[ +#0-]*[a-zA-Z%]`)
	placeholders := func(s string) []string {
		var out []string
		for _, v := range verbs.FindAllString(s, -1) {
			if v != "%%" {
				out = append(out, v)
			}
		}
		return out
	}
	for _, key := range c.Keys(ES) {
		es, en := c.messages[ES][key], c.messages[EN][key]
		pe, pn := placeholders(es), placeholders(en)
		usesArgs := strings.Contains(es, "%s") || strings.Contains(es, "%d")
		if !usesArgs {
			continue
		}
		for _, v := range append(append([]string{}, pe...), pn...) {
			if v != "%s" && v != "%d" {
				t.Errorf("%s: stray %q (write a literal percent as %%%%)", key, v)
			}
		}
		if strings.Join(pe, ",") != strings.Join(pn, ",") {
			t.Errorf("%s: placeholders differ: es %v, en %v", key, pe, pn)
		}
	}
}
