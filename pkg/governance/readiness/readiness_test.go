package readiness

import (
	"testing"

	"veriqo/pkg/blockers/livedata"
)

func baseRecord() EvidenceRecord {
	return EvidenceRecord{
		SourceID: "SRC-1", SourceType: "AIS", DataOrigin: livedata.ModeLiveLicensed,
		AccessStatus: AccessGranted, LegalStatus: LegalLicensed,
		Quality: StrengthStrong, Provenance: ProvenanceComplete,
		HasTimestamp: true, Tick: 1, Integrity: IntegrityVerified,
		EvidenceStrength: StrengthStrong, OperationalStatus: OperationalOK,
		SourceTrusted: true,
	}
}

func TestEvaluateFullyCompliantIsReady(t *testing.T) {
	res := Evaluate(baseRecord(), Criteria{
		RequireLegalAccess: true, RequireProvenanceComplete: true, RequireSourceTrusted: true,
	}, nil)
	if res.Verdict != VerdictReady {
		t.Fatalf("got %s, want READY (findings=%+v)", res.Verdict, res.Findings)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("READY verdict must carry zero findings, got %+v", res.Findings)
	}
}

func TestEvaluateHashMismatchIsBlocked(t *testing.T) {
	rec := baseRecord()
	rec.Integrity = IntegrityMismatch
	res := Evaluate(rec, Criteria{}, nil)
	if res.Verdict != VerdictBlocked {
		t.Fatalf("got %s, want BLOCKED", res.Verdict)
	}
	if !res.HasReason(ReasonHashMismatch) {
		t.Fatalf("expected HASH_MISMATCH reason, got %+v", res.Findings)
	}
}

func TestEvaluateUntrustedSourceIsBlocked(t *testing.T) {
	rec := baseRecord()
	rec.SourceTrusted = false
	res := Evaluate(rec, Criteria{RequireSourceTrusted: true}, nil)
	if res.Verdict != VerdictBlocked {
		t.Fatalf("got %s, want BLOCKED", res.Verdict)
	}
	if !res.HasReason(ReasonSourceUntrusted) {
		t.Fatalf("expected SOURCE_UNTRUSTED reason, got %+v", res.Findings)
	}
}

func TestEvaluateMissingLegalAccessIsNotEligible(t *testing.T) {
	rec := baseRecord()
	rec.AccessStatus = AccessDenied
	rec.LegalStatus = LegalUnlicensed
	res := Evaluate(rec, Criteria{RequireLegalAccess: true}, nil)
	if res.Verdict != VerdictNotEligible {
		t.Fatalf("got %s, want NOT_ELIGIBLE", res.Verdict)
	}
	if !res.HasReason(ReasonLegalAccessMissing) {
		t.Fatalf("expected LEGAL_ACCESS_MISSING reason, got %+v", res.Findings)
	}
}

// TestEvaluateSyntheticCannotSatisfyExternalEvidenceRequirement is the
// direct link between A1 and A2: synthetic/replay/derived-benchmark data
// origin can never satisfy a "requires independent real-world evidence"
// criterion, exactly because livedata.IsIndependentRealObservation
// returns false for those origins.
func TestEvaluateSyntheticCannotSatisfyExternalEvidenceRequirement(t *testing.T) {
	for _, origin := range []livedata.DataMode{livedata.ModeSynthetic, livedata.ModeReplay, livedata.ModeRealDerivedBenchmark} {
		rec := baseRecord()
		rec.DataOrigin = origin
		res := Evaluate(rec, Criteria{RequireExternalEvidence: true}, nil)
		if res.Verdict != VerdictNotEligible {
			t.Errorf("origin=%s: got %s, want NOT_ELIGIBLE", origin, res.Verdict)
		}
		if !res.HasReason(ReasonExternalEvidenceRequired) {
			t.Errorf("origin=%s: expected EXTERNAL_EVIDENCE_REQUIRED reason, got %+v", origin, res.Findings)
		}
	}
}

func TestEvaluateIncompleteProvenanceIsPartial(t *testing.T) {
	rec := baseRecord()
	rec.Provenance = ProvenancePartial
	res := Evaluate(rec, Criteria{RequireProvenanceComplete: true}, nil)
	if res.Verdict != VerdictPartial {
		t.Fatalf("got %s, want PARTIAL", res.Verdict)
	}
	if !res.HasReason(ReasonProvenanceIncomplete) {
		t.Fatalf("expected PROVENANCE_INCOMPLETE reason, got %+v", res.Findings)
	}
}

func TestEvaluateUnmetNamedCriterionIsPartialAndDeterministic(t *testing.T) {
	rec := baseRecord()
	crit := Criteria{Named: []string{"zero_replay_acceptance", "dedup_verified"}}
	res1 := Evaluate(rec, crit, map[string]bool{"dedup_verified": true})
	res2 := Evaluate(rec, crit, map[string]bool{"dedup_verified": true})
	if res1.Verdict != VerdictPartial {
		t.Fatalf("got %s, want PARTIAL", res1.Verdict)
	}
	if len(res1.Findings) != 1 || res1.Findings[0].Detail != "unmet: zero_replay_acceptance" {
		t.Fatalf("unexpected findings: %+v", res1.Findings)
	}
	// Determinism: identical input must produce byte-identical output,
	// not merely an equal Verdict -- Findings[].Detail ordering must also
	// be stable (this is why Evaluate sorts unmet criteria explicitly).
	if res1.Verdict != res2.Verdict || res1.Findings[0].Detail != res2.Findings[0].Detail {
		t.Fatalf("Evaluate is not deterministic: %+v vs %+v", res1, res2)
	}
}

// TestSeverityNeverDowngrades proves the "worst finding wins" rule
// directly: a record with both a BLOCKED-tier and a PARTIAL-tier finding
// must report the BLOCKED verdict, and the PARTIAL finding must still be
// visible in Findings (never silently dropped by the worse one).
func TestSeverityNeverDowngrades(t *testing.T) {
	rec := baseRecord()
	rec.Integrity = IntegrityMismatch  // BLOCKED
	rec.Provenance = ProvenancePartial // PARTIAL
	res := Evaluate(rec, Criteria{RequireProvenanceComplete: true}, nil)
	if res.Verdict != VerdictBlocked {
		t.Fatalf("got %s, want BLOCKED (worst of BLOCKED+PARTIAL)", res.Verdict)
	}
	if !res.HasReason(ReasonHashMismatch) || !res.HasReason(ReasonProvenanceIncomplete) {
		t.Fatalf("both findings must be preserved, got %+v", res.Findings)
	}
}
