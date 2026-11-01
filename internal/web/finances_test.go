package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/igomezal/horizonte/internal/i18n"
	"github.com/igomezal/horizonte/internal/store"
	"github.com/igomezal/horizonte/ui"
)

func newFinanceServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cat, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{
		Store: st, Catalog: cat, Assets: ui.FS,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:    func() time.Time { return time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, st
}

func htmxReq(method, target string, form url.Values) *http.Request {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, target, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("HX-Request", "true")
	return req
}

func TestFinancesPageRendersEmptyStates(t *testing.T) {
	s, _ := newFinanceServer(t)
	rec, body := do(s, httptest.NewRequest("GET", "/finances", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	for _, want := range []string{
		"Finanzas actuales", "Tu patrimonio neto hoy", "Todavía no hay nadie",
		"Todavía no hay cuentas", `id="section-debts"`, `aria-current="page"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("finances page missing %q", want)
		}
	}
}

func TestCreateAccountWithSpanishNumbers(t *testing.T) {
	s, st := newFinanceServer(t)
	form := url.Values{"name": {"Fondo indexado"}, "kind": {"investment"}, "balance": {"12.345,67"},
		"contribution": {"300"}, "return": {"6,5"}}
	rec, body := do(s, htmxReq("POST", "/finances/accounts", form))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, body)
	}
	if got := rec.Header().Get("HX-Trigger"); got != evFinancesChanged {
		t.Errorf("HX-Trigger = %q", got)
	}
	if !strings.Contains(body, "Fondo indexado") || !strings.Contains(body, "12.346 €") {
		t.Errorf("section missing new account: %s", body)
	}

	plan, _ := st.CurrentPlan(context.Background(), "x")
	accts, _ := st.ListAccounts(context.Background(), plan)
	if len(accts) != 1 || accts[0].Balance != 1234567 || accts[0].MonthlyContribution != 30000 ||
		accts[0].AnnualReturn == nil || *accts[0].AnnualReturn != 0.065 {
		t.Fatalf("stored account = %+v", accts)
	}
}

func TestValidationErrorsRetargetForm(t *testing.T) {
	s, _ := newFinanceServer(t)
	form := url.Values{"name": {""}, "kind": {"salary"}, "amount": {"abc"}, "payments": {"14"},
		"start": {"2030-01"}, "end": {"2029-01"}}
	rec, body := do(s, htmxReq("POST", "/finances/incomes", form))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("HX-Retarget"); got != "#form-incomes-new" {
		t.Errorf("HX-Retarget = %q", got)
	}
	for _, want := range []string{"Rellena este campo.", "Escribe un número", "posterior a la de inicio", `value="abc"`} {
		if !strings.Contains(body, want) {
			t.Errorf("form missing %q", want)
		}
	}
}

func TestEditUpdateDeleteFlow(t *testing.T) {
	s, _ := newFinanceServer(t)
	debt := url.Values{"name": {"Hipoteca"}, "kind": {"mortgage"}, "balance": {"150.000"},
		"rate": {"3"}, "payment": {"711,32"}}
	if rec, body := do(s, htmxReq("POST", "/finances/debts", debt)); rec.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rec.Code, body)
	} else if !strings.Contains(body, "oct 2051") {
		t.Errorf("payoff month missing: %s", body)
	}

	// The edit form shows values in Spanish input format.
	rec, body := do(s, htmxReq("GET", "/finances/debts/1/edit", nil))
	if rec.Code != http.StatusOK || !strings.Contains(body, `value="150000"`) || !strings.Contains(body, `value="711,32"`) {
		t.Fatalf("edit form: %d %s", rec.Code, body)
	}

	debt.Set("balance", "100000")
	if rec, body := do(s, htmxReq("PUT", "/finances/debts/1", debt)); rec.Code != http.StatusOK ||
		!strings.Contains(body, "100.000 €") {
		t.Fatalf("update: %d %s", rec.Code, body)
	}

	if rec, body := do(s, htmxReq("GET", "/finances/debts/1", nil)); rec.Code != http.StatusOK || !strings.Contains(body, `id="debts-1"`) {
		t.Fatalf("row: %d %s", rec.Code, body)
	}

	if rec, body := do(s, htmxReq("DELETE", "/finances/debts/1", nil)); rec.Code != http.StatusOK ||
		!strings.Contains(body, "No hay deudas") {
		t.Fatalf("delete: %d %s", rec.Code, body)
	}
	if rec, _ := do(s, htmxReq("GET", "/finances/debts/1/edit", nil)); rec.Code != http.StatusNotFound {
		t.Errorf("edit after delete = %d, want 404", rec.Code)
	}
	if rec, _ := do(s, htmxReq("PUT", "/finances/debts/1", debt)); rec.Code != http.StatusNotFound {
		t.Errorf("update after delete = %d, want 404", rec.Code)
	}
}

func TestPeopleLimitAndOwnerSelect(t *testing.T) {
	s, _ := newFinanceServer(t)
	for _, name := range []string{"Ana", "Luis"} {
		form := url.Values{"name": {name}, "birth": {"1990-04"}, "life_expectancy": {"90"}}
		rec, _ := do(s, htmxReq("POST", "/finances/people", form))
		if rec.Code != http.StatusOK {
			t.Fatalf("create %s: %d", name, rec.Code)
		}
		if got := rec.Header().Get("HX-Trigger"); !strings.Contains(got, evPeopleChanged) {
			t.Errorf("HX-Trigger = %q, want people-changed", got)
		}
	}
	form := url.Values{"name": {"Third"}, "birth": {"1990-04"}, "life_expectancy": {"90"}}
	rec, body := do(s, htmxReq("POST", "/finances/people", form))
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(body, "como máximo dos personas") {
		t.Errorf("third person: %d", rec.Code)
	}

	// Account form now offers the owners.
	_, body = do(s, htmxReq("GET", "/finances/accounts/new", nil))
	for _, want := range []string{"Titular", "Conjunta", ">Ana<", ">Luis<"} {
		if !strings.Contains(body, want) {
			t.Errorf("account form missing %q", want)
		}
	}
	// An owner from nowhere is rejected as a field error, not a 500.
	acct := url.Values{"name": {"X"}, "kind": {"cash"}, "owner": {"999"}}
	if rec, body := do(s, htmxReq("POST", "/finances/accounts", acct)); rec.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(body, "ya no existe") {
		t.Errorf("bad owner: %d", rec.Code)
	}
}

func TestSummaryTotals(t *testing.T) {
	s, _ := newFinanceServer(t)
	posts := []struct {
		path string
		form url.Values
	}{
		{"/finances/accounts", url.Values{"name": {"Cuenta"}, "kind": {"cash"}, "balance": {"20000"}, "contribution": {"200"}}},
		{"/finances/incomes", url.Values{"name": {"Nómina"}, "kind": {"salary"}, "amount": {"2000"}, "payments": {"14"}}},
		{"/finances/expenses", url.Values{"name": {"Alquiler"}, "category": {"housing"}, "amount": {"900"}, "payments": {"12"}}},
		{"/finances/debts", url.Values{"name": {"Coche"}, "kind": {"car"}, "balance": {"5000"}, "rate": {"0"}, "payment": {"250"}}},
	}
	for _, p := range posts {
		if rec, body := do(s, htmxReq("POST", p.path, p.form)); rec.Code != http.StatusOK {
			t.Fatalf("POST %s: %d %s", p.path, rec.Code, body)
		}
	}
	_, body := do(s, htmxReq("GET", "/finances/summary", nil))
	// Net worth 20.000 − 5.000; unallocated 2.333 − 900 − 250 − 200 = 983.
	for _, want := range []string{"15.000 €", "2.333 €", "983 €"} {
		if !strings.Contains(body, want) {
			t.Errorf("summary missing %q in %s", want, body)
		}
	}
}

func TestUnknownKindIs404(t *testing.T) {
	s, _ := newFinanceServer(t)
	if rec, _ := do(s, htmxReq("GET", "/finances/crypto/new", nil)); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d", rec.Code)
	}
}
