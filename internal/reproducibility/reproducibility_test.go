// Package reproducibility closes the one genuinely internal-only slice
// of an external audit's "Reproducible build + container provenance"
// gap (P1): "independent builder, binary equality, container digest,
// base-image SBOM, OS package inventory, build attestation."
//
// Of those six, only "binary equality" is provable without external
// infrastructure this sandbox does not have -- a real second build
// environment for "independent builder", a Docker daemon and registry
// access for container digest/base-image SBOM/OS package inventory,
// and (for a REAL build-attestation scheme like SLSA/in-toto) a
// production key-management and CI-identity setup this project's own
// pkg/blockers/hsmkms and pkg/blockers/pentest already document as
// external-blocked for the same underlying reason. This package proves
// the one piece that genuinely does not require any of that: that this
// repository's own build, using this repository's own toolchain and
// flags, produces bit-for-bit identical binaries across two
// INDEPENDENT compilations -- not two builds sharing Go's build cache
// (which would trivially "reproduce" without proving anything;
// TestBinaryEqualityAcrossIndependentBuilds deliberately points GOCACHE
// at two separate, freshly-created directories per build to rule that
// out), but two genuinely separate compiler invocations from the same
// source tree.
package reproducibility

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// buildOnce compiles pkg into outPath using its own fresh, empty
// GOCACHE directory (so this build shares NO cached object files with
// any other build in this test, including a prior call to buildOnce
// itself) and returns the resulting binary's SHA-256.
func buildOnce(t *testing.T, repoRoot, pkg, outPath string) string {
	t.Helper()
	cache := t.TempDir()
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", outPath, pkg) // #nosec G204 -- fixed 'go build' invocation against this repo's own packages, not untrusted input
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "GOCACHE="+cache)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build %s: %v\n%s", pkg, err, out)
	}
	f, err := os.Open(outPath) // #nosec G304 -- outPath is this test's own t.TempDir()-derived path, not external input
	if err != nil {
		t.Fatalf("open built binary: %v", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatalf("hash built binary: %v", err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TestBinaryEqualityAcrossIndependentBuilds is the real, rigorous
// "binary equality" proof: cmd/veriqo-verify (chosen for a fast build:
// no heavy transitive package tree) compiled twice, each build using
// its own throwaway GOCACHE so the two compiler invocations are
// genuinely independent, must produce byte-identical output.
func TestBinaryEqualityAcrossIndependentBuilds(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "go.mod")); err != nil {
		t.Fatalf("expected %s to be the repo root (go.mod not found): %v", repoRoot, err)
	}

	const pkg = "veriqo/cmd/veriqo-verify"
	outA := filepath.Join(t.TempDir(), "veriqo-verify-a")
	outB := filepath.Join(t.TempDir(), "veriqo-verify-b")

	hashA := buildOnce(t, repoRoot, pkg, outA)
	hashB := buildOnce(t, repoRoot, pkg, outB)

	if hashA == "" || hashB == "" {
		t.Fatal("expected non-empty hashes from both independent builds")
	}
	if hashA != hashB {
		t.Fatalf("expected two independent builds of %s (separate GOCACHE each) to be byte-identical, got %s vs %s -- "+
			"this repository's build is NOT currently reproducible, which is a real regression from the claim this test exists to enforce",
			pkg, hashA, hashB)
	}
}
