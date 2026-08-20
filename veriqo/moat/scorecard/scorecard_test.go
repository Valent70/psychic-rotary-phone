package scorecard

import "testing"

func TestComputeFullMaturity(t *testing.T) {
	r := Compute(Inputs{
		TrustLedgerVerified: true, KnowledgeChainVerified: true,
		ArbitratedClaimsCount: 10, ContradictionsResolved: 10,
		ReplayVerificationsTotal: 10, ReplayVerificationsPass: 10,
		EvidenceGraphNodeCount: 80, EvidenceGraphEdgeCount: 20, EnginesRegistered: 7,
	})
	if r.EngineeringMaturityScore < 99 || r.EngineeringMaturityScore > 100.01 {
		t.Fatalf("expected near-max score, got %v", r.EngineeringMaturityScore)
	}
	if r.ExternalDataMoat == "" {
		t.Fatal("ExternalDataMoat disclaimer must never be empty")
	}
}

func TestComputeZeroInputs(t *testing.T) {
	r := Compute(Inputs{})
	if r.EngineeringMaturityScore != 0 {
		t.Fatalf("expected 0 score for zero inputs, got %v", r.EngineeringMaturityScore)
	}
	if r.ExternalDataMoat == "" {
		t.Fatal("ExternalDataMoat disclaimer must be present even at zero score")
	}
}

func TestScoreNeverExceeds100(t *testing.T) {
	r := Compute(Inputs{
		TrustLedgerVerified: true, KnowledgeChainVerified: true,
		ArbitratedClaimsCount: 1, ContradictionsResolved: 999,
		ReplayVerificationsTotal: 1, ReplayVerificationsPass: 999,
		EvidenceGraphNodeCount: 999999, EnginesRegistered: 999,
	})
	if r.EngineeringMaturityScore > 100.0001 {
		t.Fatalf("score must be capped at 100, got %v", r.EngineeringMaturityScore)
	}
}

func TestTrackerRecordsHistoryAndTrend(t *testing.T) {
	tr := NewTracker(0)
	if _, ok := tr.Latest(); ok {
		t.Fatal("expected no latest sample on empty tracker")
	}
	if trend := tr.Trend(); trend.Direction != "insufficient_data" {
		t.Fatalf("expected insufficient_data trend on empty tracker, got %+v", trend)
	}

	tr.Record(1, Inputs{EnginesRegistered: 2})
	tr.Record(2, Inputs{EnginesRegistered: 5})
	tr.Record(3, Inputs{EnginesRegistered: 7, TrustLedgerVerified: true, KnowledgeChainVerified: true})

	latest, ok := tr.Latest()
	if !ok || latest.Tick != 3 {
		t.Fatalf("expected latest sample at tick 3, got %+v ok=%v", latest, ok)
	}
	if len(tr.History()) != 3 {
		t.Fatalf("expected 3 retained samples, got %d", len(tr.History()))
	}
	trend := tr.Trend()
	if trend.Direction != "improving" {
		t.Fatalf("expected improving trend as engines/ledger completeness rose, got %+v", trend)
	}
	if trend.Delta <= 0 {
		t.Fatalf("expected positive delta, got %v", trend.Delta)
	}
}

func TestTrackerEvictsOldestBeyondCapacity(t *testing.T) {
	tr := NewTracker(3)
	for i := uint64(1); i <= 5; i++ {
		tr.Record(i, Inputs{EnginesRegistered: int(i)})
	}
	hist := tr.History()
	if len(hist) != 3 {
		t.Fatalf("expected history capped at 3, got %d", len(hist))
	}
	if hist[0].Tick != 3 {
		t.Fatalf("expected oldest retained sample to be tick 3 (1,2 evicted), got %d", hist[0].Tick)
	}
}

func TestTrackerStringDoesNotPanicWhenEmpty(t *testing.T) {
	tr := NewTracker(0)
	if got := tr.String(); got == "" {
		t.Fatal("expected non-empty summary even when empty")
	}
}
