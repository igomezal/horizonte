package engine

import (
	"errors"
	"math"
	"math/rand/v2"

	"github.com/igomezal/horizonte/internal/domain"
	"github.com/igomezal/horizonte/internal/history"
)

// Monte Carlo models, stored per plan in Assumptions.MCModel.
const (
	// MCParametric draws independent normal monthly log returns around the
	// expected returns, with fixed inflation.
	MCParametric = "parametric"
	// MCBootstrap resamples blocks of history, inflation included.
	MCBootstrap = "bootstrap"
)

// MCModels are the models offered in the UI.
var MCModels = []string{MCParametric, MCBootstrap}

// MCBlockMonths is the length of each block of history MCBootstrap samples.
// Long enough to keep the runs of good and bad months that make sequences
// dangerous, short enough that every run is a different mix of history.
const MCBlockMonths = 12

// ErrShortHistory means the series cannot hold a single bootstrap block.
var ErrShortHistory = errors.New("engine: history shorter than a bootstrap block")

// bootstrapSampler builds the scenarios of MCBootstrap. It is read-only after
// newBootstrapSampler, so runs can share it across goroutines.
//
// History alone would ignore what the person expects, so every series is
// recentred: its monthly log returns are shifted by a constant, which keeps
// their shape (volatility, crashes, autocorrelation) and moves their long-run
// mean to the plan's. Multiplying (1+r) by exp(shift) is that same shift.
// Targets are ln(1+μ)/12 and ln(1+inflation)/12, the geometric means, so as in
// the parametric model the expected return is the median outcome and the
// median scenario follows the deterministic projection.
type bootstrapSampler struct {
	series *history.Series
	kinds  []domain.AccountKind
	// scale multiplies (1+r) of the account's market series (stocks, or the
	// pension mix). Each account has its own: its expected return can differ.
	scale []float64
	// realMonthly is the expected real return of deposits, property and other.
	realMonthly []float64
	inflScale   float64
}

func newBootstrapSampler(in Input, s *history.Series) (*bootstrapSampler, error) {
	if s == nil || s.Len() < MCBlockMonths {
		return nil, ErrShortHistory
	}
	a := in.Assumptions
	stockMean := blockMeanLog(s.Len(), func(k int) float64 { return s.Stock[k] })
	// Pension plans are shifted as a mix: the plan has one expected return
	// for them, not one each for stocks and bonds.
	pensionMean := blockMeanLog(s.Len(), func(k int) float64 { return pensionReturn(s, k) })
	inflMean := blockMeanLog(s.Len(), func(k int) float64 { return s.Inflation[k] })

	b := &bootstrapSampler{
		series:      s,
		kinds:       make([]domain.AccountKind, len(in.Accounts)),
		scale:       make([]float64, len(in.Accounts)),
		realMonthly: make([]float64, len(in.Accounts)),
		inflScale:   math.Exp(math.Log(1+a.Inflation)/12 - inflMean),
	}
	for i, acc := range in.Accounts {
		mu := a.AnnualReturn(acc)
		b.kinds[i] = acc.Kind
		b.realMonthly[i] = monthlyRate((1+mu)/(1+a.Inflation) - 1)
		switch acc.Kind {
		case domain.AccountInvestment:
			b.scale[i] = math.Exp(math.Log(1+mu)/12 - stockMean)
		case domain.AccountPension:
			b.scale[i] = math.Exp(math.Log(1+mu)/12 - pensionMean)
		}
	}
	return b, nil
}

// pensionReturn is the return of a pension plan in month k of the series.
func pensionReturn(s *history.Series, k int) float64 {
	return PensionStockShare*s.Stock[k] + (1-PensionStockShare)*s.Bond[k]
}

// blockMeanLog is the mean monthly log return the sampler will produce. Months
// near the ends of the data fall in fewer blocks, so a plain average of the
// series would leave the scenarios slightly off the plan's assumptions.
func blockMeanLog(length int, ret func(k int) float64) float64 {
	cum := make([]float64, length+1)
	for k := range length {
		cum[k+1] = cum[k] + math.Log(1+ret(k))
	}
	starts := length - MCBlockMonths + 1
	var sum float64
	for st := range starts {
		sum += cum[st+MCBlockMonths] - cum[st]
	}
	return sum / float64(starts*MCBlockMonths)
}

// sampleMonths returns n positions in a series of the given length, made of
// contiguous blocks of MCBlockMonths (the last one cut to fit). Blocks never
// wrap around the end of the data, which would join unrelated months.
func sampleMonths(rng *rand.Rand, length, n int) []int {
	months := make([]int, 0, n)
	for len(months) < n {
		start := rng.IntN(length - MCBlockMonths + 1)
		for k := range min(MCBlockMonths, n-len(months)) {
			months = append(months, start+k)
		}
	}
	return months
}

// scenario samples n months of history and returns their recentred inflation
// and the account returns, mapped as in Backtest. Stocks, bonds and inflation
// all read the same months, which preserves the relationship between them.
func (b *bootstrapSampler) scenario(rng *rand.Rand, n int) ([]float64, func(t, i int) float64) {
	s := b.series
	months := sampleMonths(rng, s.Len(), n)
	inflation := make([]float64, n)
	for t, k := range months {
		inflation[t] = (1+s.Inflation[k])*b.inflScale - 1
	}
	return inflation, func(t, i int) float64 {
		k := months[t]
		switch b.kinds[i] {
		case domain.AccountCash:
			return 0
		case domain.AccountInvestment:
			return (1+s.Stock[k])*b.scale[i] - 1
		case domain.AccountPension:
			return (1+pensionReturn(s, k))*b.scale[i] - 1
		default: // deposits, property, other: expected real return + sampled inflation
			return (1+b.realMonthly[i])*(1+inflation[t]) - 1
		}
	}
}
