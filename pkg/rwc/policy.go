package rwc

import (
	"veriqo/pkg/canonical"
	"veriqo/pkg/moat/decision"
)

// VesselPortPolicy is the native decision.Policy RWC-001 registers with
// the shared decision.Engine (via canonical.CaseInput.Policy, consumed
// inside pkg/canonical.RunCanonical). It consumes the ONLY factor
// RunCanonical ever populates — "tbml_composite_risk_score", the single
// canonical projection risk.ToDecisionValues emits (see
// pkg/moat/intelligence/risk.ToDecisionValues's doc comment — RunCanonical
// is hard-wired to compute exactly this factor and no other, so any
// Policy used on this path must declare it under this exact name to be
// consulted at all).
//
// Thresholds are calibrated against ConstraintResult.ViolationRatio's
// three fixed bands (0 / 0.5 / 1.0), not against any specific case, and
// verified empirically against the real risk.Model.Score formula: with a
// single declaring source (RWC-001's norm), pkg/moat/provenance reports
// trivial full independence (Ratio=1.0 for len(sourceIDs)<=1), so
// risk.Model.Score's discount term is 1.0 and its composite reduces to
// exactly PatternScore*0.6 + PriceAnomaly*0.4. RWC sets both
// PatternScore and PriceAnomaly to the same ViolationRatio (documented
// in run.go), so composite == ViolationRatio exactly for the
// single-source case. Thresholds below are therefore chosen directly
// against the 0 / 0.5 / 1.0 band boundaries.
var VesselPortPolicy = decision.Policy{
	Name: "rwc001_vessel_port_suitability_v1",
	Factors: []decision.FactorWeight{
		{Name: "tbml_composite_risk_score", Weight: 1.0},
	},
	FlagThreshold:     0.3, // crosses at ViolationRatio=0.5 (unresolved-only)
	EscalateThreshold: 0.7, // crosses at ViolationRatio=1.0 (hard violation)
}

// InterpretVerdict is the smallest-necessary RWC-domain adapter the
// baseline doc's §7/G1 calls for: a deterministic projection of the
// evidence-derived constraint findings onto the
// PASS/FAIL/CONDITIONAL/INSUFFICIENT_EVIDENCE vocabulary the brief
// requires. It contains no per-case branching (no vessel name, no case
// ID) — only counts already computed by EvaluateVesselAtPort.
//
// dec is the REAL decision.Engine output for the same case (its
// RiskScore was computed FROM the same ConstraintResult's
// ViolationRatio — see run001.go) and is used here only to cross-check
// consistency, never to decide the verdict a second, independent way:
// mismatch is returned as a non-empty warning so a genuine wiring bug
// (e.g. PatternScore not actually reaching the engine) surfaces instead
// of being silently absorbed by two disconnected computations that
// happen to agree by coincidence.
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

// ClassifyProvenance is the RWC-002 provenance-status adapter the
// baseline doc's §5/G3 calls for. It derives CLAIMED / CORROBORATED /
// UNVERIFIED / CONTRADICTED purely from the REAL outputs of
// pkg/moat/contradiction (res.Truth.Contradiction) and
// pkg/moat/provenance (res.Provenance, computed independence — never
// caller-asserted, per pkg/canonical's own "provenance dihitung, bukan
// diberikan" invariant), plus the raw count of distinct sources
// submitted. No new belief/trust system is introduced; this is a naming
// layer over already-computed native fields.
func ClassifyProvenance(res *canonical.CanonicalResult, distinctSourceCount int) ProvenanceStatus {
	if res.Truth.Observation.Contradiction {
		return StatusContradicted
	}
	if distinctSourceCount >= 2 && res.Provenance.Score > 0 {
		// Two or more distinct-provider sources were actually submitted
		// AND provenance.Graph computed a non-zero independence score
		// for them (i.e. at least one pair shares no declared upstream)
		// — a genuine independent check occurred and did not conflict.
		return StatusCorroborated
	}
	if distinctSourceCount >= 1 {
		return StatusUnverified
	}
	return StatusClaimed
}
