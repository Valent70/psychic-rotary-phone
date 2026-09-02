package reverseproof

import (
	"errors"
	"strings"
	"testing"
)

func claim() Claim {
	return Claim{
		ID: "CLAIM-1", Description: "vessel deviated intentionally to an unauthorized location",
		Conditions: []Condition{
			{ID: "COND-1", Description: "vessel was physically at that location", Material: true},
			{ID: "COND-2", Description: "the deviation was not weather-forced", Material: true},
			{ID: "COND-3", Description: "the master had no authorization", Material: false},
		},
	}
}

func req(id, cond string, st Status) Requirement {
	return Requirement{
		ID: id, ConditionID: cond,
		Description:        "evidence for " + cond,
		ExpectedIfTrue:     "shows presence at location",
		ContradictsIfShows: "shows presence elsewhere in the same window",
		Status:             st, DiagnosticValue: 0.5,
	}
}

func TestBuildRequiresConditionsAndRequirements(t *testing.T) {
	if _, err := Build(Claim{ID: "C", Description: "d"}, []Requirement{req("R", "", Obtained)}, nil, 1); !errors.Is(err, ErrNoConditions) {
		t.Fatalf("expected ErrNoConditions, got %v", err)
	}
	if _, err := Build(claim(), nil, nil, 1); !errors.Is(err, ErrNoRequirements) {
		t.Fatalf("expected ErrNoRequirements, got %v", err)
	}
	if _, err := Build(Claim{}, []Requirement{req("R", "", Obtained)}, nil, 1); !errors.Is(err, ErrEmptyClaim) {
		t.Fatalf("expected ErrEmptyClaim, got %v", err)
	}
}

func TestBuildRejectsDuplicateRequirementID(t *testing.T) {
	_, err := Build(claim(), []Requirement{req("R-1", "COND-1", Obtained), req("R-1", "COND-2", Obtained)}, nil, 1)
	if !errors.Is(err, ErrDuplicateRequirement) {
		t.Fatalf("expected ErrDuplicateRequirement, got %v", err)
	}
}

func TestBuildRejectsRequirementReferencingUnknownCondition(t *testing.T) {
	_, err := Build(claim(), []Requirement{req("R-1", "COND-NOPE", Obtained)}, nil, 1)
	if err == nil || !strings.Contains(err.Error(), "unknown condition") {
		t.Fatalf("expected an unknown-condition error, got %v", err)
	}
}

// TestUnattemptedIsTheZeroStatus proves a requirement added but never
// tracked reads as an open gap, not as satisfied.
func TestUnattemptedIsTheZeroStatus(t *testing.T) {
	var r Requirement
	if r.Status != "" && r.Status != Unattempted {
		t.Fatalf("the zero Status should behave as Unattempted, got %q", r.Status)
	}
	rs, err := Build(claim(), []Requirement{{ID: "R-1", ConditionID: "COND-1", ContradictsIfShows: "x"}}, nil, 1)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	g := Analyze(rs, map[string]bool{"COND-1": true})
	if len(g.Unattempted) != 1 {
		t.Fatalf("an untracked requirement must appear as unattempted, got %+v", g)
	}
	if g.Complete {
		t.Fatal("a set with an unattempted material requirement is not complete")
	}
}

// TestGapDistinguishesObservedAbsentFromUnattempted is the core
// epistemic distinction: "we looked and it wasn't there" is a result;
// "we never looked" is a gap. A single "missing" bucket would conflate
// them.
func TestGapDistinguishesObservedAbsentFromUnattempted(t *testing.T) {
	rs, err := Build(claim(), []Requirement{
		req("R-1", "COND-1", ObservedAbsent),
		req("R-2", "COND-2", Unattempted),
	}, nil, 1)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	g := Analyze(rs, map[string]bool{"COND-1": true, "COND-2": true})

	if len(g.ObservedAbsent) != 1 || g.ObservedAbsent[0] != "R-1" {
		t.Fatalf("R-1 should be observed-absent, got %+v", g.ObservedAbsent)
	}
	if len(g.Unattempted) != 1 || g.Unattempted[0] != "R-2" {
		t.Fatalf("R-2 should be unattempted, got %+v", g.Unattempted)
	}
	if g.Complete {
		t.Fatal("an unattempted material requirement must block completeness")
	}
}

// TestObservedAbsentCountsAsResolved proves a properly-gated absence
// closes a requirement rather than leaving it open forever.
func TestObservedAbsentCountsAsResolved(t *testing.T) {
	rs, err := Build(claim(), []Requirement{
		req("R-1", "COND-1", ObservedAbsent),
		req("R-2", "COND-2", Obtained),
	}, nil, 1)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	g := Analyze(rs, map[string]bool{"COND-1": true, "COND-2": true})
	if !g.Complete {
		t.Fatalf("observed-absent plus obtained should complete the material set: %s", g.Reason)
	}
}

// TestImmaterialGapsDoNotBlockCompleteness proves the analysis is
// calibrated: only conditions the claim turns on are blocking.
func TestImmaterialGapsDoNotBlockCompleteness(t *testing.T) {
	rs, err := Build(claim(), []Requirement{
		req("R-1", "COND-1", Obtained),
		req("R-3", "COND-3", Unattempted), // COND-3 is immaterial
	}, nil, 1)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	g := Analyze(rs, map[string]bool{"COND-1": true}) // only COND-1 material
	if !g.Complete {
		t.Fatalf("an immaterial gap must not block completeness: %s", g.Reason)
	}
	if len(g.Unattempted) != 1 {
		t.Fatal("the immaterial gap should still be reported, just not blocking")
	}
}

// TestUntestedAlternativeBlocksCompleteness is the anti-confirmation
// rule: a reverse proof that never evaluated a rival explanation has
// not been done.
func TestUntestedAlternativeBlocksCompleteness(t *testing.T) {
	rs, err := Build(claim(),
		[]Requirement{req("R-1", "COND-1", Obtained), req("R-2", "COND-2", Obtained)},
		[]AlternativeHypothesis{{ID: "ALT-1", Description: "engine failure forced the diversion", Tested: false}},
		1)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	g := Analyze(rs, map[string]bool{"COND-1": true, "COND-2": true})
	if g.Complete {
		t.Fatal("an untested alternative must block completeness")
	}
	if len(g.UntestedAlternatives) != 1 || g.UntestedAlternatives[0] != "ALT-1" {
		t.Fatalf("the untested alternative must be named, got %+v", g.UntestedAlternatives)
	}
}

func TestTestedAlternativeAllowsCompleteness(t *testing.T) {
	rs, err := Build(claim(),
		[]Requirement{req("R-1", "COND-1", Obtained), req("R-2", "COND-2", Obtained)},
		[]AlternativeHypothesis{{ID: "ALT-1", Description: "engine failure", Tested: true}},
		1)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if g := Analyze(rs, map[string]bool{"COND-1": true, "COND-2": true}); !g.Complete {
		t.Fatalf("expected complete, got: %s", g.Reason)
	}
}

// TestRemainingDiagnosticValueAccumulates supports next-best-evidence
// prioritisation: how much uncertainty is still removable.
func TestRemainingDiagnosticValueAccumulates(t *testing.T) {
	r1 := req("R-1", "COND-1", Unattempted)
	r1.DiagnosticValue = 0.8
	r2 := req("R-2", "COND-2", Unattempted)
	r2.DiagnosticValue = 0.3
	r3 := req("R-3", "COND-3", Obtained)
	r3.DiagnosticValue = 0.9 // already obtained; contributes nothing remaining

	rs, err := Build(claim(), []Requirement{r1, r2, r3}, nil, 1)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	g := Analyze(rs, map[string]bool{})
	if g.RemainingDiagnosticValue < 1.09 || g.RemainingDiagnosticValue > 1.11 {
		t.Fatalf("expected remaining diagnostic value ~1.1, got %v", g.RemainingDiagnosticValue)
	}
}

// TestValidateFalsifiabilityRejectsUnfalsifiableRequirements is the
// confirmation-bias guard: a requirement that can only confirm is not
// a test of anything.
func TestValidateFalsifiabilityRejectsUnfalsifiableRequirements(t *testing.T) {
	weak := req("R-WEAK", "COND-1", Obtained)
	weak.ContradictsIfShows = ""
	rs, err := Build(claim(), []Requirement{weak}, nil, 1)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	err = ValidateFalsifiability(rs)
	if err == nil {
		t.Fatal("a requirement with no falsifying observation must be rejected")
	}
	if !strings.Contains(err.Error(), "R-WEAK") {
		t.Fatalf("the error must name the offending requirement, got %q", err)
	}
}

func TestValidateFalsifiabilityAcceptsWellFormedSet(t *testing.T) {
	rs, err := Build(claim(), []Requirement{req("R-1", "COND-1", Obtained)}, nil, 1)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := ValidateFalsifiability(rs); err != nil {
		t.Fatalf("expected falsifiable set to pass, got %v", err)
	}
}

func TestBuildSortsDeterministically(t *testing.T) {
	rs, err := Build(claim(), []Requirement{
		req("R-3", "COND-1", Obtained), req("R-1", "COND-1", Obtained), req("R-2", "COND-2", Obtained),
	}, []AlternativeHypothesis{{ID: "ALT-2"}, {ID: "ALT-1"}}, 1)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if rs.Requirements[0].ID != "R-1" || rs.Requirements[2].ID != "R-3" {
		t.Fatalf("requirements must be sorted, got %v", rs.Requirements)
	}
	if rs.Alternatives[0].ID != "ALT-1" {
		t.Fatalf("alternatives must be sorted, got %v", rs.Alternatives)
	}
}
