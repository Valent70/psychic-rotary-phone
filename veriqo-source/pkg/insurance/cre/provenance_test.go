package cre

import (
	"errors"
	"testing"

	"veriqo/pkg/governance/lifecycle"
	"veriqo/pkg/inference"
	"veriqo/pkg/insurance/causation"
	"veriqo/pkg/insurance/evidence"
	"veriqo/pkg/insurance/finding"
)

func activeModelForProvenanceTest(t *testing.T, reg *lifecycle.Registry, modelID, version string, tick uint64) string {
	t.Helper()
	m := lifecycle.Model{ModelID: modelID, Version: version, Type: "x", ParametersHash: "p", TrainingDataHash: "d"}
	ev, err := reg.RegisterModel(m, "actor", tick)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.SetCalibration(ev.Key, "calib-1"); err != nil {
		t.Fatal(err)
	}
	for _, to := range []lifecycle.ModelState{lifecycle.ModelValidated, lifecycle.ModelCalibrated} {
		if _, err := reg.TransitionModel(ev.Key, to, "actor", "", "", tick, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := reg.TransitionModel(ev.Key, lifecycle.ModelApproved, "actor", "approver", "ok", tick, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.TransitionModel(ev.Key, lifecycle.ModelActive, "actor", "approver", "go", tick, nil); err != nil {
		t.Fatal(err)
	}
	return ev.Key
}

func TestVerifyFindingProvenanceWithNoCitationIsFine(t *testing.T) {
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
	if f.SourceInferenceTraceID != "" {
		t.Fatal("expected no InferenceTrace citation in this fixture")
	}
	if err := VerifyFindingProvenance(f, nil); err != nil {
		t.Fatalf("expected no error for a Finding with no InferenceTrace citation, got %v", err)
	}
}

func TestVerifyFindingProvenanceConfirmsARealCitedTrace(t *testing.T) {
	reg := lifecycle.NewRegistry()
	modelKey := activeModelForProvenanceTest(t, reg, "m1", "v1", 1)
	recorder := inference.NewRecorder(reg)
	trace, err := recorder.Record(modelKey, "inputhash", "output", 0.8, "actor", "case_resolution", 1)
	if err != nil {
		t.Fatal(err)
	}

	hs := buildHypothesisSet(t)
	candidates := CandidateHypotheses(hs)
	dg := evidence.NewDependencyGraph()
	f, err := BuildFinding(hs, candidates[0], dg, FindingInput{
		CaseID: "case-1", ContractBasis: "clause-9.3", ObligationRef: "obl-1",
		EventRef: "event-1", QuantumRef: "calc-1", HumanReviewRequired: true,
		SourceInferenceTraceID: trace.TraceID,
	}, "f1", 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyFindingProvenance(f, recorder.Traces()); err != nil {
		t.Fatalf("VerifyFindingProvenance: %v", err)
	}
}

func TestVerifyFindingProvenanceRejectsCitationToNonexistentTrace(t *testing.T) {
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
	if err := VerifyFindingProvenance(f, nil); !errors.Is(err, ErrInferenceTraceNotFound) {
		t.Fatalf("expected ErrInferenceTraceNotFound, got %v", err)
	}
}

func TestVerifyFindingProvenanceRejectsTamperedTrace(t *testing.T) {
	reg := lifecycle.NewRegistry()
	modelKey := activeModelForProvenanceTest(t, reg, "m1", "v1", 1)
	recorder := inference.NewRecorder(reg)
	trace, err := recorder.Record(modelKey, "inputhash", "output", 0.8, "actor", "case_resolution", 1)
	if err != nil {
		t.Fatal(err)
	}
	tampered := trace
	tampered.Confidence = 0.99 // tamper, hash now stale

	hs := buildHypothesisSet(t)
	candidates := CandidateHypotheses(hs)
	dg := evidence.NewDependencyGraph()
	f, err := BuildFinding(hs, candidates[0], dg, FindingInput{
		CaseID: "case-1", ContractBasis: "clause-9.3", ObligationRef: "obl-1",
		EventRef: "event-1", QuantumRef: "calc-1", HumanReviewRequired: true,
		SourceInferenceTraceID: trace.TraceID,
	}, "f1", 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyFindingProvenance(f, []inference.InferenceTrace{tampered}); err == nil {
		t.Fatal("expected a tampered cited trace to fail provenance verification")
	}
}

func TestVerifyFindingProvenanceRejectsTamperedFindingItself(t *testing.T) {
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
	f.CaseID = "tampered-case-id"
	if err := VerifyFindingProvenance(f, nil); err == nil {
		t.Fatal("expected tampering with the Finding itself to fail provenance verification")
	}
}

func TestVerifyFindingAgainstHypothesisAcceptsAMechanicallyBuiltFinding(t *testing.T) {
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
	if err := VerifyFindingAgainstHypothesis(f, hs, candidates[0].ID); err != nil {
		t.Fatalf("expected a Finding built by BuildFinding itself to verify, got %v", err)
	}
}

func TestVerifyFindingAgainstHypothesisRejectsFabricatedConfidenceBasis(t *testing.T) {
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
	// Simulate a caller hand-constructing (or upgrading) a Finding's
	// ConfidenceBasis to a stronger status than the real hypothesis
	// actually earned -- re-evaluate so the Finding's own Hash stays
	// internally consistent (an attacker capable of forging a Finding
	// would also recompute its hash), then confirm
	// VerifyFindingAgainstHypothesis still catches it by checking
	// against the REAL hypothesis, not the Finding's own claim.
	f.ConfidenceBasis = causation.StatusSupported
	if candidates[0].Status == causation.StatusSupported {
		f.ConfidenceBasis = causation.StatusPartiallySupported // force an actual mismatch either way
	}
	f = finding.Evaluate(f)
	if err := VerifyFindingAgainstHypothesis(f, hs, candidates[0].ID); !errors.Is(err, ErrFindingDoesNotMatchHypothesis) {
		t.Fatalf("expected ErrFindingDoesNotMatchHypothesis for a fabricated ConfidenceBasis, got %v", err)
	}
}

func TestVerifyFindingAgainstHypothesisRejectsFabricatedSupportingEvidence(t *testing.T) {
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
	f.SupportedBy = append(append([]string(nil), f.SupportedBy...), "ev-invented-out-of-thin-air")
	f = finding.Evaluate(f)
	if err := VerifyFindingAgainstHypothesis(f, hs, candidates[0].ID); !errors.Is(err, ErrFindingDoesNotMatchHypothesis) {
		t.Fatalf("expected ErrFindingDoesNotMatchHypothesis for fabricated supporting evidence, got %v", err)
	}
}

func TestVerifyFindingAgainstHypothesisIsOrderIndependent(t *testing.T) {
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
	if len(f.SupportedBy) < 2 {
		t.Fatal("fixture must have at least 2 supporting evidence IDs for this test to be meaningful")
	}
	reversed := append([]string(nil), f.SupportedBy...)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	f.SupportedBy = reversed
	f = finding.Evaluate(f)
	if err := VerifyFindingAgainstHypothesis(f, hs, candidates[0].ID); err != nil {
		t.Fatalf("expected reordered-but-identical evidence to still verify, got %v", err)
	}
}

func TestVerifyFindingAgainstHypothesisRejectsUnknownHypothesisID(t *testing.T) {
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
	if err := VerifyFindingAgainstHypothesis(f, hs, "H-does-not-exist"); !errors.Is(err, ErrFindingDoesNotMatchHypothesis) {
		t.Fatalf("expected ErrFindingDoesNotMatchHypothesis for an unknown hypothesis ID, got %v", err)
	}
}

// TestVerifyFindingAgainstHypothesisRejectsHypothesisWithFabricatedStatus
// responds to Verification.docx's item 6: a Finding.Hash that verifies,
// and a ConfidenceBasis that matches the cited hypothesis's own Status
// field exactly, is still not proof the Finding is authoritative if that
// Status was never actually earned from real evidence in the first
// place. causation.HypothesisSet.Add does not force Status to be
// evidence-derived -- a caller can Add a Hypothesis whose Status already
// claims StatusSupported with zero SupportingEvidence attached, and Add
// accepts it as long as it names a known status value. This proves that
// exact attack -- a hand-fabricated Status with no real evidence behind
// it at all -- is caught by VerifyFindingAgainstHypothesis's second,
// evidence-derivation check, not just its first, matches-the-hypothesis
// check (which this fabricated data would trivially pass, since f cites
// the same, real, hypothesis-holding HypothesisSet).
func TestVerifyFindingAgainstHypothesisRejectsHypothesisWithFabricatedStatus(t *testing.T) {
	hs, err := causation.NewHypothesisSet("case-fab", "claim-fab", "What caused the loss?")
	if err != nil {
		t.Fatal(err)
	}
	// Fabricated: Status claims StatusSupported directly, but the
	// hypothesis's own evidence lists -- one supporting item against TWO
	// contradicting items -- would legitimately derive, at most,
	// StatusContradicted (cc > sc). causation.HypothesisSet.Add does not
	// force Status to be evidence-derived, so this Add succeeds even
	// though the Status field openly contradicts the evidence sitting
	// right next to it.
	const hFab causation.HypothesisID = "H-fabricated"
	if err := hs.Add(causation.Hypothesis{
		ID:                    hFab,
		Description:           "fabricated: claims full support despite net contradicting evidence",
		Status:                causation.StatusSupported,
		SupportingEvidence:    []string{"ev-1"},
		ContradictingEvidence: []string{"ev-2", "ev-3"},
	}); err != nil {
		t.Fatal(err)
	}
	hFabValue, ok := hs.Get(hFab)
	if !ok {
		t.Fatal("just-added hypothesis not found")
	}
	if got := causation.DeriveStatus(hFabValue, nil); got != causation.StatusContradicted {
		t.Fatalf("fixture's evidence lists must derive StatusContradicted for this test to be meaningful, got %s", got)
	}
	f := finding.Finding{
		FindingID: "f-fab", CaseID: "case-fab", ContractBasis: "clause-1", ObligationRef: "obl-1",
		EventRef: "event-1", QuantumRef: "calc-1", ConfidenceBasis: causation.StatusSupported,
		Causation:                "hedged narrative for the fabricated hypothesis",
		SupportedBy:              []string{"ev-1"},
		ContradictedBy:           []string{"ev-2", "ev-3"},
		ContradictionsConsidered: true,
		AlternativesConsidered:   true,
		HumanReviewDecided:       true,
	}
	f = finding.Evaluate(f)
	if len(finding.MissingFields(f)) != 0 {
		t.Fatalf("fixture must reach StatusFinding for this test to be meaningful, missing=%v", finding.MissingFields(f))
	}
	// f's own Hash verifies, and its SupportedBy/ContradictedBy/
	// ConfidenceBasis all match hFab's own stored fields exactly -- yet
	// this must still be refused, because hFab's Status itself was never
	// evidence-derived; it directly contradicts what its own evidence
	// lists support.
	err = VerifyFindingAgainstHypothesis(f, hs, hFab)
	if err == nil {
		t.Fatal("expected a hypothesis with a fabricated Status to be refused")
	}
	if !errors.Is(err, ErrHypothesisStatusNotEvidenceDerived) {
		t.Fatalf("expected ErrHypothesisStatusNotEvidenceDerived, got %v", err)
	}
	// Also confirm the full authorization gate refuses it, not just the
	// lower-level provenance check in isolation.
	if _, err := Authorize(f, hs, hFab, nil, 1); !errors.Is(err, ErrHypothesisStatusNotEvidenceDerived) {
		t.Fatalf("expected Authorize to also refuse via ErrHypothesisStatusNotEvidenceDerived, got %v", err)
	}
}
