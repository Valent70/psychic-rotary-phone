package failureclass

import (
	"errors"
	"strings"
	"testing"
)

// TestTheDisciplineIsEightStages. The count is load-bearing: the three
// stages that get dropped are the last three, so a register that
// quietly became five stages would look complete and govern nothing.
func TestTheDisciplineIsEightStages(t *testing.T) {
	if got := len(Stages()); got != 8 {
		t.Fatalf("the discipline has %d stages, expected 8", got)
	}
	for _, s := range Stages() {
		if s.Meaning() == "" {
			t.Errorf("stage %s has no stated meaning, so nothing distinguishes it from its neighbour", s)
		}
	}
}

// TestEveryStageAsksADistinctQuestion.
func TestEveryStageAsksADistinctQuestion(t *testing.T) {
	seen := map[string]Stage{}
	for _, s := range Stages() {
		m := s.Meaning()
		if prev, dup := seen[m]; dup {
			t.Errorf("%s and %s state the same meaning", prev, s)
		}
		seen[m] = s
	}
}

// TestEveryClosedFindingCompletesTheDiscipline is the whole point.
func TestEveryClosedFindingCompletesTheDiscipline(t *testing.T) {
	r, err := NewRegister(Closed...)
	if err != nil {
		t.Fatalf("NewRegister: %v", err)
	}
	for _, e := range r.All() {
		if m := e.Missing(); len(m) > 0 {
			t.Errorf("%s stops at %s", e.ID, e.LastCompleteStage())
		}
	}
}

// TestAResponseThatStopsAtThePositiveTestIsRefused. This is the shape
// of a patch: the site is fixed, a test shows it working, and nothing
// attacks it.
func TestAResponseThatStopsAtThePositiveTestIsRefused(t *testing.T) {
	e := Closed[0]
	e.MutationTest = ""
	e.RegressionTest = ""
	err := e.Validate()
	if !errors.Is(err, ErrStageMissing) {
		t.Fatalf("a patch was accepted as a closure: %v", err)
	}
	if e.LastCompleteStage() != StageNegative {
		t.Fatalf("the truncated entry reports %s as its last stage, expected %s",
			e.LastCompleteStage(), StageNegative)
	}
}

// TestTheFourTestsMustBeDistinct. Citing the same test in two stages is
// the cheapest way to make a chain look complete.
func TestTheFourTestsMustBeDistinct(t *testing.T) {
	e := Closed[0]
	e.MutationTest = e.PositiveTest
	if err := e.Validate(); !errors.Is(err, ErrTestsNotDistinct) {
		t.Fatalf("one test was accepted in two stages: %v", err)
	}
}

// TestAnInvariantMustBeViolable. "The pipeline verifies derivatives" is
// a description; nothing can fail it.
func TestAnInvariantMustBeViolable(t *testing.T) {
	e := Closed[0]
	e.Invariant = "the pipeline handles findings carefully"
	if err := e.Validate(); !errors.Is(err, ErrInvariantNotARule) {
		t.Fatalf("a description was accepted as an invariant: %v", err)
	}
}

// TestAnUndeclaredClassIsRefused. A new class must be named in Classes
// before it can be used, so the set of shapes stays enumerable.
func TestAnUndeclaredClassIsRefused(t *testing.T) {
	e := Closed[0]
	e.Class = Class("SOMETHING_NEW")
	if err := e.Validate(); !errors.Is(err, ErrUnknownClass) {
		t.Fatalf("an undeclared class was accepted: %v", err)
	}
}

// TestDuplicateIDsAreRefused.
func TestDuplicateIDsAreRefused(t *testing.T) {
	if _, err := NewRegister(Closed[0], Closed[0]); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("a duplicate entry was accepted: %v", err)
	}
}

// TestEveryDeclaredClassHasBeenMet. A declared class with no closed
// finding is a shape we named and never encountered, which is worth
// knowing and is not a defect -- so this test reports rather than
// fails, except that the register must not be mostly aspiration.
func TestEveryDeclaredClassHasBeenMet(t *testing.T) {
	r, err := NewRegister(Closed...)
	if err != nil {
		t.Fatalf("NewRegister: %v", err)
	}
	uncovered := r.UncoveredClasses()
	if len(uncovered) > 0 {
		t.Logf("declared but not yet met: %v", uncovered)
	}
	if len(uncovered)*2 > len(Classes()) {
		t.Fatalf("%d of %d declared classes have no closed finding: the register is a "+
			"taxonomy rather than a history", len(uncovered), len(Classes()))
	}
}

// TestTheRegisterIsNotVacuous.
func TestTheRegisterIsNotVacuous(t *testing.T) {
	r, err := NewRegister(Closed...)
	if err != nil {
		t.Fatalf("NewRegister: %v", err)
	}
	if len(r.All()) < 5 {
		t.Fatalf("the register holds %d entries; it is not carrying the findings this "+
			"repository has actually closed", len(r.All()))
	}
	if len(r.CitedTests()) != 4*len(r.All()) {
		t.Fatalf("%d distinct tests cited for %d entries: some entries share tests across "+
			"chains, which weakens both", len(r.CitedTests()), len(r.All()))
	}
}

// TestTheReportShowsTheWholeChain.
func TestTheReportShowsTheWholeChain(t *testing.T) {
	r, err := NewRegister(Closed...)
	if err != nil {
		t.Fatalf("NewRegister: %v", err)
	}
	rep := r.Report()
	for _, e := range r.All() {
		if !strings.Contains(rep, e.ID) {
			t.Errorf("the report omits %s", e.ID)
		}
		for _, name := range e.Tests() {
			if !strings.Contains(rep, name) {
				t.Errorf("the report omits %s cited by %s", name, e.ID)
			}
		}
	}
	if !strings.Contains(rep, "a response that stops early is a patch") {
		t.Error("the report does not state why the last three stages exist")
	}
}
