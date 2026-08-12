// Package state closes PHASE 8 of the v7.10.3 production-readiness
// audit. pkg/trust already had kernel, policy, evidence, score and
// certificate — a trust SCORE at a point in time. What it could not do
// is answer the two questions the audit named:
//
//	Why does VERIQO trust this entity RIGHT NOW?
//	What evidence caused trust to increase/decrease?
//
// A score cannot answer either, because a score has no predecessor, no
// reason, no validity window and no way to be revoked. This package
// models trust as a STATE MACHINE over an append-only transition
// ledger, so "right now" is a fold over history and every movement has
// a named cause.
//
// Design decisions worth stating explicitly:
//
//   - Trust DECAYS. A trust state carries a validity window; past its
//     half-life the effective score decays toward the neutral prior.
//     Trust that never decays is indistinguishable from a hardcoded
//     allowlist, which is what VERIQO exists to replace.
//   - Revocation is TERMINAL and cannot be undone by an automated
//     path — only by an explicit governance decision with an actor.
//     "Auto-unrevoke on a good day" is how trust systems get gamed.
//   - Escalation is not a score change; it is a routing decision that
//     sends the subject to human approval (PHASE 49).
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
)

// Level is the coarse trust classification a policy layer consumes.
type Level string

const (
	LevelUnknown     Level = "UNKNOWN"     // never assessed
	LevelProvisional Level = "PROVISIONAL" // assessed, thin evidence
	LevelTrusted     Level = "TRUSTED"
	LevelDegraded    Level = "DEGRADED" // measurable decline, still usable
	LevelSuspect     Level = "SUSPECT"  // contradicted; requires approval
	LevelRevoked     Level = "REVOKED"  // terminal
)

// TransitionKind names why trust moved.
type TransitionKind string

const (
	TransitionAssess    TransitionKind = "ASSESS"
	TransitionDecay     TransitionKind = "DECAY"
	TransitionEscalate  TransitionKind = "ESCALATE"
	TransitionRevoke    TransitionKind = "REVOKE"
	TransitionReinstate TransitionKind = "REINSTATE"
)

// Errors.
var (
	ErrEmptySubject      = errors.New("truststate: subject must be non-empty")
	ErrBadScore          = errors.New("truststate: score must be in [0,1]")
	ErrNoReason          = errors.New("truststate: every transition must carry a reason")
	ErrNoEvidence        = errors.New("truststate: a trust increase requires at least one evidence reference")
	ErrRevokedTerminal   = errors.New("truststate: a revoked subject can only be reinstated by an explicit governance decision")
	ErrNoGovernanceActor = errors.New("truststate: reinstatement requires a named governance actor and approval")
	ErrChainBroken       = errors.New("truststate: trust ledger hash chain is broken")
	ErrUnknownSubject    = errors.New("truststate: subject has no trust history")
)

// Transition is one append-only trust ledger entry.
type Transition struct {
	Index      uint64         `json:"index"`
	Subject    string         `json:"subject"`
	Kind       TransitionKind `json:"kind"`
	FromLevel  Level          `json:"from_level"`
	ToLevel    Level          `json:"to_level"`
	FromScore  float64        `json:"from_score"`
	ToScore    float64        `json:"to_score"`
	Reason     string         `json:"reason"`
	ActorID    string         `json:"actor_id"`
	PolicyName string         `json:"policy_name"`
	// EvidenceRefs are the identifiers (evidence IDs, certificate
	// hashes, calibration record hashes) that justify the movement.
	EvidenceRefs []string `json:"evidence_refs"`
	Tick         uint64   `json:"tick"`
	// ValidUntil is when this state expires if nothing refreshes it.
	ValidUntil uint64 `json:"valid_until"`
	PrevHash   string `json:"prev_hash"`
	Hash       string `json:"hash"`
}

func hashTransition(t Transition) string {
	h := sha256.New()
	fmt.Fprintf(h, "veriqo.truststate/v1|idx=%d|subj=%s|kind=%s|from=%s/%.9f|to=%s/%.9f|reason=%s|actor=%s|policy=%s|tick=%d|until=%d|prev=%s|",
		t.Index, t.Subject, t.Kind, t.FromLevel, t.FromScore, t.ToLevel, t.ToScore,
		t.Reason, t.ActorID, t.PolicyName, t.Tick, t.ValidUntil, t.PrevHash)
	refs := append([]string(nil), t.EvidenceRefs...)
	sort.Strings(refs)
	for _, r := range refs {
		fmt.Fprintf(h, "ev=%s|", r)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TrustState is the current, explainable trust posture of a subject.
type TrustState struct {
	Subject        string      `json:"subject"`
	Level          Level       `json:"level"`
	Score          float64     `json:"score"`
	EffectiveScore float64     `json:"effective_score"` // after decay at the asked tick
	Confidence     float64     `json:"confidence"`
	EvidenceSet    []string    `json:"evidence_set"`
	Policy         string      `json:"policy"`
	ValidFrom      uint64      `json:"valid_from"`
	ValidTo        uint64      `json:"valid_to"`
	PreviousState  *TrustState `json:"previous_state,omitempty"`
	Reason         string      `json:"reason"`
	Certificate    string      `json:"certificate"` // hash of the transition that produced it
	Revoked        bool        `json:"revoked"`
	Escalated      bool        `json:"escalated"`
}

// GovernanceDecision is the human/authority act required to move a
// subject out of a terminal state.
type GovernanceDecision struct {
	ActorID      string
	ApprovedBy   string
	Reason       string
	Tick         uint64
	EvidenceRefs []string
}

// Engine is the trust state machine. Safe for concurrent use.
type Engine struct {
	mu     sync.RWMutex
	ledger []Transition
	// HalfLife is the number of ticks after which an unrefreshed trust
	// score has decayed halfway toward the neutral prior.
	HalfLife uint64
	// NeutralPrior is what trust decays toward.
	NeutralPrior float64
}

// NewEngine constructs the trust state engine.
func NewEngine(halfLife uint64, neutralPrior float64) *Engine {
	if halfLife == 0 {
		halfLife = 1000
	}
	return &Engine{HalfLife: halfLife, NeutralPrior: neutralPrior}
}

func levelFor(score float64) Level {
	switch {
	case score >= 0.75:
		return LevelTrusted
	case score >= 0.5:
		return LevelProvisional
	case score >= 0.25:
		return LevelDegraded
	default:
		return LevelSuspect
	}
}

// Assess records a new trust assessment. An INCREASE requires
// evidence; a decrease does not (evidence of absence is still a
// reason, and refusing to lower trust without paperwork is the wrong
// default for a trust system).
func (e *Engine) Assess(subject, actorID, policy, reason string, score float64, evidenceRefs []string, tick, validFor uint64) (Transition, error) {
	if subject == "" {
		return Transition{}, ErrEmptySubject
	}
	if score < 0 || score > 1 {
		return Transition{}, ErrBadScore
	}
	if reason == "" {
		return Transition{}, ErrNoReason
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	cur := e.stateLocked(subject, tick)
	if cur.Revoked {
		return Transition{}, ErrRevokedTerminal
	}
	if score > cur.EffectiveScore && len(evidenceRefs) == 0 {
		return Transition{}, ErrNoEvidence
	}
	validUntil := uint64(0)
	if validFor > 0 {
		validUntil = tick + validFor
	}
	return e.appendLocked(Transition{
		Subject: subject, Kind: TransitionAssess, FromLevel: cur.Level, ToLevel: levelFor(score),
		FromScore: cur.Score, ToScore: score, Reason: reason, ActorID: actorID,
		PolicyName: policy, EvidenceRefs: evidenceRefs, Tick: tick, ValidUntil: validUntil,
	}), nil
}

// Escalate routes a subject to human approval without changing its
// score — the audit's point that escalation is a workflow, not an enum.
func (e *Engine) Escalate(subject, actorID, reason string, evidenceRefs []string, tick uint64) (Transition, error) {
	if reason == "" {
		return Transition{}, ErrNoReason
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	cur := e.stateLocked(subject, tick)
	return e.appendLocked(Transition{
		Subject: subject, Kind: TransitionEscalate, FromLevel: cur.Level, ToLevel: LevelSuspect,
		FromScore: cur.Score, ToScore: cur.Score, Reason: reason, ActorID: actorID,
		EvidenceRefs: evidenceRefs, Tick: tick,
	}), nil
}

// Revoke is terminal.
func (e *Engine) Revoke(subject, actorID, reason string, evidenceRefs []string, tick uint64) (Transition, error) {
	if reason == "" {
		return Transition{}, ErrNoReason
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	cur := e.stateLocked(subject, tick)
	return e.appendLocked(Transition{
		Subject: subject, Kind: TransitionRevoke, FromLevel: cur.Level, ToLevel: LevelRevoked,
		FromScore: cur.Score, ToScore: 0, Reason: reason, ActorID: actorID,
		EvidenceRefs: evidenceRefs, Tick: tick,
	}), nil
}

// Reinstate requires an explicit governance decision with an approver.
func (e *Engine) Reinstate(subject string, d GovernanceDecision, score float64) (Transition, error) {
	if d.ActorID == "" || d.ApprovedBy == "" || d.Reason == "" {
		return Transition{}, ErrNoGovernanceActor
	}
	if score < 0 || score > 1 {
		return Transition{}, ErrBadScore
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	cur := e.stateLocked(subject, d.Tick)
	if !cur.Revoked {
		return Transition{}, fmt.Errorf("truststate: %s is not revoked", subject)
	}
	return e.appendLocked(Transition{
		Subject: subject, Kind: TransitionReinstate, FromLevel: LevelRevoked, ToLevel: levelFor(score),
		FromScore: 0, ToScore: score,
		Reason:  fmt.Sprintf("governance reinstatement approved by %s: %s", d.ApprovedBy, d.Reason),
		ActorID: d.ActorID, EvidenceRefs: d.EvidenceRefs, Tick: d.Tick,
	}), nil
}

func (e *Engine) appendLocked(t Transition) Transition {
	t.Index = uint64(len(e.ledger))
	if len(e.ledger) > 0 {
		t.PrevHash = e.ledger[len(e.ledger)-1].Hash
	}
	t.Hash = hashTransition(t)
	e.ledger = append(e.ledger, t)
	return t
}

// StateAt answers "why does VERIQO trust this subject at tick T" — the
// full explainable state, including decay applied at that tick.
func (e *Engine) StateAt(subject string, tick uint64) TrustState {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.stateLocked(subject, tick)
}

func (e *Engine) stateLocked(subject string, tick uint64) TrustState {
	st := TrustState{Subject: subject, Level: LevelUnknown, Score: e.NeutralPrior,
		EffectiveScore: e.NeutralPrior, Reason: "never assessed"}
	var prev *TrustState
	var lastTick uint64
	found := false
	for _, t := range e.ledger {
		if t.Subject != subject || t.Tick > tick {
			continue
		}
		snapshot := st
		prev = &snapshot
		st = TrustState{
			Subject: subject, Level: t.ToLevel, Score: t.ToScore, Policy: t.PolicyName,
			EvidenceSet: append(append([]string(nil), st.EvidenceSet...), t.EvidenceRefs...),
			ValidFrom:   t.Tick, ValidTo: t.ValidUntil, Reason: t.Reason,
			Certificate: t.Hash, PreviousState: prev,
			Revoked:   t.Kind == TransitionRevoke || (st.Revoked && t.Kind != TransitionReinstate),
			Escalated: t.Kind == TransitionEscalate,
		}
		lastTick = t.Tick
		found = true
	}
	if !found {
		return st
	}
	st.EffectiveScore = e.decay(st.Score, tick-lastTick)
	// Expiry: past ValidTo the state is no longer authoritative and
	// collapses to PROVISIONAL at the decayed score.
	if st.ValidTo != 0 && tick > st.ValidTo && !st.Revoked {
		st.Level = levelFor(st.EffectiveScore)
		st.Reason = st.Reason + " (expired: state past its validity window, decayed)"
	} else if !st.Revoked && !st.Escalated {
		st.Level = levelFor(st.EffectiveScore)
	}
	st.Confidence = confidenceFrom(len(st.EvidenceSet))
	return st
}

// decay moves a score toward the neutral prior with the configured
// half-life.
func (e *Engine) decay(score float64, elapsed uint64) float64 {
	if elapsed == 0 {
		return score
	}
	f := math.Pow(0.5, float64(elapsed)/float64(e.HalfLife))
	return e.NeutralPrior + (score-e.NeutralPrior)*f
}

func confidenceFrom(n int) float64 {
	// Saturating: more corroborating evidence raises confidence but it
	// never reaches 1.
	return 1 - math.Pow(0.6, float64(n))
}

// History answers "what evidence caused trust to increase/decrease" —
// every transition for a subject, in order.
func (e *Engine) History(subject string) []Transition {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var out []Transition
	for _, t := range e.ledger {
		if t.Subject == subject {
			out = append(out, t)
		}
	}
	return out
}

// Explain renders the human-readable answer to both audit questions.
func (e *Engine) Explain(subject string, tick uint64) ([]string, error) {
	hist := e.History(subject)
	if len(hist) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrUnknownSubject, subject)
	}
	st := e.StateAt(subject, tick)
	out := []string{
		fmt.Sprintf("At tick %d, %s is %s (score %.4f, decayed to %.4f, confidence %.4f).",
			tick, subject, st.Level, st.Score, st.EffectiveScore, st.Confidence),
		fmt.Sprintf("Current reason: %s", st.Reason),
	}
	for _, t := range hist {
		if t.Tick > tick {
			continue
		}
		dir := "held"
		if t.ToScore > t.FromScore {
			dir = "raised"
		} else if t.ToScore < t.FromScore {
			dir = "lowered"
		}
		out = append(out, fmt.Sprintf("tick %d: %s %s trust %.4f -> %.4f (%s -> %s) because %q; evidence=%v",
			t.Tick, t.Kind, dir, t.FromScore, t.ToScore, t.FromLevel, t.ToLevel, t.Reason, t.EvidenceRefs))
	}
	return out, nil
}

// Ledger returns a defensive copy.
func (e *Engine) Ledger() []Transition {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]Transition, len(e.ledger))
	copy(out, e.ledger)
	return out
}

// VerifyChain independently re-derives the ledger.
func (e *Engine) VerifyChain() error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	prev := ""
	for i, t := range e.ledger {
		if t.Index != uint64(i) || t.PrevHash != prev || hashTransition(t) != t.Hash {
			return fmt.Errorf("%w at index %d", ErrChainBroken, i)
		}
		prev = t.Hash
	}
	return nil
}
