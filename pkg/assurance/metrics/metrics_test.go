package metrics

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

// TestThereIsNoWayToCombineTheRegisters. The separation is the whole
// package, so the absence of an aggregate is asserted rather than
// assumed.
func TestThereIsNoWayToCombineTheRegisters(t *testing.T) {
	sv, _ := Engineering()
	aq, _ := Epistemic()
	pe, _ := External()

	// A measure cannot cross registers.
	m := sv.Measures()[0]
	m.Register = EpistemicIntegrity
	if _, err := New(EngineeringIntegrity, m); !errors.Is(err, ErrWrongRegister) {
		t.Fatalf("a measure from another register was accepted: %v", err)
	}

	// The panel refuses a set in the wrong slot, so a caller cannot
	// pass one register three times and produce a complete-looking
	// report.
	if _, err := Panel(aq, sv, pe); !errors.Is(err, ErrWrongRegister) {
		t.Fatalf("the panel accepted registers in the wrong slots: %v", err)
	}
	if _, err := Panel(sv, aq, nil); !errors.Is(err, ErrCombine) {
		t.Fatalf("the panel rendered fewer than three registers: %v", err)
	}
}

// TestTheExternalBoardIsEmptyAndSaysSo.
func TestTheExternalBoardIsEmptyAndSaysSo(t *testing.T) {
	pe, err := External()
	if err != nil {
		t.Fatal(err)
	}
	if !pe.Empty() {
		t.Fatal("a register that should be empty has entries; if that is now true, the " +
			"entries must name who produced them and this test changed deliberately")
	}
	for _, s := range []*Set{pe} {
		if !strings.Contains(s.Report(), "EMPTY. Nothing in this register has been") {
			t.Fatalf("%s does not state its emptiness plainly", s.Register())
		}
		if !strings.Contains(s.Report(), "cannot be improved by working harder") {
			t.Fatalf("%s does not state that effort will not move it", s.Register())
		}
	}
}

// TestAnEmptyExternalBoardIsCalledOutInThePanel.
//
// A reader who takes a full software-verification section as
// reassurance has made the exact error the separation prevents, and
// the panel must say so rather than leaving them to notice.
func TestAnEmptyExternalBoardIsCalledOutInThePanel(t *testing.T) {
	out, err := VeriqoPanel()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"The EXTERNAL QUALIFICATION board is EMPTY",
		"No quantity of",
		"exact error this\nseparation prevents",
		"no total below",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the panel omits %q", want)
		}
	}
}

// TestTheTestCountCarriesTheInflationWarning.
//
// This is the measure the audit warned about: the moment a test count
// becomes a quality signal, the cheapest way to improve quality is to
// write tests that assert what the code already does.
func TestTheTestCountCarriesTheInflationWarning(t *testing.T) {
	sv, err := Engineering()
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, m := range sv.Measures() {
		if m.Name != "tests" {
			continue
		}
		found = true
		if !strings.Contains(m.Caveat, "not a quality measure") {
			t.Fatalf("the test count does not warn about inflation: %s", m.Caveat)
		}
	}
	if !found {
		t.Fatal("the software register does not report a test count at all")
	}
}

// TestEveryMeasureStatesWhatItDoesNotShow.
func TestEveryMeasureStatesWhatItDoesNotShow(t *testing.T) {
	sv, _ := Engineering()
	for _, m := range sv.Measures() {
		if err := m.Validate(); err != nil {
			t.Fatalf("%s: %v", m.Name, err)
		}
		if len(strings.Fields(m.Caveat)) < 5 {
			t.Fatalf("%s has a caveat too short to say anything: %q", m.Name, m.Caveat)
		}
	}
	// A measure with no caveat is refused, so the property above
	// cannot be satisfied by omission.
	bad := Measure{Register: EngineeringIntegrity, Name: "x", Value: "1", Basis: "b"}
	if err := bad.Validate(); !errors.Is(err, ErrNoMeasure) {
		t.Fatalf("a measure with no caveat validated: %v", err)
	}
}

// TestValuesAreTextSoThatNeverAttemptedIsNotZero.
//
// Forcing "none", "not run" and "never attempted" into a float would
// turn three honest answers into the same misleading one.
func TestValuesAreTextSoThatNeverAttemptedIsNotZero(t *testing.T) {
	sv, _ := Engineering()
	var nonNumeric int
	for _, m := range sv.Measures() {
		if _, err := strconv.Atoi(m.Value); err != nil {
			nonNumeric++
		}
	}
	if nonNumeric == 0 {
		t.Fatal("every value in the register parses as a number; the fixture no longer " +
			"exercises the reason values are text")
	}
}

// TestOnlyTheExternalBoardIsBeyondTheBuilder. That asymmetry is why
// the three must not be summed: an aggregate would let the two the
// builder can move hide the one it cannot, which is precisely the one
// a customer is asking about.
func TestOnlyTheExternalBoardIsBeyondTheBuilder(t *testing.T) {
	for _, r := range Registers() {
		want := r != ExternalQualification
		if r.SelfProducible() != want {
			t.Fatalf("%s.SelfProducible() = %v", r, r.SelfProducible())
		}
		if strings.TrimSpace(r.WhatItEstablishes()) == "" {
			t.Fatalf("%s does not state the limit of what it can show", r)
		}
	}
	if len(Registers()) != 3 {
		t.Fatalf("%d registers", len(Registers()))
	}
}

// TestTheEpistemicBoardExistsAndIsNotASubstituteForTheExternalOne.
//
// It is the board most systems do not have, and it is the one most
// easily mistaken for the third: reasoning honestly about what you do
// not know is not the same as somebody else confirming you were right.
func TestTheEpistemicBoardExistsAndIsNotASubstituteForTheExternalOne(t *testing.T) {
	ep, err := Epistemic()
	if err != nil {
		t.Fatal(err)
	}
	if ep.Empty() {
		t.Fatal("the epistemic board is empty; it is the one VERIQO is actually about")
	}
	want := []string{"unknown handling", "source independence", "contradiction handling",
		"evidence provenance", "hypothesis separation", "decision traceability",
		"challengeability"}
	have := map[string]bool{}
	for _, m := range ep.Measures() {
		have[m.Name] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Fatalf("the epistemic board omits %q", w)
		}
	}
	// Every entry must state what it does NOT establish, or the board
	// becomes a list of strengths.
	for _, m := range ep.Measures() {
		if len(strings.Fields(m.Caveat)) < 6 {
			t.Fatalf("%s has a caveat too short to limit it: %q", m.Name, m.Caveat)
		}
	}
	// And the challengeability entry must admit nobody has accepted.
	for _, m := range ep.Measures() {
		if m.Name != "challengeability" {
			continue
		}
		if !strings.Contains(m.Caveat, "nobody outside has run it") {
			t.Fatalf("challengeability does not admit that no outsider has run it: %q",
				m.Caveat)
		}
	}
	// The panel must say the middle board is not the third.
	out, err := VeriqoPanel()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "not the same as somebody else confirming you were right") {
		t.Fatalf("the panel lets epistemic integrity stand in for external qualification:\n%s",
			out)
	}
}
