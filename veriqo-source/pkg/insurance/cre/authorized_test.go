package cre

import (
	"errors"
	"testing"

	"veriqo/pkg/evidence/manifest"
	"veriqo/pkg/insurance/causation"
	"veriqo/pkg/insurance/evidence"
	"veriqo/pkg/insurance/finding"
)

// groundedManifestRegistry builds a manifest.Registry with a single
// evidence ID finalized (and hash-verified), simulating real grounded
// evidence.
func groundedManifestRegistry(t *testing.T, evidenceID string) *manifest.Registry {
	t.Helper()
	reg := manifest.NewRegistry()
	_, err := reg.RegisterDraft(manifest.Manifest{
		TenantID: "t1", CaseID: "case-1", EvidenceID: evidenceID, Version: 1,
		URI: "evidence://survey-1.pdf", Filename: "survey-1.pdf", MediaType: "application/pdf",
		ByteSize: 1024, SHA256: "aa11bb22cc33dd44ee55ff66aa11bb22cc33dd44ee55ff66aa11bb22cc33dd4",
		Method: "UPLOAD", Collector: "surveyor-1", Source: "independent-surveyor", AcquiredAt: 1, ReceivedAt: 1,
		HashStatus: "COMPUTED", Classification: "INTERNAL",
		AcquisitionRecord: "uploaded by independent surveyor via case portal",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Advance through the full finalization lifecycle, recording the
	// real custody events Authority Round 2's transitionPrerequisiteLocked
	// now requires at each step (RECEIVED, HASHED, REVIEWED) -- the
	// honest way to reach FINALIZED.
	const contentHash = "aa11bb22cc33dd44ee55ff66aa11bb22cc33dd44ee55ff66aa11bb22cc33dd4"
	if _, err := reg.RecordCustodyEvent(evidenceID, evidenceID+"-received", "cre-system", manifest.CustodyReceived, 1, "received into custody", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Advance(evidenceID, manifest.StateIngested, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.RecordCustodyEvent(evidenceID, evidenceID+"-hashed", "cre-system", manifest.CustodyHashed, 1, "hash computed", contentHash); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Advance(evidenceID, manifest.StateIntegrityAssessed, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Advance(evidenceID, manifest.StateProvenanceComplete, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.RecordCustodyEvent(evidenceID, evidenceID+"-reviewed", "cre-system", manifest.CustodyReviewed, 1, "independent review complete", contentHash); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Advance(evidenceID, manifest.StateReadyForFinalization, 1); err != nil {
		t.Fatal(err)
	}
	cur, err := reg.Advance(evidenceID, manifest.StateFinalized, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.VerifyManifestHash(cur); err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestZeroValueAuthorizedFindingIsZero(t *testing.T) {
	var a AuthorizedFinding
	if !a.IsZero() {
		t.Fatal("expected the zero value to report IsZero() == true")
	}
	if a.Finding().Status != "" {
		t.Fatalf("expected the zero value's Finding() to be the zero finding.Finding, got status %q", a.Finding().Status)
	}
	if a.AuthorizationHash() != "" {
		t.Fatal("expected the zero value's AuthorizationHash to be empty")
	}
}

func TestAuthorizeAcceptsAGenuinelyCompleteFinding(t *testing.T) {
	hs := buildHypothesisSet(t)
	candidates := CandidateHypotheses(hs)
	dg := evidence.NewDependencyGraph()
	f, err := BuildFinding(hs, candidates[0], dg, FindingInput{
		CaseID: "case-1", ContractBasis: "clause-9.3", ObligationRef: "obl-1",
		EventRef: "event-1", QuantumRef: "calc-1", HumanReviewRequired: true,
	}, "f1", 100)
	if err != nil {
		t.Fatal(err)
	}
	a, err := Authorize(f, hs, candidates[0].ID, nil, 100)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if a.IsZero() {
		t.Fatal("expected a populated AuthorizedFinding")
	}
	if a.Finding().FindingID != f.FindingID {
		t.Fatalf("expected Finding() to return the authorized Finding, got %v", a.Finding())
	}
	if a.HypothesisID() != candidates[0].ID {
		t.Fatalf("expected HypothesisID() to be %s, got %s", candidates[0].ID, a.HypothesisID())
	}
	if a.AuthorizedAt() != 100 {
		t.Fatalf("expected AuthorizedAt() to be 100, got %d", a.AuthorizedAt())
	}
	if a.AuthorizationHash() == "" {
		t.Fatal("expected a non-empty AuthorizationHash")
	}
}

func TestAuthorizeRefusesAnIncompleteFinding(t *testing.T) {
	hs := buildHypothesisSet(t)
	candidates := CandidateHypotheses(hs)
	incomplete := finding.Finding{FindingID: "f1", CaseID: "case-1"} // never Evaluate()'d to completeness
	a, err := Authorize(incomplete, hs, candidates[0].ID, nil, 1)
	if !errors.Is(err, ErrFindingNotReady) {
		t.Fatalf("expected ErrFindingNotReady, got %v", err)
	}
	if !a.IsZero() {
		t.Fatal("expected the zero value on refusal")
	}
}

// TestAuthorizeRefusesAHandForgedFindingEvenWhenInternallyConsistent is
// the direct proof the Red Flag review asked for: a caller cannot reach
// AUTHORIZED FINDING by hand-building a finding.Finding and skipping
// the real HypothesisSet -- even one that recomputes its own hash to
// stay internally self-consistent (exactly what a real forger capable
// of producing one would do).
func TestAuthorizeRefusesAHandForgedFindingEvenWhenInternallyConsistent(t *testing.T) {
	hs, err := causation.NewHypothesisSet("case-adv", "claim-adv", "question")
	if err != nil {
		t.Fatal(err)
	}
	if err := hs.Add(causation.Hypothesis{ID: "H1", Description: "genuinely unproven, zero evidence"}); err != nil {
		t.Fatal(err)
	}
	forged := finding.Finding{
		FindingID: "forged-1", CaseID: "case-adv",
		SupportedBy: []string{"ev-fabricated"}, ContradictionsConsidered: true,
		ContractBasis: "clause-1", ObligationRef: "obl-1", EventRef: "event-1",
		Causation: "This was definitely caused by X.", QuantumRef: "calc-1",
		ConfidenceBasis:        causation.StatusSupported, // fabricated: H1 is actually UNPROVEN
		AlternativesConsidered: true, HumanReviewDecided: true, Tick: 1,
	}
	forged = finding.Evaluate(forged) // attacker recomputes the hash to stay self-consistent
	if forged.Status != finding.StatusFinding {
		t.Fatalf("expected the forged Finding to structurally reach FINDING on its own terms, got %s", forged.Status)
	}
	a, err := Authorize(forged, hs, "H1", nil, 1)
	if err == nil {
		t.Fatal("expected Authorize to refuse a hand-forged Finding")
	}
	if !errors.Is(err, ErrFindingDoesNotMatchHypothesis) {
		t.Fatalf("expected ErrFindingDoesNotMatchHypothesis, got %v", err)
	}
	if !a.IsZero() {
		t.Fatal("expected the zero value on refusal")
	}
}

func TestAuthorizeRefusesAFindingCitingATamperedTrace(t *testing.T) {
	hs := buildHypothesisSet(t)
	candidates := CandidateHypotheses(hs)
	dg := evidence.NewDependencyGraph()
	f, err := BuildFinding(hs, candidates[0], dg, FindingInput{
		CaseID: "case-1", ContractBasis: "clause-9.3", ObligationRef: "obl-1",
		EventRef: "event-1", QuantumRef: "calc-1", HumanReviewRequired: true,
		SourceInferenceTraceID: "trace:does-not-exist",
	}, "f1", 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Authorize(f, hs, candidates[0].ID, nil, 100); !errors.Is(err, ErrInferenceTraceNotFound) {
		t.Fatalf("expected ErrInferenceTraceNotFound, got %v", err)
	}
}

func TestAuthorizationHashChangesWithTickOrHypothesis(t *testing.T) {
	hs := buildHypothesisSet(t)
	candidates := CandidateHypotheses(hs)
	dg := evidence.NewDependencyGraph()
	f, err := BuildFinding(hs, candidates[0], dg, FindingInput{
		CaseID: "case-1", ContractBasis: "clause-9.3", ObligationRef: "obl-1",
		EventRef: "event-1", QuantumRef: "calc-1", HumanReviewRequired: true,
	}, "f1", 100)
	if err != nil {
		t.Fatal(err)
	}
	a1, err := Authorize(f, hs, candidates[0].ID, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	a2, err := Authorize(f, hs, candidates[0].ID, nil, 200)
	if err != nil {
		t.Fatal(err)
	}
	if a1.AuthorizationHash() == a2.AuthorizationHash() {
		t.Fatal("expected authorizing at a different tick to change the AuthorizationHash")
	}
}

// TestFindingAccessorReturnsAnIndependentCopy is the direct proof for
// the immutability review item: mutating the slice a caller gets back
// from Finding() must never be observable through a's own sealed
// state, nor through any other call to Finding().
func TestFindingAccessorReturnsAnIndependentCopy(t *testing.T) {
	hs := buildHypothesisSet(t)
	candidates := CandidateHypotheses(hs)
	dg := evidence.NewDependencyGraph()
	f, err := BuildFinding(hs, candidates[0], dg, FindingInput{
		CaseID: "case-1", ContractBasis: "clause-9.3", ObligationRef: "obl-1",
		EventRef: "event-1", QuantumRef: "calc-1", HumanReviewRequired: true,
	}, "f1", 100)
	if err != nil {
		t.Fatal(err)
	}
	a, err := Authorize(f, hs, candidates[0].ID, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Finding().SupportedBy) == 0 {
		t.Fatal("fixture must have at least one SupportedBy entry for this test to be meaningful")
	}
	original := append([]string(nil), a.Finding().SupportedBy...)

	got := a.Finding()
	got.SupportedBy[0] = "TAMPERED"

	again := a.Finding()
	for i := range original {
		if again.SupportedBy[i] != original[i] {
			t.Fatalf("mutating a value returned by Finding() corrupted a's own sealed state: got %v, want %v", again.SupportedBy, original)
		}
	}
	if err := VerifyFindingProvenance(again, nil); err != nil {
		t.Fatalf("expected a's own state to still verify after an external mutation attempt: %v", err)
	}
}

// TestAuthorizeClonesInputSoCallerCannotMutateAfterTheFact proves the
// immutability boundary holds at CONSTRUCTION time too: a caller who
// keeps their own reference to the slice they used to build the
// pre-authorization Finding cannot reach into the sealed
// AuthorizedFinding by mutating that reference after Authorize returns.
func TestAuthorizeClonesInputSoCallerCannotMutateAfterTheFact(t *testing.T) {
	hs := buildHypothesisSet(t)
	candidates := CandidateHypotheses(hs)
	dg := evidence.NewDependencyGraph()
	f, err := BuildFinding(hs, candidates[0], dg, FindingInput{
		CaseID: "case-1", ContractBasis: "clause-9.3", ObligationRef: "obl-1",
		EventRef: "event-1", QuantumRef: "calc-1", HumanReviewRequired: true,
	}, "f1", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.SupportedBy) == 0 {
		t.Fatal("fixture must have at least one SupportedBy entry for this test to be meaningful")
	}
	original := append([]string(nil), f.SupportedBy...)

	a, err := Authorize(f, hs, candidates[0].ID, nil, 100)
	if err != nil {
		t.Fatal(err)
	}

	// The caller still holds f, built with its own SupportedBy slice --
	// mutate it after the fact, exactly as a caller who reused a buffer
	// or a shared slice might do.
	f.SupportedBy[0] = "TAMPERED-AFTER-AUTHORIZE"

	got := a.Finding()
	for i := range original {
		if got.SupportedBy[i] != original[i] {
			t.Fatalf("mutating the caller's own pre-authorization Finding after Authorize returned corrupted the sealed AuthorizedFinding: got %v, want %v", got.SupportedBy, original)
		}
	}
}

// singleEvidenceHypothesisSet builds a minimal HypothesisSet with one
// hypothesis citing exactly one evidence ID, so AuthorizeGrounded tests
// only need to ground a single manifest.
func singleEvidenceHypothesisSet(t *testing.T, evidenceID string) (*causation.HypothesisSet, causation.Hypothesis) {
	t.Helper()
	hs, err := causation.NewHypothesisSet("case-grounded", "claim-grounded", "question")
	if err != nil {
		t.Fatal(err)
	}
	if err := hs.Add(causation.Hypothesis{ID: "H1", Description: "grounded hypothesis"}); err != nil {
		t.Fatal(err)
	}
	if err := hs.AddSupportingEvidence("H1", evidenceID); err != nil {
		t.Fatal(err)
	}
	h, _ := hs.Get("H1")
	return hs, h
}

func TestAuthorizeGroundedAcceptsRealFinalizedEvidence(t *testing.T) {
	const evidenceID = "ev-grounded-1"
	hs, h := singleEvidenceHypothesisSet(t, evidenceID)
	dg := evidence.NewDependencyGraph()
	f, err := BuildFinding(hs, h, dg, FindingInput{
		CaseID: "case-grounded", ContractBasis: "clause-1", ObligationRef: "obl-1",
		EventRef: "event-1", QuantumRef: "calc-1", HumanReviewRequired: true,
	}, "f-grounded", 1)
	if err != nil {
		t.Fatal(err)
	}
	manifests := groundedManifestRegistry(t, evidenceID)
	a, err := AuthorizeGrounded(f, hs, h.ID, nil, manifests, 1)
	if err != nil {
		t.Fatalf("AuthorizeGrounded: %v", err)
	}
	if a.IsZero() {
		t.Fatal("expected a populated AuthorizedFinding")
	}
}

func TestAuthorizeGroundedRefusesNilRegistry(t *testing.T) {
	const evidenceID = "ev-grounded-2"
	hs, h := singleEvidenceHypothesisSet(t, evidenceID)
	dg := evidence.NewDependencyGraph()
	f, err := BuildFinding(hs, h, dg, FindingInput{
		CaseID: "case-grounded", ContractBasis: "clause-1", ObligationRef: "obl-1",
		EventRef: "event-1", QuantumRef: "calc-1", HumanReviewRequired: true,
	}, "f-grounded", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AuthorizeGrounded(f, hs, h.ID, nil, nil, 1); !errors.Is(err, ErrEvidenceNotGrounded) {
		t.Fatalf("expected ErrEvidenceNotGrounded for a nil registry, got %v", err)
	}
}

func TestAuthorizeGroundedRefusesUnknownEvidence(t *testing.T) {
	const evidenceID = "ev-grounded-3"
	hs, h := singleEvidenceHypothesisSet(t, evidenceID)
	dg := evidence.NewDependencyGraph()
	f, err := BuildFinding(hs, h, dg, FindingInput{
		CaseID: "case-grounded", ContractBasis: "clause-1", ObligationRef: "obl-1",
		EventRef: "event-1", QuantumRef: "calc-1", HumanReviewRequired: true,
	}, "f-grounded", 1)
	if err != nil {
		t.Fatal(err)
	}
	emptyRegistry := manifest.NewRegistry() // no manifest registered for evidenceID at all
	if _, err := AuthorizeGrounded(f, hs, h.ID, nil, emptyRegistry, 1); !errors.Is(err, ErrEvidenceNotGrounded) {
		t.Fatalf("expected ErrEvidenceNotGrounded for unknown evidence, got %v", err)
	}
}

func TestAuthorizeGroundedRefusesNonFinalizedEvidence(t *testing.T) {
	const evidenceID = "ev-grounded-4"
	hs, h := singleEvidenceHypothesisSet(t, evidenceID)
	dg := evidence.NewDependencyGraph()
	f, err := BuildFinding(hs, h, dg, FindingInput{
		CaseID: "case-grounded", ContractBasis: "clause-1", ObligationRef: "obl-1",
		EventRef: "event-1", QuantumRef: "calc-1", HumanReviewRequired: true,
	}, "f-grounded", 1)
	if err != nil {
		t.Fatal(err)
	}
	reg := manifest.NewRegistry()
	if _, err := reg.RegisterDraft(manifest.Manifest{
		TenantID: "t1", CaseID: "case-1", EvidenceID: evidenceID, Version: 1,
		URI: "evidence://x.pdf", Filename: "x.pdf", MediaType: "application/pdf",
		ByteSize: 1, SHA256: "aa11bb22cc33dd44ee55ff66aa11bb22cc33dd44ee55ff66aa11bb22cc33dd4",
		Method: "UPLOAD", Collector: "s", Source: "s", AcquiredAt: 1, ReceivedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	// Left in DRAFT -- never advanced to FINALIZED.
	if _, err := AuthorizeGrounded(f, hs, h.ID, nil, reg, 1); !errors.Is(err, ErrEvidenceNotGrounded) {
		t.Fatalf("expected ErrEvidenceNotGrounded for a non-finalized manifest, got %v", err)
	}
}

func TestAuthorizeGroundedRefusesPartiallyGroundedEvidence(t *testing.T) {
	hs, err := causation.NewHypothesisSet("case-partial", "claim-partial", "question")
	if err != nil {
		t.Fatal(err)
	}
	if err := hs.Add(causation.Hypothesis{ID: "H1", Description: "partially grounded"}); err != nil {
		t.Fatal(err)
	}
	if err := hs.AddSupportingEvidence("H1", "ev-real"); err != nil {
		t.Fatal(err)
	}
	if err := hs.AddSupportingEvidence("H1", "ev-fake"); err != nil {
		t.Fatal(err)
	}
	h, _ := hs.Get("H1")
	dg := evidence.NewDependencyGraph()
	f, err := BuildFinding(hs, h, dg, FindingInput{
		CaseID: "case-partial", ContractBasis: "clause-1", ObligationRef: "obl-1",
		EventRef: "event-1", QuantumRef: "calc-1", HumanReviewRequired: true,
	}, "f-partial", 1)
	if err != nil {
		t.Fatal(err)
	}
	// Only "ev-real" is grounded; "ev-fake" has no manifest at all.
	manifests := groundedManifestRegistry(t, "ev-real")
	if _, err := AuthorizeGrounded(f, hs, h.ID, nil, manifests, 1); !errors.Is(err, ErrEvidenceNotGrounded) {
		t.Fatalf("expected ErrEvidenceNotGrounded when even one cited evidence ID is ungrounded, got %v", err)
	}
}
