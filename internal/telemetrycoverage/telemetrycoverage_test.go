package telemetrycoverage

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMeasureAgainstRealRepoIsInternallyConsistent runs Measure against
// the real repo and asserts the report's own arithmetic holds. It does
// NOT assert a coverage threshold -- that policy decision belongs to
// cmd/veriqo-readiness's gate, not to this measurement package, so this
// test stays green regardless of the real (currently low) coverage
// number rather than hard-failing the whole test suite on an
// architectural gap that isn't fixed by rerunning a test.
func TestMeasureAgainstRealRepoIsInternallyConsistent(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	rep, err := Measure(repoRoot, DomainPackageRoots)
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if rep.TotalPackages == 0 {
		t.Fatal("expected at least one domain package to exist in the real repo")
	}
	if len(rep.InstrumentedPackages) > rep.TotalPackages {
		t.Fatalf("instrumented packages (%d) cannot exceed total packages (%d)",
			len(rep.InstrumentedPackages), rep.TotalPackages)
	}
	wantCoverage := float64(len(rep.InstrumentedPackages)) / float64(rep.TotalPackages)
	if rep.Coverage != wantCoverage {
		t.Fatalf("Coverage field (%.6f) does not match instrumented/total (%.6f)", rep.Coverage, wantCoverage)
	}
	t.Logf("real production telemetry.StartSpan coverage: %d/%d packages (%.1f%%)",
		len(rep.InstrumentedPackages), rep.TotalPackages, rep.Coverage*100)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestMeasureDetectsARealCallSite is the adversarial proof this
// package actually measures what it claims: given a synthetic tree
// with one instrumented package and one uninstrumented package,
// Measure must attribute coverage to exactly the right one, not just
// report a plausible-looking number.
func TestMeasureDetectsARealCallSite(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pkg", "moat", "withspan", "engine.go"),
		"package withspan\n\nimport \"veriqo/pkg/platform/telemetry\"\n\nfunc Run() {\n\ttelemetry.StartSpan(nil, \"run\")\n}\n")
	writeFile(t, filepath.Join(dir, "pkg", "moat", "withoutspan", "engine.go"),
		"package withoutspan\n\nfunc Run() {}\n")

	rep, err := Measure(dir, []string{"pkg/moat"})
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if rep.TotalPackages != 2 {
		t.Fatalf("expected 2 packages scanned, got %d: %+v", rep.TotalPackages, rep)
	}
	if len(rep.InstrumentedPackages) != 1 {
		t.Fatalf("expected exactly 1 instrumented package, got %d: %+v", len(rep.InstrumentedPackages), rep)
	}
	if !contains(rep.InstrumentedPackages, filepath.Join(dir, "pkg", "moat", "withspan")) {
		t.Fatalf("expected the withspan package to be the instrumented one, got %+v", rep.InstrumentedPackages)
	}
	if rep.Coverage != 0.5 {
		t.Fatalf("expected 0.5 coverage (1/2), got %.4f", rep.Coverage)
	}
}

// TestMeasureIgnoresTestFiles proves a call site that exists ONLY in a
// _test.go file does not count as production instrumentation -- a test
// helper calling StartSpan says nothing about whether the domain
// engine itself is instrumented.
func TestMeasureIgnoresTestFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pkg", "moat", "onlytested", "engine.go"),
		"package onlytested\n\nfunc Run() {}\n")
	writeFile(t, filepath.Join(dir, "pkg", "moat", "onlytested", "engine_test.go"),
		"package onlytested\n\nimport \"veriqo/pkg/platform/telemetry\"\n\nfunc helper() { telemetry.StartSpan(nil, \"x\") }\n")

	rep, err := Measure(dir, []string{"pkg/moat"})
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if len(rep.InstrumentedPackages) != 0 {
		t.Fatalf("a call site only in a _test.go file must not count as production instrumentation, got %+v",
			rep.InstrumentedPackages)
	}
}

// TestMeasureIgnoresCommentedMentions proves a mention inside a
// comment (documentation referencing the function, a TODO, a
// commented-out call) does not count as a real call site.
func TestMeasureIgnoresCommentedMentions(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pkg", "moat", "onlycommented", "engine.go"),
		"package onlycommented\n\n// TODO: wire in telemetry.StartSpan( here eventually\nfunc Run() {}\n")

	rep, err := Measure(dir, []string{"pkg/moat"})
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if len(rep.InstrumentedPackages) != 0 {
		t.Fatalf("a commented-out mention must not count as a real call site, got %+v", rep.InstrumentedPackages)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
