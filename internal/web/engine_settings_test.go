package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/igomezal/horizonte/internal/domain"
	"github.com/igomezal/horizonte/internal/engine"
)

func TestCostBasisFromAccountForm(t *testing.T) {
	s, st := newFinanceServer(t)
	plan, _ := st.CurrentPlan(t.Context(), "x")
	mustPost(t, s, "/finances/accounts", url.Values{"name": {"Fondo"}, "kind": {"investment"}, "balance": {"20000"}, "cost_basis": {"8000"}})
	accs, _ := st.ListAccounts(t.Context(), plan)
	if len(accs) != 1 || accs[0].CostBasis == nil || *accs[0].CostBasis != 800000 {
		t.Fatalf("accounts = %+v", accs)
	}
	// Left empty there is no basis, and it is kept off kinds it does not apply to.
	mustPost(t, s, "/finances/accounts", url.Values{"name": {"Vacía"}, "kind": {"investment"}, "balance": {"1"}, "cost_basis": {""}})
	mustPost(t, s, "/finances/accounts", url.Values{"name": {"Nómina"}, "kind": {"cash"}, "balance": {"1"}, "cost_basis": {"5"}})
	accs, _ = st.ListAccounts(t.Context(), plan)
	if accs[1].CostBasis != nil || accs[2].CostBasis != nil {
		t.Errorf("basis = %v / %v, want none", accs[1].CostBasis, accs[2].CostBasis)
	}
	rec, _ := do(s, htmxReq("POST", "/finances/accounts", url.Values{"name": {"Mal"}, "kind": {"investment"}, "balance": {"1"}, "cost_basis": {"-1"}}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("negative basis: %d", rec.Code)
	}
}

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
		"irpf indexing":      func(in *engine.Input) { in.Assumptions.FreezeIRPF = true },
		"property sale": func(in *engine.Input) {
			in.Accounts = []domain.Account{{Sale: &domain.PropertySale{Share: 0.5}}}
		},
		"cost basis": func(in *engine.Input) {
			c := domain.Cents(100)
			in.Accounts = []domain.Account{{CostBasis: &c}}
		},
	} {
		in := base()
		change(&in)
		if mcKey(in, set) == mcKey(base(), set) || rollingKey(in) == rollingKey(base()) {
			t.Errorf("%s does not change the simulation keys", name)
		}
	}
}

func TestIRPFIndexingFromPlanForm(t *testing.T) {
	s, st := newFinanceServer(t)
	plan, _ := st.CurrentPlan(t.Context(), "x")
	form := baseAssumptionsForm()
	form.Set("tax_model", "es")
	form.Set("tax_region", "general")
	form.Set("irpf_indexing", "frozen")
	rec, body := do(s, htmxReq("POST", "/plan/assumptions", form))
	if rec.Code != http.StatusOK || !strings.Contains(body, `value="frozen" selected`) {
		t.Fatalf("save: %d %s", rec.Code, body)
	}
	if a, _ := st.GetAssumptions(t.Context(), plan); !a.FreezeIRPF {
		t.Error("frozen not stored")
	}
	form.Del("irpf_indexing")
	do(s, htmxReq("POST", "/plan/assumptions", form))
	if a, _ := st.GetAssumptions(t.Context(), plan); !a.FreezeIRPF {
		t.Error("a request without the field must keep the setting")
	}
	form.Set("irpf_indexing", "indexed")
	do(s, htmxReq("POST", "/plan/assumptions", form))
	if a, _ := st.GetAssumptions(t.Context(), plan); a.FreezeIRPF {
		t.Error("indexed not stored")
	}
	form.Set("irpf_indexing", "bogus")
	if rec, _ := do(s, htmxReq("POST", "/plan/assumptions", form)); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad choice: %d", rec.Code)
	}
}

func TestPropertySaleFromAccountForm(t *testing.T) {
	s, st := newFinanceServer(t)
	plan, _ := st.CurrentPlan(t.Context(), "x")
	mustPost(t, s, "/finances/people", url.Values{"name": {"Ana"}, "birth": {"1970-01"}, "life_expectancy": {"90"}})
	mustPost(t, s, "/finances/accounts", url.Values{"name": {"Cuenta"}, "kind": {"cash"}, "balance": {"1000"}})
	year := s.now().Year() + 3
	body := mustPost(t, s, "/finances/accounts", url.Values{"name": {"Piso"}, "kind": {"realestate"}, "balance": {"300000"},
		"sale_enabled": {"on"}, "sale_month": {strconv.Itoa(year) + "-05"}, "sale_share": {""}, "sale_cost": {"5"}, "sale_main_residence": {"on"}})
	if !strings.Contains(body, "Venta del") {
		t.Errorf("the row should show the sale: %s", body)
	}
	accs, _ := st.ListAccounts(t.Context(), plan)
	sale := accs[1].Sale
	if sale == nil || sale.Share != 1 || sale.CostRate != 0.05 || !sale.MainResidence || sale.Month.Year != year {
		t.Fatalf("sale = %+v", sale)
	}
	if _, res := do(s, htmxReq("GET", "/plan/results", nil)); !strings.Contains(res, "Venta de Piso") {
		t.Error("the plan timeline should show the sale")
	}
	if _, form := do(s, htmxReq("GET", "/finances/accounts/2/edit", nil)); !strings.Contains(form, `name="sale_month"`) || !strings.Contains(form, strconv.Itoa(year)+"-05") {
		t.Errorf("edit form does not carry the sale: %s", form)
	}

	// Without the checkbox there is no sale; other kinds never have one.
	mustPost(t, s, "/finances/accounts", url.Values{"name": {"Local"}, "kind": {"realestate"}, "balance": {"1"}, "sale_month": {"2040-05"}})
	mustPost(t, s, "/finances/accounts", url.Values{"name": {"Otra"}, "kind": {"cash"}, "balance": {"1"}, "sale_enabled": {"on"}, "sale_month": {"2040-05"}})
	accs, _ = st.ListAccounts(t.Context(), plan)
	if accs[2].Sale != nil || accs[3].Sale != nil {
		t.Errorf("unexpected sales: %+v / %+v", accs[2].Sale, accs[3].Sale)
	}

	// A sale needs a date and a valid share.
	rec, _ := do(s, htmxReq("POST", "/finances/accounts", url.Values{"name": {"X"}, "kind": {"realestate"}, "balance": {"1"}, "sale_enabled": {"on"}}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("sale without a date: %d", rec.Code)
	}
	rec, _ = do(s, htmxReq("POST", "/finances/accounts", url.Values{"name": {"X"}, "kind": {"realestate"}, "balance": {"1"}, "sale_enabled": {"on"}, "sale_month": {"2040-05"}, "sale_share": {"150"}}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("share over 100 %%: %d", rec.Code)
	}
}

// getIn fetches a partial in a language.
func getIn(s *Server, path, lang string) string {
	req := htmxReq("GET", path, nil)
	req.AddCookie(&http.Cookie{Name: "lang", Value: lang})
	_, body := do(s, req)
	return body
}

func TestPlanSaysWhenASaleHasNowhereToGo(t *testing.T) {
	s, st := newFinanceServer(t)
	plan, _ := st.CurrentPlan(t.Context(), "x")
	mustPost(t, s, "/finances/people", url.Values{"name": {"Ana"}, "birth": {"1970-01"}, "life_expectancy": {"90"}})
	year := s.now().Year() + 3
	sale := domain.PropertySale{Month: domain.Month{Year: year, Month: 5}, Share: 1}
	if _, err := st.SaveAccount(t.Context(), domain.Account{PlanID: plan, Name: "Piso", Kind: domain.AccountRealEstate, Balance: 100, Sale: &sale}); err != nil {
		t.Fatal(err)
	}
	if body := getIn(s, "/plan/results", "es"); !strings.Contains(body, "No se ha podido vender Piso") {
		t.Errorf("no notice in Spanish: %s", body)
	}
	if body := getIn(s, "/plan/results", "en"); !strings.Contains(body, "Piso could not be sold") {
		t.Errorf("no notice in English: %s", body)
	}
	if _, err := st.SaveAccount(t.Context(), domain.Account{PlanID: plan, Name: "Cuenta", Kind: domain.AccountCash}); err != nil {
		t.Fatal(err)
	}
	if body := getIn(s, "/plan/results", "es"); strings.Contains(body, "No se ha podido vender") {
		t.Error("notice shown although the sale can happen")
	}
}

func TestAccountRowSaysALapsedSaleWillNotHappen(t *testing.T) {
	s, st := newFinanceServer(t)
	plan, _ := st.CurrentPlan(t.Context(), "x")
	now := domain.MonthOf(s.now())
	save := func(name string, m domain.Month) {
		t.Helper()
		if _, err := st.SaveAccount(t.Context(), domain.Account{PlanID: plan, Name: name, Kind: domain.AccountRealEstate, Balance: 100,
			Sale: &domain.PropertySale{Month: m, Share: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	save("Futura", now.AddMonths(6))
	if body := getIn(s, "/finances/accounts", "es"); strings.Contains(body, "la venta no se hará") {
		t.Error("a future sale is not lapsed")
	}
	save("Pasada", now.AddMonths(-1))
	if body := getIn(s, "/finances/accounts", "es"); !strings.Contains(body, "la venta no se hará") {
		t.Errorf("no notice in Spanish: %s", body)
	}
	if body := getIn(s, "/finances/accounts", "en"); !strings.Contains(body, "the sale will not happen") {
		t.Errorf("no notice in English: %s", body)
	}
}

func TestDeletingAMilestoneWarnsAboutItsSale(t *testing.T) {
	s, st := newFinanceServer(t)
	plan, _ := st.CurrentPlan(t.Context(), "x")
	ms, err := st.SaveMilestone(t.Context(), domain.Milestone{PlanID: plan, Name: "Mudanza", Kind: domain.MilestoneDate, Date: domain.Month{Year: 2045, Month: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if body := getIn(s, "/finances/milestones", "es"); strings.Contains(body, "Se cancelará la venta") {
		t.Error("warning without a sale")
	}
	if _, err := st.SaveAccount(t.Context(), domain.Account{PlanID: plan, Name: "Piso", Kind: domain.AccountRealEstate, Balance: 100,
		Sale: &domain.PropertySale{MS: &ms, Share: 1}}); err != nil {
		t.Fatal(err)
	}
	if body := getIn(s, "/finances/milestones", "es"); !strings.Contains(body, "Se cancelará la venta de Piso") {
		t.Errorf("no warning in Spanish: %s", body)
	}
	if body := getIn(s, "/finances/milestones", "en"); !strings.Contains(body, "The sale of Piso depends on this milestone") {
		t.Errorf("no warning in English: %s", body)
	}
	if body := getIn(s, "/finances/milestones", "es"); !strings.Contains(body, "accounts-changed from:body") {
		t.Error("the section should refresh when a sale is added")
	}
}
