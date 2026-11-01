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
	for _, want := range []string{
		"Prueba histórica", "Empezando en cualquier año entre 1871 y", "años de inicio salen bien",
		"Gran Depresión", "Estanflación de los 70", "Burbuja puntocom", "Crisis financiera", "COVID",
		"Desde sept 1929", `"type":"start-years"`, `"type":"scenario"`,
		"Crisis financiera, desde oct 2007", "Datos de Robert Shiller", "ene 1871", "más prudente que la historia", "60 % bolsa", "De 1971 a 2023, con datos parciales", `"year":2023,"partial":true`, `"year":1970,"col"`, `aria-current="page"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("backtest page missing %q", want)
		}
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
