// causal_bridge.go closes a gap the V7.10.1 technical audit named
// explicitly: Digital Twin and pkg/moat/causal existed as two separate
// islands — a causal Why()/WhatIf() analysis could explain an outcome,
// but nothing carried that explanation into the twin's own future-state
// projection, so "Evidence Graph -> Truth -> Causal Graph -> Digital
// Twin -> What-if -> Future State" was not one connected path.
//
// The bridge is intentionally thin and one-directional (Causal ->
// Twin, read-only from the twin's perspective): pkg/moat/causal stays
// the single source of causal analysis; digitaltwin only records what
// it was told and lets Simulate consume it. No new causal-inference
// logic is duplicated here.
package digitaltwin

// CausalState is a twin's latest recorded causal-support signal for
// one policy — how strongly the causal graph's accumulated evidence
// supports the outcome this twin's risk projection is being driven
// toward, per pkg/moat/causal.Graph.AggregateSupport /
// pkg/moat/causal.Graph.WhatIf.
type CausalState struct {
	PolicyName    string
	Support       float64 // [0,1], caller-supplied from causal.Graph
	UpdatedAtTick uint64
}

// ApplyCausalProjection folds a causal-support score (typically
// causal.Graph.AggregateSupport(effectNode) or the "after" value from
// causal.Graph.WhatIf) into the named entity's twin, under the given
// policy name. support is expected in [0,1] (causal.Graph's own
// convention); values outside that range are clamped rather than
// rejected, since a defensively-clamped signal is safer for a
// downstream projection than a hard failure on an upstream engine's
// edge case.
func (r *Registry) ApplyCausalProjection(actorID string, entity EntityID, policyName string, support float64, tick uint64) error {
	if support < 0 {
		support = 0
	}
	if support > 1 {
		support = 1
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	rec := UpdateRecord{
		Index: uint64(len(r.log)), ActorID: actorID, Entity: entity, Kind: UpdateCausal,
		Key: policyName, CausalSupport: support, Tick: tick, PrevHash: r.head,
	}
	rec.Hash = hashUpdate(rec)
	r.applyRecord(rec)
	return nil
}

// CausalAdjustedTarget blends a base target risk score with the
// twin's recorded causal support for that policy: the more strongly
// the causal graph supports the adverse outcome, the closer the
// adjusted target moves toward 1.0 (maximum risk); with zero causal
// support recorded, the base target is returned unchanged. This is a
// deliberately simple, explainable linear blend (not a learned model),
// consistent with the rest of the digitaltwin package's discipline —
// see the package comment in simulate.go.
//
//	adjusted = base + support*(1-base)
//
// which is the same "close a fraction of the remaining gap" shape
// Simulate itself already uses for its own tick-by-tick approach, kept
// consistent on purpose.
func CausalAdjustedTarget(baseTarget float64, causal CausalState) float64 {
	if baseTarget < 0 {
		baseTarget = 0
	}
	if baseTarget > 1 {
		baseTarget = 1
	}
	return baseTarget + causal.Support*(1-baseTarget)
}

// SimulateWithCausalSupport is Simulate, but with TargetRiskScore
// first adjusted by the twin's recorded CausalState for policyName
// (via CausalAdjustedTarget) — the concrete "Causal Graph -> Digital
// Twin -> What-if -> Future State" wiring the audit asked for. If no
// causal projection has been recorded for this policy yet, behaves
// identically to plain Simulate (zero support -> target unchanged).
func SimulateWithCausalSupport(twin Twin, policyName string, params SimulationParams) []ProjectedPoint {
	if cs, ok := twin.Causal[policyName]; ok {
		params.TargetRiskScore = CausalAdjustedTarget(params.TargetRiskScore, cs)
	}
	return Simulate(twin, policyName, params)
}
