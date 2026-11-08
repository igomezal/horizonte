package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/igomezal/horizonte/internal/domain"
)

var (
	oct2026 = domain.Month{Year: 2026, Month: 10}
	nov2026 = domain.Month{Year: 2026, Month: 11}
)

func openTracking(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func entry(m domain.Month, cash, debt domain.Cents) domain.NetWorthEntry {
	return domain.NetWorthEntry{Month: m, Debt: debt, Balances: map[domain.AccountKind]domain.Cents{domain.AccountCash: cash, domain.AccountInvestment: 5}}
}

func baseline(name string, nw domain.Cents, months ...domain.Month) domain.NetWorthBaseline {
	b := domain.NetWorthBaseline{PlanName: name, SavedOn: time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)}
	for _, m := range months {
		b.Points = append(b.Points, domain.NetWorthPoint{Month: m, NetWorth: nw})
	}
	return b
}

func TestNetWorthEntryRoundTrip(t *testing.T) {
	ctx := t.Context()
	s := openTracking(t)

	// Stored out of order, returned in month order.
	if err := s.AddNetWorthEntry(ctx, entry(nov2026, 100, 7)); err != nil {
		t.Fatal(err)
	}
	if err := s.AddNetWorthEntry(ctx, domain.NetWorthEntry{Month: oct2026, Debt: 3}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListNetWorthEntries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Month != oct2026 || got[1].Month != nov2026 {
		t.Fatalf("entries = %+v", got)
	}
	if got[0].Debt != 3 || len(got[0].Balances) != 0 {
		t.Errorf("entry without balances = %+v", got[0])
	}
	if got[1].Balances[domain.AccountCash] != 100 || got[1].Balances[domain.AccountInvestment] != 5 || got[1].NetWorth() != 98 {
		t.Errorf("entry = %+v", got[1])
	}

	// Editing replaces the balances, including ones that are now gone.
	edited := domain.NetWorthEntry{Month: nov2026, Debt: 1, Balances: map[domain.AccountKind]domain.Cents{domain.AccountPension: 9}}
	if err := s.UpdateNetWorthEntry(ctx, edited); err != nil {
		t.Fatal(err)
	}
	e, err := s.GetNetWorthEntry(ctx, nov2026)
	if err != nil {
		t.Fatal(err)
	}
	if e.Debt != 1 || len(e.Balances) != 1 || e.Balances[domain.AccountPension] != 9 {
		t.Errorf("after edit = %+v", e)
	}

	if err := s.DeleteNetWorthEntry(ctx, nov2026); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetNetWorthEntry(ctx, nov2026); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete: %v", err)
	}
	var orphans int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM net_worth_balances WHERE month = ?`, nov2026.String()).Scan(&orphans); err != nil || orphans != 0 {
		t.Errorf("balances left after delete: %d, %v", orphans, err)
	}
	if err := s.UpdateNetWorthEntry(ctx, edited); !errors.Is(err, ErrNotFound) {
		t.Errorf("update of a missing month: %v", err)
	}
	if err := s.DeleteNetWorthEntry(ctx, nov2026); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete of a missing month: %v", err)
	}
}

func TestOneNetWorthEntryPerMonth(t *testing.T) {
	ctx := t.Context()
	s := openTracking(t)
	if err := s.AddNetWorthEntry(ctx, entry(nov2026, 100, 0)); err != nil {
		t.Fatal(err)
	}
	if err := s.AddNetWorthEntry(ctx, entry(nov2026, 999, 0)); !errors.Is(err, ErrMonthExists) {
		t.Fatalf("second entry for the month: %v", err)
	}
	e, err := s.GetNetWorthEntry(ctx, nov2026)
	if err != nil {
		t.Fatal(err)
	}
	if e.Balances[domain.AccountCash] != 100 {
		t.Errorf("the first entry was changed: %+v", e)
	}
}

func TestHistorySurvivesPlanChanges(t *testing.T) {
	ctx := t.Context()
	s := openTracking(t)
	first, err := s.CurrentPlan(ctx, "plan")
	if err != nil {
		t.Fatal(err)
	}
	copied, err := s.CopyPlan(ctx, first, "copy")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddNetWorthEntry(ctx, entry(nov2026, 100, 0)); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveNetWorthBaseline(ctx, baseline("plan", 1, nov2026)); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePlan(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePlan(ctx, copied); !errors.Is(err, ErrLastPlan) {
		t.Fatalf("delete last plan: %v", err)
	}
	entries, err := s.ListNetWorthEntries(ctx)
	if err != nil || len(entries) != 1 {
		t.Errorf("entries after deleting a plan = %+v, %v", entries, err)
	}
	b, err := s.GetNetWorthBaseline(ctx)
	if err != nil || b == nil || b.PlanName != "plan" {
		t.Errorf("baseline after deleting its plan = %+v, %v", b, err)
	}
}

func TestBaselineReplacement(t *testing.T) {
	ctx := t.Context()
	s := openTracking(t)
	if b, err := s.GetNetWorthBaseline(ctx); err != nil || b != nil {
		t.Fatalf("no baseline yet: %+v, %v", b, err)
	}
	if err := s.SaveNetWorthBaseline(ctx, baseline("first", 1, oct2026, nov2026)); err != nil {
		t.Fatal(err)
	}
	second := baseline("second", 2, nov2026)
	second.SavedOn = time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	if err := s.SaveNetWorthBaseline(ctx, second); err != nil {
		t.Fatal(err)
	}
	b, err := s.GetNetWorthBaseline(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if b.PlanName != "second" || b.SavedOn.Format(dateLayout) != "2026-12-01" {
		t.Errorf("baseline = %+v", b)
	}
	if len(b.Points) != 1 || b.Points[0] != (domain.NetWorthPoint{Month: nov2026, NetWorth: 2}) {
		t.Errorf("points = %+v, want only the new baseline's", b.Points)
	}
}

func TestMergeNeverOverwrites(t *testing.T) {
	ctx := t.Context()
	s := openTracking(t)
	if err := s.AddNetWorthEntry(ctx, entry(nov2026, 100, 0)); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveNetWorthBaseline(ctx, baseline("mine", 1, nov2026)); err != nil {
		t.Fatal(err)
	}

	imported := baseline("theirs", 9, oct2026)
	added, baselineAdded, err := s.MergeNetWorthHistory(ctx, []domain.NetWorthEntry{entry(oct2026, 50, 0), entry(nov2026, 999, 0)}, &imported)
	if err != nil {
		t.Fatal(err)
	}
	if added != 1 || baselineAdded {
		t.Errorf("added = %d, baseline added = %v", added, baselineAdded)
	}
	e, _ := s.GetNetWorthEntry(ctx, nov2026)
	if e.Balances[domain.AccountCash] != 100 {
		t.Errorf("an existing month was overwritten: %+v", e)
	}
	if e, err := s.GetNetWorthEntry(ctx, oct2026); err != nil || e.Balances[domain.AccountCash] != 50 {
		t.Errorf("the missing month wasn't added: %+v, %v", e, err)
	}
	if b, _ := s.GetNetWorthBaseline(ctx); b.PlanName != "mine" {
		t.Errorf("the existing baseline was replaced: %+v", b)
	}

	// With none saved, the file's baseline is taken.
	empty := openTracking(t)
	if _, baselineAdded, err := empty.MergeNetWorthHistory(ctx, nil, &imported); err != nil || !baselineAdded {
		t.Fatalf("merge = %v, %v", baselineAdded, err)
	}
	if b, _ := empty.GetNetWorthBaseline(ctx); b == nil || b.PlanName != "theirs" {
		t.Errorf("baseline = %+v", b)
	}
}
