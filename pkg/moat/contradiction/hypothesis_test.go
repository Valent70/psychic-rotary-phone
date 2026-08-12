package contradiction

import "testing"

func TestRankHypothesesOrdersAllCandidates(t *testing.T) {
	e := NewArbitrationEngine()
	claim := "VESSEL:1|FLAG"
	must(t, e.Observe(RawObservation{ClaimKey: claim, SourceID: "s1", Value: "PANAMA", SourceReliability: 0.9, Tick: 1}))
	must(t, e.Observe(RawObservation{ClaimKey: claim, SourceID: "s2", Value: "PANAMA", SourceReliability: 0.8, Tick: 1}))
	must(t, e.Observe(RawObservation{ClaimKey: claim, SourceID: "s3", Value: "LIBERIA", SourceReliability: 0.95, Tick: 1}))
	must(t, e.Observe(RawObservation{ClaimKey: claim, SourceID: "s4", Value: "MARSHALL_ISLANDS", SourceReliability: 0.3, Tick: 1}))

	ranked, err := e.RankHypotheses(claim, 1, 100)
	must(t, err)
	if len(ranked) != 3 {
		t.Fatalf("expected 3 ranked candidates, got %d", len(ranked))
	}
	if ranked[0].Value != "PANAMA" || ranked[0].Rank != 1 {
		t.Fatalf("expected PANAMA to rank first, got %+v", ranked[0])
	}
	if ranked[len(ranked)-1].Value != "MARSHALL_ISLANDS" {
		t.Fatalf("expected weakest-supported candidate ranked last, got %+v", ranked[len(ranked)-1])
	}
	// confidences should sum to ~1
	sum := 0.0
	for _, c := range ranked {
		sum += c.Confidence
	}
	if sum < 0.999 || sum > 1.001 {
		t.Fatalf("expected candidate confidences to sum to ~1, got %f", sum)
	}
	if len(ranked[0].SupportingSources) != 2 {
		t.Fatalf("expected PANAMA to have 2 supporting sources, got %v", ranked[0].SupportingSources)
	}
}

func TestRankHypothesesConsistentWithArbitrateClaimWinner(t *testing.T) {
	e := NewArbitrationEngine()
	claim := "C1"
	must(t, e.Observe(RawObservation{ClaimKey: claim, SourceID: "s1", Value: "A", SourceReliability: 0.9, Tick: 1}))
	must(t, e.Observe(RawObservation{ClaimKey: claim, SourceID: "s2", Value: "B", SourceReliability: 0.3, Tick: 1}))

	tv, err := e.ArbitrateClaim(claim, 1, 100)
	must(t, err)
	ranked, err := e.RankHypotheses(claim, 1, 100)
	must(t, err)

	if ranked[0].Value != tv.Value {
		t.Fatalf("expected top-ranked hypothesis %s to match ArbitrateClaim winner %s", ranked[0].Value, tv.Value)
	}
}

func TestRankHypothesesNoObservationsErrors(t *testing.T) {
	e := NewArbitrationEngine()
	if _, err := e.RankHypotheses("nonexistent", 1, 10); err == nil {
		t.Fatalf("expected error for claim with no observations")
	}
}
