package adversarial

import (
	"strings"
	"testing"

	"veriqo/pkg/assurance"
	"veriqo/pkg/casefabric"
	"veriqo/pkg/caseproofgraph"
	"veriqo/pkg/evidence/redaction"
	"veriqo/pkg/fref"
	"veriqo/pkg/proof"
)

// Adversarial cases against the constitutional sequencing law.
//
// Each is an attempt by someone who wants a conclusion sooner than the
// law allows. The question is never "does the lawful path work" — the
// integration proof answers that — but "what does a deadline do to this
// system, and what stops it?"

// TestResolveFirstProveLater is the shortcut a deadline creates.
func TestResolveFirstProveLater(t *testing.T) {
	c := scopedCase(t)
	mustNil(t, c.AddEvidence([]casefabric.EvidenceRef{
		{EvidenceID: "E-1", EvidenceVersionID: "v1", SHA256: "abc"}}, "a", 3))
	mustNil(t, c.AddHypothesis(casefabric.Hypothesis{ID: "H-1", Description: "rival", Tested: true}, "a", 4))
	mustNil(t, c.RegisterClaim(casefabric.Claim{ID: "CL-1", Material: true,
		Proposition: proof.Proposition{ID: "P-1", Statement: "contaminated before loading"}}, "a", 5))
	mustNil(t, c.BeginQualification("a", 6))

	if _, err := c.Resolve(casefabric.ResolutionGate{}, "evidence_package_delivered", "", "a", 7); err == nil {
		t.Fatal("a case must not resolve before anything is proved")
	}
}

// TestReverseAsAnAfterthought is the reviewer's exact finding, attempted
// deliberately: resolve, then run the reverse direction to tidy up.
func TestReverseAsAnAfterthought(t *testing.T) {
	s, err := fref.NewSequence("CASE-1")
	if err != nil {
		t.Fatalf("NewSequence: %v", err)
	}
	for _, step := range []fref.Step{
		fref.StepCase, fref.StepScope, fref.StepEvidence, fref.StepHypothesis,
	} {
		mustNil(t, s.CompleteGated(step))
	}
	// Skip straight to resolution, intending to reverse afterwards.
	err = s.CompleteGated(fref.StepCaseResolution)
	if err == nil {
		t.Fatal("resolution before the reverse direction must be refused")
	}
	if !strings.Contains(err.Error(), "REVERSE_PROOF") {
		t.Fatalf("the refusal must name the missing gate, got %q", err)
	}
}

// TestAnUngatedLedgerIsCaughtEvenWhenPerfectlyOrdered is the subtler
// attack: emit a stream that is in order but simply omits the gate.
// Order alone cannot catch an omission.
func TestAnUngatedLedgerIsCaughtEvenWhenPerfectlyOrdered(t *testing.T) {
	ordered := []string{
		"case.opened", "case.scoped", "case.evidence_pinned",
		"case.hypothesis_recorded", "case.hypothesis_tested",
		"case.qualification_begun", "proof.sealed",
		"claim.finding_founded", "case.decision_authorized", "case.resolved",
	}
	if v := fref.VerifyEventOrder(ordered); len(v) != 0 {
		t.Fatalf("this stream is in order; the order check should pass: %v", v)
	}
	g := fref.VerifyEventGates(ordered)
	if len(g) == 0 {
		t.Fatal("a stream that never recorded a reverse closure must be caught by the gate check")
	}
	found := false
	for _, m := range g {
		if strings.Contains(m, "REVERSE_PROOF") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the missing reverse closure must be named, got %v", g)
	}
}

// TestPostResolutionEvidenceInjection: change the evidential picture
// after the decision is taken.
func TestPostResolutionEvidenceInjection(t *testing.T) {
	c, o := resolvedCase(t)
	err := c.AddEvidence([]casefabric.EvidenceRef{
		{EvidenceID: "E-LATE", EvidenceVersionID: "v-late", SHA256: "zzz"}}, "attacker", 50)
	if err == nil {
		t.Fatal("evidence added after resolution would alter the basis of a decision already taken")
	}
	if !strings.Contains(err.Error(), "reopen") {
		t.Fatalf("the refusal must point at the lawful route, got %q", err)
	}
	_ = o
}

// TestGraphAsABackDoorToAFinding is the second-authority attack: get a
// finding out of the rendering layer instead of the authority.
func TestGraphAsABackDoorToAFinding(t *testing.T) {
	c, o := resolvedCase(t)
	// A sufficient proof object, and no finding supplied.
	g, err := caseproofgraph.Build(c, map[string]proof.Object{"CL-1": o}, nil, 60)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if n := len(g.NodesOfKind(caseproofgraph.NodeFinding)); n != 0 {
		t.Fatalf("the graph produced %d finding node(s) nobody authorized", n)
	}
}

// TestVisualOnlyRedactionPresentedAsIrreversible is Article 18's attack:
// a black box drawn over the text, shipped as a redaction.
func TestVisualOnlyRedactionPresentedAsIrreversible(t *testing.T) {
	const name = "Alicia Fernandez"
	orig := []byte("Inspector: " + name + "\nFindings: sound.\n")
	visual := append([]byte("%% black rectangle over the inspector line\n"), orig...)

	chain, err := redaction.Verify(orig, visual, "EV-1-v1", "EV-1-v2", redaction.Hash(orig), []string{name})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if chain.Verified {
		t.Fatal("a visual-only redaction must not verify as absent")
	}
	if !strings.Contains(chain.Explain(), "FAILED") {
		t.Fatalf("the explanation must fail loudly, got %q", chain.Explain())
	}
}

// TestRedactionThatStripsOneEncodingOnly is the realistic failure: the
// worker replaces the UTF-8 occurrence and leaves the UTF-16 copy in the
// document's string table.
func TestRedactionThatStripsOneEncodingOnly(t *testing.T) {
	const name = "Alicia Fernandez"
	orig := []byte("Inspector: " + name + "\n")
	utf16le := make([]byte, 0, len(name)*2)
	for _, b := range []byte(name) {
		utf16le = append(utf16le, b, 0x00)
	}
	partial := append([]byte("Inspector: [REDACTED]\n"), utf16le...)

	chain, err := redaction.Verify(orig, partial, "EV-1-v1", "EV-1-v2", redaction.Hash(orig), []string{name})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if chain.Verified {
		t.Fatal("stripping one encoding is not redaction")
	}
}

// TestClaimingAMaturityLevelNobodyGranted is the reporting attack.
func TestClaimingAMaturityLevelNobodyGranted(t *testing.T) {
	if err := assurance.ValidateMaturityLadder(); err != nil {
		t.Fatalf("ValidateMaturityLadder: %v", err)
	}
	if assurance.HighestClaimed().RequiresOutsideParty() {
		t.Fatal("a level requiring an outside party is claimed with none engaged")
	}
	// The claim that would matter commercially is L7, and it must be
	// unreachable from where the system stands.
	if assurance.HighestClaimed() >= assurance.L7ProductionQualified {
		t.Fatal("PRODUCTION_QUALIFIED is claimed")
	}
}

// TestASecondAuthorityCannotBeDocumentedIntoExistence: the audit must
// refuse to record a rival authority rather than describing one.
func TestASecondAuthorityCannotBeDocumented(t *testing.T) {
	if err := assurance.ValidateAuthorities(); err != nil {
		t.Fatalf("ValidateAuthorities: %v", err)
	}
	for _, d := range assurance.DecisionAuthorities() {
		for _, p := range d.Participants {
			if p.Role == assurance.RoleDecides {
				t.Fatalf("decision %q documents %s as a second authority", d.Decision, p.Package)
			}
		}
	}
}

// --- fixtures ---------------------------------------------------------

func resolvedCase(t *testing.T) (*casefabric.Case, proof.Object) {
	t.Helper()
	c := scopedCase(t)
	o := sufficientObject(t)
	mustNil(t, c.AddEvidence(o.EvidenceSet, "a", 3))
	mustNil(t, c.AddHypothesis(casefabric.Hypothesis{ID: "H-1", Description: "rival", Tested: true}, "a", 4))
	mustNil(t, c.RegisterClaim(casefabric.Claim{ID: "CL-1", Material: true, Proposition: o.Proposition}, "a", 5))
	mustNil(t, c.RecordReverseClosure(o.Proposition.ID, true, "a", 6))
	mustNil(t, c.BeginQualification("a", 7))
	mustNil(t, c.AttachProof("CL-1", o, "a", 8))

	f, err := proof.NewFinding(o, 20)
	if err != nil {
		t.Fatalf("NewFinding: %v", err)
	}
	a, err := proof.Authorize(f, o, "partner-1", "partner", "policy-v1", "adopted", 30)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	d, err := proof.Decide(a, "refer_to_tribunal", "package complete", nil, 40)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	gate := casefabric.ResolutionGate{
		Decision: d, ReverseClosureHolds: true,
		ClosureSubject: o.Proposition.ID, ClosureExplanation: "closure holds",
	}
	if _, err := c.Resolve(gate, "evidence_package_delivered", "established", "partner-1", 41); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return c, o
}
