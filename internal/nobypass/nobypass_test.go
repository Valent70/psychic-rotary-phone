package nobypass

import (
	"os"
	"path/filepath"
	"testing"
)

// TestArbitrationEngineIsOnlyConstructedThroughTheFacade is the audit's
// own acceptance criterion made mechanical: "Tidak boleh ada bypass."
// This is the property that matters -- it fails the build the moment
// ANY future caller (not just the ones named by the audit) constructs
// contradiction.ArbitrationEngine OR fusion.Engine directly instead of
// going through pkg/evidence/api.Facade.
func TestArbitrationEngineIsOnlyConstructedThroughTheFacade(t *testing.T) {
	root := repoRoot(t)
	rep, err := Check(root)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(rep.Violations) != 0 {
		t.Fatalf("found %d unauthorized direct engine construction(s) (bypassing pkg/evidence/api.Facade), "+
			"violating audit item P0-A's \"Tidak boleh ada bypass\": %v",
			len(rep.Violations), rep.Violations)
	}
	if rep.ScannedFiles < 100 {
		t.Fatalf("expected to scan the real repo (hundreds of .go files), only scanned %d -- repoRoot likely wrong", rep.ScannedFiles)
	}
}

// TestCheckDetectsARealViolation is the adversarial proof this checker
// actually works, not just a grep that happens to find nothing today:
// a synthetic tree with an unauthorized construction site must be
// caught, for each of the two guarded constructors.
func TestCheckDetectsARealViolation(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "pkg", "sneaky", "bypass.go"), `package sneaky

import "veriqo/pkg/moat/contradiction"

func New() *contradiction.ArbitrationEngine {
	return contradiction.NewArbitrationEngine()
}
`)
	mustWriteFile(t, filepath.Join(dir, "pkg", "sneaky2", "bypass.go"), `package sneaky2

import "veriqo/pkg/moat/fusion"

func New() *fusion.Engine {
	return fusion.NewEngine(nil)
}
`)
	mustWriteFile(t, filepath.Join(dir, "pkg", "sneaky3", "bypass.go"), `package sneaky3

import "veriqo/pkg/canonical"

func New() *canonical.Pipeline {
	return canonical.NewPipeline(nil)
}
`)
	rep, err := Check(dir)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	want := []string{
		"pkg/sneaky/bypass.go: contradiction.ArbitrationEngine",
		"pkg/sneaky2/bypass.go: fusion.Engine",
		"pkg/sneaky3/bypass.go: canonical.Pipeline",
	}
	if len(rep.Violations) != 3 || rep.Violations[0] != want[0] || rep.Violations[1] != want[1] || rep.Violations[2] != want[2] {
		t.Fatalf("expected exactly %v, got %v", want, rep.Violations)
	}
}

// TestCheckAllowsTheAuditedExemptions proves the deliberate exemptions
// for each guarded constructor (the facade itself, IVF's independent
// replay, and fusion's demo-binary exemption) do NOT trip the checker --
// the negative-space proof that allowedFiles is doing real filtering,
// not accidentally matching everything.
func TestCheckAllowsTheAuditedExemptions(t *testing.T) {
	dir := t.TempDir()
	arbBody := `package p

import "veriqo/pkg/moat/contradiction"

func f() *contradiction.ArbitrationEngine { return contradiction.NewArbitrationEngine() }
`
	fusionBody := `package p

import "veriqo/pkg/moat/fusion"

func f() *fusion.Engine { return fusion.NewEngine(nil) }
`
	pipelineBody := `package p

import "veriqo/pkg/canonical"

func f() *canonical.Pipeline { return canonical.NewPipeline(nil) }
`
	mustWriteFile(t, filepath.Join(dir, "pkg", "evidence", "api", "api.go"), arbBody+pipelineBody)
	mustWriteFile(t, filepath.Join(dir, "pkg", "engine", "replay.go"), arbBody+fusionBody)
	mustWriteFile(t, filepath.Join(dir, "pkg", "canonical", "canonical.go"), fusionBody)
	mustWriteFile(t, filepath.Join(dir, "cmd", "veriqo-demo", "main.go"), fusionBody)
	mustWriteFile(t, filepath.Join(dir, "veriqo", "kernel", "kernel.go"), pipelineBody)
	mustWriteFile(t, filepath.Join(dir, "pkg", "replay", "replay.go"), pipelineBody)
	mustWriteFile(t, filepath.Join(dir, "pkg", "lifecycle", "lifecycle.go"), pipelineBody)
	mustWriteFile(t, filepath.Join(dir, "pkg", "execution", "execution.go"), pipelineBody)

	rep, err := Check(dir)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(rep.Violations) != 0 {
		t.Fatalf("expected the audited exemptions to be allowed, got violations: %v", rep.Violations)
	}
}

// TestCheckIgnoresCommentedMentions mirrors
// internal/telemetrycoverage's own adversarial proof for the identical
// class of false positive: a call mentioned only in a comment must not
// be flagged.
func TestCheckIgnoresCommentedMentions(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "pkg", "docs", "notes.go"), `package docs

// Historically this called contradiction.NewArbitrationEngine() and
// fusion.NewEngine() directly.
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

// --- PHASE A (P0-1): canonical evidence production coverage ----------

// TestEvidenceProductionCoverageIsCleanOnTheRealTree is the gate's own
// assertion, run as a test so a violation breaks the build rather than
// waiting for the next readiness run.
func TestEvidenceProductionCoverageIsCleanOnTheRealTree(t *testing.T) {
	cov, err := EvidenceProductionCoverage(repoRoot(t), "veriqo.evidence.contract/v1", "sha256:test")
	if err != nil {
		t.Fatalf("EvidenceProductionCoverage: %v", err)
	}
	if cov.ScannedFiles < 100 {
		t.Fatalf("scanned only %d files -- a pass would be meaningless", cov.ScannedFiles)
	}
	if cov.UnauthorizedEvidenceWriters != 0 {
		t.Errorf("unauthorized evidence writers = %d: %v", cov.UnauthorizedEvidenceWriters, cov.WriterViolations)
	}
	if cov.UnauthorizedIngestionPaths != 0 {
		t.Errorf("unauthorized ingestion paths = %d: %v", cov.UnauthorizedIngestionPaths, cov.IngestionViolations)
	}
	if !cov.Pass() {
		t.Fatal("coverage does not pass its own condition")
	}
	if len(cov.AuditedAdapters) < 6 {
		t.Errorf("audited adapters = %v; the five connector adapters plus the facade is six", cov.AuditedAdapters)
	}
}

// TestEvidenceProductionCoverageFailsWithoutADeclaredContract is the
// fail-closed half: the program requires the contract version to be
// DECLARED, and an undeclared contract cannot be honoured.
func TestEvidenceProductionCoverageFailsWithoutADeclaredContract(t *testing.T) {
	root := repoRoot(t)
	for _, tc := range []struct{ version, hash string }{
		{"", "sha256:test"},
		{"veriqo.evidence.contract/v1", ""},
		{"", ""},
	} {
		cov, err := EvidenceProductionCoverage(root, tc.version, tc.hash)
		if err != nil {
			t.Fatalf("EvidenceProductionCoverage: %v", err)
		}
		if cov.Pass() {
			t.Errorf("coverage passed with version=%q hash=%q", tc.version, tc.hash)
		}
	}
}

// TestEvidenceProductionCoverageDetectsAnInjectedIngestionPath proves
// the ingestion scan actually scans, rather than passing because it
// never finds anything.
func TestEvidenceProductionCoverageDetectsAnInjectedIngestionPath(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "pkg", "rogue")
	if err := os.MkdirAll(pkgDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	src := "package rogue\n\nimport \"veriqo/pkg/evidence/ontology\"\n\n" +
		"func Ingest() { _, _ = ontology.New(ontology.Evidence{}) }\n"
	if err := os.WriteFile(filepath.Join(pkgDir, "rogue.go"), []byte(src), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cov, err := EvidenceProductionCoverage(dir, "v1", "sha256:x")
	if err != nil {
		t.Fatalf("EvidenceProductionCoverage: %v", err)
	}
	if cov.UnauthorizedIngestionPaths == 0 {
		t.Fatal("an injected ingestion path was not detected")
	}
	if cov.Pass() {
		t.Fatal("coverage passed with a detected unauthorized ingestion path")
	}
}
