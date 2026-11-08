package engine

import (
	"testing"

	"github.com/igomezal/horizonte/internal/domain"
)

var saleStart = domain.Month{Year: 2027, Month: 1}

func saleDate(months int) domain.Month { return saleStart.AddMonths(months) }

// saleEs returns assumptions with no returns or inflation and the Spanish tax.
func saleEs() Assumptions {
	a := flat()
	a.TaxModel, a.TaxRegion = TaxES, "general"
	return a
}

// aged is a person who is age in January 2027 and lives another 40 years.
func aged(id int64, age int) domain.Person {
	return domain.Person{ID: id, Name: "P", Birth: domain.Month{Year: 2027 - age, Month: 1}, LifeExpectancy: age + 40}
}

// salePlan is one person, a current account and a home sold in month 2.
func salePlan(a Assumptions, sale domain.PropertySale, months int) Input {
	if sale.Share == 0 {
		sale.Share = 1
	}
	if sale.Month.IsZero() && sale.MS == nil {
		sale.Month = saleDate(2)
	}
	return Input{
		People: []domain.Person{{ID: 1, Name: "Ana", Birth: domain.Month{Year: 1980, Month: 1},
			LifeExpectancy: 47 + months/12}},
		Accounts: []domain.Account{
			{ID: 1, Kind: domain.AccountCash, Balance: 100000},
			{ID: 2, Kind: domain.AccountRealEstate, Balance: 40000000, Sale: &sale}, // 400.000 €
		},
		Assumptions: a,
		Start:       saleStart,
	}
}

func TestFullSale(t *testing.T) {
	in := salePlan(flat(), domain.PropertySale{CostRate: 0.05}, 24)
	res := Simulate(in)
	m := res.Months[2]
	if m.Balances[1] != 0 || !near(m.Balances[0], 1000+380000, 1e-6) {
		t.Fatalf("balances = %v", m.Balances)
	}
	if res.Months[1].Balances[1] != 400000 {
		t.Errorf("sold too early: %v", res.Months[1].Balances)
	}
	if len(res.Sales) != 1 || res.Sales[0].Month != saleDate(2) || !near(res.Sales[0].Costs, 20000, 1e-6) || !near(res.Sales[0].Net, 380000, 1e-6) {
		t.Errorf("sales = %+v", res.Sales)
	}
	// The sale is an exchange of assets, less the costs.
	if got := res.Months[2].NetWorth - res.Months[1].NetWorth; !near(got, -20000, 1e-6) {
		t.Errorf("net worth moved by %v, want only the costs", got)
	}
}

func TestSoldPropertyTakesNoMoreMoney(t *testing.T) {
	fund := int64(1)
	home := int64(2)
	a := flat()
	a.SurplusAccountID = &fund
	in := salePlan(a, domain.PropertySale{}, 12)
	in.Accounts[1].MonthlyContribution = 10000
	in.Incomes = []domain.Income{{Amount: 50000, PaymentsPerYear: 12}}
	in.Debts = []domain.Debt{{ID: 1, Balance: 10000, MonthlyPayment: 10000, RedirectAccountID: &home}}
	res := Simulate(in)
	last := res.Months[len(res.Months)-1]
	if last.Balances[1] != 0 {
		t.Errorf("sold property holds %v", last.Balances[1])
	}
	if res.Months[1].Balances[1] <= 400000 {
		t.Errorf("contributions should run until the sale: %v", res.Months[1].Balances[1])
	}
}

func TestPartialSale(t *testing.T) {
	basis := domain.Cents(10000000) // 100.000 €
	a := saleEs()
	in := salePlan(a, domain.PropertySale{Share: 0.25, CostRate: 0.04}, 36)
	in.Accounts[1].CostBasis = &basis
	res := Simulate(in)
	if got := res.Months[2].Balances[1]; !near(got, 300000, 1e-6) {
		t.Fatalf("property after a quarter sold = %v", got)
	}
	s := res.Sales[0]
	// 100.000 sold, 4.000 costs, 25.000 of basis.
	if !near(s.Net, 96000, 1e-6) || !near(s.Gain, 71000, 1e-6) {
		t.Errorf("sale = %+v", s)
	}
	if got := res.TaxYears[0].People[0].Income.Savings; !near(got, 71000, 1e-6) {
		t.Errorf("savings income = %v", got)
	}
}

func TestSaleTaxWithAndWithoutBasis(t *testing.T) {
	a := saleEs()
	a.Returns[domain.AccountRealEstate] = 0.12
	withoutBasis := salePlan(a, domain.PropertySale{}, 36)
	base := Simulate(withoutBasis)
	gain := base.Sales[0].Gain
	if gain <= 0 || gain > 15000 { // only the appreciation since the start
		t.Fatalf("gain from the starting balance = %v", gain)
	}

	basis := domain.Cents(25000000) // bought for 250.000 €
	withBasis := salePlan(a, domain.PropertySale{}, 36)
	withBasis.Accounts[1].CostBasis = &basis
	res := Simulate(withBasis)
	if want := res.Sales[0].Net - 250000; !near(res.Sales[0].Gain, want, 1e-6) {
		t.Fatalf("gain with basis = %v, want %v", res.Sales[0].Gain, want)
	}
	y := res.TaxYears[0]
	if !near(y.People[0].Income.Savings, res.Sales[0].Gain, 1e-6) || y.IRPF() <= 0 {
		t.Fatalf("tax year = %+v", y)
	}
	// Net proceeds arrive untaxed, and the tax is paid in June of the next
	// year, exactly once.
	if !near(res.Months[2].Balances[0], 1000+res.Sales[0].Net, 1e-6) {
		t.Errorf("cash after the sale = %v", res.Months[2].Balances[0])
	}
	june := saleStart.MonthsUntil(domain.Month{Year: 2028, Month: 6})
	if got, want := res.Months[june].Tax, y.Settlement; !near(got, want, 1e-6) || got <= 0 {
		t.Errorf("June tax = %v, want the settlement %v", got, want)
	}
	if got := res.Months[june-1].Tax; got != 0 {
		t.Errorf("May tax = %v", got)
	}
	if res.Months[june].Balances[0] >= res.Months[june-1].Balances[0] {
		t.Error("the settlement should come out of the cash")
	}
}

func TestFlatTaxLeavesTheSaleUntaxed(t *testing.T) {
	a := flat()
	a.TaxRate = 0.3
	basis := domain.Cents(0)
	in := salePlan(a, domain.PropertySale{}, 36)
	in.Accounts[1].CostBasis = &basis
	res := Simulate(in)
	for i, m := range res.Months {
		if m.Tax != 0 {
			t.Fatalf("month %d tax = %v", i, m.Tax)
		}
	}
	if !near(res.Months[2].Balances[0], 401000, 1e-6) {
		t.Errorf("cash = %v", res.Months[2].Balances[0])
	}
}

func TestSaleInAPartialYearStillPaysTax(t *testing.T) {
	in := salePlan(saleEs(), domain.PropertySale{Month: start}, 36)
	in.Start = start // November: the first calendar year is partial
	in.People[0].LifeExpectancy = 50
	basis := domain.Cents(0)
	in.Accounts[1].CostBasis = &basis
	res := Simulate(in)
	y := res.TaxYears[0]
	if !y.Partial || y.Settlement <= 0 {
		t.Fatalf("year = %+v", y)
	}
	june := start.MonthsUntil(domain.Month{Year: 2027, Month: 6})
	if !near(res.Months[june].Tax, y.Settlement, 1e-6) {
		t.Errorf("June tax = %v, want %v", res.Months[june].Tax, y.Settlement)
	}
}

func TestMainResidenceExemption(t *testing.T) {
	savings := func(people []domain.Person, owner *int64, main bool) []float64 {
		basis := domain.Cents(0)
		sale := domain.PropertySale{Share: 1, Month: saleDate(2), MainResidence: main}
		res := Simulate(Input{People: people,
			Accounts: []domain.Account{{ID: 1, Kind: domain.AccountCash}, {ID: 2, Kind: domain.AccountRealEstate, Balance: 10000000,
				OwnerID: owner, CostBasis: &basis, Sale: &sale}},
			Assumptions: saleEs(), Start: saleStart})
		var out []float64
		for _, p := range res.TaxYears[0].People {
			out = append(out, p.Income.Savings)
		}
		return out
	}
	id := int64(1)
	if got := savings([]domain.Person{aged(1, 66)}, &id, true); got[0] != 0 {
		t.Errorf("over 65, main residence: taxed on %v", got[0])
	}
	if got := savings([]domain.Person{aged(1, 65)}, &id, true); got[0] != 0 {
		t.Errorf("turned 65: taxed on %v", got[0])
	}
	if got := savings([]domain.Person{aged(1, 64)}, &id, true); got[0] != 100000 {
		t.Errorf("under 65: gain = %v", got[0])
	}
	if got := savings([]domain.Person{aged(1, 70)}, &id, false); got[0] != 100000 {
		t.Errorf("not a main residence: gain = %v", got[0])
	}
	// Joint: each owner's half follows their own age.
	got := savings([]domain.Person{aged(1, 70), aged(2, 60)}, nil, true)
	if got[0] != 0 || got[1] != 50000 {
		t.Errorf("joint sale = %v", got)
	}
}

func TestSaleCanRescueAFailingPlan(t *testing.T) {
	build := func(sale bool) Input {
		in := salePlan(flat(), domain.PropertySale{Month: saleDate(11)}, 30)
		in.Accounts[0].Balance = 1200000 // twelve months of spending
		in.Expenses = []domain.Expense{{Amount: 100000, PaymentsPerYear: 12}}
		if !sale {
			in.Accounts[1].Sale = nil
		}
		return in
	}
	if !Simulate(build(false)).Failed() {
		t.Fatal("the plan should fail without selling")
	}
	if res := Simulate(build(true)); res.Failed() {
		t.Errorf("the plan should survive the sale; failed in %v", res.FailMonth)
	}
}

func TestSaleWorksInMonteCarloAndBacktest(t *testing.T) {
	build := func(sale bool) Input {
		in := salePlan(flat(), domain.PropertySale{Month: saleDate(11)}, 30)
		in.Accounts[0].Balance = 1200000 // twelve months of spending
		in.Expenses = []domain.Expense{{Amount: 100000, PaymentsPerYear: 12}}
		if !sale {
			in.Accounts[1].Sale = nil
		}
		return in
	}
	set := MCSettings{Runs: 20, Seed: 3}
	if got := MonteCarlo(build(false), DefaultVolatility(), set).SuccessRate(); got != 0 {
		t.Fatalf("Monte Carlo without the sale: success %v", got)
	}
	if got := MonteCarlo(build(true), DefaultVolatility(), set).SuccessRate(); got != 1 {
		t.Errorf("Monte Carlo with the sale: success %v", got)
	}
	series := synthetic(1950, 60, 0.005, 0.002, 0.001)
	if res, _ := Backtest(build(false), series, domain.Month{Year: 1950, Month: 1}); !res.Failed() {
		t.Fatal("backtest should fail without the sale")
	}
	res, _ := Backtest(build(true), series, domain.Month{Year: 1950, Month: 1})
	if res.Failed() || len(res.Sales) != 1 {
		t.Errorf("backtest with the sale: failed=%v sales=%d", res.Failed(), len(res.Sales))
	}
}

func TestSaleTimedByMilestones(t *testing.T) {
	ms := int64(7)
	in := salePlan(flat(), domain.PropertySale{MS: &ms}, 60)
	in.Milestones = []domain.Milestone{{ID: 7, Kind: domain.MilestoneDate, Date: saleDate(5)}}
	res := Simulate(in)
	if len(res.Sales) != 1 || res.Sales[0].Month != saleDate(5) {
		t.Fatalf("sales = %+v", res.Sales)
	}

	// Waiting for financial independence: nothing happens until it arrives.
	in = salePlan(flat(), domain.PropertySale{MS: &ms}, 60)
	in.Milestones = []domain.Milestone{{ID: 7, Kind: domain.MilestoneFI, FIVariant: domain.FILean, FIMultiple: 5, FIAmount: 1000000}}
	in.Incomes = []domain.Income{{Amount: 100000, PaymentsPerYear: 12}}
	res = Simulate(in)
	if len(res.Sales) != 1 {
		t.Fatalf("sales = %+v", res.Sales)
	}
	// 5 × 10.000 € saved, at 1.000 €/month from 1.000 €: reached after month 48.
	if res.Sales[0].Month != saleDate(49) {
		t.Errorf("sold in %v", res.Sales[0].Month)
	}

	in.Incomes = nil
	if res := Simulate(in); len(res.Sales) != 0 {
		t.Errorf("milestone never reached: sales = %+v", res.Sales)
	}
}

func TestSaleDatedBeforeThePlanNeverHappens(t *testing.T) {
	in := salePlan(flat(), domain.PropertySale{Month: saleStart.AddMonths(-3)}, 12)
	if res := Simulate(in); len(res.Sales) != 0 || res.Months[0].Balances[1] != 400000 {
		t.Errorf("sales = %+v", res.Sales)
	}
}

func TestSaleDestination(t *testing.T) {
	fund := int64(3)
	a := flat()
	in := salePlan(a, domain.PropertySale{DestinationID: &fund}, 12)
	in.Accounts = append(in.Accounts, domain.Account{ID: 3, Kind: domain.AccountInvestment})
	m := Simulate(in).Months[2]
	if !near(m.Balances[2], 400000, 1e-6) || !near(m.Balances[0], 1000, 1e-6) {
		t.Errorf("explicit destination: %v", m.Balances)
	}

	// By default the surplus account, unless it is a pension plan.
	in = salePlan(a, domain.PropertySale{}, 12)
	in.Accounts = append(in.Accounts, domain.Account{ID: 3, Kind: domain.AccountInvestment})
	in.Assumptions.SurplusAccountID = &fund
	if m := Simulate(in).Months[2]; !near(m.Balances[2], 400000, 1e-6) {
		t.Errorf("surplus destination: %v", m.Balances)
	}
	in.Accounts[2].Kind = domain.AccountPension
	if m := Simulate(in).Months[2]; !near(m.Balances[0], 401000, 1e-6) || m.Balances[2] != 0 {
		t.Errorf("a pension plan must not receive the money: %v", m.Balances)
	}

	// With nowhere to put the money the property stays.
	in = salePlan(a, domain.PropertySale{}, 12)
	in.Accounts = in.Accounts[1:]
	if res := Simulate(in); len(res.Sales) != 0 || res.Months[3].Balances[0] != 400000 {
		t.Errorf("no destination: %+v", res.Sales)
	}
}

func TestFlowBalancesWithASale(t *testing.T) {
	a := saleEs()
	a.Returns[domain.AccountRealEstate] = 0.05
	basis := domain.Cents(10000000)
	in := salePlan(a, domain.PropertySale{Share: 0.5, CostRate: 0.05, Month: saleDate(14)}, 40)
	in.Accounts[1].CostBasis = &basis
	in.Incomes = []domain.Income{{Amount: 250000, PaymentsPerYear: 14, End: saleDate(20)}}
	in.Expenses = []domain.Expense{{Amount: 200000, PaymentsPerYear: 12, Category: domain.ExpenseHousing}}
	in.Detail = true
	res := Simulate(in)
	if len(res.Sales) != 1 {
		t.Fatalf("sales = %+v", res.Sales)
	}
	for _, m := range res.Months {
		f := m.Flow
		in, out := sum(f.Income)+sum(f.Withdrawals)+m.Shortfall, m.Tax+sum(f.Expenses)+sum(f.Debts)+sum(f.Contributions)+m.SurplusSaved
		if !near(in, out, 1e-6) {
			t.Fatalf("%v: in %.4f != out %.4f", m.Month, in, out)
		}
	}
}

func TestSaleValidation(t *testing.T) {
	ok := domain.Account{Name: "Piso", Kind: domain.AccountRealEstate, Sale: &domain.PropertySale{Share: 1, Month: start}}
	if errs := ok.Validate(); len(errs) != 0 {
		t.Fatalf("valid sale: %v", errs)
	}
	for field, change := range map[string]func(*domain.Account){
		"sale_month":   func(a *domain.Account) { a.Sale.Month = domain.Month{} },
		"sale_share":   func(a *domain.Account) { a.Sale.Share = 1.5 },
		"sale_cost":    func(a *domain.Account) { a.Sale.CostRate = -0.01 },
		"sale_enabled": func(a *domain.Account) { a.Kind = domain.AccountCash },
	} {
		s := *ok.Sale
		a := ok
		a.Sale = &s
		change(&a)
		if errs := a.Validate(); errs[field] == "" {
			t.Errorf("%s not rejected: %v", field, errs)
		}
	}
}

// lateSale sells the house (cost 100.000 €, worth 400.000 €) in month saleAt
// of a plan that ends at planEnd.
func lateSale(saleAt, planEnd domain.Month) Input {
	basis := domain.Cents(10000000)
	in := salePlan(saleEs(), domain.PropertySale{Month: saleAt}, 36)
	in.Accounts[1].CostBasis = &basis
	in.People[0].Birth = domain.Month{Year: planEnd.Year - in.People[0].LifeExpectancy, Month: planEnd.Month}
	return in
}

func taxYear(res Result, year int) TaxYear {
	for _, y := range res.TaxYears {
		if y.Year == year {
			return y
		}
	}
	return TaxYear{}
}

func TestSaleTaxIsPaidWhenTheSettlementFallsAfterThePlan(t *testing.T) {
	end := domain.Month{Year: 2030, Month: 1}
	early := Simulate(lateSale(domain.Month{Year: 2027, Month: 3}, end))
	res := Simulate(lateSale(domain.Month{Year: 2029, Month: 7}, end))
	last := res.Months[len(res.Months)-1]
	if last.Month != end {
		t.Fatalf("plan ends in %v", last.Month)
	}
	y := taxYear(res, 2029)
	if !near(y.Settlement, 70825.50, 0.01) || !near(last.Tax, y.Settlement, 1e-6) {
		t.Errorf("last month tax = %v, settlement of the sale = %v", last.Tax, y.Settlement)
	}
	if !near(last.NetWorth, 401000-y.Settlement, 1e-6) {
		t.Errorf("final net worth = %v, want the sale net of tax", last.NetWorth)
	}
	if e := early.Months[len(early.Months)-1].NetWorth; !near(last.NetWorth, e, 1e-6) {
		t.Errorf("late sale ends at %v, the same sale early ends at %v", last.NetWorth, e)
	}
}

func TestSaleTaxInTheFinalYear(t *testing.T) {
	end := domain.Month{Year: 2030, Month: 1}
	early := Simulate(lateSale(domain.Month{Year: 2027, Month: 3}, end))
	want := early.Months[len(early.Months)-1].NetWorth
	for _, sold := range []domain.Month{end, {Year: 2029, Month: 12}} {
		months := Simulate(lateSale(sold, end)).Months
		last := months[len(months)-1]
		if !near(last.NetWorth, want, 1e-6) {
			t.Errorf("sold %v: final net worth = %v, want %v (tax in last month %v)", sold, last.NetWorth, want, last.Tax)
		}
	}
}

func TestSaleTaxIsChargedOnceWhenThePlanEndsInJune(t *testing.T) {
	end := domain.Month{Year: 2030, Month: 6}
	res := Simulate(lateSale(domain.Month{Year: 2029, Month: 7}, end))
	y := taxYear(res, 2029)
	if got := res.Months[len(res.Months)-1].Tax; !near(got, y.Settlement, 1e-6) || got <= 0 {
		t.Errorf("June tax = %v, want the settlement %v once", got, y.Settlement)
	}
}

func TestExemptSalePaysNoFinalTax(t *testing.T) {
	end := domain.Month{Year: 2030, Month: 1}
	in := lateSale(domain.Month{Year: 2029, Month: 7}, end)
	in.Accounts[1].Sale.MainResidence = true
	in.People[0].Birth = domain.Month{Year: 1950, Month: 1}
	in.People[0].LifeExpectancy = 80
	res := Simulate(in)
	if got := res.Months[len(res.Months)-1].Tax; got != 0 {
		t.Errorf("last month tax = %v", got)
	}
}

func TestOrdinarySettlementsStayUnpaidAtTheEnd(t *testing.T) {
	// A salary, no sale, and a plan that ends in January: the last month pays
	// its own charges and nothing for the settlement due the June after.
	end := domain.Month{Year: 2030, Month: 1}
	in := lateSale(end, end)
	in.Accounts[1].Sale = nil
	in.Incomes = []domain.Income{{Name: "Nomina", Kind: domain.IncomeSalary, Amount: 500000, PaymentsPerYear: 12}}
	res := Simulate(in)
	y := res.TaxYears[len(res.TaxYears)-1]
	if got, want := res.Months[len(res.Months)-1].Tax, y.Withheld+y.People[0].Breakdown.SocialSecurity; !near(got, want, 1e-6) {
		t.Errorf("last month tax = %v, want only withholding and Social Security (%v)", got, want)
	}
}

func TestFlowBalancesWithASaleInTheLastMonth(t *testing.T) {
	end := domain.Month{Year: 2030, Month: 1}
	in := lateSale(end, end)
	in.Detail = true
	res := Simulate(in)
	for _, m := range res.Months {
		f := m.Flow
		got := sum(f.Income) + sum(f.Withdrawals) + m.Shortfall
		want := m.Tax + sum(f.Expenses) + sum(f.Debts) + sum(f.Contributions) + m.SurplusSaved
		if !near(got, want, 1e-6) {
			t.Fatalf("%v: in %.4f != out %.4f", m.Month, got, want)
		}
	}
	if res.Months[len(res.Months)-1].Tax <= 0 {
		t.Error("the sale in the last month should pay its tax there")
	}
}

func TestSaleSkippedIsReported(t *testing.T) {
	in := salePlan(flat(), domain.PropertySale{Month: saleStart.AddMonths(3)}, 12)
	in.Accounts = in.Accounts[1:]
	res := Simulate(in)
	if len(res.Sales) != 0 || len(res.SalesSkipped) != 1 || res.SalesSkipped[0].ID != 2 {
		t.Errorf("sales = %+v, skipped = %+v", res.Sales, res.SalesSkipped)
	}
}

func TestSaleLapsed(t *testing.T) {
	r := domain.Resolved{7: saleStart.AddMonths(-2), 8: saleStart.AddMonths(4)}
	ms := func(id int64) *int64 { return &id }
	cases := []struct {
		sale domain.PropertySale
		want bool
	}{
		{domain.PropertySale{Month: saleStart.AddMonths(-1)}, true},
		{domain.PropertySale{Month: saleStart}, false},
		{domain.PropertySale{MS: ms(7)}, true},
		{domain.PropertySale{MS: ms(8)}, false},
		{domain.PropertySale{MS: ms(9)}, false}, // not reached yet
	}
	for _, c := range cases {
		if got := c.sale.Lapsed(r, saleStart); got != c.want {
			t.Errorf("%+v: lapsed = %v", c.sale, got)
		}
	}
}
