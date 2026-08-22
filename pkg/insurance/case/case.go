// Package caseinsurance is the top-level insurance case aggregate —
// blueprint §2. It owns the case lifecycle state machine and holds
// (references, never re-implements) the party, policy, claim and
// evidence registries the rest of pkg/insurance builds on.
//
// The package name is "caseinsurance" rather than "case" because
// "case" is a Go reserved word and cannot be a package identifier used
// as a qualifier (case.Case would be fine syntactically, but every
// call site would still need to alias the import); callers typically
// import it as:
//
//	import insurancecase "veriqo/pkg/insurance/case"
package caseinsurance

import (
	"errors"
	"fmt"
	"sync"

	"veriqo/pkg/insurance/claim"
	"veriqo/pkg/insurance/evidence"
	"veriqo/pkg/insurance/party"
	"veriqo/pkg/insurance/policy"
)

// State is one stage of the case lifecycle — blueprint §2's own fixed
// sequence.
type State string

const (
	StateCaseCreated              State = "CASE_CREATED"
	StatePartiesIdentified        State = "PARTIES_IDENTIFIED"
	StatePolicyRegistered         State = "POLICY_REGISTERED"
	StateClaimRegistered          State = "CLAIM_REGISTERED"
	StateEvidenceIngested         State = "EVIDENCE_INGESTED"
	StateEvidenceVerified         State = "EVIDENCE_VERIFIED"
	StateTimelineReconstructed    State = "TIMELINE_RECONSTRUCTED"
	StatePolicyMapped             State = "CONTRACT_POLICY_MAPPED"
	StateContradictionsAnalyzed   State = "CONTRADICTIONS_ANALYZED"
	StateCausationAnalyzed        State = "CAUSATION_ANALYZED"
	StateQuantumAnalyzed          State = "QUANTUM_ANALYZED"
	StateCoverageIssuesIdentified State = "COVERAGE_ISSUES_IDENTIFIED"
	StateRecoveryAnalyzed         State = "RECOVERY_ANALYZED"
	StateHumanReview              State = "HUMAN_REVIEW"
	StateDossierGenerated         State = "DOSSIER_GENERATED"
	// Terminal states, reachable only from StateDossierGenerated.
	StateCaseClosed State = "CASE_CLOSED"
	StateOpenIssues State = "OPEN_ISSUES"
)

// sequence is the blueprint's own linear lifecycle (§2), in order.
// State transitions may only move FORWARD through this sequence, one
// step at a time — the mechanism that makes "CLAIM -> APPROVED" or any
// other skip structurally impossible, not just discouraged by
// convention.
var sequence = []State{
	StateCaseCreated, StatePartiesIdentified, StatePolicyRegistered, StateClaimRegistered,
	StateEvidenceIngested, StateEvidenceVerified, StateTimelineReconstructed, StatePolicyMapped,
	StateContradictionsAnalyzed, StateCausationAnalyzed, StateQuantumAnalyzed,
	StateCoverageIssuesIdentified, StateRecoveryAnalyzed, StateHumanReview, StateDossierGenerated,
}

var stateIndex = func() map[State]int {
	m := make(map[State]int, len(sequence))
	for i, s := range sequence {
		m[s] = i
	}
	return m
}()

var (
	ErrEmptyCaseID       = errors.New("case: CaseID must be non-empty")
	ErrUnknownState      = errors.New("case: unknown State")
	ErrTerminalState     = errors.New("case: case is already in a terminal state")
	ErrCannotSkipState   = errors.New("case: cannot advance more than one lifecycle stage at a time")
	ErrCannotGoBackward  = errors.New("case: cannot move the case lifecycle backward")
	ErrInvalidTerminal   = errors.New("case: terminal state is only reachable from DOSSIER_GENERATED")
	ErrPolicyNotFound    = errors.New("case: PolicyID not registered on this case")
	ErrClaimNotFound     = errors.New("case: ClaimID not registered on this case")
	ErrDuplicateClaimID  = errors.New("case: ClaimID already registered on this case")
	ErrDuplicatePolicyID = errors.New("case: PolicyID already registered on this case")
)

// Case is the top-level aggregate for one insurance case.
type Case struct {
	mu sync.RWMutex

	CaseID    string
	state     State
	CreatedAt uint64

	Parties  *party.Registry
	Policies map[string]*policy.History // PolicyID -> History
	Claims   map[string]claim.Claim     // ClaimID -> Claim
	Evidence *evidence.Registry

	stateLog []stateTransition

	// exceptions is the case's append-only exception-state overlay —
	// see stage.go. Deliberately guarded by its OWN mutex, so raising an
	// exception can never take the lifecycle lock and therefore can
	// never interact with Advance.
	exceptions exceptionLog
}

type stateTransition struct {
	From State  `json:"from"`
	To   State  `json:"to"`
	Tick uint64 `json:"tick"`
}

// New constructs a fresh case in StateCaseCreated.
func New(caseID string, createdAt uint64) (*Case, error) {
	if caseID == "" {
		return nil, ErrEmptyCaseID
	}
	return &Case{
		CaseID:    caseID,
		state:     StateCaseCreated,
		CreatedAt: createdAt,
		Parties:   party.NewRegistry(),
		Policies:  make(map[string]*policy.History),
		Claims:    make(map[string]claim.Claim),
		Evidence:  evidence.NewRegistry(),
	}, nil
}

// State returns the case's current lifecycle state.
func (c *Case) State() State {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

// StateLog returns every recorded transition, oldest first — the case
// lifecycle's own audit trail.
func (c *Case) StateLog() []stateTransition {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]stateTransition, len(c.stateLog))
	copy(out, c.stateLog)
	return out
}

// Advance moves the case to the next lifecycle state. It enforces the
// blueprint's own rule mechanically: a case can only move ONE step
// forward through the fixed sequence at a time. Attempting to jump two
// or more stages (the shape of "CLAIM -> APPROVED"), move backward, or
// advance a terminal case are all refused with a specific error, never
// silently normalised into "the closest valid move".
func (c *Case) Advance(to State, tick uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.state == StateCaseClosed || c.state == StateOpenIssues {
		return ErrTerminalState
	}

	// Terminal transitions are a special case: only reachable from
	// StateDossierGenerated, and not part of the linear `sequence`.
	if to == StateCaseClosed || to == StateOpenIssues {
		if c.state != StateDossierGenerated {
			return ErrInvalidTerminal
		}
		c.stateLog = append(c.stateLog, stateTransition{From: c.state, To: to, Tick: tick})
		c.state = to
		return nil
	}

	toIdx, ok := stateIndex[to]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownState, to)
	}
	fromIdx := stateIndex[c.state]
	switch {
	case toIdx < fromIdx:
		return ErrCannotGoBackward
	case toIdx > fromIdx+1:
		return ErrCannotSkipState
	case toIdx == fromIdx:
		return nil // idempotent no-op: already at this state
	}

	c.stateLog = append(c.stateLog, stateTransition{From: c.state, To: to, Tick: tick})
	c.state = to
	return nil
}

// RegisterPolicy attaches a new policy History to the case.
func (c *Case) RegisterPolicy(policyID string, h *policy.History) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.Policies[policyID]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicatePolicyID, policyID)
	}
	c.Policies[policyID] = h
	return nil
}

// RegisterClaim attaches a new claim to the case. Every claim's
// Status is verified to be StatusRegistered — a Case must never be
// handed a claim that already carries an out-of-band settlement
// decision.
func (c *Case) RegisterClaim(cl claim.Claim) error {
	if cl.Status != claim.StatusRegistered {
		return fmt.Errorf("case: claim %s must be registered in StatusRegistered, got %s", cl.ClaimID, cl.Status)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.Claims[cl.ClaimID]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateClaimID, cl.ClaimID)
	}
	c.Claims[cl.ClaimID] = cl
	return nil
}

// PolicyEffectiveAt resolves which policy version was effective for
// policyID at the given tick — the case-level entry point for
// blueprint §6's P0 requirement.
func (c *Case) PolicyEffectiveAt(policyID string, tick uint64) (policy.Version, error) {
	c.mu.RLock()
	h, ok := c.Policies[policyID]
	c.mu.RUnlock()
	if !ok {
		return policy.Version{}, fmt.Errorf("%w: %s", ErrPolicyNotFound, policyID)
	}
	return h.EffectiveAt(tick)
}

// ClaimByID returns the claim for claimID.
func (c *Case) ClaimByID(claimID string) (claim.Claim, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	cl, ok := c.Claims[claimID]
	if !ok {
		return claim.Claim{}, fmt.Errorf("%w: %s", ErrClaimNotFound, claimID)
	}
	return cl, nil
}

// Sequence returns the fixed lifecycle sequence (excluding terminal
// states) in order — exposed so tests and the dossier generator can
// walk it without re-declaring it.
func Sequence() []State {
	out := make([]State, len(sequence))
	copy(out, sequence)
	return out
}
