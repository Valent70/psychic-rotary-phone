// Package adversarial holds the cross-cutting adversarial suite MIP-001
// §23 mandates: "Every adversarial case must have deterministic expected
// outcome."
//
// The per-package tests prove each component refuses its own attack in
// isolation. This suite proves the components COMPOSE -- that an
// attacker who defeats one layer is still stopped, and that a claim
// cannot be laundered by routing it through a different subsystem.
// Composition is where layered defences usually fail: each guard is
// correct alone and the seam between two of them is not.
//
// Each test below names the §23 case it covers.
package adversarial

import (
	"strings"
	"testing"

	aigateway "veriqo/pkg/ai/gateway"
	"veriqo/pkg/constitution"
	"veriqo/pkg/contract/event"
	"veriqo/pkg/disclosure/access"
	"veriqo/pkg/qualification/independence"
	"veriqo/pkg/qualification/nextbest"
	"veriqo/pkg/qualification/observability"
	qstate "veriqo/pkg/qualification/state"
)

// fullyAssessed builds source attributes covering every disqualifying
// dimension, so a pair can legitimately reach INDEPENDENT.
func fullyAssessed(suffix string) map[independence.Dimension]string {
	m := map[independence.Dimension]string{}
	for _, d := range independence.DisqualifyingDimensions() {
		m[d] = string(d) + "-" + suffix
	}
	return m
}

func verdictOf(t *testing.T, rs []constitution.Result, article int) constitution.Result {
	t.Helper()
	for _, r := range rs {
		if r.Article == article {
			return r
		}
	}
	t.Fatalf("article %d missing", article)
	return constitution.Result{}
}

// §23 "Shared upstream source" -- END TO END.
//
// An analyst has two feeds with different vendor names and wants to
// claim corroboration. The independence layer clusters them to one
// effective source; the constitution refuses the claim; and the
// single-source exception is then the ONLY honest route forward -- and
// it yields a state that cannot be quoted as corroboration.
func TestSharedUpstreamSourceCannotBecomeCorroborationByAnyRoute(t *testing.T) {
	a := fullyAssessed("a")
	b := fullyAssessed("b")
	b[independence.RootOrigin] = a[independence.RootOrigin] // same constellation

	// Route 1: ask the independence layer directly.
	n, err := independence.EffectiveSourceCount([]independence.Source{
		{ID: "vendor-alpha", Attributes: a}, {ID: "vendor-beta", Attributes: b},
	})
	if err != nil {
		t.Fatalf("EffectiveSourceCount: %v", err)
	}
	if n != 1 {
		t.Fatalf("two same-root feeds are ONE effective source, got %d", n)
	}

	// Route 2: assert it constitutionally anyway.
	res := constitution.Check(constitution.Subject{
		Corroboration: &constitution.CorroborationFacts{
			ClaimedIndependent: true,
			SourceRoots:        map[string]string{"vendor-alpha": "constellation-X", "vendor-beta": "constellation-X"},
			DependencyKnown:    map[string]bool{"vendor-alpha|vendor-beta": true},
		},
	})
	if v := verdictOf(t, res, 3); v.Verdict != "VIOLATED" {
		t.Fatalf("Article 3 must refuse same-root corroboration, got %s", v.Verdict)
	}

	// Route 3: fall back to a single-source exception -- the honest
	// route. It must NOT produce anything quotable as corroboration.
	got, err := qstate.Apply(qstate.SingleSourceException{
		ClaimID: "C-1", SourceID: "vendor-alpha",
		WhyNecessary: "one constellation covers the window", WhyAlternativesUnavailable: "no other provider",
		SourceAssurance: "audited", Coverage: "full window", KnownLimitations: "no berth resolution",
		Reviewer: "analyst-1", PolicyVersion: "policy-v1", ReviewTick: 100,
	}, n, 50)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got != qstate.SupportedBySingleHighAssuranceSource {
		t.Fatalf("expected the single-source state, got %s", got)
	}
	if got.AssertsLegalConclusion() {
		t.Fatal("the single-source state must not assert a legal conclusion")
	}
	if strings.Contains(string(got), "CORROBORATED") {
		t.Fatal("the single-source state must not read as corroboration")
	}
}

// §23 "AI direct connector attempt" + "AI trust manipulation" +
// "AI policy manipulation" -- composed.
//
// An AI holding every disclosure right, an explicit privilege
// override, and enclave-level content still cannot perform any action
// carrying evidence authority. Article 8 is not a permission.
func TestMaximallyPrivilegedAIStillCannotTouchEvidenceAuthority(t *testing.T) {
	p := aigateway.Policy{
		AllowedModels: []string{"aureum-1"}, AllowedJurisdictions: []string{"SG"},
		LicensesPermittingAI: []string{"lic-ok"},
		PurposeFields:        map[string][]string{"summarize": {"evidence_id"}},
	}
	g := access.Grant{
		EvidenceVersionID: "EV-1", RecipientID: "runner", RecipientRole: "service",
		Content: access.C5PrivilegedEnclave, Rights: access.Rights(),
		PolicyVersion: "policy-v1", Privilege: access.PrivilegeConfirmed,
	}

	for _, action := range aigateway.ForbiddenActions() {
		d, err := aigateway.Evaluate(p, g, aigateway.Request{
			Model:             aigateway.Model{ID: "aureum-1", Version: "1.4", TrainingPermitted: true},
			EvidenceVersionID: "EV-1", RecipientID: "runner",
			Right: access.AIProcess, Purpose: "summarize", Action: action,
			Jurisdiction: "SG", License: "lic-ok", Tick: 10,
			PrivilegeOverride: "tribunal order",
		})
		if err != nil {
			t.Fatalf("Evaluate %s: %v", action, err)
		}
		if d.Allowed {
			t.Fatalf("action %q must be refused even to a maximally-privileged AI", action)
		}
	}

	// And the constitution agrees, independently of the gateway.
	res := constitution.Check(constitution.Subject{
		AI: &constitution.AIFacts{ModelID: "aureum-1", Actions: aigateway.ForbiddenActions()},
	})
	if v := verdictOf(t, res, 8); v.Verdict != "VIOLATED" {
		t.Fatalf("Article 8 must refuse, got %s", v.Verdict)
	}
}

// §23 "Privilege leakage" + "Asymmetric access" -- composed.
//
// Privileged material must not reach a model, and the refusal must
// hold whichever AI right is attempted.
func TestPrivilegedMaterialCannotReachAModelByAnyAIRight(t *testing.T) {
	p := aigateway.Policy{
		AllowedModels: []string{"m"}, AllowedJurisdictions: []string{"SG"},
		LicensesPermittingAI: []string{"lic"},
		PurposeFields:        map[string][]string{"analyse": {"evidence_id"}},
	}
	g := access.Grant{
		EvidenceVersionID: "EV-1", RecipientID: "r", Content: access.C5PrivilegedEnclave,
		Rights: access.AIRights(), PolicyVersion: "p1", Privilege: access.PrivilegeConfirmed,
	}
	for _, right := range access.AIRights() {
		d, err := aigateway.Evaluate(p, g, aigateway.Request{
			Model:             aigateway.Model{ID: "m", Version: "1", TrainingPermitted: true},
			EvidenceVersionID: "EV-1", RecipientID: "r", Right: right,
			Purpose: "analyse", Jurisdiction: "SG", License: "lic", Tick: 1,
		})
		if err != nil {
			t.Fatalf("Evaluate %s: %v", right, err)
		}
		if d.Allowed {
			t.Fatalf("privileged material reached a model via %s", right)
		}
	}
}

// §23 "Narrative injection" / "Source selection after result".
//
// An analyst who already knows the answer tries to steer acquisition
// toward a source with no rights. The hard filter removes it BEFORE
// scoring, so no diagnostic value can rescue it.
func TestSourceSteeringCannotDefeatRightsByScoring(t *testing.T) {
	steered := nextbest.Candidate{
		ID: "steered", SourceID: "party-controlled",
		RightsGranted: false, AuthorityGranted: true, SourcePermitted: true, WithinCaseScope: true,
		DiagnosticValue: 1, Independence: 1, Relevance: 1, Freshness: 1, AcquisitionFeasibility: 1,
		Cost: 0.001, Latency: 0.001, RightsRisk: 0.001,
	}
	lawful := nextbest.Candidate{
		ID: "lawful", SourceID: "neutral",
		RightsGranted: true, AuthorityGranted: true, SourcePermitted: true, WithinCaseScope: true,
		DiagnosticValue: 0.2, Independence: 0.2, Relevance: 0.2, Freshness: 0.2, AcquisitionFeasibility: 0.2,
		Cost: 5, Latency: 5, RightsRisk: 5,
	}
	r, err := nextbest.Rank([]nextbest.Candidate{steered, lawful})
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	best, ok := nextbest.Best(r)
	if !ok || best != "lawful" {
		t.Fatalf("the rights-denied candidate must never win; got %q", best)
	}
	if len(r.Excluded) != 1 || r.Excluded[0].Reason != nextbest.NoRights {
		t.Fatalf("the exclusion must be visible and reasoned, got %+v", r.Excluded)
	}
}

// §23 "Party credential attack" -- party-mediated evidence cannot
// satisfy an independence requirement.
func TestPartyMediatedEvidenceCannotSatisfyIndependence(t *testing.T) {
	c := nextbest.Candidate{
		ID: "party-doc", RightsGranted: true, AuthorityGranted: true,
		SourcePermitted: true, WithinCaseScope: true,
		IndependenceRequired: true, PartyMediated: true,
		DiagnosticValue: 0.9, Independence: 0.9, Relevance: 0.9,
		Freshness: 0.9, AcquisitionFeasibility: 0.9,
		Cost: 1, Latency: 1, RightsRisk: 1,
	}
	r, err := nextbest.Rank([]nextbest.Candidate{c})
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	if len(r.Ranked) != 0 {
		t.Fatal("party-mediated evidence must not satisfy an independence requirement")
	}
	if r.Excluded[0].Reason != nextbest.PartyMediated {
		t.Fatalf("expected PARTY_MEDIATED exclusion, got %s", r.Excluded[0].Reason)
	}
}

// §23 "Dissent suppression" -- composed across the qualification
// vocabulary and the constitution.
//
// A reviewer's CRITICAL dissent is recorded and someone tries to
// publish a clean SUPPORTED finding. Both layers refuse independently.
func TestCriticalDissentCannotBeSuppressed(t *testing.T) {
	// Layer 1: the qualification constructor corrects the state.
	q, err := qstate.New("C-1", qstate.Supported, "policy-v1", "clean finding",
		[]string{"reviewer-2: acquisition legality disputed"}, 10)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if q.State != qstate.QualifiedWithDissent {
		t.Fatalf("material dissent must force QUALIFIED_WITH_DISSENT, got %s", q.State)
	}
	if len(q.MaterialDissent) == 0 {
		t.Fatal("the dissent must be carried, not dropped")
	}

	// Layer 2: the constitution catches the drop directly.
	res := constitution.Check(constitution.Subject{
		Dissent: &constitution.DissentFacts{Recorded: []string{"CRITICAL"}},
	})
	if v := verdictOf(t, res, 11); v.Verdict != "VIOLATED" {
		t.Fatalf("Article 11 must refuse a dropped CRITICAL dissent, got %s", v.Verdict)
	}
}

// §23 "Policy retroactivity" -- composed.
//
// Re-evaluating a historical case under today's policy is refused, and
// the event chain independently surfaces that two policy versions
// touched one case.
func TestPolicyRetroactivityIsRefusedAndVisible(t *testing.T) {
	res := constitution.Check(constitution.Subject{
		Policy: &constitution.PolicyFacts{
			CasePolicyVersion: "policy-2025-01", EvaluatedPolicyVersion: "policy-2026-09",
		},
	})
	if v := verdictOf(t, res, 7); v.Verdict != "VIOLATED" {
		t.Fatalf("Article 7 must refuse historical re-evaluation, got %s", v.Verdict)
	}

	c := event.NewChain()
	base := event.Envelope{
		EventID: "E1", TenantID: "t", CaseID: "CASE-1", EventType: "case.opened",
		AggregateType: "Case", AggregateID: "CASE-1",
		ActorID: "a", ActorType: event.ActorHuman, PolicyVersion: "policy-2025-01",
	}
	if _, err := c.Append(base, nil); err != nil {
		t.Fatalf("Append: %v", err)
	}
	base.EventID = "E2"
	base.EventType = "qualification.recorded"
	base.PolicyVersion = "policy-2026-09"
	if _, err := c.Append(base, nil); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if got := event.PolicyVersions(c.Events()); len(got) != 2 {
		t.Fatalf("both policy versions must be visible on the chain, got %v", got)
	}
}

// §23 "Ledger tampering" + "Hash mismatch" -- an attacker who edits
// one field and repairs the chain still cannot repair the payload
// binding, and vice versa.
func TestLedgerTamperingIsDetectableFromBothDirections(t *testing.T) {
	c := event.NewChain()
	base := event.Envelope{
		EventID: "E1", TenantID: "t", EventType: "evidence.ingested",
		AggregateType: "Evidence", AggregateID: "E-1",
		ActorID: "a", ActorType: event.ActorService, PolicyVersion: "p1",
	}
	rec, err := c.Append(base, map[string]any{"amount": 100})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	base.EventID = "E2"
	if _, err := c.Append(base, nil); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Direction 1: edit a hashed field -> chain verification fails.
	tampered := c.Events()
	tampered[0].Purpose = "changed"
	if err := event.VerifyChain(tampered); err == nil {
		t.Fatal("editing a hashed field must break chain verification")
	}

	// Direction 2: leave the chain perfect, swap the payload.
	if err := event.VerifyChain(c.Events()); err != nil {
		t.Fatalf("the untouched chain must verify: %v", err)
	}
	if err := event.VerifyPayload(rec, map[string]any{"amount": 999}); err == nil {
		t.Fatal("a swapped payload must fail payload verification even with a perfect chain")
	}
}

// §23 "Raw artifact missing" / "Parsed response survives without raw".
func TestParsedResponseCannotSurviveWithoutItsRaw(t *testing.T) {
	res := constitution.Check(constitution.Subject{
		Acquisition: &constitution.AcquisitionFacts{
			SourceID: "src-1", RightsChecked: true, RightsGranted: true,
			ContactMade: true, RawPreserved: false, Transformed: true,
		},
	})
	if v := verdictOf(t, res, 5); v.Verdict != "VIOLATED" {
		t.Fatalf("Article 5 must refuse a transformation without preserved raw, got %s", v.Verdict)
	}
}

// §23 "Source unavailable" -- an outage must not be reported as a
// finding about the world. This is the AIS case: "we were not
// receiving" is not "the vessel did not transmit".
func TestSourceOutageCannotBecomeAFindingAboutTheWorld(t *testing.T) {
	conds := observability.AllConditionsMet()
	conds[observability.OperationalAvailability] = false

	r, err := observability.Evaluate(observability.Assessment{
		Subject: "AIS transmission", SourceID: "ais-1", Conditions: conds, Material: true,
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if r.State == observability.ObservedAbsent {
		t.Fatal("a source outage must never yield OBSERVED_ABSENT")
	}
	if r.State.CarriesEvidentialWeight() {
		t.Fatalf("state %s must not carry evidential weight", r.State)
	}
	if r.State != observability.SourceUnavailable {
		t.Fatalf("expected SOURCE_UNAVAILABLE, got %s", r.State)
	}

	// Asserting it constitutionally anyway is refused.
	gate := map[string]bool{}
	for _, c := range constitution.ObservabilityGateConditions() {
		gate[c] = true
	}
	gate["operational_availability"] = false
	res := constitution.Check(constitution.Subject{
		Absence: &constitution.AbsenceFacts{ReportedState: "OBSERVED_ABSENT", GateConditions: gate},
	})
	if v := verdictOf(t, res, 29); v.Verdict != "VIOLATED" {
		t.Fatalf("Article 29 must refuse an ungated OBSERVED_ABSENT, got %s", v.Verdict)
	}
}

// §23 "Reviewer conflict" + "Commercial influence" -- both are
// RECORDED/DECLARED class. The system must surface them honestly
// rather than pretending a test settles them.
func TestUndeclaredConflictIsCaughtAndCommercialNeutralityIsNotFaked(t *testing.T) {
	res := constitution.Check(constitution.Subject{
		Procedure: &constitution.ProcedureFacts{ConflictsKnown: 3, ConflictsDeclared: 1},
	})
	if v := verdictOf(t, res, 14); v.Verdict != "VIOLATED" {
		t.Fatalf("Article 14 must catch undeclared conflicts, got %s", v.Verdict)
	}

	// Article 15 is a commercial arrangement. Without an attestation it
	// is NOT_EVALUABLE -- never silently satisfied.
	v15 := verdictOf(t, constitution.Check(constitution.Subject{}), 15)
	if v15.Verdict != "NOT_EVALUABLE" {
		t.Fatalf("Article 15 must be NOT_EVALUABLE without attestation, got %s", v15.Verdict)
	}
	if v15.Class != "DECLARED" {
		t.Fatalf("Article 15 must be DECLARED class, got %s", v15.Class)
	}
}

// §23 "Cross-tenant access" -- a grant to one recipient never serves
// another, and the AI gateway inherits that refusal.
func TestCrossRecipientAccessIsRefusedAtBothLayers(t *testing.T) {
	g := access.Grant{
		EvidenceVersionID: "EV-1", RecipientID: "tenant-a-runner",
		Content: access.C4Export, Rights: access.Rights(), PolicyVersion: "p1",
	}
	d, err := access.Evaluate(g, access.Request{
		EvidenceVersionID: "EV-1", RecipientID: "tenant-b-runner",
		Right: access.View, Tick: 1,
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if d.Allowed {
		t.Fatal("disclosure layer served a foreign recipient")
	}

	gw, err := aigateway.Evaluate(aigateway.Policy{
		AllowedModels: []string{"m"}, AllowedJurisdictions: []string{"SG"},
		LicensesPermittingAI: []string{"lic"},
		PurposeFields:        map[string][]string{"x": {"evidence_id"}},
	}, g, aigateway.Request{
		Model:             aigateway.Model{ID: "m", Version: "1"},
		EvidenceVersionID: "EV-1", RecipientID: "tenant-b-runner",
		Right: access.AIProcess, Purpose: "x", Jurisdiction: "SG", License: "lic", Tick: 1,
	})
	if err != nil {
		t.Fatalf("gateway Evaluate: %v", err)
	}
	if gw.Allowed {
		t.Fatal("AI gateway served a foreign recipient")
	}
}

// TestConstitutionalComplianceIsNeverClaimedFromSilence is the
// meta-adversarial case, and the one most likely to be got wrong in
// reporting: a caller must not read "no violations" as compliance when
// most articles were never judged.
func TestConstitutionalComplianceIsNeverClaimedFromSilence(t *testing.T) {
	res := constitution.Check(constitution.Subject{})
	if !constitution.NoViolations(res) {
		t.Fatal("an empty subject violates nothing")
	}
	ne := constitution.NotEvaluables(res)
	if len(ne) < 20 {
		t.Fatalf("an empty subject must leave most articles unjudged, got %d", len(ne))
	}
	// The honest reading: no violations AND many unjudged is not
	// compliance. This test exists so that any future change making
	// NoViolations imply full compliance fails loudly here.
	if len(ne) == 0 {
		t.Fatal("NoViolations would become a compliance claim; it must not")
	}
}
