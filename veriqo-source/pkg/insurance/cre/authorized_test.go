package cre

import (
	"errors"
	"testing"

	"veriqo/pkg/insurance/causation"
	"veriqo/pkg/insurance/evidence"
	"veriqo/pkg/insurance/finding"
)

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
