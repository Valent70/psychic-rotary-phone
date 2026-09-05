package freeze

import (
	"errors"
	"strings"
	"testing"
)

// TestAWarrantThatNamesNoGateIsTheEvasionThisRegisterCatches.
//
// "EXTERNAL_GATE" with an empty Serves is how a freeze is routed
// around: the work gets a warrant-shaped label and no gate. It has to
// fail at construction, not at review.
func TestAWarrantThatNamesNoGateIsTheEvasionThisRegisterCatches(t *testing.T) {
	for _, w := range []Warrant{ExternalGate, CustomerPilot} {
		_, err := NewRegister(6, Item{
			Name: "something desirable", Warrant: w,
			Why: "it would help", Built: true,
		})
		if !errors.Is(err, ErrVagueWarrant) {
			t.Errorf("%s with no named gate was accepted: %v", w, err)
		}
	}
}

// TestDiscretionaryWorkThatWasBuiltAnywayIsARegisterOfAFreezeNobodyApplied.
func TestDiscretionaryWorkThatWasBuiltAnywayIsARegisterOfAFreezeNobodyApplied(t *testing.T) {
	_, err := NewRegister(6, Item{
		Name: "the interesting refactor", Warrant: Discretionary,
		Why: "it is good work", Built: true,
	})
	if err == nil {
		t.Fatal("discretionary work was recorded as built; the freeze was declared and " +
			"not applied, and nothing caught it")
	}
	if !strings.Contains(err.Error(), "not applied") {
		t.Errorf("the refusal does not name what happened: %v", err)
	}
}

// TestFixingABugIsAlwaysPermitted.
//
// A freeze that forbids fixing a defect will be ignored, and a rule
// that is ignored constrains nothing. This is the deliberate hole.
func TestFixingABugIsAlwaysPermitted(t *testing.T) {
	r, err := NewRegister(6, Item{
		Name: "fix the off-by-one in the interval check", Warrant: Correctness,
		Why: "it is wrong", Built: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Permitted()) != 1 {
		t.Fatal("a correctness fix was not permitted")
	}
}

// TestTheFreezeBeginsAtRoundSix.
func TestTheFreezeBeginsAtRoundSix(t *testing.T) {
	if _, err := NewRegister(5); err == nil {
		t.Fatal("a Round 5 freeze register was accepted; the freeze did not exist then")
	}
}

// TestRound6ObeyedItsOwnFreeze.
//
// The first test of a rule is whether the round that declared it kept
// it. Every permitted item must name a gate, a pilot, or be a fix.
func TestRound6ObeyedItsOwnFreeze(t *testing.T) {
	r, err := Round6()
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range r.Permitted() {
		if i.Warrant == Correctness {
			continue
		}
		if strings.TrimSpace(i.Serves) == "" {
			t.Errorf("%q was permitted and names nothing it serves", i.Name)
		}
		if len(strings.Fields(i.Why)) < 12 {
			t.Errorf("%q justifies itself in %d words; a dependency needs describing, "+
				"not asserting", i.Name, len(strings.Fields(i.Why)))
		}
	}
}

// TestRound6RefusedSomething.
//
// A freeze register containing only approvals is a record of a freeze
// that was not applied. This test is the one that would have caught
// Round 6 writing itself a blank cheque.
func TestRound6RefusedSomething(t *testing.T) {
	r, err := Round6()
	if err != nil {
		t.Fatal(err)
	}
	ref := r.Refused()
	if len(ref) == 0 {
		t.Fatal("Round 6 refused nothing under its own freeze, which means the freeze " +
			"permitted everything Round 6 wanted to do")
	}
	for _, i := range ref {
		if i.Built {
			t.Errorf("%q is refused and marked built", i.Name)
		}
		if !strings.Contains(strings.ToUpper(i.Why), "REFUSED") {
			t.Errorf("%q does not say it was refused, in the entry a reader will scan", i.Name)
		}
	}
}

// TestTheRefusedWorkIncludesSomethingWorthDoing.
//
// A freeze is only evidence of discipline if it stopped something the
// team wanted. Refusing only obviously bad ideas is not a constraint.
func TestTheRefusedWorkIncludesSomethingWorthDoing(t *testing.T) {
	r, err := Round6()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, i := range r.Refused() {
		w := strings.ToLower(i.Why)
		if strings.Contains(w, "interesting") || strings.Contains(w, "wanted") ||
			strings.Contains(w, "most") {
			found = true
		}
	}
	if !found {
		t.Error("nothing in the refused list is described as worth doing, so the " +
			"register does not show the freeze costing anything")
	}
}

// TestARegisterCannotContainTheSameWorkTwice.
func TestARegisterCannotContainTheSameWorkTwice(t *testing.T) {
	i := Item{Name: "x", Warrant: Correctness, Why: "y", Built: true}
	if _, err := NewRegister(6, i, i); err == nil {
		t.Fatal("the same item was recorded twice")
	}
}

// TestTheReportShowsBothColumns.
func TestTheReportShowsBothColumns(t *testing.T) {
	r, err := Round6()
	if err != nil {
		t.Fatal(err)
	}
	rep := r.Report()
	if !strings.Contains(rep, "PERMITTED") || !strings.Contains(rep, "REFUSED UNDER THE FREEZE") {
		t.Error("the report omits a column")
	}
	if r.Report() != r.Report() {
		t.Error("Report() is not deterministic")
	}
	for _, line := range strings.Split(rep, "\n") {
		if len([]rune(line)) > 78 {
			t.Errorf("a %d-column line will wrap: %q", len([]rune(line)), line)
		}
	}
}

// TestAnEmptyRefusalListIsCalledOutInTheReport.
func TestAnEmptyRefusalListIsCalledOutInTheReport(t *testing.T) {
	r, err := NewRegister(6, Item{
		Name: "a fix", Warrant: Correctness, Why: "it is wrong", Built: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Report(), "declared and not applied") {
		t.Error("a register with no refusals does not say so; it reads as a disciplined round")
	}
}
