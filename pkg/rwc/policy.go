package rwc

import (
	"veriqo/pkg/canonical"
	"veriqo/pkg/moat/decision"
)

// VesselPortPolicy is the native decision.Policy every RWC case
// registers with the shared decision.Engine (via canonical.CaseInput.
// Policy, consumed inside pkg/canonical.Pipeline.RunCanonical). It
// consumes the ONLY factor RunCanonical ever populates —
// "tbml_composite_risk_score", the single canonical projection
// risk.ToDecisionValues emits (pkg/moat/intelligence/risk/risk.go:244).
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

// InterpretVerdict projects the evidence-derived constraint findings onto
// the PASS/FAIL/CONDITIONAL/INSUFFICIENT_EVIDENCE vocabulary the corpus
// requires. It contains no per-case branching (no vessel name, no case
// ID) — only counts already computed by EvaluateVesselAtPort.
//
// WHAT dec IS AND IS NOT USED FOR. dec is the REAL decision.Engine
// output for the same case, whose RiskScore was computed FROM the same
// ConstraintResult's ViolationRatio (see buildVesselPortCase). It is
// used here to CROSS-CHECK the verdict, and a mismatch is returned as a
// non-empty consistencyWarning so a genuine wiring bug (e.g.
// PatternScore not actually reaching the engine) surfaces instead of
// being silently absorbed by two disconnected computations that happen
// to agree. It is NOT what selects the verdict: the switch below reads
// only cr. A caller must therefore not describe a Verdict as the
// decision engine's own output. See Verdict's doc comment in types.go.
func InterpretVerdict(cr ConstraintResult, dec decision.Decision) (verdict Verdict, consistencyWarning string) {
	switch {
	case cr.Evaluated == 0:
		verdict = VerdictInsufficientEvidence
	case len(cr.HardViolations) > 0:
		verdict = VerdictFail
	case len(cr.Unresolved) > 0:
		verdict = VerdictConditional
	default:
		verdict = VerdictPass
	}

	wantAction := decision.ActionMonitor
	switch {
	case len(cr.HardViolations) > 0:
		wantAction = decision.ActionEscalate
	case len(cr.Unresolved) > 0:
		wantAction = decision.ActionFlag
	}
	if cr.Evaluated > 0 && dec.Action != wantAction {
		consistencyWarning = "native decision.Action=" + string(dec.Action) +
			" does not match the action expected from ConstraintResult (" + string(wantAction) +
			") — PatternScore/PriceAnomaly wiring should be checked"
	}
	return verdict, consistencyWarning
}

// ClassifyProvenance derives CLAIMED / CORROBORATED / UNVERIFIED /
// CONTRADICTED from the REAL outputs of pkg/moat/contradiction
// (res.Truth.Observation.Contradiction) and pkg/moat/provenance
// (res.Provenance, computed — never caller-asserted, per pkg/canonical's
// own "provenance is computed, not given" invariant), plus the raw count
// of distinct sources submitted. No new belief/trust system is
// introduced; this is a naming layer over already-computed native
// fields.
func ClassifyProvenance(res *canonical.CanonicalResult, distinctSourceCount int) ProvenanceStatus {
	if res.Truth.Observation.Contradiction {
		return StatusContradicted
	}
	if distinctSourceCount >= 2 && res.Provenance.Score > 0 {
		// Two or more distinct sources were actually submitted AND
		// provenance.Graph computed a non-zero independence score for
		// them (i.e. at least one pair shares no declared upstream).
		return StatusCorroborated
	}
	if distinctSourceCount >= 1 {
		return StatusUnverified
	}
	return StatusClaimed
}
