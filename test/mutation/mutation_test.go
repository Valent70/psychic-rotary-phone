// Package mutation is VERIQO's mutation qualification suite.
//
// # The fear this addresses
//
// The review named it exactly, and it is the right fear:
//
//	Yang paling saya khawatirkan sekarang: bukan code, bukan test,
//	bukan coverage. OVERFITTING terhadap test suite.
//
//	Reviewer menemukan bug A -> buat test A -> PASS -> "problem
//	solved". Padahal bug family: A B C D E.
//
// Every test in this repository can pass for two reasons: the control
// works, or the test is too weak to notice that it does not. Nothing
// written so far distinguishes those.
//
// # What a mutation test is here
//
// Take a control whose invariant is known. Deliberately BREAK the
// invariant -- flip UNKNOWN to INDEPENDENT, REFUSED to FAILED,
// HISTORICAL to CURRENT -- and assert that the system rejects the
// mutant. A mutation that survives is a hole in the invariant, and it
// is a hole whether or not any test currently exercises it.
//
// This is stronger than a negative test. A negative test proves one
// wrong input is rejected. A mutation test proves the RULE cannot be
// satisfied by a value that violates it, which is a statement about the
// whole input space rather than about the case somebody thought of.
//
// # Why the mutations are these three
//
// They are the three the review named, and each is the exact confusion
// that would let a weaker claim be presented as a stronger one:
//
//	UNKNOWN   -> INDEPENDENT   unassessed becomes corroborating
//	REFUSED   -> FAILED        a designed refusal becomes a defect,
//	                           or worse in reverse: a defect becomes a
//	                           safe outcome
//	HISTORICAL-> CURRENT       a superseded claim becomes a live one
package mutation

import (
	"errors"
	"strings"
	"testing"

	"veriqo/pkg/evidence/quality"
	"veriqo/pkg/evidence/redaction/corpus"
	"veriqo/pkg/provenance/temporal"
	"veriqo/pkg/qualification/independence"
	"veriqo/pkg/qualification/ledger"
)

// mutant describes one deliberate violation and what must reject it.
type mutant struct {
	// name is the mutation, in the review's own notation.
	name string
	// invariant is the rule being attacked.
	invariant string
	// apply performs the mutation and returns whether the system
	// rejected it, plus what it said.
	apply func(t *testing.T) (rejected bool, detail string)
}

// --- Mutation 1: UNKNOWN -> INDEPENDENT --------------------------------

// TestMutationUnknownBecomesIndependent attacks Article 28.
//
// The mutation is not a code edit: it is constructing the value the
// invariant forbids and asking the system to accept it. A system whose
// rule is real cannot be made to produce the forbidden answer from any
// input.
func TestMutationUnknownBecomesIndependent(t *testing.T) {
	// A source with no assessed dimensions is UNKNOWN against anything.
	unassessed := independence.Source{ID: "an-unassessed-feed"}
	assessed := independence.Source{ID: "ais-provider", Attributes: fullyAssessedAttrs("ais")}

	// The mutation: try to obtain a corroboration count of 2 from a
	// pair one of which was never assessed.
	count, unknownPairs, err := independence.EffectiveIndependentCount(
		[]independence.Source{assessed, unassessed})
	if err != nil {
		t.Fatalf("EffectiveIndependentCount: %v", err)
	}
	if count >= 2 {
		t.Fatalf("MUTANT SURVIVED: an unassessed pair produced a corroboration count of %d. "+
			"UNKNOWN was promoted to INDEPENDENT, which Article 28 forbids", count)
	}
	if len(unknownPairs) == 0 {
		t.Fatal("MUTANT SURVIVED: the unassessed pair was not named, so a caller cannot tell " +
			"a low count from a small source set")
	}

	// The second half of the mutation, which is the one a weak test
	// misses: the pairwise verdict itself must not be Independent.
	res, err := independence.Assess(assessed, unassessed)
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if res.Verdict == independence.Independent {
		t.Fatal("MUTANT SURVIVED: an unassessed pair was assessed INDEPENDENT")
	}
	if res.Verdict.SatisfiesIndependenceRequirement() {
		t.Fatalf("MUTANT SURVIVED: verdict %s satisfies an independence requirement", res.Verdict)
	}
	// And the strict form must refuse loudly rather than returning a
	// weaker verdict a caller might not inspect.
	if _, err := independence.RequireIndependent(assessed, unassessed); err == nil {
		t.Fatal("MUTANT SURVIVED: RequireIndependent accepted an unassessed pair")
	}
}

// --- Mutation 2: REFUSED -> FAILED (and back) ---------------------------

// TestMutationRefusedBecomesFailed attacks the distinction between a
// designed refusal and a defect.
//
// Both directions are dangerous and they are dangerous differently. A
// defect reported as REFUSED is a bug hidden behind a safe-looking
// word. A refusal reported as FAILED makes the corpus look broken and
// pushes somebody to "fix" correct behaviour.
func TestMutationRefusedBecomesFailed(t *testing.T) {
	outcomes, cov, err := corpus.Run()
	if err != nil {
		t.Fatalf("corpus.Run: %v", err)
	}
	if cov.Failed > 0 {
		t.Fatalf("MUTANT SURVIVED: %d variant(s) FAILED rather than being accepted or refused. "+
			"A defect is being carried in the same bucket as a designed outcome", cov.Failed)
	}
	// Every refusal must carry a reason. An unexplained refusal is
	// indistinguishable from a swallowed failure, which is the mutation
	// in its most dangerous form: it looks safe.
	for _, o := range outcomes {
		if o.Actual != corpus.Refused {
			continue
		}
		if strings.TrimSpace(o.Detail) == "" {
			t.Errorf("MUTANT SURVIVED: %s was REFUSED with no reason", o.Variant.ID)
		}
		if strings.TrimSpace(o.Variant.Why) == "" {
			t.Errorf("MUTANT SURVIVED: %s is expected to be refused but the taxonomy says nothing "+
				"about why, so a regression to REFUSED would look intentional", o.Variant.ID)
		}
	}
	// And the ledger must refuse to let a REFUSED support a level: a
	// control that declined was safe, and safety is not capability.
	l := ledger.New()
	e := ledger.Entry{
		ControlID: "ARTICLE-18", Test: "TestMutationRefusedBecomesFailed",
		ExecutionID: "mut-1", Environment: "hermetic", Tool: "go test", ToolVersion: "1.x",
		Result: ledger.Refused, Level: ledger.Assured, Boundary: ledger.SelfTested,
	}
	if _, err := l.Append(e); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, ok := l.HighestLevelFor("ARTICLE-18"); ok {
		t.Fatal("MUTANT SURVIVED: a REFUSED result raised a control's assurance level")
	}
}

// --- Mutation 3: HISTORICAL -> CURRENT ----------------------------------

// TestMutationHistoricalBecomesCurrent attacks temporal provenance.
//
// This is the mutation that produced a real defect two rounds ago: a
// superseded runtime ledger quoted as fact. The invariant now has to
// survive being attacked rather than merely being documented.
func TestMutationHistoricalBecomesCurrent(t *testing.T) {
	hist := temporal.Reference{Subject: "AUDIT-009-case.resolved", State: temporal.Historical}

	// The mutation: promote without a reason.
	if _, err := hist.Transition(temporal.Current, ""); !errors.Is(err, temporal.ErrPromotion) {
		t.Fatalf("MUTANT SURVIVED: HISTORICAL was promoted to CURRENT with no stated reason (%v)", err)
	}

	// The subtler mutation: keep the state and claim current usability.
	if hist.PresentableAsCurrent() {
		t.Fatal("MUTANT SURVIVED: a HISTORICAL reference may be presented as current")
	}

	// The subtlest, and the one a boolean marker could never catch:
	// claim HISTORICAL and VALID together, which asserts a reference
	// quoted as history also holds now.
	s := temporal.Standing{Reference: hist, Validity: temporal.Valid}
	if err := s.Validate(); !errors.Is(err, temporal.ErrValidityMismatch) {
		t.Fatalf("MUTANT SURVIVED: HISTORICAL + VALID validated (%v)", err)
	}

	// And a SUPERSEDED reference must not lose its successor by
	// transitioning: a mutation that clears the link would leave a
	// dangling claim nobody can follow forward.
	sup := temporal.Reference{Subject: "AUDIT-009", State: temporal.Superseded,
		SupersededBy: "AUDIT-013"}
	if sup.PresentableAsCurrent() {
		t.Fatal("MUTANT SURVIVED: a SUPERSEDED reference is presentable as current")
	}
	moved, err := sup.Transition(temporal.Historical, "the successor was withdrawn")
	if err != nil {
		t.Fatalf("Transition: %v", err)
	}
	if moved.SupersededBy != "" {
		t.Fatal("MUTANT SURVIVED: a successor link survived a transition away from SUPERSEDED, " +
			"leaving a reference that is HISTORICAL and names a replacement")
	}
}

// --- Mutation 4: NOT_ASSESSED -> ASSESSED --------------------------------

// TestMutationNotAssessedBecomesAssessed attacks the evidence quality
// model at the same joint the others attack: an absence of work being
// read as work.
func TestMutationNotAssessedBecomesAssessed(t *testing.T) {
	// The mutation: attach a reason to an unasked question, so the
	// vector reads as though somebody looked.
	j := quality.Judgement{Attribute: quality.Independence, Grade: quality.NotAssessed,
		Basis: "we are confident about this one"}
	if err := j.Validate(); !errors.Is(err, quality.ErrBasisWithoutWork) {
		t.Fatalf("MUTANT SURVIVED: a NOT_ASSESSED attribute carried a basis (%v)", err)
	}

	// The mutation that matters most: a partly-assessed vector
	// reporting INSUFFICIENT rather than UNASSESSABLE, which converts
	// "nobody looked" into a judgement about the evidence.
	a, err := quality.New("EV-1",
		quality.Judgement{Attribute: quality.Integrity, Grade: quality.Strong, Basis: "hash verified"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d, why, err := a.Decide()
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d != quality.Unassessable {
		t.Fatalf("MUTANT SURVIVED: a vector with eight unasked questions decided %s, "+
			"turning an absence of work into a verdict about the evidence", d)
	}
	if !strings.Contains(why, "nobody looked") {
		t.Fatalf("MUTANT SURVIVED: the reason does not distinguish absence of work: %s", why)
	}
}

// --- Mutation 5: SELF -> EXTERNAL VALIDATOR --------------------------------

// TestMutationVeriqoBecomesItsOwnValidator attacks the boundary that
// every commercial claim rests on.
func TestMutationVeriqoBecomesItsOwnValidator(t *testing.T) {
	for _, who := range []string{
		"VERIQO Operations Ltd", "veriqo internal audit", "ourselves",
		"the internal team", "self-assessment",
	} {
		e := ledger.Entry{
			ControlID: "ARTICLE-1", Test: "TestMutation", ExecutionID: "mut-2",
			Environment: "hermetic", Tool: "go test", ToolVersion: "1.x",
			Result: ledger.Pass, Evidence: "somewhere", Limitations: []string{"a limit"},
			Level: ledger.Qualified, Boundary: ledger.Validated, Validator: who,
		}
		if err := e.Validate(); !errors.Is(err, ledger.ErrSelfValidator) {
			t.Errorf("MUTANT SURVIVED: %q was accepted as an external validator (%v)", who, err)
		}
	}
	// And the level/boundary mutation: claiming QUALIFIED while only
	// self-tested.
	e := ledger.Entry{
		ControlID: "ARTICLE-1", Test: "TestMutation", ExecutionID: "mut-3",
		Environment: "hermetic", Tool: "go test", ToolVersion: "1.x",
		Result: ledger.Pass, Evidence: "somewhere", Limitations: []string{"a limit"},
		Level: ledger.Qualified, Boundary: ledger.SelfTested,
	}
	if err := e.Validate(); !errors.Is(err, ledger.ErrLevelBoundary) {
		t.Fatalf("MUTANT SURVIVED: QUALIFIED was claimed at the SELF_TESTED boundary (%v)", err)
	}
}

// --- the suite as a whole -------------------------------------------------

// TestEveryMutationIsAttempted guards the suite against itself.
//
// A mutation suite that stopped running its mutations would pass
// silently, which is the same failure it exists to prevent one level up.
func TestEveryMutationIsAttempted(t *testing.T) {
	required := []string{
		"TestMutationUnknownBecomesIndependent",
		"TestMutationRefusedBecomesFailed",
		"TestMutationHistoricalBecomesCurrent",
		"TestMutationNotAssessedBecomesAssessed",
		"TestMutationVeriqoBecomesItsOwnValidator",
	}
	src := readSelf(t)
	for _, name := range required {
		if !strings.Contains(src, "func "+name+"(") {
			t.Errorf("the mutation suite no longer attempts %s", name)
		}
	}
	// Every mutation must assert with the MUTANT SURVIVED phrasing, so
	// a failure is unmistakable in a log and cannot be read as an
	// ordinary assertion miss.
	if strings.Count(src, "MUTANT SURVIVED") < len(required) {
		t.Fatal("some mutation does not report a surviving mutant distinctly")
	}
}

func fullyAssessedAttrs(id string) map[independence.Dimension]string {
	m := map[independence.Dimension]string{}
	for _, d := range independence.DisqualifyingDimensions() {
		m[d] = id + "-" + string(d)
	}
	return m
}
