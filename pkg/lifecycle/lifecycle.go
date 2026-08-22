// Package lifecycle closes the P0 gap both v7.10.2 audit documents
// named as the top architectural priority: VERIQO's canonical path
// (pkg/canonical, v7.10.2) starts from Evidence, not from Intent. The
// audit's required contract is broader:
//
//	Intent -> Evidence Planning -> Entity Resolution -> Evidence
//	-> Truth -> Trust -> Bayesian -> Causal -> Decision -> Policy
//	-> Digital Twin -> Economic Consequence -> IVF -> Outcome
//	-> Calibration -> Immutable Replay
//
// RunUnified is that lifecycle. Same discipline as pkg/canonical: this
// package does not duplicate any engine — it composes pkg/canonical
// (Evidence/Provenance/Fusion/Truth/Causal/Risk/Decision/Twin/Economic
// Impact/Certificate), pkg/moat/entity (Entity Resolution),
// pkg/verification (IVF — reused, not rebuilt: a real
// EvidencePackage/ReplayPackage/VerificationBundle is built from the
// SAME fusion records pkg/canonical already produced, with a genuine
// independently-registered ReplayFunc), and pkg/moat/calibration
// (Outcome -> Trust Reassessment).
//
// Intent, here, is deliberately a NEW top-level type — not a
// duplicate of pkg/kernel/intent.Statement. That package verifies one
// actor's specific factual claim against an observation (Class
// A-vs-Class-C accuracy scoring); it is a valid INPUT a lifecycle case
// can carry evidence from, not a substitute for "why is this
// investigation happening at all", which nothing in the repo modeled
// before this package. This is filling the audit's named gap #1, not
// adding a second orchestration framework — see the audit's own "What
// should not be done" section, which this package's design was
// checked against before writing any code.
package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"

	"veriqo/internal/version"
	"veriqo/pkg/canonical"
	"veriqo/pkg/execution"
	bayescalibration "veriqo/pkg/governance/calibration"
	"veriqo/pkg/identity"
	"veriqo/pkg/lineage"
	"veriqo/pkg/moat/calibration"
	"veriqo/pkg/moat/digitaltwin"
	"veriqo/pkg/moat/entity"
	"veriqo/pkg/moat/fusion"
	"veriqo/pkg/moat/hbayes"
	"veriqo/pkg/platform/correlation"
	"veriqo/pkg/platform/telemetry"
	"veriqo/pkg/verification"
)

// identityAuthoritySourceID is the source ID RunUnified registers its
// own identity-resolver merges under (see NewOrchestrator). It is a
// real, greppable source name, not a placeholder -- the ledger records
// every merge as originating from "lifecycle", the caller that
// actually decided the two aliases denote one entity.
const identityAuthoritySourceID = "lifecycle"

// Intent is the OS-level top-level canonical input the audit requires:
// an investigative objective, not yet evidence. It is content-
// addressed (ID is derived from its own fields, not caller-supplied)
// so IntentID is stable and independently reproducible from the same
// inputs — consistent with this repo's no-UUID, no-time.Now()
// discipline.
type Intent struct {
	ActorID   string
	Objective string // e.g. "assess dark-vessel risk"
	// Tenant identifies which tenant this Intent was raised under. It is
	// a real, required field (P0-1: pkg/execution.Context.Tenant is
	// mandatory and this repository has never modeled a fabricated
	// default) -- the caller that knows which tenant an investigation
	// belongs to must say so explicitly, the same way it must supply
	// ActorID; RunUnified refuses (via execution.Context.validate) to
	// silently run under an invented tenant.
	Tenant             string
	EntityAliases      []entity.Alias
	RequiredConfidence float64
	TemporalScope      string
	PolicyConstraints  []string
	Tick               uint64
}

// ID is Intent's content-addressed identifier.
func (in Intent) ID() string {
	h := sha256.New()
	fmt.Fprintf(h, "actor=%s|tenant=%s|obj=%s|conf=%.4f|scope=%s|tick=%d|",
		in.ActorID, in.Tenant, in.Objective, in.RequiredConfidence, in.TemporalScope, in.Tick)
	aliases := make([]string, len(in.EntityAliases))
	for i, a := range in.EntityAliases {
		aliases[i] = a.Kind + "|" + a.Value
	}
	sort.Strings(aliases)
	for _, a := range aliases {
		fmt.Fprintf(h, "alias=%s|", a)
	}
	constraints := append([]string(nil), in.PolicyConstraints...)
	sort.Strings(constraints)
	for _, c := range constraints {
		fmt.Fprintf(h, "policy=%s|", c)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// EvidenceRequirement is one thing an Intent's plan says evidence
// gathering must satisfy.
type EvidenceRequirement struct {
	Kind       string // e.g. "AIS_STATUS" — matched against CaseInput.Predicate
	Required   bool
	MinSources int
}

// EvidencePlan is the audit's required "Intent must generate an
// explicit Evidence Plan" artifact — versioned, hashable,
// deterministic, replayable, auditable (all four properties come from
// Hash being a pure function of IntentID + Requirements, nothing
// else).
type EvidencePlan struct {
	IntentID     string
	Requirements []EvidenceRequirement
	Hash         string
}

// PlanEvidence deterministically builds an EvidencePlan from an
// Intent and its requirements — pure function, no side effects, no
// wall-clock.
func PlanEvidence(in Intent, requirements []EvidenceRequirement) EvidencePlan {
	p := EvidencePlan{IntentID: in.ID(), Requirements: requirements}
	h := sha256.New()
	fmt.Fprintf(h, "intent=%s|", p.IntentID)
	for _, r := range requirements {
		fmt.Fprintf(h, "kind=%s|required=%v|min=%d|", r.Kind, r.Required, r.MinSources)
	}
	p.Hash = hex.EncodeToString(h.Sum(nil))
	return p
}

// ErrPlanUnsatisfied is returned when a CaseInput's submissions do not
// meet a required EvidenceRequirement's MinSources for its Kind
// (matched against CaseInput.Predicate) — the audit's "evidence
// planning must determine what evidence is missing" requirement made
// mechanically enforced rather than advisory.
var ErrPlanUnsatisfied = errors.New("lifecycle: evidence plan requirement not satisfied")

// checkPlan verifies a CaseInput satisfies every Required
// EvidenceRequirement in a plan. Optional requirements are recorded
// as unmet in the returned list but do not block the run — the audit
// explicitly requires "preserve unknown/missing evidence as
// uncertainty" rather than silently failing on it.
func checkPlan(plan EvidencePlan, in canonical.CaseInput) (unmet []EvidenceRequirement, err error) {
	for _, req := range plan.Requirements {
		count := 0
		if req.Kind == in.Predicate {
			count = len(in.Submissions)
		}
		if count < req.MinSources {
			unmet = append(unmet, req)
			if req.Required {
				return unmet, fmt.Errorf("%w: kind=%s need>=%d got=%d", ErrPlanUnsatisfied, req.Kind, req.MinSources, count)
			}
		}
	}
	return unmet, nil
}

// LifecycleCertificate carries the full ID chain the audit requires
// end to end: IntentID -> EntityID -> EvidencePlanHash ->
// CanonicalCertificate (which itself covers
// Evidence/Provenance/Truth/Causal/Risk/Decision/Twin) -> IVF
// AuditCertificate -> ReplayID -> (optional, post-outcome)
// CalibrationRecordHash. Hash is a single commitment over every field
// below it, so tampering with any layer's ID is detectable at the
// top.
type LifecycleCertificate struct {
	IntentID           string
	EntityID           string
	EvidencePlanHash   string
	UnmetRequirements  []EvidenceRequirement
	Canonical          canonical.CanonicalCertificate
	IVFVerified        bool
	IVFCertificateHash string
	ReplayID           string // = Canonical.Hash, named explicitly per the audit's contract
	// ExecutionRootHash binds this certificate to the exact
	// pkg/execution DAG run that produced it (P0-1: pkg/execution is
	// now the real production execution path RunUnified runs through,
	// not a parallel engine only tests could reach) -- tampering with
	// any DAG node after the fact changes ExecutionRootHash and is
	// therefore detectable at this certificate's own layer, the same
	// way ReplayID already ties this certificate to the canonical
	// certificate one layer down.
	ExecutionRootHash string
	Hash              string
}

// Result is everything one RunUnified call produces.
type Result struct {
	Intent    Intent
	Plan      EvidencePlan
	EntityID  entity.CanonicalID
	SourceIDs []string // captured from the case's own submissions, for RecordOutcome
	Canonical *canonical.CanonicalResult
	// Execution is the full pkg/execution DAG result RunUnified's
	// canonical chain now runs through end to end (P0-1). It carries
	// the ExecutionTrace, ExecutionRootHash, ReplayPackage and
	// VerificationCertificate that were previously reachable only from
	// pkg/execution's own tests and cmd/veriqo-cold-replay, never from
	// the real production entrypoint.
	Execution   *execution.Result
	IVFResult   verification.VerificationResult
	Certificate LifecycleCertificate
	// Correlation is the audit's P1-07 ("Unified Observability")
	// deliverable: the ONE joined set of real identifiers (intent,
	// execution, evidence, entity, decision, verification, replay) this
	// specific RunUnified call produced, in the audit's own named
	// vocabulary. pkg/platform/correlation.Key already existed but had
	// zero production callers anywhere in this repository before this
	// field -- a real "documented but not a universal runtime
	// primitive" gap, closed here by giving it its first one. Unlike
	// correlation.FromExecutionResult's own bare-execution.Result case
	// (which cannot know an IntentID -- pkg/execution never imports
	// pkg/kernel/intent), RunUnified genuinely holds the originating
	// Intent, so IntentID below is real, not the permanently-empty
	// placeholder FromExecutionResult's own doc comment describes.
	Correlation correlation.Key
	// CaseID is PHASE D2 (P0-5)'s single identifier every artifact of
	// this investigation hangs from. It is deliberately the Intent's
	// own content-addressed ID rather than a new, separately-generated
	// value: one Intent IS one case, and minting a second identifier
	// for the same thing would create exactly the kind of parallel
	// identity this program exists to close.
	CaseID lineage.CaseID
}

func hashLifecycleCert(c LifecycleCertificate) string {
	h := sha256.New()
	fmt.Fprintf(h, "intent=%s|entity=%s|plan=%s|unmet=%d|canonical=%s|ivf=%v|ivfcert=%s|replay=%s|execroot=%s|",
		c.IntentID, c.EntityID, c.EvidencePlanHash, len(c.UnmetRequirements),
		c.Canonical.Hash, c.IVFVerified, c.IVFCertificateHash, c.ReplayID, c.ExecutionRootHash)
	return hex.EncodeToString(h.Sum(nil))
}

// ErrLifecycleCertificateTampered is returned by VerifyCertificate
// when a LifecycleCertificate's Hash does not match what its own
// fields recompute to.
var ErrLifecycleCertificateTampered = errors.New("lifecycle: certificate hash does not match its own fields")

// VerifyCertificate independently recomputes a LifecycleCertificate's
// hash from its own fields and compares — the lifecycle-layer
// counterpart to canonical.VerifyCertificate, one level up the chain.
func VerifyCertificate(c LifecycleCertificate) error {
	if hashLifecycleCert(c) != c.Hash {
		return ErrLifecycleCertificateTampered
	}
	return nil
}

// Orchestrator composes the engines RunUnified needs. Entities and
// the Verifier are stateful across calls (so entity resolution and
// IVF domain registration accumulate correctly); Pipeline is
// pkg/canonical's own (already stateful).
type Orchestrator struct {
	mu       sync.Mutex
	Pipeline *canonical.Pipeline
	// Identity is the CANONICAL entity-resolution authority (audit item
	// P0-B / R4): RunUnified resolves every EntityAlias set through
	// Identity.Merge/EntityIDAt first. Entities (below) is consulted
	// only as a fail-safe fallback for alias Kinds identity.Kind does
	// not model (see resolveCanonicalEntity) -- for the alias vocabulary
	// this OS actually uses in production (IMO, CALLSIGN, MMSI, NAME,
	// OWNER, ...; the full set is identity.Kind's own const block),
	// Entities is never written to by this path at all, so it can no
	// longer silently diverge from Identity's opinion for those cases.
	Identity *identity.Resolver
	// Entities is the pre-P0-B union-find, kept as the fallback
	// authority ONLY (see Identity's doc comment above) and as the
	// live comparison target for pkg/governance/entityconsistency.
	Entities *entity.Registry
	Verifier *verification.Verifier
	// Execution is the real production execution engine (P0-1 / audit's
	// "Biggest new finding": pkg/execution.Engine existed as a complete,
	// tested 16-stage DAG executor but was never constructed by any real
	// production entrypoint -- only by tests and cmd/veriqo-cold-replay's
	// replay-only path). RunUnified now runs the canonical MOAT chain
	// THROUGH this engine rather than calling
	// canonical.Pipeline.RunCanonical directly, so the DAG trace,
	// ExecutionRootHash, ReplayPackage and VerificationCertificate this
	// engine produces are genuinely load-bearing artifacts of every real
	// lifecycle run, not a capability that only unit tests exercise.
	Execution   *execution.Engine
	Calibration *calibration.Engine
	// TemporalCalibration, when set, is the real Evidence -> Observation
	// bridge (audit item P0-C, pkg/governance/calibration) RunUnified
	// consults to drive pkg/execution's TEMPORAL_BAYESIAN stage for
	// real, instead of leaving it unconditionally SKIPPED. Nil by
	// default and left unset by every production Orchestrator this
	// repository constructs today (see NewOrchestrator) -- an operator
	// opts in by assigning a *bayescalibration.Registry populated with
	// real, provenanced LikelihoodTable/temporal-Model registrations for
	// the predicates it wants to actually reason over. Do not confuse
	// with Calibration above (pkg/moat/calibration.Engine): that is the
	// UNRELATED Class-A/B/C ground-truth trust-calibration feedback
	// loop; this is the evidence-to-hidden-state-likelihood bridge for
	// hbayes's temporal model. Two packages both named "calibration"
	// solving two different problems -- imported here as
	// bayescalibration to keep the two unmistakably separate at every
	// call site.
	TemporalCalibration *bayescalibration.Registry
	// Lineage, when set, is the case-lineage ledger (PHASE D2 / P0-5)
	// RunUnified registers this call's whole case into: Intent, Entity,
	// Evidence, Policy, Decision, Verification, Replay and the identity
	// ledger head, all under ONE CaseID, in dependency order. Nil by
	// default, exactly like TemporalCalibration above: a deployment
	// opts in by assigning a *lineage.Ledger, and every existing caller
	// and test keeps working unmodified.
	//
	// RunUnified deliberately does NOT register an OUTCOME node --
	// ground truth is not known at case-run time, which is precisely
	// why RecordOutcome is a separate call. A case therefore reports
	// Complete=false until RecordOutcome runs, which is the honest
	// answer, not a gap.
	Lineage *lineage.Ledger
}

// NewOrchestrator builds a lifecycle Orchestrator over an existing
// canonical.Pipeline (typically veriqo/kernel.Kernel.Canonical, so the
// TrustCalculus stays shared OS-wide — see pkg/canonical's own
// package comment on why that sharing matters) and an existing
// *identity.Resolver.
//
// sharedIdentity closes a real production entity-resolution
// fragmentation an external audit named (P0-B / R4, "One Canonical
// Entity Authority"): before this parameter existed, NewOrchestrator
// always built its own private identity.Resolver, and
// veriqo/kernel.New's OWN pkg/evidence/api.Facade (unifiedEvidence)
// separately built a second, disconnected one -- two live production
// identity authorities inside the SAME composition root, silently
// unable to see each other's alias resolutions, exactly the
// fragmentation risk pkg/governance/entityconsistency's package
// comment already documents in the abstract. veriqo/kernel.New now
// constructs exactly ONE *identity.Resolver and hands it to both. Pass
// nil to have this Orchestrator own a private resolver instead (every
// existing standalone caller/test keeps working unmodified, mirroring
// the same nil-safe-default pattern this codebase already uses for
// trustcalc.Calculus and *api.Facade sharing).
func NewOrchestrator(pipeline *canonical.Pipeline, sharedIdentity *identity.Resolver) *Orchestrator {
	if pipeline == nil {
		pipeline = canonical.NewPipeline(nil)
	}
	id := sharedIdentity
	if id == nil {
		id = identity.NewResolver()
	}
	// A single, maximally-weighted authority for RunUnified's own
	// merges. Weight=1 and an empty AuthoritativeFor (full weight for
	// every kind) matches exactly what the pre-existing
	// pkg/moat/entity.Registry union-find already assumed implicitly:
	// every alias RunUnified is handed is trusted equally, since it
	// arrives already vetted by the caller's own Intent construction.
	_ = id.RegisterAuthority(identity.Authority{SourceID: identityAuthoritySourceID, Weight: 1}) // RegisterAuthority only errors on an empty SourceID or a Weight outside (0,1]; both are fixed, hardcoded-safe literals here, so this can never fail
	// The execution engine shares this SAME pipeline pointer, not a
	// second one -- RunUnified must call RunCanonical exactly once per
	// case (through the engine, see RunUnified below), so a second,
	// independently-stateful pipeline here would silently double-run
	// arbitration against the fusion ledger and diverge from it.
	exec := execution.NewEngine(pipeline)
	exec.Identity = id
	o := &Orchestrator{
		Pipeline: pipeline, Identity: id, Entities: entity.NewRegistry(),
		Verifier: verification.NewVerifier(), Execution: exec,
		Calibration: calibration.NewEngineWithCalculus(pipeline.Trust),
	}
	o.Verifier.RegisterReplayFunc("lifecycle.fusion_arbitration", replayFusionArbitration)
	return o
}

// resolveCanonicalEntity resolves in.EntityAliases through Identity,
// the canonical authority, returning ok=false ONLY when an alias Kind
// is outside identity.Kind's fixed, audited vocabulary -- in which
// case RunUnified falls back to the legacy union-find rather than
// force a fabricated Kind mapping (see pkg/governance/entityconsistency's
// package comment on why inventing a translation layer would itself be
// the fabrication this package refuses to do).
// The third return value is the primary Identifier resolution actually
// keyed off (aliases[0]), meaningful only when ok is true -- callers
// use it to let pkg/execution's IDENTITY_RESOLUTION stage (P0-4)
// independently re-derive the SAME entity ID from the SAME ledger,
// rather than only trusting this function's own answer.
func (o *Orchestrator) resolveCanonicalEntity(actorID string, aliases []entity.Alias, tick uint64) (entity.CanonicalID, identity.Identifier, bool) {
	ids := make([]identity.Identifier, len(aliases))
	for i, a := range aliases {
		ids[i] = identity.Identifier{Kind: identity.Kind(a.Kind), Value: a.Value}
		if err := ids[i].Validate(); err != nil {
			return "", identity.Identifier{}, false
		}
	}
	first := ids[0]
	for _, other := range ids[1:] {
		if _, err := o.Identity.Merge(actorID, identityAuthoritySourceID, first, other, tick,
			"lifecycle.RunUnified: aliases co-occur on one Intent"); err != nil {
			return "", identity.Identifier{}, false
		}
	}
	resolved, err := o.Identity.EntityIDAt(first, tick)
	if err != nil {
		return "", identity.Identifier{}, false
	}
	return entity.CanonicalID(resolved), first, true
}

// RunUnified is the audit's required central chain: Intent -> Plan ->
// Entity Resolution -> (delegates Evidence/Provenance/Truth/Causal/
// Risk/Decision/Twin/EconomicImpact to canonical.RunCanonical) -> IVF
// -> LifecycleCertificate. Outcome/Calibration is a separate call
// (RecordOutcome below) since ground truth, by definition, is not yet
// known at case-run time.
// ctx is the real caller-supplied context (P0-6): the Intent
// entrypoint's own request/operation context, threaded through to
// pkg/execution.Engine.Run rather than fabricated internally via
// context.Background(). A real production HTTP/gateway caller DOES
// invoke RunUnified now: veriqo/gateway/rest/lifecycle_route.go's
// POST /lifecycle/run_unified passes r.Context() straight through, so
// a client disconnect or server shutdown genuinely cancels an
// in-flight execution (this paragraph previously said no such caller
// existed; that became stale the round that route shipped and is
// corrected here rather than left to silently mislead a future
// reader).
func (o *Orchestrator) RunUnified(ctx context.Context, in Intent, plan EvidencePlan, caseIn canonical.CaseInput) (*Result, error) {
	// The audit's P1-07 ("Unified Observability") named RunUnified
	// itself as the one join-point every downstream span, log line and
	// audit record should be traceable back to, using the SAME IDs --
	// but before this span existed, RunUnified carried NO telemetry of
	// its own at all (confirmed by grep: zero telemetry.StartSpan calls
	// in this file), even though P0-9 gave 12 of the engines it calls
	// real spans. ctx is reassigned to the span's own context so
	// o.Execution.Run below (which already threads ctx down through
	// pkg/execution's own stages per P0-6) is genuinely parented under
	// this span, not a disconnected root. Attributes it can't know yet
	// (execution_id, decision_id, entity_id) are attached via
	// SetAttribute once each becomes available below, rather than
	// waiting until the end and losing them on an early error return.
	ctx, span := telemetry.StartSpan(ctx, "lifecycle.RunUnified",
		telemetry.Attribute{Key: "intent_id", Value: in.ID()},
		telemetry.Attribute{Key: "tenant_id", Value: in.Tenant},
		telemetry.Attribute{Key: "actor_id", Value: in.ActorID})
	defer span.End()

	// Entities and Verifier accumulate state across calls (see the
	// Orchestrator doc comment); mu serializes RunUnified and
	// RecordOutcome so two concurrent callers cannot race on that
	// accumulation. staticcheck's U1000 flagged mu as unused, which was
	// a real gap: the field existed but nothing ever locked it.
	o.mu.Lock()
	defer o.mu.Unlock()

	if plan.IntentID != in.ID() {
		err := fmt.Errorf("lifecycle: evidence plan does not belong to this intent (plan.IntentID=%s intent.ID()=%s)", plan.IntentID, in.ID())
		span.RecordError(err)
		return nil, err
	}

	// --- Entity Resolution ---------------------------------------------
	// Identity (pkg/identity) is the canonical authority (P0-B / R4):
	// resolveCanonicalEntity is tried first, and Entities (the legacy
	// union-find) is used ONLY as a fallback for alias Kinds identity
	// does not model. See resolveCanonicalEntity's doc comment.
	var canonEntity entity.CanonicalID
	// identityKey, when identityKeySet, is the primary Identifier this
	// resolution was keyed off THROUGH pkg/identity (not the union-find
	// fallback) -- threaded into execution.Input.IdentityAliases below
	// so pkg/execution's IDENTITY_RESOLUTION stage (P0-4) can
	// independently re-derive the same canonical entity from the same
	// ledger, instead of only trusting caseIn.Entity as given.
	var identityKey identity.Identifier
	var identityKeySet bool
	if len(in.EntityAliases) > 0 {
		if resolved, key, ok := o.resolveCanonicalEntity(in.ActorID, in.EntityAliases, in.Tick); ok {
			canonEntity, identityKey, identityKeySet = resolved, key, true
		} else {
			first := in.EntityAliases[0]
			var err error
			canonEntity, err = o.Entities.Register(in.ActorID, first, in.Tick)
			if err != nil {
				err = fmt.Errorf("lifecycle: entity resolution: %w", err)
				span.RecordError(err)
				return nil, err
			}
			for _, alias := range in.EntityAliases[1:] {
				canonEntity, err = o.Entities.Merge(in.ActorID, first, alias, in.Tick)
				if err != nil {
					err = fmt.Errorf("lifecycle: entity resolution: %w", err)
					span.RecordError(err)
					return nil, err
				}
			}
		}
		caseIn.Entity = digitaltwin.EntityID(canonEntity)
		caseIn.Subject = string(canonEntity)
	}
	span.SetAttribute(telemetry.Attribute{Key: "entity_id", Value: string(canonEntity)})

	// --- Evidence Plan enforcement --------------------------------------
	unmet, err := checkPlan(plan, caseIn)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	// --- Canonical MOAT chain (Evidence -> ... -> Certificate), run
	// THROUGH pkg/execution's real DAG engine (P0-1) rather than calling
	// canonical.Pipeline.RunCanonical directly -- see Orchestrator's
	// Execution field doc comment for why this is the fix, not a
	// cosmetic wrapper: it is the same Pipeline pointer, so RunCanonical
	// still runs exactly once per case, but the run is now genuinely
	// attributed to and traced by every DAG stage (identity binding,
	// trust state, explanation, replay package, verification
	// certificate) instead of only the canonical certificate. -----------
	execCtx := execution.Context{
		ExecutionID: executionID(in, plan, caseIn), CaseID: in.ID(),
		Tenant: in.Tenant, Actor: in.ActorID, PolicyVersion: caseIn.Policy.Name,
		EvidencePackageID: plan.Hash, IdentityResolutionVersion: "identity/" + version.Current,
		LedgerPosition: uint64(len(o.Identity.Ledger())), Tick: in.Tick,
		ReplayMetadata: "lifecycle.RunUnified",
	}
	execIn := execution.Input{Context: execCtx, Case: caseIn}
	if identityKeySet {
		execIn.IdentityAliases = []identity.Identifier{identityKey}
	}
	// P0-D Test 3 production wiring: INTENT independently re-verifies
	// the SAME plan.Requirements checkPlan just enforced above, so the
	// DAG's own committed trace carries a genuine, independently-
	// checked record of evidence-plan satisfaction (and a cold replay
	// of this execution re-confirms it too) -- not only an invisible
	// pre-flight gate that leaves no trace inside the DAG itself. This
	// never changes RunUnified's own pass/fail outcome (checkPlan
	// already returned above on any unmet Required item, so execution.
	// Engine.Run is never reached with one), but it is not redundant:
	// it is the difference between "trust one caller-side check" and
	// "the DAG itself can prove this, including under independent cold
	// replay with no access to pkg/lifecycle at all".
	if len(plan.Requirements) > 0 {
		reqs := make([]execution.EvidenceRequirement, len(plan.Requirements))
		for i, r := range plan.Requirements {
			reqs[i] = execution.EvidenceRequirement{Kind: r.Kind, Required: r.Required, MinSources: r.MinSources}
		}
		execIn.EvidenceRequirements = reqs
	}

	// --- Temporal Bayesian bridge (P0-C WIRING half) --------------------
	// Real, but strictly additive: TemporalCalibration is nil by default
	// (see its own doc comment), so this block is a no-op for every
	// production Orchestrator this repository constructs today and
	// TEMPORAL_BAYESIAN keeps recording SKIPPED exactly as before. When
	// an operator DOES register a real LikelihoodTable + temporal Model
	// for caseIn.Predicate, every submission is mapped through
	// BuildObservation -- fail-closed per-submission, not per-request:
	// a submission whose specific Value has no calibrated likelihood
	// entry simply contributes no observation rather than aborting the
	// whole case, since a calibration gap on one source must never turn
	// into a production outage. Only a submission that fails for a
	// reason OTHER than "no calibration for this predicate/value" (i.e.
	// tick precedes the table's effective tick) is treated the same
	// way -- honestly excluded, not fabricated around.
	if o.TemporalCalibration != nil && o.TemporalCalibration.Calibrated(caseIn.Predicate) &&
		o.TemporalCalibration.TemporalModelRegistered(caseIn.Predicate) {
		var obs []hbayes.Observation
		for _, sub := range caseIn.Submissions {
			built, buildErr := o.TemporalCalibration.BuildObservation(string(sub.SourceID), caseIn.Predicate, sub.Value, in.Tick, 0)
			if buildErr != nil {
				continue // honestly excluded: no calibrated likelihood for this value
			}
			obs = append(obs, built)
		}
		if len(obs) > 0 {
			if model, err := o.TemporalCalibration.BuildTemporalModel(caseIn.Predicate); err == nil {
				execIn.TemporalModel = model
				execIn.TemporalObservations = []hbayes.TickObservations{{Tick: in.Tick, Observations: obs}}
			}
		}
	}

	span.SetAttribute(telemetry.Attribute{Key: "execution_id", Value: execCtx.ExecutionID})
	span.SetAttribute(telemetry.Attribute{Key: "evidence_package_id", Value: execCtx.EvidencePackageID})
	execRes, err := o.Execution.Run(ctx, execIn)
	if err != nil {
		err = fmt.Errorf("lifecycle: execution run: %w", err)
		span.RecordError(err)
		return nil, err
	}
	canonRes := execRes.Canonical
	span.SetAttribute(telemetry.Attribute{Key: "decision_id", Value: execRes.Explanation.DecisionID})

	// --- IVF: build a REAL bundle from the fusion records that backed
	// this exact arbitration, and independently verify it. -------------
	ivfBundle, err := buildIVFBundle(o.Pipeline, caseIn, canonRes.Arbitration)
	if err != nil {
		err = fmt.Errorf("lifecycle: building IVF bundle: %w", err)
		span.RecordError(err)
		return nil, err
	}
	ivfResult, err := o.Verifier.Verify("lifecycle.fusion_arbitration", ivfBundle)
	if err != nil {
		err = fmt.Errorf("lifecycle: IVF verify: %w", err)
		span.RecordError(err)
		return nil, err
	}

	cert := LifecycleCertificate{
		IntentID: in.ID(), EntityID: string(canonEntity), EvidencePlanHash: plan.Hash,
		UnmetRequirements: unmet, Canonical: canonRes.Certificate,
		IVFVerified:       ivfResult.ManifestValid && ivfResult.ReplayValid,
		ReplayID:          canonRes.Certificate.Hash,
		ExecutionRootHash: execRes.ExecutionRootHash,
	}
	if ivfResult.Certificate != nil {
		cert.IVFCertificateHash = ivfResult.Certificate.CertificateHash
	}
	cert.Hash = hashLifecycleCert(cert)

	// P0-F (universal correlation propagation): corr is built from the
	// SAME values already individually attached to the span above as
	// each became available (see this function's own doc comment on
	// why they are set progressively rather than only here -- an early
	// error return must not leave a trace blind to the identifiers it
	// already had). WithIdentityLedgerHead attaches the real
	// *identity.Resolver.Head() this Orchestrator resolved entities
	// against for this call -- the SAME value IDENTITY_RESOLUTION's own
	// node hash already committed to (see pkg/execution's
	// StageIdentityResolution) -- so this is the identical real
	// commitment, not a second, disconnected one. Every field of corr
	// is now attached to the span, not only the two it used to be
	// attached for, closing the "Key exists but is not really used by
	// logs/telemetry/audit records" gap: corr (not ad-hoc individual
	// values) is now the one object every consumer of this call --
	// telemetry, the returned Result, and any caller logging or
	// auditing this execution -- reads the same seven identifiers from.
	corr := correlation.FromExecutionResult(*execRes).WithIdentityLedgerHead(o.Identity.Head())
	corr.IntentID = in.ID()
	span.SetAttribute(telemetry.Attribute{Key: "intent_id", Value: corr.IntentID})
	span.SetAttribute(telemetry.Attribute{Key: "execution_id", Value: corr.ExecutionID})
	span.SetAttribute(telemetry.Attribute{Key: "evidence_package_id", Value: corr.EvidencePackageID})
	span.SetAttribute(telemetry.Attribute{Key: "entity_id", Value: corr.EntityID})
	span.SetAttribute(telemetry.Attribute{Key: "decision_id", Value: corr.DecisionID})
	span.SetAttribute(telemetry.Attribute{Key: "verification_certificate_id", Value: corr.VerificationCertificateID})
	span.SetAttribute(telemetry.Attribute{Key: "replay_package_id", Value: corr.ReplayPackageID})
	span.SetAttribute(telemetry.Attribute{Key: "entity_identity_ledger_head", Value: corr.EntityIdentityLedgerHead})

	res := &Result{
		Intent: in, Plan: plan, EntityID: canonEntity, SourceIDs: canonical.SortedSourceIDs(caseIn.Submissions),
		Canonical: canonRes, Execution: execRes, IVFResult: ivfResult, Certificate: cert,
		Correlation: corr, CaseID: lineage.CaseID(in.ID()),
	}
	if o.Lineage != nil {
		if err := o.recordLineage(res, caseIn); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// recordLineage registers this case's whole lineage under ONE CaseID
// (PHASE D2 / P0-5). Every Ref it attaches is an identifier some
// subsystem genuinely produced for THIS call -- the Intent's own
// content-addressed ID, pkg/identity's resolved entity, the execution's
// evidence-package ID, the policy's own hash, the decision, replay and
// verification identities -- so the lineage is a join over real values,
// never a set of labels this function made up.
//
// It reuses lineage.Ledger.FromCorrelation for the six identifiers
// correlation.Key already carries rather than re-listing them, then
// adds the two a correlation key structurally cannot supply: the
// per-source evidence submissions of this specific case, and the policy
// that governed it.
func (o *Orchestrator) recordLineage(res *Result, caseIn canonical.CaseInput) error {
	caseID := res.CaseID
	if _, err := o.Lineage.FromCorrelation(caseID, res.Correlation, caseIn.Tick); err != nil {
		return fmt.Errorf("lifecycle: case lineage: %w", err)
	}
	if h := caseIn.Policy.Hash(); h != "" {
		if _, err := o.Lineage.Attach(caseID, lineage.Node{
			Kind: lineage.KindPolicy, Ref: h,
			Subsystem: "pkg/moat/decision.Policy.Hash", Tick: caseIn.Tick,
		}); err != nil {
			return fmt.Errorf("lifecycle: case lineage policy: %w", err)
		}
	}
	// One EVIDENCE node per real source submission, each depending on
	// the evidence package the execution actually committed to.
	var upstream []string
	if res.Correlation.EvidencePackageID != "" {
		upstream = []string{res.Correlation.EvidencePackageID}
	}
	for _, id := range canonical.SortedSourceIDs(caseIn.Submissions) {
		if _, err := o.Lineage.Attach(caseID, lineage.Node{
			Kind: lineage.KindEvidence, Ref: caseIn.Subject + "|" + caseIn.Predicate + "|" + id,
			Subsystem: "pkg/canonical.SourceSubmission", Tick: caseIn.Tick, Upstream: upstream,
		}); err != nil {
			return fmt.Errorf("lifecycle: case lineage evidence: %w", err)
		}
	}
	return nil
}

// executionID is pkg/execution.Context's mandatory ExecutionID, derived
// deterministically (no time.Now, no UUID -- this repository's
// established discipline, see Intent.ID/EvidencePlan.Hash) from exactly
// the inputs that make one RunUnified call distinct from another: which
// Intent, under which EvidencePlan, over which case content. Two calls
// with byte-identical in/plan/caseIn therefore get the same
// ExecutionID, which is correct: pkg/execution's own determinism tests
// already rely on being able to reproduce a run's identity from its
// inputs alone.
func executionID(in Intent, plan EvidencePlan, caseIn canonical.CaseInput) string {
	h := sha256.New()
	fmt.Fprintf(h, "intent=%s|plan=%s|entity=%s|subject=%s|predicate=%s|tick=%d|",
		in.ID(), plan.Hash, caseIn.Entity, caseIn.Subject, caseIn.Predicate, caseIn.Tick)
	ids := canonical.SortedSourceIDs(caseIn.Submissions)
	bySource := make(map[string]canonical.SourceSubmission, len(caseIn.Submissions))
	for _, s := range caseIn.Submissions {
		bySource[string(s.SourceID)] = s
	}
	for _, id := range ids {
		s := bySource[id]
		fmt.Fprintf(h, "sub=%s|val=%s|rel=%s|", id, s.Value, strconv.FormatFloat(s.BaseReliability, 'g', 17, 64))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// canonicalEntityAsTwinID intentionally removed — digitaltwin.EntityID
// is used directly above (entity.CanonicalID and digitaltwin.EntityID
// are both plain string types; no adapter needed).

// buildIVFBundle packages the fusion arbitration records that
// produced canonRes into a real pkg/verification EvidencePackage/
// ReplayPackage/VerificationBundle — reusing the existing IVF
// machinery exactly as the audit instructs ("do not rebuild it").
func buildIVFBundle(p *canonical.Pipeline, caseIn canonical.CaseInput, arb fusion.ArbitrationResult) (verification.VerificationBundle, error) {
	claim := fusion.Claim{Subject: caseIn.Subject, Predicate: caseIn.Predicate}
	var records []verification.EvidenceRecord
	for i, ev := range p.Fusion.EvidenceFor(claim) {
		payload, err := json.Marshal(ev)
		if err != nil {
			return verification.VerificationBundle{}, err
		}
		records = append(records, verification.EvidenceRecord{Kind: "fusion.Evidence", Index: uint64(i), Payload: payload})
	}
	rp := verification.ReplayPackage{
		Evidence:      verification.EvidencePackage{Subject: claim.Key(), Records: records},
		ClaimedResult: arb.Winner,
	}
	return verification.NewBundle(rp)
}

// replayFusionArbitration is the REGISTERED, independent replay
// function: given only the raw evidence records (no access to the
// original fusion.Engine instance), it recomputes the plurality
// winner from scratch and returns it — a genuine second computation,
// not a trivial echo of the claimed result. Mirrors fusion.Engine's
// own plurality-by-weight logic at the record level, deliberately
// re-derived here rather than imported, since IVF's entire value is
// that the replay does NOT depend on the original engine's live
// state (see pkg/verification's package comment).
func replayFusionArbitration(pkg verification.EvidencePackage) (string, error) {
	type rawEvidence struct {
		Source string  `json:"Source"`
		Value  string  `json:"Value"`
		Weight float64 `json:"Weight"`
	}
	tally := map[string]float64{}
	for _, rec := range pkg.Records {
		var ev rawEvidence
		if err := json.Unmarshal(rec.Payload, &ev); err != nil {
			return "", err
		}
		tally[ev.Value] += ev.Weight
	}
	best, bestWeight := "", -1.0
	keys := make([]string, 0, len(tally))
	for k := range tally {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic tie-break
	for _, k := range keys {
		if tally[k] > bestWeight {
			best, bestWeight = k, tally[k]
		}
	}
	return best, nil
}

// RecordOutcome closes the audit's "Outcome -> Calibration / Trust
// Reassessment" requirement: ground truth arrives (typically much
// later than the case ran), is scored against the prediction this
// lifecycle case made, and the result feeds back into the shared
// Calculus via calibration.Engine — completing the loop back to Trust
// this package's doc comment promises. groundTruthSourceID must be an
// independent source (not one of the case's own arbitration inputs);
// RecordOutcome registers it as independent automatically if it isn't
// already, since a ground-truth verifier is by definition external to
// the case's own evidence.
func (o *Orchestrator) RecordOutcome(res *Result, actualValue, groundTruthSourceID string, tick uint64) (calibration.Record, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	claim := fusion.Claim{Subject: res.Canonical.Certificate.Subject, Predicate: res.Canonical.Certificate.Predicate}
	pred := calibration.Prediction{
		ClaimKey: claim.Key(), Value: res.Canonical.Arbitration.Winner,
		Confidence:           res.Canonical.Arbitration.WinnerConfidence,
		ArbitrationSourceIDs: res.SourceIDs,
		Tick:                 tick,
	}
	if err := o.Calibration.RecordPrediction(pred); err != nil {
		return calibration.Record{}, fmt.Errorf("lifecycle: record prediction: %w", err)
	}
	if !o.Calibration.IsIndependentSource(groundTruthSourceID) {
		o.Calibration.RegisterIndependentSource(groundTruthSourceID)
	}
	rec, err := o.Calibration.RecordGroundTruth(calibration.GroundTruth{
		ClaimKey: claim.Key(), Value: actualValue, SourceID: groundTruthSourceID, Tick: tick,
	})
	if err != nil {
		return rec, err
	}
	// PHASE D2 (P0-5): the OUTCOME node is attached HERE, not in
	// RunUnified, because this is the moment ground truth actually
	// exists. Until it does, the case honestly reports Complete=false.
	if o.Lineage != nil && res.CaseID != "" {
		var upstream []string
		for _, ref := range []string{res.Correlation.DecisionID, res.Correlation.VerificationCertificateID} {
			if ref != "" {
				upstream = append(upstream, ref)
			}
		}
		if _, lerr := o.Lineage.Attach(res.CaseID, lineage.Node{
			Kind:      lineage.KindOutcome,
			Ref:       claim.Key() + "|" + actualValue + "|" + groundTruthSourceID,
			Subsystem: "pkg/moat/calibration.Engine.RecordGroundTruth", Tick: tick,
			Upstream: upstream,
		}); lerr != nil {
			return rec, fmt.Errorf("lifecycle: case lineage outcome: %w", lerr)
		}
	}
	return rec, nil
}
