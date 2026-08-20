// Package telemetrycoverage closes a false-green gap named by audit
// V7.12.1.6 item #5 ("Unified Observability adalah FALSE-GREEN RISK"):
// cmd/veriqo-readiness's "observability" gate ran only
// `go test ./pkg/platform/telemetry/` -- proof the telemetry package's
// OWN unit tests pass, not proof any production domain engine actually
// calls it. engine_registry.json already, correctly, reports
// telemetry_support=false for every engine; the readiness gate could
// still claim StatusVerified with a description that read "the
// telemetry schema covers every VERIQO domain and metric", which is
// exactly the contradiction the audit flagged: two artifacts produced
// by the same tool, in the same run, disagreeing about the same fact.
//
// Measure answers the real question directly: which production domain
// packages actually call telemetry.StartSpan? Today the honest answer
// is zero (a real, non-fabricated finding this package makes visible
// rather than hiding behind a package-scoped test), and
// cmd/veriqo-readiness now reports that as its own mandatory gate
// instead of folding it into "observability"'s already-passing one.
package telemetrycoverage

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DomainPackageRoots lists the source directories this check treats as
// "production domain engines" -- where real instrumentation would
// matter for an operator debugging a live decision, as opposed to
// pkg/platform/telemetry and pkg/platform/observability themselves
// (the instrumentation libraries, not their consumers).
var DomainPackageRoots = []string{
	"pkg/moat", "pkg/kernel", "pkg/engine", "pkg/consensus",
	"pkg/execution", "pkg/governance", "pkg/authz", "pkg/identity",
	"pkg/transport", "pkg/storage", "veriqo/core",
}

// callMarker is the exact call site Measure looks for. It intentionally
// matches only the telemetry package's real exported entry point, not
// pkg/platform/observability's separate MOATMetrics (a different,
// already-integrated instrumentation surface the audit's own findings
// distinguish from "unified observability").
const callMarker = "telemetry.StartSpan("

// Report is Measure's honest result: which domain packages (by
// directory, not file) contain at least one real, non-test,
// non-commented call to telemetry.StartSpan, out of how many were
// scanned.
type Report struct {
	TotalPackages        int      `json:"total_domain_packages"`
	InstrumentedPackages []string `json:"instrumented_packages"`
	Coverage             float64  `json:"coverage"` // len(InstrumentedPackages) / TotalPackages, 0 when TotalPackages == 0
}

// Measure scans every roots directory under repoRoot for Go packages
// (directories containing at least one non-test .go file) and reports
// which of them contain a real call to telemetry.StartSpan.
func Measure(repoRoot string, roots []string) (Report, error) {
	packages := map[string]bool{}     // dir -> has at least one non-test .go file
	instrumented := map[string]bool{} // dir -> has a real call site
	for _, root := range roots {
		full := filepath.Join(repoRoot, root)
		err := filepath.Walk(full, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			dir := filepath.Dir(path)
			packages[dir] = true
			raw, err := os.ReadFile(path) // #nosec G304 G122 -- path comes from this function's own filepath.Walk over repoRoot (a local source checkout this same readiness run already trusts enough to compile and test), not external/adversarial input; a symlink swapped mid-walk could at worst make this specific coverage measurement miss or misattribute one file, never a security-relevant read
			if err != nil {
				return err
			}
			if hasRealCall(string(raw)) {
				instrumented[dir] = true
			}
			return nil
		})
		if err != nil {
			return Report{}, fmt.Errorf("telemetrycoverage: walk %s: %w", root, err)
		}
	}

	out := make([]string, 0, len(instrumented))
	for dir := range instrumented {
		out = append(out, dir)
	}
	sort.Strings(out)

	rep := Report{TotalPackages: len(packages), InstrumentedPackages: out}
	if rep.TotalPackages > 0 {
		rep.Coverage = float64(len(out)) / float64(rep.TotalPackages)
	}
	return rep, nil
}

// hasRealCall reports whether raw (one Go source file's content)
// contains callMarker outside of a // line comment. It does not need
// to understand Go syntax fully -- only to avoid the one false
// positive that would matter here (a call mentioned in a comment,
// mirroring internal/determinism's same narrow-but-sufficient
// technique for the same class of check).
func hasRealCall(raw string) bool {
	for _, line := range strings.Split(raw, "\n") {
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = line[:idx]
		}
		if strings.Contains(line, callMarker) {
			return true
		}
	}
	return false
}
