package contradiction

import "testing"

// TestArbitrateClaimProducesReason is audit item A5's "reason" output
// requirement: every TruthVersion must carry a non-empty, deterministic
// explanation of why its value won.
func TestArbitrateClaimProducesReason(t *testing.T) {
	e := NewArbitrationEngine()
	_ = e.Observe(RawObservation{ClaimKey: "c1", SourceID: "AIS", Value: "10:05", SourceReliability: 0.6, Tick: 0})
	_ = e.Observe(RawObservation{ClaimKey: "c1", SourceID: "PORT", Value: "10:11", SourceReliability: 0.9, Tick: 0})
	_ = e.Observe(RawObservation{ClaimKey: "c1", SourceID: "NOR", Value: "10:14", SourceReliability: 0.8, Tick: 0})
	_ = e.Observe(RawObservation{ClaimKey: "c1", SourceID: "SOF", Value: "10:18", SourceReliability: 0.7, Tick: 0})

	tv, err := e.ArbitrateClaim("c1", 0, 100)
	if err != nil {
		t.Fatalf("ArbitrateClaim: %v", err)
	}
	if tv.Reason == "" {
		t.Fatal("Reason must not be empty when multiple conflicting values are arbitrated")
	}
	if tv.Value != "10:11" {
		t.Fatalf("expected PORT's higher-reliability value to win, got %q", tv.Value)
	}
	t.Logf("Reason: %s", tv.Reason)

	// Reason is a pure function of the TruthVersion's own fields — two
	// arbitrations of byte-identical observations must produce
	// byte-identical Reason strings.
	e2 := NewArbitrationEngine()
	_ = e2.Observe(RawObservation{ClaimKey: "c1", SourceID: "AIS", Value: "10:05", SourceReliability: 0.6, Tick: 0})
	_ = e2.Observe(RawObservation{ClaimKey: "c1", SourceID: "PORT", Value: "10:11", SourceReliability: 0.9, Tick: 0})
	_ = e2.Observe(RawObservation{ClaimKey: "c1", SourceID: "NOR", Value: "10:14", SourceReliability: 0.8, Tick: 0})
	_ = e2.Observe(RawObservation{ClaimKey: "c1", SourceID: "SOF", Value: "10:18", SourceReliability: 0.7, Tick: 0})
	tv2, err := e2.ArbitrateClaim("c1", 0, 100)
	if err != nil {
		t.Fatalf("ArbitrateClaim (2nd engine): %v", err)
	}
	if tv2.Reason != tv.Reason {
		t.Fatalf("Reason is not deterministic:\n  1: %s\n  2: %s", tv.Reason, tv2.Reason)
	}
}

// TestArbitrateClaimSingleValueReason covers the no-contradiction case
// explicitly, since reasonForArbitration branches on it.
func TestArbitrateClaimSingleValueReason(t *testing.T) {
	e := NewArbitrationEngine()
	_ = e.Observe(RawObservation{ClaimKey: "c2", SourceID: "AIS", Value: "OFF", SourceReliability: 0.9, Tick: 0})
	tv, err := e.ArbitrateClaim("c2", 0, 100)
	if err != nil {
		t.Fatalf("ArbitrateClaim: %v", err)
	}
	if tv.Reason == "" {
		t.Fatal("Reason must not be empty even with a single, uncontested value")
	}
	if len(tv.ConflictingEdges) != 0 {
		t.Fatalf("expected no conflicts, got %+v", tv.ConflictingEdges)
	}
}

// TestRegisterAuthorityIsReportedNotDecisive proves A5's design choice
// directly: RegisterAuthority changes WHAT IS REPORTED
// (WinningAuthority/HighestAuthoritySource) but never changes WHICH
// VALUE WINS — the existing, already-tested weighted-score mechanism
// (TestArbitrateClaimEvidenceDecayAffectsOutcome, etc.) is untouched.
func TestRegisterAuthorityIsReportedNotDecisive(t *testing.T) {
	build := func() *ArbitrationEngine {
		e := NewArbitrationEngine()
		// LOW_RELIABILITY has far lower SourceReliability, so it must lose
		// regardless of any authority level registered for it.
		_ = e.Observe(RawObservation{ClaimKey: "c3", SourceID: "HIGH_RELIABILITY", Value: "A", SourceReliability: 0.95, Tick: 0})
		_ = e.Observe(RawObservation{ClaimKey: "c3", SourceID: "LOW_RELIABILITY", Value: "B", SourceReliability: 0.1, Tick: 0})
		return e
	}

	withoutAuthority := build()
	tvNoAuth, err := withoutAuthority.ArbitrateClaim("c3", 0, 100)
	if err != nil {
		t.Fatalf("ArbitrateClaim: %v", err)
	}

	withAuthority := build()
	withAuthority.RegisterAuthority("LOW_RELIABILITY", 100) // maximally authoritative, but still low-reliability
	tvWithAuth, err := withAuthority.ArbitrateClaim("c3", 0, 100)
	if err != nil {
		t.Fatalf("ArbitrateClaim: %v", err)
	}

	if tvNoAuth.Value != tvWithAuth.Value {
		t.Fatalf("registering authority must not change the arbitration winner: %q vs %q", tvNoAuth.Value, tvWithAuth.Value)
	}
	if tvWithAuth.Value != "A" {
		t.Fatalf("HIGH_RELIABILITY should still win on weighted score, got winner=%q", tvWithAuth.Value)
	}
	if tvWithAuth.WinningAuthority != withAuthority.Authority("HIGH_RELIABILITY") {
		t.Fatalf("WinningAuthority=%d should reflect the WINNING source's (HIGH_RELIABILITY, unregistered=0) authority, not LOW_RELIABILITY's", tvWithAuth.WinningAuthority)
	}
	if tvWithAuth.HighestAuthoritySource != "HIGH_RELIABILITY" {
		t.Fatalf("HighestAuthoritySource should be the (only) supporting source of the winning value, got %q", tvWithAuth.HighestAuthoritySource)
	}
}
