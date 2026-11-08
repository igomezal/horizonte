package backup

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/igomezal/horizonte/internal/domain"
)

// Net worth history belongs to the household rather than to a plan, so it only
// travels in the all-plans bundle.

// Limits on a file's history: a century of months each.
const (
	maxTrackingEntries  = 1200
	maxBaselinePoints   = 1200
	trackingSection     = "tracking"
	trackingSavedLayout = "2006-01-02"
)

// Tracking is the household's net worth history in a bundle.
type Tracking struct {
	Entries  []TrackingEntry `json:"entries,omitempty"`
	Baseline *Baseline       `json:"baseline,omitempty"`
}

// TrackingEntry is one recorded month.
type TrackingEntry struct {
	Month    string             `json:"month"`
	Balances map[string]float64 `json:"balances"` // by account kind
	Debt     float64            `json:"debt"`
}

// Baseline is the saved projection.
type Baseline struct {
	PlanName string          `json:"plan_name"`
	SavedOn  string          `json:"saved_on"` // YYYY-MM-DD
	Points   []BaselinePoint `json:"points"`
}

// BaselinePoint is the projected net worth at a month.
type BaselinePoint struct {
	Month    string  `json:"month"`
	NetWorth float64 `json:"net_worth"`
}

// TrackingData is the validated history read from a bundle.
type TrackingData struct {
	Entries  []domain.NetWorthEntry
	Baseline *domain.NetWorthBaseline
}

// TrackingFrom builds a bundle's history, or nil if there is none to save.
func TrackingFrom(entries []domain.NetWorthEntry, b *domain.NetWorthBaseline) *Tracking {
	if len(entries) == 0 && b == nil {
		return nil
	}
	t := &Tracking{}
	for _, e := range entries {
		out := TrackingEntry{Month: month(e.Month), Balances: map[string]float64{}, Debt: eur(e.Debt)}
		for k, v := range e.Balances {
			out.Balances[string(k)] = eur(v)
		}
		t.Entries = append(t.Entries, out)
	}
	if b != nil {
		t.Baseline = &Baseline{PlanName: b.PlanName, SavedOn: b.SavedOn.Format(trackingSavedLayout)}
		for _, p := range b.Points {
			t.Baseline.Points = append(t.Baseline.Points, BaselinePoint{Month: month(p.Month), NetWorth: eur(p.NetWorth)})
		}
	}
	return t
}

// ReadTracking decodes and validates the history of a file Read has already
// accepted. A single-plan file, or a bundle without history, returns nil.
func ReadTracking(data []byte, now domain.Month) (*TrackingData, error) {
	var b Bundle
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, ErrNotPlan
	}
	if b.Format != BundleFormat || b.Tracking == nil {
		return nil, nil
	}
	if len(b.Tracking.Entries) > maxTrackingEntries {
		return nil, ErrTooLarge
	}
	out := &TrackingData{}
	seen := map[domain.Month]bool{}
	for _, x := range b.Tracking.Entries {
		m, err := parseMonth(trackingSection, x.Month, "month", x.Month)
		if err != nil {
			return nil, err
		}
		if seen[m] {
			return nil, &InvalidError{Section: trackingSection, Name: x.Month, Field: "month"}
		}
		seen[m] = true
		e := domain.NetWorthEntry{Month: m, Debt: domain.FromEuros(x.Debt), Balances: map[domain.AccountKind]domain.Cents{}}
		for k, v := range x.Balances {
			e.Balances[domain.AccountKind(k)] = domain.FromEuros(v)
		}
		if err := invalid(trackingSection, x.Month, e.Validate(now)); err != nil {
			return nil, err
		}
		out.Entries = append(out.Entries, e)
	}
	if b.Tracking.Baseline != nil {
		baseline, err := readBaseline(*b.Tracking.Baseline)
		if err != nil {
			return nil, err
		}
		out.Baseline = baseline
	}
	return out, nil
}

func readBaseline(x Baseline) (*domain.NetWorthBaseline, error) {
	if len(x.Points) > maxBaselinePoints {
		return nil, ErrTooLarge
	}
	fail := func(field string) error {
		return &InvalidError{Section: trackingSection, Name: x.PlanName, Field: field}
	}
	if n := len([]rune(x.PlanName)); n == 0 || n > 80 {
		return nil, fail("plan_name")
	}
	saved, err := time.Parse(trackingSavedLayout, x.SavedOn)
	if err != nil {
		return nil, fail("saved_on")
	}
	if len(x.Points) == 0 {
		return nil, fail("points")
	}
	out := &domain.NetWorthBaseline{PlanName: x.PlanName, SavedOn: saved}
	var prev domain.Month
	for i, p := range x.Points {
		m, err := domain.ParseMonth(p.Month)
		if err != nil || m.IsZero() || (i > 0 && !prev.Before(m)) {
			return nil, fail("points[" + strconv.Itoa(i) + "].month")
		}
		prev = m
		out.Points = append(out.Points, domain.NetWorthPoint{Month: m, NetWorth: domain.FromEuros(p.NetWorth)})
	}
	return out, nil
}
