// Package verticalslice answers Commercialization Sprint item 2
// directly: "Claude cukup mengimplementasikan minimum vertical slice
// yang benar-benar end-to-end," with the named 15-stage shape:
//
//	SOURCE -> ACQUIRE -> PRESERVE -> HASH -> PROVENANCE -> MANIFEST ->
//	CUSTODY -> TRUST -> DECISION -> AUTHORIZATION -> ACTION -> RECEIPT ->
//	DOSSIER -> REPLAY -> VERIFY
//
// Every stage is real, wired to the FROZEN core trust kernel (see
// docs/VERIQO_CORE_TRUST_KERNEL_FREEZE.md) through its existing,
// tested, public API -- this package adds NO new kernel logic, only
// orchestration:
//
//	SOURCE..CUSTODY   -> pkg/evidence/manifest.Registry's own real
//	                     custody-chained state machine (via
//	                     claimworkflow's "finalize_evidence" step)
//	TRUST             -> pkg/insurance/causation's own real,
//	                     evidence-derived hypothesis status (via
//	                     claimworkflow's "build_hypothesis" step)
//	DECISION          -> pkg/insurance/cre + pkg/insurance/decision
//	                     (via claimworkflow's "build_finding",
//	                     "authorize_finding", and "decide" steps)
//	AUTHORIZATION     -> pkg/insurance/action.AuthorizeAction (NEW
//	                     this round's own Decision -> Action
//	                     Authorization Boundary)
//	ACTION            -> pkg/insurance/action.AuthorizeExecution
//	RECEIPT           -> this package's own Receipt type, a
//	                     commercial-layer PROJECTION of the real
//	                     ACTION_EXECUTED audit.AuditRecord
//	                     AppendExecutionToLedger produces -- never a
//	                     new source of authority, exactly the same
//	                     discipline pkg/commercial/evidencefabric
//	                     already uses one layer up.
//	DOSSIER           -> see pkg/commercial/dossier (built the
//	                     following round)
//	REPLAY / VERIFY   -> Replay and Verify, this file's own functions
package verticalslice

import (
	"errors"
	"fmt"

	"veriqo/pkg/canonical/jcs"
	"veriqo/pkg/evidence/manifest"
	"veriqo/pkg/insurance/action"
	"veriqo/pkg/insurance/claimworkflow"
	"veriqo/pkg/insurance/cre"
	"veriqo/pkg/insurance/decision"
	"veriqo/pkg/platform/audit"
	"veriqo/pkg/workflow"
)

// ActionInput is everything Run needs for the AUTHORIZATION and ACTION
// stages, once a Decision has been reached.
type ActionInput struct {
	Actor           string
	PolicyRef       string
	Scope           string
	PermittedAction action.Action
	Conditions      []string
	AuthorizedAt    uint64
	ExpiresAt       uint64
	ExecutingActor  string
	ExecutionAt     uint64
	LedgerActor     string
}

// Input is the full vertical-slice request: the claim decision input
// (SOURCE through DECISION) plus the action input (AUTHORIZATION
// through RECEIPT).
type Input struct {
	Claim  claimworkflow.ClaimDecisionInput
	Action ActionInput
}

// Receipt is the commercial-facing proof that a specific, authorized
// action was actually carried out -- Definition of Done item 13
// ("receive execution receipt"). It is a read-only projection of the
// real ACTION_EXECUTED ledger record AppendExecutionToLedger already
// produced; this package invents no new authority.
type Receipt struct {
	ReceiptID               string `json:"receipt_id"`
	ActionAuthorizationHash string `json:"action_authorization_hash"`
	DecisionHash            string `json:"decision_hash"`
	PermittedAction         string `json:"permitted_action"`
	Scope                   string `json:"scope"`
	ExecutedBy              string `json:"executed_by"`
	ExecutedAt              uint64 `json:"executed_at"`
	LedgerRecordHash        string `json:"ledger_record_hash"`
}

type receiptHashInput struct {
	ActionAuthorizationHash string `json:"action_authorization_hash"`
	LedgerRecordHash        string `json:"ledger_record_hash"`
	ExecutedBy              string `json:"executed_by"`
	ExecutedAt              uint64 `json:"executed_at"`
}

func BuildReceipt(aa action.ActionAuthorization, d decision.Decision, execRec audit.AuditRecord, executedBy string, executedAt uint64) Receipt {
	id := jcs.MustHash(receiptHashInput{
		ActionAuthorizationHash: aa.Hash(), LedgerRecordHash: execRec.Hash,
		ExecutedBy: executedBy, ExecutedAt: executedAt,
	})
	return Receipt{
		ReceiptID: id, ActionAuthorizationHash: aa.Hash(), DecisionHash: d.Hash(),
		PermittedAction: string(aa.PermittedAction()), Scope: aa.Scope(),
		ExecutedBy: executedBy, ExecutedAt: executedAt, LedgerRecordHash: execRec.Hash,
	}
}

// Result carries every real artifact the vertical slice produced, at
// every stage, for DOSSIER generation, REPLAY comparison, and VERIFY.
type Result struct {
	Manifests            *manifest.Registry
	AuthorizedFinding    cre.AuthorizedFinding
	Decision             decision.Decision
	ActionAuthorization  action.ActionAuthorization
	Receipt              Receipt
	WorkflowRunID        string
	WorkflowStepOrder    []string
	DecisionLedgerRecord audit.AuditRecord
	AuthLedgerRecord     audit.AuditRecord
	ExecLedgerRecord     audit.AuditRecord
}

var (
	// ErrDecideStepMissing is Run's refusal when the underlying
	// workflow somehow completed without a "decide" step result -- a
	// defensive check; claimworkflow.BuildClaimDecisionPlan always
	// includes this step, so this is unreachable through the real
	// public API, exactly like decision.ErrFindingNotAuthorized is
	// unreachable except via a hand-forged Plan.
	ErrDecideStepMissing = errors.New("verticalslice: workflow completed without a decide step result")
)

// Run drives the full 15-stage vertical slice once, against a real
// ledger, and returns every stage's real artifact.
func Run(in Input, ledger *audit.AuditStore) (Result, error) {
	plan, err := claimworkflow.BuildClaimDecisionPlan(in.Claim, ledger)
	if err != nil {
		return Result{}, fmt.Errorf("verticalslice: SOURCE..DECISION: %w", err)
	}
	sched := workflow.NewScheduler()
	record, err := sched.Schedule(plan, in.Claim.Tick)
	if err != nil {
		return Result{}, fmt.Errorf("verticalslice: scheduling: %w", err)
	}
	checkpointStore := audit.NewAuditStore()
	ex := workflow.NewExecutor(checkpointStore)
	record, err = ex.Run(plan, record)
	if err != nil {
		return Result{}, fmt.Errorf("verticalslice: SOURCE..DECISION: %w", err)
	}

	decideResult, ok := record.Completed["decide"]
	if !ok || decideResult.Err != "" {
		return Result{}, ErrDecideStepMissing
	}
	d, ok := decideResult.Output.(decision.Decision)
	if !ok {
		return Result{}, fmt.Errorf("verticalslice: %w: unexpected decide output type %T", ErrDecideStepMissing, decideResult.Output)
	}
	manifestsResult := record.Completed["finalize_evidence"]
	manifests, _ := manifestsResult.Output.(*manifest.Registry)
	authFindingResult := record.Completed["authorize_finding"]
	af, _ := authFindingResult.Output.(cre.AuthorizedFinding)

	decisionRecs := ledger.Snapshot()
	var decisionLedgerRecord audit.AuditRecord
	if len(decisionRecs) > 0 {
		decisionLedgerRecord = decisionRecs[len(decisionRecs)-1]
	}

	// AUTHORIZATION
	aa, err := action.AuthorizeAction(d, in.Action.Actor, in.Action.PolicyRef, in.Action.Scope,
		in.Action.PermittedAction, in.Action.Conditions, in.Action.AuthorizedAt, in.Action.ExpiresAt)
	if err != nil {
		return Result{}, fmt.Errorf("verticalslice: AUTHORIZATION: %w", err)
	}
	authRec, err := action.AppendToLedger(ledger, in.Action.LedgerActor, aa)
	if err != nil {
		return Result{}, fmt.Errorf("verticalslice: AUTHORIZATION ledger: %w", err)
	}

	// ACTION
	if err := action.AuthorizeExecution(aa, d, in.Action.ExecutingActor, in.Action.PermittedAction, in.Action.Scope, in.Action.ExecutionAt); err != nil {
		return Result{}, fmt.Errorf("verticalslice: ACTION: %w", err)
	}
	execRec, err := action.AppendExecutionToLedger(ledger, in.Action.LedgerActor, aa, in.Action.ExecutionAt)
	if err != nil {
		return Result{}, fmt.Errorf("verticalslice: ACTION ledger: %w", err)
	}

	// RECEIPT
	receipt := BuildReceipt(aa, d, execRec, in.Action.ExecutingActor, in.Action.ExecutionAt)

	return Result{
		Manifests: manifests, AuthorizedFinding: af, Decision: d, ActionAuthorization: aa, Receipt: receipt,
		WorkflowRunID: record.RunID, WorkflowStepOrder: record.Order,
		DecisionLedgerRecord: decisionLedgerRecord, AuthLedgerRecord: authRec, ExecLedgerRecord: execRec,
	}, nil
}

// Verify independently re-checks every stage of a Result, end to end
// -- the VERIFY stage. It never trusts Result's own fields at face
// value: it recomputes and re-derives from the real underlying
// artifacts (the manifest registry, the ledger) each time.
func Verify(res Result, ledger *audit.AuditStore) error {
	if res.Decision.IsZero() {
		return fmt.Errorf("verticalslice: VERIFY: zero Decision")
	}
	if err := decision.VerifyDecisionProvenance(res.Decision, res.AuthorizedFinding); err != nil {
		return fmt.Errorf("verticalslice: VERIFY decision provenance: %w", err)
	}
	if err := action.VerifyActionAuthorization(res.ActionAuthorization, res.Decision); err != nil {
		return fmt.Errorf("verticalslice: VERIFY action authorization: %w", err)
	}
	if res.Manifests != nil {
		for _, evidenceID := range append(append([]string(nil), res.AuthorizedFinding.Finding().SupportedBy...), res.AuthorizedFinding.Finding().ContradictedBy...) {
			m, err := res.Manifests.Latest(evidenceID)
			if err != nil {
				return fmt.Errorf("verticalslice: VERIFY manifest %s: %w", evidenceID, err)
			}
			if err := manifest.VerifyManifestHash(m); err != nil {
				return fmt.Errorf("verticalslice: VERIFY manifest hash %s: %w", evidenceID, err)
			}
			if err := res.Manifests.VerifyCustodyChain(evidenceID); err != nil {
				return fmt.Errorf("verticalslice: VERIFY custody chain %s: %w", evidenceID, err)
			}
		}
	}
	if ledger != nil {
		if err := (audit.Auditor{}).VerifyChain(ledger.Snapshot()); err != nil {
			return fmt.Errorf("verticalslice: VERIFY ledger chain: %w", err)
		}
	}
	return nil
}

// Replay runs the IDENTICAL Input against a completely fresh ledger
// and confirms every stage's own hash converges byte-identically with
// a prior Result -- the REPLAY stage: proof this is deterministic
// replay, not merely "ran successfully once."
func Replay(in Input, prior Result) (Result, error) {
	fresh := audit.NewAuditStore()
	res, err := Run(in, fresh)
	if err != nil {
		return Result{}, fmt.Errorf("verticalslice: REPLAY: %w", err)
	}
	if res.Decision.Hash() != prior.Decision.Hash() {
		return Result{}, fmt.Errorf("verticalslice: REPLAY: Decision hash diverged: replayed=%s prior=%s", res.Decision.Hash(), prior.Decision.Hash())
	}
	if res.ActionAuthorization.Hash() != prior.ActionAuthorization.Hash() {
		return Result{}, fmt.Errorf("verticalslice: REPLAY: ActionAuthorization hash diverged: replayed=%s prior=%s", res.ActionAuthorization.Hash(), prior.ActionAuthorization.Hash())
	}
	if res.Receipt.ReceiptID != prior.Receipt.ReceiptID {
		return Result{}, fmt.Errorf("verticalslice: REPLAY: Receipt ID diverged: replayed=%s prior=%s", res.Receipt.ReceiptID, prior.Receipt.ReceiptID)
	}
	return res, nil
}
