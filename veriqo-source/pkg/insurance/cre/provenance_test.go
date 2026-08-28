package cre

import (
	"errors"
	"testing"

	"veriqo/pkg/governance/lifecycle"
	"veriqo/pkg/inference"
	"veriqo/pkg/insurance/evidence"
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
