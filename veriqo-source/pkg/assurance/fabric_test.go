package assurance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAllFiveFabricsAreAuditedOnEveryDimension is the audit's own
// completeness check. A blank dimension is the finding, and it must be
// impossible to ship one quietly.
func TestAllFiveFabricsAreAuditedOnEveryDimension(t *testing.T) {
	audits := FabricAudits()
	if len(audits) != 5 {
		t.Fatalf("expected five fabrics, got %d", len(audits))
	}
	for _, f := range audits {
		if missing := f.Incomplete(); len(missing) > 0 {
			t.Fatalf("fabric %s is not audited on: %s", f.Fabric, strings.Join(missing, ", "))
		}
		if len(f.Dimensions()) != 11 {
			t.Fatalf("fabric %s: expected eleven dimensions, got %d", f.Fabric, len(f.Dimensions()))
		}
	}
}

func TestTheFiveFabricsAreTheFiveNamed(t *testing.T) {
	want := []FabricID{TECP, EQF, IF, CRF, FREF}
	got := Fabrics()
	if len(got) != len(want) {
		t.Fatalf("expected %d fabrics, got %d", len(want), len(got))
	}
	seen := map[FabricID]bool{}
	for _, f := range FabricAudits() {
		seen[f.Fabric] = true
	}
	for _, w := range want {
		if !seen[w] {
			t.Fatalf("fabric %s has no audit", w)
		}
	}
}

// TestNoFabricHasItsOwnPackage is the anti-duplication rule the backlog
// states explicitly: the five are architectural capabilities, and
// creating pkg/tecp, pkg/eqf, pkg/if or pkg/crf-engine would be five new
// façades over code that already exists.
//
// pkg/fref is the one exception and is deliberate: it is a contract that
// refuses out-of-order executions, not an engine that runs them. It owns
// no stage; every stage names another package.
func TestNoFabricHasItsOwnPackage(t *testing.T) {
	root := repoRoot(t)
	for _, forbidden := range []string{"tecp", "eqf", "crf", "intelligentfabric", "trustengine", "caseengine"} {
		if _, err := os.Stat(filepath.Join(root, "pkg", forbidden)); err == nil {
			t.Fatalf("pkg/%s exists: the fabrics are capabilities over existing packages, not new engines", forbidden)
		}
	}
}

// TestEveryCanonicalPackageExists keeps the audit honest. A dimension
// pointing at a package that is not there would be worse than a blank.
func TestEveryCanonicalPackageExists(t *testing.T) {
	root := repoRoot(t)
	for _, f := range FabricAudits() {
		for _, p := range f.CanonicalPackages {
			rel := strings.TrimPrefix(p, "veriqo/")
			if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
				t.Fatalf("fabric %s names %s, which does not exist: %v", f.Fabric, p, err)
			}
		}
	}
}

// TestEveryFabricNamesItsDuplicationRisk: the packages that LOOK like a
// second authority are the ones a reviewer will find, so the audit names
// them first and says why each is not one.
func TestEveryFabricNamesItsDuplicationRisk(t *testing.T) {
	for _, f := range FabricAudits() {
		if strings.TrimSpace(f.DuplicationRisk) == "" {
			t.Fatalf("fabric %s names no duplication risk: every fabric spans packages that could look like rivals", f.Fabric)
		}
	}
}

// TestEveryFabricFailsClosed asserts the eleventh dimension says what is
// refused, not merely what is logged.
func TestEveryFabricFailsClosed(t *testing.T) {
	refusals := []string{"refus", "never", "cannot", "denies", "no ", "excluded", "stays", "fails", "is not"}
	for _, f := range FabricAudits() {
		lower := strings.ToLower(f.FailClosed)
		found := false
		for _, r := range refusals {
			if strings.Contains(lower, r) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("fabric %s does not state a refusal: %q", f.Fabric, f.FailClosed)
		}
	}
}

// --- Vocabulary freeze -----------------------------------------------

// TestEveryRetiredSynonymHasExactlyOneReplacement is the freeze: a name
// that maps to two fabrics has not been retired, it has been made
// ambiguous in a new way.
func TestEveryRetiredSynonymHasExactlyOneReplacement(t *testing.T) {
	counts := map[string]int{}
	for _, f := range FabricAudits() {
		for _, s := range f.RetiredSynonyms {
			counts[s]++
		}
	}
	for s, n := range counts {
		if n != 1 {
			t.Fatalf("synonym %q maps to %d fabrics: the vocabulary is still ambiguous", s, n)
		}
	}
	if len(counts) < 15 {
		t.Fatalf("expected the freeze to retire at least fifteen names, got %d", len(counts))
	}
}

// TestTheAmbiguousNamesTheBacklogListedAreAllRetired names them
// explicitly, so the freeze covers the actual problem rather than a
// convenient subset.
func TestTheAmbiguousNamesTheBacklogListedAreAllRetired(t *testing.T) {
	retired := RetiredVocabulary()
	for _, name := range []string{
		"Unified Evidence", "Evidence Fabric", "Evidence Engine", "Trust Engine", "Trust Kernel",
		"Knowledge Fabric", "Intelligent Fabric", "Intelligence Layer",
		"Qualification", "EQF", "Case lineage", "Case Engine", "Orchestrator", "Execution",
	} {
		if _, ok := retired[name]; !ok {
			t.Fatalf("%q was listed as ambiguous and is not retired to a canonical fabric", name)
		}
	}
}

func TestFabricReportRendersEveryDimension(t *testing.T) {
	r := FabricReport()
	for _, f := range FabricAudits() {
		if !strings.Contains(r, string(f.Fabric)) {
			t.Fatalf("fabric %s is missing from the report", f.Fabric)
		}
	}
	for _, d := range []string{"CAPABILITY", "CALL GRAPH", "EVIDENCE FLOW", "E2E TEST", "FAIL-CLOSED BEHAVIOR", "RETIRES"} {
		if !strings.Contains(r, d) {
			t.Fatalf("the report omits the %s dimension", d)
		}
	}
	if strings.Contains(r, "NOT AUDITED") {
		t.Fatal("the report contains an unaudited dimension")
	}
}

// repoRoot walks up from the test's working directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not find the module root")
	return ""
}
