package es

import "math"

// Income is one person's taxable income for a year, in euros.
type Income struct {
	Age int // at the end of the year

	Salary         float64 // gross salary (Social Security contributions are deducted)
	Pension        float64 // public pensions
	PlanWithdrawal float64 // money taken out of pension plans
	SelfEmployed   float64 // net business profit before IRPF
	Rental         float64 // housing rental income
	Other          float64 // other income for the general base

	Savings     float64 // interest and realised capital gains
	PlanContrib float64 // contributions to pension plans
}

// Breakdown explains a year's tax.
type Breakdown struct {
	SocialSecurity float64 // employee contributions on the salary (paid, not IRPF)
	WorkNet        float64 // net work income after reductions
	GeneralBase    float64 // base liquidable general
	SavingsBase    float64 // base liquidable del ahorro
	Minimum        float64 // personal minimum
	GeneralTax     float64
	SavingsTax     float64
	Tax            float64 // total IRPF
}

// Apply returns the tax of a scale on base.
func (s Scale) Apply(base float64) float64 {
	if base <= 0 {
		return 0
	}
	tax, low := 0.0, 0.0
	for _, b := range s {
		high := b.Upto
		if high == 0 || base < high {
			high = base
		}
		if high > low {
			tax += (high - low) * b.Rate
		}
		if b.Upto == 0 || base <= b.Upto {
			break
		}
		low = b.Upto
	}
	return tax
}

// WorkReduction is the art. 20 reduction for a net work income.
func WorkReduction(net float64) float64 {
	switch {
	case net <= WorkReductionStep1:
		return WorkReductionMax
	case net <= WorkReductionStep2:
		return WorkReductionMax - 1.75*(net-WorkReductionStep1)
	case net <= WorkReductionStep3:
		return 2364.34 - 1.14*(net-WorkReductionStep2)
	default:
		return 0
	}
}

// Compute returns the IRPF of one person for one year (individual filing).
func Compute(in Income, region Region) Breakdown {
	var b Breakdown

	// Work income: salary (minus Social Security), public pensions and
	// pension plan withdrawals.
	b.SocialSecurity = math.Min(in.Salary, SocialSecurityMaxBase) * SocialSecurityRate
	work := in.Salary - b.SocialSecurity + in.Pension + in.PlanWithdrawal
	if work > 0 {
		work = math.Max(0, work-WorkOtherExpenses)
	}
	other := in.SelfEmployed + in.Rental + in.Other + in.Savings
	if work > 0 && other <= WorkReductionOtherIncomeLimit {
		work = math.Max(0, work-WorkReduction(work))
	}
	b.WorkNet = work

	business := in.SelfEmployed
	if business > 0 {
		business -= math.Min(business*SelfEmployedExpenseRate, SelfEmployedExpenseMax)
	}
	rental := in.Rental * (1 - RentalReduction)

	general := work + business + rental + in.Other
	contrib := math.Min(in.PlanContrib, math.Min(PensionContribLimit, PensionContribShare*math.Max(0, work+business)))
	b.GeneralBase = math.Max(0, general-contrib)
	b.SavingsBase = math.Max(0, in.Savings)

	// Personal minimum: taxed at zero, first against the general base and the
	// rest against the savings base.
	b.Minimum = PersonalMinimum
	if in.Age > 65 {
		b.Minimum += PersonalMinimumOver65
	}
	if in.Age > 75 {
		b.Minimum += PersonalMinimumOver75
	}
	minGeneral := math.Min(b.Minimum, b.GeneralBase)
	minSavings := math.Min(b.Minimum-minGeneral, b.SavingsBase)

	state, regional := StateGeneral, region.Scale
	b.GeneralTax = math.Max(0, state.Apply(b.GeneralBase)+regional.Apply(b.GeneralBase)-
		state.Apply(minGeneral)-regional.Apply(minGeneral))
	b.SavingsTax = math.Max(0, Savings.Apply(b.SavingsBase)-Savings.Apply(minSavings))
	b.Tax = b.GeneralTax + b.SavingsTax
	return b
}
