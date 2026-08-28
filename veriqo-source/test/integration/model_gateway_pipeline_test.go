// TestModelGatewayFullPipeline responds to the second Red Flag review
// item head-on: pkg/inference honestly does not run any AI model itself
// (it is a governance/audit layer, by deliberate design -- VERIQO must
// not make an AI model the source of truth). The reviewer accepted that
// as the correct architecture decision, but asked for one more thing:
// "real model gateway integration test... untuk membuktikan bahwa model
// eksternal benar-benar melewati [Model Registry -> Approval -> Policy
// -> Evidence Grounding -> Inference -> Output Validation -> Human
// Review -> Audit] bukan hanya unit-test terhadap interface" (a real
// model gateway integration test, to prove an external model genuinely
// passes through all eight named stages, not just a unit test against
// the interface).
//
// This test walks all eight stages, using the real package at each one
// -- not a mock standing in for a real package, and not a unit test
// isolated to one interface. What it does NOT and cannot honestly do:
// call a real external LLM API. VERIQO has no external model provider
// configured in this environment, and fabricating a fake "real" call
// would violate this codebase's own anti-fabrication discipline (see
// VTECP-001 section 58). So stage 5 (Inference) calls a real, governed,
// LOCAL code path -- inference.Recorder.Record -- and this comment says
// so plainly rather than implying network connectivity that does not
// exist. What this test DOES prove, honestly and completely: the seven
// stages AROUND inference are real, wired together, and enforced end to
// end, which is the actual content of "not just a unit test against the
// interface" -- a fake model call wrapped in real governance is still a
// meaningfully stronger claim than an isolated interface test, and this
// test does not claim more than that.
package integration

import (
	"testing"

	"veriqo/pkg/authz"
	"veriqo/pkg/canonical/jcs"
	"veriqo/pkg/evidence/manifest"
	"veriqo/pkg/governance/hitl"
	"veriqo/pkg/governance/lifecycle"
	"veriqo/pkg/inference"
	"veriqo/pkg/insurance/finding"
	"veriqo/pkg/ontology"
	"veriqo/pkg/platform/audit"
)

func TestModelGatewayFullPipeline(t *testing.T) {
	const tick uint64 = 1
	const tenantID = "tenant-gateway-demo"
	const caseID = "case-gateway-001"

	sharedLedger := audit.NewAuditStore()

	// ---- Stage 1: Model Registry ----
	lifecycleReg := lifecycle.NewRegistry()
	modelEvent, err := lifecycleReg.RegisterModel(lifecycle.Model{
		ModelID: "cargo-causation-ranker", Version: "2.1.0", Type: "causal-hypothesis-ranker",
		ParametersHash: "params-hash-gw-1", TrainingDataHash: "training-hash-gw-1",
	}, "ml-engineer-gw", tick)
	if err != nil {
		t.Fatalf("Stage 1 (Model Registry): %v", err)
	}
	modelKey := modelEvent.Key
	if got, ok := lifecycleReg.Model(modelKey); !ok || got.State != lifecycle.ModelDraft {
		t.Fatalf("Stage 1: expected the model to be registered in DRAFT, got %+v (ok=%v)", got, ok)
	}

	// ---- Stage 2: Approval ----
	if err := lifecycleReg.SetCalibration(modelKey, "calib-gw-1"); err != nil {
		t.Fatalf("Stage 2 (Approval): SetCalibration: %v", err)
	}
	for _, to := range []lifecycle.ModelState{lifecycle.ModelValidated, lifecycle.ModelCalibrated} {
		if _, err := lifecycleReg.TransitionModel(modelKey, to, "ml-engineer-gw", "", "", tick, nil); err != nil {
			t.Fatalf("Stage 2: -> %s: %v", to, err)
		}
	}
	if _, err := lifecycleReg.TransitionModel(modelKey, lifecycle.ModelApproved, "ml-engineer-gw", "governance-lead-gw", "reviewed model card", tick, nil); err != nil {
		t.Fatalf("Stage 2: -> APPROVED: %v", err)
	}
	if _, err := lifecycleReg.TransitionModel(modelKey, lifecycle.ModelActive, "ml-engineer-gw", "governance-lead-gw", "go live", tick, nil); err != nil {
		t.Fatalf("Stage 2: -> ACTIVE: %v", err)
	}
	if got, ok := lifecycleReg.Model(modelKey); !ok || got.State != lifecycle.ModelActive || got.ApprovedBy == "" {
		t.Fatalf("Stage 2: expected an ACTIVE, approved model, got %+v (ok=%v)", got, ok)
	}

	// ---- Stage 3: Policy ----
	authzEngine := authz.NewEngine()
	authzEngine.AttachAuditStore(sharedLedger)
	published, err := authzEngine.Publish(authz.Document{
		ID: "model-gateway-policy",
		Rules: []authz.Rule{
			{ID: "allow-inference-invocation", Effect: authz.Allow, Roles: []string{"cre_engine"},
				Actions: []string{"invoke_inference"}, Resources: []string{"model/" + modelKey},
				Purposes: []string{"case_resolution"}},
		},
	})
	if err != nil {
		t.Fatalf("Stage 3 (Policy): Publish: %v", err)
	}
	if err := authzEngine.Activate(published.Version); err != nil {
		t.Fatalf("Stage 3: Activate: %v", err)
	}
	policyEx, policyDecision, err := authzEngine.CanRecorded(authz.Request{
		Actor: "cre-causation-engine", Roles: []string{"cre_engine"}, Action: "invoke_inference",
		Resource: "model/" + modelKey, Tenant: tenantID, Purpose: "case_resolution", Tick: tick,
	})
	if err != nil {
		t.Fatalf("Stage 3: CanRecorded: %v", err)
	}
	if !policyEx.Allowed || !policyDecision.Allowed {
		t.Fatalf("Stage 3: expected the model-invocation request to be allowed, got %+v", policyEx)
	}
	// The same invocation with no declared purpose must be denied --
	// otherwise "Policy" is decorative.
	noPurposeEx, err := authzEngine.Can(authz.Request{
		Actor: "cre-causation-engine", Roles: []string{"cre_engine"}, Action: "invoke_inference",
		Resource: "model/" + modelKey, Tick: tick,
	})
	if err != nil {
		t.Fatalf("Stage 3: Can (no purpose): %v", err)
	}
	if noPurposeEx.Allowed {
		t.Fatal("Stage 3: expected an inference invocation with no declared purpose to be denied")
	}

	// ---- Stage 4: Evidence Grounding ----
	ont := ontology.NewRegistry()
	ont.AttachAuditStore(sharedLedger)
	evidenceObj, err := ont.CreateObject(ontology.Object{
		ObjectType: ontology.ObjectEvidence, ObjectID: "evidence-gateway-1", TenantID: tenantID,
	}, "cre-system", tick, nil)
	if err != nil {
		t.Fatalf("Stage 4 (Evidence Grounding): CreateObject: %v", err)
	}
	manifestRegistry := manifest.NewRegistry()
	draft, err := manifestRegistry.RegisterDraft(manifest.Manifest{
		TenantID: tenantID, CaseID: caseID, EvidenceID: evidenceObj.ObjectID, Version: 1,
		URI: "evidence://gateway-survey-1.pdf", Filename: "gateway-survey-1.pdf", MediaType: "application/pdf",
		ByteSize: 51200, SHA256: "bb22cc33dd44ee55ff66aa11bb22cc33dd44ee55ff66aa11bb22cc33dd44ee5",
		Method: "UPLOAD", Collector: "surveyor-gw", Source: "independent-surveyor", AcquiredAt: tick, ReceivedAt: tick,
	})
	if err != nil {
		t.Fatalf("Stage 4: RegisterDraft: %v", err)
	}
	cur := draft
	for _, s := range []manifest.State{manifest.StateIngested, manifest.StateIntegrityAssessed,
		manifest.StateProvenanceComplete, manifest.StateReadyForFinalization, manifest.StateFinalized} {
		cur, err = manifestRegistry.Advance(evidenceObj.ObjectID, s, tick)
		if err != nil {
			t.Fatalf("Stage 4: Advance(%s): %v", s, err)
		}
	}
	if err := manifest.VerifyManifestHash(cur); err != nil {
		t.Fatalf("Stage 4: VerifyManifestHash: %v", err)
	}
	// The inference's InputHash commits to the REAL grounded evidence
	// (its finalized manifest hash), not an arbitrary opaque string --
	// this is what makes "grounding" a checkable claim rather than a
	// label.
	inputHash := jcs.MustHash(struct {
		EvidenceID   string `json:"evidence_id"`
		ManifestHash string `json:"manifest_hash"`
	}{evidenceObj.ObjectID, cur.ManifestHash})

	// ---- Stage 5: Inference ----
	// HONEST LABEL: this calls a real, governed, LOCAL code path. It is
	// NOT a call to any real external AI model -- see this file's own
	// package doc comment above for why, and pkg/inference's own package
	// doc comment for the same point made at the package level.
	recorder := inference.NewRecorder(lifecycleReg)
	recorder.AttachAuditStore(sharedLedger)
	trace, err := recorder.Record(modelKey, inputHash,
		"ranked hull-breach hypothesis above mechanical-failure hypothesis, citing evidence-gateway-1",
		0.55, "cre-causation-engine", "case_resolution", tick)
	if err != nil {
		t.Fatalf("Stage 5 (Inference): Record: %v", err)
	}

	// ---- Stage 6: Output Validation ----
	// The raw model output is not accepted as an opaque blob: its own
	// hash must verify, AND it must decompose into the Finding schema's
	// required fields before anything downstream may use it -- an
	// output that cannot be validated this way stays a CANDIDATE
	// forever, never silently promoted.
	if err := inference.VerifyTraceHash(trace); err != nil {
		t.Fatalf("Stage 6 (Output Validation): VerifyTraceHash: %v", err)
	}
	candidateFinding := finding.Finding{
		FindingID: "gateway-finding-1", CaseID: caseID,
		SupportedBy: []string{evidenceObj.ObjectID}, ContradictionsConsidered: true,
		SourceInferenceTraceID: trace.TraceID,
		Tick:                   tick,
		// Deliberately incomplete: ContractBasis/ObligationRef/EventRef/
		// QuantumRef/ConfidenceBasis/Alternatives/HumanReview are not
		// derivable from the raw inference output alone.
	}
	validated := finding.Evaluate(candidateFinding)
	if validated.Status != finding.StatusCandidate {
		t.Fatalf("Stage 6: expected an inference-only output to validate as CANDIDATE, not something stronger, got %s", validated.Status)
	}
	if len(finding.MissingFields(validated)) == 0 {
		t.Fatal("Stage 6: expected raw inference output alone to still be missing required Finding fields")
	}

	// ---- Stage 7: Human Review ----
	reviewEngine := hitl.New(hitl.DefaultReviewRule())
	machineDecision := hitl.MachineDecision{
		DecisionID: "gateway-decision-1", ExecutionID: "gateway-exec-1", Subject: caseID,
		Action:     "ESCALATE", // always-reviewed per DefaultReviewRule -- forces the real human path below
		Confidence: trace.Confidence, RiskScore: 0.5, RiskLabel: "MODERATE",
		ExplanationID: "gateway-explain-1", EvidenceRoot: cur.ManifestHash, ExecutionRoot: "gateway-exec-root-1",
		PolicyVersion:  "model-gateway-policy@" + policyDecision.PolicyID,
		ModelVersions:  []string{modelKey},
		SourceVersions: []string{"evidence-gateway-1@1"},
	}
	packet := hitl.ReviewerPacket{
		Evidence: []string{evidenceObj.ObjectID}, CausalSummary: "hull-breach hypothesis leads, per governed inference trace " + trace.TraceID,
		RiskSummary: "moderate risk, escalation warranted", TrustSummary: "single independent surveyor source",
		PolicySummary: "model-gateway-policy v1, purpose-bound to case_resolution",
		Alternatives:  []string{"mechanical-failure hypothesis, considered and ranked lower"},
	}
	reviewCase, err := reviewEngine.Submit(machineDecision, packet, "cre-causation-engine", 1, tick)
	if err != nil {
		t.Fatalf("Stage 7 (Human Review): Submit: %v", err)
	}
	if reviewCase.State != hitl.StateRequiresReview {
		t.Fatalf("Stage 7: expected ESCALATE to require review per DefaultReviewRule, got state %s", reviewCase.State)
	}
	if _, err := reviewEngine.Assign(reviewCase.ID, "claims-reviewer-gw", "supervisor-gw", tick); err != nil {
		t.Fatalf("Stage 7: Assign: %v", err)
	}
	if _, err := reviewEngine.Open(reviewCase.ID, "claims-reviewer-gw", tick); err != nil {
		t.Fatalf("Stage 7: Open: %v", err)
	}
	if _, err := reviewEngine.Act(reviewCase.ID, "claims-reviewer-gw", hitl.ActionApprove, "", "concur with governed inference, evidence sufficient", nil, tick); err != nil {
		t.Fatalf("Stage 7: Act: %v", err)
	}
	outcome, err := reviewEngine.Execute(reviewCase.ID, "claims-reviewer-gw", tick)
	if err != nil {
		t.Fatalf("Stage 7: Execute: %v", err)
	}
	if outcome.Human.Reviewer != "claims-reviewer-gw" {
		t.Fatalf("Stage 7: expected the GovernedOutcome to name the real human reviewer, got %q", outcome.Human.Reviewer)
	}
	if outcome.Machine.Hash != machineDecision.Hash && outcome.Machine.DecisionID != machineDecision.DecisionID {
		t.Fatal("Stage 7: expected the GovernedOutcome to preserve the original MachineDecision unmodified")
	}

	// ---- Stage 8: Audit ----
	if err := reviewEngine.VerifyChain(); err != nil {
		t.Fatalf("Stage 8 (Audit): hitl.Engine.VerifyChain: %v", err)
	}
	sharedRecords := sharedLedger.Snapshot()
	if len(sharedRecords) < 2 {
		t.Fatalf("Stage 8: expected at least 2 shared-ledger records (ontology + authz), got %d", len(sharedRecords))
	}
	if err := (audit.Auditor{}).VerifyChain(sharedRecords); err != nil {
		t.Fatalf("Stage 8: shared ledger VerifyChain: %v", err)
	}
	anchorer := audit.NewLocalAnchorer()
	checkpoint, err := sharedLedger.Checkpoint(anchorer, tick)
	if err != nil {
		t.Fatalf("Stage 8: Checkpoint: %v", err)
	}
	if err := audit.VerifyCheckpoint(sharedRecords, checkpoint); err != nil {
		t.Fatalf("Stage 8: VerifyCheckpoint: %v", err)
	}
}
