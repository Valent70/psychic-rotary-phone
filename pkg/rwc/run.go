package rwc

import (
	"context"
	"fmt"

	"veriqo/pkg/canonical"
	"veriqo/pkg/lifecycle"
	"veriqo/pkg/lineage"
	"veriqo/pkg/moat/decision"
	"veriqo/pkg/moat/entity"
	"veriqo/pkg/platform/correlation"
	"veriqo/veriqo/kernel"
)

// CaseRequest is everything one native RunUnified call needs, expressed
// in RWC terms. It is deliberately a thin wrapper: every field maps 1:1
// onto an existing lifecycle.Intent / canonical.CaseInput field — see
// Run below. Tick is caller-injected, never derived from time.Now(),
// matching this repository's standing no-wall-clock discipline.
type CaseRequest struct {
	CaseID        string
	Objective     string
	Tenant        string
	EntityAliases []entity.Alias
	Predicate     string
	Submissions   []canonical.SourceSubmission
	Policy        decision.Policy
	PatternScore  float64
	PriceAnomaly  float64
	Tick          uint64

	// VerdictPolicy is the mapping from the native decision.Action onto
	// the corpus verdict vocabulary. Zero value means
	// DefaultVerdictPolicy. It is a REQUEST field rather than a package
	// constant so a test can prove that changing the mapping changes the
	// verdict and the decision root — the mandate's third proof
	// obligation (section II).
	VerdictPolicy VerdictPolicy
	// Constraint is the constraint evaluation this case's
	// PatternScore/PriceAnomaly were derived from. It is carried purely
	// so Run can compute ConstraintCrossCheck; nothing in the verdict
	// path reads it. A zero value simply produces no cross-check.
	Constraint ConstraintResult
}

// CaseResult is one case's outcome, carrying every hash/ID the RWC
// evidence bundle records plus the full native lifecycle.Result for
// anyone who wants to inspect a deeper layer.
type CaseResult struct {
	CaseID string

	InputHash       string // sha256 over the CaseRequest itself, as originally supplied
	ExecutionID     string // execution.Context.ExecutionID, from the real DAG run
	CanonicalHash   string // canonical.CanonicalCertificate.Hash
	CertificateHash string // lifecycle.LifecycleCertificate.Hash
	// The pkg/execution DAG's own root hash is deliberately NOT copied
	// onto a field here. internal/entrypoints guards assignment of that
	// field name outside the canonical path, because "only the execution
	// DAG may compute an ExecutionRootHash". A consumer that needs it
	// reads it from where the DAG actually put it:
	// Lifecycle.Execution.ExecutionRootHash, or equivalently
	// Lifecycle.Certificate.ExecutionRootHash.
	//
	// LedgerAnchor is the in-process, in-memory hash-chain head this
	// case's arbitration was appended to (pkg/moat/fusion.Engine.Head())
	// at the moment this case ran — independently re-derivable via
	// Fusion.VerifyChain(). It is NOT durable storage, NOT a write-ahead
	// log, and NOT an external anchor: nothing in this repository writes
	// this chain to disk or to a third party. Do not describe it as
	// "ledger anchoring" without that qualification.
	LedgerAnchor string

	DecisionAction string // decision.Action, verbatim from the native engine (MONITOR/FLAG/ESCALATE)
	RiskScore      float64

	// Verdict is the corpus-vocabulary outcome, and since round R24 it is
	// derived from DecisionAction and nothing else (see verdict.go). Run
	// fails rather than returning a CaseResult with an empty Verdict: a
	// case that reached no native decision has no verdict, which is the
	// mandate's "if the native decision engine is removed: no final
	// verdict".
	Verdict Verdict
	// VerdictPolicyHash is the mapping that produced Verdict, and
	// DecisionRoot commits to the execution root, the action, that
	// mapping and the verdict together — so "changing the policy mapping
	// changes the decision root" is a checkable property of this struct.
	VerdictPolicyHash string
	DecisionRoot      string
	// ConstraintWarning is non-empty when the native action disagrees
	// with what the constraint findings implied. It is a diagnostic, not
	// an input: no field above is derived from it. Every caller treats a
	// non-empty warning as a failure.
	ConstraintWarning string
	// HumanReviewRequired is the release condition the native trust
	// evaluation produced. It is carried here because a Verdict of PASS
	// that may not be released without review is not the same artifact as
	// a PASS that may.
	HumanReviewRequired bool

	// Correlation is the seven-identifier join key pkg/lifecycle produced
	// for this exact run (pkg/platform/correlation.Key). It is the real
	// value RunUnified computed, copied out unchanged.
	Correlation correlation.Key
	// LineageCaseID is the one CaseID every artifact of this
	// investigation hangs from (pkg/lineage). Populated for every run;
	// whether a lineage LEDGER actually recorded the case depends on
	// whether the caller enabled one — see EnableCaseLineage.
	LineageCaseID lineage.CaseID

	Lifecycle *lifecycle.Result
}

// EnableCaseLineage attaches a real pkg/lineage.Ledger to this Kernel's
// lifecycle Orchestrator, so every subsequent RunUnified call registers
// its whole case (intent, entity, evidence, policy, decision,
// verification, replay, identity ledger head) under ONE CaseID.
//
// pkg/lifecycle.Orchestrator.Lineage is nil by default and every
// existing caller leaves it nil, so this is opt-in exactly as that
// field's own doc comment specifies. It is called by cmd/veriqo-rwc-v2
// and by this package's tests, and it returns the ledger so the caller
// can read Completeness back out.
//
// A case run this way honestly reports Complete=false: lineage's
// requiredKinds includes OUTCOME, and ground truth for a vessel/port
// suitability judgment or a broker claim does not exist at case-run
// time. That is the correct answer, not a gap to be filled in.
func EnableCaseLineage(k *kernel.Kernel) *lineage.Ledger {
	l := lineage.NewLedger()
	k.Lifecycle.Lineage = l
	return l
}

// Run executes one RWC case through VERIQO's real native path: Intent ->
// EvidencePlan -> lifecycle.Orchestrator.RunUnified, which itself runs
// Entity Resolution (pkg/identity) and then the whole canonical MOAT
// chain THROUGH pkg/execution.Engine's 16-stage DAG (which is what
// actually calls pkg/canonical.Pipeline.RunCanonical), then IVF
// verification and the LifecycleCertificate. No engine here is called
// directly by this package — Run only builds inputs and reads outputs.
func Run(ctx context.Context, k *kernel.Kernel, req CaseRequest) (*CaseResult, error) {
	inputHash, err := hashOf("rwc.case_input/v1", req)
	if err != nil {
		return nil, fmt.Errorf("rwc: hashing case input: %w", err)
	}

	intent := lifecycle.Intent{
		ActorID:            "rwc-v2-adapter",
		Objective:          req.Objective,
		Tenant:             req.Tenant,
		EntityAliases:      req.EntityAliases,
		RequiredConfidence: 0, // no minimum-confidence gate: evidence completeness is reported, not gated
		TemporalScope:      "rwc-v2",
		Tick:               req.Tick,
	}
	// No MinSources gating: RWC v2 reports INSUFFICIENT_EVIDENCE
	// explicitly (see VerdictPolicy.InsufficientEvidenceBelowSources)
	// rather than hard-failing the call, which is what
	// lifecycle.ErrPlanUnsatisfied would do.
	plan := lifecycle.PlanEvidence(intent, nil)

	caseIn := canonical.CaseInput{
		Predicate:    req.Predicate,
		Submissions:  req.Submissions,
		Policy:       req.Policy,
		PatternScore: req.PatternScore,
		PriceAnomaly: req.PriceAnomaly,
		Tick:         req.Tick,
	}

	lcRes, err := k.Lifecycle.RunUnified(ctx, intent, plan, caseIn)
	if err != nil {
		return nil, fmt.Errorf("rwc: RunUnified(%s): %w", req.CaseID, err)
	}

	// --- The verdict, derived from the NATIVE decision -----------------
	// WAVE A item 1. The action below is the one pkg/execution's DECISION
	// stage recorded, which is pkg/moat/decision.Engine's own output for
	// this case. Nothing in this function can reach a verdict any other
	// way: InterpretNativeDecision does not accept a ConstraintResult,
	// and a missing action is an error rather than a fallback.
	verdictPolicy := req.VerdictPolicy
	if verdictPolicy.Mapping == nil {
		verdictPolicy = DefaultVerdictPolicy()
	}
	nativeDecision := lcRes.Canonical.Decision
	verdict, err := InterpretNativeDecision(nativeDecision,
		lcRes.Canonical.Arbitration.EvidenceCount, verdictPolicy)
	if err != nil {
		return nil, fmt.Errorf("rwc: interpreting the native decision for %s: %w", req.CaseID, err)
	}

	return &CaseResult{
		Verdict:             verdict,
		VerdictPolicyHash:   verdictPolicy.Hash(),
		DecisionRoot:        DecisionRoot(lcRes.Execution.ExecutionRootHash, nativeDecision.Action, verdictPolicy, verdict),
		ConstraintWarning:   ConstraintCrossCheck(req.Constraint, nativeDecision),
		HumanReviewRequired: lcRes.HumanReviewRequired,
		CaseID:              req.CaseID,
		InputHash:           inputHash,
		ExecutionID:         lcRes.Execution.Trace.Context.ExecutionID,
		CanonicalHash:       lcRes.Certificate.Canonical.Hash,
		CertificateHash:     lcRes.Certificate.Hash,
		LedgerAnchor:        k.Canonical.Fusion.Head(),
		DecisionAction:      lcRes.Execution.Decision,
		RiskScore:           lcRes.Canonical.Decision.RiskScore,
		Correlation:         lcRes.Correlation,
		LineageCaseID:       lcRes.CaseID,
		Lifecycle:           lcRes,
	}, nil
}
