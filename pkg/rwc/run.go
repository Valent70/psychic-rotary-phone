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
	// explicitly (see InterpretVerdict) rather than hard-failing the
	// call, which is what lifecycle.ErrPlanUnsatisfied would do.
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

	return &CaseResult{
		CaseID:          req.CaseID,
		InputHash:       inputHash,
		ExecutionID:     lcRes.Execution.Trace.Context.ExecutionID,
		CanonicalHash:   lcRes.Certificate.Canonical.Hash,
		CertificateHash: lcRes.Certificate.Hash,
		LedgerAnchor:    k.Canonical.Fusion.Head(),
		DecisionAction:  lcRes.Execution.Decision,
		RiskScore:       lcRes.Canonical.Decision.RiskScore,
		Correlation:     lcRes.Correlation,
		LineageCaseID:   lcRes.CaseID,
		Lifecycle:       lcRes,
	}, nil
}
