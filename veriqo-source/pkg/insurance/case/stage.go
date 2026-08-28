package caseinsurance

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ---- The externally-reported lifecycle stage (Final Design §41 STEP 2)
//
// Two lifecycle vocabularies are in play, and they do not agree:
//
//   - the sixteen-state `sequence` in case.go, forward-only and one step
//     at a time, which is what makes calling AnalyzeCausation before
//     evidence is ingested STRUCTURALLY impossible rather than merely
//     documented; and
//   - the nine-stage vocabulary the Final Design freezes in §41 STEP 2,
//     plus five exception states.
//
// This file implements the reconciliation recorded in
// docs/governance/INSURANCE_IMPLEMENTATION_REPORT.md:
//
//	The nine-stage vocabulary is the canonical EXTERNALLY REPORTED
//	lifecycle stage. The sixteen-state sequence remains the internal
//	step-ordering mechanism and is not replaced. Each internal state
//	maps to exactly one external stage by a total, tested mapping. The
//	external stage is DERIVED, never settable. Exception states are an
//	additive, recorded overlay that never moves the internal machine
//	forward or backward.
//
// Why that way round, in one line each:
//
//  1. Collapsing sixteen states into nine would delete real ordering
//     constraints — there would no longer be a state boundary between
//     CONTRADICTIONS_ANALYZED and CAUSATION_ANALYZED — and would weaken
//     the one-step-forward-only invariant existing tests assert.
//     Deleting an enforcement to match a vocabulary is backwards.
//  2. The nine-stage vocabulary is REPORTING vocabulary: its own
//     document introduces it in the context of a Case Room and a
//     dossier. It is coarser on purpose.
//  3. The five exception states are orthogonal to progress. A case can
//     be ON_LEGAL_HOLD *and* RECONSTRUCTING; CONTRADICTED is not a
//     position in a sequence. Modelling them as sequence members would
//     force a false choice between "where the case is" and "what is
//     wrong with it".

// Stage is the coarse, externally-reported lifecycle stage — the Final
// Design's own frozen nine-value vocabulary, verbatim.
type Stage string

const (
	StageIntake           Stage = "INTAKE"
	StagePreserved        Stage = "PRESERVED"
	StageReconstructing   Stage = "RECONSTRUCTING"
	StageReviewRequired   Stage = "REVIEW_REQUIRED"
	StageActionRequired   Stage = "ACTION_REQUIRED"
	StageEvidenceComplete Stage = "EVIDENCE_COMPLETE"
	StageHumanDecision    Stage = "HUMAN_DECISION"
	StageResolved         Stage = "RESOLVED"
	StageClosed           Stage = "CLOSED"
)

// StageOrder returns the nine stages in the design document's own
// order. Exposed so a Case Room, dossier renderer or status view can
// walk them without re-declaring the list.
func StageOrder() []Stage {
	return []Stage{
		StageIntake, StagePreserved, StageReconstructing, StageReviewRequired,
		StageActionRequired, StageEvidenceComplete, StageHumanDecision,
		StageResolved, StageClosed,
	}
}

var stageOrderIndex = func() map[Stage]int {
	m := make(map[Stage]int, 9)
	for i, s := range StageOrder() {
		m[s] = i
	}
	return m
}()

// IsKnownStage reports whether s is one of the nine modelled stages.
func IsKnownStage(s Stage) bool { _, ok := stageOrderIndex[s]; return ok }

// stageOf is the TOTAL mapping from each internal lifecycle state to
// exactly one external stage. Every one of the seventeen State values
// (fifteen sequence members plus the two terminals) appears here
// exactly once; TestStageMappingIsTotalAndSingleValued proves it, so a
// future state added to `sequence` without a stage cannot compile past
// the test suite.
//
// The groupings follow what each internal step actually accomplishes:
//
//   - INTAKE            — the case exists and its participants,
//     contract and claim are on record.
//   - PRESERVED         — evidence is in the case and its integrity has
//     been assessed; this is the point after which
//     the evidence set is a preserved artifact.
//   - RECONSTRUCTING    — building the picture: what happened, when,
//     and under which policy version.
//   - REVIEW_REQUIRED   — the analyses that surface disagreement:
//     contradictions and competing causal hypotheses.
//   - ACTION_REQUIRED   — the analyses that generate work for a human:
//     quantum figures and coverage issues.
//   - EVIDENCE_COMPLETE — recovery avenues assessed; the evidentiary
//     picture is as complete as this case will get.
//   - HUMAN_DECISION    — awaiting the authorized human determination.
//   - RESOLVED          — the dossier exists; the work product is done.
//   - CLOSED            — terminal, either way.
//
// OPEN_ISSUES maps to CLOSED deliberately: it is a terminal state of the
// case's PROCESS. That the case closed with unresolved issues is a fact
// about its content, and it is reported by the dossier's own
// HumanReviewRequired flag and by the INSUFFICIENT/CONTRADICTED
// exception markers below — not by pretending the process is still
// running.
var stageOf = map[State]Stage{
	StateCaseCreated:              StageIntake,
	StatePartiesIdentified:        StageIntake,
	StatePolicyRegistered:         StageIntake,
	StateClaimRegistered:          StageIntake,
	StateEvidenceIngested:         StagePreserved,
	StateEvidenceVerified:         StagePreserved,
	StateTimelineReconstructed:    StageReconstructing,
	StatePolicyMapped:             StageReconstructing,
	StateContradictionsAnalyzed:   StageReviewRequired,
	StateCausationAnalyzed:        StageReviewRequired,
	StateQuantumAnalyzed:          StageActionRequired,
	StateCoverageIssuesIdentified: StageActionRequired,
	StateRecoveryAnalyzed:         StageEvidenceComplete,
	StateHumanReview:              StageHumanDecision,
	StateDossierGenerated:         StageResolved,
	StateCaseClosed:               StageClosed,
	StateOpenIssues:               StageClosed,
}

// StageForState returns the external stage a given internal state
// reports as. It is a pure lookup with no default: an unmapped state is
// an error, never silently reported as INTAKE.
func StageForState(s State) (Stage, error) {
	st, ok := stageOf[s]
	if !ok {
		return "", fmt.Errorf("%w: %q has no external stage mapping", ErrUnknownState, s)
	}
	return st, nil
}

// StatesForStage returns every internal state that reports as stage, in
// lifecycle order. The inverse of StageForState, for a Case Room that
// wants to show what a coarse stage actually covers.
func StatesForStage(stage Stage) []State {
	var out []State
	for _, s := range sequence {
		if stageOf[s] == stage {
			out = append(out, s)
		}
	}
	for _, s := range []State{StateCaseClosed, StateOpenIssues} {
		if stageOf[s] == stage {
			out = append(out, s)
		}
	}
	return out
}

// Stage returns this case's current externally-reported lifecycle
// stage. It is DERIVED from the internal state on every call — there is
// no stored stage field, and therefore no way to set one, which is what
// makes a reported stage impossible to desynchronise from the real
// lifecycle position.
func (c *Case) Stage() Stage {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return stageOf[c.state]
}

// ---- Exception states -----------------------------------------------

// Exception is one of the five exception states the Final Design
// freezes alongside the nine stages. An Exception is NOT a position in
// the lifecycle: it is a fact ABOUT a case that holds (or does not)
// independently of how far the case has progressed. A case can be
// ON_LEGAL_HOLD while RECONSTRUCTING, and CONTRADICTED while
// ACTION_REQUIRED.
type Exception string

const (
	// ExceptionDisputed: the claim has become a dispute. Set when a
	// dispute matter is opened against this case.
	ExceptionDisputed Exception = "DISPUTED"
	// ExceptionContradicted: material evidence in this case contradicts
	// other material evidence, unresolved.
	ExceptionContradicted Exception = "CONTRADICTED"
	// ExceptionInsufficient: mandatory evidence is missing. Never a
	// conclusion about the claim's merits — a statement about the
	// evidence set.
	ExceptionInsufficient Exception = "INSUFFICIENT"
	// ExceptionOnLegalHold: a preservation/legal hold is in force over
	// this case's evidence.
	ExceptionOnLegalHold Exception = "ON_LEGAL_HOLD"
	// ExceptionSuperseded: this case has been superseded by another
	// (e.g. consolidated into a larger matter).
	ExceptionSuperseded Exception = "SUPERSEDED"
)

var knownExceptions = map[Exception]bool{
	ExceptionDisputed: true, ExceptionContradicted: true, ExceptionInsufficient: true,
	ExceptionOnLegalHold: true, ExceptionSuperseded: true,
}

// IsKnownException reports whether e is a modelled exception state.
func IsKnownException(e Exception) bool { return knownExceptions[e] }

// ExceptionOrder returns the five exception states in the design
// document's own order.
func ExceptionOrder() []Exception {
	return []Exception{
		ExceptionDisputed, ExceptionContradicted, ExceptionInsufficient,
		ExceptionOnLegalHold, ExceptionSuperseded,
	}
}

var (
	ErrUnknownException = errors.New("case: unknown exception state")
	ErrEmptyReason      = errors.New("case: an exception state must record why it was raised")
	ErrEmptyEvidence    = errors.New(
		"case: an exception state must cite the artifact that raised it — a contradiction ID, a gap " +
			"assessment issue, a preservation order ID or a dispute ID; an exception nothing points at is prose")
)

// ExceptionRecord is one raised exception, with the reason and the real
// artifact that raised it. Both are mandatory: an exception state with
// no cited artifact is exactly the hand-set status this repository's
// governance forbids everywhere else.
type ExceptionRecord struct {
	Exception Exception `json:"exception"`
	Reason    string    `json:"reason"`
	// RaisedBy names the REAL artifact that caused this exception — a
	// contradiction.ContradictionRecord's ContradictionID, a
	// gap.Sufficiency issue, a preservation order ID, a dispute matter
	// ID. Never a value this package invents.
	RaisedBy   string `json:"raised_by"`
	RaisedTick uint64 `json:"raised_tick"`

	// ClearedTick is 0 while the exception still holds. Clearing is
	// recorded, not deleted: the fact that a case WAS contradicted at
	// some point survives the contradiction being resolved.
	ClearedTick  uint64 `json:"cleared_tick,omitempty"`
	ClearedBy    string `json:"cleared_by,omitempty"`
	ClearReason  string `json:"clear_reason,omitempty"`
	ClearedState bool   `json:"cleared"`
}

// exceptionLog is the case's append-only exception history. Stored on
// Case via a separate mutex-guarded field so raising an exception can
// never take the lifecycle lock and therefore can never interact with
// Advance.
type exceptionLog struct {
	mu      sync.RWMutex
	records []ExceptionRecord
}

// RaiseException records that an exception state now holds for this
// case. It CANNOT move the lifecycle: this method does not touch
// c.state, does not call Advance, and does not take the lifecycle
// mutex. TestExceptionNeverMovesTheLifecycle proves that mechanically.
func (c *Case) RaiseException(e Exception, reason, raisedBy string, tick uint64) error {
	if !IsKnownException(e) {
		return fmt.Errorf("%w: %q", ErrUnknownException, e)
	}
	if reason == "" {
		return ErrEmptyReason
	}
	if raisedBy == "" {
		return ErrEmptyEvidence
	}
	c.exceptions.mu.Lock()
	defer c.exceptions.mu.Unlock()
	c.exceptions.records = append(c.exceptions.records, ExceptionRecord{
		Exception: e, Reason: reason, RaisedBy: raisedBy, RaisedTick: tick,
	})
	return nil
}

// ClearException records that a previously-raised exception no longer
// holds. The original raising is NOT deleted — a cleared record keeps
// its RaisedTick, RaisedBy and Reason, and gains the clearing metadata.
// Clearing an exception that was never raised (or is already cleared)
// is refused rather than silently ignored.
func (c *Case) ClearException(e Exception, clearedBy, reason string, tick uint64) error {
	if !IsKnownException(e) {
		return fmt.Errorf("%w: %q", ErrUnknownException, e)
	}
	if reason == "" {
		return ErrEmptyReason
	}
	if clearedBy == "" {
		return ErrEmptyEvidence
	}
	c.exceptions.mu.Lock()
	defer c.exceptions.mu.Unlock()
	for i := len(c.exceptions.records) - 1; i >= 0; i-- {
		r := c.exceptions.records[i]
		if r.Exception == e && !r.ClearedState {
			r.ClearedState = true
			r.ClearedTick = tick
			r.ClearedBy = clearedBy
			r.ClearReason = reason
			c.exceptions.records[i] = r
			return nil
		}
	}
	return fmt.Errorf("case: exception %q is not currently raised on this case", e)
}

// ActiveExceptions returns every exception currently holding, in the
// design document's own order. Derived from the log on every call.
func (c *Case) ActiveExceptions() []Exception {
	c.exceptions.mu.RLock()
	defer c.exceptions.mu.RUnlock()
	active := map[Exception]bool{}
	for _, r := range c.exceptions.records {
		if !r.ClearedState {
			active[r.Exception] = true
		}
	}
	var out []Exception
	for _, e := range ExceptionOrder() {
		if active[e] {
			out = append(out, e)
		}
	}
	return out
}

// ExceptionLog returns the full append-only exception history, oldest
// first — including cleared entries, which are history and not garbage.
func (c *Case) ExceptionLog() []ExceptionRecord {
	c.exceptions.mu.RLock()
	defer c.exceptions.mu.RUnlock()
	out := make([]ExceptionRecord, len(c.exceptions.records))
	copy(out, c.exceptions.records)
	return out
}

// HasException reports whether e currently holds for this case.
func (c *Case) HasException(e Exception) bool {
	for _, active := range c.ActiveExceptions() {
		if active == e {
			return true
		}
	}
	return false
}

// ---- The composed external status -----------------------------------

// Status is the whole externally-reported position of a case: the
// derived nine-value Stage, plus every exception state currently
// holding. This is what a Case Room panel, a dossier header or an
// external status view renders. Every field is derived; the type has no
// constructor that takes a stage, because there is deliberately no way
// to assert one.
type Status struct {
	CaseID string `json:"case_id"`
	// Stage is derived from the internal lifecycle state.
	Stage Stage `json:"stage"`
	// InternalState is the fine-grained state the stage was derived
	// from, reported alongside rather than hidden: an operator debugging
	// an ordering problem needs the state, and a claims reviewer reading
	// the same object needs the stage.
	InternalState State `json:"internal_state"`
	// Exceptions are the exception states currently holding, orthogonal
	// to Stage.
	Exceptions []Exception `json:"exceptions,omitempty"`
	// Terminal reports whether the case's process has finished.
	Terminal bool `json:"terminal"`
}

// Status composes this case's current external status.
func (c *Case) Status() Status {
	c.mu.RLock()
	state := c.state
	c.mu.RUnlock()
	return Status{
		CaseID:        c.CaseID,
		Stage:         stageOf[state],
		InternalState: state,
		Exceptions:    c.ActiveExceptions(),
		Terminal:      state == StateCaseClosed || state == StateOpenIssues,
	}
}

// StageRank returns the position of a stage in the nine-stage order,
// for a caller that needs to compare two stages. Returns -1 for an
// unknown stage rather than 0, so an unrecognised value never sorts as
// if it were INTAKE.
func StageRank(s Stage) int {
	if i, ok := stageOrderIndex[s]; ok {
		return i
	}
	return -1
}

// sortedExceptions is a small helper kept for deterministic rendering
// in callers that accumulate exceptions from several cases.
func sortedExceptions(in []Exception) []Exception {
	out := append([]Exception(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
