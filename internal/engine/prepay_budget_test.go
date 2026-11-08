package engine

import (
	"testing"

	"github.com/igomezal/horizonte/internal/domain"
)

// budgetInput is a plan with no returns, a 100 €/month instalment on a large
// debt and one once-off repayment of amount cents in the first month.
func budgetInput(months int, a Assumptions, accounts []domain.Account, income, amount domain.Cents) Input {
	in := Input{
		People:   []domain.Person{personFor(months)},
		Accounts: accounts,
		Debts:    []domain.Debt{{ID: 1, Balance: 10000000, MonthlyPayment: 10000}},
		Prepayments: []domain.Prepayment{{DebtID: 1, Amount: amount, Frequency: domain.PrepayOnce,
			Mode: domain.ReduceTerm, Start: start}},
		Assumptions: a,
		Start:       start,
	}
	if income > 0 {
		in.Incomes = []domain.Income{{Amount: income, PaymentsPerYear: 12}}
	}
	return in
}

func TestPrepaymentLeavesMoneyForThisMonthsDeficit(t *testing.T) {
	in := budgetInput(1, flat(), []domain.Account{{ID: 1, Kind: domain.AccountCash, Balance: 100000}}, 0, 100000)
	res := Simulate(in)
	m := res.Months[0]
	if !near(m.Prepayments, 900, 1e-9) {
		t.Errorf("prepayment = %v, want 900 (1.000 € less the 100 € deficit)", m.Prepayments)
	}
	if m.Shortfall != 0 || res.Failed() {
		t.Errorf("shortfall = %v, the repayment must not cause one", m.Shortfall)
	}
	if !near(m.Balances[0], 0, 1e-9) {
		t.Errorf("cash = %v, want 0", m.Balances[0])
	}
}

func TestPrepaymentsStopAtTheReserve(t *testing.T) {
	a := flat()
	a.PrepaymentReserve = 40000 // 400 €
	in := budgetInput(6, a, []domain.Account{{ID: 1, Kind: domain.AccountCash, Balance: 100000}}, 10000, 20000)
	in.Prepayments[0].Frequency = domain.PrepayMonthly
	res := Simulate(in)
	var paid float64
	for _, m := range res.Months {
		paid += m.Prepayments
	}
	if !near(paid, 600, 1e-9) {
		t.Errorf("repaid %v, want 600 (1.000 € down to the 400 € reserve)", paid)
	}
	if got := res.Months[5].Balances[0]; !near(got, 400, 1e-9) {
		t.Errorf("cash = %v, want the 400 € reserve", got)
	}
}

func TestReserveLeavesThisMonthsSurplusAvailable(t *testing.T) {
	a := flat()
	a.PrepaymentReserve = 500000 // above everything held
	in := budgetInput(1, a, []domain.Account{{ID: 1, Kind: domain.AccountCash, Balance: 100000}}, 30000, 100000)
	if got := Simulate(in).Months[0].Prepayments; !near(got, 200, 1e-9) {
		t.Errorf("prepayment = %v, want the 200 € surplus", got)
	}
}

func TestReserveRisesWithInflation(t *testing.T) {
	a := flat()
	a.Inflation = 0.10
	a.PrepaymentReserve = 90000 // 900 € today
	in := budgetInput(14, a, []domain.Account{{ID: 1, Kind: domain.AccountCash, Balance: 100000}}, 10000, 100000)
	in.Incomes[0].AnnualGrowth = new(float64)
	in.Prepayments[0].Start = start.AddMonths(12)
	m := Simulate(in).Months[12]
	if want := 1000 - 900*m.Deflator; !near(m.Prepayments, want, 1e-6) || want > 10.01 {
		t.Errorf("prepayment = %v, want %v (reserve grown by a year of inflation)", m.Prepayments, want)
	}
}

func TestPensionPlansNeverFundPrepayments(t *testing.T) {
	accounts := []domain.Account{
		{ID: 1, Kind: domain.AccountCash, Balance: 20000},
		{ID: 2, Kind: domain.AccountPension, Balance: 1000000},
		{ID: 3, Kind: domain.AccountRealEstate, Balance: 5000000},
	}
	in := budgetInput(1, flat(), accounts, 0, 500000)
	m := Simulate(in).Months[0]
	// 200 € of cash pays the 100 € deficit, leaving 100 € to repay.
	if !near(m.Prepayments, 100, 1e-9) || !near(m.Balances[0], 0, 1e-9) || !near(m.Balances[1], 10000, 1e-9) {
		t.Errorf("default order: repaid %v, balances %v", m.Prepayments, m.Balances)
	}

	// With pension plans first in the order they still pay the deficit, and
	// only the deficit: the repayment comes out of the cash account.
	a := flat()
	a.Order = []domain.AccountKind{domain.AccountPension, domain.AccountCash, domain.AccountDeposit, domain.AccountInvestment, domain.AccountOther}
	in = budgetInput(1, a, accounts, 0, 500000)
	m = Simulate(in).Months[0]
	if !near(m.Prepayments, 100, 1e-9) {
		t.Errorf("pension first: repaid %v, want 100", m.Prepayments)
	}
	if !near(m.Balances[1], 9900, 1e-9) || !near(m.Balances[0], 100, 1e-9) {
		t.Errorf("pension first: balances %v, the plan should pay only the 100 € deficit", m.Balances)
	}
}

func TestRedirectsRespectTheBudget(t *testing.T) {
	fund := int64(2)
	a := flat()
	a.PrepaymentReserve = 10000 // 100 €
	in := Input{
		People: []domain.Person{personFor(3)},
		Accounts: []domain.Account{
			{ID: 1, Kind: domain.AccountCash, Balance: 30000},
			{ID: 2, Kind: domain.AccountInvestment},
		},
		// Paid off in the first month, so later repayments are redirected.
		Debts:   []domain.Debt{{ID: 1, Balance: 10000, MonthlyPayment: 10000}},
		Incomes: []domain.Income{{Amount: 10000, PaymentsPerYear: 12, End: start}},
		Prepayments: []domain.Prepayment{{DebtID: 1, Amount: 50000, Frequency: domain.PrepayOnce, Mode: domain.ReduceTerm,
			Start: start.AddMonths(1), RedirectAccountID: &fund}},
		Assumptions: a,
		Start:       start,
	}
	m := Simulate(in).Months[1]
	// Cash 300 € with no income or instalment this month, reserve 100 €.
	if !near(m.Redirected, 200, 1e-9) || !near(m.Balances[1], 200, 1e-9) || !near(m.Balances[0], 100, 1e-9) {
		t.Errorf("redirected %v, balances %v, want 200 moved and 100 kept", m.Redirected, m.Balances)
	}
}

func TestNegativeReserveIsRejected(t *testing.T) {
	a := DefaultAssumptions()
	a.PrepaymentReserve = -1
	if errs := a.Validate(); errs["prepay_reserve"] == "" {
		t.Errorf("errors = %v", errs)
	}
}
