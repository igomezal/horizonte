package engine

import (
	"math"
	"testing"

	"github.com/igomezal/horizonte/internal/domain"
	"github.com/igomezal/horizonte/internal/history"
)

// synthetic builds a small history: constant monthly stock/bond/inflation.
func synthetic(startYear, months int, stock, bond, infl float64) *history.Series {
	s := &history.Series{Start: domain.Month{Year: startYear, Month: 1}}
	for i := 0; i < months; i++ {
		s.Stock = append(s.Stock, stock)
		s.Bond = append(s.Bond, bond)
		s.Inflation = append(s.Inflation, infl)
	}
	return s
}

func TestBacktestUsesHistoryThenExpected(t *testing.T) {
	s := synthetic(1900, 24, 0.01, 0.002, 0.005)
	a := flat()
	a.Returns[domain.AccountInvestment] = 0.06
	in := Input{
		People: []domain.Person{personFor(36)},
		Accounts: []domain.Account{
			{ID: 1, Kind: domain.AccountInvestment, Balance: 1000000},
			{ID: 2, Kind: domain.AccountPension, Balance: 1000000},
			{ID: 3, Kind: domain.AccountCash, Balance: 1000000},
		},
		Expenses:    []domain.Expense{{Amount: 10000, PaymentsPerYear: 12}},
		Assumptions: a,
		Start:       start,
	}
	res, covered := Backtest(in, s, domain.Month{Year: 1900, Month: 1})
	if covered != 24 {
		t.Fatalf("covered = %d, want 24", covered)
	}
	// Month 0: investment +1 %, pension 0,6·1 % + 0,4·0,2 % = 0,68 %, cash 0.
	b := res.Months[0].Balances
	if !near(b[0], 10100, 1e-6) || !near(b[1], 10068, 1e-6) {
		t.Errorf("first-month balances = %v", b)
	}
	// Expenses follow historical inflation: after 12 months, 100 × 1,005^12.
	if got, want := res.Months[12].Expenses, 100*math.Pow(1.005, 12); !near(got, want, 1e-9) {
		t.Errorf("year-2 expense = %v, want %v", got, want)
	}
	// After the data ends (month 24), the investment earns its expected 6 % a year.
	before := res.Months[24].Balances[0]
	after := res.Months[25].Balances[0]
	if got := after/before - 1; !near(got, monthlyRate(0.06), 1e-12) {
		t.Errorf("post-data monthly return = %v", got)
	}
}

func TestBacktestCrashCausesFailure(t *testing.T) {
	// Stocks lose 5 % a month for two years: a plan living off stocks fails early.
	s := synthetic(1929, 600, -0.05, 0, 0)
	in := Input{
		People:      []domain.Person{personFor(120)},
		Accounts:    []domain.Account{{ID: 1, Kind: domain.AccountInvestment, Balance: 5000000}},
		Expenses:    []domain.Expense{{Amount: 100000, PaymentsPerYear: 12}},
		Assumptions: flat(),
		Start:       start,
	}
	if res, _ := Backtest(in, s, domain.Month{Year: 1929, Month: 1}); !res.Failed() {
		t.Error("a sustained crash should make the plan fail")
	}
}

func TestRollingUsesYearsThatFit(t *testing.T) {
	s := synthetic(1900, 12*30, 0.005, 0.002, 0.002)
	in := Input{
		People:      []domain.Person{personFor(120)}, // 10-year plan
		Accounts:    []domain.Account{{ID: 1, Kind: domain.AccountInvestment, Balance: 1000000}},
		Assumptions: flat(),
		Start:       start,
	}
	starts := Rolling(in, s)
	// 30 years of data, 10-year plan: every year 1900..1929, of which
	// 1900..1920 are fully historical and 1921..1929 partial.
	if len(starts) != 30 || starts[0].Year != 1900 || starts[29].Year != 1929 {
		t.Fatalf("rolling = %d starts, first %d", len(starts), starts[0].Year)
	}
	for _, st := range starts {
		wantPartial := st.Year > 1920
		if st.Partial != wantPartial {
			t.Errorf("%d: partial = %v, want %v", st.Year, st.Partial, wantPartial)
		}
	}
	if starts[20].Covered != 120 || starts[29].Covered != 12 {
		t.Errorf("covered months: 1920 %d, 1929 %d", starts[20].Covered, starts[29].Covered)
	}
	if starts[0].Failed || starts[0].EndReal <= starts[0].MinReal {
		t.Errorf("growing plan: %+v", starts[0])
	}
}

func TestRealDataCrises(t *testing.T) {
	s, err := history.Load()
	if err != nil {
		t.Fatal(err)
	}
	// 600.000 € all in stocks, spending 30.000 €/year from 2026 for 30 years.
	in := Input{
		People:      []domain.Person{personFor(360)},
		Accounts:    []domain.Account{{ID: 1, Kind: domain.AccountInvestment, Balance: 60000000}},
		Expenses:    []domain.Expense{{Amount: 250000, PaymentsPerYear: 12}},
		Assumptions: DefaultAssumptions(),
		Start:       start,
	}
	dep, _ := Backtest(in, s, domain.Month{Year: 1929, Month: 9})
	boom, _ := Backtest(in, s, domain.Month{Year: 1982, Month: 8})
	minDep, minBoom := math.Inf(1), math.Inf(1)
	for k := range dep.Months[:48] {
		minDep = math.Min(minDep, dep.Months[k].NetWorth/dep.Months[k].Deflator)
		minBoom = math.Min(minBoom, boom.Months[k].NetWorth/boom.Months[k].Deflator)
	}
	if minDep > 0.6*600000 {
		t.Errorf("1929 should cut net worth hard in 4 years: lowest %.0f", minDep)
	}
	if minDep >= minBoom {
		t.Errorf("1929 start (%.0f) should be worse than 1982 (%.0f)", minDep, minBoom)
	}
	for _, c := range Crises {
		if s.Index(c.Start) < 0 {
			t.Errorf("crisis %s starts outside the data", c.Key)
		}
	}
}
