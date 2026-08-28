package dr

import (
	"context"
	"testing"
)

func TestMeasureRPOZeroWhenEverythingReadsBack(t *testing.T) {
	acked := AcknowledgedLedgerState{"k1": "v1", "k2": "v2", "k3": "v3"}
	read := func(key string) (string, bool) {
		v, ok := acked[key]
		return v, ok
	}
	rpo := measureRPO(acked, read)
	if !rpo.Zero() {
		t.Fatalf("expected zero RPO when every acknowledged key reads back correctly, got %+v", rpo)
	}
	if rpo.AcknowledgedWrites != 3 {
		t.Fatalf("expected AcknowledgedWrites=3, got %d", rpo.AcknowledgedWrites)
	}
}

// TestMeasureRPODetectsRealDataLoss is the test the task explicitly
// asks for: a scenario with REAL, deliberately-injected lost writes,
// proving measureRPO reports nonzero RPO when data is actually
// missing -- not merely when raftlite's own safety property happens to
// hold. If this function's implementation were faked (e.g. hardcoded
// to always return LostWrites=0), this test would fail.
func TestMeasureRPODetectsRealDataLoss(t *testing.T) {
	acked := AcknowledgedLedgerState{"k1": "v1", "k2": "v2", "k3": "v3", "k4": "v4"}
	// A "post-failover leader" that lost k2 entirely and returns a
	// WRONG value for k4 -- two distinct real-loss shapes: missing, and
	// present-but-wrong.
	lossy := func(key string) (string, bool) {
		switch key {
		case "k2":
			return "", false
		case "k4":
			return "stale-value", true
		default:
			return acked[key], true
		}
	}
	rpo := measureRPO(acked, lossy)
	if rpo.Zero() {
		t.Fatal("expected measureRPO to report nonzero loss for a genuinely lossy read set")
	}
	if rpo.LostWrites != 2 {
		t.Fatalf("expected exactly 2 lost writes (k2 missing, k4 wrong), got %d: %v", rpo.LostWrites, rpo.LostKeys)
	}
	wantLost := map[string]bool{"k2": true, "k4": true}
	for _, k := range rpo.LostKeys {
		if !wantLost[k] {
			t.Fatalf("unexpected key reported lost: %s (lost=%v)", k, rpo.LostKeys)
		}
		delete(wantLost, k)
	}
	if len(wantLost) != 0 {
		t.Fatalf("expected k2 and k4 both reported lost, missing: %v", wantLost)
	}
}

func TestMeasureRPOHandlesEmptyLedger(t *testing.T) {
	rpo := measureRPO(AcknowledgedLedgerState{}, func(string) (string, bool) { return "", false })
	if !rpo.Zero() || rpo.AcknowledgedWrites != 0 {
		t.Fatalf("expected a zero-value result for an empty ledger, got %+v", rpo)
	}
}

func TestMeasureReplicationReportsPartialCatchUp(t *testing.T) {
	acked := AcknowledgedLedgerState{"k1": "v1", "k2": "v2", "k3": "v3"}
	provider := &stubRegionProvider{
		values: map[string]map[string]string{
			"region-caught-up": {"k1": "v1", "k2": "v2", "k3": "v3"},
			"region-behind":    {"k1": "v1", "k2": "v2"}, // missing k3
		},
	}

	caughtUp := measureReplication("region-caught-up", acked, provider)
	if !caughtUp.CaughtUp || caughtUp.MatchedEntries != 3 {
		t.Fatalf("expected the fully-replicated region to be CaughtUp with 3/3 matched, got %+v", caughtUp)
	}

	behind := measureReplication("region-behind", acked, provider)
	if behind.CaughtUp || behind.MatchedEntries != 2 {
		t.Fatalf("expected the partially-replicated region to report 2/3 matched and CaughtUp=false, got %+v", behind)
	}
}

func TestSnapshotTopologyIdentifiesLeaderAndFollowers(t *testing.T) {
	regions := []Region{{ID: "region-0"}, {ID: "region-1"}, {ID: "region-2"}}
	topo := snapshotTopology(regions, Region{ID: "region-1"})
	if topo.Leader != "region-1" {
		t.Fatalf("expected leader region-1, got %s", topo.Leader)
	}
	if len(topo.Followers) != 2 {
		t.Fatalf("expected 2 followers, got %v", topo.Followers)
	}
	for _, f := range topo.Followers {
		if f == "region-1" {
			t.Fatal("expected the leader to not also appear in Followers")
		}
	}
	if len(topo.Regions) != 3 {
		t.Fatalf("expected 3 total regions, got %v", topo.Regions)
	}
}

// stubRegionProvider is a minimal, directly-controllable RegionProvider
// for exercising measureReplication's per-region Read comparison
// deterministically. It only implements Read meaningfully; every other
// method panics because measureReplication (unlike RunQualification)
// never calls them.
type stubRegionProvider struct{ values map[string]map[string]string }

func (s *stubRegionProvider) Mode() string { panic("not used by measureReplication") }
func (s *stubRegionProvider) CreateRegions(ctx context.Context, n int) ([]Region, error) {
	panic("not used by measureReplication")
}
func (s *stubRegionProvider) CurrentLeader(ctx context.Context) (Region, error) {
	panic("not used by measureReplication")
}
func (s *stubRegionProvider) Write(ctx context.Context, key, value string) error {
	panic("not used by measureReplication")
}
func (s *stubRegionProvider) Read(regionID, key string) (string, bool) {
	v, ok := s.values[regionID][key]
	return v, ok
}
func (s *stubRegionProvider) Partition(regionID string) error {
	panic("not used by measureReplication")
}
func (s *stubRegionProvider) Heal(regionID string) error { panic("not used by measureReplication") }
func (s *stubRegionProvider) Destroy(ctx context.Context) error {
	panic("not used by measureReplication")
}
