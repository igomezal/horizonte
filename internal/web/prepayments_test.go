package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestPrepaymentsSection(t *testing.T) {
	s, _ := newFinanceServer(t)

	// Without debts the section explains what to do first and offers no add button.
	_, body := do(s, htmxReq("GET", "/finances/prepayments", nil))
	if !strings.Contains(body, "Añade primero una deuda") || strings.Contains(body, `hx-get="/finances/prepayments/new"`) {
		t.Errorf("empty prepayments section: %s", body)
	}

	rec, _ := do(s, htmxReq("POST", "/finances/debts", url.Values{"name": {"Hipoteca"}, "kind": {"mortgage"},
		"balance": {"150.000"}, "rate": {"3"}, "payment": {"711,32"}}))
	if got := rec.Header().Get("HX-Trigger"); !strings.Contains(got, evDebtsChanged) {
		t.Errorf("debt save HX-Trigger = %q", got)
	}

	// The form preselects the only debt.
	_, body = do(s, htmxReq("GET", "/finances/prepayments/new", nil))
	for _, want := range []string{`name="debt"`, ">Hipoteca<", "Una sola vez", `x-data="{ freq: 'once' }"`} {
		if !strings.Contains(body, want) {
			t.Errorf("prepayment form missing %q", want)
		}
	}

	// One-off payment needs a date.
	rec, body = do(s, htmxReq("POST", "/finances/prepayments", url.Values{"debt": {"1"}, "amount": {"20.000"},
		"frequency": {"once"}, "mode": {"term"}, "start": {""}}))
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(body, "Rellena este campo") {
		t.Errorf("once without date: %d", rec.Code)
	}

	body = mustPost(t, s, "/finances/prepayments", url.Values{"debt": {"1"}, "amount": {"20.000"},
		"frequency": {"once"}, "mode": {"term"}, "start": {"2030-06"}, "end": {"2031-01"}, "fee": {"0,25"}})
	for _, want := range []string{"Hipoteca", "Una vez, en jun 2030", "Reduce plazo", "Comisión 0,25", "20.000"} {
		if !strings.Contains(body, want) {
			t.Errorf("prepayment row missing %q: %s", want, body)
		}
	}
	body = mustPost(t, s, "/finances/prepayments", url.Values{"debt": {"1"}, "amount": {"100"},
		"frequency": {"monthly"}, "mode": {"payment"}})
	if !strings.Contains(body, "Cada mes") || !strings.Contains(body, "Reduce cuota") || !strings.Contains(body, "100 €/mes") {
		t.Errorf("monthly prepayment row: %s", body)
	}

	// The debt row now warns that its payoff date ignores early repayments.
	_, body = do(s, htmxReq("GET", "/finances/debts", nil))
	if !strings.Contains(body, "sin contar amortizaciones") {
		t.Errorf("debt row should mention early repayments: %s", body)
	}
}

func TestPrepaymentShortensPlanDebt(t *testing.T) {
	s, _ := newFinanceServer(t)
	mustPost(t, s, "/finances/people", url.Values{"name": {"Ana"}, "birth": {"1990-04"}, "life_expectancy": {"90"}})
	mustPost(t, s, "/finances/accounts", url.Values{"name": {"Cuenta"}, "kind": {"cash"}, "balance": {"60000"}})
	mustPost(t, s, "/finances/incomes", url.Values{"name": {"Nómina"}, "kind": {"salary"}, "amount": {"3000"}, "payments": {"12"}})
	mustPost(t, s, "/finances/debts", url.Values{"name": {"Hipoteca"}, "kind": {"mortgage"},
		"balance": {"150.000"}, "rate": {"3"}, "payment": {"711,32"}})

	_, before := do(s, htmxReq("GET", "/plan/results?view=year&real=1", nil))
	if !strings.Contains(before, "Deuda pagada: Hipoteca") || !strings.Contains(before, "oct 2051") {
		t.Fatalf("baseline payoff missing")
	}
	mustPost(t, s, "/finances/prepayments", url.Values{"debt": {"1"}, "amount": {"50.000"},
		"frequency": {"once"}, "mode": {"term"}, "start": {"2027-01"}})
	_, after := do(s, htmxReq("GET", "/plan/results?view=year&real=1", nil))
	if strings.Contains(after, "oct 2051") {
		t.Error("a 50.000 € early repayment should bring the payoff forward")
	}
}

func TestRedirectSelects(t *testing.T) {
	s, _ := newFinanceServer(t)
	mustPost(t, s, "/finances/accounts", url.Values{"name": {"Fondo indexado"}, "kind": {"investment"}})
	mustPost(t, s, "/finances/accounts", url.Values{"name": {"Piso"}, "kind": {"realestate"}})

	_, body := do(s, htmxReq("GET", "/finances/debts/new", nil))
	if !strings.Contains(body, "Al terminar de pagar, la cuota va a") || !strings.Contains(body, ">Fondo indexado<") {
		t.Errorf("debt form missing the redirect select: %s", body)
	}
	if strings.Contains(body, ">Piso<") {
		t.Error("property must not be offered as a destination")
	}
	body = mustPost(t, s, "/finances/debts", url.Values{"name": {"Coche"}, "kind": {"car"}, "balance": {"6000"},
		"rate": {"0"}, "payment": {"500"}, "redirect": {"1"}})
	if !strings.Contains(body, "Después, la cuota va a Fondo indexado") {
		t.Errorf("debt row should show the destination: %s", body)
	}
	body = mustPost(t, s, "/finances/prepayments", url.Values{"debt": {"1"}, "amount": {"100"},
		"frequency": {"monthly"}, "mode": {"term"}, "redirect": {"1"}})
	if !strings.Contains(body, "Después, a Fondo indexado") {
		t.Errorf("prepayment row should show the destination: %s", body)
	}
}
