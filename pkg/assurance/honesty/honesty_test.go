package honesty

import (
	"errors"
	"strings"
	"testing"
)

func suite(t *testing.T) *Suite {
	t.Helper()
	s, err := Veriqo()
	if err != nil {
		t.Fatalf("the suite does not build: %v", err)
	}
	return s
}

// TestAWeakCheckCannotBeCalledHonestyVerification.
//
// This is the package's reason to exist: a keyword screen described as
// honesty verification is an overclaim about an overclaim detector.
func TestAWeakCheckCannotBeCalledHonestyVerification(t *testing.T) {
	for _, l := range []Level{None, H1, H2, H3, H4} {
		for _, d := range []string{
			"honesty verification", "the honesty check passed",
			"content is fully verified", "independently verified by the suite",
		} {
			if err := DescribeSafely(l, d); !errors.Is(err, ErrOverstated) {
				t.Fatalf("%s was described as %q: %v", l, d, err)
			}
		}
	}
	// H5 is the one level entitled to the strong words.
	if err := DescribeSafely(H5, "independently verified"); err != nil {
		t.Fatalf("H5 was refused the words it earns: %v", err)
	}
	// And an accurate description at a low level is accepted.
	if err := DescribeSafely(H1, "claim-language screening over the statement text"); err != nil {
		t.Fatalf("an accurate H1 description was refused: %v", err)
	}
}

// TestH1IsNamedClaimLanguageScreening. The name is the control.
func TestH1IsNamedClaimLanguageScreening(t *testing.T) {
	if H1.String() != "CLAIM_LANGUAGE_SCREENING" {
		t.Fatalf("H1 is called %s", H1)
	}
	if strings.Contains(strings.ToLower(H1.String()), "verif") {
		t.Fatal("H1's name contains a verification word")
	}
	got, err := Parse("CLAIM_LANGUAGE_SCREENING")
	if err != nil || got != H1 {
		t.Fatalf("Parse gave %v, %v", got, err)
	}
}

// TestEveryLevelStatesWhatDefeatsIt.
//
// A reader told what defeats a check cannot mistake it for one that
// nothing defeats, and that mistake is the whole failure mode.
func TestEveryLevelStatesWhatDefeatsIt(t *testing.T) {
	for _, l := range Levels() {
		if strings.TrimSpace(l.DefeatedBy()) == "" {
			t.Fatalf("%s does not say what defeats it", l)
		}
		if strings.TrimSpace(l.Question()) == "" {
			t.Fatalf("%s does not say what it asks", l)
		}
	}
	// Even the strongest level states its limit.
	if !strings.Contains(H5.DefeatedBy(), "not a guarantee") {
		t.Fatalf("H5 presents itself as complete: %s", H5.DefeatedBy())
	}
	if H5.DefeatedBy() == "" {
		t.Fatal("H5 claims nothing defeats it")
	}
}

// TestOnlyH4AndAboveCatchADeliberateOverclaim.
func TestOnlyH4AndAboveCatchADeliberateOverclaim(t *testing.T) {
	for _, l := range Levels() {
		want := l >= H4
		if l.CatchesADeliberateOverclaim() != want {
			t.Fatalf("%s.CatchesADeliberateOverclaim() = %v", l, l.CatchesADeliberateOverclaim())
		}
	}
	if !H5.RequiresAnOutsideParty() {
		t.Fatal("H5 does not require an outside party")
	}
	for _, l := range []Level{H1, H2, H3, H4} {
		if l.RequiresAnOutsideParty() {
			t.Fatalf("%s was marked as requiring an outside party", l)
		}
	}
}

// TestVeriqoReachesH4AndNotH5. The uncomfortable finding, asserted so
// that it fails loudly when it changes.
func TestVeriqoReachesH4AndNotH5(t *testing.T) {
	s := suite(t)
	if h := s.Highest(); h != H4 {
		t.Fatalf("the suite reaches %s; if H5 is now genuine, the reviewer must be named "+
			"and this test changed deliberately", h)
	}
	missing := s.Missing()
	if len(missing) != 1 || missing[0] != H5 {
		t.Fatalf("missing levels = %v", missing)
	}
	if !strings.Contains(s.Report(), "INDEPENDENT_EXTERNAL_REVIEW") {
		t.Fatalf("the report does not name the missing level:\n%s", s.Report())
	}
}

// TestThePassportScreenIsGradedH1AndNotHigher.
//
// It is the check most likely to be described as more than it is,
// because it looks like semantic analysis and is a phrase list.
func TestThePassportScreenIsGradedH1AndNotHigher(t *testing.T) {
	for _, c := range suite(t).Checks() {
		if !strings.Contains(c.Name, "passport conclusion") {
			continue
		}
		if c.Level != H1 {
			t.Fatalf("the passport screen is graded %s", c.Level)
		}
		if !strings.Contains(c.Describe(), "paraphrase") {
			t.Fatalf("its description does not name what defeats it:\n%s", c.Describe())
		}
		return
	}
	t.Fatal("the suite does not grade the passport screen at all")
}

// TestTheHighestLevelIsNotAnAverage.
//
// A suite of forty H1 checks reaches H1. Averaging would let quantity
// substitute for strength -- the test-inflation failure in a different
// costume.
func TestTheHighestLevelIsNotAnAverage(t *testing.T) {
	var many []Check
	for i := 0; i < 40; i++ {
		many = append(many, Check{
			Name:  "screen " + string(rune('a'+i%26)) + string(rune('a'+i/26)),
			Level: H1, Performs: "greps for a phrase", Where: "scripts/verify.sh"})
	}
	many = append(many, Check{Name: "one real check", Level: H4,
		Performs: "recomputes the supportable state from the evidence",
		Where:    "pkg/assurance/invariant"})
	s, err := NewSuite(many...)
	if err != nil {
		t.Fatal(err)
	}
	if s.Highest() != H4 {
		t.Fatalf("forty H1 checks and one H4 gave %s", s.Highest())
	}
	weak, err := NewSuite(many[:40]...)
	if err != nil {
		t.Fatal(err)
	}
	if weak.Highest() != H1 {
		t.Fatalf("forty H1 checks gave %s", weak.Highest())
	}
	if !strings.Contains(weak.Report(), "catch an author who is trying") {
		t.Fatalf("an all-H1 suite does not say what it cannot do:\n%s", weak.Report())
	}
	if !strings.Contains(weak.Report(), "count of checks is not the measure") {
		t.Fatalf("the report does not warn against counting:\n%s", weak.Report())
	}
}

// TestACheckMustSayWhatItDoesAndWhere. A level nobody can argue with
// is a level nobody checked.
func TestACheckMustSayWhatItDoesAndWhere(t *testing.T) {
	for _, c := range suite(t).Checks() {
		if err := c.Validate(); err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		if len(strings.Fields(c.Performs)) < 5 {
			t.Fatalf("%s describes its mechanism too briefly to argue with: %q",
				c.Name, c.Performs)
		}
	}
	bad := Check{Name: "x", Level: H1, Where: "somewhere"}
	if err := bad.Validate(); err == nil {
		t.Fatal("a check with no mechanism validated")
	}
}

// TestTheZeroLevelIsNoCheck. An unpopulated struct must not default to
// something better than nothing.
func TestTheZeroLevelIsNoCheck(t *testing.T) {
	var l Level
	if l != None || l.String() != "NO_CHECK" {
		t.Fatalf("the zero level is %s", l)
	}
	if l.CatchesADeliberateOverclaim() {
		t.Fatal("no check at all catches a deliberate overclaim")
	}
}

// TestTheReportNeverPresentsTheLevelsAsAFraction.
//
// "H1 [x] H2 [x] H3 [x] H4 [x] H5 [ ]" is read by every human being
// as "four of five -- nearly certified". That reading is not careless;
// it is what a five-item checklist MEANS. And it is wrong.
func TestTheReportNeverPresentsTheLevelsAsAFraction(t *testing.T) {
	out := suite(t).Report()
	for _, forbidden := range []string{"4/5", "4 of 5", "80%", "of 5 ", "/5"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("the report presents the levels as a fraction (%q):\n%s", forbidden, out)
		}
	}
	if regexpDigitSlashDigit(out) {
		t.Fatalf("the report contains an N/M form:\n%s", out)
	}
	// The grouping must be what the reader sees instead.
	for _, want := range []string{
		"INTERNAL CLAIM SCREENING",
		"EXTERNAL CLAIM VALIDATION",
		"NOT PERFORMED. No check in this group exists.",
		"It is not\n  four fifths of anything.",
		"a change of who is speaking",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the report omits %q:\n%s", want, out)
		}
	}
}

func regexpDigitSlashDigit(s string) bool {
	for i := 1; i+1 < len(s); i++ {
		if s[i] == '/' && s[i-1] >= '0' && s[i-1] <= '9' && s[i+1] >= '0' && s[i+1] <= '9' {
			return true
		}
	}
	return false
}

// TestH5IsAChangeOfEpistemicSourceNotTheNextCheckbox.
func TestH5IsAChangeOfEpistemicSourceNotTheNextCheckbox(t *testing.T) {
	for _, l := range []Level{H1, H2, H3, H4} {
		if l.Source() != SelfEvaluated {
			t.Fatalf("%s is not grouped as self-evaluated", l)
		}
	}
	if H5.Source() != IndependentlyEvaluated {
		t.Fatalf("H5 is grouped as %s", H5.Source())
	}
	s := suite(t)
	if n := len(s.Group(IndependentlyEvaluated)); n != 0 {
		t.Fatalf("%d externally evaluated checks exist; if that is now true, the party "+
			"must be named and this test changed deliberately", n)
	}
	if len(s.Group(SelfEvaluated)) == 0 {
		t.Fatal("no self-evaluated checks; the grouping is not exercised")
	}
}

// TestThePackageOffersNoScoreOfAnyKind. The absence is the design.
func TestThePackageOffersNoScoreOfAnyKind(t *testing.T) {
	s := suite(t)
	// Highest returns a LEVEL, which is a name, not a number that
	// invites comparison against a maximum.
	h := s.Highest()
	if !h.Valid() {
		t.Fatal("Highest returned an invalid level")
	}
	if strings.Contains(s.Report(), "score") || strings.Contains(s.Report(), "Score") {
		t.Fatal("the report offers a score")
	}
}
