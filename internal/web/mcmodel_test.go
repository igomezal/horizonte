package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/igomezal/horizonte/internal/domain"
	"github.com/igomezal/horizonte/internal/engine"
)

func mcForm(model string) url.Values {
	form := url.Values{"vol_cash": {"0"}, "vol_deposit": {"1"}, "vol_investment": {"16"}, "vol_pension": {"10"},
		"vol_realestate": {"8"}, "vol_other": {"5"}, "mc_runs": {"250"}}
	if model != "" {
		form.Set("mc_model", model)
	}
	return form
}

func planModel(t *testing.T, s *Server) string {
	t.Helper()
	id, err := s.store.CurrentPlan(t.Context(), "x")
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.store.GetAssumptions(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return a.MCModel
}

func TestMCModelSetting(t *testing.T) {
	s, _ := newFinanceServer(t)
	seedMC(t, s)

	_, body := do(s, httptest.NewRequest("GET", "/success", nil))
	for _, want := range []string{`name="mc_model"`, `<option value="parametric" selected>Aleatorio</option>`, `<option value="bootstrap">Bloques históricos</option>`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if got := planModel(t, s); got != engine.MCParametric {
		t.Errorf("default model = %q", got)
	}

	rec, body := do(s, htmxReq("POST", "/success/settings", mcForm("bootstrap")))
	if rec.Code != http.StatusOK || rec.Header().Get("HX-Trigger") != evSuccessChanged {
		t.Fatalf("save: %d %q", rec.Code, rec.Header().Get("HX-Trigger"))
	}
	if got := planModel(t, s); got != engine.MCBootstrap {
		t.Errorf("saved model = %q", got)
	}
	if !strings.Contains(body, `<option value="bootstrap" selected>`) {
		t.Error("the form should show the saved model")
	}

	// A post without the field keeps the plan's model.
	if rec, _ := do(s, htmxReq("POST", "/success/settings", mcForm(""))); rec.Code != http.StatusOK {
		t.Fatalf("save without model: %d", rec.Code)
	}
	if got := planModel(t, s); got != engine.MCBootstrap {
		t.Errorf("model after a post without it = %q", got)
	}

	// The Plan form does not edit the model.
	plan := url.Values{"inflation": {"3"}, "tax_rate": {"20"}, "surplus": {""}, "return_cash": {"0"},
		"return_deposit": {"2"}, "return_investment": {"7"}, "return_pension": {"5"}, "return_realestate": {"3"}, "return_other": {"2"}}
	if rec, _ := do(s, htmxReq("POST", "/plan/assumptions", plan)); rec.Code != http.StatusOK {
		t.Fatal("save plan assumptions")
	}
	if got := planModel(t, s); got != engine.MCBootstrap {
		t.Errorf("Plan form reset the model to %q", got)
	}

	// An unknown model is refused and nothing changes.
	if rec, _ := do(s, htmxReq("POST", "/success/settings", mcForm("magic"))); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("unknown model: %d", rec.Code)
	}
	if got := planModel(t, s); got != engine.MCBootstrap {
		t.Errorf("model after a refused post = %q", got)
	}

	// The results come from the chosen model.
	rec, body = do(s, htmxReq("GET", "/success/results", nil))
	if rec.Code != http.StatusOK || !strings.Contains(body, "250") {
		t.Errorf("results: %d", rec.Code)
	}
}

func TestSuccessPageExplainsTheModel(t *testing.T) {
	s, _ := newFinanceServer(t)
	seedMC(t, s)
	const median, parametric, bootstrap = "resultado mediano", "Cada escenario usa rentabilidades mensuales aleatorias", "Modelo de bloques históricos"

	_, page := do(s, httptest.NewRequest("GET", "/success", nil))
	_, results := do(s, htmxReq("GET", "/success/results", nil))
	for name, body := range map[string]string{"page": page, "results": results} {
		if !strings.Contains(body, median) || !strings.Contains(body, "1,4 puntos") || !strings.Contains(body, parametric) || strings.Contains(body, bootstrap) {
			t.Errorf("parametric %s does not explain its model", name)
		}
	}

	mustPost(t, s, "/success/settings", mcForm("bootstrap"))
	_, page = do(s, httptest.NewRequest("GET", "/success", nil))
	_, results = do(s, htmxReq("GET", "/success/results", nil))
	for name, body := range map[string]string{"page": page, "results": results} {
		if !strings.Contains(body, median) || !strings.Contains(body, bootstrap) || strings.Contains(body, parametric) {
			t.Errorf("bootstrap %s does not explain its model", name)
		}
	}
}

func TestBacktestSaysItIsUSDollars(t *testing.T) {
	s, _ := newFinanceServer(t)
	seedMC(t, s)
	_, body := do(s, httptest.NewRequest("GET", "/backtest", nil))
	if !strings.Contains(body, "medidos en dólares") || !strings.Contains(body, "inversor en euros") {
		t.Error("the backtest page should say it replays US data in dollars")
	}
}

func TestMCKeyDependsOnTheModel(t *testing.T) {
	in := engine.Input{Assumptions: engine.DefaultAssumptions(), Start: domain.Month{Year: 2026, Month: 1}}
	set := mcSettings(in, defaultSeed)
	parametric := mcKey(in, set)
	in.Assumptions.MCModel = engine.MCBootstrap
	if mcKey(in, set) == parametric {
		t.Error("changing the model must change the cache key")
	}
}

func TestWarmUsesThePlansModel(t *testing.T) {
	s := newTestServer(t)
	id := seedPlans(t, s, 1)[0]
	a, err := s.store.GetAssumptions(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	a.MCModel = engine.MCBootstrap
	if err := s.store.SaveAssumptions(t.Context(), id, a); err != nil {
		t.Fatal(err)
	}
	if res := s.Warm(t.Context()); res.Outcome != warmCompleted || res.Warmed != 1 {
		t.Fatalf("result = %+v", res)
	}
	if !planIsWarm(t, s, id) {
		t.Error("the warm-up should fill the keys of the plan's model")
	}

	in, err := s.loadInput(t.Context(), finCtx{plan: id, now: domain.MonthOf(s.now())})
	if err != nil {
		t.Fatal(err)
	}
	warm, ok := cachedSim[engine.MCResult](s.sims, mcKey(in, mcSettings(in, defaultSeed)))
	if !ok {
		t.Fatal("not cached")
	}
	direct := engine.MonteCarlo(in, in.Assumptions.Volatility, mcSettings(in, defaultSeed))
	if warm.Successes != direct.Successes || warm.NetWorth[0] != direct.NetWorth[0] {
		t.Errorf("the warm-up ran another model: %d vs %d successes", warm.Successes, direct.Successes)
	}
}

func TestSuccessResultsWithoutPeopleNamesTheModel(t *testing.T) {
	s, _ := newFinanceServer(t)
	rec, body := do(s, htmxReq("GET", "/success/results", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if strings.Contains(body, "mc.method.") {
		t.Errorf("raw translation key in the page: %s", body)
	}
}
