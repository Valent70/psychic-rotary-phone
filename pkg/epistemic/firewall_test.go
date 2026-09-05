package epistemic

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

// TestTheZeroStateIsUnexamined.
//
// Every failure this package prevents happens through a zero value: a
// field nobody filled in must not read as "fine", and must not read as
// "absent" either -- nobody looked, and that is a third thing.
func TestTheZeroStateIsUnexamined(t *testing.T) {
	var s State
	if s != Unexamined {
		t.Fatalf("the zero state is %s", s)
	}
	var o Observation
	if o.State != Unexamined {
		t.Fatal("an unpopulated observation does not default to UNEXAMINED")
	}
	if s.MayIncreaseAssurance() {
		t.Fatal("the zero state may increase assurance")
	}
}

// TestUnreadableIsNotVerified. A document nobody could parse has not
// been checked and found clean; it has not been checked.
func TestUnreadableIsNotVerified(t *testing.T) {
	if err := UnreadableIsNotVerified(Unreadable, true); !errors.Is(err, ErrCoerced) {
		t.Fatalf("unreadable material was reported verified: %v", err)
	}
	if err := UnreadableIsNotVerified(Verified, true); err != nil {
		t.Fatalf("genuinely verified material was refused: %v", err)
	}
	if err := UnreadableIsNotVerified(Unreadable, false); err != nil {
		t.Fatalf("unreadable material not claimed verified was refused: %v", err)
	}
}

// TestUnparseableIsNotAbsent. A field that failed to decode is a fault
// in the observation; a field that is not there is a fact about the
// world.
func TestUnparseableIsNotAbsent(t *testing.T) {
	if err := UnparseableIsNotAbsent(Unreadable, true); !errors.Is(err, ErrCoerced) {
		t.Fatalf("a decode failure was treated as absence: %v", err)
	}
	if err := UnparseableIsNotAbsent(Absent, true); err != nil {
		t.Fatalf("genuine absence was refused: %v", err)
	}
	// The two states must not be equal, or nothing above matters.
	if Unreadable == Absent {
		t.Fatal("UNREADABLE and ABSENT are the same value")
	}
	if Unreadable.IsFault() == Absent.IsFault() {
		t.Fatal("UNREADABLE and ABSENT are classified alike; one is a fault and one is a fact")
	}
}

// TestMissingIsNotValid. Skipping a check is not passing it.
func TestMissingIsNotValid(t *testing.T) {
	if err := MissingIsNotValid(false, true); !errors.Is(err, ErrCoerced) {
		t.Fatalf("a check that did not run was treated as satisfied: %v", err)
	}
	if err := MissingIsNotValid(true, true); err != nil {
		t.Fatalf("a check that ran and passed was refused: %v", err)
	}
	if err := MissingIsNotValid(false, false); err != nil {
		t.Fatalf("a check that did not run and was not claimed was refused: %v", err)
	}
}

// TestUnknownIsNotNegative. Not having found something is not having
// found its absence -- the one people re-derive wrongly most often,
// because in ordinary speech "we found nothing" and "there is nothing"
// are the same sentence.
func TestUnknownIsNotNegative(t *testing.T) {
	for _, s := range []State{Unexamined, Unreadable} {
		if err := UnknownIsNotNegative(s, true); !errors.Is(err, ErrCoerced) {
			t.Fatalf("%s was treated as a negative finding: %v", s, err)
		}
	}
	if err := UnknownIsNotNegative(Absent, true); err != nil {
		t.Fatalf("a genuine finding of absence was refused: %v", err)
	}
}

// TestOnlyPresentAndVerifiedMayIncreaseAssurance.
//
// ABSENT is the subtle exclusion: confirmed absence is real
// information, and it is information about what is NOT there, so it
// cannot raise confidence in a positive claim.
func TestOnlyPresentAndVerifiedMayIncreaseAssurance(t *testing.T) {
	want := map[State]bool{
		Unexamined: false, Unreadable: false, Absent: false,
		Present: true, Verified: true, Contradicted: false,
	}
	for s, w := range want {
		if s.MayIncreaseAssurance() != w {
			t.Fatalf("%s.MayIncreaseAssurance() = %v, want %v", s, s.MayIncreaseAssurance(), w)
		}
	}
	if Absent.MayIncreaseAssurance() {
		t.Fatal("confirmed absence raised confidence in a positive claim")
	}
	if !Absent.Readable() {
		t.Fatal("ABSENT was classified as unreadable; it is a finding, not a failure")
	}
}

// TestAFaultMustSayWhy. "Could not be read" without a reason is not
// actionable: the fix depends entirely on whether it was encrypted,
// corrupt, or in a format nothing here decodes.
func TestAFaultMustSayWhy(t *testing.T) {
	for _, s := range []State{Unexamined, Unreadable} {
		o := Observation{Subject: "the bill of lading", State: s}
		if err := o.Validate(); err == nil {
			t.Fatalf("a %s observation with no reason validated", s)
		}
		o.Why = "the PDF is encrypted and no key was supplied"
		if err := o.Validate(); err != nil {
			t.Fatalf("a fault with a reason was refused: %v", err)
		}
	}
	// An informative state needs no reason.
	if err := (Observation{Subject: "x", State: Present, Value: "y"}).Validate(); err != nil {
		t.Fatalf("a PRESENT observation was refused: %v", err)
	}
	// ABSENT carrying a value is incoherent.
	if err := (Observation{Subject: "x", State: Absent, Value: "y"}).Validate(); err == nil {
		t.Fatal("an ABSENT observation carried a value")
	}
}

// TestSupportingExcludesFaultsSoACallerCannotCountThem.
//
// The firewall in its most-used form: ranging over Supporting()
// instead of Observations makes the mistake unavailable.
func TestSupportingExcludesFaultsSoACallerCannotCountThem(t *testing.T) {
	s := Set{Observations: []Observation{
		{Subject: "loading survey", State: Verified, Value: "60,000 MT"},
		{Subject: "discharge survey", State: Present, Value: "58,200 MT"},
		{Subject: "third survey", State: Absent},
		{Subject: "customs declaration", State: Unreadable,
			Why: "the file is encrypted and no key was supplied"},
		{Subject: "port log", State: Unexamined, Why: "the terminal has not responded"},
	}}
	if err := s.Sound(); err != nil {
		t.Fatal(err)
	}
	if n := len(s.Supporting()); n != 2 {
		t.Fatalf("%d observations counted as support", n)
	}
	if n := len(s.Faults()); n != 2 {
		t.Fatalf("%d faults", n)
	}
	c, err := s.Assess()
	if err != nil {
		t.Fatal(err)
	}
	if c.Total != 5 || c.Informative != 3 || c.Supporting != 2 || c.Faults != 2 {
		t.Fatalf("coverage = %+v", c)
	}
}

// TestCoverageIsNotARatio.
//
// "4 of 6 verified" reads as two-thirds of the way to something, and
// the missing two are not two-thirds of anything -- one of them may be
// the one that matters.
func TestCoverageIsNotARatio(t *testing.T) {
	s := Set{Observations: []Observation{
		{Subject: "a", State: Verified, Value: "1"},
		{Subject: "b", State: Unreadable, Why: "encrypted"},
	}}
	c, err := s.Assess()
	if err != nil {
		t.Fatal(err)
	}
	st := c.Statement()
	// The forbidden shape is "N of M" -- a fraction of a whole, which
	// invites the reader to see the faults as the remaining fraction
	// of the same thing. Prose containing the word "of" is fine.
	if regexp.MustCompile(`\d+\s+of\s+\d+`).MatchString(st) || strings.Contains(st, "%") {
		t.Fatalf("the statement reads as a ratio: %s", st)
	}
	if !strings.Contains(st, "not evidence of anything") {
		t.Fatalf("the statement does not say what a fault is worth: %s", st)
	}
	if !strings.Contains(st, "may be the thing that mattered") {
		t.Fatalf("the statement lets a fault read as a partial result: %s", st)
	}
}

// TestContradictedIsKnowledgeNotFailure.
func TestContradictedIsKnowledgeNotFailure(t *testing.T) {
	if Contradicted.IsFault() {
		t.Fatal("a contradiction was classified as a fault; it is knowledge")
	}
	if !Contradicted.Readable() {
		t.Fatal("a contradiction was classified as unreadable")
	}
	if Contradicted.MayIncreaseAssurance() {
		t.Fatal("a contradiction raised assurance")
	}
	if !strings.Contains(Contradicted.Meaning(), "knowledge, not a failure") {
		t.Fatalf("the meaning does not say so: %s", Contradicted.Meaning())
	}
}

// TestEveryStateStatesWhatItDoesAndDoesNotSay.
func TestEveryStateStatesWhatItDoesAndDoesNotSay(t *testing.T) {
	for _, s := range States() {
		if !s.Valid() {
			t.Fatalf("%d has no name", int(s))
		}
		if strings.TrimSpace(s.Meaning()) == "" {
			t.Fatalf("%s says nothing about what it means", s)
		}
		got, err := Parse(s.String())
		if err != nil || got != s {
			t.Fatalf("Parse(%s) = %v, %v", s, got, err)
		}
	}
	if len(States()) != 6 {
		t.Fatalf("%d states", len(States()))
	}
	if _, err := Parse("CLEAN"); !errors.Is(err, ErrUnknownState) {
		t.Fatalf("an invented state parsed: %v", err)
	}
}

// TestTheReportCarriesTheFourInequalities. A reader who sees the
// output should not have to have read this package.
func TestTheReportCarriesTheFourInequalities(t *testing.T) {
	s := Set{Observations: []Observation{
		{Subject: "a", State: Unreadable, Why: "encrypted"}}}
	r := s.Report()
	for _, want := range []string{
		"unreadable != verified", "unparseable != absent",
		"missing    != valid", "unknown     != negative",
		"why: encrypted",
	} {
		if !strings.Contains(r, want) {
			t.Fatalf("the report omits %q:\n%s", want, r)
		}
	}
}
