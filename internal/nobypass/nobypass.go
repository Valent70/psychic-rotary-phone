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
// pkg/kernel/intentgraph, veriqo/core/intelligence) onto it. A later
// round gave the Facade the equivalent real Fusion capability
// (FusionRegisterSource/FusionSubmit/FusionArbitrate/FusionEvidenceFor/
// FusionVerifyChain) and rerouted pkg/engine/adapters.go's
// FusionEngineAdapter the same way. That closes today's known
// bypasses, but a convention ("please go through the facade") is not
// the audit's own bar -- nothing stopped a FUTURE caller from
// constructing its own contradiction.ArbitrationEngine or fusion.Engine
// directly and quietly reopening exactly the fragmentation the audit
// named. Check makes that structurally impossible to do silently: it
// scans the real source tree for either constructor's call sites and
// fails if any exist outside their own audited, deliberately-exempt
// locations.
//
// A later round's own re-read of the master implementation directive
// found a THIRD, real construction site this package had not yet
// covered: pkg/canonical.NewPipeline itself. Five production files
// each independently construct a canonical.Pipeline (veriqo/kernel's
// composition root, pkg/replay's isolated re-execution, pkg/evidence/
// api's own facade default, pkg/lifecycle's nil-safe default, pkg/
// execution's nil-safe default) -- every one of them individually
// justified (see checkedConstructors below), but, exactly like the
// other two constructors before this round, nothing PROVED that set
// was exhaustive or would stay that way. Check now scans for this
// constructor too, closing what the master directive names
// "unified_evidence_production_coverage": a real, whole-tree scan
// proving every production evidence ingress path -- not just the
// three Facade-reachable call sites the P0-A doc comment above
// enumerates -- terminates at an audited, exhaustively-listed
// construction site, not a silently-reopened parallel authority.
package nobypass

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// constructor is one bypass-checkable direct-construction call: Marker
// is the literal source text Check searches for, AllowedFiles are the
// only repo-relative paths permitted to contain it.
type constructor struct {
	name         string
	marker       string
	allowedFiles []string
}

// checkedConstructors are every direct-engine-construction call this
// package currently guards. Each entry's AllowedFiles list is audited
// and justified individually in its own comment.
var checkedConstructors = []constructor{
	{
		name:   "contradiction.ArbitrationEngine",
		marker: "contradiction.NewArbitrationEngine(",
		// - pkg/evidence/api/api.go is the facade itself -- the ONE
		//   canonical path every other caller must reach the engine
		//   through.
		// - pkg/engine/replay.go's ReplayContradiction is an IVF replay
		//   function: it deliberately constructs a brand-new, isolated
		//   engine with ZERO shared state, precisely so an independent
		//   verifier's recomputation cannot be influenced by (or
		//   influence) the live production engine reached through the
		//   facade. Routing IVF replay through the facade would defeat
		//   replay's entire purpose, the same reasoning
		//   cmd/veriqo-cold-replay's fresh execution.NewEngine(nil)
		//   already relies on for the whole-DAG case (see its own doc
		//   comment).
		// - internal/nobypass/nobypass.go is THIS file: marker below is
		//   the literal string this checker searches for, which
		//   necessarily contains the text it is looking for without
		//   that being a real construction call. hasRealCall's own
		//   line-by-line scan cannot distinguish "this line calls the
		//   constructor" from "this line is a string literal naming the
		//   constructor" without a real Go parser, so the exemption is
		//   listed explicitly here instead.
		allowedFiles: []string{
			filepath.FromSlash("pkg/evidence/api/api.go"),
			filepath.FromSlash("pkg/engine/replay.go"),
			filepath.FromSlash("internal/nobypass/nobypass.go"),
		},
	},
	{
		name:   "fusion.Engine",
		marker: "fusion.NewEngine(",
		// - pkg/canonical/canonical.go is the ONE production
		//   constructor: canonical.Pipeline's own Fusion field, the
		//   exact engine instance every Facade method (Submit/Arbitrate
		//   above, FusionRegisterSource/FusionSubmit/FusionArbitrate/
		//   FusionEvidenceFor/FusionVerifyChain) already reaches through
		//   f.pipeline.Fusion.
		// - pkg/engine/replay.go's ReplayFusion is the fusion half of
		//   the same deliberately-isolated IVF replay reasoning as
		//   ReplayContradiction above.
		// - cmd/veriqo-demo/main.go is a demo binary, not a production
		//   entrypoint -- confirmed by grep: nothing in cmd/veriqo-node,
		//   veriqo/kernel, or any other production composition root
		//   imports cmd/veriqo-demo.
		// - internal/nobypass/nobypass.go: same string-literal reason as
		//   above.
		allowedFiles: []string{
			filepath.FromSlash("pkg/canonical/canonical.go"),
			filepath.FromSlash("pkg/engine/replay.go"),
			filepath.FromSlash("cmd/veriqo-demo/main.go"),
			filepath.FromSlash("internal/nobypass/nobypass.go"),
		},
	},
	{
		name:   "canonical.Pipeline",
		marker: "canonical.NewPipeline(",
		// Five audited production sites, each independently justified
		// (not a blanket exemption):
		// - veriqo/kernel/kernel.go: the ONE true composition root.
		//   Constructs exactly one Pipeline and shares that SAME pointer
		//   with everything downstream (lifecycle.NewOrchestrator, api.
		//   New) by injection -- see kernel.go's own doc comment on why
		//   a second, independently-stateful Pipeline anywhere else
		//   would silently double-run arbitration and diverge from this
		//   one.
		// - pkg/replay/replay.go: "Fresh engines. No access to the
		//   originals." -- the same deliberately-isolated independent-
		//   verification reasoning as ReplayContradiction/ReplayFusion
		//   above; an independent verifier's recomputation must not
		//   share state with the live production pipeline it is
		//   checking.
		// - pkg/evidence/api/api.go: the facade's own nil-safe default,
		//   used ONLY when no pipeline is injected -- the facade IS the
		//   canonical path, so its own fallback construction is not a
		//   bypass of itself.
		// - pkg/lifecycle/lifecycle.go (NewOrchestrator) and pkg/
		//   execution/execution.go (NewEngine): both nil-safe defaults
		//   for standalone/test construction, mirroring the exact same
		//   pattern; every real production caller (veriqo/kernel.New)
		//   always passes the shared pipeline explicitly, so this
		//   branch is never taken outside tests and standalone tooling.
		// - internal/nobypass/nobypass.go: same string-literal reason
		//   as the other two constructors' exemptions.
		allowedFiles: []string{
			filepath.FromSlash("veriqo/kernel/kernel.go"),
			filepath.FromSlash("pkg/replay/replay.go"),
			filepath.FromSlash("pkg/evidence/api/api.go"),
			filepath.FromSlash("pkg/lifecycle/lifecycle.go"),
			filepath.FromSlash("pkg/execution/execution.go"),
			filepath.FromSlash("internal/nobypass/nobypass.go"),
		},
	},
}

// skipDirs are never descended into: VCS metadata and generated
// evidence, mirroring internal/sourcehash's own exclusions for the
// same reasons.
var skipDirs = []string{".git", "evidence"}

// Report is Check's result.
type Report struct {
	ScannedFiles int      `json:"scanned_files"`
	Violations   []string `json:"violations"` // "path: ConstructorName" for each unauthorized construction found
}

// Check walks every .go file under repoRoot (excluding _test.go files,
// vendor-style skipDirs) for each checkedConstructors marker outside its
// own AllowedFiles.
// TestArbitrationEngineIsOnlyConstructedThroughTheFacade and
// TestFusionEngineIsOnlyConstructedThroughTheFacade convert a non-empty
// Report.Violations into a build-breaking failure.
func Check(repoRoot string) (Report, error) {
	rep := Report{}
	allowedByMarker := make([]map[string]bool, len(checkedConstructors))
	for i, c := range checkedConstructors {
		m := map[string]bool{}
		for _, f := range c.allowedFiles {
			m[f] = true
		}
		allowedByMarker[i] = m
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
		content := string(raw)
		for i, c := range checkedConstructors {
			if hasRealCall(content, c.marker) && !allowedByMarker[i][rel] {
				rep.Violations = append(rep.Violations, filepath.ToSlash(rel)+": "+c.name)
			}
		}
		return nil
	})
	if err != nil {
		return Report{}, fmt.Errorf("nobypass: walk %s: %w", repoRoot, err)
	}
	sort.Strings(rep.Violations)
	return rep, nil
}

// hasRealCall reports whether raw contains marker outside a // line
// comment -- same narrow-but-sufficient technique as
// internal/telemetrycoverage.hasRealCall.
func hasRealCall(raw, marker string) bool {
	for _, line := range strings.Split(raw, "\n") {
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = line[:idx]
		}
		if strings.Contains(line, marker) {
			return true
		}
	}
	return false
}
