// TestAuthorityCannotBeLostAcrossThePipeline responds to Verification.docx's
// item 7: proving not just that GenerateFindings() produces an
// AuthorizedFinding, but that authority survives the WHOLE pipeline --
// Detection -> Inference -> Finding -> Authorization -> AuthorizedFinding
// -> Decision -> Reporting -- with no point along the way where a bare,
// unauthorized finding.Finding could be substituted for the real thing.
//
// decide() and report() below are illustrative downstream consumers,
// typed to require cre.AuthorizedFinding, never finding.Finding. That
// typing is itself the proof for the reviewer's "must not happen"
// diagram:
//
//	AuthorizedFinding -> cast/rebuild -> Raw Finding -> Decision
//
// The following does NOT compile, and this comment records that fact
// rather than asserting it at runtime (Go has no "must not compile"
// test primitive without extra tooling):
//
//	raw := authorized.Finding()   // raw has type finding.Finding
//	decide(raw)                   // compile error: finding.Finding is not cre.AuthorizedFinding
//
// What this test DOES prove at runtime: the full positive pipeline
// compiles and runs end to end with real packages at every stage, AND a
// sophisticated attempt to re-inject a modified Finding back into the
// authorization boundary (extract, escalate a claim, recompute the
// hash to stay self-consistent, try to re-authorize) is refused by the
// same gate a first-time forgery would hit.
package integration

import (
	"fmt"
	"testing"

	"veriqo/pkg/canonical/jcs"
	"veriqo/pkg/evidence/manifest"
	"veriqo/pkg/governance/lifecycle"
	"veriqo/pkg/inference"
	"veriqo/pkg/insurance/causation"
	"veriqo/pkg/insurance/cre"
	"veriqo/pkg/insurance/evidence"
	"veriqo/pkg/insurance/finding"
	"veriqo/pkg/ontology"
)

// decide is a stand-in Decision stage: its signature requires an
// AuthorizedFinding, so there is no way to call it with a bare
// finding.Finding, forged or not.
func decide(a cre.AuthorizedFinding) string {
	f := a.Finding()
	return fmt.Sprintf("DECISION: escalate for human review on %s (confidence_basis=%s, authorized_at=%d)",
		f.FindingID, f.ConfidenceBasis, a.AuthorizedAt())
}

// report is a stand-in Reporting stage, same discipline as decide.
func report(a cre.AuthorizedFinding) string {
	return fmt.Sprintf("REPORT: finding=%s authorization_hash=%s", a.Finding().FindingID, a.AuthorizationHash())
}

func TestAuthorityCannotBeLostAcrossThePipeline(t *testing.T) {
	const tick uint64 = 1
	const tenantID = "tenant-pipeline"
	const caseID = "case-pipeline-001"

	// ---- Detection: real grounded evidence ----
	ont := ontology.NewRegistry()
	evidenceObj, err := ont.CreateObject(ontology.Object{
		ObjectType: ontology.ObjectEvidence, ObjectID: "evidence-pipeline-1", TenantID: tenantID,
	}, "cre-system", tick, nil)
	if err != nil {
		t.Fatalf("Detection: CreateObject: %v", err)
	}
	manifests := manifest.NewRegistry()
	draft, err := manifests.RegisterDraft(manifest.Manifest{
		TenantID: tenantID, CaseID: caseID, EvidenceID: evidenceObj.ObjectID, Version: 1,
		URI: "evidence://pipeline-survey-1.pdf", Filename: "pipeline-survey-1.pdf", MediaType: "application/pdf",
		ByteSize: 2048, SHA256: "cc33dd44ee55ff66aa11bb22cc33dd44ee55ff66aa11bb22cc33dd44ee55ff6",
		Method: "UPLOAD", Collector: "surveyor-pipeline", Source: "independent-surveyor", AcquiredAt: tick, ReceivedAt: tick,
	})
	if err != nil {
		t.Fatalf("Detection: RegisterDraft: %v", err)
	}
	cur := draft
	for _, s := range []manifest.State{manifest.StateIngested, manifest.StateIntegrityAssessed,
		manifest.StateProvenanceComplete, manifest.StateReadyForFinalization, manifest.StateFinalized} {
		cur, err = manifests.Advance(evidenceObj.ObjectID, s, tick)
		if err != nil {
			t.Fatalf("Detection: Advance(%s): %v", s, err)
		}
	}
	// A second, minor piece of contradicting evidence -- also grounded,
	// so the finding legitimately cites two real evidence items rather
	// than one real and one merely-referenced-by-string.
	if _, err := manifests.RegisterDraft(manifest.Manifest{
		TenantID: tenantID, CaseID: caseID, EvidenceID: "ev-minor-contradiction", Version: 1,
		URI: "evidence://pipeline-counterpoint-1.pdf", Filename: "pipeline-counterpoint-1.pdf", MediaType: "application/pdf",
		ByteSize: 512, SHA256: "dd44ee55ff66aa11bb22cc33dd44ee55ff66aa11bb22cc33dd44ee55ff66aa1",
		Method: "UPLOAD", Collector: "surveyor-pipeline", Source: "independent-surveyor", AcquiredAt: tick, ReceivedAt: tick,
	}); err != nil {
		t.Fatalf("Detection: RegisterDraft (contradicting): %v", err)
	}
	for _, s := range []manifest.State{manifest.StateIngested, manifest.StateIntegrityAssessed,
		manifest.StateProvenanceComplete, manifest.StateReadyForFinalization, manifest.StateFinalized} {
		if _, err := manifests.Advance("ev-minor-contradiction", s, tick); err != nil {
			t.Fatalf("Detection: Advance (contradicting) (%s): %v", s, err)
		}
	}

	// ---- Inference: a governed local trace, gated on an ACTIVE model ----
	lifecycleReg := lifecycle.NewRegistry()
	modelEvent, err := lifecycleReg.RegisterModel(lifecycle.Model{
		ModelID: "pipeline-ranker", Version: "1.0.0", Type: "causal-hypothesis-ranker",
		ParametersHash: "p", TrainingDataHash: "d",
	}, "ml-engineer", tick)
	if err != nil {
		t.Fatalf("Inference: RegisterModel: %v", err)
	}
	modelKey := modelEvent.Key
	if err := lifecycleReg.SetCalibration(modelKey, "calib-1"); err != nil {
		t.Fatal(err)
	}
	for _, to := range []lifecycle.ModelState{lifecycle.ModelValidated, lifecycle.ModelCalibrated} {
		if _, err := lifecycleReg.TransitionModel(modelKey, to, "ml-engineer", "", "", tick, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := lifecycleReg.TransitionModel(modelKey, lifecycle.ModelApproved, "ml-engineer", "gov-lead", "ok", tick, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := lifecycleReg.TransitionModel(modelKey, lifecycle.ModelActive, "ml-engineer", "gov-lead", "go", tick, nil); err != nil {
		t.Fatal(err)
	}
	recorder := inference.NewRecorder(lifecycleReg)
	inputHash := jcs.MustHash(map[string]string{"evidence_id": evidenceObj.ObjectID, "manifest_hash": cur.ManifestHash})
	trace, err := recorder.Record(modelKey, inputHash, "ranked primary hypothesis first", 0.6, "cre-engine", "case_resolution", tick)
	if err != nil {
		t.Fatalf("Inference: Record: %v", err)
	}

	// ---- Finding: real causation reasoning ----
	hs, err := causation.NewHypothesisSet(caseID, "claim-pipeline", "What caused the loss?")
	if err != nil {
		t.Fatal(err)
	}
	const hPrimary causation.HypothesisID = "H1"
	if err := hs.Add(causation.Hypothesis{ID: hPrimary, Description: "primary hypothesis"}); err != nil {
		t.Fatal(err)
	}
	if err := hs.AddSupportingEvidence(hPrimary, evidenceObj.ObjectID); err != nil {
		t.Fatal(err)
	}
	// A contradicting entry too, so the real status lands at
	// PARTIALLY_SUPPORTED rather than SUPPORTED -- leaving room for the
	// adversarial escalation attempt below to actually escalate to a
	// different status, rather than trivially matching it already.
	if err := hs.AddContradictingEvidence(hPrimary, "ev-minor-contradiction"); err != nil {
		t.Fatal(err)
	}

	// ---- Authorization: GenerateFindings runs BuildFinding then Authorize internally ----
	authorized, err := cre.GenerateFindings(hs, evidence.NewDependencyGraph(), cre.FindingInput{
		CaseID: caseID, ContractBasis: "clause-1", ObligationRef: "obl-1", EventRef: "event-1",
		QuantumRef: "calc-1", SourceInferenceTraceID: trace.TraceID, HumanReviewRequired: true,
	}, []inference.InferenceTrace{trace}, "pipeline-finding", tick)
	if err != nil {
		t.Fatalf("Authorization: GenerateFindings: %v", err)
	}
	if len(authorized) != 1 {
		t.Fatalf("expected exactly 1 authorized finding, got %d", len(authorized))
	}
	af := authorized[0]
	if af.IsZero() {
		t.Fatal("expected a populated AuthorizedFinding")
	}

	// Also prove the STRICTER, evidence-hash-grounded gate accepts the
	// same real pipeline data.
	rawForGrounding, err := cre.BuildFinding(hs, mustGet(t, hs, hPrimary), evidence.NewDependencyGraph(), cre.FindingInput{
		CaseID: caseID, ContractBasis: "clause-1", ObligationRef: "obl-1", EventRef: "event-1",
		QuantumRef: "calc-1", SourceInferenceTraceID: trace.TraceID, HumanReviewRequired: true,
	}, "pipeline-finding-grounded", tick)
	if err != nil {
		t.Fatalf("BuildFinding (for grounded check): %v", err)
	}
	if _, err := cre.AuthorizeGrounded(rawForGrounding, hs, hPrimary, []inference.InferenceTrace{trace}, manifests, tick); err != nil {
		t.Fatalf("AuthorizeGrounded on real pipeline data: %v", err)
	}

	// ---- Decision & Reporting: only reachable with an AuthorizedFinding ----
	decisionOutput := decide(af)
	reportOutput := report(af)
	if decisionOutput == "" || reportOutput == "" {
		t.Fatal("expected non-empty decision/report output")
	}
	t.Logf("%s", decisionOutput)
	t.Logf("%s", reportOutput)

	// ---- Adversarial: extract, escalate, recompute hash, try to re-enter ----
	tampered := af.Finding() // independent copy, per the immutability fix
	original := tampered.ConfidenceBasis
	tampered.ConfidenceBasis = causation.StatusSupported // attempt to escalate the claim
	if tampered.ConfidenceBasis == original {
		t.Skip("fixture's real ConfidenceBasis already equals the escalation target; adjust fixture")
	}
	tampered = finding.Evaluate(tampered) // attacker recomputes hash to stay self-consistent
	if _, err := cre.Authorize(tampered, hs, hPrimary, []inference.InferenceTrace{trace}, tick); err == nil {
		t.Fatal("expected re-authorizing an escalated, hand-tampered Finding to be refused")
	}
	if _, err := cre.AuthorizeGrounded(tampered, hs, hPrimary, []inference.InferenceTrace{trace}, manifests, tick); err == nil {
		t.Fatal("expected the grounded gate to also refuse the escalated, hand-tampered Finding")
	}
	// The original AuthorizedFinding's own state must be completely
	// unaffected by any of the above -- decide()/report() still work
	// identically.
	if got := decide(af); got != decisionOutput {
		t.Fatalf("expected af's own state to be unaffected by the adversarial attempt, got %q want %q", got, decisionOutput)
	}
}

func mustGet(t *testing.T, hs *causation.HypothesisSet, id causation.HypothesisID) causation.Hypothesis {
	t.Helper()
	h, ok := hs.Get(id)
	if !ok {
		t.Fatalf("hypothesis %s not found", id)
	}
	return h
}
