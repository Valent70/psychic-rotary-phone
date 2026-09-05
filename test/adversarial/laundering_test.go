package adversarial

import (
	"strings"
	"testing"
	"time"

	"veriqo/pkg/ai"
	"veriqo/pkg/authority"
	"veriqo/pkg/canonical/jcs"
	"veriqo/pkg/contract"
	"veriqo/pkg/identity"
	"veriqo/pkg/qualification/independence"
)

// Laundering is the family of attacks that do not break anything --
// they route a weak thing through enough machinery that it comes out
// looking strong. A model output presented as a qualified fact; one
// source counted twice by passing through two vendors; an author who
// approves their own work by holding two roles.

func human(id string) identity.Principal {
	return identity.Principal{ID: contract.ID(id), Kind: identity.Human, TenantID: "t-acme",
		NotBefore: at.Add(-time.Hour), NotAfter: at.Add(time.Hour)}
}

func machine(id string) identity.Principal {
	return identity.Principal{ID: contract.ID(id), Kind: identity.Agent, TenantID: "t-acme",
		NotBefore: at.Add(-time.Hour), NotAfter: at.Add(time.Hour)}
}

func draft() ai.Artefact {
	return ai.Artefact{
		ID: "artefact:a1", TenantID: "t-acme", Act: ai.Propose,
		Producer:    ai.Producer{PrincipalID: "agent:extractor-1", ModelID: "m", ModelVersion: "1", Automated: true},
		Level:       ai.Draft,
		ContentHash: jcs.HashBytes([]byte("the vessel discharged 58,200 MT")),
	}
}

// TestAModelCannotPromoteItsOwnOutput. Self-review is the cheapest
// way to manufacture a qualified fact, and it is refused by identity,
// not by convention.
func TestAModelCannotPromoteItsOwnOutput(t *testing.T) {
	a := draft()
	_, err := ai.Promote(a, ai.Assisted, machine("agent:extractor-1"), at, "looks right",
		&ai.AutomatedPolicy{Name: "p", RiskClass: "low", MaxLevel: ai.Assisted,
			Version: contract.Version{Component: "ai", Revision: 1}})
	if err == nil {
		t.Fatal("a producer promoted its own output")
	}
}

// TestTheLadderCannotBeJumped. DRAFT to QUALIFIED in one step skips
// every check the intermediate levels exist to perform, which is the
// only reason the intermediate levels exist.
func TestTheLadderCannotBeJumped(t *testing.T) {
	a := draft()
	if _, err := ai.Promote(a, ai.Qualified, human("human:reviewer-1"), at,
		"reviewed and accepted", nil); err == nil {
		t.Fatal("the ladder was jumped from DRAFT to QUALIFIED")
	}
	if _, err := ai.Promote(a, ai.ReviewRequired, human("human:reviewer-1"), at,
		"reviewed", nil); err == nil {
		t.Fatal("a two-level jump was accepted")
	}
}

// TestQualifiedIsUnreachableByAutomationUnderAnyPolicy. This is Law 7
// at its narrowest: not "automation needs a good policy" but "no
// policy can express this".
func TestQualifiedIsUnreachableByAutomationUnderAnyPolicy(t *testing.T) {
	p := &ai.AutomatedPolicy{Name: "permissive", RiskClass: "everything",
		MaxLevel: ai.Qualified, Version: contract.Version{Component: "ai", Revision: 99}}
	if err := p.Validate(); err == nil {
		t.Fatal("a policy permitting automated QUALIFIED validated")
	}

	// And the level cannot be reached by walking the ladder with an
	// automated promoter at every rung either.
	a := draft()
	ok := &ai.AutomatedPolicy{Name: "low-risk", RiskClass: "extraction",
		MaxLevel: ai.QualificationEligible, Version: contract.Version{Component: "ai", Revision: 1}}
	for _, to := range []ai.Level{ai.Assisted, ai.ReviewRequired, ai.QualificationEligible} {
		next, err := ai.Promote(a, to, machine("agent:promoter-1"), at, "automated", ok)
		if err != nil {
			// Refusing earlier is stricter, not weaker.
			return
		}
		a = next
	}
	if _, err := ai.Promote(a, ai.Qualified, machine("agent:promoter-1"), at,
		"automated", ok); err == nil {
		t.Fatal("automation reached QUALIFIED by climbing")
	}
}

// TestAForbiddenActCannotBeRecordedAtAll. The defence is not that a
// QUALIFY_FACT artefact stays at DRAFT -- it is that such an artefact
// is malformed and cannot exist.
func TestAForbiddenActCannotBeRecordedAtAll(t *testing.T) {
	for _, act := range ai.ForbiddenActs() {
		a := draft()
		a.Act = act
		if err := a.Validate(); err == nil {
			t.Fatalf("an artefact recording %s validated", act)
		}
		if err := ai.CheckAct(machine("agent:extractor-1"), act); err == nil {
			t.Fatalf("an automated principal was cleared to perform %s", act)
		}
	}
	for _, act := range ai.PermittedActs() {
		if err := ai.CheckAct(machine("agent:extractor-1"), act); err != nil {
			t.Fatalf("a permitted act was refused: %s: %v", act, err)
		}
	}
}

// TestAHistoryThatDoesNotReachTheLevelIsRefused. The attack is to
// hand-write an artefact at QUALIFIED with an empty history, skipping
// Promote entirely.
func TestAHistoryThatDoesNotReachTheLevelIsRefused(t *testing.T) {
	a := draft()
	a.Level = ai.Qualified
	if err := a.Validate(); err == nil {
		t.Fatal("an artefact claimed QUALIFIED with no history")
	}
	a.History = []ai.Promotion{{From: ai.Draft, To: ai.Qualified,
		By: "human:reviewer-1", At: at, Reason: "accepted"}}
	if err := a.Validate(); err != nil {
		t.Fatalf("a self-consistent history was refused: %v", err)
	}
	// ... but a history with a gap is not self-consistent.
	a.History = []ai.Promotion{
		{From: ai.Draft, To: ai.Assisted, By: "human:reviewer-1", At: at, Reason: "x"},
		{From: ai.ReviewRequired, To: ai.Qualified, By: "human:reviewer-1", At: at, Reason: "y"},
	}
	if err := a.Validate(); err == nil {
		t.Fatal("a history with a gap validated")
	}
}

// TestAnAuthorCannotApproveTheirOwnWork closes FC-006 at the
// separation-of-duties layer.
func TestAnAuthorCannotApproveTheirOwnWork(t *testing.T) {
	p := human("human:analyst-1")
	if err := authority.CheckSeparation(p, p); err == nil {
		t.Fatal("a principal approved their own proposal")
	}
	if err := authority.CheckSeparation(p, human("human:reviewer-1")); err != nil {
		t.Fatalf("two distinct people were refused: %v", err)
	}
}

// TestAnInternalPartyCannotBeTheIndependentAssessor. The other half
// of FC-006: the role exists to be held by somebody outside, and a
// grant that names an internal principal must not satisfy it.
func TestAnInternalPartyCannotBeTheIndependentAssessor(t *testing.T) {
	internal := authority.Grant{Principal: "human:qa-lead", Role: authority.IndependentAssessor,
		TenantID: "t-acme"}
	if err := authority.CanQualifyExternally(internal); err == nil {
		t.Fatal("an internal principal satisfied the external assessor requirement")
	}
	// Self-attestation is the same attack wearing the external label.
	self := authority.Grant{Principal: "external:auditor-1", Role: authority.IndependentAssessor,
		TenantID: "t-acme", External: true, AttestedBy: "external:auditor-1"}
	if err := authority.CanQualifyExternally(self); err == nil {
		t.Fatal("an external party attested to its own independence")
	}
}

// TestOneSourceRoutedThroughTwoVendorsIsNotTwoSources is Law 6, and
// it is the corroboration attack that costs nothing to mount: buy the
// same feed from two resellers and call the agreement confirmation.
func TestOneSourceRoutedThroughTwoVendorsIsNotTwoSources(t *testing.T) {
	a := independence.Source{ID: "src:vendor-a", Attributes: map[independence.Dimension]string{
		independence.Producer:       "registry-1",
		independence.Sensor:         "registry-1",
		independence.Ownership:      "holding-co",
		independence.Acquisition:    "api-pull",
		independence.Transformation: "registry-extract",
		independence.Temporal:       "2026-03-01",
	}}
	b := independence.Source{ID: "src:vendor-b", Attributes: map[independence.Dimension]string{
		independence.Producer:       "registry-1",
		independence.Sensor:         "registry-1",
		independence.Ownership:      "holding-co",
		independence.Acquisition:    "file-drop",
		independence.Transformation: "vendor-b-normalise",
		independence.Temporal:       "2026-03-02",
	}}

	r, err := independence.Assess(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict != independence.Dependent {
		t.Fatalf("two resellers of one registry were called %s", r.Verdict)
	}
	if r.Verdict.SatisfiesIndependenceRequirement() {
		t.Fatal("the verdict satisfied an independence requirement")
	}
	if _, err := independence.RequireIndependent(a, b); err == nil {
		t.Fatal("RequireIndependent accepted a dependent pair")
	}
	n, unknown, err := independence.EffectiveIndependentCount([]independence.Source{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("effective independent count is %d for one underlying source", n)
	}
	if len(unknown) != 0 {
		t.Fatalf("a fully assessed pair was reported unassessed: %v", unknown)
	}
	// The statement a reader sees must say so in words, not leave the
	// number to be interpreted.
	st, err := independence.Statement([]independence.Source{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st, "1") {
		t.Fatalf("the statement does not carry the count: %q", st)
	}
}

// TestAPartiallyIndependentPairDoesNotSatisfyARequirement. The
// interesting middle case: genuinely different producers using the
// same method. The correlation is real, the verdict says so, and a
// rule that needs independence still does not get it.
func TestAPartiallyIndependentPairDoesNotSatisfyARequirement(t *testing.T) {
	a := independence.Source{ID: "src:a", Attributes: map[independence.Dimension]string{
		independence.Producer: "inspector-a", independence.Sensor: "gauge-a",
		independence.Ownership: "owner-a", independence.Acquisition: "shore-tank",
		independence.Transformation: "api-mpms-12", independence.Temporal: "2026-03-01",
	}}
	b := independence.Source{ID: "src:b", Attributes: map[independence.Dimension]string{
		independence.Producer: "inspector-b", independence.Sensor: "gauge-b",
		independence.Ownership: "owner-b", independence.Acquisition: "shore-tank",
		independence.Transformation: "api-mpms-12", independence.Temporal: "2026-03-02",
	}}
	r, err := independence.Assess(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict != independence.PartiallyIndependent {
		t.Fatalf("verdict = %s", r.Verdict)
	}
	if r.Verdict.SatisfiesIndependenceRequirement() {
		t.Fatal("PARTIALLY_INDEPENDENT satisfied a requirement for independence")
	}
	if strings.TrimSpace(r.Explanation) == "" {
		t.Fatal("a partial verdict carries no explanation for a reader")
	}
}

// TestAnUnassessedPairNeverCountsAsCorroboration. Article 28: not
// knowing whether two sources are independent is not the same as
// knowing they are, and the difference is the entire value of the
// count.
func TestAnUnassessedPairNeverCountsAsCorroboration(t *testing.T) {
	full := map[independence.Dimension]string{
		independence.Producer: "p", independence.Sensor: "s", independence.Ownership: "o",
		independence.Acquisition: "acq", independence.Transformation: "tr",
		independence.Temporal: "2026-03-01",
	}
	a := independence.Source{ID: "src:a", Attributes: copyAttrs(full, "a")}
	b := independence.Source{ID: "src:b", Attributes: copyAttrs(full, "b")}

	n, unknown, err := independence.EffectiveIndependentCount([]independence.Source{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || len(unknown) != 0 {
		t.Fatalf("an assessed independent pair counted %d (%v)", n, unknown)
	}

	// The same two sources with the disqualifying dimensions blank --
	// which is what an unassessed relationship actually looks like.
	bare := independence.Source{ID: "src:c", Attributes: map[independence.Dimension]string{
		independence.Acquisition: "acq-c",
	}}
	n2, unknown2, err := independence.EffectiveIndependentCount(
		[]independence.Source{a, bare})
	if err != nil {
		t.Fatal(err)
	}
	if n2 > 1 {
		t.Fatalf("an unassessed pair counted as %d independent sources", n2)
	}
	if len(unknown2) == 0 {
		t.Fatal("the unassessed pair was not reported")
	}
	if !strings.Contains(unknown2[0].String(), "src:a") {
		t.Fatalf("the report does not name the pair: %v", unknown2)
	}
	// The distinction the list exists to preserve: a count of 1 from
	// dependence and a count of 1 from ignorance are different
	// situations, and only one is fixed by buying more data.
	st, err := independence.Statement([]independence.Source{a, bare})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(st), "unassessed") &&
		!strings.Contains(strings.ToLower(st), "not been assessed") {
		t.Fatalf("the statement does not distinguish ignorance from dependence: %q", st)
	}
}

func copyAttrs(base map[independence.Dimension]string, suffix string) map[independence.Dimension]string {
	out := map[independence.Dimension]string{}
	for k, v := range base {
		out[k] = v + "-" + suffix
	}
	return out
}
