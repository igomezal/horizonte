package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/igomezal/horizonte/internal/domain"
	"github.com/igomezal/horizonte/internal/engine"
	"github.com/igomezal/horizonte/internal/store"
)

var (
	figureRe  = regexp.MustCompile(`<span class="figure[^"]*">([^<]+)</span>`)
	tileOdds  = regexp.MustCompile(`(?s)<a class="metric metric-link glance-odds" href="/success">\s*<span class="metric-value">([^<]+)</span>`)
	tileLasts = regexp.MustCompile(`(?s)<span class="metric-value metric-text[^"]*">([^<]*(?:Llega hasta|Se acaba)[^<]*)</span>`)
)

func TestHomeGlanceShowsOwnNumbers(t *testing.T) {
	s, _ := newFinanceServer(t)
	seedMC(t, s)

	rec, home := do(s, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	for _, want := range []string{
		`class="card hero-card glance"`, "Tu plan de un vistazo", "Mi plan", `href="/finances"`, `href="/plan"`,
		`hx-get="/glance/odds" hx-trigger="load, retry"`, `chip chip-updating`, "Calculando…",
	} {
		if !strings.Contains(home, want) {
			t.Errorf("home missing %q", want)
		}
	}
	if strings.Contains(home, `id="demo-chart"`) || strings.Contains(home, "Ejemplo") {
		t.Error("a plan with people should not show the example")
	}
	for _, want := range []string{"Sobre esta aplicación", "Qué puedes hacer"} {
		if !strings.Contains(home, want) {
			t.Errorf("home missing %q", want)
		}
	}

	// Net worth: the figure the Finances summary shows.
	_, sum := do(s, htmxReq("GET", "/finances/summary", nil))
	nw := figureRe.FindStringSubmatch(sum)
	if nw == nil || !strings.Contains(home, ">"+nw[1]+"</span>") {
		t.Errorf("net worth tile should show %v", nw)
	}

	// How long the money lasts: the Scenarios row for the same plan.
	_, scen := do(s, httptest.NewRequest("GET", "/scenarios", nil))
	lasts := tileLasts.FindStringSubmatch(home)
	if lasts == nil || !strings.Contains(scen, ">"+lasts[1]+"</td>") {
		t.Errorf("lasts tile %v should match the Scenarios row", lasts)
	}
	if strings.Contains(home, `metric-text negative`) {
		t.Error("a plan that lasts is not red")
	}

	// Next milestone: the Plan timeline's first upcoming item (the car loan
	// ends in 24 months, before the retirement milestone).
	_, plan := do(s, httptest.NewRequest("GET", "/plan", nil))
	firstUpcoming := regexp.MustCompile(`(?s)<li class="tl-debt">\s*<span class="tl-name">Deuda pagada: Coche</span>\s*<span class="tl-when">oct 2028</span>`)
	if !firstUpcoming.MatchString(plan) || !strings.Contains(home, "Deuda pagada: Coche · oct 2028") {
		t.Error("next milestone should be the debt paid off, as in the Plan timeline")
	}
}

func TestGlanceOddsMatchSuccessAndCache(t *testing.T) {
	s, _ := newFinanceServer(t)
	seedMC(t, s)

	rec, odds := do(s, htmxReq("GET", "/glance/odds", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	got := tileOdds.FindStringSubmatch(odds)
	if got == nil {
		t.Fatalf("odds tile missing: %s", odds)
	}
	// The chip is swapped out of band, from calculating to ready.
	for _, want := range []string{`id="glance-chip"`, `hx-swap-oob="true"`, "Resultado listo", `chip-live`} {
		if !strings.Contains(odds, want) {
			t.Errorf("odds response missing %q", want)
		}
	}

	// Same figure as /success for the same plan and default seed.
	_, res := do(s, htmxReq("GET", "/success/results?seed=1", nil))
	if fig := figureRe.FindStringSubmatch(res); fig == nil || fig[1] != got[1] {
		t.Errorf("odds = %q, /success shows %v", got[1], fig)
	}

	// Cached: the home page has the tile directly, with no lazy request.
	_, home := do(s, httptest.NewRequest("GET", "/", nil))
	if strings.Contains(home, "/glance/odds") || !strings.Contains(home, ">"+got[1]+"</span>") {
		t.Error("with the result cached, home should include the odds directly")
	}
	if !strings.Contains(home, "chip chip-live") {
		t.Error("chip should be ready once the odds are known")
	}
}

func TestGlanceOddsWithoutPeople(t *testing.T) {
	s, _ := newFinanceServer(t)
	rec, _ := do(s, htmxReq("GET", "/glance/odds", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestHomeWithoutPeopleKeepsDemo(t *testing.T) {
	s, _ := newFinanceServer(t)
	// Accounts but nobody in the plan: still the example.
	mustPost(t, s, "/finances/accounts", url.Values{"name": {"Cuenta"}, "kind": {"cash"}, "balance": {"40000"}})
	_, body := do(s, httptest.NewRequest("GET", "/", nil))
	for _, want := range []string{`id="demo-chart"`, "Ejemplo", "Lo que hacen 300"} {
		if !strings.Contains(body, want) {
			t.Errorf("home missing %q", want)
		}
	}
	if strings.Contains(body, "glance") {
		t.Error("an empty plan must not show the glance card")
	}
}

func TestHomeGlanceNoMilestonesAndRunsOut(t *testing.T) {
	s, _ := newFinanceServer(t)
	mustPost(t, s, "/finances/people", url.Values{"name": {"Ana"}, "birth": {"1960-04"}, "life_expectancy": {"90"}})
	mustPost(t, s, "/finances/accounts", url.Values{"name": {"Cuenta"}, "kind": {"cash"}, "balance": {"20000"}})
	mustPost(t, s, "/finances/expenses", url.Values{"name": {"Alquiler"}, "category": {"housing"}, "amount": {"2000"}, "payments": {"12"}})

	_, home := do(s, httptest.NewRequest("GET", "/", nil))
	for _, want := range []string{
		"Sin hitos próximos", "añade uno en Finanzas", `metric-text negative`, "Se acaba a los",
	} {
		if !strings.Contains(home, want) {
			t.Errorf("home missing %q", want)
		}
	}
	// The odds response shows the shortfall chip, not "ready".
	_, odds := do(s, htmxReq("GET", "/glance/odds", nil))
	if !strings.Contains(odds, "chip-critical") || !strings.Contains(odds, "Faltan fondos") {
		t.Errorf("a plan that runs out should end in the shortfall chip: %s", odds)
	}

	// English.
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "lang", Value: "en"})
	_, en := do(s, req)
	for _, want := range []string{"Your plan at a glance", "Runs out at age", "No upcoming milestones", "Next milestone"} {
		if !strings.Contains(en, want) {
			t.Errorf("english home missing %q", want)
		}
	}
}

func TestNextMilestoneRules(t *testing.T) {
	s, _ := newFinanceServer(t)
	m := func(y, mo int) domain.Month { return domain.Month{Year: y, Month: mo} }
	in := engine.Input{Start: m(2026, 11)}
	hit := func(name string, month domain.Month) engine.MilestoneHit {
		return engine.MilestoneHit{Milestone: domain.Milestone{Name: name}, Month: month}
	}
	debt := func(name string, month domain.Month) engine.DebtPaidOff {
		return engine.DebtPaidOff{Debt: domain.Debt{Name: name}, Month: month}
	}
	next := func(res engine.Result) (string, domain.Month, bool) {
		return s.nextMilestone("es", in, res)
	}

	// A milestone that already happened and one the plan never reaches are skipped.
	label, month, ok := next(engine.Result{Milestones: []engine.MilestoneHit{
		hit("Pasado", m(2020, 5)), hit("Nunca", domain.Month{}), hit("Viaje", m(2027, 3)),
	}})
	if !ok || label != "Viaje" || month != m(2027, 3) {
		t.Errorf("got %q %v %v, want Viaje 2027-03", label, month, ok)
	}

	// Nothing upcoming.
	if _, _, ok := next(engine.Result{Milestones: []engine.MilestoneHit{hit("Pasado", m(2020, 5)), hit("Nunca", domain.Month{})}}); ok {
		t.Error("no upcoming milestone expected")
	}

	// A tie in the same month: the milestone comes first, as in the Plan timeline.
	label, _, _ = next(engine.Result{
		Milestones: []engine.MilestoneHit{hit("Jubilación", m(2030, 1))},
		PaidOff:    []engine.DebtPaidOff{debt("Coche", m(2030, 1))},
	})
	if label != "Jubilación" {
		t.Errorf("tie: got %q, want the milestone", label)
	}

	// A debt paid off and a property sale count, the earliest wins.
	label, month, _ = next(engine.Result{
		Milestones: []engine.MilestoneHit{hit("Jubilación", m(2040, 1))},
		PaidOff:    []engine.DebtPaidOff{debt("Coche", m(2029, 8))},
		Sales:      []engine.PropertySold{{Account: domain.Account{Name: "Piso"}, Month: m(2028, 2)}},
	})
	if label != "Venta de Piso" || month != m(2028, 2) {
		t.Errorf("got %q %v, want the sale", label, month)
	}
}

func TestHomeFollowsTheActivePlan(t *testing.T) {
	s, st := newFinanceServer(t)
	seedMC(t, s) // plan 1 ("Mi plan") has people
	empty, err := st.CreatePlanFromSnapshot(t.Context(), "Vacío", nil, store.Snapshot{Assumptions: engine.DefaultAssumptions()})
	if err != nil {
		t.Fatal(err)
	}
	switchTo := func(id int64) string {
		rec := postForm(s, "/plans/switch", url.Values{"plan": {fmt.Sprint(id)}, "next": {"/"}})
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("switch = %d", rec.Code)
		}
		_, body := do(s, httptest.NewRequest("GET", "/", nil))
		return body
	}
	if body := switchTo(empty); !strings.Contains(body, `id="demo-chart"`) || strings.Contains(body, "Tu plan de un vistazo") {
		t.Error("an empty active plan should show the demo")
	}
	if body := switchTo(1); !strings.Contains(body, "Tu plan de un vistazo") || !strings.Contains(body, "Mi plan") {
		t.Error("switching back should show the card with the plan's name")
	}
}

func TestGlanceChipCarriesErrorLabel(t *testing.T) {
	s, _ := newFinanceServer(t)
	seedMC(t, s)
	_, home := do(s, httptest.NewRequest("GET", "/", nil))
	for _, want := range []string{`id="glance-chip"`, `data-error-label="Sin resultado"`, `<section class="card hero-card glance" aria-labelledby="glance-heading">`} {
		if !strings.Contains(home, want) {
			t.Errorf("home missing %q", want)
		}
	}
}
