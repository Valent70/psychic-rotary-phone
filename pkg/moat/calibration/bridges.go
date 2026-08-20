// bridges.go wires two existing, previously-standalone subsystems into
// the calibration loop, closing two more named priorities without
// introducing a fourth parallel scorer (this repo's own DARI
// discipline, restated in pkg/kernel/intent's own package doc):
//
//   - PRIORITAS #2 (Intent -> shared Trust Calculus): intent.Verdict's
//     Observation is, by pkg/kernel/intent's own documented design, an
//     independently re-measured outcome — exactly Class C ground truth.
//     GroundTruthFromIntentVerdict turns one into a calibration.GroundTruth
//     so Intent becomes a genuine participant in the calibration loop,
//     not just a request that gets verified in isolation.
//
//   - PRIORITAS #5 (hypothesis distribution, not just point confidence):
//     PredictionFromHypotheses turns a full RankHypotheses() candidate
//     set into a Prediction with Distribution populated, so calibration
//     scores the WHOLE belief distribution (multiclass Brier) instead
//     of collapsing it to a single winner before it ever reaches this
//     package.
package calibration

import (
	"veriqo/pkg/kernel/intent"
)

// GroundTruthFromIntentVerdict adapts an intent.Verdict into a
// GroundTruth submission for the claim identified by claimKey. The
// caller supplies claimKey (the mapping from an intent Subject to a
// calibration claim is application-specific and out of scope for this
// package to guess) and the independent source ID this observation
// should be attributed to (which must still be registered via
// RegisterIndependentSource before RecordGroundTruth will accept it —
// this bridge does not bypass that check).
//
// The realized "value" is the observed action itself (decision.Action
// is a plain string type), matching the shape a Prediction's Value
// already takes when it comes from an arbitrated claim about what
// action should be taken.
func GroundTruthFromIntentVerdict(v intent.Verdict, observedActionValue string, sourceID string, claimKey string) GroundTruth {
	return GroundTruth{
		ClaimKey: claimKey,
		Value:    observedActionValue,
		SourceID: sourceID,
		Tick:     v.VerifiedAtTick,
	}
}

// HypothesisCandidate mirrors the fields of
// contradiction.TruthCandidate this bridge needs, declared locally so
// this package does not need to import pkg/moat/contradiction (which
// would risk a cycle if contradiction ever wants calibration feedback
// in the other direction — the same defensive-decoupling pattern
// pkg/evidence/graph uses with its generic Kind/Payload Node).
type HypothesisCandidate struct {
	Value      string
	Confidence float64
	Rank       int
}

// PredictionFromHypotheses builds a Prediction whose Distribution is
// the full ranked hypothesis set (ΣP(H)=1, as RankHypotheses already
// guarantees) rather than only the rank-1 winner — so a claim where
// arbitration was genuinely uncertain (e.g. 0.55 vs 0.45) gets scored
// on the whole distribution once ground truth lands, not credited or
// penalized as if it had been a confident 0.55 point call.
func PredictionFromHypotheses(claimKey string, candidates []HypothesisCandidate, arbitrationSourceIDs []string, tick uint64) Prediction {
	dist := make(map[string]float64, len(candidates))
	var top HypothesisCandidate
	for _, c := range candidates {
		dist[c.Value] = c.Confidence
		if c.Rank == 1 {
			top = c
		}
	}
	return Prediction{
		ClaimKey:             claimKey,
		Value:                top.Value,
		Confidence:           top.Confidence,
		Distribution:         dist,
		ArbitrationSourceIDs: arbitrationSourceIDs,
		Tick:                 tick,
	}
}
