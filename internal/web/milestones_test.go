package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func mustPost(t *testing.T, s *Server, path string, form url.Values) string {
	t.Helper()
	rec, body := do(s, htmxReq("POST", path, form))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST %s: %d %s", path, rec.Code, body)
	}
	return body
}

func TestMilestoneSectionAndForms(t *testing.T) {
	s, _ := newFinanceServer(t)
	mustPost(t, s, "/finances/people", url.Values{"name": {"Ana"}, "birth": {"1990-04"}, "life_expectancy": {"90"}})

	// New form defaults to an age milestone for the only person.
	_, body := do(s, htmxReq("GET", "/finances/milestones/new", nil))
	for _, want := range []string{`x-data="{ kind: 'age', variant: 'standard' }"`, "Coast FIRE", "Barista FIRE", `value="65"`, ">Ana<", "Independencia financiera"} {
		if !strings.Contains(body, want) {
			t.Errorf("milestone form missing %q", want)
		}
	}

	rec, body := do(s, htmxReq("POST", "/finances/milestones",
		url.Values{"name": {"Jubilación"}, "kind": {"age"}, "person": {"1"}, "age": {"65"}, "date": {""}, "multiple": {"x"}}))
	if rec.Code != http.StatusOK {
		t.Fatalf("create age milestone: %d %s", rec.Code, body)
	}
	if got := rec.Header().Get("HX-Trigger"); !strings.Contains(got, evMilestonesChg) {
		t.Errorf("HX-Trigger = %q", got)
	}
	// Fields of other kinds ("multiple": "x") are ignored, and the month is resolved.
	if !strings.Contains(body, "Ana cumple 65 años") || !strings.Contains(body, "abr 2055") {
		t.Errorf("milestone row: %s", body)
	}

	mustPost(t, s, "/finances/milestones", url.Values{"name": {"IF"}, "kind": {"fi"}, "multiple": {"25"}})
	_, body = do(s, htmxReq("GET", "/finances/milestones", nil))
	if !strings.Contains(body, "Según el plan") {
		t.Error("FI milestone should be shown as conditional")
	}

	// Income form offers milestones for start and end.
	_, body = do(s, htmxReq("GET", "/finances/incomes/new", nil))
	for _, want := range []string{`name="start_ms"`, `name="end_ms"`, "Fecha concreta", ">Jubilación<"} {
		if !strings.Contains(body, want) {
			t.Errorf("income form missing %q", want)
		}
	}
	body = mustPost(t, s, "/finances/incomes", url.Values{"name": {"Nómina"}, "kind": {"salary"}, "amount": {"2000"},
		"payments": {"14"}, "end_ms": {"1"}, "end": {"2030-01"}})
	if !strings.Contains(body, "Hasta Jubilación") {
		t.Errorf("income row should show the milestone: %s", body)
	}
	body = mustPost(t, s, "/finances/accounts", url.Values{"name": {"Fondo"}, "kind": {"investment"},
		"contribution": {"300"}, "contrib_end_ms": {"1"}})
	if !strings.Contains(body, "hasta Jubilación") {
		t.Errorf("account row should show the contribution milestone: %s", body)
	}

	// Validation: missing age.
	rec, body = do(s, htmxReq("POST", "/finances/milestones", url.Values{"name": {"X"}, "kind": {"age"}, "person": {"1"}, "age": {""}}))
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(body, "Rellena este campo") {
		t.Errorf("missing age: %d", rec.Code)
	}
}

func TestPlanShowsMilestoneTimelineAndMarks(t *testing.T) {
	s, _ := newFinanceServer(t)
	mustPost(t, s, "/finances/people", url.Values{"name": {"Ana"}, "birth": {"1990-04"}, "life_expectancy": {"90"}})
	mustPost(t, s, "/finances/milestones", url.Values{"name": {"Jubilación"}, "kind": {"age"}, "person": {"1"}, "age": {"65"}})
	mustPost(t, s, "/finances/milestones", url.Values{"name": {"Imposible"}, "kind": {"fi"}, "multiple": {"100"}})
	mustPost(t, s, "/finances/accounts", url.Values{"name": {"Cuenta"}, "kind": {"cash"}, "balance": {"30000"}})
	mustPost(t, s, "/finances/incomes", url.Values{"name": {"Nómina"}, "kind": {"salary"}, "amount": {"2500"}, "payments": {"14"}, "end_ms": {"1"}})
	mustPost(t, s, "/finances/incomes", url.Values{"name": {"Pensión"}, "kind": {"pension"}, "amount": {"1800"}, "payments": {"14"}, "start_ms": {"1"}})
	mustPost(t, s, "/finances/expenses", url.Values{"name": {"Vida"}, "category": {"housing"}, "amount": {"1500"}, "payments": {"12"}})
	mustPost(t, s, "/finances/debts", url.Values{"name": {"Coche"}, "kind": {"car"}, "balance": {"6000"}, "rate": {"0"}, "payment": {"500"}})

	_, body := do(s, htmxReq("GET", "/plan/results?view=year&real=1", nil))
	for _, want := range []string{
		`class="timeline"`, "Jubilación", "abr 2055", "65 años",
		"Deuda pagada: Coche", "No se alcanza en el plan",
		`"marks":[`, `"label":"Jubilación"`, `"kind":"debt"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("plan results missing %q", want)
		}
	}
	// Salary hands over to the pension at retirement: the money lasts.
	if !strings.Contains(body, "tu dinero llega hasta los 90 años") {
		t.Error("expected the plan to succeed with the pension starting at retirement")
	}
}

func TestSectionTotalsHonourMilestones(t *testing.T) {
	s, _ := newFinanceServer(t)
	mustPost(t, s, "/finances/people", url.Values{"name": {"Ana"}, "birth": {"1990-04"}, "life_expectancy": {"90"}})
	mustPost(t, s, "/finances/milestones", url.Values{"name": {"Jubilación"}, "kind": {"age"}, "person": {"1"}, "age": {"65"}})
	mustPost(t, s, "/finances/incomes", url.Values{"name": {"Nómina"}, "kind": {"salary"}, "amount": {"1200"}, "payments": {"12"}, "end_ms": {"1"}})
	body := mustPost(t, s, "/finances/incomes", url.Values{"name": {"Pensión"}, "kind": {"pension"}, "amount": {"900"}, "payments": {"12"}, "start_ms": {"1"}})
	// Only the salary is active today: the pension waits for retirement.
	if !strings.Contains(body, `<p class="section-total amount">1.200`) {
		t.Errorf("income total should exclude the future pension: %s", body)
	}
}

func TestFIREVariantForm(t *testing.T) {
	s, _ := newFinanceServer(t)
	mustPost(t, s, "/finances/people", url.Values{"name": {"Ana"}, "birth": {"1990-04"}, "life_expectancy": {"90"}})

	body := mustPost(t, s, "/finances/milestones", url.Values{"name": {"Lean"}, "kind": {"fi"}, "fi_variant": {"lean"},
		"multiple": {"25"}, "fi_amount": {"18.000"}, "coast_person": {"1"}, "coast_age": {"x"}})
	if !strings.Contains(body, "Lean FIRE") || !strings.Contains(body, "un gasto de 18.000") {
		t.Errorf("lean row: %s", body)
	}
	body = mustPost(t, s, "/finances/milestones", url.Values{"name": {"Coast"}, "kind": {"fi"}, "fi_variant": {"coast"},
		"multiple": {"25"}, "fi_amount": {""}, "coast_person": {"1"}, "coast_age": {"60"}})
	if !strings.Contains(body, "cuando Ana cumpla 60 años") {
		t.Errorf("coast row: %s", body)
	}
	// Barista needs its part-time income.
	rec, body := do(s, htmxReq("POST", "/finances/milestones", url.Values{"name": {"B"}, "kind": {"fi"},
		"fi_variant": {"barista"}, "multiple": {"25"}, "fi_amount": {""}}))
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(body, "Rellena este campo") {
		t.Errorf("barista without income: %d", rec.Code)
	}
	// The edit form restores the variant.
	_, body = do(s, htmxReq("GET", "/finances/milestones/2/edit", nil))
	if !strings.Contains(body, "variant: 'coast'") || !strings.Contains(body, `value="60"`) {
		t.Errorf("coast edit form: %s", body)
	}
}
