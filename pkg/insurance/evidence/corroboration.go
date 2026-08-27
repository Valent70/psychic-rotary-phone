package evidence

import (
	"sort"

	"veriqo/pkg/evidence/provenance"
)

// This file answers the Round 4 work order's §28 requirement for an
// evidence independence/corroboration classification carrying exactly
// the vocabulary it names: UNKNOWN / CORROBORATED / CONTRADICTED /
// SUPERSEDED / REVOKED.
//
// It adds no new stored field and no second lifecycle engine. Every one
// of those five facts is already tracked somewhere in this package or
// in pkg/evidence/provenance — Record.Status already carries §8's own
// CORROBORATED/CONTRADICTED verification outcomes (set by
// pkg/insurance/contradiction's real ArbitrationEngine adapter, never
// asserted directly here); Record.CorrectionSuperseded/SupersededBy
// already record supersession (spec §72); Record.Rights already reuses
// provenance.RightsState, whose own REVOKED value is the canonical
// revocation fact. Giving each of those a second, independently-set
// field under a new name would be exactly the "one gap, two names"
// drift Round 4's canonical-taxonomy requirement (§2) forbids —
// CorroborationStatus is instead a pure, derived VIEW over the
// existing signals, computed fresh every call, never itself settable.

// CorroborationStatus is the closed five-value vocabulary this axis
// reports in.
type CorroborationStatus string

const (
	// CorroborationUnknown is the honest default: this record has not
	// been independently corroborated, has not been found to conflict
	// with anything, has not been superseded, and has not been revoked.
	// Deliberately distinct from StatusUnverified, which answers a
	// different question (has authenticity been checked) — a record can
	// be StatusAuthenticitySupported and still CorroborationUnknown if
	// no second independent source has ever spoken to the same fact.
	CorroborationUnknown CorroborationStatus = "UNKNOWN"
	// CorroborationCorroborated mirrors Record.Status ==
	// StatusCorroborated: a real second, independent source agrees,
	// exactly as pkg/insurance/contradiction's ArbitrationEngine adapter
	// determined it — never re-decided here.
	CorroborationCorroborated CorroborationStatus = "CORROBORATED"
	// CorroborationContradicted mirrors Record.Status ==
	// StatusContradicted for the same reason.
	CorroborationContradicted CorroborationStatus = "CONTRADICTED"
	// CorroborationSuperseded mirrors Record.CorrectionSuperseded: a
	// later record replaced this one (spec §72's "a future correction
	// cannot rewrite historical truth" — the original stays readable,
	// just no longer the standing fact).
	CorroborationSuperseded CorroborationStatus = "SUPERSEDED"
	// CorroborationRevoked mirrors Record.Rights ==
	// provenance.RightsRevoked: VERIQO's own permission to use this
	// record has been withdrawn, the same REVOKED fact
	// provenance.RightsState already tracks everywhere else in the
	// codebase.
	CorroborationRevoked CorroborationStatus = "REVOKED"
)

var knownCorroborationStatuses = map[CorroborationStatus]bool{
	CorroborationUnknown: true, CorroborationCorroborated: true,
	CorroborationContradicted: true, CorroborationSuperseded: true,
	CorroborationRevoked: true,
}

// IsKnownCorroborationStatus reports whether s is one of the five
// modelled values.
func IsKnownCorroborationStatus(s CorroborationStatus) bool { return knownCorroborationStatuses[s] }

// KnownCorroborationStatuses returns all five values in deterministic
// order.
func KnownCorroborationStatuses() []CorroborationStatus {
	out := make([]CorroborationStatus, 0, len(knownCorroborationStatuses))
	for s := range knownCorroborationStatuses {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Corroboration derives r's corroboration status from its existing
// fields. Precedence, most authoritative first:
//
//  1. REVOKED — VERIQO's own right to use this record has been
//     withdrawn; whatever the content says no longer matters for this
//     axis.
//  2. SUPERSEDED — a later record replaced this one; same reasoning.
//  3. CONTRADICTED — the real arbitration engine found a conflicting
//     independent source.
//  4. CORROBORATED — the real arbitration engine found an agreeing
//     independent source.
//  5. UNKNOWN — none of the above has happened (yet).
//
// This is a pure function of r's own fields: calling it twice on an
// unchanged Record always returns the same answer, and nothing in this
// package can set it directly.
func (r Record) Corroboration() CorroborationStatus {
	switch {
	case r.Rights == provenance.RightsRevoked:
		return CorroborationRevoked
	case r.CorrectionSuperseded:
		return CorroborationSuperseded
	case r.Status == StatusContradicted:
		return CorroborationContradicted
	case r.Status == StatusCorroborated:
		return CorroborationCorroborated
	default:
		return CorroborationUnknown
	}
}
