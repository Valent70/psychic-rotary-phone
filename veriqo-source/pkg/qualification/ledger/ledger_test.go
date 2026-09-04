package ledger

import (
	"errors"
	"strings"
	"testing"
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
	if err := e.Validate(); err != nil {
		t.Fatalf("a properly validated claim must be accepted: %v", err)
	}
}

// TestAValidatorWithoutTheBoundaryIsRefused catches the mirror error:
// naming a validator while claiming only self-testing, which reads to a
// skimmer as external validation.
func TestAValidatorWithoutTheBoundaryIsRefused(t *testing.T) {
	e := goodEntry()
	e.Validator = "some external firm"
	err := e.Validate()
	if err == nil || !strings.Contains(err.Error(), "claims only") {
		t.Fatalf("want a boundary mismatch refusal, got %v", err)
	}
}

// TestRefusedSupportsNothing. A control that declined to act was safe.
// Safety is not evidence of capability -- the exact point the review
// made about the redaction workers.
func TestRefusedSupportsNothing(t *testing.T) {
	l := New()
	e := goodEntry()
	e.Result, e.ControlID = Refused, "ARTICLE-18-PDF-ENCRYPTED"
	if _, err := l.Append(e); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, ok := l.HighestLevelFor("ARTICLE-18-PDF-ENCRYPTED"); ok {
		t.Fatal("a REFUSED entry supported a level: refusing is safe, not proof of capability")
	}
}

// TestTheChainDetectsTampering.
func TestTheChainDetectsTampering(t *testing.T) {
	l := New()
	for i, id := range []string{"A", "B", "C"} {
		e := goodEntry()
		e.ControlID, e.ExecutionID = id, string(rune('a'+i))
		if _, err := l.Append(e); err != nil {
			t.Fatalf("Append %s: %v", id, err)
		}
	}
	if err := l.Verify(); err != nil {
		t.Fatalf("a clean ledger must verify: %v", err)
	}
	l.entries[1].Result = Fail
	if err := l.Verify(); !errors.Is(err, ErrChainBroken) {
		t.Fatalf("want ErrChainBroken after tampering, got %v", err)
	}
}

// TestHighestLevelIgnoresFailures.
func TestHighestLevelIgnoresFailures(t *testing.T) {
	l := New()
	pass := goodEntry()
	pass.Level = Integrated
	if _, err := l.Append(pass); err != nil {
		t.Fatalf("Append: %v", err)
	}
	fail := goodEntry()
	fail.Result, fail.Level, fail.ExecutionID = Fail, Assured, "exec-2"
	if _, err := l.Append(fail); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, ok := l.HighestLevelFor("ARTICLE-18")
	if !ok || got != Integrated {
		t.Fatalf("HighestLevelFor = %s (%v), want INTEGRATED: a FAIL must not raise a level", got, ok)
	}
}

// TestEveryBoundaryStatesWhatItDoesNotLicense.
func TestEveryBoundaryStatesWhatItDoesNotLicense(t *testing.T) {
	for _, b := range Boundaries() {
		if strings.TrimSpace(b.Meaning()) == "" {
			t.Errorf("%s states no meaning", b)
		}
	}
	if !strings.Contains(Proved.Meaning(), "VERIQO's own reasoning about VERIQO's own code") {
		t.Fatal("VERIQO_PROVED must say that it is still VERIQO reasoning about VERIQO")
	}
	if Proved.RequiresValidator() || SelfTested.RequiresValidator() {
		t.Fatal("only EXTERNALLY_VALIDATED requires a validator")
	}
}

// TestTheReportShowsLimitations. A report that dropped them would let
// the ledger's honesty stop at its own API.
func TestTheReportShowsLimitations(t *testing.T) {
	l := New()
	if _, err := l.Append(goodEntry()); err != nil {
		t.Fatalf("Append: %v", err)
	}
	rep := l.Report()
	if !strings.Contains(rep, "fixture containers, not a real-world corpus") {
		t.Fatal("the report omits the entry's limitations")
	}
}
