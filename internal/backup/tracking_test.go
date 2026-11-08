package backup

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/igomezal/horizonte/internal/domain"
)

var nowMonth = domain.Month{Year: 2026, Month: 11}

func TestTrackingFromIsAbsentWithoutHistory(t *testing.T) {
	if TrackingFrom(nil, nil) != nil {
		t.Error("want no tracking section with no history")
	}
}

func TestTrackingRoundTrip(t *testing.T) {
	saved := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	entries := []domain.NetWorthEntry{{Month: nowMonth, Debt: 12_50, Balances: map[domain.AccountKind]domain.Cents{domain.AccountCash: 100_00}}}
	base := &domain.NetWorthBaseline{PlanName: "Plan", SavedOn: saved, Points: []domain.NetWorthPoint{
		{Month: nowMonth, NetWorth: -5_25}, {Month: nowMonth.AddMonths(1), NetWorth: 7_00}}}

	b := Bundle{Format: BundleFormat, Version: Version, Tracking: TrackingFrom(entries, base)}
	var buf bytes.Buffer
	if err := Write(&buf, b); err != nil {
		t.Fatal(err)
	}
	got, err := ReadTracking(buf.Bytes(), nowMonth)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 1 || got.Entries[0].Month != nowMonth || got.Entries[0].Debt != 12_50 || got.Entries[0].Balances[domain.AccountCash] != 100_00 {
		t.Errorf("entries = %+v", got.Entries)
	}
	if got.Baseline == nil || got.Baseline.PlanName != "Plan" || !got.Baseline.SavedOn.Equal(saved) ||
		len(got.Baseline.Points) != 2 || got.Baseline.Points[0].NetWorth != -5_25 {
		t.Errorf("baseline = %+v", got.Baseline)
	}
}

func TestReadTrackingFilesWithoutHistory(t *testing.T) {
	for _, data := range []string{
		`{"format":"horizonte/plan","version":1,"plan":{"name":"x"}}`,
		`{"format":"horizonte/plans","version":1,"plans":[{"id":1,"name":"x"}]}`,
		// A single-plan file carries no history, even if someone adds it by hand.
		`{"format":"horizonte/plan","version":1,"plan":{"name":"x"},"tracking":{"entries":[{"month":"2026-11","debt":1}]}}`,
	} {
		got, err := ReadTracking([]byte(data), nowMonth)
		if err != nil || got != nil {
			t.Errorf("%s: %+v, %v", data, got, err)
		}
	}
}

func TestReadTrackingRejectsBadHistory(t *testing.T) {
	point := `{"month":"2026-11","net_worth":1}`
	cases := []struct{ name, tracking, field string }{
		{"bad month", `{"entries":[{"month":"nope","debt":1}]}`, "month"},
		{"missing month", `{"entries":[{"debt":1}]}`, "month"},
		{"future month", `{"entries":[{"month":"2026-12","debt":1}]}`, "month"},
		{"repeated month", `{"entries":[{"month":"2026-10","debt":1},{"month":"2026-10","debt":2}]}`, "month"},
		{"negative debt", `{"entries":[{"month":"2026-10","debt":-1}]}`, "debt"},
		{"negative balance", `{"entries":[{"month":"2026-10","balances":{"cash":-1},"debt":0}]}`, "balance_cash"},
		{"unknown kind", `{"entries":[{"month":"2026-10","balances":{"gold":1},"debt":0}]}`, "balance_gold"},
		{"no plan name", `{"baseline":{"saved_on":"2026-11-09","points":[` + point + `]}}`, "plan_name"},
		{"bad date", `{"baseline":{"plan_name":"x","saved_on":"yesterday","points":[` + point + `]}}`, "saved_on"},
		{"no points", `{"baseline":{"plan_name":"x","saved_on":"2026-11-09","points":[]}}`, "points"},
		{"points out of order", `{"baseline":{"plan_name":"x","saved_on":"2026-11-09","points":[` + point + `,` + point + `]}}`, "points[1].month"},
	}
	for _, c := range cases {
		data := `{"format":"horizonte/plans","version":1,"plans":[],"tracking":` + c.tracking + `}`
		_, err := ReadTracking([]byte(data), nowMonth)
		var ie *InvalidError
		if !errors.As(err, &ie) || ie.Section != trackingSection || ie.Field != c.field {
			t.Errorf("%s: err = %v, want invalid field %s", c.name, err, c.field)
		}
	}
}

func TestReadTrackingCapsRecords(t *testing.T) {
	entries := make([]TrackingEntry, maxTrackingEntries+1)
	data, err := json.Marshal(Bundle{Format: BundleFormat, Version: Version, Tracking: &Tracking{Entries: entries}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTracking(data, nowMonth); !errors.Is(err, ErrTooLarge) {
		t.Errorf("err = %v", err)
	}
}

// Files written before tracking existed keep importing.
func TestOldBundleStillReads(t *testing.T) {
	data := `{"format":"horizonte/plans","version":1,"plans":[{"id":1,"name":"x","assumptions":{"inflation":0.02,"tax_model":"flat","tax_rate":0.2}}]}`
	if _, err := Read(strings.NewReader(data), nowMonth); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadTracking([]byte(data), nowMonth); err != nil || got != nil {
		t.Errorf("tracking = %+v, %v", got, err)
	}
}
