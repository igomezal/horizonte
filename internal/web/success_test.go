package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/igomezal/horizonte/internal/domain"
)

func seedMC(t *testing.T, s *Server) {
	t.Helper()
	seedCashflow(t, s)
	mustPost(t, s, "/finances/milestones", url.Values{"name": {"FIRE"}, "kind": {"fi"}, "fi_variant": {"standard"}, "multiple": {"25"}})
}

func TestSuccessPage(t *testing.T) {
	s, _ := newFinanceServer(t)
	seedMC(t, s)
	rec, body := do(s, httptest.NewRequest("GET", "/success", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	for _, want := range []string{
		"Probabilidad de éxito", "tu dinero llega hasta los 90 años", "escenarios simulados",
		`"type":"fan"`, "te queda dinero cada año", "Mejor caso", ">FIRE<", "Volatilidad",
		`name="vol_investment"`, `value="16"`, `hx-get="/success/results?seed=1"`, `aria-current="page"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("success page missing %q", want)
		}
	}
	if !regexp.MustCompile(`<span class="figure">\d+\x{00a0}%</span>`).MatchString(body) {
		t.Error("hero figure should be a percentage")
	}

	// Same seed, same result; reseeding changes the seed in the refresh URL.
	_, a := do(s, htmxReq("GET", "/success/results?seed=1", nil))
	_, b := do(s, htmxReq("GET", "/success/results?seed=1", nil))
	if a != b {
		t.Error("results with the same seed must be identical")
	}
	_, c := do(s, htmxReq("GET", "/success/results?reseed=1", nil))
	if strings.Contains(c, `hx-get="/success/results?seed=1"`) {
		t.Error("reseeding should carry a new seed")
	}
}

func TestSaveMCSettingsKeepsPlanAssumptions(t *testing.T) {
	s, st := newFinanceServer(t)
	seedMC(t, s)
	// Change a Plan assumption first.
	plan := url.Values{"inflation": {"3"}, "tax_rate": {"20"}, "surplus": {""}, "return_cash": {"0"},
		"return_deposit": {"2"}, "return_investment": {"7"}, "return_pension": {"5"}, "return_realestate": {"3"}, "return_other": {"2"}}
	if rec, _ := do(s, htmxReq("POST", "/plan/assumptions", plan)); rec.Code != http.StatusOK {
		t.Fatal("save plan assumptions")
	}

	form := url.Values{"vol_cash": {"0"}, "vol_deposit": {"1"}, "vol_investment": {"20"}, "vol_pension": {"10"},
		"vol_realestate": {"8"}, "vol_other": {"5"}, "mc_runs": {"500"}}
	rec, body := do(s, htmxReq("POST", "/success/settings", form))
	if rec.Code != http.StatusOK || rec.Header().Get("HX-Trigger") != evSuccessChanged || !strings.Contains(body, "Guardado") {
		t.Fatalf("save settings: %d %q", rec.Code, rec.Header().Get("HX-Trigger"))
	}
	p, _ := st.CurrentPlan(t.Context(), "x")
	a, _ := st.GetAssumptions(t.Context(), p)
	if a.Volatility[domain.AccountInvestment] != 0.2 || a.MCRuns != 500 {
		t.Errorf("settings not stored: %v %d", a.Volatility, a.MCRuns)
	}
	if a.Inflation != 0.03 || a.Returns[domain.AccountInvestment] != 0.07 {
		t.Errorf("Monte Carlo settings overwrote the Plan assumptions: %+v", a)
	}

	// Saving the Plan form again keeps the Monte Carlo settings.
	do(s, htmxReq("POST", "/plan/assumptions", plan))
	if a, _ = st.GetAssumptions(t.Context(), p); a.MCRuns != 500 || a.Volatility[domain.AccountInvestment] != 0.2 {
		t.Errorf("Plan form reset the Monte Carlo settings: %v %d", a.Volatility, a.MCRuns)
	}

	form.Set("vol_investment", "90")
	form.Set("mc_runs", "7")
	rec, body = do(s, htmxReq("POST", "/success/settings", form))
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(body, "fuera del rango") {
		t.Errorf("invalid settings: %d", rec.Code)
	}
}
