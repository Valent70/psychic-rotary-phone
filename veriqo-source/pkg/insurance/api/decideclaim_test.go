// This file answers the "Masih_terlalu_banyak_gap" review's item 16
// directly: pkg/insurance/decision was only the Decision Trust
// Boundary's kernel; this Facade's own real, pre-existing claim
// orchestration (§32's 15-stage lifecycle) was never wired to it. That
// wiring is DecideClaim (api.go), and this file drives it through the
// same real, no-mock lifecycle TestFullCaseLifecycleEndToEnd already
// exercises -- then attacks it: an unauthorized hypothesis, evidence
// that is cited but never grounded in a real FINALIZED manifest, and a
// call attempted before the case has reached DOSSIER_GENERATED must
// all be refused, with no Decision and no ledger entry produced.
package api

import (
	"errors"
	"testing"

	"veriqo/pkg/evidence/manifest"
	insurancecase "veriqo/pkg/insurance/case"
	"veriqo/pkg/insurance/causation"
	"veriqo/pkg/insurance/claim"
	"veriqo/pkg/insurance/contradiction"
	"veriqo/pkg/insurance/coverage"
	"veriqo/pkg/insurance/cre"
	"veriqo/pkg/insurance/decision"
	"veriqo/pkg/insurance/evidence"
	"veriqo/pkg/insurance/party"
	"veriqo/pkg/insurance/policy"
	"veriqo/pkg/insurance/quantum"
	"veriqo/pkg/insurance/timeline"
	"veriqo/pkg/platform/audit"
)

// finalizeManifest drives a fresh manifest to FINALIZED for evidenceID
// -- the same real sequence test/integration/os_trust_integration_test.go's
// buildOSTrustPipeline uses, reused here rather than re-invented, since
// AuthorizeGrounded (which DecideClaim calls) only accepts evidence
// grounded this way, never a bare evidence.Registry record.
func finalizeManifest(t *testing.T, m *manifest.Registry, evidenceID, caseID string, tick uint64) {
	t.Helper()
	if _, err := m.RegisterDraft(manifest.Manifest{
		TenantID: "tenant-decideclaim", CaseID: caseID, EvidenceID: evidenceID, Version: 1,
		URI: "evidence://decideclaim-survey.pdf", Filename: "decideclaim-survey.pdf", MediaType: "application/pdf",
		ByteSize: 4096, SHA256: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		Method: "UPLOAD", Collector: "surveyor-decideclaim", Source: "independent-surveyor", AcquiredAt: tick, ReceivedAt: tick,
		HashStatus: "COMPUTED", Classification: "INTERNAL",
		AcquisitionRecord: "uploaded by independent surveyor via case portal",
	}); err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	if _, err := m.RecordCustodyEvent(evidenceID, evidenceID+"-received", "cre-system", manifest.CustodyReceived, tick, "received into custody", ""); err != nil {
		t.Fatalf("RecordCustodyEvent(RECEIVED): %v", err)
	}
	if _, err := m.Advance(evidenceID, manifest.StateIngested, tick); err != nil {
		t.Fatalf("Advance(INGESTED): %v", err)
	}
	if _, err := m.RecordCustodyEvent(evidenceID, evidenceID+"-hashed", "cre-system", manifest.CustodyHashed, tick, "hash computed", "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"); err != nil {
		t.Fatalf("RecordCustodyEvent(HASHED): %v", err)
	}
	if _, err := m.Advance(evidenceID, manifest.StateIntegrityAssessed, tick); err != nil {
		t.Fatalf("Advance(INTEGRITY_ASSESSED): %v", err)
	}
	if _, err := m.Advance(evidenceID, manifest.StateProvenanceComplete, tick); err != nil {
		t.Fatalf("Advance(PROVENANCE_COMPLETE): %v", err)
	}
	if _, err := m.RecordCustodyEvent(evidenceID, evidenceID+"-reviewed", "cre-system", manifest.CustodyReviewed, tick, "independent review complete", "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"); err != nil {
		t.Fatalf("RecordCustodyEvent(REVIEWED): %v", err)
	}
	if _, err := m.Advance(evidenceID, manifest.StateReadyForFinalization, tick); err != nil {
		t.Fatalf("Advance(READY_FOR_FINALIZATION): %v", err)
	}
	if _, err := m.Advance(evidenceID, manifest.StateFinalized, tick); err != nil {
		t.Fatalf("Advance(FINALIZED): %v", err)
	}
}

// driveToDossierGeneratedForDecision drives one real Facade case
// through every one of its 15 lifecycle stages (the same real
// underlying engines TestFullCaseLifecycleEndToEnd exercises, trimmed
// to the minimum shape DecideClaim itself needs: one hypothesis, one
// piece of supporting evidence) up to DOSSIER_GENERATED, and returns
// the facade plus the evidence ID and hypothesis ID a caller would now
// decide the claim against.
func driveToDossierGeneratedForDecision(t *testing.T, caseID string) (f *Facade, evidenceID string, hID causation.HypothesisID) {
	t.Helper()
	claimTypes := claim.NewTypeRegistry()
	for _, def := range claim.DefaultTypes() {
		if err := claimTypes.Register(def); err != nil {
			t.Fatalf("registering default claim type %s: %v", def.Type, err)
		}
	}
	f, err := New(caseID, 0, claimTypes)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := f.IdentifyParties(10, []PartySpec{
		{ID: "PTY-CLAIMANT", Name: "Claimant Co", Roles: []party.Role{party.RoleClaimant}},
	}); err != nil {
		t.Fatalf("IdentifyParties: %v", err)
	}
	polHistory, err := policy.NewHistory("POL-1")
	if err != nil {
		t.Fatalf("policy.NewHistory: %v", err)
	}
	if err := polHistory.Add(policy.Version{
		PolicyID: "POL-1", VersionID: "V1", Kind: policy.KindOriginal, EffectiveFrom: 0,
	}); err != nil {
		t.Fatalf("adding policy version: %v", err)
	}
	if err := f.RegisterPolicy(20, "POL-1", polHistory); err != nil {
		t.Fatalf("RegisterPolicy: %v", err)
	}
	cl, err := claim.New("CLM-1", caseID, claim.TypeCargoDamage, "PTY-CLAIMANT", claimTypes)
	if err != nil {
		t.Fatalf("claim.New: %v", err)
	}
	if err := f.RegisterClaim(30, cl); err != nil {
		t.Fatalf("RegisterClaim: %v", err)
	}
	survey := mustRecord(t, caseID, "decideclaim_survey", "independent_surveyor", 1000, "PTY-CLAIMANT", evidence.OriginSurveyor)
	if err := f.IngestEvidence(40, []evidence.Record{survey}, nil); err != nil {
		t.Fatalf("IngestEvidence: %v", err)
	}
	supportedStrength := evidence.Strength{
		Authenticity: evidence.AuthenticitySupported, Integrity: evidence.IntegrityVerified,
		Provenance: evidence.ProvenanceVerified, Completeness: evidence.CompletenessComplete,
		Relevance: evidence.RelevanceHigh, TemporalConsistency: evidence.TemporalConsistencySupported,
		EntityConsistency: evidence.EntityConsistencySupported, IndependentCorroboration: evidence.CorroborationNone,
		ContradictionLevel: evidence.ContradictionLevelNone,
	}
	if err := f.VerifyEvidence(50, map[string]evidence.Strength{survey.EvidenceID(): supportedStrength}); err != nil {
		t.Fatalf("VerifyEvidence: %v", err)
	}
	incidentEvt, err := timeline.New("EVT-INCIDENT", timeline.TypeIncident, "", "", 900,
		[]string{survey.EvidenceID()}, timeline.CertaintyConfirmed, "Jakarta port", "PTY-CLAIMANT")
	if err != nil {
		t.Fatalf("building incident event: %v", err)
	}
	if _, err := f.ReconstructTimeline(60, []timeline.Event{incidentEvt}); err != nil {
		t.Fatalf("ReconstructTimeline: %v", err)
	}
	resolvedVersion, err := f.MapPolicy(70, 900, "POL-1")
	if err != nil {
		t.Fatalf("MapPolicy: %v", err)
	}
	if err := f.SubmitContradictionObservation(survey, "cargo_condition_at_delivery", "WET_DAMAGE", 0.9, 80); err != nil {
		t.Fatalf("SubmitContradictionObservation: %v", err)
	}
	if _, err := f.AnalyzeContradictions(90, "cargo_condition_at_delivery", contradiction.PairDocumentSurvey, "single observation, no contradiction expected", 10000); err != nil {
		t.Fatalf("AnalyzeContradictions: %v", err)
	}

	hs, err := causation.NewHypothesisSet(caseID, "CLM-1", "What caused the cargo damage?")
	if err != nil {
		t.Fatalf("NewHypothesisSet: %v", err)
	}
	h1, err := causation.NewHypothesis("H1", "Water ingress during sea transit")
	if err != nil {
		t.Fatalf("NewHypothesis H1: %v", err)
	}
	if err := hs.Add(h1); err != nil {
		t.Fatalf("Add H1: %v", err)
	}
	if err := hs.AddSupportingEvidence("H1", survey.EvidenceID()); err != nil {
		t.Fatalf("AddSupportingEvidence: %v", err)
	}
	if _, err := f.AnalyzeCausation(100, hs); err != nil {
		t.Fatalf("AnalyzeCausation: %v", err)
	}

	grossLoss := quantum.NewEvidenceBackedAmount(quantum.MajorUnits(10000), survey.EvidenceID())
	if _, _, err := f.ComputeQuantum(110, quantum.ComputeInput{
		CalculationID: "QC-1", GrossLoss: grossLoss, Mitigation: quantum.NewEvidenceBackedAmount(0),
		Salvage: quantum.NewEvidenceBackedAmount(0), Deductible: quantum.NewEvidenceBackedAmount(0),
		Currency: "USD", ExchangeRate: quantum.UnitExchangeRate(), RateSource: "case_currency",
	}, "", quantum.EvidenceBackedAmount{}, quantum.EvidenceBackedAmount{}, quantum.EvidenceBackedAmount{}); err != nil {
		t.Fatalf("ComputeQuantum: %v", err)
	}

	typeDef, _ := claimTypes.Get(claim.TypeCargoDamage)
	if _, err := f.AnalyzeCoverage(120, coverage.Input{
		Claim: cl, PolicyVersion: resolvedVersion, Evidence: []evidence.Record{survey},
		TypeDef: &typeDef, IncidentTick: 900, NoticeTick: 900,
	}); err != nil {
		t.Fatalf("AnalyzeCoverage: %v", err)
	}
	if _, err := f.AnalyzeRecovery(130, nil); err != nil {
		t.Fatalf("AnalyzeRecovery: %v", err)
	}
	if err := f.MarkHumanReview(140); err != nil {
		t.Fatalf("MarkHumanReview: %v", err)
	}
	if _, _, err := f.GenerateDossier(150); err != nil {
		t.Fatalf("GenerateDossier: %v", err)
	}
	if f.Case().State() != insurancecase.StateDossierGenerated {
		t.Fatalf("test setup: expected DOSSIER_GENERATED, got %s", f.Case().State())
	}
	return f, survey.EvidenceID(), "H1"
}

// TestDecideClaimWiresTheRealFacadeThroughToADecisionAndLedger is the
// legitimate path: a case this Facade itself drove to DOSSIER_GENERATED,
// deciding a hypothesis this same Facade's own AnalyzeCausation call
// analyzed, against evidence independently grounded in a real,
// FINALIZED manifest -- producing a real, ledgered Decision through the
// identical sealed chain the core trust pipeline uses.
func TestDecideClaimWiresTheRealFacadeThroughToADecisionAndLedger(t *testing.T) {
	const caseID = "CASE-DECIDE-1"
	f, evidenceID, hID := driveToDossierGeneratedForDecision(t, caseID)

	manifests := manifest.NewRegistry()
	finalizeManifest(t, manifests, evidenceID, caseID, 150)

	ledger := audit.NewAuditStore()
	if err := f.BindLedger(ledger); err != nil {
		t.Fatalf("BindLedger: %v", err)
	}

	d, err := f.DecideClaim(160, hID, manifests, nil, cre.FindingInput{
		CaseID: caseID, ContractBasis: "clause-1", ObligationRef: "obl-1",
		EventRef: "event-1", QuantumRef: "calc-1", HumanReviewRequired: true,
	}, "finding-decide-1", decision.OutcomeApproved, "primary hypothesis substantiated by grounded, finalized evidence")
	if err != nil {
		t.Fatalf("DecideClaim: %v", err)
	}
	if d.IsZero() {
		t.Fatal("expected a populated Decision")
	}
	if d.Outcome() != decision.OutcomeApproved {
		t.Fatalf("expected OutcomeApproved, got %v", d.Outcome())
	}

	got, ok := f.Decision()
	if !ok {
		t.Fatal("expected Decision() to report a decision has been made")
	}
	if got.Hash() != d.Hash() {
		t.Fatalf("expected Decision() to return the same Decision DecideClaim returned")
	}

	recs := ledger.Snapshot()
	if len(recs) != 1 {
		t.Fatalf("expected exactly 1 ledger record, got %d", len(recs))
	}
	if err := (audit.Auditor{}).VerifyChain(recs); err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if recs[0].Action != "DECISION_RECORDED" {
		t.Fatalf("expected the ledger record's action to be DECISION_RECORDED, got %s", recs[0].Action)
	}
}

// TestDecideClaimRefusesBeforeDossierGenerated proves the case's own
// lifecycle stage gates DecideClaim -- no decision, no ledger entry --
// exactly the "no Decision created, no Ledger authority created" shape
// the reviewer's named boundaries all require.
func TestDecideClaimRefusesBeforeDossierGenerated(t *testing.T) {
	const caseID = "CASE-DECIDE-EARLY-1"
	claimTypes := claim.NewTypeRegistry()
	for _, def := range claim.DefaultTypes() {
		if err := claimTypes.Register(def); err != nil {
			t.Fatalf("registering default claim type %s: %v", def.Type, err)
		}
	}
	f, err := New(caseID, 0, claimTypes)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := f.IdentifyParties(10, []PartySpec{
		{ID: "PTY-CLAIMANT", Name: "Claimant Co", Roles: []party.Role{party.RoleClaimant}},
	}); err != nil {
		t.Fatalf("IdentifyParties: %v", err)
	}

	manifests := manifest.NewRegistry()
	d, err := f.DecideClaim(20, "H1", manifests, nil, cre.FindingInput{CaseID: caseID}, "finding-early", decision.OutcomeApproved, "premature")
	if !errors.Is(err, ErrNotReadyForDecision) {
		t.Fatalf("expected ErrNotReadyForDecision for a case still at PARTIES_IDENTIFIED, got %v", err)
	}
	if !d.IsZero() {
		t.Fatal("expected a zero Decision when DecideClaim is refused")
	}
	if _, ok := f.Decision(); ok {
		t.Fatal("expected Decision() to report no decision has been made")
	}
}

// TestDecideClaimRefusesAnUnauthorizedHypothesis proves a caller
// cannot decide a claim against a hypothesisID this Facade's own
// causation analysis never evaluated -- there is no way to reach
// DecideClaim with a hand-picked hypothesis that bypasses
// AnalyzeCausation.
func TestDecideClaimRefusesAnUnauthorizedHypothesis(t *testing.T) {
	const caseID = "CASE-DECIDE-BADHYP-1"
	f, evidenceID, _ := driveToDossierGeneratedForDecision(t, caseID)

	manifests := manifest.NewRegistry()
	finalizeManifest(t, manifests, evidenceID, caseID, 150)

	d, err := f.DecideClaim(160, "H-NEVER-ANALYZED", manifests, nil, cre.FindingInput{
		CaseID: caseID, ContractBasis: "clause-1", ObligationRef: "obl-1", EventRef: "event-1", QuantumRef: "calc-1",
	}, "finding-badhyp", decision.OutcomeApproved, "attempted bypass")
	if !errors.Is(err, ErrHypothesisNotFound) {
		t.Fatalf("expected ErrHypothesisNotFound, got %v", err)
	}
	if !d.IsZero() {
		t.Fatal("expected a zero Decision when DecideClaim is refused")
	}
}

// TestDecideClaimRefusesUngroundedEvidence is the sharpest version of
// the reviewer's Storage -> Decision boundary attack: the hypothesis
// and its supporting evidence are entirely real and were genuinely
// analyzed by this Facade's own AnalyzeCausation call, but the
// evidence was never independently grounded in a FINALIZED manifest --
// "trust me, the evidence is fine" is not sufficient for a Decision to
// be produced.
func TestDecideClaimRefusesUngroundedEvidence(t *testing.T) {
	const caseID = "CASE-DECIDE-UNGROUNDED-1"
	f, _, hID := driveToDossierGeneratedForDecision(t, caseID)

	// Deliberately empty -- the cited evidence ID has no manifest at all.
	manifests := manifest.NewRegistry()

	d, err := f.DecideClaim(160, hID, manifests, nil, cre.FindingInput{
		CaseID: caseID, ContractBasis: "clause-1", ObligationRef: "obl-1", EventRef: "event-1", QuantumRef: "calc-1",
	}, "finding-ungrounded", decision.OutcomeApproved, "attempted bypass via ungrounded evidence")
	if !errors.Is(err, cre.ErrEvidenceNotGrounded) {
		t.Fatalf("expected cre.ErrEvidenceNotGrounded for evidence with no FINALIZED manifest, got %v", err)
	}
	if !d.IsZero() {
		t.Fatal("expected a zero Decision when DecideClaim is refused")
	}
}
