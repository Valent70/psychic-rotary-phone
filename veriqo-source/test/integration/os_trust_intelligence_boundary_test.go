// This file answers the reviewer's item F directly: "AI layer boleh
// detect/infer/rank/score/hypothesize/recommend, tetapi authoritative
// state harus tetap melewati Evidence -> Truth validation -> Finding ->
// Authorization." (The AI layer may detect/infer/rank/score/hypothesize/
// recommend, but authoritative state must still pass through Evidence
// -> truth validation -> Finding -> Authorization.) In this
// repository's own vocabulary: Intelligence -> Hypothesis (informing,
// legitimate), never Intelligence -> AuthorizedFinding directly
// (forbidden, and proven forbidden here, not just asserted).
package integration

import (
	"errors"
	"testing"

	"veriqo/pkg/governance/lifecycle"
	"veriqo/pkg/inference"
	"veriqo/pkg/insurance/causation"
	"veriqo/pkg/insurance/cre"
)

// activeInferenceRecorder builds a real, lifecycle-gated
// *inference.Recorder backed by a genuinely ACTIVE model -- the same
// governance path TestAuthorityCannotBeLostAcrossThePipeline already
// uses, reused here rather than re-invented.
func activeInferenceRecorder(t *testing.T, modelID string, tick uint64) (*inference.Recorder, string) {
	t.Helper()
	reg := lifecycle.NewRegistry()
	modelEvent, err := reg.RegisterModel(lifecycle.Model{
		ModelID: modelID, Version: "1.0.0", Type: "causal-hypothesis-ranker",
		ParametersHash: "p", TrainingDataHash: "d",
	}, "ml-engineer", tick)
	if err != nil {
		t.Fatalf("RegisterModel: %v", err)
	}
	key := modelEvent.Key
	if err := reg.SetCalibration(key, "calib-1"); err != nil {
		t.Fatalf("SetCalibration: %v", err)
	}
	for _, to := range []lifecycle.ModelState{lifecycle.ModelValidated, lifecycle.ModelCalibrated} {
		if _, err := reg.TransitionModel(key, to, "ml-engineer", "", "", tick, nil); err != nil {
			t.Fatalf("TransitionModel(%s): %v", to, err)
		}
	}
	if _, err := reg.TransitionModel(key, lifecycle.ModelApproved, "ml-engineer", "gov-lead", "ok", tick, nil); err != nil {
		t.Fatalf("TransitionModel(Approved): %v", err)
	}
	if _, err := reg.TransitionModel(key, lifecycle.ModelActive, "ml-engineer", "gov-lead", "go", tick, nil); err != nil {
		t.Fatalf("TransitionModel(Active): %v", err)
	}
	return inference.NewRecorder(reg), key
}

// TestIntelligenceOutputCanOnlyInformAHypothesisNeverAssertAFinding
// drives the AI layer through every legitimate action the reviewer
// named (detect/infer/rank/score/hypothesize/recommend -- all modelled
// here as a single real inference.InferenceTrace, since this
// repository has exactly one governed AI-output type), then proves,
// adversarially, that none of the three plausible shortcuts from that
// output to authoritative state succeed.
func TestIntelligenceOutputCanOnlyInformAHypothesisNeverAssertAFinding(t *testing.T) {
	const tick uint64 = 1
	recorder, modelKey := activeInferenceRecorder(t, "intel-boundary-model", tick)
	trace, err := recorder.Record(modelKey, "input-hash-1",
		"recommend: primary hypothesis is well-supported, confidence high", 0.92,
		"ai-engine", "case_resolution", tick)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	// ---- Shortcut attempt 1: inject the AI's own recommended status
	// directly onto a Hypothesis via Add, bypassing computeStatus
	// entirely. Already closed by an earlier round's HypothesisSet.Add
	// fix -- re-verified here under the EXACT "Intelligence recommends
	// a verdict" framing this reviewer named. ----
	hs, err := causation.NewHypothesisSet("case-intel-1", "claim-intel-1", "What caused the loss?")
	if err != nil {
		t.Fatal(err)
	}
	const hID causation.HypothesisID = "H1"
	if err := hs.Add(causation.Hypothesis{
		ID: hID, Description: "primary hypothesis",
		Status: causation.StatusSupported, // the AI's own recommended verdict, asserted directly
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	stored, ok := hs.Get(hID)
	if !ok {
		t.Fatal("hypothesis not found")
	}
	if stored.Status != causation.StatusUnproven {
		t.Fatalf("Intelligence -> Hypothesis shortcut 1: expected Add to discard the AI-recommended Status and store StatusUnproven, got %q -- an AI recommendation must never become an asserted Hypothesis verdict", stored.Status)
	}

	// ---- Shortcut attempt 2: build a Finding whose ConfidenceBasis
	// claims the AI's recommended verdict, citing the real trace for
	// provenance, but with NO real supporting evidence behind the
	// hypothesis at all (mirrors "Intelligence recommends, so we skip
	// straight to a Finding"). ----
	if err := hs.AddSupportingEvidence(hID, "EV-INTEL-1"); err != nil {
		t.Fatal(err)
	}
	if err := hs.AddContradictingEvidence(hID, "EV-INTEL-2"); err != nil {
		t.Fatal(err)
	}
	if err := hs.AddContradictingEvidence(hID, "EV-INTEL-3"); err != nil {
		t.Fatal(err)
	}
	h, _ := hs.Get(hID)
	if h.Status == causation.StatusSupported {
		t.Fatal("test setup: expected the real evidence lists (1 supporting, 2 contradicting) to NOT derive StatusSupported, so shortcut 2 is a genuine escalation attempt")
	}
	forgedFinding, err := cre.BuildFinding(hs, h, nil, cre.FindingInput{
		CaseID: "case-intel-1", ContractBasis: "clause-1", ObligationRef: "obl-1",
		EventRef: "event-1", QuantumRef: "calc-1", SourceInferenceTraceID: trace.TraceID,
		HumanReviewRequired: true,
	}, "finding-intel-shortcut", tick)
	if err != nil {
		t.Fatalf("BuildFinding: %v", err)
	}
	// BuildFinding itself is honest (ConfidenceBasis = h.Status, the
	// REAL derived status) -- the adversarial step is escalating it
	// afterward to what the AI recommended, exactly as a caller
	// tempted to "trust the AI" might.
	forgedFinding.ConfidenceBasis = causation.StatusSupported
	if _, err := cre.Authorize(forgedFinding, hs, hID, []inference.InferenceTrace{trace}, tick); err == nil {
		t.Fatal("Intelligence -> Finding shortcut 2: expected Authorize to refuse a Finding whose ConfidenceBasis was escalated to the AI's recommended verdict rather than the hypothesis's real, evidence-derived status")
	}

	// ---- Shortcut attempt 3: cite a trace that doesn't actually back
	// this Finding (a fabricated SourceInferenceTraceID, or one for a
	// DIFFERENT recommendation) -- "the AI said so" is not provenance
	// unless the SPECIFIC cited trace is real and on file. ----
	honestFinding, err := cre.BuildFinding(hs, h, nil, cre.FindingInput{
		CaseID: "case-intel-1", ContractBasis: "clause-1", ObligationRef: "obl-1",
		EventRef: "event-1", QuantumRef: "calc-1", SourceInferenceTraceID: "trace-that-does-not-exist",
		HumanReviewRequired: true,
	}, "finding-intel-honest", tick)
	if err != nil {
		t.Fatalf("BuildFinding: %v", err)
	}
	if _, err := cre.Authorize(honestFinding, hs, hID, []inference.InferenceTrace{trace}, tick); !errors.Is(err, cre.ErrInferenceTraceNotFound) {
		t.Fatalf("Intelligence -> Finding shortcut 3: expected cre.ErrInferenceTraceNotFound for a Finding citing a nonexistent trace, got %v", err)
	}

	// ---- The one legitimate path: Intelligence output informs (is
	// cited by) a Finding whose ConfidenceBasis is the REAL,
	// evidence-derived Hypothesis status -- here CONTRADICTED, since
	// the AI's own "well-supported" recommendation was wrong and the
	// real evidence overrides it. This is Intelligence -> Hypothesis ->
	// (informs) -> Finding, never Intelligence -> Finding directly. ----
	if h.Status != causation.StatusContradicted {
		t.Fatalf("test setup: expected the real evidence to derive StatusContradicted, got %s", h.Status)
	}
	legitimateFinding, err := cre.BuildFinding(hs, h, nil, cre.FindingInput{
		CaseID: "case-intel-1", ContractBasis: "clause-1", ObligationRef: "obl-1",
		EventRef: "event-1", QuantumRef: "calc-1", SourceInferenceTraceID: trace.TraceID,
		HumanReviewRequired: true,
	}, "finding-intel-legitimate", tick)
	if err != nil {
		t.Fatalf("BuildFinding: %v", err)
	}
	af, err := cre.Authorize(legitimateFinding, hs, hID, []inference.InferenceTrace{trace}, tick)
	if err != nil {
		t.Fatalf("expected the legitimate path (Finding's ConfidenceBasis honestly reflects the real derived Hypothesis status, AI recommendation notwithstanding) to authorize: %v", err)
	}
	if af.IsZero() {
		t.Fatal("expected a populated AuthorizedFinding")
	}
	if af.Finding().ConfidenceBasis != causation.StatusContradicted {
		t.Fatalf("expected the authorized Finding to honestly carry StatusContradicted (the real derived status) rather than the AI's own recommended StatusSupported, got %s", af.Finding().ConfidenceBasis)
	}
}
