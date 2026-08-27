package entrypoints

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// repoRoot resolves this package's own repository root, so the tests
// scan the REAL tree rather than a fixture.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root %s does not contain go.mod: %v", root, err)
	}
	return root
}

// TestNoGovernedDecisionOutsideCanonicalExecution is PHASE C (P0-3)'s
// named acceptance test, stated exactly as the program states it: it
// PASSES only if parallel governed execution paths = 0.
//
// It is a NEGATIVE architecture test, not an integration test: it does
// not assert that the canonical path works (dozens of other tests do
// that), it asserts that no OTHER path to a governed decision exists
// anywhere in the real source tree.
func TestNoGovernedDecisionOutsideCanonicalExecution(t *testing.T) {
	rep, err := Audit(repoRoot(t))
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if rep.ScannedFiles < 100 {
		t.Fatalf("scanned only %d files -- the walk is not reaching the real tree, so a pass would be meaningless", rep.ScannedFiles)
	}
	if rep.ParallelGovernedExecutionPaths != 0 {
		for _, v := range rep.Violations {
			t.Errorf("parallel governed execution path: %s constructs %s\n  why this matters: %s", v.File, v.Marker, v.Reason)
		}
		t.Fatalf("parallel governed execution paths = %d, must be 0", rep.ParallelGovernedExecutionPaths)
	}
	if len(rep.MatrixErrors) != 0 {
		for _, e := range rep.MatrixErrors {
			t.Errorf("entrypoint matrix contradicted by the source: %s", e)
		}
		t.Fatal("the entrypoint matrix claims something the source does not support")
	}
	if !rep.Clean() {
		t.Fatal("Clean() disagrees with its own components")
	}
}

// TestEveryEntrypointKindIsAccountedFor makes the matrix's coverage
// checkable rather than a list someone hoped was complete: every kind
// the program enumerates must appear at least once, so a kind with no
// entrypoint at all is a deliberate, visible statement rather than an
// oversight.
func TestEveryEntrypointKindIsAccountedFor(t *testing.T) {
	present := map[EntrypointKind]bool{}
	for _, e := range Matrix() {
		present[e.Kind] = true
	}
	for _, k := range []EntrypointKind{
		KindHTTP, KindCLI, KindBatch, KindWorker, KindScheduler,
		KindAdminAPI, KindReplay, KindCompatibilityAPI, KindInternalJob,
	} {
		if !present[k] {
			t.Errorf("entrypoint kind %s has no row in the matrix -- an unenumerated entrypoint class is a back door nobody checked", k)
		}
	}
}

// TestGovernedDecisionEntrypointsAreExactlyTheAuditedSet is the
// structural statement PHASE C is really making. If this set ever
// changes, it should change in a diff someone reviewed, not silently.
//
// It was previously a count ("must be exactly 1"). It is now an exact
// SET, which is strictly stronger: a count of 1 could have been kept
// while swapping WHICH entrypoint is governed, and this cannot. The set
// grew to two in round R22 when cmd/veriqo-rwc-v2 was registered: it
// reaches pkg/lifecycle.Orchestrator.RunUnified through pkg/rwc.Run, so
// it genuinely originates governed decisions and recording it as a
// non-governed harness would have been false. It shares the SAME single
// execution engine -- the execution.Engine marker in this package
// independently proves no second one was constructed for it.
func TestGovernedDecisionEntrypointsAreExactlyTheAuditedSet(t *testing.T) {
	want := map[string]bool{
		"HTTP:POST /lifecycle/run_unified": true,
		"CLI:cmd/veriqo-rwc-v2":            true,
	}
	rep, err := Audit(repoRoot(t))
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	got := map[string]bool{}
	for _, e := range rep.Entrypoints {
		if e.GovernedDecisions {
			got[string(e.Kind)+":"+e.Name] = true
		}
	}
	for name := range want {
		if !got[name] {
			t.Errorf("audited governed-decision entrypoint %q is no longer marked governed", name)
		}
	}
	for name := range got {
		if !want[name] {
			t.Errorf("UNAUDITED governed-decision entrypoint %q appeared; a new path that "+
				"originates governed decisions must be reviewed, not merely listed", name)
		}
	}
	if rep.GovernedEntrypoints != len(want) {
		var names []string
		for name := range got {
			names = append(names, name)
		}
		sort.Strings(names)
		t.Fatalf("governed-decision entrypoints = %d (%s), want exactly %d",
			rep.GovernedEntrypoints, strings.Join(names, ", "), len(want))
	}
}

// TestEveryMatrixRowNamesARealFile stops the matrix decaying into
// documentation about files that were moved or deleted.
func TestEveryMatrixRowNamesARealFile(t *testing.T) {
	root := repoRoot(t)
	for _, e := range Matrix() {
		if _, err := os.Stat(filepath.Join(root, e.File)); err != nil {
			t.Errorf("%s (%s): declared file %s does not exist: %v", e.Name, e.Kind, e.File, err)
		}
		if strings.TrimSpace(e.CanonicalPath) == "" {
			t.Errorf("%s (%s): no canonical path or reason recorded -- every entrypoint must say either how it reaches a governed decision or why it never does", e.Name, e.Kind)
		}
	}
}

// TestAuditDetectsAnInjectedBackDoor proves the scanner actually works,
// rather than passing because it never finds anything. A temporary
// copy of the tree gets a file that constructs a second execution
// engine, and the audit must catch it.
func TestAuditDetectsAnInjectedBackDoor(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "pkg", "sneaky")
	if err := os.MkdirAll(pkgDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	backdoor := "package sneaky\n\nimport \"veriqo/pkg/execution\"\n\n" +
		"func Decide() { _ = execution.NewEngine(nil) }\n"
	if err := os.WriteFile(filepath.Join(pkgDir, "sneaky.go"), []byte(backdoor), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	rep, err := Audit(dir)
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if rep.ParallelGovernedExecutionPaths == 0 {
		t.Fatal("an injected second execution engine was not detected -- the scanner is not actually scanning")
	}
	if rep.Clean() {
		t.Fatal("Clean() returned true with a detected back door")
	}
	found := false
	for _, v := range rep.Violations {
		if strings.Contains(v.File, "sneaky") && v.Marker == "execution.Engine" {
			found = true
			if v.Reason == "" {
				t.Error("a violation with no reason is not actionable")
			}
		}
	}
	if !found {
		t.Fatalf("violations %v do not name the injected file", rep.Violations)
	}
}

// TestAuditDetectsAMatrixRowThatLiesAboutItsPath proves the second
// half works too: a governed-decision row pointing at a file that does
// not take the canonical path must fail, not be believed.
func TestAuditDetectsAMatrixRowThatLiesAboutItsPath(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "veriqo", "gateway", "rest")
	if err := os.MkdirAll(target, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A file at the exact path the matrix's one governed row names, but
	// which never calls RunUnified.
	if err := os.WriteFile(filepath.Join(target, "lifecycle_route.go"),
		[]byte("package rest\n\nfunc handler() {}\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	rep, err := Audit(dir)
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if len(rep.MatrixErrors) == 0 {
		t.Fatal("a matrix row claiming a canonical path its file does not take was accepted")
	}
	if rep.Clean() {
		t.Fatal("Clean() returned true with matrix errors present")
	}
}

// TestScannerIgnoresCommentedOutCalls records the deliberate,
// documented limitation and its boundary: a mention inside a line
// comment is not a call, and treating it as one would make every
// explanatory comment in this repository a violation.
func TestScannerIgnoresCommentedOutCalls(t *testing.T) {
	if hasRealCall("// see execution.NewEngine( for details", "execution.NewEngine(") {
		t.Error("a line comment was treated as a real call")
	}
	if !hasRealCall("\te := execution.NewEngine(nil) // build it", "execution.NewEngine(") {
		t.Error("a real call with a trailing comment was missed")
	}
}
