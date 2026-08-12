// hbayes_bridge.go closes PRIORITAS #4 in
// "Gap_yang_sekarang_menjadi_PRIORITAS.docx": "Dependency-aware
// confidence" — the critique's example is exact: three sources at 0.9
// each multiply to a very high combined confidence, but if all three
// are actually resellers of the same upstream feed, that is one
// observation counted three times, not three independent ones.
//
// This repo already has TWO separate answers to that problem, built in
// different sessions, and neither called the other:
//
//   - fuseGroup (fusion.go) — a fast heuristic: cluster evidence by
//     Evidence.Provider, keep only the max-reliability item per
//     provider, combine clusters via noisy-OR. Always used by the
//     default Arbitrate() path — deliberately kept as the default so
//     this bridge changes no prior behavior.
//   - hbayes.Model (pkg/moat/fusion/hbayes) — a real 3-level
//     hierarchical Bayesian correlation model with an explicit
//     correlation MATRIX (not just same-provider/different-provider
//     binary clustering) and posterior reliability updates. Verified
//     via `grep -rl "moat/fusion/hbayes"` to have had zero callers
//     anywhere in the repo outside its own package before this file —
//     the exact "built but never wired" pattern the IVF gap closure
//     found and fixed in an earlier session for pkg/verification.
//
// ComputeHierarchicalRisk below is the wire: an explicit, opt-in method
// on Engine that runs hbayes' real correlation-matrix math over an
// Engine's live evidence for one claim/hypothesis. It is additive, not
// a replacement for fuseGroup — a caller who has done the work of
// building a CorrelationMatrix (e.g. "these three AIS resellers share
// 0.8 pairwise correlation because they buy from the same satellite
// operator") gets the more rigorous, dependency-aware answer; a caller
// who has not gets the existing fast default unchanged.
package fusion

import "veriqo/pkg/moat/fusion/hbayes"

// GroupOf maps a fusion SourceID to the hbayes provider group it
// belongs to, so callers can reuse whatever grouping (e.g. by
// SourceProfile.Provider) they already maintain.
type GroupOf func(SourceID) hbayes.ProviderGroupID

// ComputeHierarchicalRisk evaluates the correlation-aware probability
// that hypothesisValue is the true value for claim, using the current
// live evidence set. Each piece of evidence becomes one hbayes.Signal
// (Confirms=true iff it asserts hypothesisValue); model supplies the
// Level-2 priors and correlation matrix; intraGroupCorrelation supplies
// the within-group redundancy for each represented provider group
// (0 = signals within the group are independent, 1 = fully redundant —
// the caller's own belief about e.g. "how correlated are two AIS
// vendors reselling the same feed").
//
// Read-only: does not mutate the engine's evidence log, arbitration
// log, or knowledge-graph sink — safe to call speculatively (e.g. "what
// would the correlation-aware risk be if we also had a 4th signal")
// without an Arbitrate() call alongside it.
func (e *Engine) ComputeHierarchicalRisk(claim Claim, hypothesisValue string, model *hbayes.Model, group GroupOf, intraGroupCorrelation map[hbayes.ProviderGroupID]float64) (hbayes.EventRiskResult, error) {
	e.mu.RLock()
	all := e.evidence[claim.Key()]
	snapshot := make([]Evidence, len(all))
	copy(snapshot, all)
	e.mu.RUnlock()

	signals := make([]hbayes.Signal, 0, len(snapshot))
	for _, ev := range snapshot {
		signals = append(signals, hbayes.Signal{
			SourceID:    string(ev.Source),
			Group:       group(ev.Source),
			Confirms:    ev.Value == hypothesisValue,
			Reliability: ev.Reliability,
		})
	}
	return model.ComputeEventRisk(signals, intraGroupCorrelation)
}
