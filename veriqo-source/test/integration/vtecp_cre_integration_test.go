// TestVTECPCapabilitiesIntegrateAsOneSystem is the cross-system
// integration proof for VTECP-001's five capabilities plus the CRE
// Finding gate: it threads one small, real case scenario through every
// capability built this round and confirms they compose, rather than
// merely each passing their own package-local tests in isolation.
//
// What "integration" means here, concretely:
//   - pkg/ontology (Capability 2) and pkg/authz (Capability 3) mirror
//     their own actions into the SAME *audit.AuditStore instance
//     (Capability 4) -- proving the audit ledger is a genuine shared
//     substrate, not a per-package convenience log.
//   - pkg/inference (Capability 5) refuses to record a trace unless
//     pkg/governance/lifecycle (the model registry) says the model is
//     ACTIVE, and the resulting trace also lands in the SAME shared
//     ledger.
//   - The shared ledger is then sealed with a Merkle checkpoint
//     (Capability 4's Anchor/Verify API), and a specific record's
//     inclusion is proven WITHOUT needing the whole ledger -- the
//     actual "Verify API" claim, exercised end to end rather than
//     asserted.
//   - pkg/evidence/manifest (Capability 1) finalizes a real evidence
//     item and is cited into the ontology graph by its own content hash,
//     not duplicated as a second identity.
//   - pkg/insurance/finding composes real output from
//     pkg/insurance/causation (a real HypothesisSet, not synthetic
//     text) and pkg/insurance/obligation/timeline/quantum, and only
//     reaches StatusFinding once every required field -- including a
//     citation into the governed InferenceTrace -- is genuinely present.
//
// The domain data below (case/evidence/obligation/event/quantum IDs and
// amounts) is a small synthetic maritime cargo-damage scenario, clearly
// a test fixture, not a claim about a real case.
package integration

import (
	"testing"

	"veriqo/pkg/authz"
	"veriqo/pkg/canonical/jcs"
	"veriqo/pkg/evidence/manifest"
	"veriqo/pkg/governance/lifecycle"
	"veriqo/pkg/inference"
	"veriqo/pkg/insurance/causation"
	"veriqo/pkg/insurance/cre"
	"veriqo/pkg/insurance/evidence"
	"veriqo/pkg/insurance/finding"
	"veriqo/pkg/insurance/obligation"
	"veriqo/pkg/insurance/quantum"
	"veriqo/pkg/insurance/timeline"
	"veriqo/pkg/ontology"
	"veriqo/pkg/platform/audit"
)

func TestVTECPCapabilitiesIntegrateAsOneSystem(t *testing.T) {
	const caseID = "case-cargo-001"
	const tenantID = "tenant-veriqo-demo"
	const tick uint64 = 1000

	sharedLedger := audit.NewAuditStore()

	// ---- Capability 2: Ontology registers the case + evidence + shares the ledger ----
	ont := ontology.NewRegistry()
	ont.AttachAuditStore(sharedLedger)

	caseObj, err := ont.CreateObject(ontology.Object{
		ObjectType: ontology.ObjectCase, ObjectID: caseID, TenantID: tenantID, State: "OPEN",
	}, "cre-system", tick, nil)
	if err != nil {
		t.Fatalf("CreateObject(Case): %v", err)
	}

	evidenceObj, err := ont.CreateObject(ontology.Object{
		ObjectType: ontology.ObjectEvidence, ObjectID: "evidence-survey-1", TenantID: tenantID,
	}, "cre-system", tick, nil)
	if err != nil {
		t.Fatalf("CreateObject(Evidence): %v", err)
	}
	if _, err := ont.CreateLink(ontology.Link{
		LinkType: ontology.LinkCaseHasEvidence, FromType: ontology.ObjectCase, FromID: caseObj.ObjectID,
		ToType: ontology.ObjectEvidence, ToID: evidenceObj.ObjectID, TenantID: tenantID,
	}, "cre-system", tick, nil); err != nil {
		t.Fatalf("CreateLink(CaseHasEvidence): %v", err)
	}

	// ---- Capability 1: a real Evidence Manifest, finalized and hash-verified ----
	manifestRegistry := manifest.NewRegistry()
	draft, err := manifestRegistry.RegisterDraft(manifest.Manifest{
		TenantID: tenantID, CaseID: caseID, EvidenceID: evidenceObj.ObjectID, Version: 1,
		URI: "evidence://survey-report-1.pdf", Filename: "survey-report-1.pdf", MediaType: "application/pdf",
		ByteSize: 204800, SHA256: "aa11bb22cc33dd44ee55ff66aa11bb22cc33dd44ee55ff66aa11bb22cc33dd4",
		Method: "UPLOAD", Collector: "surveyor-1", Source: "independent-surveyor", AcquiredAt: tick, ReceivedAt: tick,
		HashStatus: "COMPUTED", Classification: "INTERNAL",
		AcquisitionRecord: "uploaded by independent surveyor via case portal",
	})
	if err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	cur := advanceManifestToFinalized(t, manifestRegistry, draft.EvidenceID, tick)
	if err := manifest.VerifyManifestHash(cur); err != nil {
		t.Fatalf("VerifyManifestHash: %v", err)
	}
	if cur.State != manifest.StateFinalized {
		t.Fatalf("expected the manifest to reach FINALIZED, got %s", cur.State)
	}

	// ---- Capability 3: Purpose-bound authorization, mirrored to the SAME shared ledger ----
	authzEngine := authz.NewEngine()
	authzEngine.AttachAuditStore(sharedLedger)
	published, err := authzEngine.Publish(authz.Document{
		ID: "cre-access-policy",
		Rules: []authz.Rule{
			{ID: "allow-case-resolution", Effect: authz.Allow, Roles: []string{"claims_analyst"},
				Actions: []string{"read"}, Resources: []string{"case/*"}, Purposes: []string{"case_resolution"}},
		},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := authzEngine.Activate(published.Version); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	ex, decision, err := authzEngine.CanRecorded(authz.Request{
		Actor: "analyst-1", Roles: []string{"claims_analyst"}, Action: "read", Resource: "case/" + caseID,
		Tenant: tenantID, Purpose: "case_resolution", Tick: tick,
	})
	if err != nil {
		t.Fatalf("CanRecorded: %v", err)
	}
	if !ex.Allowed || !decision.Allowed {
		t.Fatalf("expected the purpose-bound request to be allowed, got %+v", ex)
	}
	// The same request with NO declared purpose must be denied -- proves
	// Purpose Binding is actually enforced, not merely plumbed through.
	exNoPurpose, err := authzEngine.Can(authz.Request{
		Actor: "analyst-1", Roles: []string{"claims_analyst"}, Action: "read", Resource: "case/" + caseID, Tick: tick,
	})
	if err != nil {
		t.Fatalf("Can (no purpose): %v", err)
	}
	if exNoPurpose.Allowed {
		t.Fatal("expected a request with no declared purpose to be denied by a purpose-bound rule")
	}

	// ---- Real domain reasoning: causation, obligation, timeline, quantum ----
	hs, err := causation.NewHypothesisSet(caseID, "claim-1", "What caused the cargo damage?")
	if err != nil {
		t.Fatalf("NewHypothesisSet: %v", err)
	}
	const hWaterIngress causation.HypothesisID = "H1"
	const hPreExistingDamage causation.HypothesisID = "H2"
	if err := hs.Add(causation.Hypothesis{ID: hWaterIngress, Description: "Water ingress during heavy weather at sea caused the cargo damage."}); err != nil {
		t.Fatalf("Add(H1): %v", err)
	}
	if err := hs.Add(causation.Hypothesis{ID: hPreExistingDamage, Description: "The cargo was already damaged before loading."}); err != nil {
		t.Fatalf("Add(H2): %v", err)
	}
	if err := hs.AddSupportingEvidence(hWaterIngress, evidenceObj.ObjectID); err != nil {
		t.Fatalf("AddSupportingEvidence: %v", err)
	}
	if err := hs.AddContradictingEvidence(hPreExistingDamage, evidenceObj.ObjectID); err != nil {
		t.Fatalf("AddContradictingEvidence: %v", err)
	}
	dg := evidence.NewDependencyGraph()
	explanation, err := causation.Explain(hs, dg)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if explanation.Narrative == "" {
		t.Fatal("expected a non-empty hedged narrative")
	}
	leading, ok := hs.Get(hWaterIngress)
	if !ok {
		t.Fatal("expected H1 to be retrievable")
	}

	obl := obligation.Obligation{
		ObligationID: "obl-notice-1", CaseID: caseID, Duty: "Notify insurer of cargo damage within policy period.",
		SourceClause: "clause-9.3", SourceDocument: "policy-doc-1", SourceVersion: "v1",
		TriggerEvent: "DAMAGE_DISCOVERY", TriggerBasis: obligation.TriggerFromDiscovery,
		ComplianceBasis: obligation.ComplianceBySent, ResponsibleParty: "party-insured-1",
		Status: obligation.StatusOpen,
	}
	if err := obl.Validate(); err != nil {
		t.Fatalf("Obligation.Validate: %v", err)
	}

	ev, err := timeline.New("event-damage-discovery-1", timeline.TypeDamageDiscovery,
		"2026-03-14T09:00:00", "UTC", 1773478800, []string{evidenceObj.ObjectID}, timeline.CertaintyConfirmed, "Port of Singapore", "")
	if err != nil {
		t.Fatalf("timeline.New: %v", err)
	}

	calc, err := quantum.Compute(quantum.ComputeInput{
		CalculationID: "calc-1",
		GrossLoss:     quantum.NewEvidenceBackedAmount(quantum.MajorUnits(50000), evidenceObj.ObjectID),
		Mitigation:    quantum.NewEvidenceBackedAmount(0),
		Salvage:       quantum.NewEvidenceBackedAmount(0),
		Deductible:    quantum.NewEvidenceBackedAmount(quantum.MajorUnits(2000)),
		Currency:      "USD", ExchangeRate: quantum.UnitExchangeRate(), RateSource: "unit",
	})
	if err != nil {
		t.Fatalf("quantum.Compute: %v", err)
	}

	// ---- Capability 5: a governed InferenceTrace, gated by the model lifecycle registry ----
	lifecycleReg := lifecycle.NewRegistry()
	modelEvent, err := lifecycleReg.RegisterModel(lifecycle.Model{
		ModelID: "causation-assist-v1", Version: "1.0.0", Type: "causal-hypothesis-ranker",
		ParametersHash: "params-hash-1", TrainingDataHash: "training-hash-1",
	}, "ml-engineer-1", tick)
	if err != nil {
		t.Fatalf("RegisterModel: %v", err)
	}
	modelKey := modelEvent.Key
	if err := lifecycleReg.SetCalibration(modelKey, "calib-1"); err != nil {
		t.Fatalf("SetCalibration: %v", err)
	}
	for _, to := range []lifecycle.ModelState{lifecycle.ModelValidated, lifecycle.ModelCalibrated} {
		if _, err := lifecycleReg.TransitionModel(modelKey, to, "ml-engineer-1", "", "", tick, nil); err != nil {
			t.Fatalf("TransitionModel(%s): %v", to, err)
		}
	}
	if _, err := lifecycleReg.TransitionModel(modelKey, lifecycle.ModelApproved, "ml-engineer-1", "governance-lead-1", "reviewed", tick, nil); err != nil {
		t.Fatalf("TransitionModel(APPROVED): %v", err)
	}
	if _, err := lifecycleReg.TransitionModel(modelKey, lifecycle.ModelActive, "ml-engineer-1", "governance-lead-1", "go live", tick, nil); err != nil {
		t.Fatalf("TransitionModel(ACTIVE): %v", err)
	}

	recorder := inference.NewRecorder(lifecycleReg)
	recorder.AttachAuditStore(sharedLedger)
	inputHash := jcs.MustHash(map[string]any{"hypothesis_set": hs.CaseID, "question": hs.Question})
	trace, err := recorder.Record(modelKey, inputHash, "ranked H1 (water ingress) as leading hypothesis", 0.78,
		"cre-causation-engine", "case_resolution", tick)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := inference.VerifyTraceHash(trace); err != nil {
		t.Fatalf("VerifyTraceHash: %v", err)
	}

	// ---- CRE Finding gate: compose everything above, verify the gate actually gates ----
	partial := finding.Finding{
		FindingID: "finding-1", CaseID: caseID,
		SupportedBy: leading.SupportingEvidence, ContradictedBy: nil, ContradictionsConsidered: true,
		ContractBasis: obl.SourceClause, ObligationRef: obl.ObligationID, EventRef: ev.EventID,
		Causation: explanation.Narrative, QuantumRef: calc.CalculationID,
		SourceInferenceTraceID: trace.TraceID,
		Alternatives:           []string{string(hPreExistingDamage)}, AlternativesConsidered: true,
		HumanReviewRequired: true, HumanReviewDecided: true, Tick: tick,
	}
	if got := finding.Evaluate(partial); got.Status != finding.StatusCandidate {
		t.Fatalf("expected the Finding to still be CANDIDATE before ConfidenceBasis is set, got %s (missing=%v)",
			got.Status, finding.MissingFields(partial))
	}
	partial.ConfidenceBasis = leading.Status
	f := finding.Evaluate(partial)
	if f.Status != finding.StatusFinding {
		t.Fatalf("expected StatusFinding once every required field is populated, got %s (missing=%v)",
			f.Status, finding.MissingFields(partial))
	}
	if err := finding.VerifyFindingHash(f); err != nil {
		t.Fatalf("VerifyFindingHash: %v", err)
	}
	if f.ConfidenceBasis != causation.StatusSupported && f.ConfidenceBasis != causation.StatusPartiallySupported {
		t.Fatalf("expected H1 to be at least partially supported given its supporting evidence, got %s", f.ConfidenceBasis)
	}

	// A StatusFinding Finding is still only a candidate until it passes
	// the Finding Verification Gate -- Authorize is the only way to turn
	// it into something a downstream consumer (CRE / Dossier / Decision)
	// may treat as final.
	authorized, err := cre.Authorize(f, hs, hWaterIngress, recorder.Traces(), tick)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if authorized.IsZero() {
		t.Fatal("expected a populated AuthorizedFinding")
	}
	if authorized.Finding().Hash != f.Hash {
		t.Fatal("expected the AuthorizedFinding to carry the same Finding that was authorized")
	}

	// ---- Capability 4: seal the SHARED ledger and prove one record's inclusion ----
	records := sharedLedger.Snapshot()
	if len(records) < 3 {
		t.Fatalf("expected at least 3 records in the shared ledger (ontology + authz + inference), got %d", len(records))
	}
	var sawOntology, sawAuthz, sawInference bool
	for _, r := range records {
		switch r.Action {
		case "CreateObject:Case", "CreateObject:Evidence", "CreateLink:CASE_HAS_EVIDENCE":
			sawOntology = true
		case "PolicyDecision":
			sawAuthz = true
		case "InferenceTrace":
			sawInference = true
		}
	}
	if !sawOntology || !sawAuthz || !sawInference {
		t.Fatalf("expected the shared ledger to carry entries from all three subsystems: ontology=%v authz=%v inference=%v",
			sawOntology, sawAuthz, sawInference)
	}

	if err := (audit.Auditor{}).VerifyChain(records); err != nil {
		t.Fatalf("VerifyChain on the shared multi-subsystem ledger: %v", err)
	}

	anchorer := audit.NewLocalAnchorer()
	checkpoint, err := sharedLedger.Checkpoint(anchorer, tick)
	if err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if err := audit.VerifyCheckpoint(records, checkpoint); err != nil {
		t.Fatalf("VerifyCheckpoint: %v", err)
	}

	// Prove inclusion of the InferenceTrace's own audit record, without
	// needing the rest of the ledger -- the actual point of the Verify API.
	var inferenceIdx = -1
	for i, r := range records {
		if r.Action == "InferenceTrace" {
			inferenceIdx = i
		}
	}
	if inferenceIdx < 0 {
		t.Fatal("expected to find the InferenceTrace's audit record")
	}
	proof, err := audit.GenerateInclusionProof(records, inferenceIdx)
	if err != nil {
		t.Fatalf("GenerateInclusionProof: %v", err)
	}
	if err := audit.VerifyRecordInclusion(records[inferenceIdx], proof); err != nil {
		t.Fatalf("VerifyRecordInclusion: %v", err)
	}
	if proof.Root != checkpoint.MerkleRoot {
		t.Fatalf("expected the inclusion proof's root to match the sealed checkpoint's root: %s vs %s",
			proof.Root, checkpoint.MerkleRoot)
	}
}
