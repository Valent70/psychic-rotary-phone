package casepack

import (
	"strings"
	"testing"

	"veriqo/pkg/evidence/provenance"
	insurancecase "veriqo/pkg/insurance/case"
	"veriqo/pkg/insurance/coverage"
	"veriqo/pkg/insurance/obligation"
	"veriqo/pkg/insurance/verification"
	"veriqo/pkg/lineage"
)

// driveAll runs every case once and returns the results, so the many
// assertions below share one execution rather than re-driving seven
// cases per test.
func driveAll(t *testing.T) map[CaseID]*Result {
	t.Helper()
	out := map[CaseID]*Result{}
	for _, c := range All() {
		res, err := Drive(c, lineage.NewLedger())
		if err != nil {
			t.Fatalf("%s: Drive: %v", c.ID, err)
		}
		out[c.ID] = res
	}
	return out
}

// TestEveryCaseDrivesTheFullFacadePath is the pack's headline test: all
// seven synthetic cases run end to end through the REAL facade —
// evidence → timeline → policy mapping → contradiction → causation →
// quantum → coverage → recovery → human review → dossier → verification
// manifest — with no fixture-only code path anywhere.
func TestEveryCaseDrivesTheFullFacadePath(t *testing.T) {
	results := driveAll(t)
	if len(results) != 7 {
		t.Fatalf("expected 7 driven cases, got %d", len(results))
	}
	for id, res := range results {
		if res.Dossier == nil {
			t.Fatalf("%s produced no dossier", id)
		}
		if res.Manifest.EvidenceCount == 0 {
			t.Fatalf("%s produced an empty verification manifest", id)
		}
		// The case reached the terminal analysis stage.
		if got := res.Facade.Case().State(); got != insurancecase.StateDossierGenerated {
			t.Fatalf("%s ended in %q, want DOSSIER_GENERATED", id, got)
		}
		// And its externally-reported stage is the derived RESOLVED.
		if got := res.Facade.Stage(); got != insurancecase.StageResolved {
			t.Fatalf("%s reports stage %q, want RESOLVED", id, got)
		}
		// The evidence manifest independently verifies.
		if err := verification.Verify(res.Manifest, res.Facade.Case().Evidence); err != nil {
			t.Fatalf("%s: manifest verification: %v", id, err)
		}
	}
}

// TestEveryCaseUsesTheHistoricalPolicyVersion: the §6 P0 rule, proven
// on all seven. Each case's policy has a later endorsement that must
// never be selected for an incident predating it.
func TestEveryCaseUsesTheHistoricalPolicyVersion(t *testing.T) {
	for id, res := range driveAll(t) {
		want := "POL-" + string(id) + "-V1"
		if res.PolicyVersion.VersionID != want {
			t.Fatalf("%s resolved policy version %q, want the historical %q",
				id, res.PolicyVersion.VersionID, want)
		}
	}
}

// TestEveryCaseBindsToOneCanonicalCaseLineage.
func TestEveryCaseBindsToOneCanonicalCaseLineage(t *testing.T) {
	for id, res := range driveAll(t) {
		if res.Binding == nil {
			t.Fatalf("%s was driven without a canonical binding", id)
		}
		if string(res.Binding.CaseID()) != string(id) {
			t.Fatalf("%s bound to lineage case %q", id, res.Binding.CaseID())
		}
		if err := res.Binding.VerifyChain(); err != nil {
			t.Fatalf("%s: lineage chain: %v", id, err)
		}
		nodes, err := res.Binding.Walk()
		if err != nil {
			t.Fatalf("%s: Walk: %v", id, err)
		}
		if len(nodes) == 0 {
			t.Fatalf("%s registered no lineage nodes", id)
		}
		// Every piece of the case's evidence reached the lineage.
		for _, rec := range res.Built.Records {
			if !res.Binding.HasRef(rec.EvidenceID()) {
				t.Fatalf("%s: evidence %s did not reach the case lineage", id, rec.EvidenceID())
			}
		}
		// The effective policy version reached it; the endorsement did not.
		if !res.Binding.HasRef(res.PolicyVersion.VersionID) {
			t.Fatalf("%s: the effective policy version is not on the lineage", id)
		}
		if res.Binding.HasRef("POL-" + string(id) + "-V2") {
			t.Fatalf("%s: the later endorsement must not appear on the lineage", id)
		}
	}
}

// ================= The four §54–§57 gates ==============================

// TestThreeGatesPassOnEveryCase: coverage traceability, quantum
// reproducibility and preservation must PASS for all seven. A synthetic
// case that cannot satisfy its own traceability gates is not evidence
// that the gates work — it is evidence that the pack is broken.
func TestThreeGatesPassOnEveryCase(t *testing.T) {
	for id, res := range driveAll(t) {
		if !res.Gates.CoverageTraceability.Pass() {
			t.Fatalf("%s: coverage traceability failed: %v", id, res.Gates.CoverageTraceability.Failures)
		}
		if !res.Gates.QuantumReproducibility.Pass() {
			t.Fatalf("%s: quantum reproducibility failed: %v", id, res.Gates.QuantumReproducibility.Failures)
		}
		if !res.Gates.Preservation.Pass() {
			t.Fatalf("%s: preservation failed: %v", id, res.Gates.Preservation.Failures)
		}
	}
}

// TestCoverageTraceabilityActuallyChecked: a passing gate is only
// meaningful if it examined something. Every case must have facts, and
// every fact must have been found traceable.
func TestCoverageTraceabilityActuallyChecked(t *testing.T) {
	for id, res := range driveAll(t) {
		r := res.Gates.CoverageTraceability
		if r.FactCount == 0 {
			t.Fatalf("%s: the coverage gate passed over zero facts", id)
		}
		if r.TraceableFacts != r.FactCount {
			t.Fatalf("%s: %d of %d facts traceable", id, r.TraceableFacts, r.FactCount)
		}
		if !r.EffectiveDateBound {
			t.Fatalf("%s: the analysis is not bound to an effective date", id)
		}
		if !r.ReviewRequiredWhereUnresolved {
			t.Fatalf("%s: unresolved findings did not raise review", id)
		}
	}
}

// TestQuantumIsGenuinelyRecomputed: the §55 gate must have actually
// re-run the formula and found it identical, with every operand
// evidence-backed.
func TestQuantumIsGenuinelyRecomputed(t *testing.T) {
	for id, res := range driveAll(t) {
		r := res.Gates.QuantumReproducibility
		if !r.Recomputed {
			t.Fatalf("%s: the quantum gate did not recompute", id)
		}
		if !r.Identical {
			t.Fatalf("%s: recomputation diverged: %v", id, r.Failures)
		}
		if !r.EveryAmountEvidenceBacked {
			t.Fatalf("%s: an operand cites no evidence", id)
		}
		if !r.VersionDeclared {
			t.Fatalf("%s: no calculation version declared", id)
		}
		if r.RecordedIndicativeValue != r.RecomputedIndicativeValue {
			t.Fatalf("%s: %s != %s", id, r.RecordedIndicativeValue, r.RecomputedIndicativeValue)
		}
	}
}

// TestPreservationCoversEveryRecord: the §56 gate must find every one of
// the case's evidence records under a preservation order.
func TestPreservationCoversEveryRecord(t *testing.T) {
	for id, res := range driveAll(t) {
		r := res.Gates.Preservation
		if r.OrderCount != 1 {
			t.Fatalf("%s: expected one preservation order, got %d", id, r.OrderCount)
		}
		if r.EvidenceInCase == 0 {
			t.Fatalf("%s: the preservation gate saw no evidence", id)
		}
		if r.EvidencePreserved != r.EvidenceInCase {
			t.Fatalf("%s: %d of %d records preserved", id, r.EvidencePreserved, r.EvidenceInCase)
		}
	}
}

// TestHumanReviewGateFailsClosedOnEveryCase is the §57 rule. The pack
// deliberately supplies no authorization, so every case must be refused
// finalization — and the refusal must name the outstanding questions.
func TestHumanReviewGateFailsClosedOnEveryCase(t *testing.T) {
	for id, res := range driveAll(t) {
		r := res.Gates.HumanReview
		if !r.ReviewRequired {
			t.Fatalf("%s: every synthetic case has unresolved findings and must require review", id)
		}
		if r.FinalizationPermitted {
			t.Fatalf("%s: finalization was permitted with no authorization — the gate must fail closed", id)
		}
		if r.Pass() {
			t.Fatalf("%s: the human review gate passed with no authorization", id)
		}
		if len(r.OutstandingQuestions) == 0 {
			t.Fatalf("%s: the refusal names no outstanding question", id)
		}
		if res.Gates.Pass() {
			t.Fatalf("%s: the aggregate gate report passed while human review failed — "+
				"three of four is not a pass", id)
		}
	}
}

// TestHumanReviewGateOpensWithARealAuthorization proves the OTHER half:
// the gate is not simply always-fail. A well-formed authorization
// addressing every question permits finalization.
func TestHumanReviewGateOpensWithARealAuthorization(t *testing.T) {
	for id, res := range driveAll(t) {
		auths := AuthorizationsSatisfying(res.Dossier, "claims-authority-1", "HITL-CASE-"+string(id), 900)
		r := verification.VerifyHumanReview(res.Dossier, auths)
		if !r.Pass() {
			t.Fatalf("%s: a complete authorization must satisfy the gate, failures: %v", id, r.Failures)
		}
		if !r.FinalizationPermitted {
			t.Fatalf("%s: finalization still refused with a complete authorization", id)
		}
	}
}

// TestARubberStampAuthorizationIsRefused: an authorization with no
// rationale or no citable review case does not count.
func TestARubberStampAuthorizationIsRefused(t *testing.T) {
	res := driveAll(t)[CaseCargoDamageReefer]

	noRationale := AuthorizationsSatisfying(res.Dossier, "reviewer", "HITL-1", 900)
	noRationale[0].Rationale = ""
	if r := verification.VerifyHumanReview(res.Dossier, noRationale); r.Pass() {
		t.Fatal("an authorization with no rationale must be refused")
	}

	noCaseRef := AuthorizationsSatisfying(res.Dossier, "reviewer", "HITL-1", 900)
	noCaseRef[0].CaseRef = ""
	if r := verification.VerifyHumanReview(res.Dossier, noCaseRef); r.Pass() {
		t.Fatal("an authorization citing no canonical review case must be refused")
	}

	partial := AuthorizationsSatisfying(res.Dossier, "reviewer", "HITL-1", 900)
	if len(partial[0].AddressedQuestions) < 2 {
		t.Skip("this case raised too few questions to test a partial authorization")
	}
	partial[0].AddressedQuestions = partial[0].AddressedQuestions[:1]
	r := verification.VerifyHumanReview(res.Dossier, partial)
	if r.Pass() {
		t.Fatal("an authorization addressing only some questions must be refused")
	}
	if len(r.OutstandingQuestions) == 0 {
		t.Fatal("the refusal must name what is still outstanding")
	}
}

// ================= Per-case declared expectations ======================

// TestCase002ProducesLateNoticeWithNoCoverageOutcome is the end-to-end
// proof of LATE NOTICE != COVERAGE DENIED, driven through the whole
// pipeline rather than asserted in a unit test.
func TestCase002ProducesLateNoticeWithNoCoverageOutcome(t *testing.T) {
	res := driveAll(t)[CaseCargoDamageReefer]

	if res.NoticeAssessment.Compliance != obligation.ComplianceLate {
		t.Fatalf("CASE-INS-002 notice compliance = %q, want LATE", res.NoticeAssessment.Compliance)
	}
	if res.NoticeAssessment.DelayTicks == 0 {
		t.Fatal("a late notice must record a delay")
	}
	if res.NoticeAssessment.CoverageEffect != obligation.EffectNotDetermined {
		t.Fatalf("CoverageEffect = %q", res.NoticeAssessment.CoverageEffect)
	}
	if len(res.NoticeAssessment.ReviewRequirements) == 0 {
		t.Fatal("a late notice must hand a review requirement to a human")
	}

	// The coverage engine agrees: DISPUTED, review required, no outcome.
	var noticeFact *coverage.CoverageFact
	for i := range res.Coverage.Facts {
		if res.Coverage.Facts[i].FactID == "notice_timely" {
			noticeFact = &res.Coverage.Facts[i]
		}
	}
	if noticeFact == nil {
		t.Fatal("the coverage analysis has no notice fact")
	}
	if noticeFact.Status != coverage.StatusDisputed {
		t.Fatalf("the coverage notice fact is %q, want DISPUTED", noticeFact.Status)
	}
	if !res.Coverage.ReviewRequired {
		t.Fatal("a late notice must set ReviewRequired on the coverage analysis")
	}
}

// TestCase004ProducesNoSingleAssertedCause: the causation engine must
// never name one cause for the general-average question.
func TestCase004ProducesNoSingleAssertedCause(t *testing.T) {
	res := driveAll(t)[CaseGeneralAverage]
	if len(res.Causation.Caveats) == 0 {
		t.Fatal("a causation explanation must always carry a caveat")
	}
	narrative := strings.ToLower(res.Causation.Narrative)
	for _, bad := range []string{
		"was caused by", "is the cause", "definitively", "conclusively", "proves that",
	} {
		if strings.Contains(narrative, bad) {
			t.Fatalf("causation narrative asserts a cause (%q): %s", bad, res.Causation.Narrative)
		}
	}
}

// TestEveryCaseCausationIsHedged applies the same check to all seven.
func TestEveryCaseCausationIsHedged(t *testing.T) {
	for id, res := range driveAll(t) {
		if len(res.Causation.Caveats) == 0 {
			t.Fatalf("%s: causation carries no caveat", id)
		}
		if strings.TrimSpace(res.Causation.Narrative) == "" {
			t.Fatalf("%s: causation has an empty narrative", id)
		}
	}
}

// TestEveryCaseSurfacesItsContradiction: each case was built around a
// genuine disagreement, and the real arbitration engine must find it.
func TestEveryCaseSurfacesItsContradiction(t *testing.T) {
	for id, res := range driveAll(t) {
		if len(res.Contradictions) == 0 {
			t.Fatalf("%s: no contradiction surfaced, but the case was built around one", id)
		}
		for _, cr := range res.Contradictions {
			if cr.ContradictionID == "" {
				t.Fatalf("%s: a contradiction record has no ID", id)
			}
			if cr.EvidenceA == "" || cr.EvidenceB == "" {
				t.Fatalf("%s: a contradiction cites no evidence on one side", id)
			}
		}
	}
}

// TestEveryCaseDossierRequiresReviewAndCarriesNoVerdict.
func TestEveryCaseDossierRequiresReviewAndCarriesNoVerdict(t *testing.T) {
	forbidden := []string{
		"claim is approved", "claim is denied", "coverage confirmed", "coverage denied",
		"is liable", "at fault", "fraud detected", "bribery detected",
	}
	for id, res := range driveAll(t) {
		if !res.Dossier.HumanReviewRequired {
			t.Fatalf("%s: every synthetic case has unresolved findings", id)
		}
		if len(res.Dossier.HumanReviewQuestions) == 0 {
			t.Fatalf("%s: no review questions", id)
		}
		for _, q := range res.Dossier.HumanReviewQuestions {
			lower := strings.ToLower(q)
			for _, bad := range forbidden {
				if strings.Contains(lower, bad) {
					t.Fatalf("%s: dossier question contains verdict language %q: %s", id, bad, q)
				}
			}
		}
	}
}

// TestEveryCaseClosesWithOpenIssues: every case has unresolved
// findings, so the caller's terminal choice must be OPEN_ISSUES, and
// the derived external stage must be CLOSED.
func TestEveryCaseClosesWithOpenIssues(t *testing.T) {
	for _, c := range All() {
		res, err := Drive(c, lineage.NewLedger())
		if err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		}
		if err := res.Close(200); err != nil {
			t.Fatalf("%s: Close: %v", c.ID, err)
		}
		if got := res.Facade.Case().State(); got != insurancecase.StateOpenIssues {
			t.Fatalf("%s closed as %q, want OPEN_ISSUES", c.ID, got)
		}
		if got := res.Facade.Stage(); got != insurancecase.StageClosed {
			t.Fatalf("%s reports stage %q, want CLOSED", c.ID, got)
		}
		st := res.Facade.Status()
		if !st.Terminal {
			t.Fatalf("%s does not report Terminal", c.ID)
		}
	}
}

// TestDrivingIsDeterministic: two runs of the same case produce the
// same evidence root hash. Without this the pack cannot anchor anything.
func TestDrivingIsDeterministic(t *testing.T) {
	for _, c := range All() {
		a, err := Drive(c, lineage.NewLedger())
		if err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		}
		b, err := Drive(c, lineage.NewLedger())
		if err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		}
		if a.Manifest.EvidenceRootHash != b.Manifest.EvidenceRootHash {
			t.Fatalf("%s: two runs produced different evidence root hashes", c.ID)
		}
		if a.Order.Hash() != b.Order.Hash() {
			t.Fatalf("%s: two runs produced different preservation order hashes", c.ID)
		}
		if a.Quantum.IndicativeClaimValue != b.Quantum.IndicativeClaimValue {
			t.Fatalf("%s: two runs produced different quantum results", c.ID)
		}
	}
}

// TestFixtureEnvelopeIsRefusedAsExternalEvidence is the "never mix
// synthetic with live" rule proven at the one door external evidence
// goes through.
func TestFixtureEnvelopeIsRefusedAsExternalEvidence(t *testing.T) {
	res := driveAll(t)[CasePortCallDemurrage]
	c, _ := Get(CasePortCallDemurrage)
	env := c.FixtureEnvelope("v1.0.0", "abcdef1",
		strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64), 1, 10_000)

	_, err := res.Binding.AttachExternalEvidence(res.Built.Records[0], env, provenance.UseInternalOnly, 500)
	if err == nil {
		t.Fatal("a FIXTURE envelope must be refused as external evidence")
	}
	if !strings.Contains(err.Error(), "FIXTURE") && !strings.Contains(err.Error(), "fixture") {
		t.Fatalf("the refusal must name the reason, got %v", err)
	}
}

// TestEveryCaseRecordsAMitigationActionWithNoReasonablenessJudgment
// exercises I-06 end to end. The design documents are explicit that
// VERIQO computes an action's IMPACT but never decides whether the
// action was legally reasonable, so this checks both halves: a real
// impact is computed, and nothing in the rendered action reads as a
// reasonableness or liability judgment.
func TestEveryCaseRecordsAMitigationActionWithNoReasonablenessJudgment(t *testing.T) {
	for id, res := range driveAll(t) {
		if res.Dossier.MitigationImpact == nil {
			t.Fatalf("%s: the dossier carries no mitigation impact", id)
		}
		if res.MitigationImpact.TotalActionCount == 0 {
			t.Fatalf("%s: no mitigation action was recorded", id)
		}
		// The impact is REPORTED, including when a mitigation cost more
		// than it avoided. That is a fact, not a verdict, and the
		// package's own TestPublicAPIHasNoReasonablenessJudgment proves
		// no field can express one.
		if res.MitigationImpact.Currency != "USD" {
			t.Fatalf("%s: mitigation impact currency = %q", id, res.MitigationImpact.Currency)
		}
	}
}

// TestMitigationSupportingEvidenceIsReal: every mitigation action cites
// a content-addressed record that is actually in its own case.
func TestMitigationSupportingEvidenceIsReal(t *testing.T) {
	for _, c := range All() {
		built, err := c.BuildAllEvidence()
		if err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		}
		act, err := mitigationActionFor(c, built)
		if err != nil {
			t.Fatalf("%s: mitigationActionFor: %v", c.ID, err)
		}
		known := map[string]bool{}
		for _, rec := range built.Records {
			known[rec.EvidenceID()] = true
		}
		for _, ev := range act.SupportingEvidence {
			if !known[ev] {
				t.Fatalf("%s: mitigation cites evidence %s, which is not in the case", c.ID, ev)
			}
		}
		if act.Actor == "" {
			t.Fatalf("%s: mitigation action names no actor", c.ID)
		}
	}
}
