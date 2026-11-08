package backup

import (
	"strings"
	"testing"

	"github.com/igomezal/horizonte/internal/engine"
)

func TestMonteCarloModelRoundTrips(t *testing.T) {
	snap := fullSnapshot()
	snap.Assumptions.MCModel = engine.MCBootstrap
	if _, got := roundTrip(t, snap); got.Assumptions.MCModel != engine.MCBootstrap {
		t.Errorf("model = %q", got.Assumptions.MCModel)
	}
}

func mcModelDoc(field string) string {
	return `{"format":"horizonte/plan","version":1,"plan":{"name":"Mano","people":[{"id":1,"name":"Eva","birth":"1980-01","life_expectancy":90}],
		"assumptions":{"inflation":0.02,"tax_rate":0.2` + field + `}}}`
}

func TestFilesWithoutMonteCarloModelGetTheDefault(t *testing.T) {
	entries, err := Read(strings.NewReader(mcModelDoc("")), now)
	if err != nil {
		t.Fatal(err)
	}
	if got := entries[0].Snapshot.Assumptions.MCModel; got != engine.MCParametric {
		t.Errorf("model = %q, want the default", got)
	}
}

func TestUnknownMonteCarloModelIsRejected(t *testing.T) {
	if _, err := Read(strings.NewReader(mcModelDoc(`,"monte_carlo_model":"magic"`)), now); err == nil {
		t.Error("an unknown model should not import")
	}
}
