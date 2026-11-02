package web

import (
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
	form := url.Values{"inflation": {"3"}, "tax_model": {"flat"}, "tax_rate": {"25,5"}, "surplus": {""},
		"return_cash": {"0"}, "return_deposit": {"2"}, "return_investment": {"7"},
		"return_pension": {"5"}, "return_realestate": {"3"}, "return_other": {"2"}}
	rec, body := do(s, htmxReq("POST", "/plan/assumptions", form))
	if rec.Code != http.StatusOK || rec.Header().Get("HX-Trigger") != evPlanChanged {
		t.Fatalf("save: %d %q %s", rec.Code, rec.Header().Get("HX-Trigger"), body)
	}
	if !strings.Contains(body, `value="25,5"`) || !strings.Contains(body, "Guardado") {
		t.Errorf("saved form not re-rendered: %s", body)
	}
	plan, _ := st.CurrentPlan(t.Context(), "x")
	a, _ := st.GetAssumptions(t.Context(), plan)
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

func TestWithdrawalStrategyFromPlanForm(t *testing.T) {
	s, st := newFinanceServer(t)
	seedMC(t, s) // milestone 1 = Jubilación at 65
	form := url.Values{"inflation": {"2,5"}, "tax_rate": {"20"}, "surplus": {""}, "return_cash": {"0"},
		"return_deposit": {"2"}, "return_investment": {"6"}, "return_pension": {"5"}, "return_realestate": {"3"}, "return_other": {"2"},
		"wd_strategy": {"guardrails"}, "wd_start": {"1"}, "wd_rate": {"4,5"}, "wd_floor": {"75"}, "wd_ceiling": {"130"},
		"wd_order": {"investment,cash,deposit,other,pension"}, "pension_from": {"1"}}
	rec, body := do(s, htmxReq("POST", "/plan/assumptions", form))
	if rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, body)
	}
	plan, _ := st.CurrentPlan(t.Context(), "x")
	a, _ := st.GetAssumptions(t.Context(), plan)
	if a.Withdrawal.Strategy != "guardrails" || a.Withdrawal.Rate != 0.045 || a.Withdrawal.Floor != 0.75 ||
		a.Order[0] != "investment" || a.PensionAvailableMS == nil {
		t.Errorf("stored = %+v / %v / %v", a.Withdrawal, a.Order, a.PensionAvailableMS)
	}
	// The plan explains how much spending varies.
	_, res := do(s, htmxReq("GET", "/plan/results", nil))
	if !strings.Contains(res, "Con «Guardarraíles»") {
		t.Errorf("plan should describe the strategy's effect")
	}
	// A strategy without a start milestone is rejected.
	form.Set("wd_start", "")
	if rec, body := do(s, htmxReq("POST", "/plan/assumptions", form)); rec.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(body, "Elige desde qué hito") {
		t.Errorf("missing start milestone: %d", rec.Code)
	}
	// An invalid order is rejected.
	form.Set("wd_start", "1")
	form.Set("wd_order", "cash,cash,deposit,other,pension")
	if rec, _ := do(s, htmxReq("POST", "/plan/assumptions", form)); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("invalid order: %d", rec.Code)
	}
}

func TestTaxModelFromPlanForm(t *testing.T) {
	s, st := newFinanceServer(t)
	seedPlan(t, s)
	_, body := do(s, httptest.NewRequest("GET", "/plan", nil))
	if !strings.Contains(body, `name="tax_region"`) || !strings.Contains(body, "IRPF (España)") || !strings.Contains(body, "no un cálculo oficial") {
		t.Fatal("IRPF is the default model and shows the region")
	}
	form := url.Values{"inflation": {"2"}, "tax_model": {"es"}, "tax_region": {"madrid"}, "tax_rate": {"20"}, "surplus": {""},
		"return_cash": {"0"}, "return_deposit": {"2"}, "return_investment": {"7"},
		"return_pension": {"5"}, "return_realestate": {"3"}, "return_other": {"2"}}
	if rec, b := do(s, htmxReq("POST", "/plan/assumptions", form)); rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, b)
	}
	plan, _ := st.CurrentPlan(t.Context(), "x")
	if a, _ := st.GetAssumptions(t.Context(), plan); a.TaxModel != "es" || a.TaxRegion != "madrid" {
		t.Errorf("stored %q / %q", a.TaxModel, a.TaxRegion)
	}
	form.Set("tax_region", "atlantis")
	if rec, _ := do(s, htmxReq("POST", "/plan/assumptions", form)); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("unknown region accepted: %d", rec.Code)
	}
}

// The table shows returns, and property appreciation only when there is property.
func TestPlanReturnColumns(t *testing.T) {
	s, _ := newFinanceServer(t)
	seedPlan(t, s)
	_, body := do(s, httptest.NewRequest("GET", "/plan", nil))
	if !strings.Contains(body, ">Rentabilidad</th>") || strings.Contains(body, "Revalorización inmuebles") {
		t.Error("without property: want a returns column and no appreciation column")
	}
	if strings.Contains(body, "sobre el saldo al inicio") {
		t.Error("a cash account at 0 % has no return rate to show")
	}
	for _, acc := range []url.Values{
		{"name": {"Fondo"}, "kind": {"investment"}, "balance": {"50000"}},
		{"name": {"Piso"}, "kind": {"realestate"}, "balance": {"200000"}},
	} {
		if rec, b := do(s, htmxReq("POST", "/finances/accounts", acc)); rec.Code != http.StatusOK {
			t.Fatalf("add account: %d %.200s", rec.Code, b)
		}
	}
	_, body = do(s, httptest.NewRequest("GET", "/plan", nil))
	if !strings.Contains(body, "Revalorización inmuebles") {
		t.Error("with property: want the appreciation column")
	}
	if !strings.Contains(body, "sobre el saldo al inicio") {
		t.Error("returns should carry the rate as a tooltip")
	}
}
