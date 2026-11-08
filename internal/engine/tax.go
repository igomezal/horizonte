package engine

import (
	"math"

	"github.com/igomezal/horizonte/internal/domain"
	"github.com/igomezal/horizonte/internal/tax/es"
)

// Tax models.
const (
	TaxFlat = "flat" // flat effective rate on gross income
	TaxES   = "es"   // Spanish IRPF with withholding and a June settlement
)

// TaxPerson is one person's IRPF for a year (amounts nominal).
type TaxPerson struct {
	Name      string
	Income    es.Income
	Breakdown es.Breakdown
}

// TaxYear is the IRPF of a calendar year.
type TaxYear struct {
	Year       int
	People     []TaxPerson
	Withheld   float64 // IRPF withheld during the year
	Settlement float64 // paid (or refunded if negative) the following June
	Partial    bool    // the plan started mid-year: no settlement is computed
}

// IRPF of the household for the year.
func (y TaxYear) IRPF() float64 {
	var t float64
	for _, p := range y.People {
		t += p.Breakdown.Tax
	}
	return t
}

type share struct {
	idx  int
	frac float64
}

// taxer applies Spanish IRPF during a simulation: monthly Social Security and
// IRPF withholding, yearly calculation per person and a settlement paid in
// June of the following year. By default brackets are indexed to inflation: the
// tax is computed in today's euros and converted back. With frozen brackets it
// is computed directly on nominal euros, so only the Social Security maximum
// base rises with inflation.
type taxer struct {
	region   es.Region
	people   []domain.Person
	pidx     map[int64]int
	year     int
	partial  bool
	frozen   bool
	deflator float64 // at the start of the year, for indexing

	acc      []es.Income // this year, nominal
	ssUsed   []float64   // salary already subject to Social Security this year
	monthSS  float64     // Social Security of the current month
	monthTax float64     // taxable gross of the current month (for withholding)
	rate     float64     // IRPF withholding rate on taxable gross income
	withheld float64
	pending  map[domain.Month]float64
	years    []TaxYear
}

func newTaxer(in Input) *taxer {
	t := &taxer{region: es.FindRegion(in.Assumptions.TaxRegion), people: in.People, pidx: map[int64]int{},
		frozen: in.Assumptions.FreezeIRPF, pending: map[domain.Month]float64{}}
	for i, p := range in.People {
		t.pidx[p.ID] = i
	}
	return t
}

// shares splits an owner's money: all to the owner, or equally among everyone
// for joint items.
func (t *taxer) shares(owner *int64) []share {
	if owner != nil {
		if i, ok := t.pidx[*owner]; ok {
			return []share{{i, 1}}
		}
	}
	n := len(t.people)
	out := make([]share, n)
	for i := range out {
		out[i] = share{i, 1 / float64(n)}
	}
	return out
}

func (t *taxer) real(x es.Income) es.Income {
	d := t.deflator
	x.Salary /= d
	x.Pension /= d
	x.PlanWithdrawal /= d
	x.SelfEmployed /= d
	x.Rental /= d
	x.Other /= d
	x.Savings /= d
	x.PlanContrib /= d
	return x
}

// compute returns a person's IRPF for a year of nominal income, in nominal euros.
func (t *taxer) compute(x es.Income) es.Breakdown {
	if t.frozen {
		return es.ComputeWithMaxBase(x, t.region, es.SocialSecurityMaxBase*t.deflator)
	}
	b := es.Compute(t.real(x), t.region)
	d := t.deflator
	b.SocialSecurity *= d
	b.WorkNet *= d
	b.GeneralBase *= d
	b.SavingsBase *= d
	b.Minimum *= d
	b.GeneralTax *= d
	b.SavingsTax *= d
	b.Tax *= d
	return b
}

// beginYear resets the accumulators and sets the withholding rate from the
// income expected this year (expected[i] per person, nominal).
func (t *taxer) beginYear(m domain.Month, first bool, deflator float64, expected []es.Income) {
	t.year, t.partial, t.deflator = m.Year, first && m.Month != 1, deflator
	t.acc = make([]es.Income, len(t.people))
	t.ssUsed = make([]float64, len(t.people))
	t.withheld = 0
	var tax, gross float64
	for i, e := range expected {
		e.Age = t.people[i].AgeAt(domain.Month{Year: m.Year, Month: 12})
		tax += t.compute(e).Tax
		gross += e.Salary + e.Pension + e.SelfEmployed + e.Rental + e.Other
	}
	t.rate = 0
	if gross > 0 {
		t.rate = tax / gross
	}
}

// addIncome records an income stream's payment this month.
func (t *taxer) addIncome(kind domain.IncomeKind, owner *int64, amount float64) {
	if amount == 0 {
		return
	}
	for _, s := range t.shares(owner) {
		v := amount * s.frac
		p := &t.acc[s.idx]
		switch kind {
		case domain.IncomeSalary:
			p.Salary += v
			capLeft := math.Max(0, es.SocialSecurityMaxBase*t.deflator-t.ssUsed[s.idx])
			base := math.Min(v, capLeft)
			t.ssUsed[s.idx] += base
			t.monthSS += base * es.SocialSecurityRate
		case domain.IncomePension:
			p.Pension += v
		case domain.IncomeSelfEmployed:
			p.SelfEmployed += v
		case domain.IncomeRental:
			p.Rental += v
		default:
			p.Other += v
		}
		t.monthTax += v
	}
}

func (t *taxer) addSavings(owner *int64, amount float64) {
	for _, s := range t.shares(owner) {
		t.acc[s.idx].Savings += amount * s.frac
	}
}

func (t *taxer) addPlanWithdrawal(owner *int64, amount float64) {
	for _, s := range t.shares(owner) {
		t.acc[s.idx].PlanWithdrawal += amount * s.frac
	}
}

func (t *taxer) addPlanContrib(owner *int64, amount float64) {
	for _, s := range t.shares(owner) {
		t.acc[s.idx].PlanContrib += amount * s.frac
	}
}

// monthCharge returns this month's Social Security plus IRPF withholding plus
// any settlement due, and resets the month's counters.
func (t *taxer) monthCharge(m domain.Month) float64 {
	withhold := t.rate * t.monthTax
	t.withheld += withhold
	charge := t.monthSS + withhold + t.pending[m]
	delete(t.pending, m)
	t.monthSS, t.monthTax = 0, 0
	return charge
}

// endYear computes the year's IRPF and schedules the settlement for June.
func (t *taxer) endYear() {
	y := TaxYear{Year: t.year, Withheld: t.withheld, Partial: t.partial}
	for i, p := range t.people {
		inc := t.acc[i]
		inc.Age = p.AgeAt(domain.Month{Year: t.year, Month: 12})
		y.People = append(y.People, TaxPerson{Name: p.Name, Income: inc, Breakdown: t.compute(inc)})
	}
	if !t.partial {
		y.Settlement = y.IRPF() - t.withheld
		t.pending[domain.Month{Year: t.year + 1, Month: 6}] += y.Settlement
	}
	t.years = append(t.years, y)
}

// expectedYear estimates each person's income for a year from the streams
// active in month m (full yearly amounts).
func (t *taxer) expectedYear(in Input, r domain.Resolved, m domain.Month, tIdx int, infl float64) []es.Income {
	out := make([]es.Income, len(t.people))
	for _, inc := range in.Incomes {
		if !r.ActiveIn(inc.Start, inc.End, inc.StartMS, inc.EndMS, m) {
			continue
		}
		v := inc.Annual().Euros() * streamGrowth(inc.AnnualGrowth, tIdx, infl)
		for _, s := range t.shares(inc.OwnerID) {
			x := v * s.frac
			switch inc.Kind {
			case domain.IncomeSalary:
				out[s.idx].Salary += x
			case domain.IncomePension:
				out[s.idx].Pension += x
			case domain.IncomeSelfEmployed:
				out[s.idx].SelfEmployed += x
			case domain.IncomeRental:
				out[s.idx].Rental += x
			default:
				out[s.idx].Other += x
			}
		}
	}
	return out
}

func validTaxRegion(key string) bool {
	for _, r := range es.Regions {
		if r.Key == key {
			return true
		}
	}
	return false
}

// TaxRegions lists the regional scales available.
func TaxRegions() []string {
	out := make([]string, len(es.Regions))
	for i, r := range es.Regions {
		out[i] = r.Key
	}
	return out
}
