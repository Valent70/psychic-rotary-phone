// Package entrypoints closes PHASE C (P0-3, "Canonical Execution
// Path") of the pre-insurance closure program: proving there is no
// back door to a governed decision.
//
// What already existed, and is deliberately NOT rebuilt here:
//
//   - internal/nobypass proves no production file constructs a second
//     evidence authority (contradiction.ArbitrationEngine,
//     fusion.Engine, canonical.Pipeline) outside an audited list. That
//     is the EVIDENCE half of the same idea.
//   - veriqo/gateway/rest.TestRegistryNeverProducesGovernedDecision-
//     Artifacts scans exactly two files (generated_routes.go and
//     registry.go) for governed-decision identity fields. That is a
//     real test, and a narrow one: it says nothing about the other
//     several hundred files in the tree, and nothing at all about
//     CLI, worker, scheduler, batch or admin entrypoints.
//
// This package is the whole-tree version of the second bullet, plus
// the entrypoint matrix the program asks for. It answers two distinct
// questions with one scan:
//
//  1. Does any production file construct a SECOND execution engine
//     (execution.NewEngine) outside the audited list? A second engine
//     is a second governed-decision path by definition.
//  2. Does any production file outside the canonical path MINT a
//     governed-decision artifact — an ExecutionRootHash, a DecisionID,
//     a VerificationCertificateID or a certificate hash — rather than
//     reading one that pkg/execution/pkg/lifecycle produced?
//
// The distinction in (2) matters and is why the marker set is
// deliberately narrow. Reading `res.Correlation.DecisionID` off a
// result and putting it in an HTTP response is exactly what an
// entrypoint SHOULD do — that is the canonical path working. Computing
// a decision identifier is not. The markers below are therefore
// constructor- and assignment-shaped, not mention-shaped.
//
// Anti-false-green discipline: the allowlist is per-marker and every
// entry is individually justified in a comment, in the same style
// internal/nobypass established. An entry added without a reason is a
// visible diff, not an invisible weakening.
package entrypoints

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// skipDirs mirrors internal/nobypass's exclusions for the same reasons.
var skipDirs = []string{".git", "evidence"}

// marker is one greppable construction this package guards.
type marker struct {
	name         string
	literal      string
	allowedFiles []string
	// why documents, in the report itself, what a violation of this
	// marker actually means — so a failing gate explains itself
	// without a reader having to find this file.
	why string
}

var guarded = []marker{
	{
		name:    "execution.Engine",
		literal: "execution.NewEngine(",
		why: "a second execution engine is a second governed-decision path by construction; " +
			"every governed decision must flow Entrypoint -> Lifecycle -> Execution Engine -> Policy -> " +
			"Evidence -> Decision -> Verification through the ONE engine pkg/lifecycle owns",
		// - pkg/lifecycle/lifecycle.go is the ONE production
		//   construction: NewOrchestrator builds the engine every real
		//   governed decision runs through, sharing the SAME
		//   canonical.Pipeline pointer (see its own doc comment on why a
		//   second, independently-stateful pipeline would diverge).
		// - cmd/veriqo-cold-replay/main.go deliberately constructs a
		//   FRESH engine with zero shared state, precisely so an
		//   independent verifier's recomputation cannot be influenced by
		//   the live production engine — the identical reasoning
		//   internal/nobypass already documents for pkg/engine/replay.go's
		//   isolated ReplayContradiction/ReplayFusion engines. Routing
		//   cold replay through the production engine would defeat
		//   replay's entire purpose.
		// - internal/entrypoints/entrypoints.go is THIS file: the
		//   literal below is the string this scanner searches for, which
		//   necessarily contains the text it looks for. Same
		//   string-literal self-reference internal/nobypass and
		//   pkg/governance/entityconsistency both had to list explicitly.
		allowedFiles: []string{
			filepath.FromSlash("pkg/lifecycle/lifecycle.go"),
			filepath.FromSlash("cmd/veriqo-cold-replay/main.go"),
			filepath.FromSlash("internal/entrypoints/entrypoints.go"),
		},
	},
	{
		name:    "execution root hash minting",
		literal: "ExecutionRootHash:",
		why: "only the execution DAG may compute an ExecutionRootHash; anything else assigning one is " +
			"asserting a governed execution happened without one having happened",
		// HONEST LIMITATION, stated rather than hidden: this marker is
		// assignment-shaped, and a textual scanner cannot distinguish
		// `ExecutionRootHash: <computed here>` from `ExecutionRootHash:
		// <copied off a real result>`. Every allowlisted file below was
		// therefore read and individually classified; the marker's real
		// value is that a NEW file assigning this field trips the gate
		// and forces the same classification to happen in review.
		//
		// - pkg/execution/execution.go computes it (it IS the DAG).
		// - pkg/lifecycle/lifecycle.go copies the engine's own value into
		//   the LifecycleCertificate — reading, not minting.
		// - cmd/veriqo-cold-replay/main.go reports the replayed value in
		//   its verdict output.
		// - veriqo/gateway/rest/lifecycle_route.go declares the field on
		//   its response DTO and fills it from
		//   res.Certificate.ExecutionRootHash — the ONE governed
		//   entrypoint returning the value the canonical path produced,
		//   which is exactly what an entrypoint should do. Verified by
		//   reading both occurrences (a struct tag declaration and one
		//   assignment from a real result), not assumed.
		allowedFiles: []string{
			filepath.FromSlash("pkg/execution/execution.go"),
			filepath.FromSlash("pkg/lifecycle/lifecycle.go"),
			filepath.FromSlash("cmd/veriqo-cold-replay/main.go"),
			filepath.FromSlash("veriqo/gateway/rest/lifecycle_route.go"),
			filepath.FromSlash("internal/entrypoints/entrypoints.go"),
		},
	},
	{
		name:    "verification certificate minting",
		literal: "replay.VerificationCertificate{",
		why: "a verification certificate is the output of an independent replay; constructing one " +
			"directly is asserting a verification that never ran",
		// - pkg/replay/replay.go's Engine.Replay is the only thing that
		//   may produce one, and its own error paths return the zero
		//   value of this type.
		// - pkg/evidence/api/api.go returns the zero value on its error
		//   paths for the same reason (Go has no other way to return an
		//   empty value of a struct type).
		allowedFiles: []string{
			filepath.FromSlash("pkg/replay/replay.go"),
			filepath.FromSlash("pkg/evidence/api/api.go"),
			filepath.FromSlash("internal/entrypoints/entrypoints.go"),
		},
	},
}

// EntrypointKind is one class of way a request can enter this system.
// The list is the program's own enumeration, verbatim.
type EntrypointKind string

const (
	KindHTTP             EntrypointKind = "HTTP"
	KindCLI              EntrypointKind = "CLI"
	KindBatch            EntrypointKind = "BATCH"
	KindWorker           EntrypointKind = "WORKER"
	KindScheduler        EntrypointKind = "SCHEDULER"
	KindAdminAPI         EntrypointKind = "ADMIN_API"
	KindReplay           EntrypointKind = "REPLAY"
	KindCompatibilityAPI EntrypointKind = "COMPATIBILITY_API"
	KindInternalJob      EntrypointKind = "INTERNAL_JOB"
)

// Entrypoint is one real, named way into this system and what it is
// permitted to produce. Every row is a claim about a specific file
// that Audit checks against the actual source, so a row cannot drift
// from reality without the gate failing.
type Entrypoint struct {
	Kind EntrypointKind `json:"kind"`
	Name string         `json:"name"`
	File string         `json:"file"`
	// GovernedDecisions states whether this entrypoint is permitted to
	// produce a governed decision at all.
	GovernedDecisions bool `json:"governed_decisions"`
	// CanonicalPath is the exact chain this entrypoint takes to reach
	// one, or the reason it never reaches one.
	CanonicalPath string `json:"canonical_path"`
	// Reaches names the function a governed-decision entrypoint must
	// actually call. Audit greps File for it; a row claiming a
	// canonical path it does not take is a violation, not a comment.
	Reaches string `json:"reaches,omitempty"`
}

// matrix is the audited entrypoint inventory. Adding an entrypoint to
// this repository without adding a row here does NOT silently pass:
// the engine/artifact markers above still apply to the new file, so a
// new back door trips the scan even before anyone updates this table.
var matrix = []Entrypoint{
	{
		Kind: KindHTTP, Name: "POST /lifecycle/run_unified",
		File:              filepath.FromSlash("veriqo/gateway/rest/lifecycle_route.go"),
		GovernedDecisions: true,
		CanonicalPath:     "HTTP -> Lifecycle.RunUnified -> execution.Engine -> Policy -> Evidence -> Decision -> Verification",
		Reaches:           "RunUnified(",
	},
	{
		Kind: KindCompatibilityAPI, Name: "veriqo/registry engine routes",
		File:              filepath.FromSlash("veriqo/gateway/rest/generated_routes.go"),
		GovernedDecisions: false,
		CanonicalPath: "compatibility/control surface only -- fine-grained component operations; " +
			"enforced by TestRegistryNeverProducesGovernedDecisionArtifacts and by this package's " +
			"artifact markers",
	},
	{
		Kind: KindCLI, Name: "veriqo/cli generated commands",
		File:              filepath.FromSlash("veriqo/cli/generated_commands.go"),
		GovernedDecisions: false,
		CanonicalPath:     "compatibility/control surface only -- same registry engines as the HTTP compatibility routes",
	},
	{
		Kind: KindReplay, Name: "cmd/veriqo-cold-replay",
		File:              filepath.FromSlash("cmd/veriqo-cold-replay/main.go"),
		GovernedDecisions: false,
		CanonicalPath: "independent verification only: rebuilds a COMMITTED trace with a deliberately " +
			"isolated fresh engine and compares. It reproduces a decision, it never originates one",
	},
	{
		Kind: KindInternalJob, Name: "cmd/veriqo-readiness",
		File:              filepath.FromSlash("cmd/veriqo-readiness/main.go"),
		GovernedDecisions: false,
		CanonicalPath:     "release gating only: runs gates and writes a manifest; produces no case decision",
	},
	{
		Kind: KindBatch, Name: "cmd/veriqo-qualification",
		File:              filepath.FromSlash("cmd/veriqo-qualification/main.go"),
		GovernedDecisions: false,
		CanonicalPath:     "blocker fixture qualification only; produces no case decision",
	},
	{
		Kind: KindWorker, Name: "cmd/veriqo-node",
		File:              filepath.FromSlash("cmd/veriqo-node/main.go"),
		GovernedDecisions: false,
		CanonicalPath:     "consensus/transport node: replicates state, does not originate governed decisions",
	},
	{
		Kind: KindWorker, Name: "cmd/veriqo-scale-node",
		File:              filepath.FromSlash("cmd/veriqo-scale-node/main.go"),
		GovernedDecisions: false,
		CanonicalPath:     "scale-harness worker: counts records, produces no decision",
	},
	{
		Kind: KindScheduler, Name: "cmd/veriqo-soak-run",
		File:              filepath.FromSlash("cmd/veriqo-soak-run/main.go"),
		GovernedDecisions: false,
		CanonicalPath:     "soak harness driver: exercises the system on a schedule, originates no governed decision",
	},
	{
		Kind: KindAdminAPI, Name: "cmd/veriqo-gateway",
		File:              filepath.FromSlash("cmd/veriqo-gateway/main.go"),
		GovernedDecisions: false,
		CanonicalPath:     "serves the REST surface above; the governed path is the lifecycle route's, not its own",
	},
}

// Matrix returns the audited entrypoint inventory.
func Matrix() []Entrypoint { return append([]Entrypoint(nil), matrix...) }

// Violation is one concrete finding, with enough context to act on.
type Violation struct {
	File   string `json:"file"`
	Marker string `json:"marker"`
	Reason string `json:"reason"`
}

// Report is Audit's machine-readable result. It is what the
// canonical_execution_entrypoint_coverage gate attaches as evidence.
type Report struct {
	ScannedFiles int `json:"scanned_files"`
	// ParallelGovernedExecutionPaths is the number the program's own
	// acceptance criterion names: it must be 0.
	ParallelGovernedExecutionPaths int          `json:"parallel_governed_execution_paths"`
	Violations                     []Violation  `json:"violations,omitempty"`
	Entrypoints                    []Entrypoint `json:"entrypoints"`
	// GovernedEntrypoints counts rows permitted to produce a governed
	// decision. More than a handful here is itself worth noticing.
	GovernedEntrypoints int `json:"governed_entrypoints"`
	// MatrixErrors records rows whose claim does not survive contact
	// with the source: a missing file, or a governed-decision row that
	// does not actually call what it says it calls.
	MatrixErrors []string `json:"matrix_errors,omitempty"`
}

// Audit walks the real source tree and checks both halves: no
// unauthorized construction of a governed-decision path, and no matrix
// row that claims something the source does not support.
func Audit(repoRoot string) (Report, error) {
	rep := Report{Entrypoints: Matrix()}
	allowed := make([]map[string]bool, len(guarded))
	for i, m := range guarded {
		set := map[string]bool{}
		for _, f := range m.allowedFiles {
			set[f] = true
		}
		allowed[i] = set
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
		raw, err := os.ReadFile(path) // #nosec G304 G122 -- path comes from this function's own filepath.Walk over repoRoot, a local source checkout this same run already compiles and tests
		if err != nil {
			return err
		}
		rep.ScannedFiles++
		content := string(raw)
		for i, m := range guarded {
			if hasRealCall(content, m.literal) && !allowed[i][rel] {
				rep.Violations = append(rep.Violations, Violation{
					File: filepath.ToSlash(rel), Marker: m.name, Reason: m.why,
				})
			}
		}
		return nil
	})
	if err != nil {
		return Report{}, fmt.Errorf("entrypoints: walk %s: %w", repoRoot, err)
	}
	sort.Slice(rep.Violations, func(i, j int) bool {
		if rep.Violations[i].File != rep.Violations[j].File {
			return rep.Violations[i].File < rep.Violations[j].File
		}
		return rep.Violations[i].Marker < rep.Violations[j].Marker
	})
	rep.ParallelGovernedExecutionPaths = len(rep.Violations)

	for _, e := range rep.Entrypoints {
		if e.GovernedDecisions {
			rep.GovernedEntrypoints++
		}
		full := filepath.Join(repoRoot, e.File)
		raw, err := os.ReadFile(full) // #nosec G304 -- e.File comes from this file's own hardcoded matrix, not external input
		if err != nil {
			rep.MatrixErrors = append(rep.MatrixErrors,
				fmt.Sprintf("%s (%s): declared file %s cannot be read: %v", e.Name, e.Kind, e.File, err))
			continue
		}
		if e.GovernedDecisions {
			if e.Reaches == "" {
				rep.MatrixErrors = append(rep.MatrixErrors,
					fmt.Sprintf("%s (%s): claims to produce governed decisions but names no canonical call to reach them", e.Name, e.Kind))
				continue
			}
			if !hasRealCall(string(raw), e.Reaches) {
				rep.MatrixErrors = append(rep.MatrixErrors,
					fmt.Sprintf("%s (%s): claims the canonical path %q but %s does not call %s",
						e.Name, e.Kind, e.CanonicalPath, e.File, e.Reaches))
			}
		}
	}
	sort.Strings(rep.MatrixErrors)
	return rep, nil
}

// Clean reports whether the audit found nothing at all: zero parallel
// governed execution paths and zero matrix rows contradicted by the
// source. Deliberately a single boolean with no threshold — the
// program's acceptance criterion is "= 0", not "few".
func (r Report) Clean() bool {
	return r.ParallelGovernedExecutionPaths == 0 && len(r.MatrixErrors) == 0
}

// hasRealCall reports whether raw contains literal outside a // line
// comment — the same narrow-but-sufficient technique
// internal/nobypass and internal/telemetrycoverage already use.
func hasRealCall(raw, literal string) bool {
	for _, line := range strings.Split(raw, "\n") {
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = line[:idx]
		}
		if strings.Contains(line, literal) {
			return true
		}
	}
	return false
}
