package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBacktestPage(t *testing.T) {
	s, _ := newFinanceServer(t)
	seedMC(t, s)
	rec, body := do(s, httptest.NewRequest("GET", "/backtest", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	// The quick part comes with the page; every start year loads afterwards.
	for _, want := range []string{
		"Prueba histórica", "Gran Depresión", "Estanflación de los 70", "Burbuja puntocom", "Crisis financiera", "COVID",
		"Desde sept 1929", `"type":"scenario"`, "Crisis financiera, desde oct 2007", "Datos de Robert Shiller", "ene 1871",
		`aria-current="page"`, `hx-get="/backtest/summary"`, `hx-get="/backtest/heat"`,
		"Reproduciendo tu plan desde cada año entre 1871 y", "60 % bolsa",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("backtest page missing %q", want)
		}
	}

	_, summary := do(s, htmxReq("GET", "/backtest/summary", nil))
	_, heat := do(s, htmxReq("GET", "/backtest/heat", nil))
	for _, want := range []string{
		"Empezando en cualquier año entre 1871 y", "años de inicio salen bien", "más prudente que la historia",
		"De 1971 a 2023, con datos parciales",
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("backtest summary missing %q", want)
		}
	}
	for _, want := range []string{`"type":"start-years"`, `"year":2023,"partial":true`, `"year":1970,"col"`} {
		if !strings.Contains(heat, want) {
			t.Errorf("backtest heat map missing %q", want)
		}
	}
	if strings.Contains(summary, "<html") || strings.Contains(heat, "<html") {
		t.Error("summary and heat map should be fragments")
	}

	// Now cached: the page comes complete, without placeholders.
	_, body = do(s, httptest.NewRequest("GET", "/backtest", nil))
	if strings.Contains(body, `hx-get="/backtest/summary"`) || !strings.Contains(body, "años de inicio salen bien") ||
		!strings.Contains(body, `"type":"start-years"`) {
		t.Error("with the result cached, the page should include it directly")
	}
}

func TestBacktestScenarioEndpoint(t *testing.T) {
	s, _ := newFinanceServer(t)
	seedMC(t, s)

	_, body := do(s, htmxReq("GET", "/backtest/scenario?crisis=depression", nil))
	if !strings.Contains(body, "Gran Depresión, desde sept 1929") || strings.Contains(body, "<html") {
		t.Errorf("crisis scenario: %s", body)
	}
	// A 2020 start runs out of data after a few years and says so.
	_, body = do(s, htmxReq("GET", "/backtest/scenario?crisis=covid", nil))
	if !strings.Contains(body, "después, el plan sigue con tus rentabilidades") || !strings.Contains(body, `"dataEndIndex":`) {
		t.Errorf("covid scenario should explain the end of the data")
	}
	_, body = do(s, htmxReq("GET", "/backtest/scenario?year=1966", nil))
	if !strings.Contains(body, "Empezar en 1966, desde ene 1966") {
		t.Errorf("start-year scenario: %s", body)
	}
	// Unknown input falls back to the financial crisis.
	_, body = do(s, htmxReq("GET", "/backtest/scenario?year=1700", nil))
	if !strings.Contains(body, "Crisis financiera") {
		t.Error("out-of-range year should fall back to the default scenario")
	}
}

func TestBacktestWithoutPeople(t *testing.T) {
	s, _ := newFinanceServer(t)
	_, body := do(s, httptest.NewRequest("GET", "/backtest", nil))
	if !strings.Contains(body, "añade al menos una persona") {
		t.Error("expected the empty state")
	}
	if rec, _ := do(s, htmxReq("GET", "/backtest/scenario?crisis=gfc", nil)); rec.Code != http.StatusNotFound {
		t.Errorf("scenario without people = %d", rec.Code)
	}
}
