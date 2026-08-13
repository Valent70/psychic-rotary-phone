package version

import (
	"os"
	"strings"
	"testing"
)

// TestEmbeddedVersionMatchesRepoRootVERSION is the CI-enforced drift
// check P0-12 required: go:embed cannot reach the repository-root
// VERSION file directly, so this package embeds a local copy
// (internal/version/VERSION). This test fails the build the moment
// that copy and the root file diverge, instead of the two silently
// drifting the way cmd/veriqo-readiness's flag default, pkg/execution's
// Engine.Version, and two shell scripts' fallbacks all did before
// P0-12.
func TestEmbeddedVersionMatchesRepoRootVERSION(t *testing.T) {
	rootVersion, err := os.ReadFile("../../VERSION")
	if err != nil {
		t.Fatalf("read repository-root VERSION: %v", err)
	}
	want := strings.TrimSpace(string(rootVersion))
	if Current != want {
		t.Fatalf("internal/version/VERSION (embedded, %q) does not match repository-root VERSION (%q) -- these must be kept identical", Current, want)
	}
}

// TestOperativeVersionSitesDeriveFromCurrent greps the specific files
// the audit named as having independently hardcoded "v7.12.0" and
// confirms none of them holds a version literal that disagrees with
// Current. This is narrowly scoped to the known operative sites (a
// flag default, a struct-literal field, two shell script fallbacks) --
// not a blanket ban on the string "v7.1x.x" appearing in historical
// doc comments elsewhere in the repository, which legitimately name
// which audit round closed a given gap.
func TestOperativeVersionSitesDeriveFromCurrent(t *testing.T) {
	cases := []struct {
		file    string
		snippet string // must appear verbatim if present at all
	}{
		{"../../cmd/veriqo-readiness/main.go", `flag.String("version", version.Current,`},
		{"../../pkg/execution/execution.go", `Version: "execution/" + version.Current`},
	}
	for _, c := range cases {
		data, err := os.ReadFile(c.file)
		if err != nil {
			t.Fatalf("read %s: %v", c.file, err)
		}
		if !strings.Contains(string(data), c.snippet) {
			t.Errorf("%s: expected to find %q -- an operative version site may have reintroduced a hardcoded literal", c.file, c.snippet)
		}
	}

	// Narrowly scoped to the operative VERIQO_VERSION fallback pattern --
	// not a blanket scan, since these scripts' own header comments
	// legitimately name which historical audit round ("PHASE 53/54 of
	// the v7.10.3 audit") wrote them.
	for _, script := range []string{"../../scripts/production-readiness.sh", "../../scripts/sbom.sh"} {
		data, err := os.ReadFile(script)
		if err != nil {
			t.Fatalf("read %s: %v", script, err)
		}
		text := string(data)
		if strings.Contains(text, `VERIQO_VERSION:-v7.`) {
			t.Errorf("%s: VERIQO_VERSION fallback is a hardcoded legacy version literal instead of deriving from VERSION", script)
		}
		if !strings.Contains(text, "VERIQO_VERSION:-$(cat VERSION)") {
			t.Errorf("%s: expected its VERIQO_VERSION fallback to read the VERSION file", script)
		}
	}
}
