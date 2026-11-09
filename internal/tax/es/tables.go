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

// SavingsHalf is one half of the savings scale (arts. 66 and 76 LIRPF). The
// State applies one half and the region the other, and the regions cannot
// change it, so both use these rates. Savings is the two halves together.
var SavingsHalf = Scale{
	{6000, 0.095}, {50000, 0.105}, {200000, 0.115}, {300000, 0.135}, {0, 0.15},
}

// Minimum is a region's own personal minimum for its half of the tax (art. 56.3
// LIRPF): the taxpayer's amount, and the extra from age 66 and from age 76.
type Minimum struct {
	Base, Over65, Over75 float64
}

// StateMinimum is the minimum of art. 57 LIRPF, used for the State half and
// for every region that sets no minimum of its own.
var StateMinimum = Minimum{PersonalMinimum, PersonalMinimumOver65, PersonalMinimumOver75}

// Amount is the minimum for a taxpayer of the given age at the end of the year.
func (m Minimum) Amount(age int) float64 {
	a := m.Base
	if age > 65 {
		a += m.Over65
	}
	if age > 75 {
		a += m.Over75
	}
	return a
}

// Region is a regional (autonómica) scale for the general base, with the
// region's own personal minimum. A zero Minimum means the State amounts.
type Region struct {
	Key     string
	Scale   Scale
	Minimum Minimum
}

// PersonalMinimum is the minimum for the regional half of the tax.
func (r Region) PersonalMinimum(age int) float64 {
	if r.Minimum == (Minimum{}) {
		return StateMinimum.Amount(age)
	}
	return r.Minimum.Amount(age)
}

// Regions available in the app, in selector order. "general" uses a regional
// half equal to the state half (the classic combined 19–47 % scale). The
// others are the 2026 scales and personal minimums in Ministerio de Hacienda,
// "Tributación Autonómica. Medidas 2026", chapter IV, section IRPF. Regions
// that do not set a minimum (Aragón, Cantabria, Castilla-La Mancha, Castilla y
// León, Extremadura, Murcia, La Rioja) use the State amounts, as does Cataluña,
// which only restates the State 5.550. País Vasco and Navarra have their own
// foral tax systems and are not modelled.
var Regions = []Region{
	{Key: "general", Scale: StateGeneral},
	{Key: "andalucia", Scale: Scale{{13000, 0.095}, {21100, 0.12}, {35200, 0.15}, {60000, 0.185}, {0, 0.225}}, Minimum: Minimum{5790, 1200, 1460}},
	{Key: "aragon", Scale: Scale{
		{13072.50, 0.095}, {21210, 0.12}, {36960, 0.15}, {52500, 0.185}, {60000, 0.205},
		{80000, 0.23}, {90000, 0.24}, {130000, 0.25}, {0, 0.255},
	}},
	{Key: "asturias", Scale: Scale{
		{12450, 0.09}, {17707.20, 0.12}, {33007.20, 0.14}, {53407.20, 0.192}, {70000, 0.215},
		{90000, 0.225}, {175000, 0.25}, {0, 0.26},
	}, Minimum: Minimum{6105, 1265, 1540}},
	{Key: "baleares", Scale: Scale{
		{10000, 0.09}, {18000, 0.1125}, {30000, 0.1425}, {48000, 0.175}, {70000, 0.19},
		{90000, 0.2175}, {120000, 0.2275}, {175000, 0.2375}, {0, 0.2475},
	}, Minimum: Minimum{5550, 1265, 1540}},
	{Key: "canarias", Scale: Scale{
		{13748, 0.09}, {19422, 0.115}, {35924, 0.14}, {57566, 0.185}, {93268, 0.235},
		{123745, 0.25}, {0, 0.26},
	}, Minimum: Minimum{5606, 1162, 1414}},
	{Key: "cantabria", Scale: Scale{{13000, 0.085}, {21000, 0.11}, {35200, 0.145}, {60000, 0.18}, {90000, 0.225}, {0, 0.245}}},
	{Key: "castilla_la_mancha", Scale: Scale{{12450, 0.095}, {20200, 0.12}, {35200, 0.15}, {60000, 0.185}, {0, 0.225}}},
	{Key: "castilla_leon", Scale: Scale{{12450, 0.09}, {20200, 0.12}, {35200, 0.14}, {53407.20, 0.185}, {0, 0.215}}},
	{Key: "cataluna", Scale: Scale{
		{12500, 0.095}, {22000, 0.125}, {33000, 0.16}, {53000, 0.19}, {90000, 0.215},
		{120000, 0.235}, {175000, 0.245}, {0, 0.255},
	}},
	{Key: "extremadura", Scale: Scale{
		{12450, 0.0775}, {20200, 0.0975}, {24200, 0.16}, {35200, 0.175}, {60000, 0.21},
		{80200, 0.235}, {99200, 0.24}, {120200, 0.245}, {0, 0.25},
	}},
	{Key: "galicia", Scale: Scale{{12985.35, 0.09}, {21068.60, 0.1165}, {35200, 0.149}, {60000, 0.184}, {0, 0.225}}, Minimum: Minimum{5789, 1199, 1460}},
	{Key: "madrid", Scale: Scale{{13362.22, 0.085}, {19004.63, 0.107}, {35425.68, 0.128}, {57320.40, 0.174}, {0, 0.205}}, Minimum: Minimum{5956.65, 1234.26, 1502.58}},
	{Key: "murcia", Scale: Scale{{12450, 0.095}, {20200, 0.112}, {34000, 0.133}, {60000, 0.179}, {0, 0.225}}},
	{Key: "rioja", Scale: Scale{
		{12450, 0.08}, {20200, 0.106}, {35200, 0.136}, {40000, 0.178}, {50000, 0.183},
		{60000, 0.19}, {120000, 0.245}, {0, 0.27},
	}},
	{Key: "valencia", Scale: Scale{
		{12000, 0.088}, {22000, 0.117}, {32000, 0.146}, {42000, 0.17}, {52000, 0.194}, {62000, 0.219},
		{72000, 0.244}, {100000, 0.261}, {150000, 0.2735}, {200000, 0.2835}, {0, 0.2935},
	}, Minimum: Minimum{6105, 1265, 1540}},
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

	// Deduction for obtaining work income (disposición adicional 61 LIRPF, as
	// raised by RDL 5/2026 from 1 January 2026). It applies to gross employee
	// income below WorkDeductionUpper when other income is at most
	// WorkDeductionOtherIncomeLimit: the full amount up to WorkDeductionStep,
	// then reduced by WorkDeductionSlope of the excess.
	WorkDeductionMax              = 590.89
	WorkDeductionStep             = 17094.0
	WorkDeductionUpper            = 20048.45
	WorkDeductionSlope            = 0.2
	WorkDeductionOtherIncomeLimit = 6500.0

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
