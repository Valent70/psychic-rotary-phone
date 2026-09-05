package dashboard

import (
	"errors"
	"strings"
	"testing"
)

// TestTheTestCountMayNotBeTheHeadline.
//
// The whole reason this package exists. "37 passed" is true, cheap to
// raise, and answers a question nobody outside engineering asked.
func TestTheTestCountMayNotBeTheHeadline(t *testing.T) {
	for _, name := range []string{"tests passed", "Tests Passed", "  TEST COUNT  ",
		"verify.sh checks passed", "gates satisfied"} {
		if err := IsHeadline(name); !errors.Is(err, ErrNotHeadline) {
			t.Errorf("%q was accepted as a headline measure", name)
		}
	}
}

// TestEveryBannedMeasureSaysWhyItIsBanned.
//
// Without the reason, the ban reads as arbitrary and the next person
// removes it.
func TestEveryBannedMeasureSaysWhyItIsBanned(t *testing.T) {
	for name, why := range Banned {
		if len(strings.Fields(why)) < 6 {
			t.Errorf("%q is banned in %d words, which is an assertion not an argument",
				name, len(strings.Fields(why)))
		}
	}
}

// TestACountedMeasureWithNoDenominatorIsRefused.
//
// "12 documents processed" is unreadable. Out of how many, of what?
func TestACountedMeasureWithNoDenominatorIsRefused(t *testing.T) {
	m := Measure{Name: "documents processed", State: Counted, Numerator: 12,
		Scope: "s", MovableBy: "a partner"}
	if err := m.Validate(); err == nil {
		t.Fatal("a bare numerator was accepted")
	}
}

// TestNotMeasuredMayNotCarryFigures.
//
// NOT MEASURED with a 0/0 beside it reads as a measurement of zero,
// which is the distinction this board exists to preserve.
func TestNotMeasuredMayNotCarryFigures(t *testing.T) {
	m := Measure{Name: "corpus coverage", State: NotMeasured, Numerator: 0, Denominator: 10,
		Scope: "s", MovableBy: "a partner"}
	if err := m.Validate(); err == nil {
		t.Fatal("a NOT_MEASURED measure carried a denominator")
	}
}

// TestZeroAndNotMeasuredRenderDifferently.
//
// If they render the same, the board commits the error the rest of the
// system refuses: absence of a measurement read as a measurement.
func TestZeroAndNotMeasuredRenderDifferently(t *testing.T) {
	zero := Measure{Name: "assessments", State: Counted, Numerator: 0, Denominator: 5,
		Scope: "s", MovableBy: "firms"}
	none := Measure{Name: "coverage", State: NotMeasured, Scope: "s", MovableBy: "a partner"}
	if zero.Figure() == none.Figure() {
		t.Fatalf("both render as %q", zero.Figure())
	}
	if !strings.Contains(none.Figure(), "NOT MEASURED") {
		t.Errorf("an unmeasured figure renders as %q", none.Figure())
	}
}

// TestAnInternalFigureSaysSoOnItsFace.
//
// "1 / 1" for replay determinism is a true statement about one
// synthetic case. Rendered without its scope it is a production claim.
func TestAnInternalFigureSaysSoOnItsFace(t *testing.T) {
	m := Measure{Name: "replay determinism", State: Internal, Numerator: 1, Denominator: 1,
		Scope: "one synthetic case", MovableBy: "a deployment"}
	if !strings.Contains(m.Figure(), "internal") {
		t.Fatalf("an internal-scope figure renders as %q, which reads as a production "+
			"figure", m.Figure())
	}
}

// TestAMeasureMustSayWhoCanMoveIt.
func TestAMeasureMustSayWhoCanMoveIt(t *testing.T) {
	m := Measure{Name: "assessments", State: Counted, Numerator: 0, Denominator: 5, Scope: "s"}
	if err := m.Validate(); err == nil {
		t.Fatal("a measure with no responsible party was accepted")
	}
}

// TestNothingOnTheBoardIsMovableByVeriqoAlone.
//
// The load-bearing property. A headline containing anything VERIQO can
// raise by working harder will, in time, be raised by working harder,
// and the eight zeroes will still be zeroes.
func TestNothingOnTheBoardIsMovableByVeriqoAlone(t *testing.T) {
	b, err := Veriqo()
	if err != nil {
		t.Fatal(err)
	}
	if got := b.SelfMovable(); len(got) != 0 {
		for _, m := range got {
			t.Errorf("%q can be moved by VERIQO alone", m.Name)
		}
	}
}

// TestTheBoardHasTheNineMeasuresTheAuditNamed.
func TestTheBoardHasTheNineMeasuresTheAuditNamed(t *testing.T) {
	b, err := Veriqo()
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Measures) != 9 {
		t.Fatalf("%d measures, want 9", len(b.Measures))
	}
	for _, want := range []string{"externally validated", "corpus coverage",
		"Independent assessments", "operational evidence", "Replay determinism",
		"authority attempts", "Cross-tenant", "provenance", "Disproof routes"} {
		found := false
		for _, m := range b.Measures {
			if strings.Contains(m.Name, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("no measure covers %q", want)
		}
	}
}

// TestEveryMeasureStatesItsScopeInEnoughDetailToBeChecked.
func TestEveryMeasureStatesItsScopeInEnoughDetailToBeChecked(t *testing.T) {
	b, err := Veriqo()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range b.Measures {
		if n := len(strings.Fields(m.Scope)); n < 10 {
			t.Errorf("%q states its scope in %d words; a denominator needs its "+
				"population described", m.Name, n)
		}
		if strings.TrimSpace(m.Blocks) == "" {
			t.Errorf("%q does not say what it blocks, so a zero reads as a shortfall "+
				"rather than a consequence", m.Name)
		}
	}
}

// TestTheReportSaysWhyTheTestCountIsAbsent.
//
// An absent metric that is not explained looks like an oversight, and
// somebody helpfully adds it back.
func TestTheReportSaysWhyTheTestCountIsAbsent(t *testing.T) {
	b, err := Veriqo()
	if err != nil {
		t.Fatal(err)
	}
	r := b.Report()
	if !strings.Contains(r, "test count is deliberately absent") {
		t.Error("the report does not explain the absence of the test count")
	}
	if !strings.Contains(r, "NOT MEASURED is not zero") {
		t.Error("the report does not distinguish an absence from a zero")
	}
	if b.Report() != b.Report() {
		t.Error("Report() is not deterministic")
	}
	for _, line := range strings.Split(r, "\n") {
		if len([]rune(line)) > 78 {
			t.Errorf("a %d-column line will wrap: %q", len([]rune(line)), line)
		}
	}
}

// TestABoardMayNotCarryABannedMeasure.
func TestABoardMayNotCarryABannedMeasure(t *testing.T) {
	_, err := NewBoard(Measure{Name: "tests passed", State: Counted, Numerator: 37,
		Denominator: 37, Scope: "the suite", MovableBy: "VERIQO"})
	if !errors.Is(err, ErrNotHeadline) {
		t.Fatalf("a board was built with the test count on it: %v", err)
	}
}
