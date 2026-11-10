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
		nil,
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

func TestSummarizePrepayments(t *testing.T) {
	now := Month{2026, 11}
	ms := int64(7)
	r := Resolved{ms: Month{2027, 1}, 9: Month{2026, 9}, 10: Month{2026, 11}}
	other := int64(8) // never reached
	debts := []Debt{{ID: 1, Balance: 10000000}, {ID: 2, Balance: 100000}}
	reachedEnd, reachedStart := int64(9), int64(10)
	income := []Income{{Amount: 500000, PaymentsPerYear: 12}}
	cases := []struct {
		name string
		p    Prepayment
		want Cents
	}{
		{"monthly", Prepayment{DebtID: 1, Amount: 20000, Frequency: PrepayMonthly}, 20000},
		{"monthly before span", Prepayment{DebtID: 1, Amount: 20000, Frequency: PrepayMonthly, Start: Month{2027, 1}}, 0},
		{"monthly after span", Prepayment{DebtID: 1, Amount: 20000, Frequency: PrepayMonthly, End: Month{2026, 10}}, 0},
		{"yearly spread", Prepayment{DebtID: 1, Amount: 120000, Frequency: PrepayYearly, Start: Month{2026, 3}}, 10000},
		{"yearly outside span", Prepayment{DebtID: 1, Amount: 120000, Frequency: PrepayYearly, Start: Month{2025, 1}, End: Month{2026, 6}}, 0},
		{"once this month", Prepayment{DebtID: 1, Amount: 70000, Frequency: PrepayOnce, Start: now}, 70000},
		{"once other month", Prepayment{DebtID: 1, Amount: 70000, Frequency: PrepayOnce, Start: Month{2026, 12}}, 0},
		{"start milestone not reached", Prepayment{DebtID: 1, Amount: 20000, Frequency: PrepayMonthly, StartMS: &other}, 0},
		{"start milestone later", Prepayment{DebtID: 1, Amount: 20000, Frequency: PrepayMonthly, StartMS: &ms}, 0},
		{"end milestone later", Prepayment{DebtID: 1, Amount: 20000, Frequency: PrepayMonthly, EndMS: &ms}, 20000},
		{"once with only a start milestone", Prepayment{DebtID: 1, Amount: 70000, Frequency: PrepayOnce, StartMS: &reachedStart}, 70000},
		{"end milestone already reached", Prepayment{DebtID: 1, Amount: 20000, Frequency: PrepayMonthly, EndMS: &reachedEnd}, 0},
		{"yearly with no start", Prepayment{DebtID: 1, Amount: 120000, Frequency: PrepayYearly}, 10000},
		{"monthly with fee", Prepayment{DebtID: 1, Amount: 20000, Frequency: PrepayMonthly, FeeRate: 0.005}, 20100},
		{"once with fee", Prepayment{DebtID: 1, Amount: 300000, Frequency: PrepayOnce, Start: now, FeeRate: 0.005}, 301500},
		{"once capped at balance, fee on capped amount", Prepayment{DebtID: 2, Amount: 900000, Frequency: PrepayOnce, Start: now, FeeRate: 0.01}, 101000},
		{"yearly capped before /12", Prepayment{DebtID: 2, Amount: 1200000, Frequency: PrepayYearly}, 8333},
		{"deleted debt", Prepayment{DebtID: 99, Amount: 20000, Frequency: PrepayMonthly}, 0},
	}
	for _, c := range cases {
		s := Summarize(now, r, nil, income, nil, debts, []Prepayment{c.p})
		if s.MonthlyPrepayments != c.want {
			t.Errorf("%s: prepayments = %d, want %d", c.name, s.MonthlyPrepayments, c.want)
		}
		if s.MonthlyUnallocated != 500000-c.want {
			t.Errorf("%s: unallocated = %d", c.name, s.MonthlyUnallocated)
		}
		if s.NetWorth != -10100000 {
			t.Errorf("%s: net worth changed", c.name)
		}
	}
}

func TestSummarizePrepaymentsShareDebtBalance(t *testing.T) {
	now := Month{2026, 11}
	// Two one-offs of 1.500 on a 1.800 card: the second only gets what is left.
	s := Summarize(now, Resolved{}, nil, nil, nil, []Debt{{ID: 1, Balance: 180000}}, []Prepayment{
		{DebtID: 1, Amount: 150000, Frequency: PrepayOnce, Start: now},
		{DebtID: 1, Amount: 150000, Frequency: PrepayOnce, Start: now},
	})
	if s.MonthlyPrepayments != 180000 {
		t.Errorf("prepayments = %d, want 180000", s.MonthlyPrepayments)
	}
}
