package environment

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// repoRoot finds the real repository root by walking up from this
// test's working directory until go.mod is found -- mirroring
// internal/nobypass's own repoRoot test helper, needed because
// `go test` sets the working directory to this package's own
// directory, not the repo root configurationHash(".") assumes in
// production.
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

func TestCurrentReportsRealRuntimeFacts(t *testing.T) {
	id := Current()
	if id.OS != runtime.GOOS {
		t.Fatalf("OS = %q, want %q", id.OS, runtime.GOOS)
	}
	if id.Arch != runtime.GOARCH {
		t.Fatalf("Arch = %q, want %q", id.Arch, runtime.GOARCH)
	}
	if id.Compiler != runtime.Compiler {
		t.Fatalf("Compiler = %q, want %q", id.Compiler, runtime.Compiler)
	}
	if id.Toolchain != runtime.Version() {
		t.Fatalf("Toolchain = %q, want %q", id.Toolchain, runtime.Version())
	}
	if id.Hostname == "" {
		t.Fatal("expected a non-empty Hostname (even the error-fallback path produces a non-empty string)")
	}
}

// TestCurrentDoesNotFabricateInfrastructureFields is the direct proof
// of this package's own "no fake closure" discipline: fields this
// sandbox genuinely cannot know must stay honestly empty/zero, never a
// plausible-looking placeholder.
func TestCurrentDoesNotFabricateInfrastructureFields(t *testing.T) {
	id := Current()
	if id.CloudProvider != "" {
		t.Fatalf("expected CloudProvider to be honestly empty in this sandbox, got %q", id.CloudProvider)
	}
	if id.CloudRegion != "" {
		t.Fatalf("expected CloudRegion to be honestly empty, got %q", id.CloudRegion)
	}
	if id.NodeCount != 0 {
		t.Fatalf("expected NodeCount to be honestly zero, got %d", id.NodeCount)
	}
	if id.OrchestratorVersion != "" {
		t.Fatalf("expected OrchestratorVersion to be honestly empty, got %q", id.OrchestratorVersion)
	}
	if len(id.ImageDigests) != 0 {
		t.Fatalf("expected ImageDigests to be honestly empty, got %v", id.ImageDigests)
	}
	if id.TimeReferenceUnix != 0 {
		t.Fatal("Current() itself must never set TimeReferenceUnix -- it stays a pure function of the environment; callers attach a wall-clock value explicitly")
	}
}

func TestCurrentIsDeterministicAcrossCalls(t *testing.T) {
	a, b := Current(), Current()
	// Compare everything except nothing -- Current() has no hidden
	// mutable/random state, so two calls in the same process must
	// report byte-identical facts.
	ha, err := a.Hash()
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	hb, err := b.Hash()
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if ha != hb {
		t.Fatalf("expected two Current() calls in the same process to hash identically, got %q vs %q", ha, hb)
	}
}

func TestHashChangesWhenIdentityChanges(t *testing.T) {
	a := Current()
	b := a
	b.Hostname = a.Hostname + "-different"
	ha, err := a.Hash()
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	hb, err := b.Hash()
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if ha == hb {
		t.Fatal("expected Hash to change when Identity's content changes")
	}
}

// TestReadKernelVersionOnLinuxIsNonEmpty is a real, environment-
// specific assertion (this CI/sandbox runs Linux): confirms the
// /proc/sys/kernel/osrelease read actually returns real content, not
// silently falling through to the not-found branch on a platform where
// it should succeed.
func TestReadKernelVersionOnLinuxIsNonEmpty(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("kernel version is only read on Linux")
	}
	v := readKernelVersion()
	if v == "" {
		t.Fatal("expected a non-empty kernel version on Linux")
	}
	if strings.ContainsAny(v, "\n\r") {
		t.Fatalf("expected a trimmed single-line kernel version, got %q", v)
	}
}

func TestConfigurationHashIsStableAndNonEmpty(t *testing.T) {
	root := repoRoot(t)
	h1 := configurationHash(root)
	h2 := configurationHash(root)
	if h1 == "" {
		t.Fatal("expected a non-empty configuration hash (go.mod exists in this repo)")
	}
	if h1 != h2 {
		t.Fatalf("expected configurationHash to be stable across calls with unchanged files, got %q vs %q", h1, h2)
	}
}

// TestConfigurationHashChangesWithDifferentInput is the adversarial
// proof this is a real content hash, not a constant: hashing a
// directory with no go.mod/go.sum at all must differ from hashing the
// real repo root.
func TestConfigurationHashChangesWithDifferentInput(t *testing.T) {
	real := configurationHash(repoRoot(t))
	empty := configurationHash(t.TempDir())
	if real == empty {
		t.Fatal("expected the real repo's configuration hash to differ from an empty directory's")
	}
}
