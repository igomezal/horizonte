package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func seedPlan(t *testing.T, s *Server) {
	t.Helper()
	posts := []struct {
		path string
		form url.Values
	}{
		// Plan clock is nov 2026; Ana turns 90 in apr 2080.
		{"/finances/people", url.Values{"name": {"Ana"}, "birth": {"1990-04"}, "life_expectancy": {"90"}}},
		{"/finances/accounts", url.Values{"name": {"Cuenta"}, "kind": {"cash"}, "balance": {"20000"}}},
		{"/finances/incomes", url.Values{"name": {"Nómina"}, "kind": {"salary"}, "amount": {"2500"}, "payments": {"14"}, "end": {"2055-04"}}},
		{"/finances/expenses", url.Values{"name": {"Vida"}, "category": {"housing"}, "amount": {"1500"}, "payments": {"12"}}},
	}
	for _, p := range posts {
		if rec, body := do(s, htmxReq("POST", p.path, p.form)); rec.Code != http.StatusOK {
			t.Fatalf("POST %s: %d %s", p.path, rec.Code, body)
		}
	}
}

func TestPlanWithoutPeopleShowsEmptyState(t *testing.T) {
	s, _ := newFinanceServer(t)
	rec, body := do(s, httptest.NewRequest("GET", "/plan", nil))
	if rec.Code != http.StatusOK || !strings.Contains(body, "añade al menos una persona") {
		t.Fatalf("plan empty state: %d", rec.Code)
	}
}

func TestPlanFailsWhenIncomeEnds(t *testing.T) {
	s, _ := newFinanceServer(t)
	seedPlan(t, s)
	rec, body := do(s, httptest.NewRequest("GET", "/plan", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	// Salary stops in 2055 with no pension: savings run out well before 90.
	for _, want := range []string{"el dinero se acaba en", `id="plan-chart"`, `"type":"projection"`, "Año a año", "Hipótesis", `aria-current="page"`} {
		if !strings.Contains(body, want) {
			t.Errorf("plan page missing %q", want)
		}
	}
	if !strings.Contains(body, `class="short"`) {
		t.Error("table should mark years with a shortfall")
	}
}

func TestPlanSucceedsWithPension(t *testing.T) {
	s, _ := newFinanceServer(t)
	seedPlan(t, s)
	pension := url.Values{"name": {"Pensión"}, "kind": {"pension"}, "amount": {"2000"}, "payments": {"14"}, "start": {"2055-05"}}
	if rec, _ := do(s, htmxReq("POST", "/finances/incomes", pension)); rec.Code != http.StatusOK {
		t.Fatal("add pension")
	}
	_, body := do(s, htmxReq("GET", "/plan/results?view=year&real=1", nil))
	if !strings.Contains(body, "tu dinero llega hasta los 90 años") {
		t.Errorf("expected success verdict")
	}
	if !strings.Contains(body, `hx-swap-oob="true"`) {
		t.Error("results should carry the table out of band")
	}
}

func TestPlanMonthlyAndNominalToggles(t *testing.T) {
	s, _ := newFinanceServer(t)
	seedPlan(t, s)
	_, body := do(s, htmxReq("GET", "/plan/results?view=month&real=0", nil))
	for _, want := range []string{"Mes a mes", "nov 2026", "En euros de cada año", `hx-get="/plan/results?real=0&amp;view=month"`, `hx-push-url="/plan?real=0&amp;view=month"`, `href="/plan?real=0&amp;view=month"`} {
		if !strings.Contains(body, want) {
			t.Errorf("monthly/nominal results missing %q", want)
		}
	}
}

func TestSaveAssumptions(t *testing.T) {
	s, st := newFinanceServer(t)
	seedPlan(t, s)
	form := url.Values{"inflation": {"3"}, "tax_rate": {"25,5"}, "surplus": {""},
		"return_cash": {"0"}, "return_deposit": {"2"}, "return_investment": {"7"},
		"return_pension": {"5"}, "return_realestate": {"3"}, "return_other": {"2"}}
	rec, body := do(s, htmxReq("POST", "/plan/assumptions", form))
	if rec.Code != http.StatusOK || rec.Header().Get("HX-Trigger") != evPlanChanged {
		t.Fatalf("save: %d %q %s", rec.Code, rec.Header().Get("HX-Trigger"), body)
	}
	if !strings.Contains(body, `value="25,5"`) || !strings.Contains(body, "Guardado") {
		t.Errorf("saved form not re-rendered: %s", body)
	}
	plan, _ := st.CurrentPlan(context.Background(), "x")
	a, _ := st.GetAssumptions(context.Background(), plan)
	if a.Inflation != 0.03 || a.TaxRate != 0.255 {
		t.Errorf("stored = %+v", a)
	}

	form.Set("inflation", "50")
	form.Set("tax_rate", "")
	rec, body = do(s, htmxReq("POST", "/plan/assumptions", form))
	if rec.Code != http.StatusUnprocessableEntity || rec.Header().Get("HX-Trigger") != "" {
		t.Fatalf("invalid save: %d", rec.Code)
	}
	if !strings.Contains(body, "fuera del rango") || !strings.Contains(body, "Rellena este campo") {
		t.Errorf("errors missing: %s", body)
	}
}
