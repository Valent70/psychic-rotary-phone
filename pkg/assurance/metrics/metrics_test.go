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
	sv, _ := Software()
	aq, _ := Assurance()
	pe, _ := Production()

	// A measure cannot cross registers.
	m := sv.Measures()[0]
	m.Register = AssuranceQualification
	if _, err := New(SoftwareVerification, m); !errors.Is(err, ErrWrongRegister) {
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

// TestTheTwoRegistersThatMatterMostAreEmptyAndSaySo.
func TestTheTwoRegistersThatMatterMostAreEmptyAndSaySo(t *testing.T) {
	aq, err := Assurance()
	if err != nil {
		t.Fatal(err)
	}
	pe, err := Production()
	if err != nil {
		t.Fatal(err)
	}
	if !aq.Empty() || !pe.Empty() {
		t.Fatal("a register that should be empty has entries; if that is now true, the " +
			"entries must name who produced them and this test changed deliberately")
	}
	for _, s := range []*Set{aq, pe} {
		if !strings.Contains(s.Report(), "EMPTY. Nothing in this register has been") {
			t.Fatalf("%s does not state its emptiness plainly", s.Register())
		}
		if !strings.Contains(s.Report(), "cannot be improved by working harder") {
			t.Fatalf("%s does not state that effort will not move it", s.Register())
		}
	}
}

// TestAnEmptyAssuranceRegisterIsCalledOutInThePanel.
//
// A reader who takes a full software-verification section as
// reassurance has made the exact error the separation prevents, and
// the panel must say so rather than leaving them to notice.
func TestAnEmptyAssuranceRegisterIsCalledOutInThePanel(t *testing.T) {
	out, err := VeriqoPanel()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"The assurance register is EMPTY",
		"No quantity of software",
		"exact error this separation prevents",
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
	sv, err := Software()
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
	sv, _ := Software()
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
	bad := Measure{Register: SoftwareVerification, Name: "x", Value: "1", Basis: "b"}
	if err := bad.Validate(); !errors.Is(err, ErrNoMeasure) {
		t.Fatalf("a measure with no caveat validated: %v", err)
	}
}

// TestValuesAreTextSoThatNeverAttemptedIsNotZero.
//
// Forcing "none", "not run" and "never attempted" into a float would
// turn three honest answers into the same misleading one.
func TestValuesAreTextSoThatNeverAttemptedIsNotZero(t *testing.T) {
	sv, _ := Software()
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

// TestOnlySoftwareVerificationIsSelfProducible. That asymmetry is why
// the three must not be summed.
func TestOnlySoftwareVerificationIsSelfProducible(t *testing.T) {
	for _, r := range Registers() {
		want := r == SoftwareVerification
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
