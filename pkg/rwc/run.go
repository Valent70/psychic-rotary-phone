package rwc

import (
	"context"
	"fmt"

	"veriqo/pkg/canonical"
	"veriqo/pkg/lifecycle"
	"veriqo/pkg/moat/decision"
	"veriqo/pkg/moat/entity"
	"veriqo/veriqo/kernel"
)

// CaseRequest is everything one native RunUnified call needs, expressed
// in RWC terms. It is deliberately a thin wrapper: every field maps
// 1:1 onto an existing lifecycle.Intent / canonical.CaseInput field —
// see Run below. baseTime (Tick) is caller-injected, never derived from
// time.Now(), per the brief's §6 determinism requirement.
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

// CaseResult is one case's outcome, carrying every hash/ID the brief's
// §6/§10 requires plus the full native lifecycle.Result for anyone who
// wants to inspect a deeper layer (evidence bundle assembly does).
type CaseResult struct {
	CaseID string

	InputHash       string // sha256 over the CaseRequest itself, as originally supplied
	ExecutionID     string // execution.Context.ExecutionID, from the real DAG run
	CanonicalHash   string // canonical.CanonicalCertificate.Hash
	CertificateHash string // lifecycle.LifecycleCertificate.Hash
	// LedgerAnchor is the real, in-memory, hash-chain head this case's
	// arbitration was appended to (pkg/moat/fusion.Engine.Head()) at the
	// moment this case ran — independently re-derivable via
	// Fusion.VerifyChain(). It is NOT durable/external anchoring: see
	// baseline doc §9 (gap G5) for why no such mechanism exists anywhere
	// in this codebase, and docs/VERIQO_RWC_V2_VALIDATION_REPORT.md for
	// the honest statement of what this field does and does not prove.
	LedgerAnchor string

	DecisionAction string // decision.Action, verbatim from the native engine (MONITOR/FLAG/ESCALATE)
	RiskScore      float64

	Lifecycle *lifecycle.Result
}

// Run executes one RWC case through VERIQO's real native path:
// Intent -> EvidencePlan -> lifecycle.Orchestrator.RunUnified (which
// itself runs Entity Resolution -> Evidence -> Truth -> Trust ->
// Bayesian -> Causal -> Decision -> Policy -> Digital Twin -> Economic
// Consequence -> IVF -> LifecycleCertificate through the real 18-stage
// pkg/execution DAG). No engine here is called directly by this
// package — Run only builds inputs and reads outputs.
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
		RequiredConfidence: 0, // no minimum confidence gate for RWC v2 — evidence completeness is reported, not gated
		TemporalScope:      "rwc-v2",
		Tick:               req.Tick,
	}
	plan := lifecycle.PlanEvidence(intent, nil) // no MinSources gating — RWC v2 reports INSUFFICIENT_EVIDENCE explicitly rather than hard-failing the call

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
		Lifecycle:       lcRes,
	}, nil
}
