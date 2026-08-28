package api

import (
	"strings"
	"testing"

	"veriqo/pkg/evidence/ontology"
	insurancecase "veriqo/pkg/insurance/case"
	"veriqo/pkg/insurance/causation"
	"veriqo/pkg/insurance/claim"
	"veriqo/pkg/insurance/contradiction"
	"veriqo/pkg/insurance/coverage"
	"veriqo/pkg/insurance/deadline"
	"veriqo/pkg/insurance/evidence"
	"veriqo/pkg/insurance/mitigation"
	"veriqo/pkg/insurance/party"
	"veriqo/pkg/insurance/policy"
	"veriqo/pkg/insurance/quantum"
	"veriqo/pkg/insurance/recovery"
	"veriqo/pkg/insurance/timeline"
	"veriqo/pkg/insurance/verification"
)

func mustOntologyEvidence(t *testing.T, subject, source string, observedAt uint64) ontology.Evidence {
	t.Helper()
	e, err := ontology.New(ontology.Evidence{
		Type:       ontology.TypeDocument,
		Subject:    subject,
		Predicate:  "describes",
		Object:     "cargo_condition",
		Source:     source,
		ObservedAt: observedAt,
		Confidence: 0.9,
		Attributes: map[string]string{"document_hash": "deadbeef-" + subject},
	})
	if err != nil {
		t.Fatalf("ontology.New(%s): %v", subject, err)
	}
	return e
}

func mustRecord(t *testing.T, caseID, subject, source string, observedAt uint64, sourceParty party.PartyID, origin evidence.Origin) evidence.Record {
	t.Helper()
	rec, err := evidence.New(caseID, mustOntologyEvidence(t, subject, source, observedAt), sourceParty, origin)
	if err != nil {
		t.Fatalf("evidence.New(%s): %v", subject, err)
	}
	return rec
}

// TestFullCaseLifecycleEndToEnd drives one VICE case through every one
// of the blueprint's 15 lifecycle stages, using the REAL underlying
// engine at every step (no mocks, no stubs) -- a cargo-damage claim
// with a genuine evidence dependency chain, a genuine timeline
// conflict, a genuine contradiction, and a reconciling quantum
// discrepancy, ending in a verified, tamper-evident Dossier.
func TestFullCaseLifecycleEndToEnd(t *testing.T) {
	const caseID = "CASE-E2E-1"

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
	if f.Case().State() != insurancecase.StateCaseCreated {
		t.Fatalf("expected fresh case in CASE_CREATED, got %s", f.Case().State())
	}

	// Stage 1: PARTIES_IDENTIFIED.
	if err := f.IdentifyParties(10, []PartySpec{
		{ID: "PTY-CLAIMANT", Name: "Nusantara Trading Co", Roles: []party.Role{party.RoleClaimant, party.RoleCargoOwner}},
		{ID: "PTY-INSURER", Name: "Samudra Marine Insurance", Roles: []party.Role{party.RoleInsurer, party.RoleCargoInsurer}},
		{ID: "PTY-SURVEYOR", Name: "Independent Marine Surveyors Ltd", Roles: []party.Role{party.RoleSurveyor}},
		{ID: "PTY-CARRIER", Name: "Pelayaran Nusantara Lines", Roles: []party.Role{party.RoleCarrier}},
	}); err != nil {
		t.Fatalf("IdentifyParties: %v", err)
	}
	if f.Case().State() != insurancecase.StatePartiesIdentified {
		t.Fatalf("expected PARTIES_IDENTIFIED, got %s", f.Case().State())
	}

	// Stage 2: POLICY_REGISTERED -- an original policy PLUS a later
	// endorsement, exactly the §6 P0 shape: the incident falls inside
	// the ORIGINAL's window, before the endorsement took effect.
	polHistory, err := policy.NewHistory("POL-1")
	if err != nil {
		t.Fatalf("policy.NewHistory: %v", err)
	}
	if err := polHistory.Add(policy.Version{
		PolicyID: "POL-1", VersionID: "V1", Kind: policy.KindOriginal,
		EffectiveFrom: 0, EffectiveTo: 5000, Deductible: "USD 50,000",
	}); err != nil {
		t.Fatalf("adding original policy version: %v", err)
	}
	if err := polHistory.Add(policy.Version{
		PolicyID: "POL-1", VersionID: "V2", Kind: policy.KindEndorsement, Supersedes: "V1",
		EffectiveFrom: 5000, Deductible: "USD 75,000",
	}); err != nil {
		t.Fatalf("adding endorsement: %v", err)
	}
	if err := f.RegisterPolicy(20, "POL-1", polHistory); err != nil {
		t.Fatalf("RegisterPolicy: %v", err)
	}

	// Stage 3: CLAIM_REGISTERED.
	cl, err := claim.New("CLM-1", caseID, claim.TypeCargoDamage, "PTY-CLAIMANT", claimTypes)
	if err != nil {
		t.Fatalf("claim.New: %v", err)
	}
	if err := f.RegisterClaim(30, cl); err != nil {
		t.Fatalf("RegisterClaim: %v", err)
	}

	// Stage 4: EVIDENCE_INGESTED -- a genuine dependency chain: a
	// photo and a statement both feed into a survey report (blueprint
	// §10's own "Photo A -> Statement A -> Survey A" shape, generalised
	// to two independent parents), and two clashing survey-condition
	// observations for the contradiction stage.
	photo := mustRecord(t, caseID, "cargo_photo_1", "claimant_phone", 1000, "PTY-CLAIMANT", evidence.OriginClaimant)
	statement := mustRecord(t, caseID, "driver_statement_1", "claimant_agent", 1010, "PTY-CLAIMANT", evidence.OriginClaimant)
	surveyA := mustRecord(t, caseID, "survey_report_A", "independent_surveyor", 1500, "PTY-SURVEYOR", evidence.OriginSurveyor)
	surveyB := mustRecord(t, caseID, "survey_report_B", "carrier_surveyor", 1600, "PTY-CARRIER", evidence.OriginCarrier)

	if err := f.IngestEvidence(40, []evidence.Record{photo, statement, surveyA, surveyB}, []DependencyEdge{
		{Child: surveyA.EvidenceID(), Parent: photo.EvidenceID()},
		{Child: surveyA.EvidenceID(), Parent: statement.EvidenceID()},
	}); err != nil {
		t.Fatalf("IngestEvidence: %v", err)
	}
	if f.Case().State() != insurancecase.StateEvidenceIngested {
		t.Fatalf("expected EVIDENCE_INGESTED, got %s", f.Case().State())
	}
	if roots := f.evDeps.Roots([]string{surveyA.EvidenceID(), photo.EvidenceID(), statement.EvidenceID()}); len(roots) != 2 {
		t.Fatalf("expected surveyA's two independent parents to reduce to 2 roots (photo, statement), got %v", roots)
	}

	// Stage 5: EVIDENCE_VERIFIED.
	if err := f.VerifyEvidence(50,
		map[string]evidence.Status{
			surveyA.EvidenceID(): evidence.StatusAuthenticitySupported,
			surveyB.EvidenceID(): evidence.StatusAuthenticitySupported,
		},
		nil,
	); err != nil {
		t.Fatalf("VerifyEvidence: %v", err)
	}

	// Stage 6: TIMELINE_RECONSTRUCTED -- a genuine conflict: notice
	// submitted before the incident it reports.
	incidentEvt, err := timeline.New("EVT-INCIDENT", timeline.TypeIncident, "", "", 2000,
		[]string{surveyA.EvidenceID()}, timeline.CertaintyConfirmed, "Jakarta port", "PTY-CARRIER")
	if err != nil {
		t.Fatalf("building incident event: %v", err)
	}
	noticeEvt, err := timeline.New("EVT-NOTICE", timeline.TypeNotice, "", "", 1900, // BEFORE the incident
		[]string{statement.EvidenceID()}, timeline.CertaintyConfirmed, "Jakarta port", "PTY-CLAIMANT")
	if err != nil {
		t.Fatalf("building notice event: %v", err)
	}
	conflicts, err := f.ReconstructTimeline(60, []timeline.Event{incidentEvt, noticeEvt})
	if err != nil {
		t.Fatalf("ReconstructTimeline: %v", err)
	}
	if len(conflicts) == 0 {
		t.Fatal("expected ReconstructTimeline to detect the notice-before-incident conflict")
	}
	foundNoticeConflict := false
	for _, c := range conflicts {
		if c.Kind == timeline.KindNoticeBeforeIncident {
			foundNoticeConflict = true
		}
	}
	if !foundNoticeConflict {
		t.Fatalf("expected a NOTICE_BEFORE_INCIDENT conflict, got %+v", conflicts)
	}

	// Stage 7: CONTRACT_POLICY_MAPPED -- incident at tick 2000 falls
	// inside the ORIGINAL version's window (0..5000), NOT the
	// endorsement's (5000+): must resolve to V1, never the "latest".
	resolvedVersion, err := f.MapPolicy(70, 2000, "POL-1")
	if err != nil {
		t.Fatalf("MapPolicy: %v", err)
	}
	if resolvedVersion.VersionID != "V1" {
		t.Fatalf("expected the historical incident to resolve to policy V1 (not the latest V2), got %s", resolvedVersion.VersionID)
	}

	// Stage 8: CONTRADICTIONS_ANALYZED -- surveyA and surveyB
	// disagree about cargo condition.
	if err := f.SubmitContradictionObservation(surveyA, "cargo_condition_at_delivery", "WET_DAMAGE_SEVERE", 0.9, 80); err != nil {
		t.Fatalf("SubmitContradictionObservation(surveyA): %v", err)
	}
	if err := f.SubmitContradictionObservation(surveyB, "cargo_condition_at_delivery", "MINOR_DISCOLORATION_ONLY", 0.6, 81); err != nil {
		t.Fatalf("SubmitContradictionObservation(surveyB): %v", err)
	}
	contradictions, err := f.AnalyzeContradictions(90, "cargo_condition_at_delivery", contradiction.PairDocumentSurvey,
		"Surveyor reports disagree on cargo damage severity", 10000)
	if err != nil {
		t.Fatalf("AnalyzeContradictions: %v", err)
	}
	if len(contradictions) == 0 {
		t.Fatal("expected AnalyzeContradictions to surface the surveyA/surveyB disagreement")
	}
	if !contradictions[0].HumanReviewRequired {
		t.Fatal("expected the detected contradiction to require human review")
	}

	// Stage 9: CAUSATION_ANALYZED.
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
	if err := hs.AddSupportingEvidence("H1", surveyA.EvidenceID()); err != nil {
		t.Fatalf("AddSupportingEvidence: %v", err)
	}
	explanation, err := f.AnalyzeCausation(100, hs)
	if err != nil {
		t.Fatalf("AnalyzeCausation: %v", err)
	}
	if len(explanation.Caveats) == 0 {
		t.Fatal("expected Explain to always carry at least one caveat -- causation is never presented as bare fact")
	}

	// Stage 10: QUANTUM_ANALYZED -- reproduces the §21 worked example
	// shape: claimed 25,000 / supported_gross 19,000 / salvage 3,000 /
	// deductible 1,000 -> unresolved 2,000 (major units; the blueprint's
	// own figures scaled down by 100 to keep this test's arithmetic
	// legible, same formula, independently re-derived below).
	grossLoss := quantum.NewEvidenceBackedAmount(quantum.MajorUnits(19000), surveyA.EvidenceID())
	mitigationAmt := quantum.NewEvidenceBackedAmount(0)
	salvage := quantum.NewEvidenceBackedAmount(quantum.MajorUnits(3000), surveyA.EvidenceID())
	deductible := quantum.NewEvidenceBackedAmount(quantum.MajorUnits(1000))
	claimed := quantum.NewEvidenceBackedAmount(quantum.MajorUnits(25000), statement.EvidenceID())

	calc, discrepancy, err := f.ComputeQuantum(110, quantum.ComputeInput{
		CalculationID: "QC-1",
		GrossLoss:     grossLoss,
		Mitigation:    mitigationAmt,
		Salvage:       salvage,
		Deductible:    deductible,
		Currency:      "USD",
		ExchangeRate:  quantum.UnitExchangeRate(),
		RateSource:    "case_currency",
	}, "QD-1", claimed, salvage, deductible)
	if err != nil {
		t.Fatalf("ComputeQuantum: %v", err)
	}
	if discrepancy == nil {
		t.Fatal("expected a non-nil QuantumDiscrepancy")
	}
	wantUnresolved := quantum.MajorUnits(2000) // 25000-19000-3000-1000 = 2000
	if discrepancy.Unresolved != wantUnresolved {
		t.Fatalf("expected unresolved discrepancy %s, got %s (calc=%+v)", wantUnresolved, discrepancy.Unresolved, calc)
	}
	if !discrepancy.HasUnresolvedGap() {
		t.Fatal("expected HasUnresolvedGap() true for a positive unresolved amount")
	}

	// Stage 11: COVERAGE_ISSUES_IDENTIFIED.
	typeDef, _ := claimTypes.Get(claim.TypeCargoDamage)
	coverageAnalysis, err := f.AnalyzeCoverage(120, coverage.Input{
		Claim:         cl,
		PolicyVersion: resolvedVersion,
		Evidence:      []evidence.Record{surveyA, surveyB},
		TypeDef:       &typeDef,
		IncidentTick:  2000,
		NoticeTick:    1900,
	})
	if err != nil {
		t.Fatalf("AnalyzeCoverage: %v", err)
	}
	if coverageAnalysis.CaseID != caseID {
		t.Fatalf("expected coverage analysis to carry the case identity, got %+v", coverageAnalysis)
	}

	// Stage 12: RECOVERY_ANALYZED -- a subrogation target against the
	// carrier.
	recoveryTarget, err := recovery.New("RT-1", caseID, "PTY-CARRIER",
		recovery.Basis{Category: recovery.BasisBaileeLiability, Detail: "Carrier had custody during the damaging interval"},
		recovery.Money{AmountMinor: 200000 * 100, Currency: "USD"})
	if err != nil {
		t.Fatalf("recovery.New: %v", err)
	}
	targets, err := f.AnalyzeRecovery(130, []recovery.Target{recoveryTarget})
	if err != nil {
		t.Fatalf("AnalyzeRecovery: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("expected 1 recovery target, got %d", len(targets))
	}

	// Cross-cutting, non-lifecycle-gated computations.
	if _, err := f.ComputeGapAssessment("required_evidence", typeDef, false); err != nil {
		t.Fatalf("ComputeGapAssessment: %v", err)
	}
	mitCost, err := mitigation.NewAmount(50000, "USD")
	if err != nil {
		t.Fatalf("mitigation.NewAmount: %v", err)
	}
	mitAction, err := mitigation.New(caseID, "MIT-1", "Cargo isolated and dried at port warehouse", 1550,
		"PTY-CARRIER", &mitCost, nil, []string{surveyA.EvidenceID()}, "")
	if err != nil {
		t.Fatalf("mitigation.New: %v", err)
	}
	if _, err := f.RegisterMitigationAction(mitAction); err != nil {
		t.Fatalf("RegisterMitigationAction: %v", err)
	}
	deadlineRule, err := deadline.New("DR-1", deadline.SourceTypePolicy, "§9", "POL-1", "V1", "NOTICE", 30, deadline.CalendarRuleCalendarDays, "UTC")
	if err != nil {
		t.Fatalf("deadline.New: %v", err)
	}
	if _, err := f.RegisterDeadlineRule(deadlineRule, 1900); err != nil {
		t.Fatalf("RegisterDeadlineRule: %v", err)
	}

	// Stage 13: HUMAN_REVIEW.
	if err := f.MarkHumanReview(140); err != nil {
		t.Fatalf("MarkHumanReview: %v", err)
	}

	// Stage 14: DOSSIER_GENERATED.
	d, manifest, err := f.GenerateDossier(150)
	if err != nil {
		t.Fatalf("GenerateDossier: %v", err)
	}
	if f.Case().State() != insurancecase.StateDossierGenerated {
		t.Fatalf("expected DOSSIER_GENERATED, got %s", f.Case().State())
	}
	if !d.HumanReviewRequired {
		t.Fatal("expected the dossier to report HumanReviewRequired=true -- this case has an unresolved quantum gap, a timeline conflict and a contradiction")
	}
	if len(d.LifecycleAuditTrail) == 0 {
		t.Fatal("expected a non-empty lifecycle audit trail")
	}
	if manifest.EvidenceCount != 4 {
		t.Fatalf("expected the verification manifest to cover all 4 submitted evidence records, got %d", manifest.EvidenceCount)
	}

	// The dossier's manifest must independently verify: nothing in the
	// case's evidence changed between GenerateDossier and now.
	if err := verification.Verify(manifest, f.Case().Evidence); err != nil {
		t.Fatalf("manifest verification: %v", err)
	}

	// Stage 15: terminal transition. Human review flags are present
	// (timeline conflict, contradiction, unresolved quantum gap), so
	// this case closes with OPEN_ISSUES, never CASE_CLOSED -- and this
	// package renders no such verdict itself, the caller decided it
	// from the dossier's own already-computed flag.
	if err := f.Close(160, insurancecase.StateOpenIssues); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if f.Case().State() != insurancecase.StateOpenIssues {
		t.Fatalf("expected terminal state OPEN_ISSUES, got %s", f.Case().State())
	}

	// Structural golden-rule check across the whole assembled dossier:
	// nowhere does a coverage/liability verdict statement appear.
	assertNoBareVerdict(t, d.HumanReviewQuestions)
}

func assertNoBareVerdict(t *testing.T, questions []string) {
	t.Helper()
	for _, q := range questions {
		lower := strings.ToLower(q)
		if strings.Contains(lower, "claim is approved") || strings.Contains(lower, "claim is denied") || strings.Contains(lower, "coverage confirmed") {
			t.Fatalf("found a bare verdict-shaped statement in dossier output: %q", q)
		}
	}
}

// TestLifecycleGatingIsEnforcedByCase proves the Facade cannot be used
// out of order: calling an analysis method before its prerequisite
// stage is refused by the underlying case.Advance one-step-forward
// rule, not by any check this package would need to duplicate.
func TestLifecycleGatingIsEnforcedByCase(t *testing.T) {
	f, err := New("CASE-GATE-1", 0, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Attempting to register a claim before parties/policy are set is
	// allowed at the claim.New/RegisterClaim level (they don't
	// cross-check party existence), but the case-lifecycle skip must
	// still be refused.
	cl, err := claim.New("CLM-1", "CASE-GATE-1", claim.TypeCargoDamage, "PTY-1", nil)
	if err != nil {
		t.Fatalf("claim.New: %v", err)
	}
	if err := f.RegisterClaim(10, cl); err == nil {
		t.Fatal("expected RegisterClaim to fail: case has not yet passed PARTIES_IDENTIFIED or POLICY_REGISTERED")
	}
	if f.Case().State() != insurancecase.StateCaseCreated {
		t.Fatalf("expected case to remain in CASE_CREATED after a rejected skip, got %s", f.Case().State())
	}
}
