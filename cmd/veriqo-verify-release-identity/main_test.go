package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"veriqo/internal/assurance"
	"veriqo/internal/sourcehash"
)

func writeManifest(t *testing.T, path string, cert assurance.ReleaseCertificate) {
	t.Helper()
	m := assurance.ReadinessManifest{Release: cert}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil { // #nosec G306 -- test fixture, not production key material
		t.Fatalf("write manifest: %v", err)
	}
}

func TestRunSourceHashPass(t *testing.T) {
	sourceDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}
	res, err := sourcehash.Compute(sourceDir)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	manifestPath := filepath.Join(t.TempDir(), "READINESS_MANIFEST.json")
	writeManifest(t, manifestPath, assurance.ReleaseCertificate{Version: "vTest", GitCommit: "deadbeef", SourceHash: res.RootHash})

	code := run([]string{"-manifest", manifestPath, "-source-dir", sourceDir})
	if code != 0 {
		t.Fatalf("expected PASS (exit 0), got exit %d", code)
	}
}

func TestRunSourceHashFailOnMismatch(t *testing.T) {
	sourceDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}
	manifestPath := filepath.Join(t.TempDir(), "READINESS_MANIFEST.json")
	writeManifest(t, manifestPath, assurance.ReleaseCertificate{Version: "vTest", GitCommit: "deadbeef", SourceHash: "not-the-real-hash"})

	code := run([]string{"-manifest", manifestPath, "-source-dir", sourceDir})
	if code != 1 {
		t.Fatalf("expected FAIL (exit 1) for a genuinely wrong source_hash, got exit %d", code)
	}
}

// TestRunGitModeNormalizationMakesHashReproducible is the direct
// regression proof for the real bug this tool's own development
// surfaced: a file that is chmod 0600 in one checkout (as
// cmd/veriqo-readiness writes engine_registry.json) must still produce
// the SAME source hash as the identical git blob checked out fresh
// elsewhere at 0644, because git itself cannot represent that
// distinction. Before gitMode() normalization in internal/sourcehash,
// this test would fail.
func TestRunGitModeNormalizationMakesHashReproducible(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	content := []byte("package a\nfunc F() {}\n")
	if err := os.WriteFile(filepath.Join(dirA, "a.go"), content, 0o600); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dirB, "a.go"), content, 0o644); err != nil {
		t.Fatalf("write b: %v", err)
	}
	resA, err := sourcehash.Compute(dirA)
	if err != nil {
		t.Fatalf("Compute(dirA): %v", err)
	}
	resB, err := sourcehash.Compute(dirB)
	if err != nil {
		t.Fatalf("Compute(dirB): %v", err)
	}
	if resA.RootHash != resB.RootHash {
		t.Fatalf("source hash must be identical for byte-identical content regardless of local chmod (git cannot represent 0600 vs 0644): got %s vs %s", resA.RootHash, resB.RootHash)
	}
}

// gitRepo builds a real, throwaway git repository for the commit-
// lineage tests below, since that reconciliation path genuinely shells
// out to `git diff` and `git archive` -- there is no way to prove it
// correct without a real repo.
func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...) // #nosec G204 -- fixed args, test setup only
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	return dir
}

func commitFile(t *testing.T, repo, relPath, content, msg string) string {
	t.Helper()
	full := filepath.Join(repo, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
	cmd := exec.Command("git", "-C", repo, "add", relPath) // #nosec G204 -- fixed subcommand, test setup only
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	cmd = exec.Command("git", "-C", repo, "commit", "-q", "-m", msg, // #nosec G204 -- test setup only
		"--author=t <t@t.com>")
	cmd.Env = append(os.Environ(), "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output() // #nosec G204 -- fixed args, test setup only
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}
	return trimNL(string(out))
}

func trimNL(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

func archiveZip(t *testing.T, repo, commit, out string) {
	t.Helper()
	cmd := exec.Command("git", "-C", repo, "archive", "--format=zip", "-o", out, commit) // #nosec G204 -- test setup only
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git archive: %v\n%s", err, o)
	}
}

// TestRunCommitLineageExactMatch proves the trivial case: the zip's
// embedded commit equals the manifest's git_commit exactly.
func TestRunCommitLineageExactMatch(t *testing.T) {
	repo := gitRepo(t)
	commit := commitFile(t, repo, "a.go", "package a\n", "init")

	res, err := sourcehash.Compute(repo)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	manifestPath := filepath.Join(repo, "READINESS_MANIFEST.json")
	writeManifest(t, manifestPath, assurance.ReleaseCertificate{Version: "vTest", GitCommit: commit, SourceHash: res.RootHash})

	zipPath := filepath.Join(t.TempDir(), "out.zip")
	archiveZip(t, repo, commit, zipPath)

	code := run([]string{"-manifest", manifestPath, "-source-dir", repo, "-zip", zipPath, "-repo-dir", repo})
	if code != 0 {
		t.Fatalf("expected PASS for an exact commit match, got exit %d", code)
	}
}

// TestRunCommitLineageBenignReconciliation is the direct proof for
// this tool's whole reason to exist: a manifest necessarily records
// the commit that existed BEFORE the manifest file itself was
// committed, so the shipped zip's commit can never literally equal
// manifest.git_commit. This must PASS (not fail) when the only files
// that differ between the two commits are declared self-referential
// exclusions.
func TestRunCommitLineageBenignReconciliation(t *testing.T) {
	repo := gitRepo(t)
	preManifestCommit := commitFile(t, repo, "a.go", "package a\n", "source only")

	res, err := sourcehash.Compute(repo)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	manifestPath := filepath.Join(repo, "READINESS_MANIFEST.json")
	writeManifest(t, manifestPath, assurance.ReleaseCertificate{Version: "vTest", GitCommit: preManifestCommit, SourceHash: res.RootHash})

	// Committing the manifest itself is exactly the kind of self-
	// referential-only diff this test exists to prove is benign.
	postManifestCommit := commitFile(t, repo, "READINESS_MANIFEST.json", mustRead(t, manifestPath), "add manifest")

	zipPath := filepath.Join(t.TempDir(), "out.zip")
	archiveZip(t, repo, postManifestCommit, zipPath)

	code := run([]string{"-manifest", manifestPath, "-source-dir", repo, "-zip", zipPath, "-repo-dir", repo})
	if code != 0 {
		t.Fatalf("expected PASS (benign reconciliation) when only the manifest itself differs between commits, got exit %d", code)
	}
}

// TestRunCommitLineageRealMismatchFails proves the negative: if a
// NON-excluded file differs between the manifest's claimed commit and
// the zip's actual commit, that is a genuine integrity problem and
// must fail, not be waved through as "probably fine."
func TestRunCommitLineageRealMismatchFails(t *testing.T) {
	repo := gitRepo(t)
	commit1 := commitFile(t, repo, "a.go", "package a\n", "v1")

	res, err := sourcehash.Compute(repo)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	manifestPath := filepath.Join(repo, "READINESS_MANIFEST.json")
	writeManifest(t, manifestPath, assurance.ReleaseCertificate{Version: "vTest", GitCommit: commit1, SourceHash: res.RootHash})

	// A genuine, non-excluded source change -- e.g. a substituted
	// production file -- between the claimed and shipped commits.
	commit2 := commitFile(t, repo, "b.go", "package a\nfunc Tampered() {}\n", "unexpected real change")

	zipPath := filepath.Join(t.TempDir(), "out.zip")
	archiveZip(t, repo, commit2, zipPath)

	code := run([]string{"-manifest", manifestPath, "-source-dir", repo, "-zip", zipPath, "-repo-dir", repo})
	if code != 1 {
		t.Fatalf("expected FAIL for a real, non-excluded content mismatch between manifest commit and shipped zip commit, got exit %d", code)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) // #nosec G304 -- test fixture path constructed by the test itself
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
