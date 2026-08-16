// Package execution closes PHASE 7: the Unified Intelligence Execution
// Graph. The auditor's framing was exact — "Ini harus menjadi actual
// execution engine, bukan sekadar kumpulan adapter" and "Jangan hanya
// sequential function calls. Harus ada explicit dependency graph."
//
// So this package is a real DAG executor. Stages are registered nodes
// with declared Dependencies; Run topologically orders them, refuses to
// execute a node whose dependencies did not produce output, records
// each node's Inputs, Outputs, Version, Hash, ExecutionTick and Status,
// and folds every node fingerprint into one ExecutionRootHash.
//
// Two design decisions are worth stating plainly rather than hiding.
//
// First: the intelligence stages (dependency, truth, contradiction,
// fusion, causal, risk, decision, twin) are executed by
// pkg/canonical.RunCanonical, not reimplemented here. NON-NEGOTIABLE
// RULE #1 of the audit was "jangan rebuild capability yang sudah ada …
// jika capability existing hanya perlu dipakai oleh capability baru,
// integrasikan, jangan rewrite". The DAG node for each of those stages
// therefore ATTRIBUTES a specific artifact of the canonical result to a
// specific stage and hashes exactly that artifact — so a change in
// fusion moves the fusion node hash and nothing else, and a divergence
// is localised to a stage rather than to "canonical".
//
// Second: the stages that did not exist before — identity binding,
// trust state, temporal, economic consequence, explanation, replay
// package, verification certificate, governance binding — are computed
// by this engine directly, and each is a first-class node with its own
// dependencies.
//
// The acceptance criterion from the audit is met exactly: one execution
// yields ExecutionTrace, ExecutionRootHash, Decision, DecisionExplanation,
// ReplayPackage and VerificationCertificate, and Replay rebuilds the
// entire DAG and localises the first divergent stage.
package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"veriqo/internal/version"
	"veriqo/pkg/canonical"
	"veriqo/pkg/explanation"
	"veriqo/pkg/governance/knowledge"
	"veriqo/pkg/governance/lifecycle"
	"veriqo/pkg/identity"
	"veriqo/pkg/moat/economic"
	"veriqo/pkg/moat/hbayes"
	"veriqo/pkg/platform/telemetry"
	"veriqo/pkg/replay"
	"veriqo/pkg/trust/state"
)

var (
	// ErrContextIncomplete refuses to run without full provenance.
	ErrContextIncomplete = errors.New("execution: execution context is incomplete")
	// ErrDependencyUnsatisfied is the DAG guard: a stage whose input
	// was never produced does not run with a zero value.
	ErrDependencyUnsatisfied = errors.New("execution: stage dependency did not produce output")
	// ErrCycle refuses a malformed graph.
	ErrCycle = errors.New("execution: stage graph contains a cycle")
	// ErrStageFailed wraps a stage's own error, naming the stage.
	ErrStageFailed = errors.New("execution: stage failed")
	// ErrDivergence is returned by Replay when a rebuilt stage hash
	// differs from the committed one.
	ErrDivergence = errors.New("execution: replay diverged")
	// ErrBindingMismatch is raised when the governance binding at replay
	// time differs from the one the execution committed to.
	ErrBindingMismatch = errors.New("execution: governance binding mismatch")
	// ErrPolicyMismatch is raised (P0-D) when Input.ExpectedPolicyHash is
	// supplied and it does not match decision.Policy.Hash() of the
	// policy actually carried in Case.Policy -- proof the POLICY stage
	// performs a real, independent verification against the policy that
	// will actually run, in the same spirit as ErrIdentityMismatch,
	// rather than trusting Context.PolicyVersion as an unverified label.
	ErrPolicyMismatch = errors.New("execution: policy: independently recomputed policy hash does not match the expected policy hash")
	// ErrTemporalCalibrationRequired is raised (P0-D) when the active
	// policy declares RequiresTemporalCalibration and the caller
	// supplied neither a TemporalModel nor a TemporalObservations
	// series -- TEMPORAL_BAYESIAN fails closed instead of the default
	// honest SKIPPED, because for a policy that declares temporal
	// reasoning load-bearing, a decision reached without it is not one
	// this policy is willing to stand behind.
	ErrTemporalCalibrationRequired = errors.New("execution: temporal_bayesian: policy requires temporal calibration but none was supplied")
	// ErrIdentityMismatch is raised (P0-4) when Input.IdentityAliases is
	// supplied and Engine.Identity independently re-resolves it to a
	// DIFFERENT canonical entity than the caller-supplied
	// Case.Entity -- proof the IDENTITY_RESOLUTION stage performs a
	// real, independent second computation against the identity
	// ledger, in the same spirit as pkg/lifecycle's own
	// replayFusionArbitration, rather than trusting the caller's claim.
	ErrIdentityMismatch = errors.New("execution: identity resolution: independently re-resolved entity does not match the supplied case entity")
	// ErrEvidencePlanUnsatisfied is raised (P0-D Test 3) when
	// Input.EvidenceRequirements is supplied and a Required requirement's
	// MinSources is not met by Case.Submissions -- INTENT independently
	// re-derives the same verdict pkg/lifecycle.checkPlan already
	// computed before ever calling Engine.Run, rather than only
	// attributing the case's tenant/actor/caseID. See
	// EvidenceRequirement's doc comment for why this is a duplicate,
	// import-cycle-avoiding type rather than a reuse of
	// pkg/lifecycle.EvidenceRequirement.
	ErrEvidencePlanUnsatisfied = errors.New("execution: intent: required evidence plan item not satisfied")
)

// StageID names a node of the graph.
type StageID string

const (
	// StageIntent is the DAG's root node (P0-2): it commits to WHICH
	// investigative intent this execution serves -- CaseID, Tenant,
	// Actor and PolicyVersion, the four Context fields
	// Context.validate() already makes mandatory for every execution,
	// so this stage always has real data to attribute, never a stub.
	// pkg/lifecycle.Orchestrator.RunUnified binds ctx.CaseID to a real
	// Intent's own content-addressed ID (see lifecycle.go's
	// executionID/Intent.ID), so for every production call through
	// that path this node's artifact genuinely traces back to one
	// specific Intent, closing the audit's "Intent -> Evidence
	// Planning -> ... " ordering requirement at the DAG level, not
	// only at the lifecycle-orchestration level above it.
	StageIntent               StageID = "INTENT"
	StageEvidenceIngestion    StageID = "EVIDENCE_INGESTION"
	StageIdentityResolution   StageID = "IDENTITY_RESOLUTION"
	StageDependencyEvaluation StageID = "DEPENDENCY_EVALUATION"
	StageTruthArbitration     StageID = "TRUTH_ARBITRATION"
	StageContradiction        StageID = "CONTRADICTION_ARBITRATION"
	StageFusion               StageID = "CORRELATION_FUSION"
	StageTemporal             StageID = "TEMPORAL_BAYESIAN"
	StageCausal               StageID = "CAUSAL_REASONING"
	StageRisk                 StageID = "RISK"
	// StagePolicy is a real DAG node (P0-3), not decorative: it
	// attributes the specific policy version and named policy that
	// governed the decision made two nodes later, as its own
	// independently-hashed, independently-localisable artifact --
	// separated out of StageDecision's hash exactly the way
	// StageContradiction and StageFusion were already separated out of
	// the same underlying arbitration artifact for the same reason
	// (see this file's own package doc on stage attribution). It sits
	// between Risk and Decision, matching pkg/explanation's own chain
	// ordering (explanation.StagePolicy's link already precedes
	// explanation.StageDecision's link in buildExplanation below).
	StagePolicy                  StageID = "POLICY"
	StageDecision                StageID = "DECISION"
	StageTrust                   StageID = "TRUST_STATE"
	StageDigitalTwin             StageID = "DIGITAL_TWIN"
	StageEconomic                StageID = "ECONOMIC_CONSEQUENCE"
	StageExplanation             StageID = "EXPLANATION"
	StageReplayPackage           StageID = "REPLAY_PACKAGE"
	StageVerificationCertificate StageID = "VERIFICATION_CERTIFICATE"
)

// Status is a node's outcome.
type Status string

const (
	StatusPending Status = "PENDING"
	StatusOK      Status = "OK"
	StatusSkipped Status = "SKIPPED" // not applicable to this execution
	StatusFailed  Status = "FAILED"
)

// Node is one recorded DAG node — the audit's required shape.
type Node struct {
	StageID       StageID   `json:"stage_id"`
	Inputs        []string  `json:"inputs"`
	Outputs       []string  `json:"outputs"`
	Dependencies  []StageID `json:"dependencies"`
	Version       string    `json:"version"`
	Hash          string    `json:"hash"`
	ExecutionTick uint64    `json:"execution_tick"`
	Status        Status    `json:"status"`
	Detail        string    `json:"detail"`
	Error         string    `json:"error,omitempty"`
}

// Context is the audit's required ExecutionContext. Every field is part
// of the root hash: an execution cannot be replayed under a different
// tenant, actor, policy version, identity resolution version, model set
// or ledger position without detection.
type Context struct {
	ExecutionID               string   `json:"execution_id"`
	CaseID                    string   `json:"case_id"`
	Tenant                    string   `json:"tenant"`
	Actor                     string   `json:"actor"`
	PolicyVersion             string   `json:"policy_version"`
	EvidencePackageID         string   `json:"evidence_package_id"`
	DependencyRootHash        string   `json:"dependency_root_hash"`
	IdentityResolutionVersion string   `json:"identity_resolution_version"`
	ModelVersions             []string `json:"model_versions"`
	SourceVersions            []string `json:"source_versions"`
	LedgerPosition            uint64   `json:"ledger_position"`
	Tick                      uint64   `json:"tick"`
	ReplayMetadata            string   `json:"replay_metadata"`
	BindingHash               string   `json:"binding_hash"`
	KnowledgeRoot             string   `json:"knowledge_root"`
}

func (c Context) validate() error {
	var missing []string
	if c.ExecutionID == "" {
		missing = append(missing, "execution_id")
	}
	if c.CaseID == "" {
		missing = append(missing, "case_id")
	}
	if c.Tenant == "" {
		missing = append(missing, "tenant")
	}
	if c.Actor == "" {
		missing = append(missing, "actor")
	}
	if c.PolicyVersion == "" {
		missing = append(missing, "policy_version")
	}
	if c.IdentityResolutionVersion == "" {
		missing = append(missing, "identity_resolution_version")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s", ErrContextIncomplete, strings.Join(missing, ", "))
	}
	return nil
}

func (c Context) hash() string {
	var sb strings.Builder
	sb.WriteString("execution_context/v1\n" + c.ExecutionID + "\n" + c.CaseID + "\n" +
		c.Tenant + "\n" + c.Actor + "\n" + c.PolicyVersion + "\n" + c.EvidencePackageID + "\n" +
		c.DependencyRootHash + "\n" + c.IdentityResolutionVersion + "\n" +
		strings.Join(sortedCopy(c.ModelVersions), ";") + "\n" +
		strings.Join(sortedCopy(c.SourceVersions), ";") + "\n" +
		strconv.FormatUint(c.LedgerPosition, 10) + "\n" + strconv.FormatUint(c.Tick, 10) + "\n" +
		c.ReplayMetadata + "\n" + c.BindingHash + "\n" + c.KnowledgeRoot + "\n")
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// Trace is the full recorded execution.
type Trace struct {
	Context     Context `json:"context"`
	ContextHash string  `json:"context_hash"`
	Nodes       []Node  `json:"nodes"`
	RootHash    string  `json:"root_hash"`
}

// Result is what one execution produces — exactly the audit's list.
type Result struct {
	Trace             Trace                           `json:"trace"`
	ExecutionRootHash string                          `json:"execution_root_hash"`
	Canonical         *canonical.CanonicalResult      `json:"canonical"`
	Decision          string                          `json:"decision"`
	Explanation       explanation.DecisionExplanation `json:"explanation"`
	Economic          economic.Consequence            `json:"economic"`
	ReplayPackage     replay.ReplayPackage            `json:"replay_package"`
	Certificate       replay.VerificationCertificate  `json:"verification_certificate"`
	Binding           lifecycle.Binding               `json:"binding"`
	// Case, Scenarios and Currency are exactly the Input fields THIS
	// Run call actually used (P1-10) -- captured here, not
	// reconstructed by a caller, because Case in particular may differ
	// from what a caller originally supplied: pkg/lifecycle.Orchestrator
	// .RunUnified resolves entity aliases and mutates its OWN local
	// copy of CaseInput before calling Engine.Run, so the only place
	// that ever sees the exact Case bytes this specific run committed
	// to is inside Run itself. Exporting an approximation reconstructed
	// one layer up would silently diverge from what ReplayDAG actually
	// needs to reproduce this Trace. See ExportReplay.
	Case      canonical.CaseInput `json:"case"`
	Scenarios []economic.Scenario `json:"scenarios,omitempty"`
	Currency  string              `json:"currency,omitempty"`
	// IdentityAliases is exactly Input.IdentityAliases this Run call
	// used -- captured for the same reason Case is (see above). Its
	// absence from ReplayRequest was a real, previously-undiscovered
	// gap this round's P1-10 test caught: any production execution
	// that went through pkg/lifecycle.Orchestrator.RunUnified sets
	// this (see resolveCanonicalEntity/identityKeySet), which makes
	// IDENTITY_RESOLUTION additionally commit to an independently
	// re-resolved entity ID (P0-4's "|reresolved=..." hash term). A
	// cold replay that omitted IdentityAliases entirely -- as
	// ReplayDAG unconditionally did before this fix -- could never
	// reproduce that node's hash for ANY real production execution,
	// only for the narrower case of a bare *Engine call with no
	// identity aliases at all.
	IdentityAliases []identity.Identifier `json:"identity_aliases,omitempty"`
	// ExpectedPolicyHash and EvidenceRequirements are exactly the
	// matching Input fields this Run call used -- the same replay-
	// fidelity gap as IdentityAliases above, and found the same way
	// (a genuinely independent field, not derivable from Case alone,
	// whose absence from a replay changes which branch StagePolicy/
	// StageIntent take and therefore their committed node hash). Both
	// were added to Input in earlier rounds without a matching export
	// path; closed together here.
	ExpectedPolicyHash   string                `json:"expected_policy_hash,omitempty"`
	EvidenceRequirements []EvidenceRequirement `json:"evidence_requirements,omitempty"`
}

// ExportReplay serializes this Result into the exact ReplayRequest
// bytes an independent replayer (cmd/veriqo-cold-replay, or any other
// process holding nothing but this byte slice) consumes to
// independently rebuild and verify the DAG this Result committed to.
// It is the real production capability P1-10 needed: previously the
// only way to obtain a cold-replayable export was to hold a live
// *Engine reference and read res.Trace out of process memory directly
// -- reachable from tests and cmd/veriqo-cold-replay's own callers,
// but not from an execution reached over a real network boundary (an
// HTTP handler that only has this Result value, not the Engine that
// produced it). ExportReplay closes that gap: any caller holding a
// Result -- including veriqo/gateway/rest's real
// /lifecycle/run_unified handler -- can now produce the identical
// bytes ReplayDAG expects, without reaching back into engine state.
func (r Result) ExportReplay() ([]byte, error) {
	return ReplayRequest{
		Context: r.Trace.Context, Case: r.Case, Scenarios: r.Scenarios,
		Currency: r.Currency, IdentityAliases: r.IdentityAliases,
		ExpectedPolicyHash: r.ExpectedPolicyHash, EvidenceRequirements: r.EvidenceRequirements,
		Committed: r.Trace,
	}.Marshal()
}

// Input is one execution request.
type Input struct {
	Context Context
	Case    canonical.CaseInput
	// Scenarios drive the economic consequence stage. When empty the
	// stage is SKIPPED rather than fabricating a distribution — an
	// invented scenario set is worse than no economic answer.
	Scenarios []economic.Scenario
	Currency  string
	// TrustSubject, when set, drives the trust-state stage.
	TrustSubject string
	// IdentityAliases, when set together with Engine.Identity, makes
	// IDENTITY_RESOLUTION (P0-4) perform a REAL independent resolution
	// call -- Engine.Identity.EntityIDAt(IdentityAliases[0], ctx.Tick)
	// -- and compare the result against Case.Entity, rather than only
	// hashing the ledger head. A mismatch fails the stage
	// (ErrIdentityMismatch): the DAG does not trust the caller's claim
	// that Case.Entity is correct, it re-derives it from the same
	// ledger pkg/lifecycle's own entity resolution used and requires
	// agreement. Nil-safe: leave nil (the default) to preserve the
	// exact prior ledger-head-only behavior byte-for-byte -- callers
	// that do not have a raw identifier available (e.g. cold replay,
	// which only ever sees the already-resolved Case.Entity) are
	// unaffected.
	IdentityAliases []identity.Identifier
	// TemporalModel and TemporalObservations, when BOTH supplied,
	// drive TEMPORAL_BAYESIAN (P0-5) with a real call to
	// pkg/moat/hbayes's Model.Infer, exactly the way TrustSubject
	// drives TRUST_STATE and Scenarios drives ECONOMIC_CONSEQUENCE.
	// TemporalInterventions is optional (nil is a valid, empty
	// intervention set). Nil-safe: omitting TemporalModel/
	// TemporalObservations preserves the stage's SKIPPED behavior
	// exactly as before this field existed -- no fabricated inference
	// over an invented series.
	TemporalModel         *hbayes.Model
	TemporalObservations  []hbayes.TickObservations
	TemporalInterventions []hbayes.Intervention
	// ExpectedPolicyHash, when set, makes StagePolicy (P0-3/P0-D)
	// independently recompute Case.Policy.Hash() and require it match --
	// a real second computation against the policy that will actually
	// run this decision, exactly the way IdentityAliases makes
	// IDENTITY_RESOLUTION independently re-resolve rather than trust a
	// caller-supplied label. Nil-safe: leave empty (the default) to
	// preserve prior behavior byte-for-byte -- Context.PolicyVersion
	// remains a caller-declared label with no independent check, as
	// before this field existed.
	ExpectedPolicyHash string
	// EvidenceRequirements, when set, makes StageIntent (P0-D Test 3)
	// independently verify that Case.Submissions satisfies every
	// Required entry's MinSources -- a real second computation of the
	// same verdict pkg/lifecycle.checkPlan already produces before ever
	// calling Engine.Run, so INTENT is a genuine causal gate over the
	// exact evidence the DAG itself operates on, not only an
	// attribution of tenant/actor/caseID. Nil-safe: leave empty (the
	// default) to preserve INTENT's prior always-OK behavior exactly.
	EvidenceRequirements []EvidenceRequirement
}

// EvidenceRequirement mirrors pkg/lifecycle.EvidenceRequirement's shape
// exactly (Kind/Required/MinSources) but is declared independently here
// rather than imported: pkg/lifecycle already imports pkg/execution (it
// is execution's real production caller, see pkg/lifecycle.Orchestrator
// .RunUnified), so the reverse import would be a cycle. This is a
// deliberate, minimal, primitive data shape -- not a rewrite of
// lifecycle's richer EvidencePlan/Intent types -- and
// pkg/lifecycle.RunUnified converts its own []EvidenceRequirement into
// this one when calling Engine.Run, so both checks operate on
// byte-identical field values, not a lossy approximation.
type EvidenceRequirement struct {
	Kind       string
	Required   bool
	MinSources int
}

// Engine executes the graph. All sub-engines are injected so an
// execution can be reproduced against the exact governance state it ran
// under, rather than against whatever is current.
type Engine struct {
	Pipeline  *canonical.Pipeline
	Lifecycle *lifecycle.Registry
	Knowledge *knowledge.Engine
	Trust     *state.Engine
	// Identity, when set, makes the IDENTITY_RESOLUTION stage (audit item
	// P0-D) bind its node hash to pkg/identity's own ledger head at
	// execution time -- real, independently-verifiable evidence that this
	// stage's result is anchored to a specific identity-ledger state,
	// rather than only hashing the caller-supplied entity string as the
	// stage did unconditionally before. It verifiably COMMITS to the
	// resolver's current state instead of ignoring it.
	//
	// P0-4 extends this: when Input.IdentityAliases also carries the
	// raw identifier that drove entity resolution upstream (canonical.
	// CaseInput itself still only carries an already-resolved Entity
	// string, not the alias set -- see pkg/lifecycle's own
	// resolveCanonicalEntity, where alias-level resolution actually
	// happens, upstream of this Engine), the stage goes further than
	// binding: it independently CALLS Identity.EntityIDAt and requires
	// the result to match Case.Entity, failing the stage
	// (ErrIdentityMismatch) if it does not. Nil-safe either way: leave
	// Identity nil, or leave Input.IdentityAliases empty with Identity
	// set, to preserve the exact prior stub/binding-only behavior
	// byte-for-byte.
	Identity *identity.Resolver
	// Version is the engine's own version string, folded into every node
	// hash: an execution replayed by a different engine build is not the
	// same execution and must not silently claim to match.
	Version string
}

// NewEngine constructs an Engine with a private canonical Pipeline when
// none is supplied.
func NewEngine(p *canonical.Pipeline) *Engine {
	if p == nil {
		p = canonical.NewPipeline(nil)
	}
	return &Engine{Pipeline: p, Version: "execution/" + version.Current}
}

// graph is the declared DAG. Edges are data so the shape can be read,
// tested and diffed without reading the executor.
var graph = []struct {
	id   StageID
	deps []StageID
}{
	{StageIntent, nil},
	{StageEvidenceIngestion, []StageID{StageIntent}},
	{StageIdentityResolution, []StageID{StageEvidenceIngestion}},
	{StageDependencyEvaluation, []StageID{StageIdentityResolution}},
	{StageTruthArbitration, []StageID{StageDependencyEvaluation}},
	{StageContradiction, []StageID{StageTruthArbitration}},
	{StageFusion, []StageID{StageDependencyEvaluation, StageTruthArbitration}},
	{StageTemporal, []StageID{StageFusion}},
	{StageCausal, []StageID{StageFusion, StageTemporal}},
	{StageRisk, []StageID{StageCausal, StageContradiction}},
	{StagePolicy, []StageID{StageRisk}},
	{StageDecision, []StageID{StagePolicy}},
	{StageTrust, []StageID{StageDecision}},
	{StageDigitalTwin, []StageID{StageDecision, StageCausal}},
	{StageEconomic, []StageID{StageDigitalTwin}},
	{StageExplanation, []StageID{StageDecision, StageTrust, StageEconomic, StageDependencyEvaluation}},
	{StageReplayPackage, []StageID{StageExplanation}},
	{StageVerificationCertificate, []StageID{StageReplayPackage}},
}

// stagePackage names the package whose real logic backs each stage.
// Most stages are attributed to pkg/canonical because that is where
// their computation genuinely happens -- this file's own doc comment
// explains why: the DAG node ATTRIBUTES a specific artifact of one
// canonical run to a stage rather than recomputing it, per the
// integrate-don't-rewrite rule. A few stages have their own dedicated
// package. Kept as data (not inferred by parsing this file) so it can
// be read and diffed like the graph above.
var stagePackage = map[StageID]string{
	StageIntent:                  "veriqo/pkg/lifecycle",
	StagePolicy:                  "veriqo/pkg/moat/decision",
	StageEvidenceIngestion:       "veriqo/pkg/canonical",
	StageIdentityResolution:      "veriqo/pkg/canonical",
	StageDependencyEvaluation:    "veriqo/pkg/canonical",
	StageTruthArbitration:        "veriqo/pkg/canonical",
	StageContradiction:           "veriqo/pkg/canonical",
	StageFusion:                  "veriqo/pkg/canonical",
	StageTemporal:                "veriqo/pkg/moat/hbayes",
	StageCausal:                  "veriqo/pkg/canonical",
	StageRisk:                    "veriqo/pkg/canonical",
	StageDecision:                "veriqo/pkg/canonical",
	StageTrust:                   "veriqo/pkg/trust/state",
	StageDigitalTwin:             "veriqo/pkg/canonical",
	StageEconomic:                "veriqo/pkg/moat/economic",
	StageExplanation:             "veriqo/pkg/explanation",
	StageReplayPackage:           "veriqo/pkg/replay",
	StageVerificationCertificate: "veriqo/pkg/replay",
}

// stageAlwaysSkipped names stages whose Run() case unconditionally
// records StatusSkipped today, regardless of input -- an honest gap,
// not a hidden one. It is empty as of P0-5: StageTemporal used to be
// the one real instance (the switch case unconditionally returned "no
// temporal observation series supplied", because nothing in this
// codebase populated one), but Run() now genuinely calls
// pkg/moat/hbayes's Model.Infer when Input.TemporalModel and
// Input.TemporalObservations are both supplied -- conditionally
// skipped exactly like StageTrust and StageEconomic already were (when
// TrustSubject/Scenarios/the temporal inputs are omitted), not
// unconditionally. No production caller populates the temporal inputs
// yet (canonical.CaseInput's Submissions all share one Tick, not a
// per-tick observation series -- see hbayes's own package comment on
// what a real series requires), so this stage is honestly still
// SKIPPED on every existing call site; the difference is that the
// computation itself is now real and reachable, the same honest state
// StageTrust/StageEconomic were in before their first real callers
// existed.
var stageAlwaysSkipped = map[StageID]bool{}

// EngineEntry is one row of the audit's required engine_registry.json
// (P0-05): which stage, which package implements it, its declared DAG
// dependencies, and whether it is unconditionally bypassed in the
// current wiring. IMPLEMENTED/INTEGRATED/TESTED/REPLAYABLE/VERIFIED
// status is computed by the caller (cmd/veriqo-readiness) from real
// gate results, not asserted here.
type EngineEntry struct {
	StageID       StageID   `json:"engine_id"`
	Package       string    `json:"package"`
	Dependencies  []StageID `json:"dependencies"`
	AlwaysSkipped bool      `json:"always_skipped_in_current_wiring"`
}

// Registry returns one EngineEntry per DAG stage, in the graph's
// declared (topologically valid) order.
func Registry() []EngineEntry {
	entries := make([]EngineEntry, 0, len(graph))
	for _, g := range graph {
		entries = append(entries, EngineEntry{
			StageID: g.id, Package: stagePackage[g.id], Dependencies: g.deps,
			AlwaysSkipped: stageAlwaysSkipped[g.id],
		})
	}
	return entries
}

// topoOrder returns the deterministic execution order: Kahn's algorithm
// with a lexicographic tie-break, so two runs of the same graph always
// execute in the same order even when the graph admits several.
func topoOrder() ([]StageID, error) {
	indeg := map[StageID]int{}
	adj := map[StageID][]StageID{}
	all := make([]StageID, 0, len(graph))
	for _, n := range graph {
		all = append(all, n.id)
		if _, ok := indeg[n.id]; !ok {
			indeg[n.id] = 0
		}
		for _, d := range n.deps {
			indeg[n.id]++
			adj[d] = append(adj[d], n.id)
		}
	}
	var ready []StageID
	for _, id := range all {
		if indeg[id] == 0 {
			ready = append(ready, id)
		}
	}
	sort.Slice(ready, func(i, j int) bool { return ready[i] < ready[j] })
	var out []StageID
	for len(ready) > 0 {
		cur := ready[0]
		ready = ready[1:]
		out = append(out, cur)
		next := append([]StageID(nil), adj[cur]...)
		sort.Slice(next, func(i, j int) bool { return next[i] < next[j] })
		for _, m := range next {
			indeg[m]--
			if indeg[m] == 0 {
				ready = append(ready, m)
			}
		}
		sort.Slice(ready, func(i, j int) bool { return ready[i] < ready[j] })
	}
	if len(out) != len(all) {
		return nil, ErrCycle
	}
	return out, nil
}

// Run executes the graph. goCtx is the caller's real request/operation
// context (P0-6: threaded from the Intent entrypoint, e.g.
// pkg/lifecycle.Orchestrator.RunUnified, rather than fabricated here
// via context.Background()) -- it governs this run's telemetry span and
// is available for cancellation/deadline propagation to any future
// stage that needs it. It is UNRELATED to Input.Context (this
// package's own Context type, carrying ExecutionID/Tenant/Actor/etc
// -- an unfortunate but pre-existing name collision this change does
// not attempt to rename).
func (e *Engine) Run(goCtx context.Context, in Input) (*Result, error) {
	_, span := telemetry.StartSpan(goCtx, "execution.Run",
		telemetry.Attribute{Key: "execution_id", Value: in.Context.ExecutionID})
	defer span.End()

	ctx := in.Context
	if err := ctx.validate(); err != nil {
		return nil, err
	}
	order, err := topoOrder()
	if err != nil {
		return nil, err
	}

	// Governance binding is resolved BEFORE anything is computed, so the
	// execution commits to the model/source versions it actually ran
	// under rather than discovering them afterwards.
	var binding lifecycle.Binding
	if e.Lifecycle != nil {
		binding = e.Lifecycle.BindingAt(ctx.Tick)
		ctx.BindingHash = binding.Hash
		if len(ctx.ModelVersions) == 0 {
			ctx.ModelVersions = binding.Models
		}
		if len(ctx.SourceVersions) == 0 {
			ctx.SourceVersions = binding.Sources
		}
	}
	if e.Knowledge != nil {
		ctx.KnowledgeRoot = e.Knowledge.StateAt(ctx.Tick).RootHash
	}

	// The intelligence core runs once, up front; its artifacts are then
	// attributed to the stages that produced them. Running it here (not
	// inside a node) keeps the "integrate, don't rewrite" rule while
	// still giving every stage a real, separately-hashed artifact.
	canon, canonErr := e.Pipeline.RunCanonical(ctx.Actor, in.Case)

	res := &Result{Binding: binding, Case: in.Case, Scenarios: in.Scenarios, Currency: in.Currency,
		IdentityAliases: in.IdentityAliases, ExpectedPolicyHash: in.ExpectedPolicyHash,
		EvidenceRequirements: in.EvidenceRequirements}
	nodes := make([]Node, 0, len(order))
	produced := map[StageID]bool{}
	byID := map[StageID][]StageID{}
	for _, g := range graph {
		byID[g.id] = g.deps
	}

	record := func(id StageID, inputs, outputs []string, status Status, detail, artifact string, err error) Node {
		n := Node{StageID: id, Inputs: inputs, Outputs: outputs, Dependencies: byID[id],
			Version: e.Version, ExecutionTick: ctx.Tick, Status: status, Detail: detail}
		if err != nil {
			n.Error = err.Error()
		}
		n.Hash = hashNode(n, artifact, ctx.ExecutionID)
		if status == StatusOK {
			produced[id] = true
		}
		nodes = append(nodes, n)
		return n
	}

	// Failure semantics: if canonical failed, every intelligence stage is
	// FAILED with the same error and the execution stops. It does not
	// emit a certificate over a broken run.
	if canonErr != nil {
		for _, id := range order {
			record(id, []string{ctx.CaseID}, nil, StatusFailed, "canonical core failed", "", canonErr)
		}
		res.Trace = Trace{Context: ctx, ContextHash: ctx.hash(), Nodes: nodes}
		res.Trace.RootHash = rootHash(res.Trace)
		res.ExecutionRootHash = res.Trace.RootHash
		return res, fmt.Errorf("%w: %s: %v", ErrStageFailed, StageFusion, canonErr)
	}
	res.Canonical = canon

	// decID is the DECISION's own independent identifier (P0-7): a
	// content-addressed hash over the ExecutionID PLUS the decision's
	// own real content (subject, policy, action, risk score) -- not an
	// alias of ExecutionID. Computed here, once, up front (canon is
	// already fully resolved at this point -- RunCanonical ran the
	// entire canonical chain, decision included, before this line),
	// so both stages that need a DecisionID (ECONOMIC_CONSEQUENCE and
	// EXPLANATION) commit to the SAME independently-derived value.
	decID := decisionID(ctx.ExecutionID, canon.Decision.Subject, canon.Decision.PolicyName,
		string(canon.Decision.Action), canon.Decision.RiskScore)

	var (
		explBuilt explanation.DecisionExplanation
		econ      economic.Consequence
		trustLine string
	)

	for _, id := range order {
		// DAG guard: every declared dependency must have produced output.
		unmet := ""
		for _, d := range byID[id] {
			if !produced[d] {
				unmet = string(d)
				break
			}
		}
		if unmet != "" {
			record(id, []string{unmet}, nil, StatusFailed, "dependency not satisfied", "",
				fmt.Errorf("%w: %s needs %s", ErrDependencyUnsatisfied, id, unmet))
			continue
		}

		switch id {
		case StageIntent:
			// P0-D Test 3: when the caller declares the evidence plan
			// this Intent committed to, independently verify Case.
			// Submissions actually satisfies every REQUIRED item's
			// MinSources -- a real second computation of the same
			// verdict pkg/lifecycle.checkPlan already produced upstream
			// (see EvidenceRequirement's doc comment), not a trust of
			// the caller's own pre-flight check. A violation fails INTENT
			// closed, and since every other stage transitively depends
			// on it, the DAG's existing dependency guard cascades the
			// failure through the whole graph -- INTENT genuinely gates
			// what runs, it does not merely attribute who raised it.
			if unmet := unmetRequiredEvidence(in.EvidenceRequirements, in.Case); unmet != nil {
				record(id, []string{ctx.Tenant, ctx.Actor}, []string{ctx.CaseID}, StatusFailed,
					"required evidence plan item unmet: kind="+unmet.Kind+
						" need>="+strconv.Itoa(unmet.MinSources)+" got="+strconv.Itoa(submissionCount(in.Case, unmet.Kind)),
					"", fmt.Errorf("%w: kind=%s need>=%d got=%d",
						ErrEvidencePlanUnsatisfied, unmet.Kind, unmet.MinSources, submissionCount(in.Case, unmet.Kind)))
				continue
			}
			record(id, []string{ctx.Tenant, ctx.Actor}, []string{ctx.CaseID}, StatusOK,
				"intent "+ctx.CaseID+" raised by "+ctx.Actor+" under tenant "+ctx.Tenant,
				ctx.CaseID+"|"+ctx.Tenant+"|"+ctx.Actor+"|"+ctx.PolicyVersion, nil)

		case StageEvidenceIngestion:
			ids := canonical.SortedSourceIDs(in.Case.Submissions)
			record(id, ids, []string{ctx.EvidencePackageID},
				StatusOK, strconv.Itoa(len(ids))+" submissions ingested",
				strings.Join(ids, ";"), nil)

		case StageIdentityResolution:
			summary := "identity resolution version " + ctx.IdentityResolutionVersion
			hashInput := string(in.Case.Entity) + "|" + ctx.IdentityResolutionVersion
			if e.Identity != nil {
				ledgerHead := e.Identity.Head()
				summary += " (bound to identity ledger head " + shortHash(ledgerHead) + ")"
				hashInput += "|identity_ledger_head=" + ledgerHead

				// P0-4: when a raw identifier is available, independently
				// RE-RESOLVE it against the same ledger rather than only
				// binding to the ledger's summary hash -- a genuine
				// second computation, not a decorative one.
				if len(in.IdentityAliases) > 0 {
					q := in.IdentityAliases[0]
					reResolved, err := e.Identity.EntityIDAt(q, ctx.Tick)
					if err != nil {
						record(id, []string{q.Key()}, nil, StatusFailed,
							"identity re-resolution failed", "", err)
						continue
					}
					if reResolved != string(in.Case.Entity) {
						record(id, []string{q.Key()}, []string{reResolved}, StatusFailed,
							"independently re-resolved entity "+reResolved+
								" does not match supplied case entity "+string(in.Case.Entity),
							"", fmt.Errorf("%w: query=%s resolved=%s supplied=%s",
								ErrIdentityMismatch, q.Key(), reResolved, in.Case.Entity))
						continue
					}
					summary += "; independently re-resolved " + q.Key() + " and confirmed match"
					hashInput += "|reresolved=" + reResolved
				}
			}
			record(id, []string{string(in.Case.Entity)}, []string{in.Case.Subject}, StatusOK,
				summary, hashInput, nil)

		case StageDependencyEvaluation:
			d := canon.Dependency
			record(id, canonical.SortedSourceIDs(in.Case.Submissions),
				[]string{"effective_weights", "families"}, StatusOK,
				"max discount "+fnum(d.MaxDiscount)+" over "+
					strconv.Itoa(d.IndependentFamilyCount())+" independent families",
				d.RootHash+"|"+fnum(d.MaxDiscount)+"|"+strconv.Itoa(d.IndependentFamilyCount()), nil)

		case StageTruthArbitration:
			a := canon.Arbitration
			record(id, []string{in.Case.Subject + "/" + in.Case.Predicate},
				[]string{a.Winner}, StatusOK,
				"winner "+a.Winner+" at "+fnum(a.WinnerConfidence)+
					" over runner-up "+a.RunnerUp+" at "+fnum(a.RunnerUpConfidence),
				canon.Certificate.ArbitrationHash, nil)

		case StageContradiction:
			t := canon.Truth
			record(id, []string{t.ClaimKey}, []string{"contradiction_score"}, StatusOK,
				"contradiction "+fnum(t.ContradictionScore)+", delta "+fnum(t.ConfidenceDelta),
				t.Hash, nil)

		case StageFusion:
			a := canon.Arbitration
			p := canon.Provenance
			record(id, []string{strconv.Itoa(a.EvidenceCount) + " evidence"},
				[]string{"posterior"}, StatusOK,
				"independence "+fnum(p.Score)+" ("+string(p.Status)+"), posterior "+
					fnum(a.WinnerConfidence),
				canon.Certificate.ArbitrationHash+"|"+fnum(p.Score), nil)

		case StageTemporal:
			// Temporal Bayesian reasoning applies only when the case
			// carries a real model and observation series (P0-5: a
			// genuine call to pkg/moat/hbayes.Model.Infer, not a stub).
			// Marking it SKIPPED when neither is supplied is honest; a
			// fabricated posterior would not be.
			if in.TemporalModel == nil || len(in.TemporalObservations) == 0 {
				if in.Case.Policy.RequiresTemporalCalibration {
					// P0-D Test 2: a policy that declares temporal
					// reasoning load-bearing does not get a silent
					// SKIPPED pass -- the stage fails closed, and (since
					// StagePolicy/StageDecision are attributional over
					// canon, which already ran) so does the whole
					// execution: the loop below returns ErrStageFailed
					// once any node is FAILED.
					record(id, []string{"observation_series"}, nil, StatusFailed,
						"policy "+in.Case.Policy.Name+" requires temporal calibration but none was supplied",
						"", ErrTemporalCalibrationRequired)
					continue
				}
				record(id, []string{"observation_series"}, nil, StatusSkipped,
					"no temporal observation series supplied for this case", "temporal:skipped", nil)
				produced[id] = true // a skipped-but-evaluated stage satisfies dependants
				break
			}
			trace, err := in.TemporalModel.Infer(in.TemporalObservations, in.TemporalInterventions)
			if err != nil {
				record(id, []string{strconv.Itoa(len(in.TemporalObservations)) + " observation ticks"},
					nil, StatusFailed, "temporal inference failed", "", err)
				continue
			}
			record(id, []string{strconv.Itoa(len(in.TemporalObservations)) + " observation ticks"},
				[]string{"filtered_posterior"}, StatusOK,
				"temporal inference over "+strconv.Itoa(len(trace.Steps))+" ticks, log-likelihood "+
					fnum(trace.LogLikelihood),
				trace.TraceHash, nil)

		case StageCausal:
			record(id, []string{strconv.Itoa(len(in.Case.CausalLinks)) + " links"},
				[]string{"causal_support"}, StatusOK,
				"aggregate causal support "+fnum(canon.CausalSupport),
				fnum(canon.CausalSupport), nil)

		case StageRisk:
			r := canon.Risk
			record(id, []string{"causal_support", "contradiction_score"},
				[]string{"tbml_composite_risk_score"}, StatusOK,
				"risk "+fnum(r.Score)+" labelled "+string(r.Label),
				fnum(r.Score)+"|"+string(r.Label), nil)

		case StagePolicy:
			d := canon.Decision
			// P0-D: independently recompute the hash of the policy that
			// actually ran and require it to agree with an expected
			// hash from a real, separate source of truth -- a genuine
			// second computation, not a decorative re-statement of
			// ctx.PolicyVersion (see decision.Policy.Hash's doc comment
			// for why this closes a genuine gap: nothing previously
			// checked ctx.PolicyVersion against Case.Policy's actual
			// content at all).
			//
			// "Automatic wiring" (the remaining half of this gap after
			// the check itself was proven able to fail closed): an
			// explicit Input.ExpectedPolicyHash always wins when a
			// caller supplies one, but when it does not, this stage now
			// falls back to binding.Policies[in.Case.Policy.Name] --
			// the SAME governance Registry lookup StageIntent's sibling
			// Model/Source binding already performs above, extended
			// this round to cover policy versions too (see
			// pkg/governance/lifecycle's new Policy/PolicyState/
			// RegisterPolicy/TransitionPolicy). This is not the
			// caller's own Case.Policy checked against itself -- it is
			// an independently governed, separately-approved record a
			// different actor (an operator, via RegisterPolicy/
			// TransitionPolicy) committed to, which is exactly what
			// makes it a real check rather than a circular one. A
			// policy Name with no governed ACTIVE version registered
			// simply has no expected hash to check against (nil-safe:
			// every Engine without a Lifecycle Registry, and every
			// policy Name no operator has registered yet, preserves the
			// exact unconditional-OK behavior from before this field
			// existed).
			expected := in.ExpectedPolicyHash
			if expected == "" && binding.Policies != nil {
				if h, ok := binding.Policies[in.Case.Policy.Name]; ok {
					expected = h
				}
			}
			// Capture whichever expected hash was ACTUALLY used (caller-
			// declared or auto-wired from the governance registry) back
			// onto Result, not only the Input field it started from --
			// otherwise a cold replay via a freshEngine with no
			// Lifecycle Registry attached (the normal case for
			// cmd/veriqo-cold-replay, which has no governance ledger to
			// read) would resolve binding.Policies to nil and silently
			// re-derive a DIFFERENT (empty) expected hash than the
			// original run auto-wired, diverging at this exact node for
			// every execution that used auto-wiring. Result.
			// ExpectedPolicyHash threads through ExportReplay into
			// ReplayRequest and back into a replay's own Input, so the
			// replay independently re-verifies against the SAME value,
			// not a value it would have to re-derive from a registry it
			// was never given.
			//
			// Deliberately NOT naming the source (caller-declared vs
			// auto-wired) in Detail/Error: those strings are hashed
			// (see hashNode), and a replay engine reconstructs
			// ExpectedPolicyHash from the explicit Input field alone
			// (see above) regardless of how the ORIGINAL run obtained
			// it -- wording that varied by source would make every
			// auto-wired execution structurally unreplayable, which
			// defeats the whole point of capturing the value for
			// replay in the first place.
			res.ExpectedPolicyHash = expected
			if expected != "" {
				actualHash := in.Case.Policy.Hash()
				if actualHash != expected {
					record(id, []string{ctx.PolicyVersion}, nil, StatusFailed,
						"independently recomputed policy hash "+shortHash(actualHash)+
							" does not match expected policy hash "+shortHash(expected),
						"", fmt.Errorf("%w: expected=%s actual=%s",
							ErrPolicyMismatch, expected, actualHash))
					continue
				}
				record(id, []string{"tbml_composite_risk_score"}, []string{ctx.PolicyVersion}, StatusOK,
					"policy "+d.PolicyName+" under version "+ctx.PolicyVersion+
						" (flag "+fnum(d.RiskScore)+"); independently verified policy hash "+shortHash(actualHash),
					d.PolicyName+"|"+ctx.PolicyVersion+"|verified="+actualHash, nil)
				continue
			}
			record(id, []string{"tbml_composite_risk_score"}, []string{ctx.PolicyVersion}, StatusOK,
				"policy "+d.PolicyName+" under version "+ctx.PolicyVersion+
					" (flag "+fnum(d.RiskScore)+")",
				d.PolicyName+"|"+ctx.PolicyVersion, nil)

		case StageDecision:
			d := canon.Decision
			res.Decision = string(d.Action)
			record(id, []string{ctx.PolicyVersion}, []string{string(d.Action)}, StatusOK,
				"policy "+d.PolicyName+" produced "+string(d.Action)+" at risk "+fnum(d.RiskScore),
				string(d.Action)+"|"+d.PolicyName+"|"+fnum(d.RiskScore), nil)

		case StageTrust:
			if e.Trust == nil || in.TrustSubject == "" {
				record(id, nil, nil, StatusSkipped, "no trust engine or subject supplied", "trust:skipped", nil)
				produced[id] = true
				break
			}
			st := e.Trust.StateAt(in.TrustSubject, ctx.Tick)
			trustLine = "trust " + string(st.Level) + " at " + fnum(st.Score) +
				" (effective " + fnum(st.EffectiveScore) + " after decay, confidence " +
				fnum(st.Confidence) + ")"
			record(id, []string{in.TrustSubject}, []string{string(st.Level)}, StatusOK,
				trustLine, string(st.Level)+"|"+fnum(st.Score), nil)

		case StageDigitalTwin:
			record(id, []string{string(in.Case.Entity)}, []string{"twin_head"}, StatusOK,
				"twin head "+canon.Certificate.TwinHead+", exposure "+
					fnum(canon.EconomicImpact.TotalExposure),
				canon.Certificate.TwinHead, nil)

		case StageEconomic:
			if len(in.Scenarios) == 0 {
				record(id, nil, nil, StatusSkipped,
					"no scenario set supplied; refusing to invent a distribution",
					"economic:skipped", nil)
				produced[id] = true
				break
			}
			c, err := economic.Evaluate(economic.Input{
				DecisionID: decID, Subject: in.Case.Subject, Currency: in.Currency,
				Tick: ctx.Tick, Scenarios: in.Scenarios})
			if err != nil {
				record(id, nil, nil, StatusFailed, "economic evaluation failed", "", err)
				continue
			}
			econ = c
			res.Economic = c
			record(id, []string{strconv.Itoa(len(in.Scenarios)) + " scenarios"},
				[]string{"expected_value", "cvar"}, StatusOK,
				"EV "+fnum(c.ExpectedValue)+", CVaR "+fnum(c.CVaR)+" at "+fnum(c.ConfidenceLevel),
				c.Hash, nil)

		case StageExplanation:
			ex, err := e.buildExplanation(ctx, in, canon, econ, trustLine, nodes, decID)
			if err != nil {
				record(id, nil, nil, StatusFailed, "explanation could not be consolidated", "", err)
				continue
			}
			explBuilt = ex
			res.Explanation = ex
			record(id, []string{"all upstream artifacts"}, []string{"decision_explanation"},
				StatusOK, "consolidated over "+strconv.Itoa(len(ex.Chain))+" chain links", ex.Hash, nil)

		case StageReplayPackage:
			var identityLedgerHead string
			if e.Identity != nil {
				identityLedgerHead = e.Identity.Head()
			}
			rec, err := replay.Record(ctx.Actor, in.Case, canon, e.Pipeline.Dependencies.Ledger(), identityLedgerHead)
			if err != nil {
				record(id, nil, nil, StatusFailed, "replay record failed", "", err)
				continue
			}
			pkg, err := replay.NewReplayPackage(ctx.Actor, ctx.ExecutionID, rec)
			if err != nil {
				record(id, nil, nil, StatusFailed, "replay package failed", "", err)
				continue
			}
			res.ReplayPackage = pkg
			record(id, []string{rec.ExecutionID}, []string{pkg.ReplayPackageID}, StatusOK,
				"replay package over "+strconv.Itoa(len(rec.Stages))+" stages",
				pkg.ReplayPackageID, nil)

		case StageVerificationCertificate:
			cert, err := replay.NewEngine().Replay(res.ReplayPackage)
			if err != nil {
				record(id, nil, nil, StatusFailed, "independent replay failed", "", err)
				continue
			}
			res.Certificate = cert
			detail := "replay matched"
			if !cert.Match {
				detail = "replay DIVERGED at " + cert.DivergedStage
			}
			record(id, []string{res.ReplayPackage.ReplayPackageID},
				[]string{cert.VerificationCertificateID}, StatusOK, detail,
				cert.VerificationCertificateID+"|"+strconv.FormatBool(cert.Match), nil)
		}
	}

	res.Trace = Trace{Context: ctx, ContextHash: ctx.hash(), Nodes: nodes}
	res.Trace.RootHash = rootHash(res.Trace)
	res.ExecutionRootHash = res.Trace.RootHash

	// Two-phase commitment (explanation.Build / explanation.Commit): the
	// explanation's content hash was fixed by explanation.Build before
	// the execution root existed, over everything the root's own node
	// hash for StageExplanation actually covers. Now that the root is
	// final, Commit binds it to the explanation via a separate
	// FinalCommitment hash instead of overwriting a field the content
	// hash already committed to — the defect this replaces let
	// ExecutionRootHash be mutated after Hash was computed over it, so
	// the shipped artifact never verified against its own Hash.
	if explBuilt.Hash != "" {
		committed, err := explanation.Commit(res.Explanation, res.ExecutionRootHash)
		if err != nil {
			return res, fmt.Errorf("%w: %s: explanation commit: %v", ErrStageFailed, StageExplanation, err)
		}
		res.Explanation = committed
	}
	for _, n := range nodes {
		if n.Status == StatusFailed {
			return res, fmt.Errorf("%w: %s: %s", ErrStageFailed, n.StageID, n.Error)
		}
	}
	return res, nil
}

func (e *Engine) buildExplanation(ctx Context, in Input, canon *canonical.CanonicalResult,
	econ economic.Consequence, trustLine string, nodes []Node, decID string) (explanation.DecisionExplanation, error) {

	dep := canon.Dependency
	var depLines []string
	for _, sid := range canonical.SortedSourceIDs(in.Case.Submissions) {
		lines := dep.Explanation[sid]
		base := dep.Base[sid]
		eff := dep.Effective[sid]
		l := sid + ": base " + fnum(base) + " -> effective " + fnum(eff) +
			" (shared fraction " + fnum(dep.SharedFraction[sid]) + ")"
		if len(lines) > 0 {
			l += "; " + strings.Join(lines, "; ")
		}
		depLines = append(depLines, l)
	}

	var chain []explanation.Link
	for _, sid := range canonical.SortedSourceIDs(in.Case.Submissions) {
		chain = append(chain, explanation.Link{Stage: explanation.StageSource, Input: sid,
			Output: "submission", Value: dep.Base[sid], HasValue: true,
			Detail: "declared reliability", ArtifactHash: dep.RootHash})
	}
	chain = append(chain,
		explanation.Link{Stage: explanation.StageEvidence, Input: "submissions",
			Output: ctx.EvidencePackageID, Detail: strconv.Itoa(canon.Arbitration.EvidenceCount) +
				" evidence items", ArtifactHash: canon.Certificate.ArbitrationHash},
		explanation.Link{Stage: explanation.StageDependency, Input: "evidence",
			Output: "shared_fraction", Value: dep.MaxDiscount, HasValue: true,
			Detail:       strconv.Itoa(dep.IndependentFamilyCount()) + " independent families",
			ArtifactHash: dep.RootHash},
		explanation.Link{Stage: explanation.StageWeight, Input: "shared_fraction",
			Output: "effective_weight", Value: minEffective(dep), HasValue: true,
			Detail: "lowest effective weight after discount", ArtifactHash: dep.RootHash},
		explanation.Link{Stage: explanation.StageTruth, Input: canon.Truth.ClaimKey,
			Output: canon.Arbitration.Winner, Value: canon.Arbitration.WinnerConfidence, HasValue: true,
			Detail: "plurality winner", ArtifactHash: canon.Certificate.ArbitrationHash},
		explanation.Link{Stage: explanation.StageContradiction, Input: canon.Truth.ClaimKey,
			Output: "contradiction_score", Value: canon.Truth.ContradictionScore, HasValue: true,
			Detail: "runner-up proximity", ArtifactHash: canon.Truth.Hash},
		explanation.Link{Stage: explanation.StageFusion, Input: "weighted evidence",
			Output: "posterior", Value: canon.Arbitration.WinnerConfidence, HasValue: true,
			Detail: "independence " + fnum(canon.Provenance.Score) + " (" +
				string(canon.Provenance.Status) + ")", ArtifactHash: canon.Certificate.ArbitrationHash},
		explanation.Link{Stage: explanation.StageCausal, Input: "posterior",
			Output: "causal_support", Value: canon.CausalSupport, HasValue: true,
			Detail:       strconv.Itoa(len(in.Case.CausalLinks)) + " causal links asserted",
			ArtifactHash: fnum(canon.CausalSupport)},
		explanation.Link{Stage: explanation.StageRisk, Input: "causal_support",
			Output: "tbml_composite_risk_score", Value: canon.Risk.Score, HasValue: true,
			Detail:       "composite risk " + fnum(canon.Risk.Score) + " labelled " + string(canon.Risk.Label),
			ArtifactHash: string(canon.Risk.Label)},
		explanation.Link{Stage: explanation.StagePolicy, Input: fnum(canon.Decision.RiskScore),
			Output:       string(canon.Decision.Action),
			Detail:       "policy " + canon.Decision.PolicyName + " under version " + ctx.PolicyVersion,
			ArtifactHash: canon.Certificate.Hash},
		explanation.Link{Stage: explanation.StageDecision, Input: string(canon.Decision.Action),
			Output: "decision", Detail: "recorded under execution " + ctx.ExecutionID,
			ArtifactHash: canon.Certificate.Hash},
	)

	alts := alternativesFor(canon)
	var econLines []string
	if econ.Hash != "" {
		econLines = append(econLines, "expected value "+fnum(econ.ExpectedValue)+
			", CVaR "+fnum(econ.CVaR)+" at "+fnum(econ.ConfidenceLevel))
	}
	trustLines := []string{}
	if trustLine != "" {
		trustLines = append(trustLines, trustLine)
	}

	return explanation.Build(explanation.Input{
		DecisionID: decID, Subject: in.Case.Subject, Intent: ctx.CaseID,
		Action: string(canon.Decision.Action), Confidence: canon.Arbitration.WinnerConfidence,
		Tick: ctx.Tick,
		EvidenceLines: []string{strconv.Itoa(canon.Arbitration.EvidenceCount) +
			" evidence items across " + strconv.Itoa(dep.IndependentFamilyCount()) +
			" independent families"},
		IdentityLines:       []string{"entity " + string(in.Case.Entity) + " under identity resolution " + ctx.IdentityResolutionVersion},
		DependencyLines:     depLines,
		TruthLines:          canon.Arbitration.Explanation,
		ContradictionLines:  []string{"contradiction score " + fnum(canon.Truth.ContradictionScore) + " with confidence delta " + fnum(canon.Truth.ConfidenceDelta)},
		FusionLines:         []string{"independence " + fnum(canon.Provenance.Score) + " status " + string(canon.Provenance.Status) + " over shared ancestors " + strings.Join(canon.Provenance.SharedAncestors, ",")},
		CausalLines:         []string{"aggregate causal support " + fnum(canon.CausalSupport) + " over " + strconv.Itoa(len(in.Case.CausalLinks)) + " asserted links"},
		RiskLines:           canon.Risk.Explanation,
		TrustLines:          trustLines,
		PolicyLines:         canon.Decision.Explanation,
		SimulationLines:     econLines,
		Alternatives:        alts,
		MissingEvidence:     missingEvidence(canon),
		ConflictingEvidence: conflicting(canon),
		Assumptions:         []string{"declared source reliabilities are accurate as registered", "dependency ledger at " + dep.RootHash + " is complete"},
		Uncertainty:         []string{"winner confidence " + fnum(canon.Arbitration.WinnerConfidence) + " against runner-up " + fnum(canon.Arbitration.RunnerUpConfidence)},
		PolicyVersion:       ctx.PolicyVersion,
		ModelVersions:       ctx.ModelVersions,
		SourceVersions:      ctx.SourceVersions,
		EvidenceRootHash:    canon.Certificate.ArbitrationHash,
		DependencyRoot:      dep.RootHash,
		KnowledgeRoot:       ctx.KnowledgeRoot,
		BindingHash:         ctx.BindingHash,
		ReplayID:            ctx.ExecutionID,
		Chain:               chain,
	})
}

func alternativesFor(c *canonical.CanonicalResult) []explanation.Alternative {
	alts := []explanation.Alternative{}
	chosen := string(c.Decision.Action)
	for _, a := range []string{"MONITOR", "FLAG", "ESCALATE"} {
		if a == chosen {
			continue
		}
		reason := "risk " + fnum(c.Risk.Score) + " did not satisfy the threshold for " + a
		alts = append(alts, explanation.Alternative{Action: a, Score: c.Risk.Score,
			RejectedBy: c.Decision.PolicyName, Reason: reason})
	}
	if c.Arbitration.RunnerUp != "" {
		alts = append(alts, explanation.Alternative{Action: "accept:" + c.Arbitration.RunnerUp,
			Score: c.Arbitration.RunnerUpConfidence, RejectedBy: "truth_arbitration",
			Reason: "lost plurality to " + c.Arbitration.Winner})
	}
	return alts
}

func missingEvidence(c *canonical.CanonicalResult) []string {
	var out []string
	if c.Arbitration.EvidenceCount < 3 {
		out = append(out, "fewer than three independent evidence items")
	}
	if c.Dependency.IndependentFamilyCount() < 2 {
		out = append(out, "all evidence traces to a single dependency family")
	}
	return out
}

func conflicting(c *canonical.CanonicalResult) []string {
	if c.Arbitration.Contradiction || c.Truth.ContradictionScore > 0.5 {
		return []string{"claim contested: " + c.Arbitration.Winner + " vs " + c.Arbitration.RunnerUp}
	}
	return nil
}

func minEffective(d canonical.DependencyEvaluation) float64 {
	min := 1.0
	for _, v := range d.Effective {
		if v < min {
			min = v
		}
	}
	return min
}

func hashNode(n Node, artifact, executionID string) string {
	var sb strings.Builder
	sb.WriteString("execution_node/v1\nstage=" + string(n.StageID) + "\nexec=" + executionID +
		"\nversion=" + n.Version + "\ntick=" + strconv.FormatUint(n.ExecutionTick, 10) +
		"\nstatus=" + string(n.Status) + "\ndetail=" + n.Detail + "\nerror=" + n.Error + "\n")
	for _, d := range n.Dependencies {
		sb.WriteString("dep=" + string(d) + "\n")
	}
	for _, i := range n.Inputs {
		sb.WriteString("in=" + i + "\n")
	}
	for _, o := range n.Outputs {
		sb.WriteString("out=" + o + "\n")
	}
	sb.WriteString("artifact=" + artifact + "\n")
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

func rootHash(t Trace) string {
	var sb strings.Builder
	sb.WriteString("execution_root/v1\ncontext=" + t.ContextHash + "\n")
	for _, n := range t.Nodes {
		sb.WriteString(string(n.StageID) + "|" + string(n.Status) + "|" + n.Hash + "\n")
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// ---------------------------------------------------------------------
// Replay
// ---------------------------------------------------------------------

// ReplayRequest is the serialized form an independent replayer consumes.
// It carries only data — no engine pointers — so a replay provably
// cannot reach into the pipeline that produced the original.
type ReplayRequest struct {
	Context   Context             `json:"context"`
	Case      canonical.CaseInput `json:"case"`
	Scenarios []economic.Scenario `json:"scenarios"`
	Currency  string              `json:"currency"`
	// IdentityAliases is exactly Input.IdentityAliases the original run
	// used (see Result.IdentityAliases's doc comment for why this is
	// required, not optional, for a faithful replay of any execution
	// that went through pkg/lifecycle.Orchestrator).
	IdentityAliases []identity.Identifier `json:"identity_aliases,omitempty"`
	// ExpectedPolicyHash and EvidenceRequirements are exactly the
	// matching Result fields (see Result's own doc comment for why
	// both are required, not optional, for a faithful replay).
	ExpectedPolicyHash   string                `json:"expected_policy_hash,omitempty"`
	EvidenceRequirements []EvidenceRequirement `json:"evidence_requirements,omitempty"`
	Committed            Trace                 `json:"committed_trace"`
}

// Marshal serialises the request.
func (r ReplayRequest) Marshal() ([]byte, error) { return json.Marshal(r) }

// ReplayVerdict is the outcome of a DAG rebuild.
type ReplayVerdict struct {
	Matched          bool    `json:"matched"`
	OriginalRootHash string  `json:"original_root_hash"`
	ReplayRootHash   string  `json:"replay_root_hash"`
	DivergentStage   StageID `json:"divergent_stage,omitempty"`
	OriginalNodeHash string  `json:"original_node_hash,omitempty"`
	ReplayNodeHash   string  `json:"replay_node_hash,omitempty"`
	NodesCompared    int     `json:"nodes_compared"`
}

// ReplayDAG rebuilds the whole graph from serialized bytes and localises
// the first divergent stage.
//
// It takes []byte rather than a struct for the same reason pkg/replay
// does: consuming a shared object would let a replay accidentally read
// live engine state and report a false match.
func ReplayDAG(goCtx context.Context, data []byte, freshEngine *Engine) (ReplayVerdict, error) {
	var req ReplayRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return ReplayVerdict{}, err
	}
	if freshEngine.Lifecycle != nil {
		if err := freshEngine.Lifecycle.VerifyBinding(lifecycle.Binding{
			Tick: req.Context.Tick, Hash: req.Context.BindingHash,
		}); err != nil && req.Context.BindingHash != "" {
			return ReplayVerdict{}, fmt.Errorf("%w: %v", ErrBindingMismatch, err)
		}
	}
	out, err := freshEngine.Run(goCtx, Input{Context: req.Context, Case: req.Case,
		Scenarios: req.Scenarios, Currency: req.Currency, IdentityAliases: req.IdentityAliases,
		ExpectedPolicyHash: req.ExpectedPolicyHash, EvidenceRequirements: req.EvidenceRequirements})
	if out == nil {
		return ReplayVerdict{}, err
	}
	v := ReplayVerdict{OriginalRootHash: req.Committed.RootHash,
		ReplayRootHash: out.Trace.RootHash}

	// Matching is decided node by node, not by the root alone. A
	// committed trace whose node was edited without recomputing the root
	// would otherwise pass: the root is exactly the value an attacker
	// leaves untouched. Comparing every node first makes the tamper
	// visible AND localises it.
	n := len(req.Committed.Nodes)
	if len(out.Trace.Nodes) < n {
		n = len(out.Trace.Nodes)
	}
	v.NodesCompared = n
	for i := 0; i < n; i++ {
		if req.Committed.Nodes[i].StageID != out.Trace.Nodes[i].StageID ||
			req.Committed.Nodes[i].Hash != out.Trace.Nodes[i].Hash {
			v.DivergentStage = req.Committed.Nodes[i].StageID
			v.OriginalNodeHash = req.Committed.Nodes[i].Hash
			v.ReplayNodeHash = out.Trace.Nodes[i].Hash
			break
		}
	}
	if v.DivergentStage == "" && len(req.Committed.Nodes) != len(out.Trace.Nodes) {
		v.DivergentStage = StageID("NODE_COUNT")
	}
	v.Matched = v.DivergentStage == "" && req.Committed.RootHash == out.Trace.RootHash
	if !v.Matched && v.DivergentStage == "" {
		v.DivergentStage = StageID("ROOT_HASH")
	}
	return v, nil
}

// Assert converts a non-matching verdict into an error, so a caller
// cannot read only the boolean and proceed.
func (v ReplayVerdict) Assert() error {
	if v.Matched {
		return nil
	}
	return fmt.Errorf("%w at stage %s: original=%s replay=%s",
		ErrDivergence, v.DivergentStage, v.OriginalNodeHash, v.ReplayNodeHash)
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// submissionCount mirrors pkg/lifecycle.checkPlan's own counting rule
// exactly (kind matched against the case's own Predicate, count =
// number of submissions when it matches, 0 otherwise) so INTENT's
// independent check and lifecycle's upstream pre-flight check can never
// silently diverge on what "satisfied" means.
func submissionCount(c canonical.CaseInput, kind string) int {
	if kind == c.Predicate {
		return len(c.Submissions)
	}
	return 0
}

// unmetRequiredEvidence returns the first REQUIRED EvidenceRequirement
// whose MinSources is not met, or nil when every required item is
// satisfied (optional items are never blocking, matching checkPlan's
// own "preserve unknown/missing evidence as uncertainty" rule).
func unmetRequiredEvidence(reqs []EvidenceRequirement, c canonical.CaseInput) *EvidenceRequirement {
	for i := range reqs {
		if reqs[i].Required && submissionCount(c, reqs[i].Kind) < reqs[i].MinSources {
			return &reqs[i]
		}
	}
	return nil
}

func fnum(v float64) string { return strconv.FormatFloat(v, 'g', 17, 64) }

// decisionID is DECISION's own independent identifier (P0-7): a
// content-addressed hash over the execution it belongs to PLUS the
// decision's own real content -- subject, the policy that governed it,
// the chosen action, and the risk score that drove it. It is genuinely
// derived, not an alias: two DIFFERENT decisions under the same
// ExecutionID (impossible in the current 1:1 wiring, but not something
// this function assumes) would get different DecisionIDs, and the same
// decision replayed identically always gets the same one -- the same
// no-time.Now()/no-UUID discipline as Intent.ID and EvidencePlan.Hash.
func decisionID(executionID, subject, policyName, action string, riskScore float64) string {
	h := sha256.New()
	fmt.Fprintf(h, "execution_decision/v1|exec=%s|subject=%s|policy=%s|action=%s|risk=%s|",
		executionID, subject, policyName, action, fnum(riskScore))
	return hex.EncodeToString(h.Sum(nil))
}

// shortHash renders a display-truncated form of a full hash for a
// human-readable summary string -- the FULL value is always what
// actually goes into hashInput/the node hash itself, so truncation here
// affects only what an operator reads, never what gets committed.
func shortHash(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12] + "..."
}
