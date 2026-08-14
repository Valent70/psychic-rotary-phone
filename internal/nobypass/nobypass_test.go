package nobypass

import (
	"os"
	"path/filepath"
	"testing"
)

// TestArbitrationEngineIsOnlyConstructedThroughTheFacade is the audit's
// own acceptance criterion made mechanical: "Tidak boleh ada bypass."
// This is the property that matters -- it fails the build the moment
// ANY future caller (not just the three named by the audit) constructs
// contradiction.ArbitrationEngine directly instead of going through
// pkg/evidence/api.Facade.
func TestArbitrationEngineIsOnlyConstructedThroughTheFacade(t *testing.T) {
	root := repoRoot(t)
	rep, err := Check(root)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(rep.Violations) != 0 {
		t.Fatalf("found %d unauthorized direct construction(s) of contradiction.ArbitrationEngine "+
			"(bypassing pkg/evidence/api.Facade), violating audit item P0-A's \"Tidak boleh ada bypass\": %v",
			len(rep.Violations), rep.Violations)
	}
	if rep.ScannedFiles < 100 {
		t.Fatalf("expected to scan the real repo (hundreds of .go files), only scanned %d -- repoRoot likely wrong", rep.ScannedFiles)
	}
}

// TestCheckDetectsARealViolation is the adversarial proof this checker
// actually works, not just a grep that happens to find nothing today:
// a synthetic tree with an unauthorized construction site must be
// caught.
func TestCheckDetectsARealViolation(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "pkg", "sneaky", "bypass.go"), `package sneaky

import "veriqo/pkg/moat/contradiction"

func New() *contradiction.ArbitrationEngine {
	return contradiction.NewArbitrationEngine()
}
`)
	rep, err := Check(dir)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(rep.Violations) != 1 || rep.Violations[0] != "pkg/sneaky/bypass.go" {
		t.Fatalf("expected exactly one detected violation at pkg/sneaky/bypass.go, got %v", rep.Violations)
	}
}

// TestCheckAllowsTheTwoAuditedExemptions proves the two deliberate
// exemptions (the facade itself, and IVF's independent replay) do NOT
// trip the checker -- the negative-space proof that AllowedFiles is
// doing real filtering, not accidentally matching everything.
func TestCheckAllowsTheTwoAuditedExemptions(t *testing.T) {
	dir := t.TempDir()
	body := `package p

import "veriqo/pkg/moat/contradiction"

func f() *contradiction.ArbitrationEngine { return contradiction.NewArbitrationEngine() }
`
	mustWriteFile(t, filepath.Join(dir, "pkg", "evidence", "api", "api.go"), body)
	mustWriteFile(t, filepath.Join(dir, "pkg", "engine", "replay.go"), body)

	rep, err := Check(dir)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(rep.Violations) != 0 {
		t.Fatalf("expected the two audited exemptions to be allowed, got violations: %v", rep.Violations)
	}
}

// TestCheckIgnoresCommentedMentions mirrors
// internal/telemetrycoverage's own adversarial proof for the identical
// class of false positive: a call mentioned only in a comment must not
// be flagged.
func TestCheckIgnoresCommentedMentions(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "pkg", "docs", "notes.go"), `package docs

// Historically this called contradiction.NewArbitrationEngine() directly.
func f() {}
`)
	rep, err := Check(dir)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(rep.Violations) != 0 {
		t.Fatalf("expected a comment-only mention not to be flagged, got %v", rep.Violations)
	}
}

// TestCheckIgnoresTestFiles proves _test.go files (which legitimately
// construct engines directly for unit testing pkg/moat/contradiction
// itself) are excluded from the scan.
func TestCheckIgnoresTestFiles(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "pkg", "moat", "contradiction", "arbitration_test.go"), `package contradiction

func f() *ArbitrationEngine { return NewArbitrationEngine() }
`)
	rep, err := Check(dir)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(rep.Violations) != 0 {
		t.Fatalf("expected _test.go files to be excluded, got %v", rep.Violations)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// repoRoot finds the real repository root by walking up from this test
// file's own working directory until go.mod is found -- needed because
// `go test` sets the working directory to this package's own directory
// (internal/nobypass), but the whole-repo scan test must run against
// the real tree root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (go.mod) walking up from test working directory")
		}
		dir = parent
	}
}
