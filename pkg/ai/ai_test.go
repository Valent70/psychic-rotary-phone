package ai

import (
	"errors"
	"strings"
	"testing"
	"time"

	"veriqo/pkg/contract"
	"veriqo/pkg/identity"
)

var now = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

func human(id string) identity.Principal {
	return identity.Principal{ID: contract.ID(id), Kind: identity.Human, TenantID: "t-acme"}
}

func agent(id string) identity.Principal {
	return identity.Principal{ID: contract.ID(id), Kind: identity.Agent, TenantID: "t-acme"}
}

func artefact() Artefact {
	return Artefact{
		ID: "ai:a1", TenantID: "t-acme", Act: Extract,
		Producer: Producer{PrincipalID: "agent:extractor", ModelID: "extract-v3",
			ModelVersion: "2026-08-01", Automated: true},
		Level: Draft, ContentHash: "sha256:abc",
	}
}

func policy() *AutomatedPolicy {
	return &AutomatedPolicy{Name: "low-risk-extraction", RiskClass: "FIELD_EXTRACTION",
		MaxLevel: ReviewRequired,
		Version:  contract.Version{Component: "ai-automation", Revision: 1}}
}

// TestTheForbiddenActsAreRefusedForAutomationAndAllowedForPeople.
func TestTheForbiddenActsAreRefusedForAutomationAndAllowedForPeople(t *testing.T) {
	a := agent("agent:x")
	for _, act := range ForbiddenActs() {
		if err := CheckAct(a, act); !errors.Is(err, ErrForbiddenAct) {
			t.Errorf("an agent was permitted to %s: %v", act, err)
		}
	}
	for _, act := range PermittedActs() {
		if err := CheckAct(a, act); err != nil {
			t.Errorf("an agent was refused %s: %v", act, err)
		}
	}
	// A person may do any of them: the law is about automation, not
	// about the acts being forbidden to everybody.
	h := human("human:reviewer-1")
	for _, act := range append(PermittedActs(), ForbiddenActs()...) {
		if err := CheckAct(h, act); err != nil {
			t.Errorf("a person was refused %s: %v", act, err)
		}
	}
}

// TestTheRefusalNamesWhatIsPermitted, so a caller is not left guessing.
func TestTheRefusalNamesWhatIsPermitted(t *testing.T) {
	err := CheckAct(agent("agent:x"), QualifyFact)
	for _, want := range []string{"EXTRACT", "PROPOSE", "SUMMARIZE"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %s as permitted: %v", want, err)
		}
	}
}

// TestAnArtefactRisesOneLevelAtATime.
//
// A jump from DRAFT to QUALIFIED skips every check the intermediate
// levels exist to perform.
func TestAnArtefactRisesOneLevelAtATime(t *testing.T) {
	a := artefact()
	_, err := Promote(a, Qualified, human("human:reviewer-1"), now, "looks fine", nil)
	if !errors.Is(err, ErrSkippedLevel) {
		t.Fatalf("DRAFT was promoted straight to QUALIFIED: %v", err)
	}
	// One at a time works.
	a, err = Promote(a, Assisted, human("human:analyst-1"), now, "reviewed the extraction", nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.Level != Assisted {
		t.Fatalf("level = %s", a.Level)
	}
	if err := a.Validate(); err != nil {
		t.Fatalf("the promoted artefact is invalid: %v", err)
	}
}

// TestAProducerCannotPromoteItsOwnOutput, directly or through a
// delegation.
func TestAProducerCannotPromoteItsOwnOutput(t *testing.T) {
	a := artefact()
	self := agent("agent:extractor")
	if _, err := Promote(a, Assisted, self, now, "I checked it", policy()); !errors.Is(err, ErrSelfPromotion) {
		t.Fatalf("a producer promoted its own output: %v", err)
	}
	// And through a delegation: an agent launched by the producer.
	producer := contract.ID("agent:extractor")
	delegated := agent("agent:reviewer")
	delegated.OnBehalfOf = &producer
	if _, err := Promote(a, Assisted, delegated, now, "checked", policy()); !errors.Is(err, ErrSelfPromotion) {
		t.Fatalf("a delegate of the producer promoted its output: %v", err)
	}
}

// TestAutomationCannotReachQualified, under any policy.
func TestAutomationCannotReachQualified(t *testing.T) {
	a := artefact()
	// Walk it up to QUALIFICATION_ELIGIBLE with people.
	var err error
	for _, step := range []Level{Assisted, ReviewRequired, QualificationEligible} {
		a, err = Promote(a, step, human("human:reviewer-1"), now, "reviewed", nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	// Now an agent tries the last step, with a policy that claims to
	// permit it.
	permissive := policy()
	permissive.MaxLevel = Qualified
	if err := permissive.Validate(); err == nil {
		t.Fatal("a policy claiming to permit automated QUALIFIED was accepted")
	}
	// And even bypassing the policy check, the promotion is refused.
	permissive.MaxLevel = QualificationEligible
	if _, err := Promote(a, Qualified, agent("agent:qa"), now, "tests pass", permissive); !errors.Is(err, ErrAutomatedPromotion) {
		t.Fatalf("AN AGENT REACHED QUALIFIED: %v", err)
	}
	// A person can.
	final, err := Promote(a, Qualified, human("human:reviewer-2"), now, "qualification complete", nil)
	if err != nil {
		t.Fatalf("a person could not perform the final promotion: %v", err)
	}
	if !final.Level.MayFoundAConclusion() {
		t.Fatal("QUALIFIED does not permit founding a conclusion")
	}
}

// TestAutomatedPromotionNeedsANamedPolicyAndRiskClass.
func TestAutomatedPromotionNeedsANamedPolicyAndRiskClass(t *testing.T) {
	a := artefact()
	by := agent("agent:qa")
	if _, err := Promote(a, Assisted, by, now, "automated check", nil); !errors.Is(err, ErrAutomatedPromotion) {
		t.Fatalf("an automated promotion with no policy was accepted: %v", err)
	}
	bad := policy()
	bad.RiskClass = ""
	if _, err := Promote(a, Assisted, by, now, "automated check", bad); !errors.Is(err, ErrNoPolicy) {
		t.Fatalf("a policy with no risk class was accepted: %v", err)
	}
	bad = policy()
	bad.Version = contract.Version{}
	if _, err := Promote(a, Assisted, by, now, "automated check", bad); !errors.Is(err, ErrNoPolicy) {
		t.Fatalf("an unversioned policy was accepted: %v", err)
	}
}

// TestAnAutomatedPromotionIsRecordedAsAutomated, so a reader can tell
// which steps nobody looked at.
func TestAnAutomatedPromotionIsRecordedAsAutomated(t *testing.T) {
	a := artefact()
	out, err := Promote(a, Assisted, agent("agent:qa"), now, "schema validation passed", policy())
	if err != nil {
		t.Fatal(err)
	}
	auto := out.AutomatedPromotions()
	if len(auto) != 1 {
		t.Fatalf("AutomatedPromotions = %v", auto)
	}
	if auto[0].PolicyName != "low-risk-extraction" || auto[0].RiskClass != "FIELD_EXTRACTION" {
		t.Fatalf("the promotion does not record its policy: %+v", auto[0])
	}
	p := out.Provenance()
	if !strings.Contains(p, "[AUTOMATED under low-risk-extraction") {
		t.Fatalf("the provenance does not mark the automated step:\n%s", p)
	}
	if !strings.Contains(p, "no person examined this material at those steps") {
		t.Fatalf("the provenance does not state the consequence:\n%s", p)
	}
}

// TestAPolicyBoundedBelowTheRequestedLevelIsRefused.
func TestAPolicyBoundedBelowTheRequestedLevelIsRefused(t *testing.T) {
	a := artefact()
	p := policy()
	p.MaxLevel = Assisted
	a, err := Promote(a, Assisted, agent("agent:qa"), now, "ok", p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Promote(a, ReviewRequired, agent("agent:qa2"), now, "ok", p); !errors.Is(err, ErrAutomatedPromotion) {
		t.Fatalf("an agent exceeded its policy's ceiling: %v", err)
	}
}

// TestTheZeroLevelIsDraft. An artefact whose level nobody set must not
// read as reviewed.
func TestTheZeroLevelIsDraft(t *testing.T) {
	var l Level
	if l != Draft {
		t.Fatalf("the zero level is %s", l)
	}
	if l.MayFoundAConclusion() {
		t.Fatal("the zero level may found a conclusion")
	}
	if l.RequiresHuman() {
		t.Fatal("DRAFT is classified as requiring a human")
	}
	for _, above := range []Level{ReviewRequired, QualificationEligible, Qualified} {
		if !above.RequiresHuman() {
			t.Errorf("%s does not require a human", above)
		}
	}
}

// TestTheHistoryMustLeadToTheCurrentLevel. An artefact whose level and
// history disagree has had one of them edited.
func TestTheHistoryMustLeadToTheCurrentLevel(t *testing.T) {
	a := artefact()
	a.Level = Qualified // claimed, with no history
	if err := a.Validate(); err == nil {
		t.Fatal("an artefact claiming QUALIFIED with no history was accepted")
	}
	a = artefact()
	a.History = []Promotion{{From: ReviewRequired, To: QualificationEligible,
		By: "human:x", At: now, Reason: "r"}}
	a.Level = QualificationEligible
	if err := a.Validate(); err == nil {
		t.Fatal("a history starting above DRAFT was accepted")
	}
}

// TestDemotionNeedsNoSeparationOrPolicy. Concluding less about
// material is always safe.
func TestDemotionNeedsNoSeparationOrPolicy(t *testing.T) {
	a := artefact()
	a, err := Promote(a, Assisted, human("human:analyst-1"), now, "reviewed", nil)
	if err != nil {
		t.Fatal(err)
	}
	// The producer itself may demote.
	out, err := Demote(a, Draft, "agent:extractor", now, "the source document was withdrawn")
	if err != nil {
		t.Fatal(err)
	}
	if out.Level != Draft {
		t.Fatalf("level = %s", out.Level)
	}
	if _, err := Demote(out, Assisted, "agent:extractor", now, "r"); err == nil {
		t.Fatal("a promotion was performed through Demote")
	}
}

// TestAPromotionMustStateWhyAndWhen.
func TestAPromotionMustStateWhyAndWhen(t *testing.T) {
	a := artefact()
	if _, err := Promote(a, Assisted, human("human:x"), now, "", nil); err == nil {
		t.Fatal("an unreasoned promotion was accepted")
	}
	if _, err := Promote(a, Assisted, human("human:x"), time.Time{}, "r", nil); err == nil {
		t.Fatal("a promotion with no instant was accepted")
	}
}

// TestCrossTenantPromotionIsRefused.
func TestCrossTenantPromotionIsRefused(t *testing.T) {
	a := artefact()
	other := human("human:x")
	other.TenantID = "t-beta"
	if _, err := Promote(a, Assisted, other, now, "r", nil); !errors.Is(err, contract.ErrCrossTenant) {
		t.Fatalf("a cross-tenant promotion was accepted: %v", err)
	}
}

// TestAModelMustBeVersioned.
func TestAModelMustBeVersioned(t *testing.T) {
	a := artefact()
	a.Producer.ModelVersion = ""
	if err := a.Validate(); err == nil {
		t.Fatal("a producer naming a model with no version was accepted")
	}
}

// TestAnArtefactRecordingAForbiddenActIsRefused.
func TestAnArtefactRecordingAForbiddenActIsRefused(t *testing.T) {
	a := artefact()
	a.Act = DeclareLiability
	if err := a.Validate(); err == nil {
		t.Fatal("an artefact recording a forbidden act was accepted")
	}
}
