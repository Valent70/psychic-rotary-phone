package readiness

import (
	"errors"
	"strings"
	"testing"
)

func plan(t *testing.T) *Plan {
	t.Helper()
	p, err := VeriqoPlan()
	if err != nil {
		t.Fatalf("the plan does not build: %v", err)
	}
	return p
}

// TestEveryBlockerIsBuyable.
//
// The difference between a register and a schedule: each blocker names
// the party, what they must hand back, how long, and roughly what it
// costs.
func TestEveryBlockerIsBuyable(t *testing.T) {
	for _, b := range plan(t).All() {
		if err := b.Validate(); err != nil {
			t.Fatalf("%s: %v", b.ID, err)
		}
		if len(strings.Fields(b.ExpectedEvidence)) < 8 {
			t.Fatalf("%s describes the deliverable too briefly for a statement of work: %q",
				b.ID, b.ExpectedEvidence)
		}
		if b.Validator.External() && strings.TrimSpace(b.ValidatorQualification) == "" &&
			b.Validator != DataPartner && b.Validator != CorpusPartner &&
			b.Validator != EvaluationPartner && b.Validator != TimestampAuthority {
			t.Fatalf("%s names validator type %s with no qualification; an unqualified "+
				"category is not a supplier", b.ID, b.Validator)
		}
	}
}

// TestExpectedEvidenceIsStatedInAdvance.
//
// It is what stops a report arriving that does not answer the
// question -- the commonest way an expensive engagement produces
// nothing usable.
func TestExpectedEvidenceIsStatedInAdvance(t *testing.T) {
	b := Blocker{ID: "X", Dimension: Security, Owner: "someone",
		Validator: SecurityAssessor, LeadTime: "weeks", Cost: CostHigh}
	if err := b.Validate(); !errors.Is(err, ErrNoEvidence) {
		t.Fatalf("a blocker with no expected evidence validated: %v", err)
	}
	if !strings.Contains(err(t, b), "does not answer the question") {
		t.Fatal("the refusal does not say why it matters")
	}
	// The pentest must demand a retest: a report with no retest
	// establishes that findings existed, not that they were fixed.
	for _, bl := range plan(t).All() {
		if bl.ID != "B-PENTEST" {
			continue
		}
		if !strings.Contains(bl.ExpectedEvidence, "retest") {
			t.Fatalf("the pentest does not demand a retest: %q", bl.ExpectedEvidence)
		}
		return
	}
	t.Fatal("no pentest blocker exists")
}

func err(t *testing.T, b Blocker) string {
	t.Helper()
	e := b.Validate()
	if e == nil {
		return ""
	}
	return e.Error()
}

// TestSecurityAndCryptographyAreDifferentSpecialists.
//
// Conflating them is how a cryptographic review becomes a network
// scan, and the register would look satisfied either way.
func TestSecurityAndCryptographyAreDifferentSpecialists(t *testing.T) {
	byV := plan(t).ByValidator()
	if len(byV[SecurityAssessor]) == 0 || len(byV[Cryptographer]) == 0 {
		t.Fatal("the plan does not distinguish an assessor from a cryptographer")
	}
	if len(byV[RedTeam]) == 0 {
		t.Fatal("the plan does not distinguish a red team from a penetration tester")
	}
	for _, b := range byV[Cryptographer] {
		if !strings.Contains(b.ValidatorQualification, "different specialist") {
			t.Fatalf("%s does not state that it needs a different specialist", b.ID)
		}
	}
}

// TestSecurityEngagementsDependOnTheFreeze.
//
// A report against code that no longer exists is a report about
// nothing.
func TestSecurityEngagementsDependOnTheFreeze(t *testing.T) {
	byID := map[string]Blocker{}
	for _, b := range plan(t).All() {
		byID[b.ID] = b
	}
	for _, id := range []string{"B-PENTEST", "B-CRYPTO", "B-REDTEAM", "B-SBOM"} {
		b, ok := byID[id]
		if !ok {
			t.Fatalf("%s does not exist", id)
		}
		var found bool
		for _, d := range b.DependsOn {
			if d == "B-FREEZE" {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s does not depend on the release-candidate freeze; it would assess "+
				"code that will change", id)
		}
	}
}

// TestTheCriticalPathBoundsTheSchedule.
func TestTheCriticalPathBoundsTheSchedule(t *testing.T) {
	p := plan(t)
	cp := p.CriticalPath()
	if len(cp) < 3 {
		t.Fatalf("the critical path is %d step(s): %v", len(cp), cp)
	}
	if cp[len(cp)-1] != "B-RELEASE" {
		t.Fatalf("the critical path does not end at the release decision: %v", cp)
	}
	if !strings.Contains(p.Report(), "cannot clear faster than") {
		t.Fatalf("the report does not state what the critical path bounds:\n%s", p.Report())
	}
}

// TestStartableNowIsTheMostUsefulList.
//
// A register otherwise only says what is not done; this answers "what
// can we do this week".
func TestStartableNowIsTheMostUsefulList(t *testing.T) {
	sn := plan(t).StartableNow()
	if len(sn) < 4 {
		t.Fatalf("only %d engagements could begin today", len(sn))
	}
	ids := map[string]bool{}
	for _, b := range sn {
		ids[b.ID] = true
		if !b.Startable() {
			t.Fatalf("%s is listed as startable and is not", b.ID)
		}
	}
	// Legal and the anchor have no dependencies and are cheap: if they
	// are not startable, the graph has been over-constrained.
	for _, want := range []string{"B-LEGAL-SG", "B-ANCHOR", "B-HSM"} {
		if !ids[want] {
			t.Fatalf("%s is not startable now, which over-constrains the schedule", want)
		}
	}
	// And the corpus is deliberately blocked on the legal position.
	for _, b := range plan(t).All() {
		if b.ID != "B-CORPUS" {
			continue
		}
		if b.Startable() {
			t.Fatal("handling a customer's real documents was made startable before the " +
				"legal position is settled")
		}
		if !strings.Contains(b.NotStartableBecause, "legal position") {
			t.Fatalf("B-CORPUS does not say why it cannot start: %q", b.NotStartableBecause)
		}
	}
}

// TestEveryBlockerMapsToAnEvidenceDebtOrIsInternal.
//
// A blocker with no debt behind it is work nobody recorded as
// outstanding.
func TestEveryBlockerMapsToAnEvidenceDebtOrIsInternal(t *testing.T) {
	for _, b := range plan(t).All() {
		if len(b.Debts) > 0 {
			for _, d := range b.Debts {
				if !strings.HasPrefix(d, "ED-") {
					t.Fatalf("%s cites %q, which is not an evidence debt", b.ID, d)
				}
			}
			continue
		}
		// Only the internal steps may have no debt.
		if b.Validator.External() {
			t.Fatalf("%s needs an outside party and cites no evidence debt", b.ID)
		}
	}
}

// TestTwoJurisdictionsAreTwoEngagements. An opinion for one says
// nothing about another.
func TestTwoJurisdictionsAreTwoEngagements(t *testing.T) {
	byV := plan(t).ByValidator()
	if len(byV[Counsel]) < 2 {
		t.Fatal("the plan treats legal review as one engagement across jurisdictions")
	}
	var qualifications []string
	for _, b := range byV[Counsel] {
		qualifications = append(qualifications, b.ValidatorQualification)
	}
	joined := strings.Join(qualifications, " ")
	if !strings.Contains(joined, "Singapore") || !strings.Contains(joined, "England") {
		t.Fatalf("the counsel engagements do not name their jurisdictions: %v", qualifications)
	}
}

// TestADependencyCycleIsRefused.
func TestADependencyCycleIsRefused(t *testing.T) {
	a := Blocker{ID: "A", Dimension: Security, Owner: "o", Validator: SecurityAssessor,
		ExpectedEvidence: "a report naming what was and was not assessed, with a retest",
		LeadTime:         "weeks", Cost: CostHigh, DependsOn: []string{"B"}}
	b := a
	b.ID = "B"
	b.DependsOn = []string{"A"}
	if _, err := NewPlan(a, b); !errors.Is(err, ErrDepCycle) {
		t.Fatalf("a dependency cycle was accepted: %v", err)
	}
	c := a
	c.DependsOn = []string{"DOES-NOT-EXIST"}
	if _, err := NewPlan(c); !errors.Is(err, ErrBadDep) {
		t.Fatalf("a dangling dependency was accepted: %v", err)
	}
}

// TestCostIsABandNotAFigure.
//
// A number would be wrong, would be quoted, and would be out of date.
func TestCostIsABandNotAFigure(t *testing.T) {
	r := plan(t).Report()
	for _, forbidden := range []string{"$", "USD", "EUR", "GBP"} {
		if strings.Contains(r, forbidden) {
			t.Fatalf("the plan quotes a currency (%q); a band cannot be mistaken for a "+
				"quotation and a figure can", forbidden)
		}
	}
	for _, b := range plan(t).All() {
		if !b.Cost.Valid() {
			t.Fatalf("%s has cost class %q", b.ID, b.Cost)
		}
	}
}
