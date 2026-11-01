package engine

import (
	"math"
	"strings"

	"github.com/igomezal/horizonte/internal/domain"
)

// WithdrawalStrategy decides how much is taken from savings each year once
// the strategy starts (normally at retirement).
type WithdrawalStrategy string

const (
	// WithdrawFixed spends exactly the planned expenses (the default).
	WithdrawFixed WithdrawalStrategy = "fixed"
	// WithdrawPercent takes Rate × savings every year.
	WithdrawPercent WithdrawalStrategy = "percent"
	// WithdrawRule4 takes Rate × savings in the first year, then the same
	// amount adjusted for inflation (Bengen's "4 % rule").
	WithdrawRule4 WithdrawalStrategy = "rule4"
	// WithdrawGuardrails starts like the 4 % rule and adjusts with inflation,
	// but cuts the withdrawal 10 % when the withdrawal rate rises 20 % above
	// Rate and raises it 10 % when it falls 20 % below (Guyton-Klinger).
	WithdrawGuardrails WithdrawalStrategy = "guardrails"
)

// WithdrawalStrategies lists the strategies in display order.
var WithdrawalStrategies = []WithdrawalStrategy{WithdrawFixed, WithdrawPercent, WithdrawRule4, WithdrawGuardrails}

// WithdrawalSettings configure the strategy. Planned expenses are scaled so
// that spending matches income after tax plus the withdrawal, minus debt
// payments, within [Floor, Ceiling] of the plan.
type WithdrawalSettings struct {
	Strategy WithdrawalStrategy
	StartMS  *int64  // milestone from which the strategy applies
	Rate     float64 // withdrawal rate, e.g. 0.04
	Floor    float64 // minimum spending as a share of planned expenses, e.g. 0.7
	Ceiling  float64 // maximum spending as a share of planned expenses, e.g. 1.5
}

// DefaultWithdrawal keeps the planned expenses.
func DefaultWithdrawal() WithdrawalSettings {
	return WithdrawalSettings{Strategy: WithdrawFixed, Rate: 0.04, Floor: 0.7, Ceiling: 1.5}
}

// DefaultOrder is the withdrawal order: most liquid first, pension plan last.
// Property is never sold.
var DefaultOrder = []domain.AccountKind{
	domain.AccountCash, domain.AccountDeposit, domain.AccountInvestment,
	domain.AccountOther, domain.AccountPension,
}

// ParseOrder reads a comma-separated withdrawal order; it returns nil unless
// it is a permutation of DefaultOrder.
func ParseOrder(s string) []domain.AccountKind {
	parts := strings.Split(s, ",")
	if len(parts) != len(DefaultOrder) {
		return nil
	}
	seen := map[domain.AccountKind]bool{}
	var out []domain.AccountKind
	for _, p := range parts {
		k := domain.AccountKind(strings.TrimSpace(p))
		valid := false
		for _, d := range DefaultOrder {
			valid = valid || d == k
		}
		if !valid || seen[k] {
			return nil
		}
		seen[k] = true
		out = append(out, k)
	}
	return out
}

// FormatOrder writes an order as ParseOrder reads it.
func FormatOrder(order []domain.AccountKind) string {
	parts := make([]string, len(order))
	for i, k := range order {
		parts[i] = string(k)
	}
	return strings.Join(parts, ",")
}

// validate adds errors for the withdrawal settings (form field names).
func (w WithdrawalSettings) validate(errs domain.Errors) {
	valid := false
	for _, s := range WithdrawalStrategies {
		valid = valid || s == w.Strategy
	}
	if !valid {
		errs.Add("wd_strategy", domain.ErrChoice)
		return
	}
	if w.Strategy == WithdrawFixed {
		return
	}
	if w.StartMS == nil {
		errs.Add("wd_start", domain.ErrRequired)
	}
	if w.Rate < 0.01 || w.Rate > 0.12 {
		errs.Add("wd_rate", domain.ErrRange)
	}
	if w.Floor < 0.3 || w.Floor > 1 {
		errs.Add("wd_floor", domain.ErrRange)
	}
	if w.Ceiling < 1 || w.Ceiling > 3 {
		errs.Add("wd_ceiling", domain.ErrRange)
	}
}

// spendPolicy applies a withdrawal strategy during a simulation. It keeps the
// yearly withdrawal and the factor applied to planned expenses.
type spendPolicy struct {
	s          WithdrawalSettings
	started    bool
	startPrice float64 // price index when the strategy started
	nextT      int     // month of the next yearly review
	withdrawal float64 // yearly withdrawal target, nominal
	factor     float64
}

// yearInputs are the plan's yearly amounts at a review, nominal.
type yearInputs struct {
	netIncome, expenses, debt float64
}

// update returns the spending factor for month t. The withdrawal is set when
// the start milestone arrives and reviewed every 12 months after that.
func (p *spendPolicy) update(t int, m domain.Month, r domain.Resolved, savings float64, price []float64, year func() yearInputs) float64 {
	if p.s.Strategy == WithdrawFixed || p.s.StartMS == nil {
		return 1
	}
	if start, ok := r[*p.s.StartMS]; !ok || m.Before(start) {
		return 1
	}
	switch {
	case !p.started:
		p.started, p.startPrice, p.nextT = true, price[t], t+12
		p.withdrawal = p.s.Rate * math.Max(0, savings)
	case t == p.nextT:
		p.nextT += 12
		switch p.s.Strategy {
		case WithdrawPercent:
			p.withdrawal = p.s.Rate * math.Max(0, savings)
		case WithdrawRule4:
			p.withdrawal = p.withdrawal * price[t] / price[t-12]
		case WithdrawGuardrails:
			p.withdrawal *= price[t] / price[t-12]
			if savings > 0 {
				switch rate := p.withdrawal / savings; {
				case rate > p.s.Rate*1.2:
					p.withdrawal *= 0.9
				case rate < p.s.Rate*0.8:
					p.withdrawal *= 1.1
				}
			}
		}
	default:
		return p.factor
	}
	y := year()
	if y.expenses <= 0 {
		p.factor = 1
		return p.factor
	}
	f := (y.netIncome + p.withdrawal - y.debt) / y.expenses
	p.factor = math.Min(p.s.Ceiling, math.Max(p.s.Floor, f))
	return p.factor
}
