package determinism

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRealDeterministicPackagesAreClean is the actual CI gate: run
// against this repository's real source, it must find zero violations
// today. If it ever fails, that is a real regression -- a
// nondeterministic call was added to a package this repo's own
// documentation claims is deterministic.
func TestRealDeterministicPackagesAreClean(t *testing.T) {
	violations, err := Check("../..")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	for _, v := range violations {
		t.Errorf("%s", v)
	}
}

// TestCheckDetectsAPlantedViolation is the adversarial case: prove the
// checker actually catches a forbidden call, not just that it currently
// reports zero on a clean tree (which a checker that always returns
// nil would also do).
func TestCheckDetectsAPlantedViolation(t *testing.T) {
	root := t.TempDir()
	pkgDir := filepath.Join(root, "pkg", "canonical")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	planted := "package canonical\n\nimport \"time\"\n\nfunc bad() { _ = time.Now() }\n"
	if err := os.WriteFile(filepath.Join(pkgDir, "planted.go"), []byte(planted), 0o600); err != nil {
		t.Fatal(err)
	}
	// The other two declared deterministic packages must exist too, or
	// Check fails on a missing directory before it gets to scan
	// anything.
	for _, pkg := range []string{"pkg/execution", "pkg/replay"} {
		if err := os.MkdirAll(filepath.Join(root, pkg), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	violations, err := Check(root)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected exactly 1 planted violation, got %d: %v", len(violations), violations)
	}
	if violations[0].Pattern != "time.Now(" {
		t.Fatalf("expected the planted time.Now( to be caught, got %+v", violations[0])
	}
}

// TestCheckIgnoresTestFiles confirms _test.go files (which legitimately
// use time.Now() for fixture timestamps, deadlines, etc.) are not
// scanned -- the boundary is about the deterministic PRODUCTION path,
// not test scaffolding.
func TestCheckIgnoresTestFiles(t *testing.T) {
	root := t.TempDir()
	for _, pkg := range DeterministicPackages {
		if err := os.MkdirAll(filepath.Join(root, pkg), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	testFile := "package canonical\n\nimport \"time\"\n\nfunc TestSomething() { _ = time.Now() }\n"
	if err := os.WriteFile(filepath.Join(root, "pkg/canonical", "x_test.go"), []byte(testFile), 0o600); err != nil {
		t.Fatal(err)
	}

	violations, err := Check(root)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected _test.go files to be ignored, got %v", violations)
	}
}

// TestCheckIgnoresCommentedMentions confirms a doc comment that merely
// NAMES a forbidden pattern (as this package's own source does,
// extensively) is not itself flagged as a violation.
func TestCheckIgnoresCommentedMentions(t *testing.T) {
	root := t.TempDir()
	for _, pkg := range DeterministicPackages {
		if err := os.MkdirAll(filepath.Join(root, pkg), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	commented := "package canonical\n\n// This package must never call time.Now() directly.\nfunc ok() {}\n"
	if err := os.WriteFile(filepath.Join(root, "pkg/canonical", "doc.go"), []byte(commented), 0o600); err != nil {
		t.Fatal(err)
	}

	violations, err := Check(root)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected a comment mentioning time.Now() to be ignored, got %v", violations)
	}
}
