package architecture

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"veriqo/pkg/assurance/failureclass"
)

// The failure-class register is only worth as much as its citations.
//
// An entry that names four tests looks like a completed discipline
// whether or not those tests exist. That is the same failure the
// traceability chain caught in the assurance matrix -- nine articles
// citing tests that had been renamed away -- so the register is held to
// the same rule from the day it is written rather than after it
// happens again.
//
// This is FC-004 (STALE_CITATION) applied to the register that records
// FC-004, which is the point: a class is closed when the invariant
// governs everything of that shape, including the thing that recorded
// it.

func TestEveryFailureClassCitationNamesATestThatExists(t *testing.T) {
	root := repoRoot(t)
	declared := declaredTestNames(t, root)

	r, err := failureclass.NewRegister(failureclass.Closed...)
	if err != nil {
		t.Fatalf("NewRegister: %v", err)
	}
	var missing []string
	for _, e := range r.All() {
		for i, name := range e.Tests() {
			if !declared[name] {
				stage := []string{"positive", "negative", "mutation", "regression"}[i]
				missing = append(missing, fmt.Sprintf("%s (%s): %s test %s is not declared anywhere",
					e.ID, e.Class, stage, name))
			}
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("the failure-class register cites tests that do not exist.\n"+
			"A renamed test leaves the chain looking complete while the class loses its proof.\n  %s",
			strings.Join(missing, "\n  "))
	}
}

// TestTheFailureClassScanIsNotVacuous. If CitedTests returned nothing,
// the test above would pass over an empty set.
func TestTheFailureClassScanIsNotVacuous(t *testing.T) {
	r, err := failureclass.NewRegister(failureclass.Closed...)
	if err != nil {
		t.Fatalf("NewRegister: %v", err)
	}
	if len(r.CitedTests()) == 0 {
		t.Fatal("no test name was extracted from the register: the citation check governs nothing")
	}
}

// TestTheScannerWouldCatchAnInventedCitation. The check above passes
// today; this proves it would fail if it stopped being true.
func TestTheScannerWouldCatchAnInventedCitation(t *testing.T) {
	root := repoRoot(t)
	declared := declaredTestNames(t, root)
	if declared["TestThisNameWasNeverDeclaredAnywhereInTheModule"] {
		t.Fatal("the collector reports a name nobody declared; it is matching too loosely")
	}
	if !declared["TestTheFailureClassScanIsNotVacuous"] {
		t.Fatal("the collector missed a test declared in this very file; it is matching too tightly")
	}
}

// TestEveryFailureClassRoundIsARealReviewRound. The Round field is what
// makes the register a history rather than a taxonomy, so a made-up
// round would quietly detach it from the reviews it came from.
func TestEveryFailureClassRoundIsARealReviewRound(t *testing.T) {
	real := map[string]bool{"CS": true, "F": true, "G": true, "H": true}
	for _, e := range failureclass.Closed {
		if !real[e.Round] {
			t.Errorf("%s names review round %q, which is not one of the rounds this "+
				"repository has been through", e.ID, e.Round)
		}
	}
}
