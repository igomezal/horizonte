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
	a.Inflation, a.TaxRate, a.TaxModel = 0, 0, TaxFlat
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

func TestMilestonesDriveStreams(t *testing.T) {
	// Ana is 64 at the start; retirement at 65 = start + 12 months.
	ana := domain.Person{ID: 1, Birth: start.AddMonths(-64 * 12), LifeExpectancy: 66}
	retire := domain.Milestone{ID: 7, Name: "Jubilación", Kind: domain.MilestoneAge, PersonID: ptr(int64(1)), Age: 65}
	in := Input{
		People:     []domain.Person{ana},
		Milestones: []domain.Milestone{retire},
		Accounts: []domain.Account{
			{ID: 1, Kind: domain.AccountCash},
			{ID: 2, Kind: domain.AccountInvestment, MonthlyContribution: 10000, ContributionEndMS: ptr(int64(7))},
		},
		Incomes: []domain.Income{
			{Amount: 200000, PaymentsPerYear: 12, EndMS: ptr(int64(7))},   // salary until retirement
			{Amount: 150000, PaymentsPerYear: 12, StartMS: ptr(int64(7))}, // pension from retirement
		},
		Assumptions: flat(),
		Start:       start,
	}
	res := Simulate(in)
	before, at := res.Months[11], res.Months[12]
	if before.Income != 2000 || at.Income != 1500 {
		t.Errorf("income before/at retirement = %v / %v, want 2000 / 1500", before.Income, at.Income)
	}
	if before.Contributions != 100 || at.Contributions != 0 {
		t.Errorf("contributions before/at retirement = %v / %v", before.Contributions, at.Contributions)
	}
	if len(res.Milestones) != 1 || res.Milestones[0].Month != start.AddMonths(12) {
		t.Errorf("milestone hit = %+v", res.Milestones)
	}
}

func TestFinancialIndependenceIsConditional(t *testing.T) {
	fi := domain.Milestone{ID: 9, Name: "IF", Kind: domain.MilestoneFI, FIVariant: domain.FIStandard, FIMultiple: 25}
	in := Input{
		People:     []domain.Person{personFor(36)},
		Milestones: []domain.Milestone{fi},
		Accounts: []domain.Account{
			{ID: 1, Kind: domain.AccountCash, Balance: 2900000},        // 29.000 €
			{ID: 2, Kind: domain.AccountRealEstate, Balance: 90000000}, // property does not count
		},
		// Spending 100 €/month → FI at 25 × 1.200 € = 30.000 €. Saving 100 €/month gets there in 10 months.
		Incomes:     []domain.Income{{Amount: 20000, PaymentsPerYear: 12, EndMS: ptr(int64(9))}},
		Expenses:    []domain.Expense{{Amount: 10000, PaymentsPerYear: 12}},
		Assumptions: flat(),
		Start:       start,
	}
	res := Simulate(in)
	hit := res.Milestones[0]
	if !hit.Reached() {
		t.Fatal("FI should be reached")
	}
	// Balance reaches 30.000 € at the end of month index 9; the milestone is the next month.
	if hit.Month != start.AddMonths(10) {
		t.Errorf("FI month = %v, want %v", hit.Month, start.AddMonths(10))
	}
	if res.Months[10].Income != 0 || res.Months[9].Income != 200 {
		t.Errorf("salary should stop at FI: month9=%v month10=%v", res.Months[9].Income, res.Months[10].Income)
	}

	// Never reached: a stream waiting for it never starts.
	in.Incomes = []domain.Income{{Amount: 100000, PaymentsPerYear: 12, StartMS: ptr(int64(9))}}
	in.Accounts = in.Accounts[1:] // only property
	res = Simulate(in)
	if res.Milestones[0].Reached() {
		t.Error("FI with no savings must not be reached")
	}
	for _, m := range res.Months {
		if m.Income != 0 {
			t.Fatalf("income waiting for an unreached milestone was paid in %v", m.Month)
		}
	}
}

func TestDebtPaidOffEvent(t *testing.T) {
	in := Input{
		People:      []domain.Person{personFor(12)},
		Accounts:    []domain.Account{{ID: 1, Kind: domain.AccountCash, Balance: 1000000}},
		Debts:       []domain.Debt{{Name: "Coche", Balance: 30000, MonthlyPayment: 10000}},
		Assumptions: flat(),
		Start:       start,
	}
	res := Simulate(in)
	if len(res.PaidOff) != 1 || res.PaidOff[0].Month != start.AddMonths(2) {
		t.Errorf("paid off = %+v, want third month", res.PaidOff)
	}
}

func TestFIREVariants(t *testing.T) {
	// 300.000 € saved, spending 24.000 €/year (2.000 €/month), no flows.
	base := func(ms domain.Milestone) Result {
		ms.ID, ms.Name, ms.Kind, ms.FIMultiple = 9, "FI", domain.MilestoneFI, 25
		a := flat()
		a.Returns[domain.AccountInvestment] = 0.05
		return Simulate(Input{
			People:      []domain.Person{{ID: 1, Birth: start.AddMonths(-40 * 12), LifeExpectancy: 41}},
			Milestones:  []domain.Milestone{ms},
			Accounts:    []domain.Account{{ID: 1, Kind: domain.AccountInvestment, Balance: 30000000}},
			Incomes:     []domain.Income{{Amount: 200000, PaymentsPerYear: 12}},
			Expenses:    []domain.Expense{{Amount: 200000, PaymentsPerYear: 12}},
			Assumptions: a,
			Start:       start,
		})
	}
	cases := []struct {
		name  string
		ms    domain.Milestone
		reach bool
	}{
		// Classic needs 25 × 24.000 = 600.000 €: not within a year at 5 %.
		{"classic", domain.Milestone{FIVariant: domain.FIStandard}, false},
		// Lean on 12.000 €/year needs 300.000 €: already there.
		{"lean", domain.Milestone{FIVariant: domain.FILean, FIAmount: 1200000}, true},
		// Fat on 60.000 €/year needs 1.500.000 €.
		{"fat", domain.Milestone{FIVariant: domain.FIFat, FIAmount: 6000000}, false},
		// Barista with 12.000 €/year part-time covers half: needs 25 × 12.000 = 300.000 €.
		{"barista", domain.Milestone{FIVariant: domain.FIBarista, FIAmount: 1200000}, true},
		// Coast to 65 (25 years at 5 %): 300.000 × 1,05^25 ≈ 1.016.000 € ≥ 600.000 €.
		{"coast", domain.Milestone{FIVariant: domain.FICoast, PersonID: ptr(int64(1)), Age: 65}, true},
		// Coast to 45 (5 years): 300.000 × 1,05^5 ≈ 383.000 € < 600.000 €.
		{"coast-soon", domain.Milestone{FIVariant: domain.FICoast, PersonID: ptr(int64(1)), Age: 45}, false},
	}
	for _, c := range cases {
		res := base(c.ms)
		if got := res.Milestones[0].Reached(); got != c.reach {
			t.Errorf("%s: reached = %v, want %v", c.name, got, c.reach)
		}
		if c.reach && res.Milestones[0].Month != start.AddMonths(1) {
			t.Errorf("%s: month = %v, want the second month", c.name, res.Milestones[0].Month)
		}
	}
}

func TestPrepayments(t *testing.T) {
	// 150.000 € at 3 % with the 25-year instalment (711,32 €): 300 payments.
	mortgage := domain.Debt{ID: 1, Name: "Hipoteca", Balance: 15000000, AnnualRate: 0.03, MonthlyPayment: 71132}
	run := func(pps ...domain.Prepayment) Result {
		return Simulate(Input{
			People:      []domain.Person{personFor(320)},
			Accounts:    []domain.Account{{ID: 1, Kind: domain.AccountCash, Balance: 100000000}},
			Debts:       []domain.Debt{mortgage},
			Prepayments: pps,
			Assumptions: flat(),
			Start:       start,
		})
	}
	base := run()
	if base.PaidOff[0].Month != start.AddMonths(299) {
		t.Fatalf("baseline payoff = %v", base.PaidOff[0].Month)
	}

	// Reducir plazo: 20.000 € in month 12 keeps the instalment and ends sooner.
	term := run(domain.Prepayment{DebtID: 1, Amount: 2000000, Frequency: domain.PrepayOnce,
		Mode: domain.ReduceTerm, Start: start.AddMonths(12)})
	if !term.PaidOff[0].Month.Before(base.PaidOff[0].Month.AddMonths(-24)) {
		t.Errorf("reduce term should end years earlier: %v", term.PaidOff[0].Month)
	}
	if got := term.Months[13].DebtPayments; !near(got, 711.32, 0.01) {
		t.Errorf("instalment after reducing term = %v, want unchanged 711,32", got)
	}
	if got := term.Months[12].Prepayments; got != 20000 {
		t.Errorf("prepayment = %v", got)
	}

	// Reducir cuota: same end month, lower instalment.
	pay := run(domain.Prepayment{DebtID: 1, Amount: 2000000, Frequency: domain.PrepayOnce,
		Mode: domain.ReducePayment, Start: start.AddMonths(12)})
	if pay.PaidOff[0].Month != base.PaidOff[0].Month {
		t.Errorf("reduce payment should keep the end month: %v vs %v", pay.PaidOff[0].Month, base.PaidOff[0].Month)
	}
	if got := pay.Months[13].DebtPayments; got >= 711.32 || got < 600 {
		t.Errorf("instalment after reducing payment = %v, want lower", got)
	}

	// Monthly extra payments with a 1 % fee: the fee is paid on top.
	fee := run(domain.Prepayment{DebtID: 1, Amount: 10000, Frequency: domain.PrepayMonthly,
		Mode: domain.ReduceTerm, FeeRate: 0.01})
	if got := fee.Months[0].Prepayments; !near(got, 101, 1e-9) {
		t.Errorf("monthly prepayment with fee = %v, want 101", got)
	}
	if !fee.PaidOff[0].Month.Before(base.PaidOff[0].Month) {
		t.Error("monthly extra payments should shorten the debt")
	}

	// A lump sum larger than the debt only pays what is owed.
	all := run(domain.Prepayment{DebtID: 1, Amount: 99999999, Frequency: domain.PrepayOnce,
		Mode: domain.ReduceTerm, Start: start})
	if all.PaidOff[0].Month != start || all.Months[1].DebtPayments != 0 {
		t.Errorf("lump sum should clear the debt at once: %+v", all.PaidOff)
	}
}

func TestPrepaymentCappedToAvailableMoney(t *testing.T) {
	res := Simulate(Input{
		People:   []domain.Person{personFor(3)},
		Accounts: []domain.Account{{ID: 1, Kind: domain.AccountCash, Balance: 50000}}, // 500 €
		Debts:    []domain.Debt{{ID: 1, Balance: 1000000, MonthlyPayment: 10000}},     // 100 €/month
		Incomes:  []domain.Income{{Amount: 10000, PaymentsPerYear: 12}},               // covers the instalment
		Prepayments: []domain.Prepayment{{DebtID: 1, Amount: 500000, Frequency: domain.PrepayOnce,
			Mode: domain.ReduceTerm, Start: start}},
		Assumptions: flat(),
		Start:       start,
	})
	if got := res.Months[0].Prepayments; got != 500 {
		t.Errorf("prepayment = %v, want capped to the 500 € available", got)
	}
	if res.Failed() {
		t.Error("an optional prepayment must never make the plan fail")
	}
}

func TestRedirectAfterPayoff(t *testing.T) {
	fund := int64(2)
	in := Input{
		People: []domain.Person{personFor(8)},
		Accounts: []domain.Account{
			{ID: 1, Kind: domain.AccountCash, Balance: 100000},
			{ID: 2, Kind: domain.AccountInvestment},
		},
		Incomes: []domain.Income{{Amount: 50000, PaymentsPerYear: 12}}, // 500 €/month
		// 300 € debt at 0 %, 100 €/month: paid off in month 3, then 100 €/month goes to the fund.
		Debts: []domain.Debt{{ID: 1, Balance: 30000, MonthlyPayment: 10000, RedirectAccountID: &fund}},
		// 50 €/month extra repayment, also redirected once the debt is gone.
		Prepayments: []domain.Prepayment{{DebtID: 1, Amount: 5000, Frequency: domain.PrepayMonthly,
			Mode: domain.ReduceTerm, RedirectAccountID: &fund}},
		Assumptions: flat(),
		Start:       start,
	}
	res := Simulate(in)
	// 100 + 50 per month clears 300 € in month 2 (index 1).
	if res.PaidOff[0].Month != start.AddMonths(1) {
		t.Fatalf("payoff = %v", res.PaidOff[0].Month)
	}
	m := res.Months[2] // first month after payoff
	if m.Redirected != 50 {
		t.Errorf("redirected repayment = %v, want 50", m.Redirected)
	}
	if m.Contributions != 150 {
		t.Errorf("contributions = %v, want 150 (freed instalment 100 + repayment 50)", m.Contributions)
	}
	fundGrowth := res.Months[3].Balances[1] - res.Months[2].Balances[1]
	if fundGrowth != 150 {
		t.Errorf("fund grows %v a month after payoff, want 150", fundGrowth)
	}

	// Without redirects the money stays as unallocated surplus in cash.
	in.Debts[0].RedirectAccountID = nil
	in.Prepayments[0].RedirectAccountID = nil
	res = Simulate(in)
	if res.Months[7].Balances[1] != 0 {
		t.Errorf("fund should stay empty without redirects: %v", res.Months[7].Balances[1])
	}
}

func sum(xs []float64) float64 {
	var t float64
	for _, x := range xs {
		t += x
	}
	return t
}

func TestFlowBalancesEveryMonth(t *testing.T) {
	fund := int64(2)
	a := flat()
	a.TaxRate = 0.2
	a.Returns[domain.AccountInvestment] = 0.05
	in := Input{
		People: []domain.Person{personFor(40)},
		Accounts: []domain.Account{
			{ID: 1, Kind: domain.AccountCash, Balance: 300000},
			{ID: 2, Kind: domain.AccountInvestment, Balance: 1000000, MonthlyContribution: 20000},
		},
		Incomes: []domain.Income{
			{Amount: 250000, PaymentsPerYear: 14, End: start.AddMonths(20)},
			{Amount: 30000, PaymentsPerYear: 12},
		},
		Expenses: []domain.Expense{
			{Amount: 90000, PaymentsPerYear: 12, Category: domain.ExpenseHousing},
			{Amount: 60000, PaymentsPerYear: 1, Category: domain.ExpenseLeisure},
		},
		Debts: []domain.Debt{{ID: 1, Balance: 600000, AnnualRate: 0.04, MonthlyPayment: 50000, RedirectAccountID: &fund}},
		Prepayments: []domain.Prepayment{{DebtID: 1, Amount: 20000, Frequency: domain.PrepayMonthly,
			Mode: domain.ReduceTerm, RedirectAccountID: &fund}},
		Assumptions: a,
		Start:       start,
		Detail:      true,
	}
	res := Simulate(in)
	for _, m := range res.Months {
		f := m.Flow
		in := sum(f.Income) + sum(f.Withdrawals) + m.Shortfall
		out := m.Tax + sum(f.Expenses) + sum(f.Debts) + sum(f.Contributions) + m.SurplusSaved
		if !near(in, out, 1e-6) {
			t.Fatalf("%v: in %.4f != out %.4f", m.Month, in, out)
		}
		if !near(sum(f.Income), m.Income, 1e-9) || !near(sum(f.Debts), m.DebtPayments, 1e-9) ||
			!near(sum(f.Contributions), m.Contributions, 1e-9) || !near(sum(f.Withdrawals), m.Withdrawals, 1e-9) {
			t.Fatalf("%v: flow does not match the totals", m.Month)
		}
	}
	// The salary ends in month 20: later months draw on savings.
	if sum(res.Months[30].Flow.Withdrawals) == 0 {
		t.Error("expected withdrawals once the salary has ended")
	}
	years := res.Years()
	if years[0].Months != 2 || years[1].Months != 12 || years[1].Flow == nil {
		t.Errorf("years = %d/%d months, flow %v", years[0].Months, years[1].Months, years[1].Flow != nil)
	}
	if got, want := sum(years[1].Flow.Expenses), years[1].Expenses; !near(got, want, 1e-6) {
		t.Errorf("yearly flow expenses %v != %v", got, want)
	}

	// Without Detail no breakdown is allocated.
	in.Detail = false
	if Simulate(in).Months[0].Flow != nil {
		t.Error("Flow should be nil without Detail")
	}
}

// retiree: 60 years old, 500.000 € in investments, spending 25.000 €/year,
// strategy from the start of the plan.
func retiree(strategy WithdrawalStrategy, returns float64) Input {
	a := flat()
	a.Returns[domain.AccountInvestment] = returns
	a.Withdrawal = WithdrawalSettings{Strategy: strategy, StartMS: ptr(int64(1)), Rate: 0.04, Floor: 0.5, Ceiling: 2}
	return Input{
		People:      []domain.Person{{ID: 1, Birth: start.AddMonths(-60 * 12), LifeExpectancy: 90}},
		Milestones:  []domain.Milestone{{ID: 1, Name: "Ya", Kind: domain.MilestoneDate, Date: start}},
		Accounts:    []domain.Account{{ID: 1, Kind: domain.AccountInvestment, Balance: 50000000}},
		Expenses:    []domain.Expense{{Amount: 2500000, PaymentsPerYear: 1}},
		Assumptions: a,
		Start:       start,
	}
}

func yearSpend(res Result, y int) float64 {
	var s float64
	for _, m := range res.Months[12*y : 12*y+12] {
		s += m.Expenses
	}
	return s
}

func TestWithdrawalStrategies(t *testing.T) {
	// Fixed: spends exactly the plan.
	fixed := Simulate(retiree(WithdrawFixed, 0))
	if got := yearSpend(fixed, 0); got != 25000 {
		t.Errorf("fixed spends %v, want 25000", got)
	}
	if !fixed.WithdrawalFrom.IsZero() {
		t.Error("fixed strategy should not start")
	}

	// 4 %: 4 % of 500.000 € = 20.000 € a year (factor 0,8), kept constant with no inflation.
	r4 := Simulate(retiree(WithdrawRule4, 0))
	if got := yearSpend(r4, 0); math.Abs(got-20000) > 1e-6 {
		t.Errorf("4%% rule first year = %v, want 20000", got)
	}
	if got := yearSpend(r4, 5); math.Abs(got-20000) > 1e-6 {
		t.Errorf("4%% rule year 6 = %v, want 20000 (no inflation)", got)
	}
	if r4.WithdrawalFrom != start || r4.Months[0].SpendFactor != 0.8 {
		t.Errorf("start %v, factor %v", r4.WithdrawalFrom, r4.Months[0].SpendFactor)
	}

	// Fixed percentage: with 0 % returns savings shrink, so spending falls every year.
	pct := Simulate(retiree(WithdrawPercent, 0))
	if !(yearSpend(pct, 1) < yearSpend(pct, 0) && yearSpend(pct, 10) < yearSpend(pct, 1)) {
		t.Errorf("percent rule should spend less as savings fall: %v %v %v",
			yearSpend(pct, 0), yearSpend(pct, 1), yearSpend(pct, 10))
	}
	if pct.Failed() {
		t.Error("a percentage rule never runs out of money")
	}

	// Guardrails with falling savings cut spending, but never below the floor (50 %).
	g := Simulate(retiree(WithdrawGuardrails, -0.05))
	minF := 1.0
	for _, m := range g.Months {
		minF = math.Min(minF, m.SpendFactor)
	}
	if minF < 0.5-1e-12 || yearSpend(g, 15) >= yearSpend(g, 0) {
		t.Errorf("guardrails: min factor %v, year16 %v vs year1 %v", minF, yearSpend(g, 15), yearSpend(g, 0))
	}
}

func TestWithdrawalOrderAndPensionLock(t *testing.T) {
	in := Input{
		People:     []domain.Person{personFor(24)},
		Milestones: []domain.Milestone{{ID: 9, Name: "Jubilación", Kind: domain.MilestoneDate, Date: start.AddMonths(12)}},
		Accounts: []domain.Account{
			{ID: 1, Kind: domain.AccountCash, Balance: 100000},
			{ID: 2, Kind: domain.AccountPension, Balance: 100000},
			{ID: 3, Kind: domain.AccountInvestment, Balance: 100000},
		},
		Expenses:    []domain.Expense{{Amount: 50000, PaymentsPerYear: 12}},
		Assumptions: flat(),
		Start:       start,
	}
	// Custom order: investments first.
	in.Assumptions.Order = ParseOrder("investment,cash,deposit,other,pension")
	res := Simulate(in)
	if b := res.Months[1].Balances; b[2] != 0 || b[0] != 1000 {
		t.Errorf("investments should go first: %v", b)
	}
	// Pension plan locked until retirement (month 12): months 4..11 run short.
	in.Assumptions.Order = nil
	in.Assumptions.PensionAvailableMS = ptr(int64(9))
	res = Simulate(in)
	if !res.Failed() || res.FailMonth != start.AddMonths(4) {
		t.Errorf("locked pension: fail month %v", res.FailMonth)
	}
	if res.Months[11].Balances[1] != 1000 || res.Months[12].Balances[1] >= 1000 {
		t.Errorf("pension should be untouched until month 12: %v / %v", res.Months[11].Balances[1], res.Months[12].Balances[1])
	}
	if ParseOrder("cash,cash,deposit,other,pension") != nil || ParseOrder("cash,deposit") != nil {
		t.Error("invalid orders must be rejected")
	}
}

func TestIRPFWithholdingAndSettlement(t *testing.T) {
	// A 30.000 €/year salary (12 payments) from January 2027 for two full years.
	jan := domain.Month{Year: 2027, Month: 1}
	a := flat()
	a.TaxModel, a.TaxRegion = TaxES, "general"
	in := Input{
		People:      []domain.Person{{ID: 1, Name: "Ana", Birth: domain.Month{Year: 1990, Month: 1}, LifeExpectancy: 39}},
		Accounts:    []domain.Account{{ID: 1, Kind: domain.AccountCash}},
		Incomes:     []domain.Income{{Kind: domain.IncomeSalary, OwnerID: ptr(int64(1)), Amount: 250000, PaymentsPerYear: 12}},
		Assumptions: a,
		Start:       jan,
	}
	res := Simulate(in)
	if len(res.TaxYears) < 2 {
		t.Fatalf("tax years = %d", len(res.TaxYears))
	}
	y := res.TaxYears[0]
	if y.Year != 2027 || y.Partial || math.Abs(y.IRPF()-4927.80) > 0.05 {
		t.Errorf("2027 IRPF = %.2f, want 4927.80 (%+v)", y.IRPF(), y)
	}
	// Withholding estimated from the same salary: settlement ≈ 0.
	if math.Abs(y.Settlement) > 0.05 {
		t.Errorf("settlement = %.2f, want ≈ 0", y.Settlement)
	}
	// Monthly charge = Social Security 6,48 % + withholding (4.927,80 / 30.000).
	want := 2500*0.0648 + 2500*4927.80/30000
	if got := res.Months[0].Tax; math.Abs(got-want) > 0.01 {
		t.Errorf("monthly tax = %.2f, want %.2f", got, want)
	}
}

func TestIRPFSettlementForPensionPlanWithdrawals(t *testing.T) {
	// Retiree with no income living off a pension plan: nothing is withheld,
	// so the withdrawals' IRPF is paid in June of the next year.
	jan := domain.Month{Year: 2027, Month: 1}
	a := flat()
	a.TaxModel, a.TaxRegion = TaxES, "general"
	in := Input{
		People:      []domain.Person{{ID: 1, Name: "Ana", Birth: domain.Month{Year: 1957, Month: 1}, LifeExpectancy: 72}},
		Accounts:    []domain.Account{{ID: 1, Kind: domain.AccountPension, OwnerID: ptr(int64(1)), Balance: 50000000}},
		Expenses:    []domain.Expense{{Amount: 200000, PaymentsPerYear: 12}}, // 24.000 €/year
		Assumptions: a,
		Start:       jan,
	}
	res := Simulate(in)
	y := res.TaxYears[0]
	if y.Withheld != 0 || y.People[0].Income.PlanWithdrawal < 23999 {
		t.Fatalf("2027 = %+v", y)
	}
	if y.Settlement <= 0 {
		t.Fatalf("withdrawals should owe IRPF, settlement %.2f", y.Settlement)
	}
	june := res.Months[17] // June 2028
	if june.Month != (domain.Month{Year: 2028, Month: 6}) || math.Abs(june.Tax-y.Settlement) > 0.01 {
		t.Errorf("June 2028 tax = %.2f, want the settlement %.2f", june.Tax, y.Settlement)
	}
}

func TestIRPFTaxesOnlyInvestmentGains(t *testing.T) {
	jan := domain.Month{Year: 2027, Month: 1}
	a := flat()
	a.TaxModel, a.TaxRegion = TaxES, "general"
	a.Returns[domain.AccountInvestment] = 0.10
	in := Input{
		People:      []domain.Person{{ID: 1, Name: "Ana", Birth: domain.Month{Year: 1980, Month: 1}, LifeExpectancy: 49}},
		Accounts:    []domain.Account{{ID: 1, Kind: domain.AccountInvestment, OwnerID: ptr(int64(1)), Balance: 10000000}},
		Expenses:    []domain.Expense{{Amount: 100000, PaymentsPerYear: 12, Start: domain.Month{Year: 2028, Month: 1}}},
		Assumptions: a,
		Start:       jan,
	}
	res := Simulate(in)
	// 2027: no withdrawals, so no savings income despite the growth.
	if got := res.TaxYears[0].People[0].Income.Savings; got != 0 {
		t.Errorf("2027 savings income = %.2f, want 0 (unrealised)", got)
	}
	// 2028: withdrawals of 12.000 € from an account that grew ~10 %: only the gain part is taxed.
	s28 := res.TaxYears[1].People[0].Income.Savings
	if s28 <= 0 || s28 > 0.2*12000 {
		t.Errorf("2028 realised gains = %.2f, want a fraction of the 12.000 € withdrawn", s28)
	}
}
