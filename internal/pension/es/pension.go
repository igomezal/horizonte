// Package es estimates the Spanish contributory retirement pension
// (pensión de jubilación de la Seguridad Social) with the rules that apply
// from 2027 on. Amounts are in today's euros (2025 bases and limits): public
// pensions are revalued with inflation, so the estimate grows with it.
//
// It is an approximation for planning. It assumes the contribution base stays
// the same in real terms, that past contributions were continuous up to now,
// and it ignores special schemes, partial retirement and the transitional
// choice of the best 27 of the last 29 years.
package es

import "math"

// 2025 limits.
const (
	MinBase        = 1381.20 // monthly minimum contribution base (general scheme)
	MaxBase        = 4909.50 // monthly maximum contribution base
	MaxPension     = 3267.60 // monthly maximum pension, 14 payments
	MinPension     = 936.20  // monthly minimum pension at 65+, no dependent spouse, 14 payments
	MaxComplement  = 564.70  // top-up to the minimum is capped at the non-contributory pension
	Payments       = 14
	windowMonths   = 300 // contribution bases that make up the base reguladora (25 years)
	divisor        = 350
	minMonths      = 15 * 12
	fullOrdinary   = 462 // 38 years 6 months: ordinary age 65 instead of 67
	earlyMinMonths = 35 * 12
)

// Input describes a person's contributions. Ages are whole years; months
// count from the person's birth.
type Input struct {
	AgeMonths          int     // current age in months
	YearsContributed   float64 // contributed so far
	MonthlyBase        float64 // current monthly contribution base, today's euros
	ContributeUntilAge int     // age at which contributions stop
	RetireAge          int     // age at which the pension is claimed (0 = ordinary age)
	SelfEmployed       bool    // no filling of gaps with the minimum base
	TopUpToMinimum     bool    // apply the top-up to the minimum pension
}

// Result is the estimate.
type Result struct {
	Entitled        bool    // at least 15 years contributed
	MonthsAtRetire  int     // months contributed when the pension starts
	OrdinaryAge     int     // 65 or 67
	RetireAge       int     // age the pension starts
	EarlyNotAllowed bool    // retire age too early or not enough years: RetireAge moved to the ordinary age
	BaseReguladora  float64 // monthly
	Percent         float64 // share of the base by years contributed
	Adjustment      float64 // early reduction (negative) or late bonus (positive), as a ratio
	Capped          bool    // limited to the maximum pension
	ToppedUp        bool    // raised towards the minimum pension
	Monthly         float64 // per payment, 14 payments a year
	Annual          float64
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

// Percent is the share of the base reguladora for the months contributed.
func Percent(months int) float64 {
	if months < minMonths {
		return 0
	}
	extra := months - minMonths
	p := 0.5 + 0.0019*float64(min(extra, 248)) + 0.0018*float64(max(0, extra-248))
	return math.Min(p, 1)
}

// OrdinaryAge is 65 with 38 years and 6 months contributed, 67 otherwise.
func OrdinaryAge(months int) int {
	if months >= fullOrdinary {
		return 65
	}
	return 67
}

// earlyRate is the reduction for retiring 24 months early, by months
// contributed; shorter advances are reduced proportionally.
func earlyRate(months int) float64 {
	switch {
	case months < 462:
		return 0.21
	case months < 498:
		return 0.19
	case months < 534:
		return 0.17
	default:
		return 0.13
	}
}

// Estimate computes the pension.
func Estimate(in Input) Result {
	base := clamp(in.MonthlyBase, MinBase, MaxBase)
	past := int(math.Round(in.YearsContributed * 12))
	start := in.AgeMonths - past // contributions are taken as continuous up to now

	monthsAt := func(retire int) int {
		stop := min(in.ContributeUntilAge, retire) * 12
		return past + max(0, stop-in.AgeMonths)
	}

	// Ordinary age depends on the months contributed by then.
	ord := 67
	if monthsAt(65) >= fullOrdinary {
		ord = 65
	}
	r := Result{OrdinaryAge: ord, RetireAge: in.RetireAge}
	if r.RetireAge == 0 {
		r.RetireAge = ord
	}
	m := monthsAt(r.RetireAge)
	if r.RetireAge < ord && (r.RetireAge < ord-2 || m < earlyMinMonths) {
		r.EarlyNotAllowed, r.RetireAge = true, ord
		m = monthsAt(ord)
	}
	r.MonthsAtRetire = m
	if m < minMonths {
		return r
	}
	r.Entitled = true

	// Base reguladora: the last 300 months before retiring. Months without
	// contributions after the first one are filled (employees only).
	end := r.RetireAge * 12
	stop := min(in.ContributeUntilAge*12, end)
	var sum float64
	gap := 0
	for t := end - windowMonths; t < end; t++ {
		switch {
		case t >= start && t < stop:
			sum += base
		case t < start || in.SelfEmployed:
			// before the working life (or self-employed): nothing
		default:
			gap++
			switch {
			case gap <= 48:
				sum += MinBase
			case gap <= 60:
				sum += 0.8 * MinBase
			default:
				sum += 0.5 * MinBase
			}
		}
	}
	r.BaseReguladora = sum / divisor
	r.Percent = Percent(m)
	if early := ord*12 - end; early > 0 {
		r.Adjustment = -earlyRate(m) * float64(early) / 24
	} else if late := (end - ord*12) / 12; late > 0 {
		r.Adjustment = 0.04 * float64(late)
	}
	p := r.BaseReguladora * r.Percent * (1 + r.Adjustment)
	if p > MaxPension {
		p, r.Capped = MaxPension, true
	}
	if in.TopUpToMinimum && p < MinPension && r.RetireAge >= 65 {
		p, r.ToppedUp = p+math.Min(MinPension-p, MaxComplement), true
	}
	r.Monthly = math.Round(p*100) / 100
	r.Annual = r.Monthly * Payments
	return r
}
