package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/igomezal/horizonte/internal/domain"
	"github.com/igomezal/horizonte/internal/i18n"
)

func TestPensionEstimateFromPlan(t *testing.T) {
	s, st := newFinanceServer(t)
	seedCashflow(t, s)
	// Ana, 36, earns 35.000 €/year (base 2.916,67 €) until 65 with 11 years
	// contributed: 39 years by 65, a full pension of 300/350 × base (2.916,66 €
	// after rounding the base to cents).
	_, body := do(s, httptest.NewRequest("GET", "/finances/pension", nil))
	for _, want := range []string{"2.499,99", "abr 2055", "Base reguladora", "no un cálculo oficial", `value="65"`} {
		if !strings.Contains(body, want) {
			t.Errorf("pension page missing %q", want)
		}
	}

	form := url.Values{"person": {"1"}, "years": {"11"}, "base": {"2916,67"}, "until": {"65"}, "retire": {"63"}, "regime": {"employee"}}
	_, frag := do(s, htmxReq("POST", "/finances/pension/estimate", form))
	if !strings.Contains(frag, "Reducción por jubilación anticipada") || strings.Contains(frag, "<html") {
		t.Errorf("estimate fragment: %.300s", frag)
	}
	form.Set("years", "80")
	if _, frag := do(s, htmxReq("POST", "/finances/pension/estimate", form)); !strings.Contains(frag, "Revisa estos datos") {
		t.Errorf("invalid years not reported: %.300s", frag)
	}

	form.Set("years", "11")
	form.Set("retire", "")
	rec, b := do(s, htmxReq("POST", "/finances/pension", form))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("add: %d %s", rec.Code, b)
	}
	plan, _ := st.CurrentPlan(t.Context(), "x")
	incomes, _ := st.ListIncomes(t.Context(), plan)
	got := incomes[len(incomes)-1]
	if got.Name != "Pensión pública (Ana)" || got.Kind != domain.IncomePension || got.PaymentsPerYear != 14 ||
		got.Amount != 250000 || got.Start != (domain.Month{Year: 2055, Month: 4}) || got.OwnerID == nil {
		t.Errorf("added income = %+v", got)
	}
}

func TestHealthPhasesBecomeExpenses(t *testing.T) {
	s, st := newFinanceServer(t)
	seedCashflow(t, s)
	_, body := do(s, httptest.NewRequest("GET", "/finances/health", nil))
	if !strings.Contains(body, "Seguro médico privado") || !strings.Contains(body, "Total durante el plan") {
		t.Fatal("health page without defaults")
	}
	form := url.Values{"person": {"1"}}
	for i, ph := range defaultHealthPhases {
		k := strconv.Itoa(i)
		form.Set("from"+k, itoa(ph.From))
		if ph.To > 0 {
			form.Set("to"+k, itoa(ph.To))
		}
		if ph.Monthly > 0 {
			form.Set("amount"+k, itoa(int(ph.Monthly/100)))
		}
	}
	rec, b := do(s, htmxReq("POST", "/finances/health", form))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("add: %d %s", rec.Code, b)
	}
	plan, _ := st.CurrentPlan(t.Context(), "x")
	exps, _ := st.ListExpenses(t.Context(), plan)
	var health []domain.Expense
	for _, e := range exps {
		if e.Category == domain.ExpenseHealth {
			health = append(health, e)
		}
	}
	if len(health) != 7 { // care has no amount
		t.Fatalf("health expenses = %d: %+v", len(health), health)
	}
	byName := map[string]domain.Expense{}
	for _, e := range health {
		byName[e.Name] = e
	}
	band := byName["Seguro médico privado (Ana, 45–54)"]
	if band.Amount != 8000 || band.Start != (domain.Month{Year: 2035, Month: 4}) || band.End != (domain.Month{Year: 2045, Month: 3}) {
		t.Errorf("45–54 band = %+v", band)
	}
	if first := byName["Seguro médico privado (Ana, 0–44)"]; first.Start != (domain.Month{Year: 2026, Month: 11}) {
		t.Errorf("current band starts now: %+v", first)
	}
	if last := byName["Seguro médico privado (Ana, 75+)"]; !last.End.IsZero() {
		t.Errorf("last band is open-ended: %+v", last)
	}

	none := url.Values{"person": {"1"}, "from0": {"0"}}
	if rec, b := do(s, htmxReq("POST", "/finances/health", none)); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(b, "nada que añadir") {
		t.Errorf("empty phases: %d", rec.Code)
	}
}

func TestPlanShowsEstate(t *testing.T) {
	s, _ := newFinanceServer(t)
	seedCashflow(t, s)
	_, body := do(s, httptest.NewRequest("GET", "/plan", nil))
	for _, want := range []string{"Herencia", "Patrimonio que se hereda", "impuesto de sucesiones"} {
		if !strings.Contains(body, want) {
			t.Errorf("plan page missing %q", want)
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// A person with no salary gets told which field to fill in, and a person
// already past the ordinary age gets no start date in the past to add.
func TestPensionMissingBaseAndRetired(t *testing.T) {
	s, _ := newFinanceServer(t)
	mustPost(t, s, "/finances/people", url.Values{"name": {"Bea"}, "birth": {"1996-01"}, "life_expectancy": {"90"}})
	mustPost(t, s, "/finances/people", url.Values{"name": {"Carlos"}, "birth": {"1956-01"}, "life_expectancy": {"95"}})

	_, body := do(s, httptest.NewRequest("GET", "/finances/pension?person=1", nil))
	if !strings.Contains(body, "Base de cotización mensual: Rellena este campo") {
		t.Errorf("missing base not explained: %.400s", body[strings.Index(body, `id="pension-result"`):])
	}

	form := url.Values{"person": {"2"}, "years": {"40"}, "base": {"2000"}, "until": {"70"}, "retire": {""}, "regime": {"employee"}}
	_, frag := do(s, htmxReq("POST", "/finances/pension/estimate", form))
	if !strings.Contains(frag, "ya ha pasado la edad ordinaria") || strings.Contains(frag, `type="submit"`) {
		t.Errorf("retired person: %.400s", frag)
	}
	if rec, _ := do(s, htmxReq("POST", "/finances/pension", form)); rec.Code == http.StatusSeeOther {
		t.Error("a pension starting in the past was added")
	}
	// A later claiming age still gives an estimate (delayed retirement).
	form.Set("retire", "72")
	if _, frag := do(s, htmxReq("POST", "/finances/pension/estimate", form)); !strings.Contains(frag, "Bonificación por jubilación demorada") {
		t.Errorf("delayed retirement: %.400s", frag)
	}
}

// The tool links sit inside sections with hx-target="this": they must not be
// boosted, or htmx swaps the whole tool page in place of the section.
func TestToolLinksNotBoosted(t *testing.T) {
	s, _ := newFinanceServer(t)
	mustPost(t, s, "/finances/people", url.Values{"name": {"Ana"}, "birth": {"1990-04"}, "life_expectancy": {"90"}})
	_, body := do(s, httptest.NewRequest("GET", "/finances", nil))
	for _, link := range []string{`href="/finances/pension" hx-boost="false"`, `href="/finances/health" hx-boost="false"`} {
		if !strings.Contains(body, link) {
			t.Errorf("finances page missing %s", link)
		}
	}
}

// The months field makes a partial-year start reachable: 63 years and 5 months
// is 19 months before 65 (art. 208.2), and the start date follows it.
func TestPensionRetireMonths(t *testing.T) {
	s, st := newFinanceServer(t)
	s.now = func() time.Time { return time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC) }
	seedCashflow(t, s)
	_, body := do(s, httptest.NewRequest("GET", "/finances/pension", nil))
	if !strings.Contains(body, `name="retire_months"`) || !strings.Contains(body, `value="0"`) {
		t.Errorf("no months field: %.200s", body)
	}
	if strings.Contains(body, "pension.") {
		t.Error("raw i18n key on the pension page")
	}

	form := url.Values{"person": {"1"}, "years": {"11"}, "base": {"2916,67"}, "until": {"65"}, "retire": {"63"},
		"retire_months": {"5"}, "regime": {"employee"}}
	_, frag := do(s, htmxReq("POST", "/finances/pension/estimate", form))
	// 37,8 years at the start (fewer than 38y6m: first column), 19 months
	// ahead of 65: 9,78 %.
	if !strings.Contains(frag, "-9,78") || !strings.Contains(frag, "63 años") || !strings.Contains(frag, "sept 2053") {
		t.Errorf("63 years and 5 months: %.600s", frag)
	}
	// Without the months the advance is 24 months: 21,00 % in the same column.
	form.Set("retire_months", "0")
	if _, frag = do(s, htmxReq("POST", "/finances/pension/estimate", form)); !strings.Contains(frag, "-21,00") {
		t.Errorf("63 years: %.600s", frag)
	}

	// The start date of the added income counts the months too.
	form.Set("retire_months", "5")
	if rec, b := do(s, htmxReq("POST", "/finances/pension", form)); rec.Code != http.StatusSeeOther {
		t.Fatalf("add: %d %s", rec.Code, b)
	}
	plan, _ := st.CurrentPlan(t.Context(), "x")
	incomes, _ := st.ListIncomes(t.Context(), plan)
	if got := incomes[len(incomes)-1]; got.Start != (domain.Month{Year: 2053, Month: 9}) {
		t.Errorf("start = %v, want 2053-09", got.Start)
	}

	// Validation: 0 to 11, and not without the years.
	for _, c := range []struct{ retire, months string }{{"63", "12"}, {"63", "-1"}, {"63", "x"}, {"", "3"}} {
		form.Set("retire", c.retire)
		form.Set("retire_months", c.months)
		if _, frag := do(s, htmxReq("POST", "/finances/pension/estimate", form)); !strings.Contains(frag, "Revisa estos datos") {
			t.Errorf("retire %q, months %q not reported: %.300s", c.retire, c.months, frag)
		}
		if rec, _ := do(s, htmxReq("POST", "/finances/pension", form)); rec.Code == http.StatusSeeOther {
			t.Errorf("retire %q, months %q was added", c.retire, c.months)
		}
	}
}

// Art. 205.1.b: without 2 of the last 15 years contributed there is no pension
// figure, and the add action is refused like the other not-entitled case.
func TestPensionNotEntitledCannotBeAdded(t *testing.T) {
	s, st := newFinanceServer(t)
	s.now = func() time.Time { return time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC) }
	seedCashflow(t, s)
	plan, _ := st.CurrentPlan(t.Context(), "x")
	before, _ := st.ListIncomes(t.Context(), plan)

	// Ana, 36: 13 years contributed, stops at 40.
	form := url.Values{"person": {"1"}, "years": {"13"}, "base": {"2916,67"}, "until": {"40"}, "retire": {""},
		"retire_months": {"0"}, "regime": {"employee"}}
	_, frag := do(s, htmxReq("POST", "/finances/pension/estimate", form))
	if !strings.Contains(frag, "al menos 2 dentro de los 15 años anteriores") || !strings.Contains(frag, "convenio especial") ||
		strings.Contains(frag, `type="submit"`) || strings.Contains(frag, "al mes, en 14 pagas") {
		t.Errorf("estimate: %.700s", frag)
	}
	rec, b := do(s, htmxReq("POST", "/finances/pension", form))
	if rec.Code != http.StatusUnprocessableEntity || strings.Contains(b, "pension.") {
		t.Errorf("add: %d", rec.Code)
	}
	if after, _ := st.ListIncomes(t.Context(), plan); len(after) != len(before) {
		t.Errorf("an income was added: %d -> %d", len(before), len(after))
	}

	// Contributing until a later age gives a pension again.
	form.Set("until", "65")
	if rec, _ := do(s, htmxReq("POST", "/finances/pension", form)); rec.Code != http.StatusSeeOther {
		t.Errorf("until 65: %d", rec.Code)
	}
}

// The new messages exist in both languages and show no raw keys.
func TestPensionMessagesInBothLanguages(t *testing.T) {
	s, _ := newFinanceServer(t)
	s.now = func() time.Time { return time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC) }
	seedCashflow(t, s)
	get := func(lang string, form url.Values) string {
		req := htmxReq("POST", "/finances/pension/estimate", form)
		req.AddCookie(&http.Cookie{Name: "lang", Value: lang})
		_, body := do(s, req)
		return body
	}
	// Ana, 36, 13 years contributed, stops at 40: 2 of the last 15 years are missing.
	notEntitled := url.Values{"person": {"1"}, "years": {"13"}, "base": {"2916,67"}, "until": {"40"}, "retire": {""},
		"retire_months": {"0"}, "regime": {"employee"}}
	// Ana is 36 years and 7 months old in this test: 6,6 years (79 months) and
	// contributions until 65 make 420 months, 35 years, and 24 months before 67
	// the early pension at the minimum base is 922,08, under the minimum.
	belowMin := url.Values{"person": {"1"}, "years": {"6,6"}, "base": {"1424,40"}, "until": {"65"}, "retire": {"65"},
		"retire_months": {"0"}, "regime": {"employee"}}
	for lang, want := range map[string][2]string{
		"es": {"al menos 2 dentro de los 15 años anteriores", "no superaría la pensión mínima (936,20"},
		"en": {"at least 2 within the 15 years before", "would not exceed the minimum pension (€936.20"},
	} {
		if got := get(lang, notEntitled); !strings.Contains(got, want[0]) || strings.Contains(got, "pension.") || strings.Contains(got, "%!") {
			t.Errorf("%s not entitled: %.500s", lang, got)
		}
		if got := get(lang, belowMin); !strings.Contains(got, want[1]) || strings.Contains(got, "pension.") || strings.Contains(got, "%!") {
			t.Errorf("%s below the minimum: %.500s", lang, got)
		}
	}
}

// Texts on the pension page: the field name in the message matches the field,
// the exception for "alta asimilada" is mentioned, ages show their months, and
// a non-number in the months field gets its own message.
func TestPensionPageTexts(t *testing.T) {
	s, _ := newFinanceServer(t)
	s.now = func() time.Time { return time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC) }
	seedCashflow(t, s)
	get := func(lang string, form url.Values) string {
		req := htmxReq("POST", "/finances/pension/estimate", form)
		req.AddCookie(&http.Cookie{Name: "lang", Value: lang})
		_, body := do(s, req)
		return body
	}
	notEntitled := url.Values{"person": {"1"}, "years": {"13"}, "base": {"2916,67"}, "until": {"40"}, "retire": {""},
		"retire_months": {"0"}, "regime": {"employee"}}
	for lang, want := range map[string]string{"es": "alta o asimilada sin obligación de cotizar", "en": "(alta asimilada) without the duty to contribute"} {
		got := get(lang, notEntitled)
		if !strings.Contains(got, want) {
			t.Errorf("%s: no mention of the alta asimilada exception: %.400s", lang, got)
		}
		label := s.catalog.T(i18n.Lang(lang), "pension.until")
		if !strings.Contains(got, "«"+label+"»") && !strings.Contains(got, "&#34;"+label+"&#34;") {
			t.Errorf("%s: the message does not name the field %q: %.500s", lang, label, got)
		}
	}

	// Ages with months. Ana (36y7m) claims at 63y5m, or at 63.
	form := url.Values{"person": {"1"}, "years": {"11"}, "base": {"2916,67"}, "until": {"65"}, "retire": {"63"},
		"retire_months": {"5"}, "regime": {"employee"}}
	for lang, want := range map[string]string{"es": "a los 63 años y 5 meses", "en": "at age 63 years and 5 months"} {
		if got := get(lang, form); !strings.Contains(got, want) {
			t.Errorf("%s: %q missing: %.300s", lang, want, got)
		}
	}
	form.Set("retire_months", "0")
	for lang, want := range map[string]string{"es": "a los 63 años", "en": "at age 63"} {
		if got := get(lang, form); !strings.Contains(got, want) || strings.Contains(got, "63 años y") || strings.Contains(got, "63 years and") {
			t.Errorf("%s: whole years: %.300s", lang, got)
		}
	}

	// A non-number in the months field.
	form.Set("retire_months", "x")
	for lang, want := range map[string]string{"es": "Escribe un número entero de 0 a 11", "en": "Enter a whole number from 0 to 11"} {
		got := get(lang, form)
		if !strings.Contains(got, want) || strings.Contains(got, "1.250,50") || strings.Contains(got, "1,250.50") {
			t.Errorf("%s: months message: %.400s", lang, got)
		}
	}
}

// A negative figure in a compact ledger is red (the early reduction).
func TestCompactLedgerNegativeIsRed(t *testing.T) {
	css, err := os.ReadFile("../../ui/static/css/app.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), ".ledger.compact .ledger-row dd.negative {\n  color: var(--app-negative);") {
		t.Error("no rule makes dd.negative red inside .ledger.compact")
	}
}

// The "fill in this field" for a missing base is listed once in the result
// and not left under the field, where a live estimate could not clear it.
func TestPensionMissingBaseIsNotShownInline(t *testing.T) {
	s, _ := newFinanceServer(t)
	mustPost(t, s, "/finances/people", url.Values{"name": {"Bea"}, "birth": {"1996-01"}, "life_expectancy": {"90"}})
	_, body := do(s, httptest.NewRequest("GET", "/finances/pension?person=1", nil))
	if n := strings.Count(body, "Rellena este campo"); n != 1 {
		t.Errorf("'Rellena este campo' appears %d times", n)
	}
}

// "1 mes" and "1 month", not "1 meses" or "1 months", and the note about the
// cap only says the figure could be lower when the start is before 2033.
func TestPensionSingularMonthsAndCapNote(t *testing.T) {
	s, _ := newFinanceServer(t)
	s.now = func() time.Time { return time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC) }
	mustPost(t, s, "/finances/people", url.Values{"name": {"Cleo"}, "birth": {"1969-01"}, "life_expectancy": {"90"}})
	mustPost(t, s, "/finances/people", url.Values{"name": {"Dana"}, "birth": {"1976-01"}, "life_expectancy": {"90"}})
	get := func(lang string, form url.Values) string {
		req := htmxReq("POST", "/finances/pension/estimate", form)
		req.AddCookie(&http.Cookie{Name: "lang", Value: lang})
		_, body := do(s, req)
		return body
	}
	one := url.Values{"person": {"2"}, "years": {"27"}, "base": {"3000"}, "until": {"66"}, "retire": {"65"}, "retire_months": {"1"}, "regime": {"employee"}}
	for lang, want := range map[string]string{"es": "a los 65 años y 1 mes", "en": "at age 65 years and 1 month"} {
		got := get(lang, one)
		if !strings.Contains(got, want) || strings.Contains(got, "1 meses") || strings.Contains(got, "1 months") {
			t.Errorf("%s: %q: %.300s", lang, want, got)
		}
	}
	two := url.Values{"person": {"2"}, "years": {"27"}, "base": {"3000"}, "until": {"66"}, "retire": {"65"}, "retire_months": {"2"}, "regime": {"employee"}}
	if got := get("es", two); !strings.Contains(got, "65 años y 2 meses") {
		t.Errorf("2 months: %.300s", got)
	}

	// Cleo (57y10m) retires at 63 in 2032: at the maximum, from the DT 34a table.
	before := url.Values{"person": {"1"}, "years": {"31,5"}, "base": {"5101,20"}, "until": {"63"}, "retire": {"63"}, "retire_months": {"0"}, "regime": {"employee"}}
	// Dana (50y10m) retires at 63 in 2039: the table equals art. 208.2.
	after := url.Values{"person": {"2"}, "years": {"27"}, "base": {"5101,20"}, "until": {"63"}, "retire": {"63"}, "retire_months": {"0"}, "regime": {"employee"}}
	for lang, want := range map[string]string{"es": "si esa tabla dejara de aplicarse, sería menor", "en": "if that table stopped applying, it would be lower"} {
		if got := get(lang, before); !strings.Contains(got, want) {
			t.Errorf("%s 2032: clause missing: %.600s", lang, got)
		}
		got := get(lang, after)
		if strings.Contains(got, want) || !strings.Contains(got, "art. 208.2") || strings.Contains(got, "%!") {
			t.Errorf("%s 2039: %.700s", lang, got)
		}
	}
}

// A positive total in a compact ledger is not green; a negative one stays red.
func TestCompactLedgerTotalsHaveNoGreen(t *testing.T) {
	css, err := os.ReadFile("../../ui/static/css/app.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		".ledger.compact .ledger-row.total dd.positive {\n  color: var(--app-text);",
		".ledger.compact .ledger-row.total dd.negative {\n  color: var(--app-negative);",
	} {
		if !strings.Contains(string(css), want) {
			t.Errorf("missing %q", want)
		}
	}
}
