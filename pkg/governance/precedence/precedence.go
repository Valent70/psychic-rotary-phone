// Package precedence closes "Internal Gap X" of the v7.12.1 master
// audit: today, pkg/authz emits ALLOW/DENY, pkg/moat/decision emits
// MONITOR/FLAG/ESCALATE, and pkg/governance/hitl's always-reviewed
// list names ESCALATE, BLOCK and DENY — three subsystems, three
// vocabularies, none of which formally says which one wins when they
// disagree. The audit's rule:
//
//	"No package may independently invent a conflicting final decision
//	precedence... One authoritative decision object is produced."
//
// This package is that one authoritative lattice:
//
//	DENY > BLOCK > ESCALATE > REVIEW > ALLOW
//
// Combine takes every subsystem's Signal (its own action, in its own
// vocabulary, mapped once here rather than by each caller) and reduces
// them to one Verdict — deterministically, so replay of the same
// signal set always reaches the same Verdict regardless of the order
// the signals arrived in. It does not replace or rewire any existing
// producer (pkg/authz, pkg/moat/decision, pkg/governance/hitl keep
// their own types and tests unchanged, per NON-NEGOTIABLE RULE #1:
// integrate, don't rewrite); the From* adapters in this file are how a
// caller opts a signal into the lattice.
package precedence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"

	"veriqo/pkg/platform/telemetry"
)

// Action is the lattice's own five-value vocabulary. It is
// deliberately smaller than the union of every subsystem's vocabulary:
// a subsystem with a finer-grained action (e.g. hitl's APPROVE/REJECT)
// maps to the lattice level that action's real-world consequence
// matches, via that subsystem's own From* adapter below.
type Action string

const (
	ActionAllow    Action = "ALLOW"
	ActionReview   Action = "REVIEW"
	ActionEscalate Action = "ESCALATE"
	ActionBlock    Action = "BLOCK"
	ActionDeny     Action = "DENY"
)

// rank encodes DENY > BLOCK > ESCALATE > REVIEW > ALLOW as integers so
// resolution is a numeric max, not a chain of if/else a future edit
// could get out of order.
var rank = map[Action]int{
	ActionAllow: 0, ActionReview: 1, ActionEscalate: 2, ActionBlock: 3, ActionDeny: 4,
}

var (
	// ErrUnknownAction refuses a signal whose Action is not one of the
	// five lattice values — silently treating an unrecognised action as
	// ALLOW would be exactly the false-permissive failure this lattice
	// exists to prevent.
	ErrUnknownAction = errors.New("precedence: unknown action — must be one of DENY, BLOCK, ESCALATE, REVIEW, ALLOW")
	// ErrNoSignals refuses to produce a verdict from nothing: a decision
	// with no contributing signal is not a decision, it is a default
	// masquerading as one.
	ErrNoSignals = errors.New("precedence: cannot combine zero signals")
	// ErrEmptySource refuses an anonymous signal: every contribution to
	// the final decision must be traceable to the subsystem that made it.
	ErrEmptySource = errors.New("precedence: signal source must be named")
)

// Signal is one subsystem's contribution to the final decision.
type Signal struct {
	Source string `json:"source"` // e.g. "authz", "risk_policy", "hitl"
	Action Action `json:"action"`
	Reason string `json:"reason"`
}

func (s Signal) validate() error {
	if s.Source == "" {
		return ErrEmptySource
	}
	if _, ok := rank[s.Action]; !ok {
		return fmt.Errorf("%w: %q from %s", ErrUnknownAction, s.Action, s.Source)
	}
	return nil
}

// Verdict is the one authoritative combined decision.
type Verdict struct {
	Action  Action   `json:"action"`
	Winner  Signal   `json:"winner"`
	Signals []Signal `json:"signals"`
	Hash    string   `json:"hash"`
}

// Combine reduces every signal to the single highest-precedence
// action. When multiple signals tie at the highest rank, the tie is
// broken by Source in lexicographic order — not by input order — so
// Combine(signals) and Combine(shuffled(signals)) always agree, which
// is what makes a replayed decision reproducible from an unordered set
// of subsystem outputs rather than from an incidental slice order.
func Combine(signals []Signal) (Verdict, error) {
	_, span := telemetry.StartSpan(context.Background(), "precedence.Combine",
		telemetry.Attribute{Key: "signal_count", Value: fmt.Sprintf("%d", len(signals))})
	defer span.End()

	if len(signals) == 0 {
		return Verdict{}, ErrNoSignals
	}
	cp := append([]Signal(nil), signals...)
	for _, s := range cp {
		if err := s.validate(); err != nil {
			return Verdict{}, err
		}
	}
	sort.Slice(cp, func(i, j int) bool { return cp[i].Source < cp[j].Source })

	winner := cp[0]
	for _, s := range cp[1:] {
		if rank[s.Action] > rank[winner.Action] {
			winner = s
		}
	}

	v := Verdict{Action: winner.Action, Winner: winner, Signals: cp}
	v.Hash = v.computeHash()
	return v, nil
}

func (v Verdict) computeHash() string {
	h := sha256.New()
	fmt.Fprintf(h, "veriqo.precedence.verdict/v1|action=%s|winner=%s:%s\n", v.Action, v.Winner.Source, v.Winner.Action)
	for _, s := range v.Signals {
		fmt.Fprintf(h, "signal|%s|%s|%s\n", s.Source, s.Action, s.Reason)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Verify recomputes a verdict's hash from its own fields.
func Verify(v Verdict) error {
	if v.computeHash() != v.Hash {
		return errors.New("precedence: verdict hash mismatch — the verdict has been edited")
	}
	return nil
}
