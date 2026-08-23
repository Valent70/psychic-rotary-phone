package rwc

import (
	"veriqo/pkg/canonical"
	"veriqo/pkg/moat/decision"
	"veriqo/pkg/moat/provenance"
)

// VesselPortPolicy is the native decision.Policy every RWC case
// registers with the shared decision.Engine (via canonical.CaseInput.
// Policy, consumed inside pkg/canonical.Pipeline.RunCanonical). It
// consumes the ONLY factor RunCanonical ever populates —
// "tbml_composite_risk_score", the single canonical projection
// risk.ToDecisionValues emits (pkg/moat/intelligence/risk/risk.go:242).
// RunCanonical is hard-wired to compute exactly this factor and no
// other, so any Policy used on this path must declare it under this
// exact name to be consulted at all.
//
// Thresholds are calibrated against ConstraintResult.ViolationRatio's
// three fixed bands (0 / 0.5 / 1.0), not against any specific case, and
// they are checked against the REAL risk.Model.Score formula on this
// branch rather than assumed:
//
//	composite = clamp01((0.6*pattern + 0.4*price) * (0.5 + 0.5*independence))
//
// (pkg/moat/intelligence/risk/risk.go, Model.Score). RWC sets both
// PatternScore and PriceAnomaly to the same ViolationRatio, so the
// weighted term collapses to exactly ViolationRatio. For a single-source
// case pkg/moat/provenance.ComputeIndependence returns Ratio=1.0 by
// construction (len(uniq) <= 1), making the discount exactly 1.0, so
// composite == ViolationRatio. TestPolicyThresholdsMatchTheRealRiskModel
// asserts this against the real model instead of trusting this comment.
var VesselPortPolicy = decision.Policy{
	Name: "rwc001_vessel_port_suitability_v1",
	Factors: []decision.FactorWeight{
		{Name: "tbml_composite_risk_score", Weight: 1.0},
	},
	FlagThreshold:     0.3, // crosses at ViolationRatio=0.5 (unresolved-only)
	EscalateThreshold: 0.7, // crosses at ViolationRatio=1.0 (hard violation)
}

// InterpretVerdict was REMOVED in round R24 (canonical-truth-path
// mandate, WAVE A item 1 / section II). It projected the constraint
// findings onto PASS/FAIL/CONDITIONAL using only `cr`, which meant the
// adapter could produce a final verdict with no native decision at all
// — the exact regression the mandate names. Its two replacements live
// in verdict.go and split the two jobs it had conflated:
//
//	InterpretNativeDecision(dec, evidenceCount, policy) -> Verdict
//	ConstraintCrossCheck(cr, dec)                       -> warning
//
// It is deliberately not kept as a deprecated shim. A shim would leave
// a compiling call site that still bypasses the native engine, and the
// whole point of this item is that no such call site can exist.

// ClassifyProvenance derives the claim/corroboration status from the REAL
// outputs of pkg/moat/contradiction (res.Truth.Observation.Contradiction)
// and pkg/moat/provenance (res.Provenance — computed, never
// caller-asserted, per pkg/canonical's own "provenance is computed, not
// given" invariant), together with the epistemic kind of each source that
// was actually submitted.
//
// CORRECTED IN ROUND R23 by the independent audit. The previous rule was:
//
//	if distinctSourceCount >= 2 && res.Provenance.Score > 0 {
//	        return StatusCorroborated
//	}
//
// It was wrong twice over, and both errors pointed the same way — toward
// overclaiming.
//
//  1. It counted sources without asking what a source IS. RWC-002's
//     second "source" for vessel identity is an IMO check digit computed
//     from the claimed IMO number. Two submissions were present, so the
//     claim was labelled CORROBORATED, when in fact nothing outside the
//     claim had been consulted at all.
//
//  2. It read Score without reading Status. In every RWC v2 case the real
//     assessment is Status=UNKNOWN with Score=1.0, because no source
//     declares any upstream and pkg/moat/provenance.ComputeIndependence
//     returns a trivial 1.0 when there is nothing to compare. That
//     package's own documentation exists precisely to stop a consumer
//     confusing "we checked and found no shared origin"
//     (DECLARED_INDEPENDENT) with "we never had data to check" (UNKNOWN).
//     The old rule confused exactly those two.
//
// The corrected rule requires an independent OBSERVATION, and requires
// the native assessment to have actually reached DECLARED_INDEPENDENT,
// before it will say CORROBORATED.
func ClassifyProvenance(res *canonical.CanonicalResult, subs []canonical.SourceSubmission) ProvenanceStatus {
	if res.Truth.Observation.Contradiction {
		return StatusContradicted
	}

	seen := map[string]bool{}
	observations, derived := 0, 0
	for _, s := range subs {
		id := string(s.SourceID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		switch EpistemicKindOf(id) {
		case SourceDerivedFromClaim:
			derived++
		default:
			observations++
		}
	}

	switch {
	case observations >= 2 && res.Provenance.Status == provenance.StatusDeclaredIndependent:
		return StatusCorroborated
	case observations >= 1 && derived >= 1:
		return StatusStructurallyValidated
	case observations >= 1 || derived >= 1:
		return StatusUnverified
	default:
		return StatusClaimed
	}
}
