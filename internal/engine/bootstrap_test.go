package engine

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/igomezal/horizonte/internal/domain"
	"github.com/igomezal/horizonte/internal/history"
)

func bootstrapInput() Input {
	in := mcInput()
	in.Assumptions.MCModel = MCBootstrap
	return in
}

// The current model's results, recorded before the bootstrap model existed.
func TestParametricOutputIsUnchanged(t *testing.T) {
	near := func(got, want float64) bool { return math.Abs(got-want) < 0.01 }
	for _, model := range []string{"", MCParametric} {
		in := mcInput()
		in.Assumptions.MCModel = model
		r := MonteCarlo(in, DefaultVolatility(), MCSettings{Runs: 400, Seed: 7, Correlation: 0.6})
		p := r.NetWorth[10]
		if r.Successes != 230 || !near(p[0], 182774.24) || !near(p[2], 394416.14) || !near(p[4], 987812.72) || !r.Bad.Failed {
			t.Errorf("model %q: successes %d, year 10 %v, bad %+v", model, r.Successes, p, r.Bad)
		}
	}
	if got := DefaultAssumptions().MCModel; got != MCParametric {
		t.Errorf("default model = %q", got)
	}
}

func TestBootstrapIsReproducible(t *testing.T) {
	in := bootstrapInput()
	s := MCSettings{Runs: 300, Seed: 7, Correlation: 0.6}
	r1 := MonteCarlo(in, DefaultVolatility(), s)
	r2 := MonteCarlo(in, DefaultVolatility(), s)
	if r1.Runs != 300 || r1.Successes != r2.Successes || r1.NetWorth[10] != r2.NetWorth[10] || r1.Bad != r2.Bad {
		t.Fatal("same seed must give the same result")
	}
	s.Seed = 8
	if r3 := MonteCarlo(in, DefaultVolatility(), s); r3.NetWorth[10] == r1.NetWorth[10] {
		t.Error("a new seed should change the scenarios")
	}
	if par := MonteCarlo(mcInput(), DefaultVolatility(), MCSettings{Runs: 300, Seed: 7, Correlation: 0.6}); par.NetWorth[10] == r1.NetWorth[10] {
		t.Error("the model should change the scenarios")
	}
}

func TestBootstrapBlocksAreContiguous(t *testing.T) {
	const length, n = 100, 12*7 + 5 // a cut final block
	rng := rand.New(rand.NewPCG(1, 2))
	for range 200 {
		months := sampleMonths(rng, length, n)
		if len(months) != n {
			t.Fatalf("len = %d, want %d", len(months), n)
		}
		for t2, k := range months {
			if k < 0 || k >= length {
				t.Fatalf("month %d out of the data", k)
			}
			if t2%MCBlockMonths != 0 && k != months[t2-1]+1 {
				t.Fatalf("block broken at %d: %v", t2, months)
			}
		}
	}
}

func TestBootstrapBlocksReachBothEndsOfTheData(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	seen := map[int]bool{}
	for range 2000 {
		for _, k := range sampleMonths(rng, 40, 12) {
			seen[k] = true
		}
	}
	if len(seen) != 40 {
		t.Errorf("covered %d of 40 months", len(seen))
	}
}

// Each field encodes its month, so a scenario can be traced back to it.
func tracedSeries(n int) *history.Series {
	s := &history.Series{Start: domain.Month{Year: 2000, Month: 1}}
	for k := range n {
		x := float64(k) * 1e-5
		s.Stock = append(s.Stock, 0.01+x)
		s.Bond = append(s.Bond, 0.002+x/2)
		s.Inflation = append(s.Inflation, 0.001+x/4)
	}
	return s
}

func TestBootstrapUsesTheSameMonthsForEverySeries(t *testing.T) {
	in := bootstrapInput()
	in.Accounts = []domain.Account{
		{ID: 1, Kind: domain.AccountCash}, {ID: 2, Kind: domain.AccountInvestment},
		{ID: 3, Kind: domain.AccountPension}, {ID: 4, Kind: domain.AccountDeposit},
	}
	s := tracedSeries(240)
	b, err := newBootstrapSampler(in, s)
	if err != nil {
		t.Fatal(err)
	}
	inflation, rate := b.scenario(rand.New(rand.NewPCG(3, 4)), 120)
	for tt := range inflation {
		k := int(math.Round(((1+inflation[tt])/b.inflScale - 1 - 0.001) * 4 / 1e-5))
		if tt%MCBlockMonths != 0 {
			prev := int(math.Round(((1+inflation[tt-1])/b.inflScale - 1 - 0.001) * 4 / 1e-5))
			if k != prev+1 {
				t.Fatalf("inflation not contiguous at %d: %d after %d", tt, k, prev)
			}
		}
		const eps = 1e-9
		if got := rate(tt, 0); got != 0 {
			t.Errorf("cash earned %v", got)
		}
		if got, want := rate(tt, 1), (1+s.Stock[k])*b.scale[1]-1; math.Abs(got-want) > eps {
			t.Fatalf("stock at t=%d: %v, want month %d: %v", tt, got, k, want)
		}
		mix := PensionStockShare*s.Stock[k] + (1-PensionStockShare)*s.Bond[k]
		if got, want := rate(tt, 2), (1+mix)*b.scale[2]-1; math.Abs(got-want) > eps {
			t.Fatalf("pension at t=%d: %v, want month %d: %v", tt, got, k, want)
		}
		if got, want := rate(tt, 3), (1+b.realMonthly[3])*(1+inflation[tt])-1; math.Abs(got-want) > eps {
			t.Fatalf("deposit at t=%d: %v, want %v", tt, got, want)
		}
	}
}

func TestBootstrapMeansMatchThePlan(t *testing.T) {
	hs, err := history.Load()
	if err != nil {
		t.Fatal(err)
	}
	own := 0.09
	in := bootstrapInput()
	in.Assumptions.Inflation = 0.03
	in.Accounts = []domain.Account{
		{ID: 1, Kind: domain.AccountCash},
		{ID: 2, Kind: domain.AccountInvestment},
		{ID: 3, Kind: domain.AccountInvestment, AnnualReturn: &own}, // its own assumption
		{ID: 4, Kind: domain.AccountPension},
		{ID: 5, Kind: domain.AccountDeposit},
		{ID: 6, Kind: domain.AccountRealEstate},
		{ID: 7, Kind: domain.AccountOther},
	}
	b, err := newBootstrapSampler(in, hs)
	if err != nil {
		t.Fatal(err)
	}

	const paths, months = 3000, 240
	rng := rand.New(rand.NewPCG(11, 12))
	sums := make([]float64, len(in.Accounts))
	var inflSum float64
	for range paths {
		inflation, rate := b.scenario(rng, months)
		for tt := range months {
			inflSum += math.Log(1 + inflation[tt])
			for i := range in.Accounts {
				sums[i] += math.Log(1 + rate(tt, i))
			}
		}
	}
	const tolerance = 0.0015 // annualised
	annual := func(sum float64) float64 { return math.Exp(sum*12/(paths*months)) - 1 }
	if got := annual(inflSum); math.Abs(got-0.03) > tolerance {
		t.Errorf("inflation mean = %.4f, want 0.03", got)
	}
	for i, acc := range in.Accounts {
		want := in.Assumptions.AnnualReturn(acc)
		if acc.Kind == domain.AccountCash {
			want = 0
		}
		if got := annual(sums[i]); math.Abs(got-want) > tolerance {
			t.Errorf("account %d (%s): mean return %.4f, want %.4f", acc.ID, acc.Kind, got, want)
		}
	}
}

func TestBootstrapNeedsEnoughHistory(t *testing.T) {
	in := bootstrapInput()
	_, err := MonteCarloContext(context.Background(), in, DefaultVolatility(),
		MCSettings{Runs: 5, Seed: 1, History: tracedSeries(MCBlockMonths - 1)})
	if !errors.Is(err, ErrShortHistory) {
		t.Errorf("err = %v, want ErrShortHistory", err)
	}
	// Exactly one block is enough.
	res, err := MonteCarloContext(context.Background(), in, DefaultVolatility(),
		MCSettings{Runs: 5, Seed: 1, History: tracedSeries(MCBlockMonths)})
	if err != nil || res.Runs != 5 {
		t.Errorf("one block: runs %d, err %v", res.Runs, err)
	}
}

func TestBootstrapStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	mc, err := MonteCarloContext(ctx, bootstrapInput(), DefaultVolatility(), MCSettings{Runs: 5000, Seed: 1})
	if !errors.Is(err, context.Canceled) || mc.Runs != 0 {
		t.Errorf("runs=%d err=%v, want no result and context.Canceled", mc.Runs, err)
	}
}

func TestBootstrapPanicIsRecovered(t *testing.T) {
	TestHook = func(point string, _ Input) {
		if point == "monte_carlo.item" {
			panic("boom")
		}
	}
	defer func() { TestHook = nil }()
	_, err := MonteCarloContext(context.Background(), bootstrapInput(), DefaultVolatility(), MCSettings{Runs: 50, Seed: 1})
	if !errors.Is(err, ErrPanic) {
		t.Errorf("err = %v, want ErrPanic", err)
	}
}
