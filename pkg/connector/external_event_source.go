package connector

import (
	"context"

	"veriqo/pkg/evidence/provenance"
)

// ExternalEventSource and RiskEnrichmentSource close PART 6 of the
// "Final Audit result" external-integration mandate: optional,
// pluggable interfaces for OSINT-style event/risk providers (GDELT,
// World Monitor, and others named or unnamed) with NO hard dependency
// on any concrete one. This file defines the interfaces and the event
// shapes they exchange; it does not implement a GDELT or World Monitor
// client -- the audit doc is explicit that VERIQO must not build a
// hard dependency on World Monitor, and this package follows that to
// the letter by shipping zero concrete adapters here.
//
// Both interfaces exist to make one architectural guarantee
// mechanically checkable, not just documented: every event or signal
// they produce carries a provenance.ExternalEvidence envelope (see
// pkg/evidence/provenance), and nothing in this package -- or in any
// future adapter that implements these interfaces -- can make that
// envelope's OriginClass/RightsState anything but what
// provenance.ExternalEvidence.Validate/Permits already enforce
// fail-closed. A future GDELT or World Monitor adapter plugged in here
// is therefore structurally a SECONDARY, CORROBORATIVE source: it can
// never become primary truth merely because it is reachable, because
// nothing downstream of provenance.ExternalEvidence treats
// REAL_EXTERNAL_UNVERIFIED as anything more than that.

// ExternalEvent is one real-world event reported by an
// ExternalEventSource -- a news item, an incident report, a
// geopolitical signal. It is corroborative input to VERIQO's own
// evidence and arbitration engines (pkg/moat/fusion,
// pkg/moat/contradiction), never a substitute for them.
type ExternalEvent struct {
	EventID    string
	Provenance provenance.ExternalEvidence
	Summary    string
	Actors     []string
	Location   string
	ObservedAt uint64
}

// ExternalEventSource is implemented by an optional, pluggable event
// feed (GDELT, World Monitor, or any other OSINT provider). VERIQO's
// core has no hard dependency on any implementation of this interface
// -- callers that want event enrichment construct one and pass it in;
// nothing in this repository constructs a concrete one today.
type ExternalEventSource interface {
	// Name identifies the concrete provider, for provenance and audit
	// trails (it does NOT grant trust -- see pkg/evidence/provenance.
	// Registry for that).
	Name() string
	// FetchEvents returns events observed at or after since (a VERIQO
	// tick). Implementations must populate Provenance on every
	// returned ExternalEvent, and must not synthesize an OriginClass
	// or RightsState more permissive than provenance.
	// OriginRealExternalUnverified / RightsUnknownPendingContract
	// unless the caller has separately, explicitly authorized more
	// through pkg/evidence/provenance.Registry.
	FetchEvents(ctx context.Context, since uint64) ([]ExternalEvent, error)
}

// RiskSignal is one corroborative risk indicator derived from an
// external source -- e.g. a sanctions-adjacent news mention, an
// elevated-activity flag for a region or entity. Score is a
// corroborative weight this source suggests, never a decision:
// pkg/moat/decision and pkg/moat/fusion remain the sole authorities on
// any actual trust or risk verdict.
type RiskSignal struct {
	SignalID   string
	Provenance provenance.ExternalEvidence
	Subject    string
	Category   string
	Score      float64
	Rationale  string
}

// RiskEnrichmentSource is implemented by an optional, pluggable risk
// signal provider. Like ExternalEventSource, VERIQO's core has no hard
// dependency on any implementation, and every signal it returns is
// corroborative input, never a primary truth source.
type RiskEnrichmentSource interface {
	Name() string
	// Enrich returns risk signals about subject. Implementations must
	// populate Provenance on every returned RiskSignal under the same
	// fail-closed default this package documents for FetchEvents.
	Enrich(ctx context.Context, subject string) ([]RiskSignal, error)
}
