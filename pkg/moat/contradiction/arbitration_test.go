package contradiction

import "testing"

func TestArbitrateClaimFullPipeline(t *testing.T) {
	e := NewArbitrationEngine()
	claim := "VESSEL:1|FLAG_STATE"
	must(t, e.Observe(RawObservation{ClaimKey: claim, SourceID: "ais-a", Value: "PANAMA", SourceReliability: 0.9, Tick: 1}))
	must(t, e.Observe(RawObservation{ClaimKey: claim, SourceID: "ais-b", Value: "PANAMA", SourceReliability: 0.85, Tick: 1}))
	must(t, e.Observe(RawObservation{ClaimKey: claim, SourceID: "port-auth", Value: "LIBERIA", SourceReliability: 0.95, Tick: 1}))

	tv, err := e.ArbitrateClaim(claim, 1, 100)
	must(t, err)

	if tv.Value != "PANAMA" {
		t.Fatalf("expected PANAMA to win (2 corroborating sources outweigh 1 stronger single source), got %s", tv.Value)
	}
	if len(tv.ConflictingEdges) == 0 {
		t.Fatalf("expected detected conflict edges between PANAMA and LIBERIA evidence")
	}
	if len(tv.SupportingSources) != 2 {
		t.Fatalf("expected 2 supporting sources for PANAMA, got %v", tv.SupportingSources)
	}
	if tv.RunnerUpValue != "LIBERIA" {
		t.Fatalf("expected LIBERIA as runner-up, got %s", tv.RunnerUpValue)
	}
	if tv.Confidence <= tv.RunnerUpConfidence {
		t.Fatalf("winner confidence should exceed runner-up: %f vs %f", tv.Confidence, tv.RunnerUpConfidence)
	}
}

func TestArbitrateClaimNoObservationsErrors(t *testing.T) {
	e := NewArbitrationEngine()
	if _, err := e.ArbitrateClaim("nonexistent", 1, 10); err == nil {
		t.Fatalf("expected error arbitrating a claim with no observations")
	}
}

func TestArbitrateClaimEvidenceDecayAffectsOutcome(t *testing.T) {
	e := NewArbitrationEngine()
	claim := "C1"
	// stale evidence for "A" (observed long ago), fresh evidence for "B"
	must(t, e.Observe(RawObservation{ClaimKey: claim, SourceID: "s1", Value: "A", SourceReliability: 0.99, Tick: 0}))
	must(t, e.Observe(RawObservation{ClaimKey: claim, SourceID: "s2", Value: "B", SourceReliability: 0.6, Tick: 100}))

	// arbitrate far in the future, with a short half-life: s1's evidence
	// should have decayed to near-nothing, letting fresher, weaker s2 win.
	tv, err := e.ArbitrateClaim(claim, 100, 5)
	must(t, err)
	if tv.Value != "B" {
		t.Fatalf("expected fresher evidence to win after heavy decay of stale evidence, got %s", tv.Value)
	}
}

func TestTruthVersionHashChainAndTamperDetection(t *testing.T) {
	e := NewArbitrationEngine()
	claim := "C1"
	must(t, e.Observe(RawObservation{ClaimKey: claim, SourceID: "s1", Value: "A", SourceReliability: 0.9, Tick: 1}))
	must(t, e.Observe(RawObservation{ClaimKey: claim, SourceID: "s2", Value: "A", SourceReliability: 0.8, Tick: 1}))
	_, err := e.ArbitrateClaim(claim, 1, 50)
	must(t, err)

	// second round of evidence arrives, re-arbitrate -> second TruthVersion
	must(t, e.Observe(RawObservation{ClaimKey: claim, SourceID: "s3", Value: "B", SourceReliability: 0.99, Tick: 2}))
	_, err = e.ArbitrateClaim(claim, 2, 50)
	must(t, err)

	ledger := e.TruthLedger(claim)
	if len(ledger) != 2 {
		t.Fatalf("expected 2 truth versions, got %d", len(ledger))
	}
	if err := e.VerifyTruthLedger(claim); err != nil {
		t.Fatalf("expected clean ledger: %v", err)
	}
}

func TestToObservationBridgesIntoExistingIngestLedger(t *testing.T) {
	arb := NewArbitrationEngine()
	claim := "C1"
	must(t, arb.Observe(RawObservation{ClaimKey: claim, SourceID: "s1", Value: "A", SourceReliability: 0.9, Tick: 1}))
	must(t, arb.Observe(RawObservation{ClaimKey: claim, SourceID: "s2", Value: "B", SourceReliability: 0.5, Tick: 1}))
	tv, err := arb.ArbitrateClaim(claim, 1, 50)
	must(t, err)

	contra := NewEngine()
	_, err = contra.Ingest(tv.ToObservation())
	must(t, err)
	evo := contra.TruthEvolution(claim)
	if len(evo) != 1 {
		t.Fatalf("expected the arbitration outcome to flow into the existing evolution ledger, got %d records", len(evo))
	}
	if evo[0].Observation.Winner != tv.Value {
		t.Fatalf("expected bridged observation winner %s to match arbitration winner %s", evo[0].Observation.Winner, tv.Value)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
