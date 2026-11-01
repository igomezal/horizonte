package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/igomezal/horizonte/internal/domain"
)

func postForm(s *Server, path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec, _ := do(s, req)
	return rec
}

func TestCreateScenarioWithPreset(t *testing.T) {
	s, st := newFinanceServer(t)
	seedMC(t, s)
	ctx := t.Context()
	base, _ := st.CurrentPlan(ctx, "x")

	rec := postForm(s, "/scenarios", url.Values{"name": {"Pesimista"}, "source": {"1"}, "preset": {"pessimistic"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/scenarios" {
		t.Fatalf("create: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	cur, _ := st.CurrentPlan(ctx, "x")
	if cur == base {
		t.Fatal("the new scenario should become the active plan")
	}
	a, _ := st.GetAssumptions(ctx, cur)
	orig, _ := st.GetAssumptions(ctx, base)
	if a.Returns[domain.AccountInvestment] != orig.Returns[domain.AccountInvestment]-0.02 || a.Inflation != orig.Inflation+0.01 {
		t.Errorf("pessimistic preset not applied: %+v", a)
	}
	if a.Returns[domain.AccountCash] != orig.Returns[domain.AccountCash] {
		t.Error("cash return must not change")
	}

	// The comparison page lists both plans, marks the active one and draws them.
	rec2, body := do(s, httptest.NewRequest("GET", "/scenarios", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("page: %d", rec2.Code)
	}
	for _, want := range []string{"Escenarios", "Mi plan", "Pesimista", "Copia de Mi plan", "Activo",
		`"type":"scenarios"`, "Llega hasta los 90 años", `aria-current="page"`, `id="plan-switch-select"`} {
		if !strings.Contains(body, want) {
			t.Errorf("scenarios page missing %q", want)
		}
	}

	// Other pages now show the copy: Finances works on the active plan.
	_, fin := do(s, httptest.NewRequest("GET", "/finances", nil))
	if !strings.Contains(fin, `<option value="2" selected>Pesimista</option>`) {
		t.Error("the top-bar switcher should show the active scenario")
	}
}

func TestScenarioActivateRenameDelete(t *testing.T) {
	s, st := newFinanceServer(t)
	seedMC(t, s)
	ctx := t.Context()
	postForm(s, "/scenarios", url.Values{"name": {"Copia"}, "source": {"1"}, "preset": {"exact"}})

	// Switch back from the top bar and return to where we were.
	rec := postForm(s, "/plans/switch", url.Values{"plan": {"1"}, "next": {"/plan"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/plan" {
		t.Fatalf("switch: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if cur, _ := st.CurrentPlan(ctx, "x"); cur != 1 {
		t.Errorf("current = %d", cur)
	}
	postForm(s, "/scenarios/2/rename", url.Values{"name": {"Jubilarme a los 60"}})
	if p, _ := st.GetPlan(ctx, 2); p.Name != "Jubilarme a los 60" {
		t.Errorf("renamed = %q", p.Name)
	}
	postForm(s, "/scenarios/2/rename", url.Values{"name": {"   "}}) // ignored
	if p, _ := st.GetPlan(ctx, 2); p.Name != "Jubilarme a los 60" {
		t.Error("an empty name must not rename")
	}
	postForm(s, "/scenarios/2/delete", nil)
	if plans, _ := st.ListPlans(ctx); len(plans) != 1 {
		t.Errorf("plans after delete = %d", len(plans))
	}
	// The last plan cannot be deleted, and the page offers no delete button.
	postForm(s, "/scenarios/1/delete", nil)
	if plans, _ := st.ListPlans(ctx); len(plans) != 1 {
		t.Error("the last plan was deleted")
	}
	_, body := do(s, httptest.NewRequest("GET", "/scenarios", nil))
	if strings.Contains(body, "/scenarios/1/delete") {
		t.Error("no delete action for the only plan")
	}
	if strings.Contains(body, `id="plan-switch-select"`) {
		t.Error("no switcher with a single plan")
	}
}

func TestCreateScenarioValidation(t *testing.T) {
	s, _ := newFinanceServer(t)
	seedMC(t, s)
	req := httptest.NewRequest("POST", "/scenarios", strings.NewReader(url.Values{"name": {""}, "source": {"1"}, "preset": {"exact"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec, body := do(s, req)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(body, "Escribe un nombre") {
		t.Errorf("empty name: %d", rec.Code)
	}
	rec = postForm(s, "/scenarios", url.Values{"name": {"X"}, "source": {"99"}, "preset": {"exact"}})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("unknown source plan: %d", rec.Code)
	}
}
