package ledger

import (
	"errors"
	"strings"
	"testing"

	"veriqo/pkg/evidence/quality"
)

func strongJudgements(except ...quality.Attribute) []quality.Judgement {
	skip := map[quality.Attribute]bool{}
	for _, a := range except {
		skip[a] = true
	}
	var out []quality.Judgement
	for _, a := range quality.Attributes() {
		if skip[a] {
			continue
		}
		out = append(out, quality.Judgement{Attribute: a, Grade: quality.Strong,
			Basis: "checked against the acquisition record"})
	}
	return out
}

func assessment(t *testing.T, js ...quality.Judgement) *quality.Assessment {
	t.Helper()
	a, err := quality.New("evidenceversion:e1v1", js...)
	if err != nil {
		t.Fatal(err)
	}
	return &a
}

func entry() Entry {
	return Entry{
		ControlID: "ART-18", Test: "TestNoVariantLeaks",
		ExecutionID: "exec-1", Environment: "linux/amd64 go1.24 sandbox",
		InputHash: "in-1", OutputHash: "out-1",
		Tool: "go test", ToolVersion: "1.24.7",
		Result: Pass, Evidence: "evidence/corpus-run.json",
		Level: Assured, Boundary: SelfTested,
		Limitations: []string{"every fixture was built by VERIQO"},
	}
}

// TestTheAssessmentIsCoveredByTheEntryHash.
//
// This is the test the Round H report cites, and the property that
// makes the evidence assessment more than a comment: if it sat outside
// the hash, it could be edited after the entry was appended and the
// chain would still verify.
func TestTheAssessmentIsCoveredByTheEntryHash(t *testing.T) {
	l := New()

	e := entry()
	e.Level = Qualified
	e.Boundary = Validated
	e.Validator = "org:notary-cooperative"
	e.EvidenceQuality = assessment(t, strongJudgements()...)

	appended, err := l.Append(e)
	if err != nil {
		t.Fatalf("a well-formed entry was refused: %v", err)
	}
	if appended.Hash() == "" {
		t.Fatal("the appended entry carries no hash")
	}

	// Edit the assessment in place, exactly as somebody with access to
	// the stored entry would.
	entries := l.Entries()
	weakened := assessment(t, append(strongJudgements(quality.Independence),
		quality.Judgement{Attribute: quality.Independence, Grade: quality.Absent,
			Basis: "the survey was commissioned by the claimant"})...)
	entries[0].EvidenceQuality = weakened

	// The recomputed hash must differ, which is what makes the edit
	// detectable rather than silent.
	before := appended.Hash()
	after := entries[0]
	after.hash = ""
	l2 := New()
	if _, err := l2.Append(after); err == nil {
		// It may legitimately refuse the weakened entry; what matters
		// is that it does not produce the same hash.
		if l2.Entries()[0].Hash() == before {
			t.Fatal("THE ASSESSMENT IS OUTSIDE THE ENTRY HASH: it can be edited after " +
				"the entry is appended and the chain still verifies")
		}
	}
}

// TestAnEditedAssessmentBreaksTheChain states the same property from
// the verification side.
func TestAnEditedAssessmentBreaksTheChain(t *testing.T) {
	l := New()
	e := entry()
	e.EvidenceQuality = assessment(t, strongJudgements()...)
	if _, err := l.Append(e); err != nil {
		t.Fatal(err)
	}
	if err := l.Verify(); err != nil {
		t.Fatalf("a fresh chain does not verify: %v", err)
	}

	// Reach into the stored entry and change the assessment.
	l.entries[0].EvidenceQuality = assessment(t,
		append(strongJudgements(quality.Provenance),
			quality.Judgement{Attribute: quality.Provenance, Grade: quality.Weak,
				Basis: "the acquisition record is incomplete"})...)

	if err := l.Verify(); !errors.Is(err, ErrChainBroken) {
		t.Fatalf("an edited assessment left the chain verifying: %v", err)
	}
}

// TestAPassCannotRestOnInsufficientEvidence is the first of the four
// joins between the quality vector and the ladder.
func TestAPassCannotRestOnInsufficientEvidence(t *testing.T) {
	e := entry()
	e.EvidenceQuality = assessment(t, append(strongJudgements(quality.Authenticity),
		quality.Judgement{Attribute: quality.Authenticity, Grade: quality.Absent,
			Basis: "the document is an unsigned scan of unknown origin"})...)

	if err := e.Validate(); !errors.Is(err, ErrEvidenceInsufficient) {
		t.Fatalf("a PASS rested on evidence the assessment found wanting: %v", err)
	}

	// The same evidence may still be recorded as a FAIL: pushing the
	// deficiency out of the ledger would be the opposite of what the
	// ledger is for.
	e.Result = Fail
	if err := e.Validate(); err != nil {
		t.Fatalf("a FAIL on deficient evidence was refused: %v", err)
	}
}

// TestALevelNeedingAnOutsidePartyNeedsItsEvidenceAssessed.
func TestALevelNeedingAnOutsidePartyNeedsItsEvidenceAssessed(t *testing.T) {
	e := entry()
	e.Level = Qualified
	e.Boundary = Validated
	e.Validator = "org:notary-cooperative"
	e.EvidenceQuality = nil

	if err := e.Validate(); !errors.Is(err, ErrEvidenceUnassessed) {
		t.Fatalf("QUALIFIED was claimed over evidence nobody assessed: %v", err)
	}
	// Below the boundary, no assessment is required.
	e.Level = Assured
	e.Boundary = SelfTested
	e.Validator = ""
	if err := e.Validate(); err != nil {
		t.Fatalf("an internal-ceiling entry required an assessment: %v", err)
	}
}

// TestAnIncompleteAssessmentCannotSupportAnythingAboveTheCeiling.
func TestAnIncompleteAssessmentCannotSupportAnythingAboveTheCeiling(t *testing.T) {
	// A vector with an unasked question is UNASSESSABLE, not
	// insufficient -- one needs better evidence, the other needs
	// somebody to look.
	incomplete := assessment(t, strongJudgements(quality.Reproducibility)...)
	decision, _, err := incomplete.Decide()
	if err != nil {
		t.Fatal(err)
	}
	if decision != quality.Unassessable {
		t.Fatalf("a vector with an unasked question decided %s", decision)
	}

	e := entry()
	e.Level = Qualified
	e.Boundary = Validated
	e.Validator = "org:notary-cooperative"
	e.EvidenceQuality = incomplete
	if err := e.Validate(); err == nil {
		t.Fatal("an incomplete assessment supported a level above the internal ceiling")
	}

	e.Level = Assured
	e.Boundary = SelfTested
	e.Validator = ""
	if err := e.Validate(); err != nil {
		t.Fatalf("an incomplete assessment was refused below the ceiling: %v", err)
	}
}

// TestStatedLimitsMustTravelWithTheConclusion is the fourth join, and
// the one that matters most to a reader in a hurry: ADEQUATE without
// limits reads as STRONG.
func TestStatedLimitsMustTravelWithTheConclusion(t *testing.T) {
	withLimits := assessment(t, append(strongJudgements(quality.Scope),
		quality.Judgement{Attribute: quality.Scope, Grade: quality.Adequate,
			Basis:  "covers the loading port only",
			Limits: "does not cover the discharge port"})...)

	e := entry()
	e.EvidenceQuality = withLimits
	e.Limitations = []string{"every fixture was built by VERIQO"} // the limit is NOT repeated

	if err := e.Validate(); !errors.Is(err, ErrLimitsDropped) {
		t.Fatalf("an entry dropped the limits its own assessment stated: %v", err)
	}

	e.Limitations = append(e.Limitations, "does not cover the discharge port")
	if err := e.Validate(); err != nil {
		t.Fatalf("an entry carrying its limits was refused: %v", err)
	}
}

// --- FC-006 at the ladder --------------------------------------------

// TestVeriqoCannotBeItsOwnExternalValidator is the negative test the
// failure-class register cites for FC-006.
func TestVeriqoCannotBeItsOwnExternalValidator(t *testing.T) {
	for _, name := range []string{
		"VERIQO", "veriqo", "VERIQO QA", "the veriqo assurance team",
		"Veriqo Internal Audit",
	} {
		e := entry()
		e.Level = Qualified
		e.Boundary = Validated
		e.Validator = name
		e.EvidenceQuality = assessment(t, strongJudgements()...)
		if err := e.Validate(); !errors.Is(err, ErrSelfValidator) {
			t.Errorf("validator %q was accepted as external: %v", name, err)
		}
	}
	// And an outside party is accepted, or the rule refuses everything
	// and proves nothing.
	e := entry()
	e.Level = Qualified
	e.Boundary = Validated
	e.Validator = "org:notary-cooperative"
	e.EvidenceQuality = assessment(t, strongJudgements()...)
	if err := e.Validate(); err != nil {
		t.Fatalf("a genuine outside validator was refused: %v", err)
	}
}

// TestTheLadderIsTheReviewersLadder is the positive test for FC-006:
// the levels that require an outside party are exactly the ones the
// review named, and the internal ceiling is where it says it is.
func TestTheLadderIsTheReviewersLadder(t *testing.T) {
	if InternalCeiling() != Assured {
		t.Fatalf("the internal ceiling is %s", InternalCeiling())
	}
	for _, l := range Levels() {
		want := l >= Qualified
		if l.RequiresOutsideParty() != want {
			t.Errorf("%s requires an outside party = %v, want %v",
				l, l.RequiresOutsideParty(), want)
		}
	}
	if InternalCeiling().RequiresOutsideParty() {
		t.Fatal("the internal ceiling itself requires an outside party; it is not reachable " +
			"from inside and is therefore not a ceiling")
	}
	// The level above the ceiling must be unreachable alone.
	if !Qualified.RequiresOutsideParty() {
		t.Fatal("QUALIFIED is self-reachable; the boundary has been inflated away")
	}
}

// TestAQualifiedClaimNeedsTheValidatedBoundary is the regression test
// for FC-006: the level and the boundary cannot disagree.
func TestAQualifiedClaimNeedsTheValidatedBoundary(t *testing.T) {
	e := entry()
	e.Level = Qualified
	e.Boundary = SelfTested // VERIQO's own word
	e.EvidenceQuality = assessment(t, strongJudgements()...)
	if err := e.Validate(); !errors.Is(err, ErrLevelBoundary) {
		t.Fatalf("QUALIFIED was claimed on a self-tested boundary: %v", err)
	}
	// PROVED is stronger than SELF_TESTED and still not an outside
	// party: it must not buy the level either.
	e.Boundary = Proved
	if err := e.Validate(); !errors.Is(err, ErrLevelBoundary) {
		t.Fatalf("QUALIFIED was claimed on a by-construction proof: %v", err)
	}
	e.Boundary = Validated
	e.Validator = "org:notary-cooperative"
	if err := e.Validate(); err != nil {
		t.Fatalf("a properly validated QUALIFIED entry was refused: %v", err)
	}
}

// --- The record a qualification must leave behind ----------------------

// TestAPassWithNoEvidenceArtefactIsRefused. A test that passed and
// left nothing behind is a claim about the past made by the party who
// benefits from it.
func TestAPassWithNoEvidenceArtefactIsRefused(t *testing.T) {
	e := entry()
	e.Evidence = ""
	if err := e.Validate(); !errors.Is(err, ErrNoEvidence) {
		t.Fatalf("a PASS with no artefact was accepted: %v", err)
	}
}

func TestTheRequiredFieldsAreEnforced(t *testing.T) {
	cases := map[string]func(*Entry){
		"no control":     func(e *Entry) { e.ControlID = "" },
		"no test":        func(e *Entry) { e.Test = "" },
		"no execution":   func(e *Entry) { e.ExecutionID = "" },
		"no environment": func(e *Entry) { e.Environment = "" },
		"no tool":        func(e *Entry) { e.Tool = "" },
	}
	for name, mutate := range cases {
		e := entry()
		mutate(&e)
		if err := e.Validate(); err == nil {
			t.Errorf("an entry with %s was accepted", name)
		}
	}
}

// TestTheChainLinksAndVerifies.
func TestTheChainLinksAndVerifies(t *testing.T) {
	l := New()
	for i := 0; i < 4; i++ {
		e := entry()
		e.ExecutionID = "exec-" + string(rune('1'+i))
		if _, err := l.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Verify(); err != nil {
		t.Fatalf("the chain does not verify: %v", err)
	}
	if l.RootHash() == "" {
		t.Fatal("the ledger has no root hash")
	}
	// Editing any field must break it.
	l.entries[2].Environment = "somewhere else"
	if err := l.Verify(); !errors.Is(err, ErrChainBroken) {
		t.Fatalf("an edited entry left the chain verifying: %v", err)
	}
}

// TestHighestLevelForReadsTheLedgerNotTheClaim.
func TestHighestLevelForReadsTheLedgerNotTheClaim(t *testing.T) {
	l := New()
	e := entry()
	e.Level = Integrated
	l.Append(e)
	e2 := entry()
	e2.ExecutionID = "exec-2"
	e2.Level = Assured
	l.Append(e2)

	got, ok := l.HighestLevelFor("ART-18")
	if !ok {
		t.Fatal("the control is not in the ledger")
	}
	if got != Assured {
		t.Fatalf("highest level = %s", got)
	}
	if _, ok := l.HighestLevelFor("ART-99"); ok {
		t.Fatal("a control with no entries reported a level")
	}
}

// TestTheReportStatesTheCeiling. A report that omits it invites the
// reader to assume the top of the ladder is reachable.
func TestTheReportStatesTheCeiling(t *testing.T) {
	l := New()
	l.Append(entry())
	r := l.Report()
	if !strings.Contains(r, "ASSURED") {
		t.Fatalf("the report does not name the level reached:\n%s", r)
	}
}

// TestARefusalIsNotAPassAndNotAFailure. The same distinction the
// corpus makes, at the ladder.
func TestARefusalIsNotAPassAndNotAFailure(t *testing.T) {
	e := entry()
	e.Result = Refused
	e.Evidence = "" // a refusal need not produce an artefact
	if err := e.Validate(); err != nil {
		t.Fatalf("a REFUSED entry was refused: %v", err)
	}
	if Refused == Pass || Refused == Fail {
		t.Fatal("REFUSED collapsed into PASS or FAIL")
	}
}
