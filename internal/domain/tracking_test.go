package domain

import "testing"

func TestCompareNetWorth(t *testing.T) {
	oct, nov := Month{Year: 2026, Month: 10}, Month{Year: 2026, Month: 11}
	entries := []NetWorthEntry{
		{Month: oct, Balances: map[AccountKind]Cents{AccountCash: 1000_00, AccountInvestment: 500_00}, Debt: 200_00}, // 1.300 €
		{Month: nov, Balances: map[AccountKind]Cents{AccountCash: 100_00}, Debt: 400_00},                             // -300 €
	}
	base := &NetWorthBaseline{Points: []NetWorthPoint{{Month: nov, NetWorth: 50_00}}}

	got := CompareNetWorth(entries, base)
	if len(got) != 2 {
		t.Fatalf("got %d rows", len(got))
	}
	if got[0].Actual != 1300_00 || got[0].HasBaseline {
		t.Errorf("month outside the baseline = %+v", got[0])
	}
	if !got[1].HasBaseline || got[1].Actual != -300_00 || got[1].Baseline != 50_00 || got[1].Diff != -350_00 {
		t.Errorf("month with a baseline = %+v", got[1])
	}

	for _, c := range CompareNetWorth(entries, nil) {
		if c.HasBaseline || c.Diff != 0 {
			t.Errorf("without a baseline = %+v", c)
		}
	}
}

func TestNetWorthEntryValidate(t *testing.T) {
	now := Month{Year: 2026, Month: 11}
	cases := []struct {
		name  string
		e     NetWorthEntry
		field string // "" means valid
	}{
		{"this month", NetWorthEntry{Month: now, Balances: map[AccountKind]Cents{AccountCash: 1}}, ""},
		{"past month", NetWorthEntry{Month: Month{Year: 2020, Month: 1}}, ""},
		{"missing month", NetWorthEntry{}, "month"},
		{"future month", NetWorthEntry{Month: Month{Year: 2026, Month: 12}}, "month"},
		{"mistyped year", NetWorthEntry{Month: Month{Year: 26, Month: 1}}, "month"},
		{"negative balance", NetWorthEntry{Month: now, Balances: map[AccountKind]Cents{AccountCash: -1}}, "balance_cash"},
		{"unknown kind", NetWorthEntry{Month: now, Balances: map[AccountKind]Cents{"gold": 1}}, "balance_gold"},
		{"negative debt", NetWorthEntry{Month: now, Debt: -1}, "debt"},
	}
	for _, c := range cases {
		errs := c.e.Validate(now)
		if c.field == "" && len(errs) != 0 {
			t.Errorf("%s: errors %v", c.name, errs)
		}
		if _, ok := errs[c.field]; c.field != "" && !ok {
			t.Errorf("%s: errors %v, want one on %s", c.name, errs, c.field)
		}
	}
}
