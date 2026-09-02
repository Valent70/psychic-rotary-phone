package constitution

import (
	"strings"
	"testing"
)

// verdictOf finds one article's verdict in a result set.
func verdictOf(t *testing.T, results []Result, article int) Result {
	t.Helper()
	for _, r := range results {
		if r.Article == article {
			return r
		}
	}
	t.Fatalf("article %d missing from results", article)
	return Result{}
}

// TestCheckAlwaysReturnsEveryArticle guards the core reporting
// contract: a caller must always see the full constitutional surface,
// so an article with no facts reports NOT_EVALUABLE rather than being
// silently omitted. An omitted article reads as "fine" to a human
// skimming a report, which is exactly the failure this prevents.
func TestCheckAlwaysReturnsEveryArticle(t *testing.T) {
	results := Check(Subject{})
	if len(results) != len(Articles()) {
		t.Fatalf("expected %d results for an empty subject, got %d", len(Articles()), len(results))
	}
	for i, r := range results {
		if r.Article != i+1 {
			t.Fatalf("results are not ordered by article number: index %d has article %d", i, r.Article)
		}
		if r.Title == "" || r.Class == "" || r.Verdict == "" {
			t.Fatalf("article %d has an incompletely populated result: %+v", r.Article, r)
		}
	}
}

// TestEmptySubjectYieldsNoViolationsButManyNotEvaluable is the honesty
// property. An empty subject violates nothing -- but a caller must not
// read that as compliance, because almost everything is unjudged.
func TestEmptySubjectYieldsNoViolationsButManyNotEvaluable(t *testing.T) {
	results := Check(Subject{})
	if !NoViolations(results) {
		t.Fatalf("an empty subject should violate nothing, got %+v", Violations(results))
	}
	ne := NotEvaluables(results)
	if len(ne) < 20 {
		t.Fatalf("expected most articles to be NOT_EVALUABLE for an empty subject, got only %d", len(ne))
	}
}

// TestNotEvaluableIsTheZeroVerdict proves a check that forgets to set
// a verdict reports "cannot judge", never "passes".
func TestNotEvaluableIsTheZeroVerdict(t *testing.T) {
	var v Verdict
	if v != NotEvaluable {
		t.Fatalf("the zero Verdict must be NotEvaluable, got %v", v)
	}
	if v.String() != "NOT_EVALUABLE" {
		t.Fatalf("zero verdict renders as %q", v.String())
	}
}

// TestArticlesTableIsFrozenAgainstCallerMutation proves Articles()
// hands back a copy: a caller cannot rewrite the constitution.
func TestArticlesTableIsFrozenAgainstCallerMutation(t *testing.T) {
	got := Articles()
	got[0].Title = "MUTATED"
	got[0].Class = Declared
	fresh := Articles()
	if fresh[0].Title == "MUTATED" || fresh[0].Class == Declared {
		t.Fatal("mutating the returned slice changed the constitution itself")
	}
}

// --- Article 1: No Naked Facts ---

func TestArticle1RefusesAFindingCitingNoEvidence(t *testing.T) {
	r := verdictOf(t, Check(Subject{Finding: &FindingFacts{ID: "F-1"}}), 1)
	if r.Verdict != "VIOLATED" {
		t.Fatalf("a finding citing no evidence must violate Article 1, got %s: %s", r.Verdict, r.Detail)
	}
}

func TestArticle1RefusesEvidenceWithoutLineage(t *testing.T) {
	s := Subject{Finding: &FindingFacts{
		ID:                 "F-2",
		EvidenceRefs:       []string{"EV-1", "EV-2"},
		LineageEstablished: map[string]bool{"EV-1": true}, // EV-2 has none
	}}
	r := verdictOf(t, Check(s), 1)
	if r.Verdict != "VIOLATED" {
		t.Fatalf("expected VIOLATED, got %s", r.Verdict)
	}
	if !strings.Contains(r.Detail, "EV-2") {
		t.Fatalf("the detail must name the offending evidence, got %q", r.Detail)
	}
}

func TestArticle1AcceptsFullyGroundedFinding(t *testing.T) {
	s := Subject{Finding: &FindingFacts{
		ID:                 "F-3",
		EvidenceRefs:       []string{"EV-1"},
		LineageEstablished: map[string]bool{"EV-1": true},
	}}
	if r := verdictOf(t, Check(s), 1); r.Verdict != "SATISFIED" {
		t.Fatalf("expected SATISFIED, got %s: %s", r.Verdict, r.Detail)
	}
}

// --- Article 3 / 28: independence ---

// TestArticle3DetectsSameRootCorroboration is MIP §23's "shared
// upstream source" adversarial case. Two feeds with different IDs that
// share a root origin are ONE source, however different their formats.
func TestArticle3DetectsSameRootCorroboration(t *testing.T) {
	s := Subject{Corroboration: &CorroborationFacts{
		ClaimedIndependent: true,
		SourceRoots: map[string]string{
			"feed-alpha": "constellation-X",
			"feed-beta":  "constellation-X", // same root, different vendor label
		},
		DependencyKnown: map[string]bool{"feed-alpha|feed-beta": true},
	}}
	r := verdictOf(t, Check(s), 3)
	if r.Verdict != "VIOLATED" {
		t.Fatalf("same-root corroboration must violate Article 3, got %s", r.Verdict)
	}
	if !strings.Contains(r.Detail, "constellation-X") {
		t.Fatalf("the detail must name the shared root, got %q", r.Detail)
	}
}

func TestArticle3AcceptsGenuinelyDistinctRoots(t *testing.T) {
	s := Subject{Corroboration: &CorroborationFacts{
		ClaimedIndependent: true,
		SourceRoots:        map[string]string{"terrestrial-ais": "coastal-network", "sar": "radar-satellite"},
		DependencyKnown:    map[string]bool{"terrestrial-ais|sar": true},
	}}
	if r := verdictOf(t, Check(s), 3); r.Verdict != "SATISFIED" {
		t.Fatalf("expected SATISFIED for distinct roots, got %s: %s", r.Verdict, r.Detail)
	}
}

// TestArticle28UnknownIsNotIndependent is the article's whole point,
// and the expensive one: an unassessed dependency does not default to
// independent.
func TestArticle28UnknownIsNotIndependent(t *testing.T) {
	s := Subject{Corroboration: &CorroborationFacts{
		ClaimedIndependent: true,
		SourceRoots:        map[string]string{"a": "root-1", "b": "root-2"},
		DependencyKnown:    map[string]bool{"a|b": false}, // never assessed
	}}
	r := verdictOf(t, Check(s), 28)
	if r.Verdict != "VIOLATED" {
		t.Fatalf("an unassessed dependency must violate Article 28, got %s", r.Verdict)
	}
	if !strings.Contains(r.Detail, "UNKNOWN is not INDEPENDENT") {
		t.Fatalf("the detail must state the rule, got %q", r.Detail)
	}
}

func TestArticle28RefusesIndependenceClaimedWithNoAssessmentAtAll(t *testing.T) {
	s := Subject{Corroboration: &CorroborationFacts{
		ClaimedIndependent: true,
		SourceRoots:        map[string]string{"a": "root-1", "b": "root-2"},
	}}
	if r := verdictOf(t, Check(s), 28); r.Verdict != "VIOLATED" {
		t.Fatalf("independence with zero dependency assessment must violate, got %s", r.Verdict)
	}
}

// --- Article 4: rights before contact ---

// TestArticle4ContactWithoutRightsIsViolation covers the constitutional
// ordering: the check precedes the network call. A system that calls
// first and audits after has already violated the article.
func TestArticle4ContactWithoutRightsIsViolation(t *testing.T) {
	s := Subject{Acquisition: &AcquisitionFacts{
		SourceID: "src-1", RightsChecked: true, RightsGranted: false, ContactMade: true,
	}}
	if r := verdictOf(t, Check(s), 4); r.Verdict != "VIOLATED" {
		t.Fatalf("contact after denied rights must violate Article 4, got %s", r.Verdict)
	}
}

func TestArticle4ContactWithNoRightsCheckAtAllIsViolation(t *testing.T) {
	s := Subject{Acquisition: &AcquisitionFacts{SourceID: "src-2", ContactMade: true}}
	if r := verdictOf(t, Check(s), 4); r.Verdict != "VIOLATED" {
		t.Fatalf("contact with no rights check must violate Article 4, got %s", r.Verdict)
	}
}

func TestArticle4DeniedRightsWithNoContactSatisfies(t *testing.T) {
	s := Subject{Acquisition: &AcquisitionFacts{
		SourceID: "src-3", RightsChecked: true, RightsGranted: false, ContactMade: false,
	}}
	if r := verdictOf(t, Check(s), 4); r.Verdict != "SATISFIED" {
		t.Fatalf("denied rights with no contact is the correct behaviour, got %s: %s", r.Verdict, r.Detail)
	}
}

// --- Article 5: raw before transform ---

// TestArticle5ParsedResponseSurvivingWithoutRaw is MIP §23's "parsed
// response survives without raw" adversarial case.
func TestArticle5ParsedResponseSurvivingWithoutRaw(t *testing.T) {
	s := Subject{Acquisition: &AcquisitionFacts{
		SourceID: "src-4", RightsChecked: true, RightsGranted: true, ContactMade: true,
		RawPreserved: false, Transformed: true,
	}}
	if r := verdictOf(t, Check(s), 5); r.Verdict != "VIOLATED" {
		t.Fatalf("a transformation without preserved raw must violate Article 5, got %s", r.Verdict)
	}
}

// --- Article 8 / 27: AI authority ---

// TestArticle8ForbiddenAIActions walks every forbidden action and
// proves each one is individually caught, rather than trusting that
// one representative case implies the rest.
func TestArticle8ForbiddenAIActions(t *testing.T) {
	for _, act := range ForbiddenAIActions() {
		s := Subject{AI: &AIFacts{ModelID: "aureum-1", Actions: []string{act}}}
		r := verdictOf(t, Check(s), 8)
		if r.Verdict != "VIOLATED" {
			t.Fatalf("AI action %q must violate Article 8, got %s", act, r.Verdict)
		}
		if !strings.Contains(r.Detail, act) {
			t.Fatalf("the detail must name the forbidden action %q, got %q", act, r.Detail)
		}
	}
}

// TestArticle8AIInstructingConnectorIsForbidden is the subtle case:
// an AI that can direct acquisition has acquired evidence authority
// indirectly, by choosing what enters the fabric.
func TestArticle8AIInstructingConnectorIsForbidden(t *testing.T) {
	s := Subject{AI: &AIFacts{ModelID: "godofeys", Actions: []string{"instruct_connector"}}}
	if r := verdictOf(t, Check(s), 8); r.Verdict != "VIOLATED" {
		t.Fatalf("AI instructing a connector must violate Article 8, got %s", r.Verdict)
	}
}

func TestArticle8PermittedAIActionsSatisfy(t *testing.T) {
	s := Subject{AI: &AIFacts{
		ModelID: "aureum-1",
		Actions: []string{"summarize_evidence", "generate_hypothesis", "recommend_next_evidence"},
	}}
	if r := verdictOf(t, Check(s), 8); r.Verdict != "SATISFIED" {
		t.Fatalf("permitted AI actions must satisfy Article 8, got %s: %s", r.Verdict, r.Detail)
	}
}

func TestArticle27MaterialContributionWithoutRecordIsViolation(t *testing.T) {
	s := Subject{AI: &AIFacts{ModelID: "m-1", MaterialContribution: true, ContributionRecorded: false}}
	if r := verdictOf(t, Check(s), 27); r.Verdict != "VIOLATED" {
		t.Fatalf("unrecorded material AI contribution must violate Article 27, got %s", r.Verdict)
	}
}

// --- Article 11: dissent suppression ---

// TestArticle11DissentSuppression is MIP §23's "dissent suppression"
// adversarial case.
func TestArticle11DissentSuppression(t *testing.T) {
	s := Subject{Dissent: &DissentFacts{
		Recorded:         []string{"CRITICAL", "MINOR"},
		CarriedToFinding: []string{"MINOR"}, // the CRITICAL one vanished
	}}
	r := verdictOf(t, Check(s), 11)
	if r.Verdict != "VIOLATED" {
		t.Fatalf("a dropped CRITICAL dissent must violate Article 11, got %s", r.Verdict)
	}
	if !strings.Contains(r.Detail, "CRITICAL") {
		t.Fatalf("the detail must name the dropped severity, got %q", r.Detail)
	}
}

// TestArticle11InformationalDissentNeedNotBeCarried proves the article
// is calibrated, not absolute: only MATERIAL and CRITICAL must survive
// into the finding.
func TestArticle11InformationalDissentNeedNotBeCarried(t *testing.T) {
	s := Subject{Dissent: &DissentFacts{Recorded: []string{"INFORMATIONAL"}}}
	if r := verdictOf(t, Check(s), 11); r.Verdict != "SATISFIED" {
		t.Fatalf("informational dissent need not reach the finding, got %s: %s", r.Verdict, r.Detail)
	}
}

func TestArticle11CarriedMaterialDissentSatisfies(t *testing.T) {
	s := Subject{Dissent: &DissentFacts{
		Recorded: []string{"MATERIAL"}, CarriedToFinding: []string{"MATERIAL"},
	}}
	if r := verdictOf(t, Check(s), 11); r.Verdict != "SATISFIED" {
		t.Fatalf("expected SATISFIED, got %s: %s", r.Verdict, r.Detail)
	}
}

// --- Article 12: procedural symmetry ---

// TestArticle12AsymmetricAccess is MIP §23's "asymmetric access" case.
func TestArticle12AsymmetricAccess(t *testing.T) {
	s := Subject{Procedure: &ProcedureFacts{
		PartyPolicies: map[string]string{"claimant": "policy-v1", "respondent": "policy-v2"},
	}}
	if r := verdictOf(t, Check(s), 12); r.Verdict != "VIOLATED" {
		t.Fatalf("differing party policies with no exception must violate Article 12, got %s", r.Verdict)
	}
}

func TestArticle12AuthorizedExceptionSatisfies(t *testing.T) {
	s := Subject{Procedure: &ProcedureFacts{
		PartyPolicies:       map[string]string{"claimant": "policy-v1", "respondent": "policy-v2"},
		AuthorizedException: true,
	}}
	if r := verdictOf(t, Check(s), 12); r.Verdict != "SATISFIED" {
		t.Fatalf("a recorded authorized exception makes asymmetry lawful, got %s", r.Verdict)
	}
}

// --- Article 19 / 25: privilege ---

// TestArticle19VeriqoMayNotDeterminePrivilege proves the platform
// enforces but does not decide.
func TestArticle19VeriqoMayNotDeterminePrivilege(t *testing.T) {
	for _, who := range []string{"VERIQO", "veriqo", "system"} {
		s := Subject{Privilege: &PrivilegeFacts{Status: "CONFIRMED", DeterminedBy: who}}
		if r := verdictOf(t, Check(s), 19); r.Verdict != "VIOLATED" {
			t.Fatalf("determination by %q must violate Article 19, got %s", who, r.Verdict)
		}
	}
}

func TestArticle19ExternalAuthoritySatisfies(t *testing.T) {
	s := Subject{Privilege: &PrivilegeFacts{Status: "CONFIRMED", DeterminedBy: "external-counsel-LLP"}}
	if r := verdictOf(t, Check(s), 19); r.Verdict != "SATISFIED" {
		t.Fatalf("expected SATISFIED, got %s: %s", r.Verdict, r.Detail)
	}
}

func TestArticle25SilentPrivilegeChangeIsViolation(t *testing.T) {
	s := Subject{Privilege: &PrivilegeFacts{
		Status: "WAIVED", DeterminedBy: "counsel", StatusChanged: true, EventEmitted: false,
	}}
	if r := verdictOf(t, Check(s), 25); r.Verdict != "VIOLATED" {
		t.Fatalf("a silent privilege change must violate Article 25, got %s", r.Verdict)
	}
}

// --- Article 20 / 21 / 24: disclosure ---

// TestArticle20AccessDoesNotImplyTraining is the commercially
// important case: viewing rights do not confer training rights.
func TestArticle20AccessDoesNotImplyTraining(t *testing.T) {
	s := Subject{Disclosure: &DisclosureFacts{
		GrantedRights:   []string{"VIEW"},
		ExercisedRights: []string{"VIEW", "TRAIN"},
	}}
	r := verdictOf(t, Check(s), 20)
	if r.Verdict != "VIOLATED" {
		t.Fatalf("exercising TRAIN on a VIEW grant must violate Article 20, got %s", r.Verdict)
	}
	if !strings.Contains(r.Detail, "TRAIN") {
		t.Fatalf("the detail must name the exceeded right, got %q", r.Detail)
	}
}

// TestArticle21RedactionIsNotAIEligibility proves a redacted
// derivative cleared for disclosure is not thereby cleared for a model.
func TestArticle21RedactionIsNotAIEligibility(t *testing.T) {
	s := Subject{
		Redaction:  &RedactionFacts{DerivativeCreated: true, RecoveryTestsRun: true},
		Disclosure: &DisclosureFacts{GrantedRights: []string{"VIEW"}, ExercisedRights: []string{"AI_PROCESS"}},
	}
	if r := verdictOf(t, Check(s), 21); r.Verdict != "VIOLATED" {
		t.Fatalf("AI_PROCESS on a redacted derivative without its own grant must violate Article 21, got %s", r.Verdict)
	}
}

func TestArticle24SilentDisclosureIsViolation(t *testing.T) {
	s := Subject{Disclosure: &DisclosureFacts{Occurred: true, EventEmitted: false}}
	if r := verdictOf(t, Check(s), 24); r.Verdict != "VIOLATED" {
		t.Fatalf("a disclosure with no ledger event must violate Article 24, got %s", r.Verdict)
	}
}

// --- Article 17 / 18: redaction ---

func TestArticle17OriginalMustNotChange(t *testing.T) {
	s := Subject{Redaction: &RedactionFacts{
		OriginalHashBefore: "aaa", OriginalHashAfter: "bbb", DerivativeCreated: true,
	}}
	if r := verdictOf(t, Check(s), 17); r.Verdict != "VIOLATED" {
		t.Fatalf("a changed original hash must violate Article 17, got %s", r.Verdict)
	}
}

// TestArticle18VisualOnlyRedactionIsViolation is MIP §23's "redaction
// recovery" adversarial case.
func TestArticle18VisualOnlyRedactionIsViolation(t *testing.T) {
	s := Subject{Redaction: &RedactionFacts{
		OriginalHashBefore: "aaa", OriginalHashAfter: "aaa", DerivativeCreated: true,
		RecoveryTestsRun: true, ContentRecoverable: true,
	}}
	if r := verdictOf(t, Check(s), 18); r.Verdict != "VIOLATED" {
		t.Fatalf("recoverable content must violate Article 18, got %s", r.Verdict)
	}
}

// TestArticle18UntestedRedactionIsNotEvaluable proves the honest
// middle: without recovery tests we cannot say the redaction is sound,
// and we do not pretend otherwise.
func TestArticle18UntestedRedactionIsNotEvaluable(t *testing.T) {
	s := Subject{Redaction: &RedactionFacts{
		OriginalHashBefore: "aaa", OriginalHashAfter: "aaa",
		DerivativeCreated: true, RecoveryTestsRun: false,
	}}
	if r := verdictOf(t, Check(s), 18); r.Verdict != "NOT_EVALUABLE" {
		t.Fatalf("untested redaction must be NOT_EVALUABLE, got %s: %s", r.Verdict, r.Detail)
	}
}

// --- Article 29: unqualified absence ---

// TestArticle29UnqualifiedAbsence proves OBSERVED_ABSENT is refused
// unless every one of the nine observability conditions holds. This is
// the difference between "the vessel did not transmit" and "we were
// not receiving."
func TestArticle29UnqualifiedAbsence(t *testing.T) {
	partial := map[string]bool{}
	for i, c := range ObservabilityGateConditions() {
		if i < 5 { // only five of nine met
			partial[c] = true
		}
	}
	s := Subject{Absence: &AbsenceFacts{ReportedState: "OBSERVED_ABSENT", GateConditions: partial}}
	r := verdictOf(t, Check(s), 29)
	if r.Verdict != "VIOLATED" {
		t.Fatalf("OBSERVED_ABSENT with unmet conditions must violate Article 29, got %s", r.Verdict)
	}
	if !strings.Contains(r.Detail, "4 unmet") {
		t.Fatalf("the detail must count the unmet conditions, got %q", r.Detail)
	}
}

func TestArticle29FullyGatedAbsenceSatisfies(t *testing.T) {
	all := map[string]bool{}
	for _, c := range ObservabilityGateConditions() {
		all[c] = true
	}
	s := Subject{Absence: &AbsenceFacts{ReportedState: "OBSERVED_ABSENT", GateConditions: all}}
	if r := verdictOf(t, Check(s), 29); r.Verdict != "SATISFIED" {
		t.Fatalf("expected SATISFIED, got %s: %s", r.Verdict, r.Detail)
	}
}

// TestArticle29WeakerAbsenceStatesNeedNoGate proves the six
// non-evidential states are permitted freely -- they carry no weight,
// so they need no gate.
func TestArticle29WeakerAbsenceStatesNeedNoGate(t *testing.T) {
	for _, st := range []string{
		"EXPECTED_BUT_NOT_TESTED", "NOT_OBSERVABLE", "NOT_COLLECTABLE",
		"PARTIAL_COVERAGE", "SOURCE_UNAVAILABLE", "INCONCLUSIVE",
	} {
		s := Subject{Absence: &AbsenceFacts{ReportedState: st}}
		if r := verdictOf(t, Check(s), 29); r.Verdict != "SATISFIED" {
			t.Fatalf("state %q should need no gate, got %s: %s", st, r.Verdict, r.Detail)
		}
	}
}

// --- Article 7 / 26: policy ---

// TestArticle7PolicyRetroactivity is MIP §23's "policy retroactivity"
// adversarial case.
func TestArticle7PolicyRetroactivity(t *testing.T) {
	s := Subject{Policy: &PolicyFacts{
		CasePolicyVersion: "policy-2025-01", EvaluatedPolicyVersion: "policy-2026-09",
	}}
	if r := verdictOf(t, Check(s), 7); r.Verdict != "VIOLATED" {
		t.Fatalf("evaluating a historical case under today's policy must violate Article 7, got %s", r.Verdict)
	}
}

func TestArticle26SilentRetroactivityIsViolation(t *testing.T) {
	s := Subject{Policy: &PolicyFacts{
		CasePolicyVersion: "p1", EvaluatedPolicyVersion: "p1",
		RetroactiveChange: true, ChangeRecorded: false,
	}}
	if r := verdictOf(t, Check(s), 26); r.Verdict != "VIOLATED" {
		t.Fatalf("unrecorded retroactive change must violate Article 26, got %s", r.Verdict)
	}
}

// --- Declared-class articles ---

// TestDeclaredArticlesAreNotEvaluableWithoutAttestation is the honesty
// property for commitments software cannot discharge. Article 15 (no
// outcome-contingent neutrality) is a commercial arrangement; no unit
// test settles it.
func TestDeclaredArticlesAreNotEvaluableWithoutAttestation(t *testing.T) {
	results := Check(Subject{})
	for _, n := range []int{15, 16, 30} {
		r := verdictOf(t, results, n)
		if r.Verdict != "NOT_EVALUABLE" {
			t.Fatalf("declared article %d must be NOT_EVALUABLE without attestation, got %s", n, r.Verdict)
		}
		if r.Class != "DECLARED" {
			t.Fatalf("article %d should be class DECLARED, got %s", n, r.Class)
		}
		if !strings.Contains(r.Detail, "external attestation") {
			t.Fatalf("article %d detail should name the external requirement, got %q", n, r.Detail)
		}
	}
}

func TestDeclaredArticlesAcceptExternalAttestation(t *testing.T) {
	s := Subject{Attestations: map[int]string{15: "board resolution 2026-03, fee structure independent of outcome"}}
	r := verdictOf(t, Check(s), 15)
	if r.Verdict != "SATISFIED" {
		t.Fatalf("an attested declared article should be SATISFIED, got %s", r.Verdict)
	}
	if !strings.Contains(r.Detail, "board resolution") {
		t.Fatalf("the attestation text should be carried into the detail, got %q", r.Detail)
	}
}

func TestBlankAttestationDoesNotSatisfy(t *testing.T) {
	s := Subject{Attestations: map[int]string{16: "   "}}
	if r := verdictOf(t, Check(s), 16); r.Verdict != "NOT_EVALUABLE" {
		t.Fatalf("a whitespace attestation must not satisfy, got %s", r.Verdict)
	}
}

// --- Aggregate helpers ---

func TestNoViolationsDoesNotImplyFullCompliance(t *testing.T) {
	results := Check(Subject{})
	if !NoViolations(results) {
		t.Fatal("empty subject should have no violations")
	}
	if len(NotEvaluables(results)) == 0 {
		t.Fatal("empty subject should have unjudged articles -- otherwise NoViolations would be misleading")
	}
}

func TestViolationsAreCollectedAcrossArticles(t *testing.T) {
	s := Subject{
		Finding:    &FindingFacts{ID: "F"},                                      // art 1
		AI:         &AIFacts{ModelID: "m", Actions: []string{"alter_evidence"}}, // art 8
		Disclosure: &DisclosureFacts{Occurred: true, EventEmitted: false},       // art 24
		Dissent:    &DissentFacts{Recorded: []string{"CRITICAL"}},               // art 11
	}
	v := Violations(Check(s))
	if len(v) < 4 {
		t.Fatalf("expected at least 4 violations across articles, got %d: %+v", len(v), v)
	}
	seen := map[int]bool{}
	for _, r := range v {
		seen[r.Article] = true
	}
	for _, want := range []int{1, 8, 11, 24} {
		if !seen[want] {
			t.Fatalf("expected article %d among violations, got %+v", want, seen)
		}
	}
}
