package observability

import (
	"errors"
	"testing"
)

// TestAllNineConditionsYieldObservedAbsent is the only path to the one
// state that carries evidential weight.
func TestAllNineConditionsYieldObservedAbsent(t *testing.T) {
	r, err := Evaluate(Assessment{
		Subject: "AIS transmission", SourceID: "ais-1",
		Conditions: AllConditionsMet(), Tick: 10,
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if r.State != ObservedAbsent {
		t.Fatalf("expected OBSERVED_ABSENT, got %s: %s", r.State, r.Reason)
	}
	if len(r.Unmet) != 0 || len(r.Met) != 9 {
		t.Fatalf("expected all nine met, got met=%d unmet=%d", len(r.Met), len(r.Unmet))
	}
}

// TestGateIsConjunctive proves eight of nine is not "nearly observed
// absent" -- it is not observed absent. Each condition is removed in
// turn.
func TestGateIsConjunctive(t *testing.T) {
	for _, drop := range GateConditions() {
		conds := AllConditionsMet()
		delete(conds, drop)
		r, err := Evaluate(Assessment{Subject: "x", Conditions: conds, Material: true})
		if err != nil {
			t.Fatalf("Evaluate dropping %s: %v", drop, err)
		}
		if r.State == ObservedAbsent {
			t.Fatalf("dropping %q still yielded OBSERVED_ABSENT; the gate is not conjunctive", drop)
		}
		if r.State.CarriesEvidentialWeight() {
			t.Fatalf("dropping %q yielded a weight-carrying state %s", drop, r.State)
		}
	}
}

// TestOnlyObservedAbsentCarriesWeight guards the core semantic.
func TestOnlyObservedAbsentCarriesWeight(t *testing.T) {
	weighted := 0
	for _, s := range States() {
		if s.CarriesEvidentialWeight() {
			weighted++
			if s != ObservedAbsent {
				t.Fatalf("state %s must not carry evidential weight", s)
			}
		}
	}
	if weighted != 1 {
		t.Fatalf("exactly one state may carry evidential weight, got %d", weighted)
	}
}

// TestNoAdequateSourceYieldsNotObservable is the "we were not
// receiving" case: the failure is about our coverage, not the world.
func TestNoAdequateSourceYieldsNotObservable(t *testing.T) {
	conds := AllConditionsMet()
	conds[AdequateSource] = false
	r, err := Evaluate(Assessment{Subject: "AIS", Conditions: conds})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if r.State != NotObservable {
		t.Fatalf("expected NOT_OBSERVABLE, got %s: %s", r.State, r.Reason)
	}
}

func TestSourceDownYieldsSourceUnavailable(t *testing.T) {
	conds := AllConditionsMet()
	conds[OperationalAvailability] = false
	r, _ := Evaluate(Assessment{Subject: "AIS", Conditions: conds})
	if r.State != SourceUnavailable {
		t.Fatalf("expected SOURCE_UNAVAILABLE, got %s", r.State)
	}
}

func TestIncompleteCoverageYieldsPartialCoverage(t *testing.T) {
	for _, c := range []Condition{KnownCoverage, CorrectTemporalScope, CorrectSpatialScope} {
		conds := AllConditionsMet()
		conds[c] = false
		r, _ := Evaluate(Assessment{Subject: "AIS", Conditions: conds})
		if r.State != PartialCoverage {
			t.Fatalf("dropping %s should yield PARTIAL_COVERAGE, got %s", c, r.State)
		}
	}
}

func TestInvalidQueryYieldsExpectedButNotTested(t *testing.T) {
	conds := AllConditionsMet()
	conds[ValidQuery] = false
	r, _ := Evaluate(Assessment{Subject: "AIS", Conditions: conds})
	if r.State != ExpectedButNotTested {
		t.Fatalf("expected EXPECTED_BUT_NOT_TESTED, got %s", r.State)
	}
}

// TestMaterialAssertionRequiresReview proves the ninth condition binds
// only where the assertion is material -- calibration, not absolutism.
func TestMaterialAssertionRequiresReview(t *testing.T) {
	conds := AllConditionsMet()
	conds[ReviewWhereMaterial] = false

	material, _ := Evaluate(Assessment{Subject: "x", Conditions: conds, Material: true})
	if material.State == ObservedAbsent {
		t.Fatal("an unreviewed material assertion must not reach OBSERVED_ABSENT")
	}
	if material.Reason == "" {
		t.Fatal("the refusal must state a reason")
	}
}

// TestMissingConditionIsTreatedAsUnmet proves silence is never assent:
// a condition absent from the map is not met.
func TestMissingConditionIsTreatedAsUnmet(t *testing.T) {
	r, err := Evaluate(Assessment{Subject: "x", Conditions: map[Condition]bool{AdequateSource: true}})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if r.State == ObservedAbsent {
		t.Fatal("a mostly-empty condition map must not yield OBSERVED_ABSENT")
	}
	if len(r.Unmet) != 8 {
		t.Fatalf("expected 8 unmet conditions, got %d", len(r.Unmet))
	}
}

// TestAssertObservedAbsentIsStrict covers the fail-loud form.
func TestAssertObservedAbsentIsStrict(t *testing.T) {
	conds := AllConditionsMet()
	conds[Integrity] = false
	_, err := AssertObservedAbsent(Assessment{Subject: "x", Conditions: conds})
	if !errors.Is(err, ErrGateNotMet) {
		t.Fatalf("expected ErrGateNotMet, got %v", err)
	}
}

func TestAssertObservedAbsentSucceedsWhenFullyGated(t *testing.T) {
	r, err := AssertObservedAbsent(Assessment{Subject: "x", Conditions: AllConditionsMet()})
	if err != nil {
		t.Fatalf("a fully gated assertion must succeed: %v", err)
	}
	if r.State != ObservedAbsent {
		t.Fatalf("expected OBSERVED_ABSENT, got %s", r.State)
	}
}

func TestEvaluateRejectsMalformedInput(t *testing.T) {
	if _, err := Evaluate(Assessment{Conditions: AllConditionsMet()}); !errors.Is(err, ErrEmptySubject) {
		t.Fatalf("expected ErrEmptySubject, got %v", err)
	}
	if _, err := Evaluate(Assessment{Subject: "x"}); !errors.Is(err, ErrNoAssessment) {
		t.Fatalf("expected ErrNoAssessment for a nil condition map, got %v", err)
	}
}

func TestParseState(t *testing.T) {
	for _, s := range States() {
		if got, err := ParseState(string(s)); err != nil || got != s {
			t.Fatalf("ParseState(%q) = %v, %v", s, got, err)
		}
	}
	if _, err := ParseState("PROBABLY_ABSENT"); !errors.Is(err, ErrUnknownState) {
		t.Fatalf("expected ErrUnknownState, got %v", err)
	}
}

// TestReasonAlwaysExplains guards report quality: every verdict, pass
// or fail, must say why.
func TestReasonAlwaysExplains(t *testing.T) {
	for _, drop := range append(GateConditions(), "") {
		conds := AllConditionsMet()
		if drop != "" {
			conds[drop] = false
		}
		r, err := Evaluate(Assessment{Subject: "x", Conditions: conds, Material: true})
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if r.Reason == "" {
			t.Fatalf("no reason given when dropping %q", drop)
		}
	}
}
