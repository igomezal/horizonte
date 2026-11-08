package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/igomezal/horizonte/internal/engine"
)

func baseAssumptionsForm() url.Values {
	return url.Values{"inflation": {"2,5"}, "tax_rate": {"20"}, "surplus": {""}, "return_cash": {"0"},
		"return_deposit": {"2"}, "return_investment": {"6"}, "return_pension": {"5"}, "return_realestate": {"3"}, "return_other": {"2"}}
}

func TestPrepaymentReserveFromPlanForm(t *testing.T) {
	s, st := newFinanceServer(t)
	plan, _ := st.CurrentPlan(t.Context(), "x")
	form := baseAssumptionsForm()
	form.Set("prepay_reserve", "10000")
	rec, body := do(s, htmxReq("POST", "/plan/assumptions", form))
	if rec.Code != http.StatusOK || !strings.Contains(body, `name="prepay_reserve"`) {
		t.Fatalf("save: %d %s", rec.Code, body)
	}
	if a, _ := st.GetAssumptions(t.Context(), plan); a.PrepaymentReserve != 1000000 {
		t.Errorf("reserve = %d", a.PrepaymentReserve)
	}

	// A request without the field leaves it alone.
	form.Del("prepay_reserve")
	if rec, _ := do(s, htmxReq("POST", "/plan/assumptions", form)); rec.Code != http.StatusOK {
		t.Fatalf("save without reserve: %d", rec.Code)
	}
	if a, _ := st.GetAssumptions(t.Context(), plan); a.PrepaymentReserve != 1000000 {
		t.Errorf("reserve after a partial save = %d", a.PrepaymentReserve)
	}

	form.Set("prepay_reserve", "-5")
	if rec, _ := do(s, htmxReq("POST", "/plan/assumptions", form)); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("negative reserve: %d", rec.Code)
	}
}

// Inputs added to the plan must reach the keys, or stale results get served.
func TestSimKeysSeePlanSettings(t *testing.T) {
	base := func() engine.Input { return engine.Input{Assumptions: engine.DefaultAssumptions()} }
	set := engine.MCSettings{Runs: 10, Seed: 1}
	for name, change := range map[string]func(*engine.Input){
		"prepayment reserve": func(in *engine.Input) { in.Assumptions.PrepaymentReserve = 100 },
	} {
		in := base()
		change(&in)
		if mcKey(in, set) == mcKey(base(), set) || rollingKey(in) == rollingKey(base()) {
			t.Errorf("%s does not change the simulation keys", name)
		}
	}
}
