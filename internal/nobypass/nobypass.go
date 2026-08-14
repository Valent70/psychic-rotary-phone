// Package nobypass makes audit item "P0-A — Unified Evidence"'s
// negative acceptance criterion machine-checked rather than
// convention-based:
//
//	ALL production evidence -> ONE canonical evidence contract
//	-> ONE arbitration path -> ONE provenance graph
//	Tidak boleh ada bypass.
//
// A prior round gave pkg/evidence/api.Facade real Truth Arbitration
// capability (ObserveRaw/ArbitrateClaim/RawObservations/
// VerifyRawTruthLedger/RankHypotheses) and rerouted the three
// production callers the audit named (pkg/engine/adapters.go,
// pkg/kernel/intentgraph, veriqo/core/intelligence) onto it. That
// closes today's known bypasses, but a convention ("please go through
// the facade") is not the audit's own bar -- nothing stopped a FUTURE
// caller from constructing its own contradiction.ArbitrationEngine
// directly and quietly reopening exactly the fragmentation the audit
// named. Check makes that structurally impossible to do silently: it
// scans the real source tree for contradiction.NewArbitrationEngine(
// call sites and fails if any exist outside the two audited,
// deliberately-exempt locations (see AllowedFiles).
package nobypass

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// callMarker is the exact construction call this check forbids outside
// AllowedFiles.
const callMarker = "contradiction.NewArbitrationEngine("

// AllowedFiles are the only source files permitted to contain
// callMarker, both audited and justified individually:
//
//   - pkg/evidence/api/api.go is the facade itself -- the ONE
//     canonical path every other caller must reach the engine through.
//   - pkg/engine/replay.go's ReplayContradiction is an IVF replay
//     function: it deliberately constructs a brand-new, isolated engine
//     with ZERO shared state, precisely so an independent verifier's
//     recomputation cannot be influenced by (or influence) the live
//     production engine reached through the facade. Routing IVF replay
//     through the facade would defeat replay's entire purpose, the same
//     reasoning cmd/veriqo-cold-replay's fresh execution.NewEngine(nil)
//     already relies on for the whole-DAG case (see its own doc
//     comment).
//   - internal/nobypass/nobypass.go is THIS file: callMarker below is
//     the literal string this checker searches for, which necessarily
//     contains the text it is looking for without that being a real
//     construction call. hasRealCall's own line-by-line scan cannot
//     distinguish "this line calls the constructor" from "this line is
//     a string literal naming the constructor" without a real Go
//     parser, so the exemption is listed explicitly here instead.
var AllowedFiles = []string{
	filepath.FromSlash("pkg/evidence/api/api.go"),
	filepath.FromSlash("pkg/engine/replay.go"),
	filepath.FromSlash("internal/nobypass/nobypass.go"),
}

// skipDirs are never descended into: VCS metadata and generated
// evidence, mirroring internal/sourcehash's own exclusions for the
// same reasons.
var skipDirs = []string{".git", "evidence"}

// Report is Check's result.
type Report struct {
	ScannedFiles int      `json:"scanned_files"`
	Violations   []string `json:"violations"` // repo-relative paths containing an unauthorized construction
}

// Check walks every .go file under repoRoot (excluding _test.go files,
// vendor-style skipDirs, and AllowedFiles) for callMarker.
// TestArbitrationEngineIsOnlyConstructedThroughTheFacade converts a
// non-empty Report.Violations into a build-breaking failure.
func Check(repoRoot string) (Report, error) {
	rep := Report{}
	allowed := map[string]bool{}
	for _, f := range AllowedFiles {
		allowed[f] = true
	}

	err := filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.IsDir() {
			for _, skip := range skipDirs {
				if info.Name() == skip {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(path) // #nosec G304 G122 -- path comes from this function's own filepath.Walk over repoRoot (a local source checkout this same readiness run already trusts enough to compile and test); worst case of a mid-walk symlink swap is a missed/misattributed scan result, never a security-relevant read
		if err != nil {
			return err
		}
		rep.ScannedFiles++
		if hasRealCall(string(raw)) && !allowed[rel] {
			rep.Violations = append(rep.Violations, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return Report{}, fmt.Errorf("nobypass: walk %s: %w", repoRoot, err)
	}
	sort.Strings(rep.Violations)
	return rep, nil
}

// hasRealCall reports whether raw contains callMarker outside a //
// line comment -- same narrow-but-sufficient technique as
// internal/telemetrycoverage.hasRealCall.
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
