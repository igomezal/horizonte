package engine

import (
	"math"
	"testing"

	"github.com/igomezal/horizonte/internal/domain"
)

func ptr[T any](v T) *T { return &v }

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// flat returns assumptions with no inflation, no tax and zero returns,
// so tests can reason about exact cash flows.
func flat() Assumptions {
	a := DefaultAssumptions()
	a.Inflation, a.TaxRate = 0, 0
	for k := range a.Returns {
		a.Returns[k] = 0
	}
	return a
}

var start = domain.Month{Year: 2026, Month: 11}

// person whose plan ends exactly `months` months after start.
func personFor(months int) domain.Person {
	end := start.AddMonths(months - 1)
	return domain.Person{ID: 1, Name: "Ana", Birth: domain.Month{Year: end.Year - 90, Month: end.Month}, LifeExpectancy: 90}
}

func TestHorizon(t *testing.T) {
	in := Input{People: []domain.Person{personFor(24)}, Assumptions: flat(), Start: start}
	res := Simulate(in)
	if len(res.Months) != 24 {
		t.Fatalf("months = %d, want 24", len(res.Months))
	}
	if res.Months[23].Month != start.AddMonths(23) {
		t.Errorf("last month = %v", res.Months[23].Month)
	}
	if got := Simulate(Input{Start: start}); len(got.Months) != 0 {
		t.Errorf("no people should give an empty projection")
	}
}

func TestFourteenPagasAndSurplus(t *testing.T) {
	in := Input{
		People:   []domain.Person{personFor(12)}, // nov 2026 .. oct 2027
		Accounts: []domain.Account{{ID: 1, Kind: domain.AccountCash}},
		Incomes:  []domain.Income{{Amount: 200000, PaymentsPerYear: 14}},
		Expenses: []domain.Expense{{Amount: 100000, PaymentsPerYear: 12}},
		Assumptions: func() Assumptions {
			a := flat()
			a.TaxRate = 0.1
			return a
		}(),
		Start: start,
	}
	res := Simulate(in)
	// December and June carry an extra payment.
	byMonth := map[int]float64{}
	for _, m := range res.Months {
		byMonth[m.Month.Month] = m.Income
	}
	if byMonth[12] != 4000 || byMonth[6] != 4000 || byMonth[1] != 2000 {
		t.Errorf("income by month = %v", byMonth)
	}
	// 12 months: 14 × 2000 = 28000 income, 2800 tax, 12000 expenses → 13200 saved.
	last := res.Months[len(res.Months)-1]
	if !near(last.Balances[0], 13200, 0.01) {
		t.Errorf("cash at end = %v, want 13200", last.Balances[0])
	}
	if res.Failed() {
		t.Error("plan should not fail")
	}
}

func TestContributionsOnlyFromSurplus(t *testing.T) {
	in := Input{
		People: []domain.Person{personFor(3)},
		Accounts: []domain.Account{
			{ID: 1, Kind: domain.AccountCash, Balance: 100000},
			{ID: 2, Kind: domain.AccountInvestment, MonthlyContribution: 50000},
		},
		Incomes:     []domain.Income{{Amount: 30000, PaymentsPerYear: 12}}, // 300 € surplus only
		Assumptions: flat(),
		Start:       start,
	}
	res := Simulate(in)
	m := res.Months[0]
	if m.Contributions != 300 {
		t.Errorf("contribution = %v, want 300 (capped by surplus)", m.Contributions)
	}
	if m.Balances[0] != 1000 || m.Balances[1] != 300 {
		t.Errorf("balances = %v", m.Balances)
	}
}

func TestDeficitDrawdownOrderAndFailure(t *testing.T) {
	in := Input{
		People: []domain.Person{personFor(6)},
		Accounts: []domain.Account{
			{ID: 1, Kind: domain.AccountPension, Balance: 100000},
			{ID: 2, Kind: domain.AccountRealEstate, Balance: 10000000},
			{ID: 3, Kind: domain.AccountCash, Balance: 100000},
		},
		Expenses:    []domain.Expense{{Amount: 50000, PaymentsPerYear: 12}}, // 500 €/month
		Assumptions: flat(),
		Start:       start,
	}
	res := Simulate(in)
	// Months 1–2 use cash, 3–4 the pension plan, then the money runs out.
	if b := res.Months[1].Balances; b[2] != 0 || b[0] != 1000 {
		t.Errorf("after 2 months balances = %v (cash first)", b)
	}
	if b := res.Months[3].Balances; b[0] != 0 {
		t.Errorf("pension should be empty after 4 months: %v", b)
	}
	if res.FailMonth != start.AddMonths(4) {
		t.Errorf("FailMonth = %v, want %v", res.FailMonth, start.AddMonths(4))
	}
	if !near(res.Shortfall, 1000, 0.01) {
		t.Errorf("Shortfall = %v, want 1000", res.Shortfall)
	}
	// Property is never sold.
	if res.Months[5].Balances[1] != 100000 {
		t.Errorf("property balance changed: %v", res.Months[5].Balances[1])
	}
}

func TestDebtAmortisationMatchesDomain(t *testing.T) {
	d := domain.Debt{Balance: 15000000, AnnualRate: 0.03, MonthlyPayment: 71132}
	months := d.MonthsToPayoff() // 300
	in := Input{
		People:      []domain.Person{personFor(months + 2)},
		Accounts:    []domain.Account{{ID: 1, Kind: domain.AccountCash, Balance: 100000000}},
		Debts:       []domain.Debt{d},
		Assumptions: flat(),
		Start:       start,
	}
	res := Simulate(in)
	if got := res.Months[months-1].DebtBalance; got != 0 {
		t.Errorf("debt after %d payments = %v, want 0", months, got)
	}
	if got := res.Months[months-2].DebtBalance; got <= 0 {
		t.Errorf("debt cleared too early")
	}
	if res.Months[months].DebtPayments != 0 {
		t.Errorf("payments continue after payoff")
	}
}

func TestGrowthAndInflation(t *testing.T) {
	a := flat()
	a.Inflation = 0.02
	a.Returns[domain.AccountInvestment] = 0.06
	in := Input{
		People:   []domain.Person{personFor(25)},
		Accounts: []domain.Account{{ID: 1, Kind: domain.AccountInvestment, Balance: 1000000}},
		// Expense grows with inflation: 100 € in year 1, 102 € in year 2, 104,04 € in year 3.
		Incomes:     []domain.Income{{Amount: 100000, PaymentsPerYear: 12}},
		Expenses:    []domain.Expense{{Amount: 10000, PaymentsPerYear: 12}},
		Assumptions: a,
		Start:       start,
	}
	res := Simulate(in)
	if got := res.Months[12].Expenses; !near(got, 102, 1e-9) {
		t.Errorf("year-2 expense = %v, want 102", got)
	}
	if got := res.Months[24].Expenses; !near(got, 104.04, 1e-9) {
		t.Errorf("year-3 expense = %v, want 104.04", got)
	}
	// Deflator after 12 months equals one year of inflation.
	if got := res.Months[12].Deflator; !near(got, 1.02, 1e-9) {
		t.Errorf("deflator = %v, want 1.02", got)
	}
	// With no flows into the investment account, 12 months of growth = 6 %.
	inv := Input{
		People:      []domain.Person{personFor(12)},
		Accounts:    []domain.Account{{ID: 1, Kind: domain.AccountInvestment, Balance: 1000000}},
		Assumptions: a,
		Start:       start,
	}
	r2 := Simulate(inv)
	if got := r2.Months[11].Balances[0]; !near(got, 10600, 0.01) {
		t.Errorf("balance after a year = %v, want 10600", got)
	}
}

func TestAccountReturnOverridesDefault(t *testing.T) {
	a := flat()
	a.Returns[domain.AccountDeposit] = 0.02
	acc := domain.Account{Kind: domain.AccountDeposit, AnnualReturn: ptr(0.04)}
	if got := a.AnnualReturn(acc); got != 0.04 {
		t.Errorf("AnnualReturn = %v", got)
	}
	acc.AnnualReturn = nil
	if got := a.AnnualReturn(acc); got != 0.02 {
		t.Errorf("default AnnualReturn = %v", got)
	}
}

func TestYearlyPaymentAndYears(t *testing.T) {
	in := Input{
		People:      []domain.Person{personFor(14)}, // nov 2026 .. dic 2027
		Accounts:    []domain.Account{{ID: 1, Kind: domain.AccountCash, Balance: 1000000}},
		Expenses:    []domain.Expense{{Amount: 60000, PaymentsPerYear: 1, Start: domain.Month{Year: 2026, Month: 3}}},
		Assumptions: flat(),
		Start:       start,
	}
	res := Simulate(in)
	years := res.Years()
	if len(years) != 2 || years[0].Year != 2026 || years[1].Year != 2027 {
		t.Fatalf("years = %+v", years)
	}
	// Paid every March: nothing in 2026 (Nov–Dec), once in 2027.
	if years[0].Expenses != 0 || years[1].Expenses != 600 {
		t.Errorf("yearly expenses = %v / %v", years[0].Expenses, years[1].Expenses)
	}
	if years[1].Last.Month != (domain.Month{Year: 2027, Month: 12}) {
		t.Errorf("last month of 2027 = %v", years[1].Last.Month)
	}
}

func TestSurplusAccountChoice(t *testing.T) {
	accts := []domain.Account{
		{ID: 5, Kind: domain.AccountRealEstate},
		{ID: 6, Kind: domain.AccountInvestment},
		{ID: 7, Kind: domain.AccountCash},
	}
	if got := surplusAccount(accts, nil); got != 2 {
		t.Errorf("default surplus = %d, want first cash account", got)
	}
	if got := surplusAccount(accts, ptr(int64(6))); got != 1 {
		t.Errorf("chosen surplus = %d", got)
	}
	if got := surplusAccount(accts[:1], nil); got != -1 {
		t.Errorf("property only = %d, want -1", got)
	}
}
