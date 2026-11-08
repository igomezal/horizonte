package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/igomezal/horizonte/internal/domain"
	"github.com/igomezal/horizonte/internal/engine"
	"github.com/igomezal/horizonte/internal/i18n"
)

// The test clock is 15 nov 2026 (see newFinanceServer).
var nov2026 = domain.Month{Year: 2026, Month: 11}

func englishReq(req *http.Request) *http.Request {
	req.AddCookie(&http.Cookie{Name: i18n.CookieName, Value: "en"})
	return req
}

func chartOf(t *testing.T, body string) trackingChart {
	t.Helper()
	m := regexp.MustCompile(`(?s)id="tracking-chart">(.*?)</script>`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no chart in the page:\n%s", body)
	}
	var c trackingChart
	if err := json.Unmarshal([]byte(m[1]), &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestTrackingEmptyStates(t *testing.T) {
	s, st := newFinanceServer(t)
	ctx := t.Context()

	rec, body := do(s, httptest.NewRequest("GET", "/tracking", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	for _, want := range []string{"Aún no hay nada que comparar", "Registrar este mes", "euros nominales", `href="/tracking"`, "Añade al menos una persona"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, `id="tracking-chart"`) {
		t.Error("a chart with nothing to draw")
	}

	// Entries, no baseline.
	st.AddNetWorthEntry(ctx, domain.NetWorthEntry{Month: nov2026, Balances: map[domain.AccountKind]domain.Cents{domain.AccountCash: 100_00}})
	_, body = do(s, httptest.NewRequest("GET", "/tracking", nil))
	if !strings.Contains(body, "Guarda una línea base") || !strings.Contains(body, "Editar este mes") {
		t.Errorf("page with entries and no baseline:\n%s", body)
	}

	// Baseline, no entries.
	st.DeleteNetWorthEntry(ctx, nov2026)
	st.SaveNetWorthBaseline(ctx, domain.NetWorthBaseline{PlanName: "Plan", SavedOn: s.now(), Points: []domain.NetWorthPoint{{Month: nov2026, NetWorth: 1}}})
	_, body = do(s, httptest.NewRequest("GET", "/tracking", nil))
	if !strings.Contains(body, "Aún no has registrado ningún mes") || !strings.Contains(body, "Guardada el 15 nov 2026") {
		t.Errorf("page with a baseline and no entries:\n%s", body)
	}
}

func TestTrackingPageInEnglish(t *testing.T) {
	s, _ := newFinanceServer(t)
	_, body := do(s, englishReq(httptest.NewRequest("GET", "/tracking", nil)))
	for _, want := range []string{"Net worth tracking", "nominal euros", "Record this month"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func TestTrackingRecordThisMonthIsPrefilled(t *testing.T) {
	s, _ := newFinanceServer(t)
	mustPost(t, s, "/finances/accounts", url.Values{"name": {"A"}, "kind": {"cash"}, "balance": {"1000"}})
	mustPost(t, s, "/finances/accounts", url.Values{"name": {"B"}, "kind": {"cash"}, "balance": {"500,50"}})
	mustPost(t, s, "/finances/accounts", url.Values{"name": {"C"}, "kind": {"investment"}, "balance": {"2000"}})
	mustPost(t, s, "/finances/debts", url.Values{"name": {"D"}, "kind": {"car"}, "balance": {"300"}, "rate": {"0"}, "payment": {"10"}})

	rec, body := do(s, htmxReq("GET", "/tracking/entries/new?prefill=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	for _, want := range []string{`value="2026-11"`, `value="1500,5"`, `value="2000"`, `value="300"`, "Rellenado con los saldos"} {
		if !strings.Contains(body, want) {
			t.Errorf("form lacks %q:\n%s", want, body)
		}
	}

	_, blank := do(s, htmxReq("GET", "/tracking/entries/new", nil))
	if strings.Contains(blank, `value="2026-11"`) || strings.Contains(blank, `value="2000"`) {
		t.Errorf("the form for a past month is not blank:\n%s", blank)
	}
}

func TestTrackingAddEditDelete(t *testing.T) {
	s, st := newFinanceServer(t)
	ctx := t.Context()

	body := mustPost(t, s, "/tracking/entries", url.Values{"month": {"2026-10"}, "balance_cash": {"1.000,50"}, "debt": {"200,50"}})
	if !strings.Contains(body, `id="tracking-body"`) || !strings.Contains(body, ">800") {
		t.Errorf("after adding:\n%s", body)
	}
	e, err := st.GetNetWorthEntry(ctx, domain.Month{Year: 2026, Month: 10})
	if err != nil || e.Balances[domain.AccountCash] != 1000_50 || e.Debt != 200_50 {
		t.Fatalf("stored = %+v, %v", e, err)
	}

	rec, body := do(s, htmxReq("GET", "/tracking/entries/2026-10/edit", nil))
	if rec.Code != http.StatusOK || !strings.Contains(body, `hx-put="/tracking/entries/2026-10"`) || !strings.Contains(body, `value="1000,5"`) {
		t.Fatalf("edit form: %d\n%s", rec.Code, body)
	}

	rec, _ = do(s, htmxReq("PUT", "/tracking/entries/2026-10", url.Values{"balance_pension": {"5"}}))
	if rec.Code != http.StatusOK {
		t.Fatalf("edit: %d", rec.Code)
	}
	e, _ = st.GetNetWorthEntry(ctx, domain.Month{Year: 2026, Month: 10})
	if len(e.Balances) != 1 || e.Balances[domain.AccountPension] != 5_00 || e.Debt != 0 {
		t.Errorf("after edit = %+v", e)
	}

	rec, _ = do(s, htmxReq("DELETE", "/tracking/entries/2026-10", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rows, _ := st.ListNetWorthEntries(ctx); len(rows) != 0 {
		t.Errorf("entries after delete = %+v", rows)
	}
	if rec, _ := do(s, htmxReq("DELETE", "/tracking/entries/2026-10", nil)); rec.Code != http.StatusOK {
		t.Errorf("deleting twice: %d", rec.Code)
	}
}

func TestTrackingFormValidation(t *testing.T) {
	s, st := newFinanceServer(t)
	mustPost(t, s, "/tracking/entries", url.Values{"month": {"2026-10"}, "balance_cash": {"1"}})

	cases := []struct {
		name string
		form url.Values
		want string
	}{
		{"no month", url.Values{"balance_cash": {"1"}}, "Rellena este campo"},
		{"bad month", url.Values{"month": {"nope"}, "balance_cash": {"1"}}, "Elige un mes válido"},
		{"future month", url.Values{"month": {"2026-12"}, "balance_cash": {"1"}}, "no puede estar en el futuro"},
		{"same month twice", url.Values{"month": {"2026-10"}, "balance_cash": {"99"}}, "ya está registrado"},
		{"no amounts", url.Values{"month": {"2026-09"}}, "al menos un importe"},
		{"not a number", url.Values{"month": {"2026-09"}, "debt": {"abc"}}, "Escribe un número"},
		{"negative", url.Values{"month": {"2026-09"}, "balance_cash": {"-5"}}, "no puede ser negativo"},
	}
	for _, c := range cases {
		rec, body := do(s, htmxReq("POST", "/tracking/entries", c.form))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: status = %d", c.name, rec.Code)
		}
		if rec.Header().Get("HX-Retarget") != "#tracking-entry-form" {
			t.Errorf("%s: HX-Retarget = %q", c.name, rec.Header().Get("HX-Retarget"))
		}
		if !strings.Contains(body, c.want) {
			t.Errorf("%s: response lacks %q:\n%s", c.name, c.want, body)
		}
	}
	rows, _ := st.ListNetWorthEntries(t.Context())
	if len(rows) != 1 || rows[0].Balances[domain.AccountCash] != 1_00 {
		t.Errorf("a rejected form changed the data: %+v", rows)
	}

	// An entry deleted in another tab.
	if rec, _ := do(s, htmxReq("PUT", "/tracking/entries/2020-01", url.Values{"debt": {"1"}})); rec.Code != http.StatusNotFound {
		t.Errorf("edit of a missing month: %d", rec.Code)
	}
	if rec, _ := do(s, htmxReq("GET", "/tracking/entries/2020-01/edit", nil)); rec.Code != http.StatusNotFound {
		t.Errorf("edit form of a missing month: %d", rec.Code)
	}
	if rec, _ := do(s, htmxReq("GET", "/tracking/entries/nope/edit", nil)); rec.Code != http.StatusNotFound {
		t.Errorf("edit form of a bad month: %d", rec.Code)
	}
}

func TestTrackingBaselineSaveAndReplace(t *testing.T) {
	s, st := newFinanceServer(t)
	ctx := t.Context()
	seedPlan(t, s)
	plan, _ := st.CurrentPlan(ctx, "x")
	st.RenamePlan(ctx, plan, "Plan base")

	body := mustPost(t, s, "/tracking/baseline", url.Values{})
	if !strings.Contains(body, "Guardada el 15 nov 2026") || !strings.Contains(body, "Plan base") {
		t.Errorf("after saving:\n%s", body)
	}
	b, err := st.GetNetWorthBaseline(ctx)
	if err != nil || b == nil || b.PlanName != "Plan base" || len(b.Points) == 0 || b.Points[0].Month != nov2026 {
		t.Fatalf("baseline = %+v, %v", b, err)
	}

	// The path is the projection's, in nominal euros.
	in, _ := s.loadInput(ctx, finCtx{plan: plan, now: nov2026})
	in.Start = nov2026
	last := b.Points[len(b.Points)-1]
	months := engine.Simulate(in).Months
	if want := domain.FromEuros(months[len(months)-1].NetWorth); len(months) != len(b.Points) || last.NetWorth != want {
		t.Errorf("last point = %v of %d, want the nominal projection %v of %d", last.NetWorth, len(b.Points), want, len(months))
	}

	// Without the confirmation, an existing baseline stays.
	st.RenamePlan(ctx, plan, "Otro nombre")
	rec, body := do(s, htmxReq("POST", "/tracking/baseline", url.Values{}))
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(body, "Ya hay una línea base") {
		t.Fatalf("no confirmation: %d\n%s", rec.Code, body)
	}
	if got, _ := st.GetNetWorthBaseline(ctx); got.PlanName != "Plan base" {
		t.Errorf("replaced without confirmation: %s", got.PlanName)
	}

	if body := mustPost(t, s, "/tracking/baseline", url.Values{"replace": {"1"}}); !strings.Contains(body, "Otro nombre") {
		t.Errorf("after replacing:\n%s", body)
	}
	if got, _ := st.GetNetWorthBaseline(ctx); got.PlanName != "Otro nombre" {
		t.Errorf("not replaced: %s", got.PlanName)
	}
}

func TestTrackingBaselineNeedsAProjection(t *testing.T) {
	s, st := newFinanceServer(t)
	rec, body := do(s, htmxReq("POST", "/tracking/baseline", url.Values{}))
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(body, "Añade al menos una persona") {
		t.Fatalf("status %d\n%s", rec.Code, body)
	}
	if b, _ := st.GetNetWorthBaseline(t.Context()); b != nil {
		t.Errorf("baseline = %+v", b)
	}
}

func TestTrackingTableAndChart(t *testing.T) {
	s, st := newFinanceServer(t)
	ctx := t.Context()
	cash := func(c domain.Cents) map[domain.AccountKind]domain.Cents {
		return map[domain.AccountKind]domain.Cents{domain.AccountCash: c}
	}
	// September is older than the baseline; October is not recorded.
	st.AddNetWorthEntry(ctx, domain.NetWorthEntry{Month: domain.Month{Year: 2026, Month: 8}, Balances: cash(900_00)})
	st.AddNetWorthEntry(ctx, domain.NetWorthEntry{Month: nov2026, Balances: cash(1000_00), Debt: 100_00})
	points := []domain.NetWorthPoint{}
	for i := range 240 { // far past the chart's horizon
		points = append(points, domain.NetWorthPoint{Month: domain.Month{Year: 2026, Month: 10}.AddMonths(i), NetWorth: 1000_00})
	}
	st.SaveNetWorthBaseline(ctx, domain.NetWorthBaseline{PlanName: "P", SavedOn: s.now(), Points: points})

	_, body := do(s, httptest.NewRequest("GET", "/tracking", nil))
	// 900 − 1.000 = −100, below the baseline, and only that is red.
	if !strings.Contains(body, "ibas por debajo de la línea base") {
		t.Errorf("no verdict:\n%s", body)
	}
	if n := strings.Count(body, `class="num negative"`); n != 1 {
		t.Errorf("%d negative cells, want 1", n)
	}
	if strings.Contains(body, "positive") {
		t.Error("a positive value is highlighted")
	}
	if nov := strings.Index(body, "nov 2026</th>"); nov < 0 || nov > strings.Index(body, "ago 2026</th>") {
		t.Error("the table should list the newest month first")
	}

	c := chartOf(t, body)
	// From the first entry to 12 months after today.
	if len(c.Categories) != 16 || c.Categories[0] != "ago 2026" || c.Categories[15] != "nov 2027" {
		t.Fatalf("categories = %v", c.Categories)
	}
	if len(c.Actual) != 16 || len(c.Baseline) != 16 {
		t.Fatalf("series lengths = %d, %d", len(c.Actual), len(c.Baseline))
	}
	if c.Actual[0] == nil || *c.Actual[0] != 900 || c.Actual[1] != nil || c.Actual[3] == nil || *c.Actual[3] != 900 {
		t.Errorf("actual = %v", c.Actual)
	}
	if c.Baseline[0] != nil || c.Baseline[2] == nil || *c.Baseline[2] != 1000 {
		t.Errorf("baseline = %v", c.Baseline)
	}
}

func TestTrackingAllPlansExportAndImport(t *testing.T) {
	s, st := newFinanceServer(t)
	ctx := t.Context()
	seedPlan(t, s)
	oct := domain.Month{Year: 2026, Month: 10}
	if err := st.AddNetWorthEntry(ctx, domain.NetWorthEntry{Month: oct, Debt: 1_00}); err != nil {
		t.Fatal(err)
	}
	mustPost(t, s, "/tracking/baseline", url.Values{})

	_, plan := do(s, httptest.NewRequest("GET", "/scenarios/1/export", nil))
	if strings.Contains(plan, `"tracking"`) {
		t.Error("a single plan's file should not carry the household history")
	}
	_, bundle := do(s, httptest.NewRequest("GET", "/scenarios/export", nil))
	if !strings.Contains(bundle, `"tracking"`) || !strings.Contains(bundle, `"plan_name"`) {
		t.Fatalf("bundle lacks the history: %.300s", bundle)
	}

	// Change what is stored, then import the older file over it.
	if err := st.UpdateNetWorthEntry(ctx, domain.NetWorthEntry{Month: oct, Debt: 2_00}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddNetWorthEntry(ctx, domain.NetWorthEntry{Month: nov2026, Debt: 3_00}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveNetWorthBaseline(ctx, domain.NetWorthBaseline{PlanName: "Newer", SavedOn: s.now(), Points: []domain.NetWorthPoint{{Month: nov2026, NetWorth: 1}}}); err != nil {
		t.Fatal(err)
	}
	if rec, body := uploadPlan(s, bundle); rec.Code != http.StatusSeeOther {
		t.Fatalf("import: %d %s", rec.Code, body)
	}
	if e, _ := st.GetNetWorthEntry(ctx, oct); e.Debt != 2_00 {
		t.Errorf("import overwrote an existing month: %+v", e)
	}
	if b, _ := st.GetNetWorthBaseline(ctx); b.PlanName != "Newer" {
		t.Errorf("import replaced the baseline: %s", b.PlanName)
	}

	// Into an empty database, everything is added.
	other, otherStore := newFinanceServer(t)
	if rec, body := uploadPlan(other, bundle); rec.Code != http.StatusSeeOther {
		t.Fatalf("import: %d %s", rec.Code, body)
	}
	if rows, _ := otherStore.ListNetWorthEntries(ctx); len(rows) != 1 || rows[0].Month != oct {
		t.Errorf("entries = %+v", rows)
	}
	if b, _ := otherStore.GetNetWorthBaseline(ctx); b == nil || len(b.Points) == 0 {
		t.Errorf("baseline = %+v", b)
	}
}

func TestTrackingBadImportChangesNothing(t *testing.T) {
	s, st := newFinanceServer(t)
	seedPlan(t, s)
	_, bundle := do(s, httptest.NewRequest("GET", "/scenarios/export", nil))
	bad := strings.Replace(bundle, `"plans": [`, `"tracking": {"entries": [{"month": "2030-01", "debt": 1}]}, "plans": [`, 1)

	before, _ := st.ListPlans(t.Context())
	rec, body := uploadPlan(s, bad)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(body, "seguimiento del patrimonio") {
		t.Fatalf("import of a future month: %d\n%s", rec.Code, body)
	}
	if after, _ := st.ListPlans(t.Context()); len(after) != len(before) {
		t.Errorf("plans were created: %d then %d", len(before), len(after))
	}
	if rows, _ := st.ListNetWorthEntries(t.Context()); len(rows) != 0 {
		t.Errorf("entries = %+v", rows)
	}
}

// A store error surfaces as a server error, not as an empty page.
func TestTrackingCatchesErrors(t *testing.T) {
	s, st := newFinanceServer(t)
	if err := st.DB.Close(); err != nil {
		t.Fatal(err)
	}
	if rec, _ := do(s, httptest.NewRequest("GET", "/tracking", nil)); rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d", rec.Code)
	}
}
