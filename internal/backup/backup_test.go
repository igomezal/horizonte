package backup

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/igomezal/horizonte/internal/domain"
	"github.com/igomezal/horizonte/internal/engine"
	"github.com/igomezal/horizonte/internal/store"
)

var now = domain.Month{Year: 2026, Month: 11}

func ptr[T any](v T) *T { return &v }

// fullSnapshot uses every record type and every kind of link.
func fullSnapshot() store.Snapshot {
	a := engine.DefaultAssumptions()
	a.Inflation, a.TaxModel, a.TaxRegion = 0.031, engine.TaxES, "madrid"
	a.SurplusAccountID = ptr(int64(30))
	a.Withdrawal = engine.WithdrawalSettings{Strategy: engine.WithdrawGuardrails, StartMS: ptr(int64(20)), Rate: 0.04, Floor: 0.8, Ceiling: 1.2}
	a.Order = []domain.AccountKind{domain.AccountCash, domain.AccountInvestment, domain.AccountDeposit, domain.AccountPension, domain.AccountOther}
	a.PensionAvailableMS = ptr(int64(20))
	return store.Snapshot{
		People: []domain.Person{
			{ID: 10, Name: "Ana", Birth: domain.Month{Year: 1990, Month: 4}, LifeExpectancy: 90},
			{ID: 11, Name: "Luis", Birth: domain.Month{Year: 1988, Month: 9}, LifeExpectancy: 88},
		},
		Milestones: []domain.Milestone{
			{ID: 20, Name: "Jubilación", Kind: domain.MilestoneAge, PersonID: ptr(int64(10)), Age: 65},
			{ID: 21, Name: "Libertad", Kind: domain.MilestoneFI, FIVariant: domain.FILean, FIMultiple: 25, FIAmount: 2000000},
		},
		Accounts: []domain.Account{
			{ID: 30, Name: "Cuenta", Kind: domain.AccountCash, OwnerID: ptr(int64(10)), Balance: 1234567},
			{ID: 31, Name: "Fondo", Kind: domain.AccountInvestment, MonthlyContribution: 50000, AnnualReturn: ptr(0.065), ContributionEndMS: ptr(int64(21))},
		},
		Incomes: []domain.Income{
			{ID: 40, Name: "Nómina", Kind: domain.IncomeSalary, OwnerID: ptr(int64(11)), Amount: 250099, PaymentsPerYear: 14, EndMS: ptr(int64(20))},
			{ID: 41, Name: "Alquiler", Kind: domain.IncomeRental, Amount: 70000, PaymentsPerYear: 12, AnnualGrowth: ptr(0.01),
				Start: domain.Month{Year: 2027, Month: 1}, End: domain.Month{Year: 2040, Month: 12}},
		},
		Expenses: []domain.Expense{
			{ID: 50, Name: "Casa", Category: domain.ExpenseHousing, Amount: 90000, PaymentsPerYear: 12, StartMS: ptr(int64(20))},
		},
		Debts: []domain.Debt{
			{ID: 60, Name: "Hipoteca", Kind: domain.DebtMortgage, Balance: 15000000, AnnualRate: 0.025, MonthlyPayment: 70000, RedirectAccountID: ptr(int64(31))},
		},
		Prepayments: []domain.Prepayment{
			{ID: 70, DebtID: 60, Amount: 300000, Frequency: domain.PrepayYearly, Mode: domain.ReduceTerm, FeeRate: 0.005,
				Start: domain.Month{Year: 2027, Month: 6}, EndMS: ptr(int64(20)), RedirectAccountID: ptr(int64(31))},
		},
		Assumptions: a,
	}
}

func roundTrip(t *testing.T, snap store.Snapshot) (string, store.Snapshot) {
	t.Helper()
	var buf bytes.Buffer
	if err := Write(&buf, FromSnapshot("Mi plan", snap, time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC))); err != nil {
		t.Fatal(err)
	}
	name, got, err := Read(&buf, now)
	if err != nil {
		t.Fatalf("read back: %v\n%s", err, buf.String())
	}
	return name, got
}

func TestRoundTripKeepsEverything(t *testing.T) {
	want := fullSnapshot()
	name, got := roundTrip(t, want)
	if name != "Mi plan" {
		t.Errorf("name = %q", name)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip changed the plan:\n got %+v\nwant %+v", got, want)
	}
}

func TestFileIsReadable(t *testing.T) {
	var buf bytes.Buffer
	_ = Write(&buf, FromSnapshot("Mi plan", fullSnapshot(), time.Now()))
	s := buf.String()
	for _, want := range []string{`"format": "horizonte/plan"`, `"version": 1`, `"balance": 12345.67`,
		`"amount": 2500.99`, `"birth": "1990-04"`, `"end_milestone_id": 20`, `"tax_region": "madrid"`} {
		if !strings.Contains(s, want) {
			t.Errorf("file missing %s", want)
		}
	}
}

func TestEmptyPlanRoundTrips(t *testing.T) {
	snap := store.Snapshot{Assumptions: engine.DefaultAssumptions()}
	_, got := roundTrip(t, snap)
	if len(got.People) != 0 || !reflect.DeepEqual(got.Assumptions, snap.Assumptions) {
		t.Errorf("empty plan = %+v", got)
	}
}

func TestMissingAssumptionsGetDefaults(t *testing.T) {
	doc := `{"format":"horizonte/plan","version":1,"plan":{"name":"Mano","people":[{"id":1,"name":"Eva","birth":"1980-01","life_expectancy":90}],
		"assumptions":{"inflation":0.02,"tax_rate":0.2}}}`
	_, snap, err := Read(strings.NewReader(doc), now)
	if err != nil {
		t.Fatal(err)
	}
	def := engine.DefaultAssumptions()
	if snap.Assumptions.Inflation != 0.02 || snap.Assumptions.TaxModel != def.TaxModel ||
		snap.Assumptions.Returns[domain.AccountInvestment] != def.Returns[domain.AccountInvestment] {
		t.Errorf("assumptions = %+v", snap.Assumptions)
	}
}

func TestReadRejectsBadFiles(t *testing.T) {
	valid := func(edit func(*File)) string {
		f := FromSnapshot("Mi plan", fullSnapshot(), time.Now())
		edit(&f)
		var buf bytes.Buffer
		_ = Write(&buf, f)
		return buf.String()
	}
	cases := []struct {
		name    string
		doc     string
		err     error
		section string
		field   string
	}{
		{"not json", "hola", ErrNotPlan, "", ""},
		{"other json", `{"people":[]}`, ErrNotPlan, "", ""},
		{"newer version", valid(func(f *File) { f.Version = 2 }), ErrVersion, "", ""},
		{"no name", valid(func(f *File) { f.Plan.Name = "" }), nil, "plan", "name"},
		{"bad month", valid(func(f *File) { f.Plan.People[0].Birth = "abril" }), nil, "people", "birth"},
		{"duplicate id", valid(func(f *File) { f.Plan.Accounts[1].ID = 30 }), nil, "accounts", "id"},
		{"dangling owner", valid(func(f *File) { f.Plan.Incomes[0].OwnerID = ptr(int64(99)) }), nil, "incomes", "owner_id"},
		{"dangling milestone", valid(func(f *File) { f.Plan.Expenses[0].StartMS = ptr(int64(99)) }), nil, "expenses", "start_milestone_id"},
		{"repayment of missing debt", valid(func(f *File) { f.Plan.Prepayments[0].DebtID = 99 }), nil, "prepayments", "debt_id"},
		{"negative balance", valid(func(f *File) { f.Plan.Accounts[0].Balance = -5 }), nil, "accounts", "balance"},
		{"unknown kind", valid(func(f *File) { f.Plan.Debts[0].Kind = "yate" }), nil, "debts", "kind"},
		{"three people", valid(func(f *File) {
			f.Plan.People = append(f.Plan.People, Person{ID: 12, Name: "X", Birth: "2000-01", LifeExpectancy: 90})
		}), nil, "people", "count"},
		{"bad surplus", valid(func(f *File) { f.Plan.Assumptions.SurplusAccountID = ptr(int64(99)) }), nil, "assumptions", "surplus_account_id"},
		{"bad inflation", valid(func(f *File) { f.Plan.Assumptions.Inflation = 3 }), nil, "assumptions", "inflation"},
	}
	for _, c := range cases {
		_, _, err := Read(strings.NewReader(c.doc), now)
		if c.err != nil {
			if !errors.Is(err, c.err) {
				t.Errorf("%s: err = %v, want %v", c.name, err, c.err)
			}
			continue
		}
		var ie *InvalidError
		if !errors.As(err, &ie) || ie.Section != c.section || ie.Field != c.field {
			t.Errorf("%s: err = %v, want invalid %s.%s", c.name, err, c.section, c.field)
		}
	}
	if _, _, err := Read(strings.NewReader(strings.Repeat(" ", MaxSize+1)), now); !errors.Is(err, ErrTooLarge) {
		t.Errorf("huge file: %v", err)
	}
}
