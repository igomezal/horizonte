package domain

import "testing"

func ptr[T any](v T) *T { return &v }

func TestMonth(t *testing.T) {
	m, err := ParseMonth("2026-11")
	if err != nil || m != (Month{2026, 11}) {
		t.Fatalf("ParseMonth = %v, %v", m, err)
	}
	if got := m.AddMonths(2); got != (Month{2027, 1}) {
		t.Errorf("AddMonths(2) = %v", got)
	}
	if got := m.AddMonths(-11); got != (Month{2025, 12}) {
		t.Errorf("AddMonths(-11) = %v", got)
	}
	if z, err := ParseMonth(""); err != nil || !z.IsZero() || z.String() != "" {
		t.Errorf("empty month = %v, %v", z, err)
	}
	if _, err := ParseMonth("2026-13"); err == nil {
		t.Error("expected error for month 13")
	}
}

func TestActive(t *testing.T) {
	now := Month{2026, 6}
	cases := []struct {
		start, end Month
		want       bool
	}{
		{Month{}, Month{}, true},
		{Month{2026, 7}, Month{}, false},
		{Month{2026, 6}, Month{2026, 6}, true},
		{Month{}, Month{2026, 5}, false},
	}
	for _, c := range cases {
		if got := Active(c.start, c.end, now); got != c.want {
			t.Errorf("Active(%v, %v) = %v", c.start, c.end, got)
		}
	}
}

func TestPersonValidate(t *testing.T) {
	now := Month{2026, 11}
	ok := Person{Name: "Ana", Birth: Month{1990, 4}, LifeExpectancy: 90}
	if errs := ok.Validate(now); len(errs) != 0 {
		t.Errorf("valid person has errors: %v", errs)
	}
	if got := ok.AgeAt(now); got != 36 {
		t.Errorf("AgeAt = %d, want 36", got)
	}
	bad := Person{Name: " ", Birth: Month{2030, 1}, LifeExpectancy: 30}
	errs := bad.Validate(now)
	for _, f := range []string{"name", "birth", "life_expectancy"} {
		if _, ok := errs[f]; !ok {
			t.Errorf("expected error on %s, got %v", f, errs)
		}
	}
}

func TestIncomeValidateAndAverage(t *testing.T) {
	inc := Income{Name: "Nómina", Kind: IncomeSalary, Amount: 200000, PaymentsPerYear: 14}
	if errs := inc.Validate(); len(errs) != 0 {
		t.Fatalf("valid income has errors: %v", errs)
	}
	// 2.000 € × 14 pagas = 28.000 €/year = 2.333,33 €/month.
	if got := inc.MonthlyAverage(); got != 233333 {
		t.Errorf("MonthlyAverage = %d", got)
	}
	inc.PaymentsPerYear = 13
	inc.Start, inc.End = Month{2027, 1}, Month{2026, 1}
	errs := inc.Validate()
	if errs["payments"] == "" || errs["end"] != ErrEndBeforeStr {
		t.Errorf("expected payments and end errors, got %v", errs)
	}
}

func TestDebtPayoff(t *testing.T) {
	// 150.000 € at 3 % TIN over 25 years → cuota ≈ 711,32 €.
	d := Debt{Name: "Hipoteca", Kind: DebtMortgage, Balance: 15000000, AnnualRate: 0.03, MonthlyPayment: 71132}
	if n := d.MonthsToPayoff(); n != 300 {
		t.Errorf("MonthsToPayoff = %d, want 300", n)
	}
	if got := d.PayoffMonth(Month{2026, 11}); got != (Month{2051, 10}) {
		t.Errorf("PayoffMonth = %v, want 2051-10", got)
	}
	zero := Debt{Balance: 120000, MonthlyPayment: 10000}
	if n := zero.MonthsToPayoff(); n != 12 {
		t.Errorf("zero-rate MonthsToPayoff = %d, want 12", n)
	}
	never := Debt{Balance: 1000000, AnnualRate: 0.24, MonthlyPayment: 10000}
	if n := never.MonthsToPayoff(); n != -1 {
		t.Errorf("interest-only MonthsToPayoff = %d, want -1", n)
	}
	if got := never.PayoffMonth(Month{2026, 1}); !got.IsZero() {
		t.Errorf("never PayoffMonth = %v, want zero", got)
	}
}

func TestAccountValidate(t *testing.T) {
	a := Account{Name: "Indexado", Kind: AccountInvestment, Balance: 100, AnnualReturn: ptr(0.06)}
	if errs := a.Validate(); len(errs) != 0 {
		t.Errorf("valid account has errors: %v", errs)
	}
	a.Kind, a.Balance, a.AnnualReturn = "crypto", -1, ptr(0.9)
	errs := a.Validate()
	for _, f := range []string{"kind", "balance", "return"} {
		if _, ok := errs[f]; !ok {
			t.Errorf("expected error on %s, got %v", f, errs)
		}
	}
}

func TestSummarize(t *testing.T) {
	now := Month{2026, 11}
	s := Summarize(now, Resolved{},
		[]Account{{Balance: 5000000, MonthlyContribution: 30000}, {Balance: 1000000}},
		[]Income{
			{Amount: 200000, PaymentsPerYear: 14},                       // 2.333,33 €/month
			{Amount: 50000, PaymentsPerYear: 12, Start: Month{2027, 1}}, // not started
		},
		[]Expense{
			{Amount: 120000, PaymentsPerYear: 12},
			{Amount: 60000, PaymentsPerYear: 1}, // 500 €/month
		},
		[]Debt{{Balance: 15000000, MonthlyPayment: 71132}},
	)
	if s.Assets != 6000000 || s.Debts != 15000000 || s.NetWorth != -9000000 {
		t.Errorf("balances = %+v", s)
	}
	if s.MonthlyIncome != 233333 || s.MonthlyExpenses != 125000 {
		t.Errorf("flows = %+v", s)
	}
	want := Cents(233333 - 125000 - 71132 - 30000)
	if s.MonthlyUnallocated != want {
		t.Errorf("unallocated = %d, want %d", s.MonthlyUnallocated, want)
	}
}
