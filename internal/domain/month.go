// Package domain holds the financial planning model: people, accounts,
// income, expenses and debts, plus the rules that keep them valid.
package domain

import (
	"fmt"
	"time"
)

// Month is a calendar month. The zero value means "not set" (for example an
// income with no end date).
type Month struct {
	Year  int
	Month int // 1-12
}

// ParseMonth parses "YYYY-MM" (the value of an <input type="month">).
// An empty string returns the zero Month.
func ParseMonth(s string) (Month, error) {
	if s == "" {
		return Month{}, nil
	}
	t, err := time.Parse("2006-01", s)
	if err != nil {
		return Month{}, fmt.Errorf("invalid month %q", s)
	}
	return Month{Year: t.Year(), Month: int(t.Month())}, nil
}

// MonthOf returns the month containing t.
func MonthOf(t time.Time) Month { return Month{Year: t.Year(), Month: int(t.Month())} }

// IsZero reports whether the month is unset.
func (m Month) IsZero() bool { return m.Year == 0 }

// String returns "YYYY-MM", or "" when unset.
func (m Month) String() string {
	if m.IsZero() {
		return ""
	}
	return fmt.Sprintf("%04d-%02d", m.Year, m.Month)
}

// Index returns a monotonically increasing month number, for comparisons and arithmetic.
func (m Month) Index() int { return m.Year*12 + m.Month - 1 }

// AddMonths returns m shifted by n months.
func (m Month) AddMonths(n int) Month {
	i := m.Index() + n
	return Month{Year: i / 12, Month: i%12 + 1}
}

// Before reports whether m is strictly before o.
func (m Month) Before(o Month) bool { return m.Index() < o.Index() }

// MonthsUntil returns the number of months from m to o (negative if o is earlier).
func (m Month) MonthsUntil(o Month) int { return o.Index() - m.Index() }

// Active reports whether a stream with the given start and end (either may be
// unset, meaning "open") is active in month now.
func Active(start, end, now Month) bool {
	if !start.IsZero() && now.Before(start) {
		return false
	}
	if !end.IsZero() && end.Before(now) {
		return false
	}
	return true
}
