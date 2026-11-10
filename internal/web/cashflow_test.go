package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/igomezal/horizonte/internal/engine"
)

func seedCashflow(t *testing.T, s *Server) {
	t.Helper()
	mustPost(t, s, "/finances/people", url.Values{"name": {"Ana"}, "birth": {"1990-04"}, "life_expectancy": {"90"}})
	mustPost(t, s, "/finances/milestones", url.Values{"name": {"Jubilación"}, "kind": {"age"}, "person": {"1"}, "age": {"65"}})
	mustPost(t, s, "/finances/milestones", url.Values{"name": {"Pensión"}, "kind": {"age"}, "person": {"1"}, "age": {"67"}})
	mustPost(t, s, "/finances/accounts", url.Values{"name": {"Cuenta"}, "kind": {"cash"}, "balance": {"40000"}})
	mustPost(t, s, "/finances/accounts", url.Values{"name": {"Fondo"}, "kind": {"investment"}, "balance": {"20000"}, "contribution": {"300"}, "contrib_end_ms": {"1"}})
	mustPost(t, s, "/finances/incomes", url.Values{"name": {"Nómina"}, "kind": {"salary"}, "amount": {"2500"}, "payments": {"14"}, "end_ms": {"1"}})
	mustPost(t, s, "/finances/incomes", url.Values{"name": {"Pensión pública"}, "kind": {"pension"}, "amount": {"1800"}, "payments": {"14"}, "start_ms": {"2"}})
	mustPost(t, s, "/finances/expenses", url.Values{"name": {"Alquiler"}, "category": {"housing"}, "amount": {"900"}, "payments": {"12"}})
	mustPost(t, s, "/finances/expenses", url.Values{"name": {"Comida"}, "category": {"food"}, "amount": {"400"}, "payments": {"12"}})
	mustPost(t, s, "/finances/debts", url.Values{"name": {"Coche"}, "kind": {"car"}, "balance": {"6000"}, "rate": {"0"}, "payment": {"250"}})
}

// sankeyOf extracts the Sankey JSON of the rendered year.
func sankeyOf(t *testing.T, body string) sankeyChart {
	t.Helper()
	m := regexp.MustCompile(`<script type="application/json" id="cf-sankey-\d+">(.*?)</script>`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no sankey data in the page")
	}
	var ch sankeyChart
	if err := json.Unmarshal([]byte(m[1]), &ch); err != nil {
		t.Fatal(err)
	}
	return ch
}

func TestCashflowPageDefaultsToFirstFullYear(t *testing.T) {
	s, _ := newFinanceServer(t)
	seedCashflow(t, s)
	rec, body := do(s, httptest.NewRequest("GET", "/cashflow", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	for _, want := range []string{"Flujo de caja", `value="2027"`, `min="2026"`, `max="2080"`, "En 2027, con 37 años",
		`"type":"savings-rate"`, `"type":"stacked-bars"`, "Tu dinero", `aria-current="page"`} {
		if !strings.Contains(body, want) {
			t.Errorf("cash flow page missing %q", want)
		}
	}
	// Inflows and outflows balance in the diagram.
	ch := sankeyOf(t, body)
	var in, out float64
	for _, l := range ch.Links {
		if l.Target == "center" {
			in += l.Value
		} else {
			out += l.Value
		}
	}
	if in == 0 || in-out > 10 || out-in > 10 { // rounding to whole euros per node
		t.Errorf("sankey unbalanced: in %v, out %v", in, out)
	}
}

func TestCashflowYearWithoutIncome(t *testing.T) {
	s, _ := newFinanceServer(t)
	seedCashflow(t, s)
	// 2056: retired at 65 (Apr 2055), pension from 67 (Apr 2057): no income at all.
	rec, body := do(s, htmxReq("GET", "/cashflow/year?y=2056&real=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, body)
	}
	if got := rec.Header().Get("HX-Push-Url"); got != "/cashflow?real=1&y=2056" {
		t.Errorf("HX-Push-Url = %q", got)
	}
	if !strings.Contains(body, "Sin ingresos este año") || !strings.Contains(body, "De Cuenta") {
		t.Errorf("no-income year should show money drawn from savings: %s", body)
	}
	if strings.Contains(body, "<html") {
		t.Error("year endpoint must return a fragment")
	}

	// Out-of-range years are clamped to the plan.
	_, body = do(s, htmxReq("GET", "/cashflow/year?y=1999", nil))
	if !strings.Contains(body, "En 2026") || !strings.Contains(body, "solo de nov 2026 a dic 2026") {
		t.Errorf("year 1999 should clamp to the partial first year: %s", body)
	}
}

// A year whose only money in is the previous year's tax refund (June) has no
// savings rate: it used to divide the year's spending by the refund and show
// something like -3.455 %.
func TestCashflowRefundOnlyYearHasNoRate(t *testing.T) {
	s, _ := newFinanceServer(t)
	years := []engine.YearRow{
		{Year: 2062, Months: 12, Deflator: 1, Income: 20000, Tax: 5000, Expenses: 8000, Flow: &engine.Flow{}},
		{Year: 2063, Months: 12, Deflator: 1, Income: 0, Tax: -216, Expenses: 7682, Withdrawals: 7466, Flow: &engine.Flow{}},
	}
	rate, _ := s.buildCashflowCharts("es", engine.Input{}, years, planOpts{})
	data := rate.Series[0].Data
	if data[0] == nil || *data[0] != 0.4667 {
		t.Errorf("2062 rate = %v, want 0.4667", data[0])
	}
	if data[1] != nil {
		t.Errorf("2063 (refund only) rate = %v, want no bar", *data[1])
	}

	cy := s.buildCashflowYear("es", engine.Input{}, engine.Result{}, years[1], planOpts{})
	if cy.HasIncome || cy.Rate != 0 {
		t.Errorf("refund-only year: HasIncome = %v, Rate = %v", cy.HasIncome, cy.Rate)
	}
}

func TestCashflowWithoutPeople(t *testing.T) {
	s, _ := newFinanceServer(t)
	_, body := do(s, httptest.NewRequest("GET", "/cashflow", nil))
	if !strings.Contains(body, "añade al menos una persona") {
		t.Error("expected the empty state")
	}
	if rec, _ := do(s, htmxReq("GET", "/cashflow/year", nil)); rec.Code != http.StatusNotFound {
		t.Errorf("year without people = %d", rec.Code)
	}
}

func TestCashflowShowsIRPF(t *testing.T) {
	s, _ := newFinanceServer(t)
	seedCashflow(t, s)
	_, body := do(s, httptest.NewRequest("GET", "/cashflow", nil))
	for _, want := range []string{"IRPF y Seguridad Social", "simulación aproximada", "Base imponible general", "Retenido durante el año", "renta de junio"} {
		if !strings.Contains(body, want) {
			t.Errorf("cash flow page missing %q", want)
		}
	}
}
