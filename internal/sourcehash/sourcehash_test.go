package sourcehash

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestChangingOneFileChangesTheRootHash(t *testing.T) {
	base := writeTree(t, map[string]string{
		"main.go":    "package main\nfunc main() {}\n",
		"pkg/x/x.go": "package x\n",
		"README.md":  "hello\n",
	})
	before, err := Compute(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "pkg/x/x.go"), []byte("package x\n// changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := Compute(base)
	if err != nil {
		t.Fatal(err)
	}
	if before.RootHash == after.RootHash {
		t.Fatal("editing one production file must change the source-tree root hash")
	}
	if before.FileCount != after.FileCount {
		t.Fatalf("file count should be unchanged by an edit, got %d vs %d", before.FileCount, after.FileCount)
	}
}

func TestGitDirectoryIsExcluded(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"main.go":         "package main\n",
		".git/HEAD":       "ref: refs/heads/main\n",
		".git/objects/aa": "binary-ish content",
	})
	res, err := Compute(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Files {
		if f.Path == ".git/HEAD" || f.Path == ".git/objects/aa" {
			t.Fatalf(".git contents must never be hashed, got %s", f.Path)
		}
	}
	if res.FileCount != 1 {
		t.Fatalf("expected exactly 1 hashed file (main.go), got %d: %+v", res.FileCount, res.Files)
	}
}

func TestClaudeDirectoryIsExcludedSoHarnessStateNeverAffectsTheHash(t *testing.T) {
	// Regression test for a real, reproduced bug: a long-running Claude
	// Code session accumulates .claude/ harness state (e.g. a scheduled
	// task lock) in the working tree, but that directory is
	// .gitignore'd and never part of what a fresh `git clone` or
	// `git archive` of the same commit produces. Before .claude/ was
	// added to DefaultExclusions, cmd/veriqo-verify-release-identity
	// passed against the working tree (whose Compute included the
	// stray file) but failed against an archive of the exact same
	// commit -- the source hash was not actually a function of the
	// committed source, only of incidental session-local clutter.
	dir := writeTree(t, map[string]string{
		"main.go":                      "package main\n",
		".claude/scheduled_tasks.lock": "pid=12345\n",
	})
	res, err := Compute(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Files {
		if f.Path == ".claude/scheduled_tasks.lock" {
			t.Fatalf(".claude contents must never be hashed, got %s", f.Path)
		}
	}
	if res.FileCount != 1 {
		t.Fatalf("expected exactly 1 hashed file (main.go), got %d: %+v", res.FileCount, res.Files)
	}
}

func TestEvidenceDirectoryIsExcludedSoReRunningGatesDoesNotChangeTheHash(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"main.go":                 "package main\n",
		"evidence/build.txt":      "run 1 output\n",
		"READINESS_MANIFEST.json": `{"root_hash":"whatever"}`,
	})
	before, err := Compute(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "evidence/build.txt"), []byte("run 2, different output entirely\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := Compute(dir)
	if err != nil {
		t.Fatal(err)
	}
	if before.RootHash != after.RootHash {
		t.Fatal("re-running gates (which rewrites evidence/*) must not change the source-tree hash of an unchanged commit")
	}
}

func TestRenamingAFileChangesTheHashEvenWithIdenticalContent(t *testing.T) {
	a := writeTree(t, map[string]string{"a/file.go": "package a\n"})
	b := writeTree(t, map[string]string{"b/file.go": "package a\n"})
	ra, err := Compute(a)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := Compute(b)
	if err != nil {
		t.Fatal(err)
	}
	if ra.RootHash == rb.RootHash {
		t.Fatal("the path is part of the hash preimage, so a rename must change the root hash")
	}
}

func TestHashIsDeterministicAcrossRepeatedRuns(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"z.go": "package z\n", "a.go": "package a\n", "m/n.go": "package n\n",
	})
	r1, err := Compute(dir)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := Compute(dir)
	if err != nil {
		t.Fatal(err)
	}
	if r1.RootHash != r2.RootHash {
		t.Fatal("hashing the same unchanged tree twice must produce the same root hash")
	}
}

func TestEmptyTreeProducesAStableHash(t *testing.T) {
	dir := t.TempDir()
	res, err := Compute(dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.FileCount != 0 || res.RootHash == "" {
		t.Fatalf("an empty tree must still produce a defined (empty-set) root hash, got %+v", res)
	}
}
