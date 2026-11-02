package engine

import (
	"context"
	"math"
	"math/rand/v2"
	"runtime"
	"slices"
	"sort"
	"sync"

	"github.com/igomezal/horizonte/internal/domain"
)

// MCRunsOptions are the scenario counts offered in the UI.
var MCRunsOptions = []int{250, 500, 1000, 2000}

// DefaultVolatility is a typical long-run annual volatility per account kind.
func DefaultVolatility() map[domain.AccountKind]float64 {
	return map[domain.AccountKind]float64{
		domain.AccountCash:       0,
		domain.AccountDeposit:    0.01,
		domain.AccountInvestment: 0.16,
		domain.AccountPension:    0.10,
		domain.AccountRealEstate: 0.08,
		domain.AccountOther:      0.05,
	}
}

// MCSettings configures a Monte Carlo run.
type MCSettings struct {
	Runs int
	Seed uint64
	// Correlation of every account's returns with a common market factor
	// (0 = independent, 1 = all accounts move together).
	Correlation float64
}

// Percentiles holds the 10th, 25th, 50th, 75th and 90th percentiles.
type Percentiles [5]float64

// MCMilestone summarises when a conditional milestone is reached across runs.
type MCMilestone struct {
	Milestone domain.Milestone
	Reached   int // runs in which it happens within the plan
	// Best, Median and Worst are the 10th, 50th and 90th percentile months
	// among the runs that reach it (zero when none do).
	Best, Median, Worst domain.Month
}

// MCBadCase describes the 10th-percentile scenario (ranked by when the money
// runs out, then by final net worth).
type MCBadCase struct {
	Failed    bool
	FailMonth domain.Month
	EndReal   float64 // final net worth in today's euros
}

// MCResult aggregates many simulations of the same plan.
type MCResult struct {
	Runs      int
	Successes int // runs where the money lasts to the end
	Years     []int
	YearEnd   []domain.Month // last projected month of each year
	NetWorth  []Percentiles  // per year, today's euros, at the year's last month
	Survival  []float64      // per year: share of runs that still have money at year end
	Milestone []MCMilestone  // conditional milestones only
	Bad       MCBadCase
}

// SuccessRate is the share of runs where the money lasts.
func (r MCResult) SuccessRate() float64 {
	if r.Runs == 0 {
		return 0
	}
	return float64(r.Successes) / float64(r.Runs)
}

type runSummary struct {
	failed    bool
	failIdx   int // month index of the first shortfall
	endReal   float64
	yearReal  []float64
	failMonth domain.Month
	hits      []domain.Month // per conditional milestone (zero = not reached)
}

// MonteCarlo runs the plan s.Runs times with random monthly returns.
// Monthly log returns are normal with mean ln(1+μ)/12 and standard deviation
// σ/√12, where μ is the account's expected return and σ its volatility, so the
// median scenario follows the deterministic projection. Inflation stays fixed.
func MonteCarlo(in Input, vol map[domain.AccountKind]float64, s MCSettings) MCResult {
	res, _ := MonteCarloContext(context.Background(), in, vol, s)
	return res
}

// MonteCarloContext is MonteCarlo that stops early when ctx is done (the
// person left the page), returning ctx's error and an empty result.
func MonteCarloContext(ctx context.Context, in Input, vol map[domain.AccountKind]float64, s MCSettings) (MCResult, error) {
	in.Detail = false
	end := in.EndMonth()
	if end.IsZero() || end.Before(in.Start) || s.Runs <= 0 {
		return MCResult{}, nil
	}
	n := in.Start.MonthsUntil(end) + 1

	// Per-account monthly drift and volatility.
	drift := make([]float64, len(in.Accounts))
	sigma := make([]float64, len(in.Accounts))
	for i, a := range in.Accounts {
		drift[i] = math.Log(1+in.Assumptions.AnnualReturn(a)) / 12
		sigma[i] = vol[a.Kind] / math.Sqrt(12)
	}
	common := math.Sqrt(math.Max(0, math.Min(1, s.Correlation)))
	own := math.Sqrt(1 - common*common)

	var conditional []int
	for k, m := range in.Milestones {
		if m.Conditional() {
			conditional = append(conditional, k)
		}
	}

	// Year layout (same for every run).
	var years []int
	var yearEnd []domain.Month
	var yearLastIdx []int
	for t := range n {
		m := in.Start.AddMonths(t)
		if len(years) == 0 || years[len(years)-1] != m.Year {
			years = append(years, m.Year)
			yearEnd = append(yearEnd, m)
			yearLastIdx = append(yearLastIdx, t)
		}
		yearEnd[len(yearEnd)-1] = m
		yearLastIdx[len(yearLastIdx)-1] = t
	}

	runOne := func(run int) runSummary {
		rng := rand.New(rand.NewPCG(s.Seed, uint64(run)))
		rates := make([]float64, len(in.Accounts))
		lastT := -1
		rate := func(t, i int) float64 {
			if t != lastT {
				z := rng.NormFloat64()
				for j := range rates {
					if sigma[j] == 0 {
						rates[j] = math.Exp(drift[j]) - 1
						continue
					}
					shock := common*z + own*rng.NormFloat64()
					rates[j] = math.Exp(drift[j]+sigma[j]*shock) - 1
				}
				lastT = t
			}
			return rates[i]
		}
		res := simulate(in, rate)
		sum := runSummary{yearReal: make([]float64, len(years)), hits: make([]domain.Month, len(conditional))}
		for y, t := range yearLastIdx {
			row := res.Months[t]
			sum.yearReal[y] = row.NetWorth / row.Deflator
		}
		last := res.Months[len(res.Months)-1]
		sum.endReal = last.NetWorth / last.Deflator
		if res.Failed() {
			sum.failed = true
			sum.failMonth = res.FailMonth
			sum.failIdx = in.Start.MonthsUntil(res.FailMonth)
		}
		for c, k := range conditional {
			sum.hits[c] = res.Milestones[k].Month
		}
		return sum
	}

	// Run in parallel; each run has its own deterministic random stream.
	runs := make([]runSummary, s.Runs)
	workers := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	next := make(chan int)
	for range workers {
		wg.Go(func() {
			for r := range next {
				runs[r] = runOne(r)
			}
		})
	}
feed:
	for r := range s.Runs {
		select {
		case next <- r:
		case <-ctx.Done():
			break feed
		}
	}
	close(next)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return MCResult{}, err
	}

	out := MCResult{Runs: s.Runs, Years: years, YearEnd: yearEnd}
	for _, r := range runs {
		if !r.failed {
			out.Successes++
		}
	}

	col := make([]float64, s.Runs)
	for y := range years {
		alive := 0
		for r := range runs {
			col[r] = runs[r].yearReal[y]
			if !runs[r].failed || runs[r].failIdx > yearLastIdx[y] {
				alive++
			}
		}
		out.NetWorth = append(out.NetWorth, percentiles(col))
		out.Survival = append(out.Survival, float64(alive)/float64(s.Runs))
	}

	for c, k := range conditional {
		mm := MCMilestone{Milestone: in.Milestones[k]}
		var idx []int
		for _, r := range runs {
			if h := r.hits[c]; !h.IsZero() {
				idx = append(idx, in.Start.MonthsUntil(h))
			}
		}
		mm.Reached = len(idx)
		if len(idx) > 0 {
			slices.Sort(idx)
			at := func(p float64) domain.Month { return in.Start.AddMonths(idx[int(p*float64(len(idx)-1))]) }
			mm.Best, mm.Median, mm.Worst = at(0.1), at(0.5), at(0.9)
		}
		out.Milestone = append(out.Milestone, mm)
	}

	// The 10th-percentile scenario: earliest failures first, then lowest end value.
	order := make([]int, s.Runs)
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool {
		ra, rb := runs[order[a]], runs[order[b]]
		if ra.failed != rb.failed {
			return ra.failed
		}
		if ra.failed {
			return ra.failIdx < rb.failIdx
		}
		return ra.endReal < rb.endReal
	})
	bad := runs[order[int(0.1*float64(s.Runs-1))]]
	out.Bad = MCBadCase{Failed: bad.failed, FailMonth: bad.failMonth, EndReal: bad.endReal}
	return out, nil
}

// percentiles returns the 10/25/50/75/90th percentiles of xs (xs is reordered).
func percentiles(xs []float64) Percentiles {
	slices.Sort(xs)
	at := func(p float64) float64 {
		pos := p * float64(len(xs)-1)
		lo := int(pos)
		if lo+1 >= len(xs) {
			return xs[lo]
		}
		frac := pos - float64(lo)
		return xs[lo]*(1-frac) + xs[lo+1]*frac
	}
	return Percentiles{at(0.1), at(0.25), at(0.5), at(0.75), at(0.9)}
}
