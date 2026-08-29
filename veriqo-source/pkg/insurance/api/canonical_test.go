package api

import (
	"testing"

	"veriqo/pkg/insurance/canonical"
	"veriqo/pkg/insurance/causation"
	"veriqo/pkg/insurance/claim"
	"veriqo/pkg/insurance/contradiction"
	"veriqo/pkg/insurance/coverage"
	"veriqo/pkg/insurance/evidence"
	"veriqo/pkg/insurance/party"
	"veriqo/pkg/insurance/policy"
	"veriqo/pkg/insurance/quantum"
	"veriqo/pkg/insurance/timeline"
	"veriqo/pkg/lineage"
	"veriqo/pkg/platform/correlation"
)

// TestFacadeBindsEveryStepToOneCanonicalCaseLineage drives the facade
// with a canonical binding attached and proves that each lifecycle step
// registers the REAL artifact it produced onto ONE canonical
// lineage.CaseID — the identity, the policy version actually in force,
// each content-addressed evidence record, each reconstructed event,
// each contradiction, each hypothesis, and the verification manifest's
// independently recomputable evidence root hash.
//
// This is the positive half of the non-duplication rule. Before this
// binding existed, pkg/insurance produced all of these identifiers and
// none of them reached the canonical case ledger.
func TestFacadeBindsEveryStepToOneCanonicalCaseLineage(t *testing.T) {
	const caseID = "CASE-INS-LINEAGE-1"

	claimTypes := claim.NewTypeRegistry()
	for _, def := range claim.DefaultTypes() {
		if err := claimTypes.Register(def); err != nil {
			t.Fatalf("registering claim type: %v", err)
		}
	}

	f, err := New(caseID, 0, claimTypes)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ledger := lineage.NewLedger()
	binding, err := canonical.New(ledger, caseID)
	if err != nil {
		t.Fatalf("canonical.New: %v", err)
	}
	if err := f.BindCanonical(binding); err != nil {
		t.Fatalf("BindCanonical: %v", err)
	}

	// A binding for a DIFFERENT case must be refused outright: one
	// investigation, one CaseID.
	other, err := canonical.New(ledger, "CASE-INS-SOMETHING-ELSE")
	if err != nil {
		t.Fatalf("canonical.New: %v", err)
	}
	if err := f.BindCanonical(other); err == nil {
		t.Fatal("expected BindCanonical to refuse a binding for a different case")
	}

	// Step 1 — parties. Only the party that pkg/identity has actually
	// resolved (non-empty EntityRef) becomes an ENTITY node.
	if err := f.IdentifyParties(10, []PartySpec{
		{ID: "PTY-CLAIMANT", Name: "Meridian Cargo Holdings", Roles: []party.Role{party.RoleClaimant, party.RoleCargoOwner}, EntityRef: "ENT-MERIDIAN"},
		{ID: "PTY-SURVEYOR", Name: "Harbourline Survey Services", Roles: []party.Role{party.RoleSurveyor}},
	}); err != nil {
		t.Fatalf("IdentifyParties: %v", err)
	}
	if !binding.HasRef("ENT-MERIDIAN") {
		t.Fatal("the resolved party must be registered as an ENTITY node")
	}
	if binding.HasRef("PTY-SURVEYOR") {
		t.Fatal("an unresolved PartyID must never be registered as a canonical entity")
	}

	// Step 2 — policy, with a later endorsement that must NOT be chosen.
	polHistory, err := policy.NewHistory("POL-INS-1")
	if err != nil {
		t.Fatalf("policy.NewHistory: %v", err)
	}
	if err := polHistory.Add(policy.Version{
		PolicyID: "POL-INS-1", VersionID: "POL-INS-1-V1", Kind: policy.KindOriginal,
		EffectiveFrom: 0, EffectiveTo: 5000,
	}); err != nil {
		t.Fatalf("adding original: %v", err)
	}
	if err := polHistory.Add(policy.Version{
		PolicyID: "POL-INS-1", VersionID: "POL-INS-1-V2", Kind: policy.KindEndorsement,
		Supersedes: "POL-INS-1-V1", EffectiveFrom: 5000,
	}); err != nil {
		t.Fatalf("adding endorsement: %v", err)
	}
	if err := f.RegisterPolicy(20, "POL-INS-1", polHistory); err != nil {
		t.Fatalf("RegisterPolicy: %v", err)
	}

	cl, err := claim.New("CLM-INS-1", caseID, claim.TypeCargoDamage, "PTY-CLAIMANT", claimTypes)
	if err != nil {
		t.Fatalf("claim.New: %v", err)
	}
	if err := f.RegisterClaim(30, cl); err != nil {
		t.Fatalf("RegisterClaim: %v", err)
	}

	// Step 4 — evidence. Every record's content-addressed ID must land
	// on the lineage.
	photo := mustRecord(t, caseID, "cargo_photo_1", "claimant_device", 1000, "PTY-CLAIMANT", evidence.OriginClaimant)
	surveyA := mustRecord(t, caseID, "survey_report_A", "harbourline", 1500, "PTY-SURVEYOR", evidence.OriginSurveyor)
	surveyB := mustRecord(t, caseID, "survey_report_B", "carrier_appointed", 1600, "PTY-CLAIMANT", evidence.OriginCarrier)
	if err := f.IngestEvidence(40, []evidence.Record{photo, surveyA, surveyB}, []DependencyEdge{
		{Child: surveyA.EvidenceID(), Parent: photo.EvidenceID()},
	}); err != nil {
		t.Fatalf("IngestEvidence: %v", err)
	}
	for _, rec := range []evidence.Record{photo, surveyA, surveyB} {
		if !binding.HasRef(rec.EvidenceID()) {
			t.Fatalf("evidence %s did not reach the case lineage", rec.EvidenceID())
		}
	}

	// Status is derived from Strength, never supplied directly (see
	// evidence.Registry.VerifyStatus).
	supportedStrength := evidence.Strength{
		Authenticity: evidence.AuthenticitySupported, Integrity: evidence.IntegrityVerified,
		Provenance: evidence.ProvenanceVerified, Completeness: evidence.CompletenessComplete,
		Relevance: evidence.RelevanceHigh, TemporalConsistency: evidence.TemporalConsistencySupported,
		EntityConsistency: evidence.EntityConsistencySupported, IndependentCorroboration: evidence.CorroborationNone,
		ContradictionLevel: evidence.ContradictionLevelNone,
	}
	if err := f.VerifyEvidence(50, map[string]evidence.Strength{
		surveyA.EvidenceID(): supportedStrength,
		surveyB.EvidenceID(): supportedStrength,
	}); err != nil {
		t.Fatalf("VerifyEvidence: %v", err)
	}

	// Step 6 — timeline. Each event links back to the evidence that
	// reported it.
	incident, err := timeline.New("EVT-INCIDENT", timeline.TypeIncident, "", "", 2000,
		[]string{surveyA.EvidenceID()}, timeline.CertaintyConfirmed, "Port of Calder Bay", "PTY-CLAIMANT")
	if err != nil {
		t.Fatalf("timeline.New: %v", err)
	}
	if _, err := f.ReconstructTimeline(60, []timeline.Event{incident}); err != nil {
		t.Fatalf("ReconstructTimeline: %v", err)
	}
	nodes, err := binding.Walk()
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	var eventNode *lineage.Node
	for i := range nodes {
		if nodes[i].Ref == "EVT-INCIDENT" {
			eventNode = &nodes[i]
		}
	}
	if eventNode == nil {
		t.Fatal("the reconstructed event did not reach the case lineage")
	}
	if len(eventNode.Upstream) != 1 || eventNode.Upstream[0] != surveyA.EvidenceID() {
		t.Fatalf("the event must declare its source evidence as upstream, got %v", eventNode.Upstream)
	}

	// Step 7 — the policy version actually effective at the incident.
	resolved, err := f.MapPolicy(70, 2000, "POL-INS-1")
	if err != nil {
		t.Fatalf("MapPolicy: %v", err)
	}
	if resolved.VersionID != "POL-INS-1-V1" {
		t.Fatalf("expected the historical version V1, got %s", resolved.VersionID)
	}
	if !binding.HasRef("POL-INS-1-V1") {
		t.Fatal("the effective policy version did not reach the case lineage")
	}
	if binding.HasRef("POL-INS-1-V2") {
		t.Fatal("the later endorsement must not appear on a case whose incident predates it")
	}

	// Step 8 — contradiction.
	if err := f.SubmitContradictionObservation(surveyA, "cargo_condition", "SEVERE_WETTING", 0.9, 80); err != nil {
		t.Fatalf("SubmitContradictionObservation: %v", err)
	}
	if err := f.SubmitContradictionObservation(surveyB, "cargo_condition", "SURFACE_MARKING_ONLY", 0.6, 81); err != nil {
		t.Fatalf("SubmitContradictionObservation: %v", err)
	}
	recs, err := f.AnalyzeContradictions(90, "cargo_condition", contradiction.PairDocumentSurvey,
		"Two survey reports disagree on cargo condition", 10000)
	if err != nil {
		t.Fatalf("AnalyzeContradictions: %v", err)
	}
	if len(recs) == 0 {
		t.Fatal("expected a contradiction")
	}
	for _, r := range recs {
		if !binding.HasRef(r.ContradictionID) {
			t.Fatalf("contradiction %s did not reach the case lineage", r.ContradictionID)
		}
	}

	// Step 9 — causation hypotheses.
	hs, err := causation.NewHypothesisSet(caseID, "CLM-INS-1", "What caused the cargo damage?")
	if err != nil {
		t.Fatalf("NewHypothesisSet: %v", err)
	}
	h1, err := causation.NewHypothesis("H1", "Water ingress during the sea passage")
	if err != nil {
		t.Fatalf("NewHypothesis: %v", err)
	}
	if err := hs.Add(h1); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := hs.AddSupportingEvidence("H1", surveyA.EvidenceID()); err != nil {
		t.Fatalf("AddSupportingEvidence: %v", err)
	}
	if _, err := f.AnalyzeCausation(100, hs); err != nil {
		t.Fatalf("AnalyzeCausation: %v", err)
	}
	if !binding.HasRef("H1") {
		t.Fatal("the causation hypothesis did not reach the case lineage")
	}

	// The whole chain must verify at every point along the way.
	if err := binding.VerifyChain(); err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}

	// A real execution's correlation key folds into the SAME case.
	if _, err := binding.BindCorrelation(correlation.Key{
		IntentID:                  "INT-INS-1",
		ExecutionID:               "EXE-INS-1",
		EvidencePackageID:         "EVP-INS-1",
		EntityID:                  "ENT-MERIDIAN-EXEC",
		DecisionID:                "DEC-INS-1",
		ReplayPackageID:           "RPL-INS-1",
		VerificationCertificateID: "VC-INS-1",
	}, 105); err != nil {
		t.Fatalf("BindCorrelation: %v", err)
	}

	// Drive the rest of the lifecycle to the dossier, whose verification
	// manifest root hash becomes the VERIFICATION node.
	if _, _, err := f.ComputeQuantum(110, quantum.ComputeInput{
		CalculationID: "QC-INS-1",
		GrossLoss:     quantum.NewEvidenceBackedAmount(quantum.MajorUnits(19000), surveyA.EvidenceID()),
		Mitigation:    quantum.NewEvidenceBackedAmount(0),
		Salvage:       quantum.NewEvidenceBackedAmount(quantum.MajorUnits(3000), surveyA.EvidenceID()),
		Deductible:    quantum.NewEvidenceBackedAmount(quantum.MajorUnits(1000)),
		Currency:      "USD",
		ExchangeRate:  quantum.UnitExchangeRate(),
		RateSource:    "case_currency",
	}, "", quantum.EvidenceBackedAmount{}, quantum.EvidenceBackedAmount{}, quantum.EvidenceBackedAmount{}); err != nil {
		t.Fatalf("ComputeQuantum: %v", err)
	}
	typeDef, _ := claimTypes.Get(claim.TypeCargoDamage)
	if _, err := f.AnalyzeCoverage(120, coverage.Input{
		Claim:         cl,
		PolicyVersion: resolved,
		Evidence:      []evidence.Record{surveyA, surveyB},
		TypeDef:       &typeDef,
		IncidentTick:  2000,
		NoticeTick:    2100,
	}); err != nil {
		t.Fatalf("AnalyzeCoverage: %v", err)
	}
	if _, err := f.AnalyzeRecovery(130, nil); err != nil {
		t.Fatalf("AnalyzeRecovery: %v", err)
	}
	if err := f.MarkHumanReview(140); err != nil {
		t.Fatalf("MarkHumanReview: %v", err)
	}
	_, manifest, err := f.GenerateDossier(150)
	if err != nil {
		t.Fatalf("GenerateDossier: %v", err)
	}
	if !binding.HasRef(manifest.EvidenceRootHash) {
		t.Fatal("the verification manifest's evidence root hash did not reach the case lineage")
	}
	if err := binding.VerifyChain(); err != nil {
		t.Fatalf("VerifyChain after dossier: %v", err)
	}

	// Honest completeness: this case now carries intent, entity,
	// evidence, policy, decision, verification and replay identities
	// (the last three from the real correlation key), but NO recorded
	// real-world OUTCOME — nothing has actually happened yet. It must
	// therefore report incomplete rather than green, and name exactly
	// what is missing.
	comp := binding.Completeness()
	if comp.Complete {
		t.Fatal("a case with no recorded real-world outcome must not report Complete")
	}
	missing := map[lineage.Kind]bool{}
	for _, k := range comp.MissingKinds {
		missing[k] = true
	}
	if !missing[lineage.KindOutcome] {
		t.Fatalf("Completeness must report OUTCOME as missing, got missing=%v", comp.MissingKinds)
	}
	if len(comp.Dangling) != 0 {
		t.Fatalf("the case lineage must have no dangling upstream references, got %v", comp.Dangling)
	}
	if !comp.ChainVerified {
		t.Fatal("the case lineage hash chain must verify")
	}

	// Recording the real-world outcome is what completes the case — and
	// it is a RECORDED fact from elsewhere, never something this system
	// computed.
	if _, err := binding.AttachOutcome("SETTLEMENT-REF-EXTERNAL-1", "external claims record", 200); err != nil {
		t.Fatalf("AttachOutcome: %v", err)
	}
	if comp := binding.Completeness(); !comp.Complete {
		t.Fatalf("after every required kind is present the case must report Complete, missing=%v dangling=%v chain=%t",
			comp.MissingKinds, comp.Dangling, comp.ChainVerified)
	}
}
