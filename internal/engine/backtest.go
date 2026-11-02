package engine

import (
	"context"
	"math"
	"runtime"
	"sync"

	"github.com/igomezal/horizonte/internal/domain"
	"github.com/igomezal/horizonte/internal/history"
)

// PensionStockShare is the stock weight used for pension plans in backtests
// (the rest follows bonds), a typical mixed Spanish pension plan.
const PensionStockShare = 0.6

// Crisis is a named historical period to replay the plan through.
type Crisis struct {
	Key   string
	Start domain.Month
}

// Crises start at the market peak before each crash.
var Crises = []Crisis{
	{"depression", domain.Month{Year: 1929, Month: 9}},
	{"stagflation", domain.Month{Year: 1973, Month: 1}},
	{"dotcom", domain.Month{Year: 2000, Month: 3}},
	{"gfc", domain.Month{Year: 2007, Month: 10}},
	{"covid", domain.Month{Year: 2020, Month: 2}},
}

// FindCrisis returns the crisis with key.
func FindCrisis(key string) (Crisis, bool) {
	for _, c := range Crises {
		if c.Key == key {
			return c, true
		}
	}
	return Crisis{}, false
}

// Backtest replays the plan as if the markets of history started in month
// from: investment accounts follow US stocks, pension plans a 60/40 mix of
// stocks and bonds, deposits, property and other accounts keep their expected
// real return on top of historical inflation, and cash earns nothing.
// Expenses and income that follow inflation use historical CPI. Once the data
// ends, the plan continues with the expected returns and inflation.
//
// It returns the projection and how many months were covered by history.
func Backtest(in Input, s *history.Series, from domain.Month) (Result, int) {
	start := s.Index(from)
	end := in.EndMonth()
	if start < 0 || end.IsZero() || end.Before(in.Start) {
		return Result{}, 0
	}
	n := in.Start.MonthsUntil(end) + 1
	covered := min(s.Len()-start, n)

	a := in.Assumptions
	in.InflationPath = make([]float64, covered)
	copy(in.InflationPath, s.Inflation[start:start+covered])

	expected := make([]float64, len(in.Accounts)) // monthly, used after the data ends
	realMonthly := make([]float64, len(in.Accounts))
	for i, acc := range in.Accounts {
		mu := a.AnnualReturn(acc)
		expected[i] = monthlyRate(mu)
		realMonthly[i] = monthlyRate((1+mu)/(1+a.Inflation) - 1)
	}
	rate := func(t, i int) float64 {
		if t >= covered {
			return expected[i]
		}
		k := start + t
		switch in.Accounts[i].Kind {
		case domain.AccountCash:
			return 0
		case domain.AccountInvestment:
			return s.Stock[k]
		case domain.AccountPension:
			return PensionStockShare*s.Stock[k] + (1-PensionStockShare)*s.Bond[k]
		default: // deposits, property, other: expected real return + historical inflation
			return (1+realMonthly[i])*(1+s.Inflation[k]) - 1
		}
	}
	in.Detail = false
	return simulate(in, rate), covered
}

// RollingStart is the outcome of starting the plan in one historical year.
type RollingStart struct {
	Year int
	// Covered is how many months of the plan used historical data; Partial
	// starts ran out of data and continued with the expected returns.
	Covered   int
	Partial   bool
	Failed    bool
	FailMonth domain.Month // in plan time
	EndReal   float64      // final net worth, today's euros
	MinReal   float64      // lowest net worth along the way, today's euros
}

// Rolling runs the plan starting in January of every year in the data. Starts
// whose whole plan fits inside the data are fully historical; later starts are
// Partial: once the data ends they continue with the expected returns.
func Rolling(in Input, s *history.Series) []RollingStart {
	starts, _ := RollingContext(context.Background(), in, s)
	return starts
}

// RollingContext is Rolling that stops early when ctx is done, returning
// ctx's error and no starts.
func RollingContext(ctx context.Context, in Input, s *history.Series) ([]RollingStart, error) {
	end := in.EndMonth()
	if end.IsZero() || end.Before(in.Start) {
		return nil, nil
	}
	var years []int
	for y := s.Start.Year; y <= s.End().Year; y++ {
		if s.Index(domain.Month{Year: y, Month: 1}) >= 0 {
			years = append(years, y)
		}
	}

	starts := make([]RollingStart, len(years))
	var wg sync.WaitGroup
	next := make(chan int)
	for range runtime.GOMAXPROCS(0) {
		wg.Go(func() {
			for k := range next {
				res, covered := Backtest(in, s, domain.Month{Year: years[k], Month: 1})
				starts[k] = summarizeStart(years[k], res)
				starts[k].Covered = covered
				starts[k].Partial = covered < len(res.Months)
			}
		})
	}
feed:
	for k := range years {
		select {
		case next <- k:
		case <-ctx.Done():
			break feed
		}
	}
	close(next)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return starts, nil
}

func summarizeStart(year int, res Result) RollingStart {
	rs := RollingStart{Year: year, Failed: res.Failed(), FailMonth: res.FailMonth, MinReal: math.Inf(1)}
	for _, m := range res.Months {
		if v := m.NetWorth / m.Deflator; v < rs.MinReal {
			rs.MinReal = v
		}
	}
	if len(res.Months) > 0 {
		last := res.Months[len(res.Months)-1]
		rs.EndReal = last.NetWorth / last.Deflator
	}
	return rs
}
