package verification

import (
	"errors"
	"testing"

	"veriqo/pkg/evidence/ontology"
	"veriqo/pkg/insurance/evidence"
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

func mustRecord(t *testing.T, caseID, subject, source string, observedAt uint64) evidence.Record {
	t.Helper()
	rec, err := evidence.New(caseID, mustEvidence(t, subject, source, observedAt), "PTY-001", evidence.OriginSurveyor)
	if err != nil {
		t.Fatalf("evidence.New: %v", err)
	}
	return rec
}

func buildRegistry(t *testing.T, caseID string, n int) *evidence.Registry {
	t.Helper()
	reg := evidence.NewRegistry()
	for i := 0; i < n; i++ {
		rec := mustRecord(t, caseID, "S", "surveyor", uint64(1000+i))
		if err := reg.Submit(rec); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}
	return reg
}

func TestGenerateRejectsEmptyCaseID(t *testing.T) {
	reg := buildRegistry(t, "CASE-1", 1)
	if _, err := Generate(reg, "", 100); !errors.Is(err, ErrEmptyCaseID) {
		t.Fatalf("expected ErrEmptyCaseID, got %v", err)
	}
}

func TestGenerateRejectsZeroEvidence(t *testing.T) {
	reg := evidence.NewRegistry()
	if _, err := Generate(reg, "CASE-1", 100); !errors.Is(err, ErrNoEvidence) {
		t.Fatalf("expected ErrNoEvidence for an empty registry, got %v", err)
	}
}

func TestGenerateRejectsNilRegistry(t *testing.T) {
	if _, err := Generate(nil, "CASE-1", 100); !errors.Is(err, ErrNoEvidence) {
		t.Fatalf("expected ErrNoEvidence for a nil registry, got %v", err)
	}
}

// TestGenerateIsDeterministic is the P0 property blueprint §31 and
// §37 both require: running Generate twice over an UNCHANGED registry
// must produce a byte-identical EvidenceRootHash, regardless of the
// registry's internal iteration order.
func TestGenerateIsDeterministic(t *testing.T) {
	reg := buildRegistry(t, "CASE-1", 5)
	m1, err := Generate(reg, "CASE-1", 500)
	if err != nil {
		t.Fatalf("Generate 1: %v", err)
	}
	m2, err := Generate(reg, "CASE-1", 500)
	if err != nil {
		t.Fatalf("Generate 2: %v", err)
	}
	if m1.EvidenceRootHash != m2.EvidenceRootHash {
		t.Fatalf("expected identical hash across two Generate calls, got %s vs %s", m1.EvidenceRootHash, m2.EvidenceRootHash)
	}
	if m1.EvidenceCount != 5 {
		t.Fatalf("expected EvidenceCount 5, got %d", m1.EvidenceCount)
	}
}

func TestGenerateOnlyCountsMatchingCaseID(t *testing.T) {
	reg := evidence.NewRegistry()
	reg.Submit(mustRecord(t, "CASE-1", "S1", "surveyor", 100))
	reg.Submit(mustRecord(t, "CASE-2", "S2", "surveyor", 200)) // a different case in the SAME registry
	m, err := Generate(reg, "CASE-1", 500)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if m.EvidenceCount != 1 {
		t.Fatalf("expected Generate to count only CASE-1's evidence, got %d", m.EvidenceCount)
	}
}

func TestVerifySucceedsWhenUnchanged(t *testing.T) {
	reg := buildRegistry(t, "CASE-1", 3)
	m, err := Generate(reg, "CASE-1", 500)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if err := Verify(m, reg); err != nil {
		t.Fatalf("Verify on an unchanged registry: %v", err)
	}
}

// TestVerifyDetectsAddedEvidence is the tamper-sensitivity property: a
// Manifest generated before a new evidence item was added must fail
// Verify against the now-larger registry.
func TestVerifyDetectsAddedEvidence(t *testing.T) {
	reg := buildRegistry(t, "CASE-1", 3)
	m, err := Generate(reg, "CASE-1", 500)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	reg.Submit(mustRecord(t, "CASE-1", "S-NEW", "surveyor", 999))
	if err := Verify(m, reg); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("expected ErrHashMismatch after adding evidence post-manifest, got %v", err)
	}
}

// TestVerifyDetectsAlteredEvidence proves the hash-of-hashes property:
// because EvidenceID is content-addressed, replacing one record's
// underlying content with an otherwise-similar record (different
// EvidenceID) is caught exactly the same way as an outright addition
// or removal -- there is no way to "edit in place" without changing
// the ID.
func TestVerifyDetectsAlteredEvidence(t *testing.T) {
	regA := buildRegistry(t, "CASE-1", 3)
	mA, err := Generate(regA, "CASE-1", 500)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	regB := evidence.NewRegistry()
	for i := 0; i < 3; i++ {
		observedAt := uint64(1000 + i)
		if i == 2 {
			observedAt = 777777 // one record's content differs -> different EvidenceID
		}
		regB.Submit(mustRecord(t, "CASE-1", "S", "surveyor", observedAt))
	}

	if err := Verify(mA, regB); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("expected ErrHashMismatch when one record's content was altered, got %v", err)
	}
}

func TestEvidenceIDsAreSortedRegardlessOfSubmissionOrder(t *testing.T) {
	caseID := "CASE-1"
	recA := mustRecord(t, caseID, "A", "surveyor", 100)
	recB := mustRecord(t, caseID, "B", "surveyor", 200)

	reg1 := evidence.NewRegistry()
	reg1.Submit(recA)
	reg1.Submit(recB)

	reg2 := evidence.NewRegistry()
	reg2.Submit(recB)
	reg2.Submit(recA)

	m1, _ := Generate(reg1, caseID, 500)
	m2, _ := Generate(reg2, caseID, 500)
	if m1.EvidenceRootHash != m2.EvidenceRootHash {
		t.Fatal("expected submission order to be irrelevant to the evidence root hash")
	}
	for i := 1; i < len(m1.EvidenceIDs); i++ {
		if m1.EvidenceIDs[i-1] > m1.EvidenceIDs[i] {
			t.Fatal("expected EvidenceIDs to be sorted")
		}
	}
}
