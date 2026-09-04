package ledger

import (
	"errors"
	"strings"
	"testing"

	"veriqo/pkg/evidence/quality"
)

func goodEntry() Entry {
	return Entry{
		ControlID: "ARTICLE-18", Test: "TestEachWorkerProducesAVerifiedDerivative",
		ExecutionID: "exec-1", Environment: "go1.x, linux/amd64, hermetic, no network",
		InputHash: HashOf([]byte("original")), OutputHash: HashOf([]byte("derivative")),
		Tool: "go test", ToolVersion: "1.x", Result: Pass,
		Evidence: "evidence/RUNTIME_EVIDENCE.json#AUDIT-014",
		Level:    Assured, Boundary: SelfTested,
		Limitations: []string{"fixture containers, not a real-world corpus"},
	}
}

// TestTheLadderIsTheReviewersLadder. IMPLEMENTED -> INTEGRATED ->
// ASSURED -> QUALIFIED -> EXTERNALLY VALIDATED -> PRODUCTION PROVEN.
func TestTheLadderIsTheReviewersLadder(t *testing.T) {
	want := []string{"IMPLEMENTED", "INTEGRATED", "ASSURED", "QUALIFIED",
		"EXTERNALLY_VALIDATED", "PRODUCTION_PROVEN"}
	got := Levels()
	if len(got) != len(want) {
		t.Fatalf("the ladder has %d rungs, want %d", len(got), len(want))
	}
	for i, l := range got {
		if l.String() != want[i] {
			t.Fatalf("rung %d is %s, want %s", i, l, want[i])
		}
		if i > 0 && got[i-1] >= l {
			t.Fatalf("the ladder is not ordered at rung %d", i)
		}
	}
}

// TestTheInternalCeilingIsAssured. Everything above needs somebody who
// is not VERIQO.
func TestTheInternalCeilingIsAssured(t *testing.T) {
	if InternalCeiling() != Assured {
		t.Fatalf("the internal ceiling is %s, want ASSURED", InternalCeiling())
	}
	for _, l := range Levels() {
		want := l >= Qualified
		if l.RequiresOutsideParty() != want {
			t.Errorf("%s: RequiresOutsideParty = %v, want %v", l, l.RequiresOutsideParty(), want)
		}
	}
}

// TestAPassWithNoEvidenceIsRefused. A PASS pointing at nothing is an
// assertion by the party who benefits from it.
func TestAPassWithNoEvidenceIsRefused(t *testing.T) {
	e := goodEntry()
	e.Evidence = ""
	if err := e.Validate(); !errors.Is(err, ErrNoEvidence) {
		t.Fatalf("want ErrNoEvidence, got %v", err)
	}
}

// TestAPassWithNoLimitationsIsRefused. Every real qualification has a
// boundary; one that names none has not looked for it.
func TestAPassWithNoLimitationsIsRefused(t *testing.T) {
	e := goodEntry()
	e.Limitations = nil
	err := e.Validate()
	if err == nil || !strings.Contains(err.Error(), "states no limitations") {
		t.Fatalf("want a limitations refusal, got %v", err)
	}
}

// TestEveryRequiredFieldIsRequired walks the nine fields the review
// listed and proves each is load-bearing.
func TestEveryRequiredFieldIsRequired(t *testing.T) {
	for _, tc := range []struct {
		name   string
		breaks func(*Entry)
		want   error
	}{
		{"no control", func(e *Entry) { e.ControlID = "" }, ErrNoControl},
		{"no test", func(e *Entry) { e.Test = "" }, ErrNoTest},
		{"no execution id", func(e *Entry) { e.ExecutionID = "" }, ErrNoExecution},
		{"no environment", func(e *Entry) { e.Environment = "" }, ErrNoEnvironment},
		{"no tool", func(e *Entry) { e.Tool = "" }, ErrNoTool},
		{"no tool version", func(e *Entry) { e.ToolVersion = "" }, ErrNoTool},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := goodEntry()
			tc.breaks(&e)
			if err := e.Validate(); !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

// TestVeriqoCannotBeItsOwnExternalValidator is the golden rule the
// review asked for, enforced rather than written down.
func TestVeriqoCannotBeItsOwnExternalValidator(t *testing.T) {
	for _, who := range []string{
		"VERIQO Operations Ltd", "veriqo internal audit", "ourselves", "the internal team", "self-assessment",
	} {
		e := goodEntry()
		e.Level, e.Boundary, e.Validator = Qualified, Validated, who
		if err := e.Validate(); !errors.Is(err, ErrSelfValidator) {
			t.Errorf("%q was accepted as an external validator: %v", who, err)
		}
	}
}

// TestAQualifiedClaimNeedsTheValidatedBoundary is the check that stops
// "we ran the test" being recorded as "it is qualified".
func TestAQualifiedClaimNeedsTheValidatedBoundary(t *testing.T) {
	e := goodEntry()
	e.Level = Qualified // still SelfTested
	if err := e.Validate(); !errors.Is(err, ErrLevelBoundary) {
		t.Fatalf("want ErrLevelBoundary, got %v", err)
	}
	e.Boundary, e.Validator = Validated, "an accredited assessor, named in the engagement letter"
	// A level above the internal ceiling also needs the evidence to
	// have been assessed: an outside party is supposed to have looked
	// at the artefact, not only at the claim.
	if err := e.Validate(); !errors.Is(err, ErrEvidenceUnassessed) {
		t.Fatalf("want ErrEvidenceUnassessed, got %v", err)
	}
	a := fullStrongAssessment(t)
	e.EvidenceQuality = &a
	if err := e.Validate(); err != nil {
		t.Fatalf("a properly validated and assessed claim must be accepted: %v", err)
	}
}

// fullStrongAssessment is an assessment where every one of the nine
// attributes is STRONG. It is a fixture, not a claim about any real
// artefact.
func fullStrongAssessment(t *testing.T) quality.Assessment {
	t.Helper()
	var js []quality.Judgement
	for _, attr := range quality.Attributes() {
		js = append(js, quality.Judgement{
			Attribute: attr, Grade: quality.Strong,
			Basis: "fixture: stated strong so the ledger rules can be exercised",
		})
	}
	a, err := quality.New("EV-FIXTURE-1", js...)
	if err != nil {
		t.Fatalf("building the fixture assessment: %v", err)
	}
	return a
}

// TestAPassCannotRestOnEvidenceAssessedInsufficient. The two
// vocabularies -- how far up the ladder, and how good the evidence --
// have to constrain each other, or a control can be recorded as
// passing over evidence its own assessment found wanting.
func TestAPassCannotRestOnEvidenceAssessedInsufficient(t *testing.T) {
	var js []quality.Judgement
	for _, attr := range quality.Attributes() {
		g, basis := quality.Strong, "fixture"
		if attr == quality.Independence {
			g, basis = quality.Absent, "the only source is the party the claim is about"
		}
		js = append(js, quality.Judgement{Attribute: attr, Grade: g, Basis: basis})
	}
	a, err := quality.New("EV-FIXTURE-2", js...)
	if err != nil {
		t.Fatalf("quality.New: %v", err)
	}
	e := goodEntry()
	e.EvidenceQuality = &a
	if err := e.Validate(); !errors.Is(err, ErrEvidenceInsufficient) {
		t.Fatalf("want ErrEvidenceInsufficient, got %v", err)
	}
	// The same evidence may still be recorded -- as a FAIL. Refusing
	// to record it at all would push the deficiency out of the ledger,
	// which is the opposite of what the ledger is for.
	e.Result = Fail
	if err := e.Validate(); err != nil {
		t.Fatalf("a FAIL over insufficient evidence must still be recordable: %v", err)
	}
}

// TestTheAssessmentsLimitsMustTravelIntoTheEntry. An ADEQUATE grade
// carries a statement of what it does not cover. An entry that cites
// the assessment and drops that sentence is presenting a bounded result
// as an unbounded one.
func TestTheAssessmentsLimitsMustTravelIntoTheEntry(t *testing.T) {
	limit := "covers the container only, not embedded objects"
	var js []quality.Judgement
	for _, attr := range quality.Attributes() {
		j := quality.Judgement{Attribute: attr, Grade: quality.Strong, Basis: "fixture"}
		if attr == quality.Scope {
			j = quality.Judgement{Attribute: attr, Grade: quality.Adequate,
				Basis: "fixture", Limits: limit}
		}
		js = append(js, j)
	}
	a, err := quality.New("EV-FIXTURE-3", js...)
	if err != nil {
		t.Fatalf("quality.New: %v", err)
	}
	e := goodEntry()
	e.EvidenceQuality = &a
	e.Limitations = []string{"the test ran in CI, not in production"}
	if err := e.Validate(); !errors.Is(err, ErrLimitsDropped) {
		t.Fatalf("want ErrLimitsDropped, got %v", err)
	}
	e.Limitations = append(e.Limitations, limit)
	if err := e.Validate(); err != nil {
		t.Fatalf("an entry that carries the limits forward must be accepted: %v", err)
	}
}

// TestAnIncompleteAssessmentCannotUnderwriteAnExternalLevel.
// UNASSESSABLE is unfinished work, not a bad grade: it may accompany
// any result up to the internal ceiling and nothing above it.
func TestAnIncompleteAssessmentCannotUnderwriteAnExternalLevel(t *testing.T) {
	a, err := quality.New("EV-FIXTURE-4", quality.Judgement{
		Attribute: quality.Integrity, Grade: quality.Strong, Basis: "fixture",
	})
	if err != nil {
		t.Fatalf("quality.New: %v", err)
	}
	e := goodEntry()
	e.EvidenceQuality = &a
	if err := e.Validate(); err != nil {
		t.Fatalf("an incomplete assessment is allowed below the ceiling: %v", err)
	}
	e.Level, e.Boundary, e.Validator = Qualified, Validated, "an accredited assessor"
	if err := e.Validate(); !errors.Is(err, ErrEvidenceUnassessed) {
		t.Fatalf("want ErrEvidenceUnassessed, got %v", err)
	}
}

// TestTheAssessmentIsCoveredByTheEntryHash. If it were not, the
// assessment could be edited after the entry was appended and the chain
// would still verify -- which would make the assessment a comment.
func TestTheAssessmentIsCoveredByTheEntryHash(t *testing.T) {
	l := New()
	e := goodEntry()
	a := fullStrongAssessment(t)
	e.EvidenceQuality = &a
	if _, err := l.Append(e); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.Verify(); err != nil {
		t.Fatalf("a clean ledger must verify: %v", err)
	}
	// Downgrade one attribute through the pointer the entry holds.
	a.Judgements[quality.Independence] = quality.Judgement{
		Attribute: quality.Independence, Grade: quality.Adequate,
		Basis: "edited after the entry was appended", Limits: "one source",
	}
	if err := l.Verify(); !errors.Is(err, ErrChainBroken) {
		t.Fatalf("editing the evidence assessment left the chain verifying: %v", err)
	}
}
