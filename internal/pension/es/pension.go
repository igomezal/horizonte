// Package es estimates the Spanish contributory retirement pension
// (pensión de jubilación de la Seguridad Social) with the rules that apply
// from 2027 on. Amounts are in today's euros (2026 bases and limits): public
// pensions are revalued with inflation, so the estimate grows with it.
//
// It is an approximation for planning. It assumes the contribution base stays
// the same in real terms, that past contributions were continuous up to now,
// and it ignores special schemes, partial retirement and the transitional
// choice of the best 27 of the last 29 years.
package es

import "math"

// 2026 limits.
const (
	MinBase        = 1424.40 // monthly minimum contribution base (general scheme)
	MaxBase        = 5101.20 // monthly maximum contribution base
	MaxPension     = 3359.60 // monthly maximum pension, 14 payments
	MinPension     = 936.20  // monthly minimum pension at 65+, no dependent spouse, 14 payments
	MaxComplement  = 628.80  // top-up to the minimum is capped at the non-contributory pension
	Payments       = 14
	windowMonths   = 300 // contribution bases that make up the base reguladora (25 years)
	divisor        = 350
	minMonths      = 15 * 12
	fullOrdinary   = 462     // 38 years 6 months: ordinary age 65 instead of 67
	earlyMinMonths = 35 * 12 // art. 208.1.b: 35 years of effective contribution
)

// Input describes a person's contributions. Ages are whole years; months
// count from the person's birth.
type Input struct {
	AgeMonths          int     // current age in months
	YearsContributed   float64 // contributed so far
	MonthlyBase        float64 // current monthly contribution base, today's euros
	ContributeUntilAge int     // age at which contributions stop
	RetireAge          int     // age at which the pension is claimed (0 = ordinary age)
	RetireMonths       int     // exact age in months at which it is claimed; overrides RetireAge when above 0
	SelfEmployed       bool    // no filling of gaps with the minimum base
	TopUpToMinimum     bool    // apply the top-up to the minimum pension
}

// Result is the estimate.
type Result struct {
	Entitled        bool    // at least 15 years contributed
	MonthsAtRetire  int     // months contributed when the pension starts
	OrdinaryAge     int     // 65 or 67
	RetireAge       int     // age in whole years the pension starts
	RetireMonths    int     // age in months the pension starts
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

// Columns of the art. 208.2 table, by contribution period at the date the
// pension starts. A period equal to a limit belongs to the column on its
// right ("igual o superior"). Whole months only: the law does not round
// fractions of a month up.
const (
	colUnder38y6m = iota // fewer than 38 years and 6 months
	col38y6m             // 38 years 6 months or more, fewer than 41 years 6 months
	col41y6m             // 41 years 6 months or more, fewer than 44 years 6 months
	col44y6m             // 44 years 6 months or more
	earlyColumns
)

const (
	from38y6m = 38*12 + 6 // 462 months
	from41y6m = 41*12 + 6 // 498 months
	from44y6m = 44*12 + 6 // 534 months
)

// earlyMaxMonths is how far ahead of the legal age the pension may start
// (art. 208.1.a: two years at most) and the first row of the table.
const earlyMaxMonths = 24

// earlyTable is the table of art. 208.2 of the LGSS (Real Decreto Legislativo
// 8/2015, BOE-A-2015-11724, consolidated text last modified 21 October 2026):
// percentage reduction of the pension for each number of months the pension
// starts before the legal retirement age, by contribution period. Rows are in
// the order of the BOE, from 24 months down to 1. The same values form the
// 2033 column of the tables in disposición transitoria trigésima cuarta.
var earlyTable = [earlyMaxMonths][earlyColumns]float64{
	//  <38y6m  38y6m  41y6m  44y6m   months ahead
	{21.00, 19.00, 17.00, 13.00}, // 24
	{17.60, 16.50, 15.00, 12.00}, // 23
	{14.67, 14.00, 13.33, 11.00}, // 22
	{12.57, 12.00, 11.43, 10.00}, // 21
	{11.00, 10.50, 10.00, 9.20},  // 20
	{9.78, 9.33, 8.89, 8.40},     // 19
	{8.80, 8.40, 8.00, 7.60},     // 18
	{8.00, 7.64, 7.27, 6.91},     // 17
	{7.33, 7.00, 6.67, 6.33},     // 16
	{6.77, 6.46, 6.15, 5.85},     // 15
	{6.29, 6.00, 5.71, 5.43},     // 14
	{5.87, 5.60, 5.33, 5.07},     // 13
	{5.50, 5.25, 5.00, 4.75},     // 12
	{5.18, 4.94, 4.71, 4.47},     // 11
	{4.89, 4.67, 4.44, 4.22},     // 10
	{4.63, 4.42, 4.21, 4.00},     // 9
	{4.40, 4.20, 4.00, 3.80},     // 8
	{4.19, 4.00, 3.81, 3.62},     // 7
	{4.00, 3.82, 3.64, 3.45},     // 6
	{3.83, 3.65, 3.48, 3.30},     // 5
	{3.67, 3.50, 3.33, 3.17},     // 4
	{3.52, 3.36, 3.20, 3.04},     // 3
	{3.38, 3.23, 3.08, 2.92},     // 2
	{3.26, 3.11, 2.96, 2.81},     // 1
}

// earlyReduction is the reduction (as a ratio, 0.176 for 17,60 %) of art.
// 208.2 for a pension that starts monthsAhead months before the legal age
// with the given months contributed. Zero for no advance; advances beyond 24
// months are not allowed and are treated as 24.
func earlyReduction(monthsContributed, monthsAhead int) float64 {
	if monthsAhead <= 0 {
		return 0
	}
	monthsAhead = min(monthsAhead, earlyMaxMonths)
	col := colUnder38y6m
	switch {
	case monthsContributed >= from44y6m:
		col = col44y6m
	case monthsContributed >= from41y6m:
		col = col41y6m
	case monthsContributed >= from38y6m:
		col = col38y6m
	}
	return earlyTable[earlyMaxMonths-monthsAhead][col] / 100
}

// Estimate computes the pension.
func Estimate(in Input) Result {
	base := clamp(in.MonthlyBase, MinBase, MaxBase)
	past := int(math.Round(in.YearsContributed * 12))
	start := in.AgeMonths - past // contributions are taken as continuous up to now

	// monthsAt counts the months contributed if the pension starts at the
	// given age in months.
	monthsAt := func(retire int) int {
		stop := min(in.ContributeUntilAge*12, retire)
		return past + max(0, stop-in.AgeMonths)
	}

	// Ordinary age depends on the months contributed by then.
	ord := 67
	if monthsAt(65*12) >= fullOrdinary {
		ord = 65
	}
	retire := in.RetireAge * 12
	if in.RetireMonths > 0 {
		retire = in.RetireMonths
	}
	if retire == 0 {
		retire = ord * 12
	}
	r := Result{OrdinaryAge: ord}
	m := monthsAt(retire)
	if retire < ord*12 && (retire < (ord-2)*12 || m < earlyMinMonths) {
		r.EarlyNotAllowed, retire = true, ord*12
		m = monthsAt(retire)
	}
	r.RetireMonths, r.RetireAge = retire, retire/12
	r.MonthsAtRetire = m
	if m < minMonths {
		return r
	}
	r.Entitled = true

	// Base reguladora: the last 300 months before retiring. Months without
	// contributions after the first one are filled (employees only).
	end := retire
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
		// Months ahead of the legal age, in whole months (art. 208.2).
		r.Adjustment = -earlyReduction(m, early)
	} else if late := (end - ord*12) / 12; late > 0 {
		r.Adjustment = 0.04 * float64(late)
	}
	p := r.BaseReguladora * r.Percent * (1 + r.Adjustment)
	if p > MaxPension {
		p, r.Capped = MaxPension, true
	}
	if in.TopUpToMinimum && p < MinPension && retire >= 65*12 {
		p, r.ToppedUp = p+math.Min(MinPension-p, MaxComplement), true
	}
	r.Monthly = math.Round(p*100) / 100
	r.Annual = r.Monthly * Payments
	return r
}
