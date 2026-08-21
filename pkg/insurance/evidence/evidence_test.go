package evidence

import (
	"errors"
	"testing"

	"veriqo/pkg/evidence/ontology"
	"veriqo/pkg/insurance/party"
)

func mustEvidence(t *testing.T, subject, source string, observedAt uint64) ontology.Evidence {
	t.Helper()
	e, err := ontology.New(ontology.Evidence{
		Type:       ontology.TypeDocument,
		Subject:    subject,
		Predicate:  "describes",
		Object:     "cargo_condition",
		Source:     source,
		ObservedAt: observedAt,
		Confidence: 0.9,
		Attributes: map[string]string{"document_hash": "deadbeef"},
	})
	if err != nil {
		t.Fatalf("ontology.New: %v", err)
	}
	return e
}

func TestNewRejectsEmptyCaseID(t *testing.T) {
	ev := mustEvidence(t, "S1", "src", 100)
	if _, err := New("", ev, "PTY-001", OriginClaimant); !errors.Is(err, ErrEmptyCaseID) {
		t.Fatalf("expected ErrEmptyCaseID, got %v", err)
	}
}

func TestNewRejectsInvalidUnderlying(t *testing.T) {
	if _, err := New("CASE-1", ontology.Evidence{}, "PTY-001", OriginClaimant); !errors.Is(err, ErrUnderlyingInvalid) {
		t.Fatalf("expected ErrUnderlyingInvalid, got %v", err)
	}
}

func TestNewRejectsEmptySourceParty(t *testing.T) {
	ev := mustEvidence(t, "S1", "src", 100)
	if _, err := New("CASE-1", ev, "", OriginClaimant); !errors.Is(err, ErrEmptySourceParty) {
		t.Fatalf("expected ErrEmptySourceParty, got %v", err)
	}
}

func TestNewRejectsUnknownOrigin(t *testing.T) {
	ev := mustEvidence(t, "S1", "src", 100)
	if _, err := New("CASE-1", ev, "PTY-001", Origin("NOT_AN_ORIGIN")); err == nil {
		t.Fatal("expected error for unknown origin")
	}
}

func TestNewSucceedsAndDefaultsToUnverified(t *testing.T) {
	ev := mustEvidence(t, "S1", "src", 100)
	rec, err := New("CASE-1", ev, "PTY-001", OriginClaimant)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if rec.Status != StatusUnverified {
		t.Fatalf("expected default status UNVERIFIED, got %s", rec.Status)
	}
	if rec.EvidenceID() == "" {
		t.Fatal("expected non-empty EvidenceID")
	}
}

func TestRegistrySubmitAndGet(t *testing.T) {
	reg := NewRegistry()
	ev := mustEvidence(t, "S1", "src", 100)
	rec, _ := New("CASE-1", ev, "PTY-001", OriginClaimant)
	if err := reg.Submit(rec); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	got, ok := reg.Get(rec.EvidenceID())
	if !ok {
		t.Fatal("expected record to be found")
	}
	if got.CaseID != "CASE-1" {
		t.Fatalf("unexpected CaseID %q", got.CaseID)
	}
}

// TestDuplicateEvidenceRejected reproduces blueprint §35 adversarial
// test #2 ("Duplicate evidence"): the literal same evidence content
// must be refused, not silently accepted twice.
func TestDuplicateEvidenceRejected(t *testing.T) {
	reg := NewRegistry()
	ev := mustEvidence(t, "S1", "src", 100)
	rec1, _ := New("CASE-1", ev, "PTY-001", OriginClaimant)
	if err := reg.Submit(rec1); err != nil {
		t.Fatalf("first Submit: %v", err)
	}
	rec2, _ := New("CASE-1", ev, "PTY-002", OriginInsurer) // same content, different submitter
	if err := reg.Submit(rec2); !errors.Is(err, ErrDuplicateEvidence) {
		t.Fatalf("expected ErrDuplicateEvidence, got %v", err)
	}
}

// TestSameEvidenceSubmittedByThreeParties reproduces blueprint §35
// adversarial test #3 directly: three parties each submit the exact
// same underlying content — only the first registers.
func TestSameEvidenceSubmittedByThreeParties(t *testing.T) {
	reg := NewRegistry()
	ev := mustEvidence(t, "S1", "src", 100)
	origins := []Origin{OriginClaimant, OriginInsurer, OriginCarrier}
	accepted := 0
	for i, o := range origins {
		rec, _ := New("CASE-1", ev, party.PartyID("PTY-00X"), o)
		_ = i
		if err := reg.Submit(rec); err == nil {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("expected exactly 1 of 3 identical submissions to be accepted, got %d", accepted)
	}
	if reg.Count() != 1 {
		t.Fatalf("expected registry count 1, got %d", reg.Count())
	}
}

func TestSetStatusRejectsUnknownEvidence(t *testing.T) {
	reg := NewRegistry()
	if err := reg.SetStatus("nonexistent", StatusCorroborated); !errors.Is(err, ErrEvidenceNotFound) {
		t.Fatalf("expected ErrEvidenceNotFound, got %v", err)
	}
}

func TestSetStrengthRejectsUnassessed(t *testing.T) {
	reg := NewRegistry()
	ev := mustEvidence(t, "S1", "src", 100)
	rec, _ := New("CASE-1", ev, "PTY-001", OriginClaimant)
	reg.Submit(rec)
	if err := reg.SetStrength(rec.EvidenceID(), Strength{}); !errors.Is(err, ErrStrengthNotAssessed) {
		t.Fatalf("expected ErrStrengthNotAssessed, got %v", err)
	}
}

// TestStrengthWorkedExample reproduces the blueprint's §9 worked
// example exactly.
func TestStrengthWorkedExample(t *testing.T) {
	reg := NewRegistry()
	ev := mustEvidence(t, "S1", "src", 100)
	rec, _ := New("CASE-1", ev, "PTY-001", OriginClaimant)
	reg.Submit(rec)

	s := Strength{
		Authenticity:             AuthenticitySupported,
		Integrity:                IntegrityVerified,
		Provenance:               ProvenanceVerified,
		Completeness:             CompletenessPartial,
		TemporalConsistency:      TemporalConsistencySupported,
		IndependentCorroboration: CorroborationHigh,
		ContradictionLevel:       ContradictionLevelLow,
	}
	if err := reg.SetStrength(rec.EvidenceID(), s); err != nil {
		t.Fatalf("SetStrength: %v", err)
	}
	got, _ := reg.Get(rec.EvidenceID())
	if got.Strength.Authenticity != AuthenticitySupported {
		t.Fatalf("unexpected authenticity %v", got.Strength.Authenticity)
	}
	if got.Strength.IndependentCorroboration != CorroborationHigh {
		t.Fatalf("unexpected corroboration %v", got.Strength.IndependentCorroboration)
	}
}

func TestByOrigin(t *testing.T) {
	reg := NewRegistry()
	e1 := mustEvidence(t, "S1", "src1", 100)
	e2 := mustEvidence(t, "S2", "src2", 200)
	e3 := mustEvidence(t, "S3", "src3", 300)
	r1, _ := New("CASE-1", e1, "PTY-001", OriginClaimant)
	r2, _ := New("CASE-1", e2, "PTY-002", OriginInsurer)
	r3, _ := New("CASE-1", e3, "PTY-003", OriginClaimant)
	reg.Submit(r1)
	reg.Submit(r2)
	reg.Submit(r3)

	claimantEv := reg.ByOrigin(OriginClaimant)
	if len(claimantEv) != 2 {
		t.Fatalf("expected 2 claimant records, got %d", len(claimantEv))
	}
}

func TestKnownOriginsCoversTheBlueprintList(t *testing.T) {
	// blueprint §7 enumerates exactly 8 origin classes.
	if got := len(KnownOrigins()); got != 8 {
		t.Fatalf("expected 8 known origins per VICE blueprint §7, got %d", got)
	}
}

func TestKnownStatusesCoversTheBlueprintList(t *testing.T) {
	// blueprint §8 enumerates exactly 7 statuses.
	if len(knownStatuses) != 7 {
		t.Fatalf("expected 7 known statuses per VICE blueprint §8, got %d", len(knownStatuses))
	}
}
