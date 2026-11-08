// Package es computes Spanish personal income tax (IRPF) for one person and
// one year. Figures are the 2026 rules; update tables.go when they change.
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
const RulesYear = 2026

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

// Regions available in the app, in selector order. "general" uses a regional
// half equal to the state half (the classic combined 19–47 % scale). The
// others are the 2026 scales in Ministerio de Hacienda, "Tributación
// Autonómica. Medidas 2026", chapter IV. País Vasco and Navarra have their own
// foral tax systems and are not modelled.
var Regions = []Region{
	{"general", StateGeneral},
	{"andalucia", Scale{{13000, 0.095}, {21100, 0.12}, {35200, 0.15}, {60000, 0.185}, {0, 0.225}}},
	{"aragon", Scale{
		{13072.50, 0.095}, {21210, 0.12}, {36960, 0.15}, {52500, 0.185}, {60000, 0.205},
		{80000, 0.23}, {90000, 0.24}, {130000, 0.25}, {0, 0.255},
	}},
	{"asturias", Scale{
		{12450, 0.09}, {17707.20, 0.12}, {33007.20, 0.14}, {53407.20, 0.192}, {70000, 0.215},
		{90000, 0.225}, {175000, 0.25}, {0, 0.26},
	}},
	{"baleares", Scale{
		{10000, 0.09}, {18000, 0.1125}, {30000, 0.1425}, {48000, 0.175}, {70000, 0.19},
		{90000, 0.2175}, {120000, 0.2275}, {175000, 0.2375}, {0, 0.2475},
	}},
	{"canarias", Scale{
		{13748, 0.09}, {19422, 0.115}, {35924, 0.14}, {57566, 0.185}, {93268, 0.235},
		{123745, 0.25}, {0, 0.26},
	}},
	{"cantabria", Scale{{13000, 0.085}, {21000, 0.11}, {35200, 0.145}, {60000, 0.18}, {90000, 0.225}, {0, 0.245}}},
	{"castilla_la_mancha", Scale{{12450, 0.095}, {20200, 0.12}, {35200, 0.15}, {60000, 0.185}, {0, 0.225}}},
	{"castilla_leon", Scale{{12450, 0.09}, {20200, 0.12}, {35200, 0.14}, {53407.20, 0.185}, {0, 0.215}}},
	{"cataluna", Scale{
		{12500, 0.095}, {22000, 0.125}, {33000, 0.16}, {53000, 0.19}, {90000, 0.215},
		{120000, 0.235}, {175000, 0.245}, {0, 0.255},
	}},
	{"extremadura", Scale{
		{12450, 0.0775}, {20200, 0.0975}, {24200, 0.16}, {35200, 0.175}, {60000, 0.21},
		{80200, 0.235}, {99200, 0.24}, {120200, 0.245}, {0, 0.25},
	}},
	{"galicia", Scale{{12985.35, 0.09}, {21068.60, 0.1165}, {35200, 0.149}, {60000, 0.184}, {0, 0.225}}},
	{"madrid", Scale{{13362.22, 0.085}, {19004.63, 0.107}, {35425.68, 0.128}, {57320.40, 0.174}, {0, 0.205}}},
	{"murcia", Scale{{12450, 0.095}, {20200, 0.112}, {34000, 0.133}, {60000, 0.179}, {0, 0.225}}},
	{"rioja", Scale{
		{12450, 0.08}, {20200, 0.106}, {35200, 0.136}, {40000, 0.178}, {50000, 0.183},
		{60000, 0.19}, {120000, 0.245}, {0, 0.27},
	}},
	{"valencia", Scale{
		{12000, 0.088}, {22000, 0.117}, {32000, 0.146}, {42000, 0.17}, {52000, 0.194}, {62000, 0.219},
		{72000, 0.244}, {100000, 0.261}, {150000, 0.2735}, {200000, 0.2835}, {0, 0.2935},
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
	// unemployment 1,55 %, training 0,10 % and MEI 0,15 %.
	SocialSecurityRate = 0.0650
	// Maximum contribution base: 5.101,20 € a month.
	SocialSecurityMaxBase = 5101.20 * 12

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
