package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/igomezal/horizonte/internal/domain"
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
