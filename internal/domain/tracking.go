package domain

import "time"

// minTrackingYear is the earliest year an entry can be recorded for. A year
// mistyped as 0026 would otherwise stretch the chart across two millennia.
const minTrackingYear = 1970

// NetWorthEntry is what the household really owned and owed in a month, in
// nominal euros.
type NetWorthEntry struct {
	Month    Month
	Balances map[AccountKind]Cents // kinds with no money can be missing
	Debt     Cents
}

// Assets is the total of the balances.
func (e NetWorthEntry) Assets() Cents {
	var total Cents
	for _, v := range e.Balances {
		total += v
	}
	return total
}

// NetWorth is the assets minus the debt.
func (e NetWorthEntry) NetWorth() Cents { return e.Assets() - e.Debt }

// BalanceField is the form field (and import field) of a kind's balance.
func BalanceField(k AccountKind) string { return "balance_" + string(k) }

// Validate checks the entry. now is the current month: an entry records what
// has already happened.
func (e NetWorthEntry) Validate(now Month) Errors {
	errs := Errors{}
	switch {
	case e.Month.IsZero():
		errs.Add("month", ErrRequired)
	case now.Before(e.Month):
		errs.Add("month", ErrFuture)
	case e.Month.Year < minTrackingYear:
		errs.Add("month", ErrRange)
	}
	for k, v := range e.Balances {
		if !oneOf(k, AccountKinds) {
			errs.Add(BalanceField(k), ErrChoice)
		}
		validateNonNegative(BalanceField(k), v, errs)
	}
	validateNonNegative("debt", e.Debt, errs)
	return errs
}

// NetWorthPoint is the projected net worth at a month, in nominal euros.
type NetWorthPoint struct {
	Month    Month
	NetWorth Cents
}

// NetWorthBaseline is a plan's projected path, saved so that later months can
// be compared with what was expected at the time. Points are in month order.
type NetWorthBaseline struct {
	PlanName string
	SavedOn  time.Time // a date; only year, month and day are kept
	Points   []NetWorthPoint
}

// NetWorthComparison is one recorded month against the baseline.
type NetWorthComparison struct {
	Month       Month
	Actual      Cents
	HasBaseline bool // the baseline covers this month
	Baseline    Cents
	Diff        Cents // Actual - Baseline; positive means ahead of the plan
}

// CompareNetWorth compares each entry with the baseline, which may be nil.
// The result is in the order of entries.
func CompareNetWorth(entries []NetWorthEntry, b *NetWorthBaseline) []NetWorthComparison {
	planned := map[Month]Cents{}
	if b != nil {
		for _, p := range b.Points {
			planned[p.Month] = p.NetWorth
		}
	}
	out := make([]NetWorthComparison, 0, len(entries))
	for _, e := range entries {
		c := NetWorthComparison{Month: e.Month, Actual: e.NetWorth()}
		if v, ok := planned[e.Month]; ok {
			c.HasBaseline, c.Baseline, c.Diff = true, v, c.Actual-v
		}
		out = append(out, c)
	}
	return out
}
