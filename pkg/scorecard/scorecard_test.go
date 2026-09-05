package scorecard

import (
	"errors"
	"strings"
	"testing"
	"time"

	"veriqo/pkg/gates"
)

var now = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

func emptyGates(t *testing.T) *gates.Register {
	t.Helper()
	g, err := gates.NewRegister(gates.Veriqo())
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func allGreen() []Assessment {
	var out []Assessment
	for _, d := range Dimensions() {
		a := Assessment{Dimension: d, Rating: Green, Basis: "it works"}
		if d.RequiresExternalToBeGreen() {
			a.ExternallyQualified = true
			a.QualifiedBy = "an outside firm"
		}
		out = append(out, a)
	}
	return out
}

// TestSecurityAndExternalValidationCannotBeGreenWithoutAnOutsideParty.
//
// A security posture rated GREEN by the party it protects is a
// self-assessment, and EXTERNAL_VALIDATION rated GREEN with nobody
// external is a contradiction in terms.
func TestSecurityAndExternalValidationCannotBeSelfGreen(t *testing.T) {
	for _, d := range []Dimension{Security, ExternalValidation} {
		a := Assessment{Dimension: d, Rating: Green, Basis: "our tests pass"}
		if err := a.Validate(); !errors.Is(err, ErrGreenWithoutExternal) {
			t.Errorf("%s was rated GREEN by VERIQO: %v", d, err)
		}
	}
	// And a claim of external qualification naming VERIQO is refused.
	a := Assessment{Dimension: Security, Rating: Green, Basis: "b",
		ExternallyQualified: true, QualifiedBy: "the VERIQO security team"}
	if err := a.Validate(); !errors.Is(err, ErrGreenWithoutExternal) {
		t.Fatalf("VERIQO qualified itself externally: %v", err)
	}
}

// TestAnUnratedDimensionBlocks. A dimension nobody assessed is not one
// that passed.
func TestAnUnratedDimensionBlocks(t *testing.T) {
	if !Unrated.Blocking() {
		t.Fatal("UNRATED does not block")
	}
	s, err := New(emptyGates(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Unrated()) != len(Dimensions()) {
		t.Fatalf("%d unrated of %d", len(s.Unrated()), len(Dimensions()))
	}
	ok, reasons := s.ReleasePermitted()
	if ok {
		t.Fatal("a scorecard with nothing assessed permitted a release")
	}
	if len(reasons) != len(Dimensions()) {
		t.Fatalf("%d reasons for %d unrated dimensions", len(reasons), len(Dimensions()))
	}
}

// TestOneRedBlocksARelease, whatever the others say.
func TestOneRedBlocksARelease(t *testing.T) {
	as := allGreen()
	as[0] = Assessment{Dimension: as[0].Dimension, Rating: Red,
		Basis: "the custody chain is not verified on read"}
	s, err := New(emptyGates(t), nil, as...)
	if err != nil {
		t.Fatal(err)
	}
	ok, reasons := s.ReleasePermitted()
	if ok {
		t.Fatal("a RED dimension did not block a release")
	}
	if len(reasons) != 1 || !strings.Contains(reasons[0], "is RED") {
		t.Fatalf("reasons = %v", reasons)
	}
}

// TestAMandatoryGateBlocksEvenWithEveryDimensionGreen.
//
// The release rule is a conjunction: no RED AND every mandatory gate
// satisfied. Colours alone cannot buy a release.
func TestAMandatoryGateBlocksEvenWithEveryDimensionGreen(t *testing.T) {
	s, err := New(emptyGates(t), []string{"G1", "G7"}, allGreen()...)
	if err != nil {
		t.Fatal(err)
	}
	ok, reasons := s.ReleasePermitted()
	if ok {
		t.Fatal("AN ALL-GREEN SCORECARD RELEASED WITH UNSATISFIED MANDATORY GATES")
	}
	if len(reasons) != 2 {
		t.Fatalf("reasons = %v", reasons)
	}
	for _, r := range reasons {
		if !strings.Contains(r, "mandatory gate") {
			t.Errorf("unexpected reason: %s", r)
		}
	}
}

// TestAllGreenAndAllGatesSatisfiedPermitsARelease, or the rule refuses
// everything and proves nothing.
func TestAllGreenAndAllGatesSatisfiedPermitsARelease(t *testing.T) {
	g := emptyGates(t)
	ev := gates.Evidence{Description: "a report", Ref: "r", At: now,
		ProducedBy: "an outside firm", External: true}
	for _, id := range []string{"G1", "G7"} {
		if err := g.Set(id, gates.Satisfied, "done", ev); err != nil {
			t.Fatal(err)
		}
	}
	s, err := New(g, []string{"G1", "G7"}, allGreen()...)
	if err != nil {
		t.Fatal(err)
	}
	ok, reasons := s.ReleasePermitted()
	if !ok {
		t.Fatalf("a fully satisfied scorecard was refused: %v", reasons)
	}
	if !strings.Contains(s.Report(), "RELEASE PERMITTED") {
		t.Fatalf("the report does not say so:\n%s", s.Report())
	}
}

// TestAYellowMustStateItsGap. A rating nobody can act on is a colour.
func TestAYellowMustStateItsGap(t *testing.T) {
	a := Assessment{Dimension: EvidenceIntegrity, Rating: Yellow, Basis: "it mostly works"}
	if err := a.Validate(); err == nil {
		t.Fatal("a YELLOW with no gap was accepted")
	}
	a.Gaps = []string{"no external corpus"}
	if err := a.Validate(); err != nil {
		t.Fatalf("a YELLOW with a gap was refused: %v", err)
	}
}

// TestARatingMustStateItsBasis.
func TestARatingMustStateItsBasis(t *testing.T) {
	a := Assessment{Dimension: EvidenceIntegrity, Rating: Red}
	if err := a.Validate(); !errors.Is(err, ErrNoBasis) {
		t.Fatalf("a colour with no basis was accepted: %v", err)
	}
}

// TestThereIsNoAggregateScore.
func TestThereIsNoAggregateScore(t *testing.T) {
	s, err := New(emptyGates(t), nil, allGreen()...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.Report(), "There is no aggregate score") {
		t.Fatalf("the report does not state the rule:\n%s", s.Report())
	}
	// The only aggregation is the release rule, which is a
	// conjunction rather than an average: a single RED cannot be
	// outweighed by eight GREENs.
	as := allGreen()
	as[3] = Assessment{Dimension: as[3].Dimension, Rating: Red, Basis: "unattested key root"}
	bad, _ := New(emptyGates(t), nil, as...)
	if ok, _ := bad.ReleasePermitted(); ok {
		t.Fatal("eight GREENs outweighed one RED")
	}
}

// TestTheCurrentScorecardIsHonest.
//
// Nothing is GREEN, three are RED, and the release is refused. A
// scorecard showing otherwise for a system that has never met an
// outside party would be the artefact this package exists to prevent.
func TestTheCurrentScorecardIsHonest(t *testing.T) {
	s, err := Veriqo()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range s.Assessments() {
		if a.Rating == Green {
			t.Errorf("%s is GREEN in a system no outside party has examined", a.Dimension)
		}
		if a.Rating == Unrated {
			t.Errorf("%s was not assessed", a.Dimension)
		}
	}
	red := s.Red()
	if len(red) != 3 {
		t.Fatalf("%d dimensions are RED: %v", len(red), red)
	}
	found := map[Dimension]bool{}
	for _, d := range red {
		found[d] = true
	}
	for _, want := range []Dimension{Security, OperationalReliability, ExternalValidation} {
		if !found[want] {
			t.Errorf("%s is not RED", want)
		}
	}
	ok, reasons := s.ReleasePermitted()
	if ok {
		t.Fatal("THIS REPOSITORY PERMITS ITS OWN PRODUCTION RELEASE")
	}
	if len(reasons) < 9 {
		t.Fatalf("only %d reasons: %v", len(reasons), reasons)
	}
}

// TestEveryDimensionHasAQuestion, so the scorecard is readable by
// somebody who has not seen the source.
func TestEveryDimensionHasAQuestion(t *testing.T) {
	for _, d := range Dimensions() {
		if d.Question() == "" {
			t.Errorf("%s asks nothing", d)
		}
	}
	if len(Dimensions()) != 9 {
		t.Fatalf("%d dimensions; the specification names nine", len(Dimensions()))
	}
}

// TestAScorecardWithoutGatesIsRefused. It would assess opinions.
func TestAScorecardWithoutGatesIsRefused(t *testing.T) {
	if _, err := New(nil, nil); err == nil {
		t.Fatal("a scorecard with no gate register was built")
	}
	if _, err := New(emptyGates(t), []string{"G99"}); err == nil {
		t.Fatal("a mandatory gate that does not exist was accepted")
	}
}
