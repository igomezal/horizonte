package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/igomezal/horizonte/internal/engine"
)

// Every region is offered with a translated label, the form says which systems
// are not modelled, and the assumptions form accepts every region.
func TestTaxRegionsInPlanForm(t *testing.T) {
	s, st := newFinanceServer(t)
	seedPlan(t, s)
	_, body := do(s, httptest.NewRequest("GET", "/plan", nil))
	if strings.Contains(body, "tax.region.") {
		t.Error("a region label is missing from the catalog")
	}
	if !strings.Contains(body, "País Vasco y Navarra") {
		t.Error("the form should say the foral systems are not modelled")
	}
	form := url.Values{"inflation": {"2"}, "tax_model": {"es"}, "tax_rate": {"20"}, "surplus": {""},
		"return_cash": {"0"}, "return_deposit": {"2"}, "return_investment": {"7"},
		"return_pension": {"5"}, "return_realestate": {"3"}, "return_other": {"2"}}
	plan, _ := st.CurrentPlan(t.Context(), "x")
	for _, key := range engine.TaxRegions() {
		if !strings.Contains(body, `value="`+key+`"`) {
			t.Errorf("%s: not offered in the selector", key)
		}
		form.Set("tax_region", key)
		if rec, b := do(s, htmxReq("POST", "/plan/assumptions", form)); rec.Code != http.StatusOK {
			t.Fatalf("%s: save: %d %s", key, rec.Code, b)
		}
		if a, _ := st.GetAssumptions(t.Context(), plan); a.TaxRegion != key {
			t.Errorf("stored %q, want %q", a.TaxRegion, key)
		}
	}
	form.Set("tax_region", "navarra")
	if rec, _ := do(s, htmxReq("POST", "/plan/assumptions", form)); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("foral region accepted: %d", rec.Code)
	}
}
