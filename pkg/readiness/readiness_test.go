package readiness

import (
	"errors"
	"strings"
	"testing"

	"veriqo/pkg/assurance/state"
)

func profile(t *testing.T) *Profile {
	t.Helper()
	p, err := Veriqo()
	if err != nil {
		t.Fatalf("the profile does not build: %v", err)
	}
	return p
}

// TestThereIsNoAggregateScore. The whole point of the package is the
// absence, so it is asserted rather than assumed.
func TestThereIsNoAggregateScore(t *testing.T) {
	p := profile(t)
	// Weakest returns a dimension, not a number.
	d, l := p.Weakest()
	if !d.Valid() {
		t.Fatalf("Weakest returned %q", d)
	}
	if l != NotStarted {
		t.Fatalf("the weakest dimension is at %s", l)
	}
	if strings.Contains(p.Report(), "%") {
		t.Fatal("a percentage reached the readiness report")
	}
	if !strings.Contains(p.Sentence(), "no single figure is offered") {
		t.Fatalf("the summary sentence does not say why: %s", p.Sentence())
	}
}

// TestADimensionRequiringAnOutsidePartyCannotBeSelfAssessed.
//
// "We believe we would pass a pentest" is a hope, not an assessment,
// and a model that recorded it as one would defeat the purpose.
func TestADimensionRequiringAnOutsidePartyCannotBeSelfAssessed(t *testing.T) {
	for _, d := range []Dimension{ProductionInfra, ExternalValidation} {
		a := Assessment{Dimension: d, Level: Substantial, Basis: "we are confident",
			AssessedBy: "VERIQO engineering", Blockers: []string{"none we can see"}}
		if err := a.Validate(); !errors.Is(err, ErrSelfAssessed) {
			t.Fatalf("%s was self-assessed at SUBSTANTIAL: %v", d, err)
		}
		// NOT_STARTED is the one honest internal answer.
		a.Level = NotStarted
		a.Blockers = nil
		if err := a.Validate(); err != nil {
			t.Fatalf("%s at NOT_STARTED was refused: %v", d, err)
		}
		// An external assessor may record more.
		a.Level = High
		a.External = true
		a.AssessedBy = "Acme Security"
		a.Blockers = []string{"the report's scope excluded the anchor"}
		if err := a.Validate(); err != nil {
			t.Fatalf("an external assessment was refused: %v", err)
		}
	}
}

// TestTheFirstThreeDimensionsAreSelfAssessable. A team can judge its
// own architecture; refusing that would make the model unusable.
func TestTheFirstThreeDimensionsAreSelfAssessable(t *testing.T) {
	want := map[Dimension]bool{Architecture: true, Semantics: true, Implementation: true,
		ProductionInfra: false, ExternalValidation: false}
	for d, w := range want {
		if d.SelfAssessable() != w {
			t.Fatalf("%s.SelfAssessable() = %v", d, d.SelfAssessable())
		}
	}
}

// TestEveryDimensionMustBeAssessed. An omitted dimension is one nobody
// looked at, not one that does not apply -- and omitting it is how the
// weakest axis disappears from a report.
func TestEveryDimensionMustBeAssessed(t *testing.T) {
	all := profile(t).All()
	if len(all) != 5 {
		t.Fatalf("%d dimensions", len(all))
	}
	if _, err := New(all[0], all[1]); err == nil {
		t.Fatal("a profile with three dimensions missing was accepted")
	}
	if _, err := New(append(all, all[0])...); err == nil {
		t.Fatal("a dimension assessed twice was accepted")
	}
}

// TestAnAssessmentShortOfReadyMustNameItsBlockers.
func TestAnAssessmentShortOfReadyMustNameItsBlockers(t *testing.T) {
	a := Assessment{Dimension: Architecture, Level: High, Basis: "it is good",
		AssessedBy: "VERIQO engineering"}
	if err := a.Validate(); err == nil {
		t.Fatal("a dimension short of READY named nothing standing in the way")
	}
	a.Blockers = []string{"no outside architect has reviewed it"}
	if err := a.Validate(); err != nil {
		t.Fatalf("a blocked assessment was refused: %v", err)
	}
	a.Basis = ""
	if err := a.Validate(); !errors.Is(err, ErrNoBasis) {
		t.Fatalf("an assessment with no basis validated: %v", err)
	}
}

// TestVeriqoIsNotExternallyTouchedAndSaysSo.
func TestVeriqoIsNotExternallyTouchedAndSaysSo(t *testing.T) {
	p := profile(t)
	if p.ExternallyTouched() {
		t.Fatal("a dimension claims an external assessment; if that is now true, the " +
			"assessor must be named and this test changed deliberately")
	}
	if !strings.Contains(p.Report(),
		"No dimension has been assessed by anybody outside the builder") {
		t.Fatalf("the report does not state the absence:\n%s", p.Report())
	}
	for _, a := range p.All() {
		if a.MaxAssuranceState > state.InternallyAssured {
			t.Fatalf("%s reports a control above INTERNALLY_ASSURED", a.Dimension)
		}
	}
}

// TestTheTwoExternalDimensionsAreNotStarted. This is the substantive
// claim the profile makes about VERIQO, and it should fail loudly the
// moment it changes.
func TestTheTwoExternalDimensionsAreNotStarted(t *testing.T) {
	p := profile(t)
	byDim := map[Dimension]Assessment{}
	for _, a := range p.All() {
		byDim[a.Dimension] = a
	}
	for _, d := range []Dimension{ProductionInfra, ExternalValidation} {
		if byDim[d].Level != NotStarted {
			t.Fatalf("%s is at %s", d, byDim[d].Level)
		}
	}
	blocked := p.Blocked()
	if len(blocked) != 5 {
		t.Fatalf("%d of 5 dimensions are short of READY", len(blocked))
	}
	// Every blocker on the external dimensions cites an evidence debt,
	// so the readiness model and the register cannot drift apart.
	for _, d := range []Dimension{ProductionInfra, ExternalValidation} {
		for _, b := range blocked[d] {
			if !strings.Contains(b, "ED-") {
				t.Fatalf("%s blocker %q cites no evidence debt", d, b)
			}
		}
	}
}

// TestTheSummarySentenceIsGeneratedNotWritten. A hand-written summary
// drifts away from the assessments; a generated one cannot.
func TestTheSummarySentenceIsGeneratedNotWritten(t *testing.T) {
	s := profile(t).Sentence()
	lower := strings.ToLower(s)
	for _, want := range []string{"architecture high", "implementation substantial",
		"production infra not started", "external validation not started"} {
		if !strings.Contains(lower, want) {
			t.Fatalf("the sentence omits %q: %s", want, s)
		}
	}
	if strings.Contains(strings.ToLower(s), "ready for production") {
		t.Fatalf("the sentence claims production readiness: %s", s)
	}
}

// TestTheScaleHasNoMidpointToSplit. A five-point numeric scale invites
// arithmetic; four named states that mean different kinds of thing do
// not.
func TestTheScaleHasNoMidpointToSplit(t *testing.T) {
	if len(Levels()) != 5 {
		t.Fatalf("%d levels", len(Levels()))
	}
	if NotStarted != 0 {
		t.Fatal("the zero level is not NOT_STARTED; an unpopulated assessment would " +
			"default to something better than nothing")
	}
	for _, l := range Levels() {
		if !l.Valid() || strings.HasPrefix(l.String(), "Level(") {
			t.Fatalf("%d has no name", int(l))
		}
	}
}
