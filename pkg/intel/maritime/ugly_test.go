package maritime

import (
	"strings"
	"testing"
	"time"
)

func anom(kind string) Anomaly {
	return Anomaly{Kind: kind, VesselID: "MMSI:123456789",
		From:        time.Date(2026, 6, 14, 5, 0, 0, 0, time.UTC),
		To:          time.Date(2026, 6, 14, 11, 0, 0, 0, time.UTC),
		Observation: "six hours with no report"}
}

// TestSpoofingIsNeverTheOnlyExplanation.
//
// The label a detector reaches for, and the one that is wrong in every
// case where jamming, a fault, or a late message produces the same
// observation. If this test ever passes with one explanation, the
// system has learned to accuse.
func TestSpoofingIsNeverTheOnlyExplanation(t *testing.T) {
	for _, k := range []string{"IMPLAUSIBLE_SPEED", "REPORTING_GAP", "NULL_POSITION",
		"DRAUGHT_CHANGE", "IDENTIFIER_COLLISION"} {
		tri := Explain(anom(k))
		if len(tri.Explanations) < 3 {
			t.Errorf("%s has %d explanations; a detector that offers fewer than three "+
				"is naming a cause", k, len(tri.Explanations))
		}
		spoof := 0
		for _, e := range tri.Explanations {
			if e.Cause == Spoofing {
				spoof++
			}
		}
		if spoof != 1 {
			t.Errorf("%s lists spoofing %d times", k, spoof)
		}
	}
}

// TestTheMundaneExplanationsComeFirst.
//
// Ordering is not cosmetic. An analyst reads the top of the list, and
// a list led by SPOOFING teaches them what to look for.
func TestTheMundaneExplanationsComeFirst(t *testing.T) {
	tri := Explain(anom("REPORTING_GAP"))
	seenExotic := false
	for _, e := range tri.Explanations {
		if !e.Mundane {
			seenExotic = true
			continue
		}
		if seenExotic {
			t.Fatalf("%s (mundane) is listed after a non-mundane cause", e.Cause)
		}
	}
}

// TestGenuineMovementIsAlwaysOnTheList.
//
// A detector whose explanation set omits "the thing happened" has
// assumed its own conclusion before looking.
func TestGenuineMovementIsAlwaysOnTheList(t *testing.T) {
	for _, k := range []string{"IMPLAUSIBLE_SPEED", "REPORTING_GAP", "DRAUGHT_CHANGE"} {
		found := false
		for _, e := range Explain(anom(k)).Explanations {
			if e.Cause == GenuineMovement {
				found = true
			}
		}
		if !found {
			t.Errorf("%s does not admit that the vessel may have done the thing", k)
		}
	}
}

// TestNoDiscriminatorLivesInsideAIS.
//
// The technical heart of it. A single AIS feed cannot separate a
// spoofed position from a jammed one, because in both cases the
// receiver reports a position it believes. Any discriminator that
// needed only AIS would be a false promise.
func TestNoDiscriminatorLivesInsideAIS(t *testing.T) {
	for _, k := range []string{"IMPLAUSIBLE_SPEED", "REPORTING_GAP", "NULL_POSITION",
		"DRAUGHT_CHANGE"} {
		for _, e := range Explain(anom(k)).Explanations {
			if e.Needs == "" {
				t.Errorf("%s/%s names no modality", k, e.Cause)
			}
			if strings.TrimSpace(e.Discriminator) == "" {
				t.Errorf("%s/%s offers no test", k, e.Cause)
			}
		}
	}
}

// TestWithNoModalitiesNothingIsDecidable.
//
// The honest state of every VERIQO deployment today: no imagery, no
// RF, no port records are contracted for, so every cause remains open.
func TestWithNoModalitiesNothingIsDecidable(t *testing.T) {
	tri := Explain(anom("REPORTING_GAP"))
	if tri.Distinguishable() {
		t.Fatal("the causes are reported as separable with no evidence available")
	}
	if len(tri.Undiscriminated()) != len(tri.Explanations) {
		t.Fatal("some cause was ruled in or out with nothing to do it with")
	}
	s := tri.Statement()
	if !strings.Contains(s, "not a finding") {
		t.Errorf("the statement does not refuse the promotion to a finding: %q", s)
	}
}

// TestSuppliedModalitiesNarrowTheQuestion.
func TestSuppliedModalitiesNarrowTheQuestion(t *testing.T) {
	tri := Explain(anom("REPORTING_GAP"), SatelliteImagery, FeedAudit)
	before := len(Explain(anom("REPORTING_GAP")).Undiscriminated())
	if len(tri.Undiscriminated()) >= before {
		t.Fatal("supplying imagery and a feed audit settled nothing")
	}
	needs := tri.Needs()
	for _, n := range needs {
		if n == SatelliteImagery || n == FeedAudit {
			t.Errorf("%s is still listed as needed after being supplied", n)
		}
	}
}

// TestNeedsIsAShoppingList.
//
// The output has to be actionable. "We cannot tell" is only useful
// beside "and here is what would tell us".
func TestNeedsIsAShoppingList(t *testing.T) {
	needs := Explain(anom("REPORTING_GAP")).Needs()
	if len(needs) == 0 {
		t.Fatal("nothing is named that would settle the question")
	}
	seen := map[Modality]bool{}
	for _, n := range needs {
		if seen[n] {
			t.Errorf("%s appears twice", n)
		}
		seen[n] = true
	}
}

// TestTheStatementNeverNamesACause.
//
// This sentence is the one that gets copied into a slide. It must not
// be capable of becoming "the vessel went dark to conceal a transfer".
func TestTheStatementNeverNamesACause(t *testing.T) {
	for _, k := range []string{"IMPLAUSIBLE_SPEED", "REPORTING_GAP", "DRAUGHT_CHANGE"} {
		s := strings.ToUpper(Explain(anom(k)).Statement())
		for _, banned := range []string{"SPOOF", "WENT DARK", "CONCEAL", "DELIBERAT",
			"EVAD", "SANCTION"} {
			if strings.Contains(s, banned) {
				t.Errorf("%s statement contains %q: %s", k, banned, s)
			}
		}
	}
}

// TestTriageHasNoMostLikely.
//
// Guarded by construction rather than by convention: there is no field
// and no method that reduces the set to a label, so a caller in a
// hurry cannot find one.
func TestTriageHasNoMostLikely(t *testing.T) {
	var tri any = Explain(anom("REPORTING_GAP"))
	if _, ok := tri.(interface{ MostLikely() Cause }); ok {
		t.Fatal("Triage has a MostLikely method; the set can be collapsed to a label")
	}
	if _, ok := tri.(interface{ Verdict() Cause }); ok {
		t.Fatal("Triage has a Verdict method")
	}
	if _, ok := tri.(interface{ Score() float64 }); ok {
		t.Fatal("Triage has a Score method; a number here would be used as a probability")
	}
}

// TestTheReportFitsATerminalAndIsDeterministic.
func TestTheReportFitsATerminalAndIsDeterministic(t *testing.T) {
	tri := Explain(anom("REPORTING_GAP"))
	if tri.Report() != tri.Report() {
		t.Error("Report() is not deterministic")
	}
	for _, line := range strings.Split(tri.Report(), "\n") {
		if len([]rune(line)) > 78 {
			t.Errorf("a %d-column line will wrap: %q", len([]rune(line)), line)
		}
	}
}
