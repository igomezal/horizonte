package domain

import "math"

// PrepaymentFrequency says how often an early repayment is made.
type PrepaymentFrequency string

const (
	PrepayOnce    PrepaymentFrequency = "once"
	PrepayMonthly PrepaymentFrequency = "monthly"
	PrepayYearly  PrepaymentFrequency = "yearly"
)

// PrepaymentFrequencies lists the frequencies in display order.
var PrepaymentFrequencies = []PrepaymentFrequency{PrepayOnce, PrepayMonthly, PrepayYearly}

// PrepaymentMode is what an early repayment reduces: the Spanish choice
// between "reducir plazo" and "reducir cuota".
type PrepaymentMode string

const (
	// ReduceTerm keeps the instalment, so the debt ends earlier.
	ReduceTerm PrepaymentMode = "term"
	// ReducePayment keeps the end date and lowers the instalment.
	ReducePayment PrepaymentMode = "payment"
)

// PrepaymentModes lists the modes in display order.
var PrepaymentModes = []PrepaymentMode{ReduceTerm, ReducePayment}

// Prepayment is an extra repayment of a debt's principal (amortización
// anticipada), once or on a schedule.
type Prepayment struct {
	ID        int64
	PlanID    int64
	DebtID    int64
	Amount    Cents // per payment, nominal
	Frequency PrepaymentFrequency
	Mode      PrepaymentMode
	FeeRate   float64 // commission charged on the amount repaid, e.g. 0.0025
	// Start is the month of a one-off payment, or when recurring payments
	// begin; End is the last month of recurring payments. Milestones override.
	Start, End     Month
	StartMS, EndMS *int64
	// RedirectAccountID receives payments that fall due after the debt is
	// paid off. Nil: they are simply not made.
	RedirectAccountID *int64
}

// Validate checks the prepayment. Field names match the form.
func (p Prepayment) Validate() Errors {
	errs := Errors{}
	if p.DebtID == 0 {
		errs.Add("debt", ErrRequired)
	}
	if p.Amount <= 0 {
		errs.Add("amount", ErrRequired)
	}
	if !oneOf(p.Frequency, PrepaymentFrequencies) {
		errs.Add("frequency", ErrChoice)
	}
	if !oneOf(p.Mode, PrepaymentModes) {
		errs.Add("mode", ErrChoice)
	}
	rate := p.FeeRate
	validateRate("fee", &rate, 0, 0.05, errs)
	if p.Frequency == PrepayOnce && p.Start.IsZero() && p.StartMS == nil {
		errs.Add("start", ErrRequired)
	}
	if p.Frequency != PrepayOnce && p.StartMS == nil && p.EndMS == nil {
		validateSpan(p.Start, p.End, errs)
	}
	return errs
}

// DueIn returns whether a payment falls in month now. planStart anchors yearly
// payments that have no start of their own; one-off payments dated before the
// plan starts never happen.
func (p Prepayment) DueIn(r Resolved, now, planStart Month) bool {
	if p.Frequency == PrepayOnce {
		start, _, ok := r.Window(p.Start, Month{}, p.StartMS, nil)
		return ok && !start.IsZero() && start == now && !now.Before(planStart)
	}
	start, end, ok := r.Window(p.Start, p.End, p.StartMS, p.EndMS)
	if !ok || !Active(start, end, now) {
		return false
	}
	if p.Frequency == PrepayYearly {
		anchor := planStart.Month
		if !start.IsZero() {
			anchor = start.Month
		}
		return now.Month == anchor
	}
	return true
}

// Annuity returns the fixed monthly payment that repays balance over n months
// at the nominal annual rate (French amortisation).
func Annuity(balance, annualRate float64, n int) float64 {
	if n <= 0 {
		return balance
	}
	r := annualRate / 12
	if r == 0 {
		return balance / float64(n)
	}
	return balance * r / (1 - math.Pow(1+r, -float64(n)))
}
