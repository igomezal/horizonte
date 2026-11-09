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
	recentWindow   = 15 * 12 // art. 205.1.b: the 15 years before the pension starts...
	recentMinimum  = 2 * 12  // ...must hold at least 2 contributed years
)

// Input describes a person's contributions. AgeMonths and RetireMonths are
// months from the person's birth; the other ages are whole years.
type Input struct {
	AgeMonths          int     // current age in months
	YearsContributed   float64 // contributed so far
	MonthlyBase        float64 // current monthly contribution base, today's euros
	ContributeUntilAge int     // age at which contributions stop
	RetireAge          int     // age at which the pension is claimed (0 = ordinary age)
	RetireMonths       int     // exact age in months at which it is claimed; overrides RetireAge when above 0
	CalendarMonth      int     // the current month as year*12+month-1; 0 = unknown (the final art. 208 values apply)
	SelfEmployed       bool    // no filling of gaps with the minimum base
	TopUpToMinimum     bool    // apply the top-up to the minimum pension
}

// Result is the estimate.
type Result struct {
	Entitled        bool    // at least 15 years contributed, 2 of them in the last 15
	NotEntitledWhy  string  // when not Entitled: NotEntitledYears or NotEntitledRecent
	MonthsAtRetire  int     // months contributed when the pension starts
	OrdinaryAge     int     // legal retirement age in whole years (65 to 67)
	OrdinaryMonths  int     // legal retirement age in months: 67, or 65 up to 67 when 38y6m is reached in between
	RetireAge       int     // age in whole years the pension starts
	RetireMonths    int     // age in months the pension starts
	EarlyNotAllowed bool    // early retirement refused: RetireAge moved to the ordinary age
	EarlyWhy        string  // when EarlyNotAllowed: EarlyTooEarly, EarlyTooFewYears or EarlyBelowMinimum
	BaseReguladora  float64 // monthly
	Percent         float64 // share of the base by years contributed
	Adjustment      float64 // early reduction (negative) or late bonus (positive), as a ratio
	Capped          bool    // limited to the maximum pension
	CapPhased       bool    // early reduction taken from the DT 34a table (a start before 2033)
	ToppedUp        bool    // raised towards the minimum pension
	Monthly         float64 // per payment, 14 payments a year
	Annual          float64
}

// Reasons a person gets no pension (Result.NotEntitledWhy).
const (
	NotEntitledYears  = "years"  // fewer than 15 years contributed (art. 205.1.b)
	NotEntitledRecent = "recent" // fewer than 2 contributed years in the 15 before the start (art. 205.1.b)
)

// Reasons early retirement is refused (Result.EarlyWhy).
const (
	EarlyTooEarly     = "too_early"     // more than 2 years before the legal age (art. 208.1.a)
	EarlyTooFewYears  = "too_few_years" // under 35 years of contribution (art. 208.1.b)
	EarlyBelowMinimum = "below_minimum" // the pension would not exceed the minimum pension (art. 208.1.c)
)

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
// right ("igual o superior"). Contribution periods count complete months
// only, and a fraction of a month is not counted (art. 208.2, last
// paragraph); the advance is the opposite: each month or fraction of a month
// counts as a whole month.
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

func earlyColumn(monthsContributed int) int {
	switch {
	case monthsContributed >= from44y6m:
		return col44y6m
	case monthsContributed >= from41y6m:
		return col41y6m
	case monthsContributed >= from38y6m:
		return col38y6m
	}
	return colUnder38y6m
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
	return earlyTable[earlyMaxMonths-monthsAhead][earlyColumn(monthsContributed)] / 100
}

// Years covered by the gradual tables of disposición transitoria trigésima
// cuarta, apartado 2.
const (
	capFirstYear = 2024
	capLastYear  = 2033
	capYears     = capLastYear - capFirstYear + 1
)

// capTable is the gradual table of disposición transitoria trigésima cuarta,
// apartado 2, of the LGSS (BOE-A-2015-11724, consolidated text last modified
// 21 October 2026): percentage reduction applied to the maximum pension when
// base reguladora times percentage exceeds it (art. 210.3, second
// paragraph), by contribution period, months of advance (24 down to 1) and
// calendar year of the pension start, 2024 to 2033. The layout follows the
// four tables of the BOE. The 2033 column equals earlyTable.
var capTable = [earlyColumns][earlyMaxMonths][capYears]float64{
	// <38y6m
	{
		{5.70, 7.40, 9.10, 10.80, 12.50, 14.20, 15.90, 17.60, 19.30, 21.00}, // 24
		{5.36, 6.72, 8.08, 9.44, 10.80, 12.16, 13.52, 14.88, 16.24, 17.60},  // 23
		{5.07, 6.13, 7.20, 8.27, 9.34, 10.40, 11.47, 12.54, 13.60, 14.67},   // 22
		{4.41, 5.31, 6.22, 7.13, 8.04, 8.94, 9.85, 10.76, 11.66, 12.57},     // 21
		{4.25, 5.00, 5.75, 6.50, 7.25, 8.00, 8.75, 9.50, 10.25, 11.00},      // 20
		{4.13, 4.76, 5.38, 6.01, 6.64, 7.27, 7.90, 8.52, 9.15, 9.78},        // 19
		{3.58, 4.16, 4.74, 5.32, 5.90, 6.48, 7.06, 7.64, 8.22, 8.80},        // 18
		{3.50, 4.00, 4.50, 5.00, 5.50, 6.00, 6.50, 7.00, 7.50, 8.00},        // 17
		{3.43, 3.87, 4.30, 4.73, 5.17, 5.60, 6.03, 6.46, 6.90, 7.33},        // 16
		{2.93, 3.35, 3.78, 4.21, 4.64, 5.06, 5.49, 5.92, 6.34, 6.77},        // 15
		{2.88, 3.26, 3.64, 4.02, 4.40, 4.77, 5.15, 5.53, 5.91, 6.29},        // 14
		{2.84, 3.17, 3.51, 3.85, 4.19, 4.52, 4.86, 5.20, 5.53, 5.87},        // 13
		{2.35, 2.70, 3.05, 3.40, 3.75, 4.10, 4.45, 4.80, 5.15, 5.50},        // 12
		{2.32, 2.64, 2.95, 3.27, 3.59, 3.91, 4.23, 4.54, 4.86, 5.18},        // 11
		{2.29, 2.58, 2.87, 3.16, 3.45, 3.73, 4.02, 4.31, 4.60, 4.89},        // 10
		{1.81, 2.13, 2.44, 2.75, 3.07, 3.38, 3.69, 4.00, 4.32, 4.63},        // 9
		{1.79, 2.08, 2.37, 2.66, 2.95, 3.24, 3.53, 3.82, 4.11, 4.40},        // 8
		{1.77, 2.04, 2.31, 2.58, 2.85, 3.11, 3.38, 3.65, 3.92, 4.19},        // 7
		{1.30, 1.60, 1.90, 2.20, 2.50, 2.80, 3.10, 3.40, 3.70, 4.00},        // 6
		{1.28, 1.57, 1.85, 2.13, 2.42, 2.70, 2.98, 3.26, 3.55, 3.83},        // 5
		{1.27, 1.53, 1.80, 2.07, 2.34, 2.60, 2.87, 3.14, 3.40, 3.67},        // 4
		{0.80, 1.10, 1.41, 1.71, 2.01, 2.31, 2.61, 2.92, 3.22, 3.52},        // 3
		{0.79, 1.08, 1.36, 1.65, 1.94, 2.23, 2.52, 2.80, 3.09, 3.38},        // 2
		{0.78, 1.05, 1.33, 1.60, 1.88, 2.16, 2.43, 2.71, 2.98, 3.26},        // 1
	},
	// 38y6m
	{
		{5.50, 7.00, 8.50, 10.00, 11.50, 13.00, 14.50, 16.00, 17.50, 19.00}, // 24
		{5.25, 6.50, 7.75, 9.00, 10.25, 11.50, 12.75, 14.00, 15.25, 16.50},  // 23
		{5.00, 6.00, 7.00, 8.00, 9.00, 10.00, 11.00, 12.00, 13.00, 14.00},   // 22
		{4.35, 5.20, 6.05, 6.90, 7.75, 8.60, 9.45, 10.30, 11.15, 12.00},     // 21
		{4.20, 4.90, 5.60, 6.30, 7.00, 7.70, 8.40, 9.10, 9.80, 10.50},       // 20
		{4.08, 4.67, 5.25, 5.83, 6.42, 7.00, 7.58, 8.16, 8.75, 9.33},        // 19
		{3.54, 4.08, 4.62, 5.16, 5.70, 6.24, 6.78, 7.32, 7.86, 8.40},        // 18
		{3.46, 3.93, 4.39, 4.86, 5.32, 5.78, 6.25, 6.71, 7.18, 7.64},        // 17
		{3.40, 3.80, 4.20, 4.60, 5.00, 5.40, 5.80, 6.20, 6.60, 7.00},        // 16
		{2.90, 3.29, 3.69, 4.08, 4.48, 4.88, 5.27, 5.67, 6.06, 6.46},        // 15
		{2.85, 3.20, 3.55, 3.90, 4.25, 4.60, 4.95, 5.30, 5.65, 6.00},        // 14
		{2.81, 3.12, 3.43, 3.74, 4.05, 4.36, 4.67, 4.98, 5.29, 5.60},        // 13
		{2.33, 2.65, 2.98, 3.30, 3.63, 3.95, 4.28, 4.60, 4.93, 5.25},        // 12
		{2.29, 2.59, 2.88, 3.18, 3.47, 3.76, 4.06, 4.35, 4.65, 4.94},        // 11
		{2.27, 2.53, 2.80, 3.07, 3.34, 3.60, 3.87, 4.14, 4.40, 4.67},        // 10
		{1.79, 2.08, 2.38, 2.67, 2.96, 3.25, 3.54, 3.84, 4.13, 4.42},        // 9
		{1.77, 2.04, 2.31, 2.58, 2.85, 3.12, 3.39, 3.66, 3.93, 4.20},        // 8
		{1.75, 2.00, 2.25, 2.50, 2.75, 3.00, 3.25, 3.50, 3.75, 4.00},        // 7
		{1.28, 1.56, 1.85, 2.13, 2.41, 2.69, 2.97, 3.26, 3.54, 3.82},        // 6
		{1.27, 1.53, 1.80, 2.06, 2.33, 2.59, 2.86, 3.12, 3.39, 3.65},        // 5
		{1.25, 1.50, 1.75, 2.00, 2.25, 2.50, 2.75, 3.00, 3.25, 3.50},        // 4
		{0.79, 1.07, 1.36, 1.64, 1.93, 2.22, 2.50, 2.79, 3.07, 3.36},        // 3
		{0.77, 1.05, 1.32, 1.59, 1.87, 2.14, 2.41, 2.68, 2.96, 3.23},        // 2
		{0.76, 1.02, 1.28, 1.54, 1.81, 2.07, 2.33, 2.59, 2.85, 3.11},        // 1
	},
	// 41y6m
	{
		{5.30, 6.60, 7.90, 9.20, 10.50, 11.80, 13.10, 14.40, 15.70, 17.00}, // 24
		{5.10, 6.20, 7.30, 8.40, 9.50, 10.60, 11.70, 12.80, 13.90, 15.00},  // 23
		{4.93, 5.87, 6.80, 7.73, 8.67, 9.60, 10.53, 11.46, 12.40, 13.33},   // 22
		{4.29, 5.09, 5.88, 6.67, 7.47, 8.26, 9.05, 9.84, 10.64, 11.43},     // 21
		{4.15, 4.80, 5.45, 6.10, 6.75, 7.40, 8.05, 8.70, 9.35, 10.00},      // 20
		{4.04, 4.58, 5.12, 5.66, 6.20, 6.73, 7.27, 7.81, 8.35, 8.89},       // 19
		{3.50, 4.00, 4.50, 5.00, 5.50, 6.00, 6.50, 7.00, 7.50, 8.00},       // 18
		{3.43, 3.85, 4.28, 4.71, 5.14, 5.56, 5.99, 6.42, 6.84, 7.27},       // 17
		{3.37, 3.73, 4.10, 4.47, 4.84, 5.20, 5.57, 5.94, 6.30, 6.67},       // 16
		{2.87, 3.23, 3.60, 3.96, 4.33, 4.69, 5.06, 5.42, 5.79, 6.15},       // 15
		{2.82, 3.14, 3.46, 3.78, 4.11, 4.43, 4.75, 5.07, 5.39, 5.71},       // 14
		{2.78, 3.07, 3.35, 3.63, 3.92, 4.20, 4.48, 4.76, 5.05, 5.33},       // 13
		{2.30, 2.60, 2.90, 3.20, 3.50, 3.80, 4.10, 4.40, 4.70, 5.00},       // 12
		{2.27, 2.54, 2.81, 3.08, 3.36, 3.63, 3.90, 4.17, 4.44, 4.71},       // 11
		{2.24, 2.49, 2.73, 2.98, 3.22, 3.46, 3.71, 3.95, 4.20, 4.44},       // 10
		{1.77, 2.04, 2.31, 2.58, 2.86, 3.13, 3.40, 3.67, 3.94, 4.21},       // 9
		{1.75, 2.00, 2.25, 2.50, 2.75, 3.00, 3.25, 3.50, 3.75, 4.00},       // 8
		{1.73, 1.96, 2.19, 2.42, 2.66, 2.89, 3.12, 3.35, 3.58, 3.81},       // 7
		{1.26, 1.53, 1.79, 2.06, 2.32, 2.58, 2.85, 3.11, 3.38, 3.64},       // 6
		{1.25, 1.50, 1.74, 1.99, 2.24, 2.49, 2.74, 2.98, 3.23, 3.48},       // 5
		{1.23, 1.47, 1.70, 1.93, 2.17, 2.40, 2.63, 2.86, 3.10, 3.33},       // 4
		{0.77, 1.04, 1.31, 1.58, 1.85, 2.12, 2.39, 2.66, 2.93, 3.20},       // 3
		{0.76, 1.02, 1.27, 1.53, 1.79, 2.05, 2.31, 2.56, 2.82, 3.08},       // 2
		{0.75, 0.99, 1.24, 1.48, 1.73, 1.98, 2.22, 2.47, 2.71, 2.96},       // 1
	},
	// 44y6m
	{
		{4.90, 5.80, 6.70, 7.60, 8.50, 9.40, 10.30, 11.20, 12.10, 13.00}, // 24
		{4.80, 5.60, 6.40, 7.20, 8.00, 8.80, 9.60, 10.40, 11.20, 12.00},  // 23
		{4.70, 5.40, 6.10, 6.80, 7.50, 8.20, 8.90, 9.60, 10.30, 11.00},   // 22
		{4.15, 4.80, 5.45, 6.10, 6.75, 7.40, 8.05, 8.70, 9.35, 10.00},    // 21
		{4.07, 4.64, 5.21, 5.78, 6.35, 6.92, 7.49, 8.06, 8.63, 9.20},     // 20
		{3.99, 4.48, 4.97, 5.46, 5.95, 6.44, 6.93, 7.42, 7.91, 8.40},     // 19
		{3.46, 3.92, 4.38, 4.84, 5.30, 5.76, 6.22, 6.68, 7.14, 7.60},     // 18
		{3.39, 3.78, 4.17, 4.56, 4.96, 5.35, 5.74, 6.13, 6.52, 6.91},     // 17
		{3.33, 3.67, 4.00, 4.33, 4.67, 5.00, 5.33, 5.66, 6.00, 6.33},     // 16
		{2.84, 3.17, 3.51, 3.84, 4.18, 4.51, 4.85, 5.18, 5.52, 5.85},     // 15
		{2.79, 3.09, 3.38, 3.67, 3.97, 4.26, 4.55, 4.84, 5.14, 5.43},     // 14
		{2.76, 3.01, 3.27, 3.53, 3.79, 4.04, 4.30, 4.56, 4.81, 5.07},     // 13
		{2.28, 2.55, 2.83, 3.10, 3.38, 3.65, 3.93, 4.20, 4.48, 4.75},     // 12
		{2.25, 2.49, 2.74, 2.99, 3.24, 3.48, 3.73, 3.98, 4.22, 4.47},     // 11
		{2.22, 2.44, 2.67, 2.89, 3.11, 3.33, 3.55, 3.78, 4.00, 4.22},     // 10
		{1.75, 2.00, 2.25, 2.50, 2.75, 3.00, 3.25, 3.50, 3.75, 4.00},     // 9
		{1.73, 1.96, 2.19, 2.42, 2.65, 2.88, 3.11, 3.34, 3.57, 3.80},     // 8
		{1.71, 1.92, 2.14, 2.35, 2.56, 2.77, 2.98, 3.20, 3.41, 3.62},     // 7
		{1.25, 1.49, 1.74, 1.98, 2.23, 2.47, 2.72, 2.96, 3.21, 3.45},     // 6
		{1.23, 1.46, 1.69, 1.92, 2.15, 2.38, 2.61, 2.84, 3.07, 3.30},     // 5
		{1.22, 1.43, 1.65, 1.87, 2.09, 2.30, 2.52, 2.74, 2.95, 3.17},     // 4
		{0.75, 1.01, 1.26, 1.52, 1.77, 2.02, 2.28, 2.53, 2.79, 3.04},     // 3
		{0.74, 0.98, 1.23, 1.47, 1.71, 1.95, 2.19, 2.44, 2.68, 2.92},     // 2
		{0.73, 0.96, 1.19, 1.42, 1.66, 1.89, 2.12, 2.35, 2.58, 2.81},     // 1
	},
}

// capReduction is the reduction applied to the maximum pension (art. 210.3,
// second paragraph) for a pension that starts in the given calendar year.
// From 2033 on, and when the year is unknown (0), it is the art. 208.2 value.
// Before 2024 the transitional rule did not apply yet; that is not a case the
// estimate needs, so the 2024 values are used.
func capReduction(year, monthsContributed, monthsAhead int) float64 {
	if year == 0 || year >= capLastYear || monthsAhead <= 0 {
		return earlyReduction(monthsContributed, monthsAhead)
	}
	monthsAhead = min(monthsAhead, earlyMaxMonths)
	year = max(year, capFirstYear)
	return capTable[earlyColumn(monthsContributed)][earlyMaxMonths-monthsAhead][year-capFirstYear] / 100
}

// completeMonths is the whole months in a number of years. Contribution
// periods count complete months only (art. 205.1.a and 208.2); the tiny
// tolerance keeps exact inputs such as 38.5 years at 462 months.
func completeMonths(years float64) int {
	return int(math.Floor(years*12 + 1e-6))
}

// Estimate computes the pension.
func Estimate(in Input) Result {
	base := clamp(in.MonthlyBase, MinBase, MaxBase)
	past := completeMonths(in.YearsContributed)
	start := in.AgeMonths - past // contributions are taken as continuous up to now

	// monthsAt counts the months contributed if the pension starts at the
	// given age in months.
	monthsAt := func(retire int) int {
		stop := min(in.ContributeUntilAge*12, retire)
		return past + max(0, stop-in.AgeMonths)
	}

	// legalFor is the legal retirement age, in months, for a claim at retire
	// (art. 205.1.a): 67, or 65 when 38 years and 6 months are accredited.
	// The article does not ask for the 38y6m to be held on the 65th birthday
	// and requirements are checked when the right arises, so the legal age is
	// the first age from 65 on at which the person has accredited them: 65,
	// or a later age up to 67.
	// If they are not accredited by the claim, art. 208.2 (last paragraphs)
	// takes the age the person would have had by contributing from the claim
	// up to it, so contributions are assumed to continue month by month and
	// the legal age is the month they would reach 38y6m, between 65 and 67.
	// Both are readings of arts. 205.1.a and 208.2; no INSS statement on the
	// cases between 65 and 67 was found.
	const age65, age67 = 65 * 12, 67 * 12
	legalFor := func(retire int) int {
		for a := age65; a <= min(retire, age67); a++ {
			if monthsAt(a) >= fullOrdinary {
				return a
			}
		}
		return min(age67, max(age65, retire+fullOrdinary-monthsAt(retire)))
	}
	// Without a claim the pension starts at the legal age reached by
	// contributing until the stated age.
	ord := legalFor(age67)
	retire := in.RetireAge * 12
	if in.RetireMonths > 0 {
		retire = in.RetireMonths
	}
	if retire == 0 {
		retire = ord
	}
	legal := legalFor(retire)

	// entitled checks art. 205.1.b: 15 years contributed, 2 of them within
	// the 15 years before the pension starts. Contributions run continuously
	// from start for m months. A person in an "alta asimilada" without the
	// duty to contribute would measure the 15 years from the end of that
	// duty; the estimate has no such state, so "contribute until" stands in.
	entitled := func(retire, m int) string {
		if m < minMonths {
			return NotEntitledYears
		}
		if min(start+m, retire)-max(start, retire-recentWindow) < recentMinimum {
			return NotEntitledRecent
		}
		return ""
	}
	// pension is the monthly amount before any top-up, with the base
	// reguladora, percentage and adjustment that produced it.
	type calc struct {
		br, pct, adj, p float64
		capped, phased  bool
	}
	pension := func(retire, legal, m int) (c calc) {
		// Base reguladora: the last 300 months before retiring. Months
		// without contributions after the first one are filled (employees
		// only).
		stop := min(in.ContributeUntilAge*12, retire)
		var sum float64
		gap := 0
		for t := retire - windowMonths; t < retire; t++ {
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
		br, pct := sum/divisor, Percent(m)
		full := br * pct
		c.br, c.pct = br, pct
		if early := legal - retire; early > 0 {
			// Art. 210.3: the coefficients apply to the pension from the
			// percentage; if that exceeds the maximum pension they apply to
			// the maximum instead, phased in by calendar year (DT 34a).
			// DT 34a.1 adds that the second paragraph applies only so far as
			// the evolution of the maximum pension absorbs the effect, so that
			// the pension is never lower than under the 2021 rules (art.
			// 208.2 then: 2 / 1,875 / 1,75 / 1,625 % per quarter; art. 210.3
			// then: the reduced pension capped at the maximum less 0,50 % per
			// quarter). This is read as a condition on how the maximum pension
			// is revalued, not as a per-person floor: a floor would cancel the
			// phase-in for everyone at the cap. It is not modelled.
			// The start is in whole months, so each month or fraction of
			// advance (art. 208.2) is already a whole month.
			if full > MaxPension {
				year := 0
				if in.CalendarMonth > 0 {
					year = (in.CalendarMonth + retire - in.AgeMonths) / 12
				}
				c.adj = -capReduction(year, m, early)
				c.p, c.capped, c.phased = MaxPension*(1+c.adj), true, year > 0 && year < capLastYear
				return c
			}
			c.adj = -earlyReduction(m, early)
			c.p = full * (1 + c.adj)
			return c
		}
		// Art. 210.2.a, late retirement: 4 % for each complete year
		// contributed between reaching the legal age and the claim (not for
		// time without contributions), plus 2 % for a further period of more
		// than 6 months and less than a year from the second complete year
		// of delay. It is added to the percentage for the months contributed
		// and the sum is applied to the base reguladora. Only if the 15 years
		// were accredited at the legal age.
		extra := 0.0
		if monthsAt(legal) >= minMonths {
			delay := max(0, min(in.ContributeUntilAge*12, retire)-legal)
			years, rest := delay/12, delay%12
			extra = 0.04 * float64(years)
			if years >= 2 && rest > 6 {
				extra += 0.02
			}
		}
		c.adj = extra
		c.p = br * (pct + extra)
		if c.p > MaxPension {
			c.p, c.capped = MaxPension, true
		}
		return c
	}

	r := Result{OrdinaryAge: legal / 12, OrdinaryMonths: legal}
	m := monthsAt(retire)
	if retire < legal {
		why := ""
		switch {
		case retire < legal-24:
			why = EarlyTooEarly
		case m < earlyMinMonths:
			why = EarlyTooFewYears
		}
		if why == "" {
			if nw := entitled(retire, m); nw != "" {
				r.RetireMonths, r.RetireAge, r.MonthsAtRetire, r.NotEntitledWhy = retire, retire/12, m, nw
				return r
			}
			// Art. 208.1.c: the pension must exceed the minimum pension
			// that would apply at 65. The app has no family situation, so
			// it uses MinPension (no dependent spouse).
			if c := pension(retire, legal, m); !(c.p > MinPension) {
				why = EarlyBelowMinimum
			}
		}
		if why != "" {
			// Not available: the pension starts at the ordinary age, worked
			// out from the contributions actually made.
			r.EarlyNotAllowed, r.EarlyWhy = true, why
			legal = ord
			r.OrdinaryAge, r.OrdinaryMonths = legal/12, legal
			retire = legal
			m = monthsAt(retire)
		}
	}
	r.RetireMonths, r.RetireAge, r.MonthsAtRetire = retire, retire/12, m
	if nw := entitled(retire, m); nw != "" {
		r.NotEntitledWhy = nw
		return r
	}
	r.Entitled = true
	c := pension(retire, legal, m)
	r.BaseReguladora, r.Percent, r.Adjustment, r.Capped, r.CapPhased = c.br, c.pct, c.adj, c.capped, c.phased
	p := c.p
	if in.TopUpToMinimum && p < MinPension && retire >= 65*12 {
		p, r.ToppedUp = p+math.Min(MinPension-p, MaxComplement), true
	}
	r.Monthly = math.Round(p*100) / 100
	r.Annual = r.Monthly * Payments
	return r
}
