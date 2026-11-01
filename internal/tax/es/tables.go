// Package es computes Spanish personal income tax (IRPF) for one person and
// one year. Figures are the 2025 rules; update tables.go when they change.
package es

// Bracket is one step of a progressive scale: Rate applies to income above
// the previous bracket's Upto and up to this bracket's Upto (0 = no limit).
type Bracket struct {
	Upto float64
	Rate float64
}

// Scale is a progressive tax scale.
type Scale []Bracket

// Year of the rules in this file.
const RulesYear = 2025

// StateGeneral is the state half of the general scale (escala estatal, art. 63 LIRPF).
var StateGeneral = Scale{
	{12450, 0.095}, {20200, 0.12}, {35200, 0.15}, {60000, 0.185}, {300000, 0.225}, {0, 0.245},
}

// Savings is the full savings scale (state + regional halves, art. 66 and 76 LIRPF).
var Savings = Scale{
	{6000, 0.19}, {50000, 0.21}, {200000, 0.23}, {300000, 0.27}, {0, 0.30},
}

// Region is a regional (autonómica) scale for the general base.
type Region struct {
	Key   string
	Scale Scale
}

// Regions available in the app. "general" uses a regional half equal to the
// state half (the classic combined 19–47 % scale).
var Regions = []Region{
	{"general", StateGeneral},
	{"madrid", Scale{{13362.22, 0.085}, {19004.63, 0.107}, {35425.68, 0.128}, {57320.40, 0.174}, {0, 0.205}}},
	{"cataluna", Scale{
		{12450, 0.105}, {17707.20, 0.12}, {21000, 0.14}, {33007.20, 0.15}, {53407.20, 0.188},
		{90000, 0.215}, {120000, 0.235}, {175000, 0.245}, {0, 0.255},
	}},
}

// FindRegion returns the region with key, or the general one.
func FindRegion(key string) Region {
	for _, r := range Regions {
		if r.Key == key {
			return r
		}
	}
	return Regions[0]
}

const (
	// Employee Social Security contributions: common contingencies 4,70 %,
	// unemployment 1,55 %, training 0,10 % and MEI 0,13 %.
	SocialSecurityRate = 0.0648
	// Maximum contribution base: 4.909,50 € a month.
	SocialSecurityMaxBase = 4909.50 * 12

	// Deductible "other expenses" of work income (art. 19.2.f LIRPF).
	WorkOtherExpenses = 2000.0

	// Reduction for low net work income (art. 20 LIRPF, since 2024).
	WorkReductionMax   = 7302.0
	WorkReductionStep1 = 14852.0
	WorkReductionStep2 = 17673.52
	WorkReductionStep3 = 19747.50
	// It does not apply when income other than work exceeds this amount.
	WorkReductionOtherIncomeLimit = 6500.0

	// Personal minimum (art. 57 LIRPF).
	PersonalMinimum       = 5550.0
	PersonalMinimumOver65 = 1150.0
	PersonalMinimumOver75 = 1400.0

	// Pension plan contributions reduce the general base up to this amount
	// and this share of net work and business income (art. 51–52 LIRPF).
	PensionContribLimit = 1500.0
	PensionContribShare = 0.30

	// Housing rental income reduction (art. 23.2 LIRPF, general case).
	RentalReduction = 0.50

	// Self-employed, simplified direct estimation: hard-to-document expenses.
	SelfEmployedExpenseRate = 0.05
	SelfEmployedExpenseMax  = 2000.0
)
