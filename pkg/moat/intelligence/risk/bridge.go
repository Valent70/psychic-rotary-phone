package risk

import "veriqo/pkg/moat/calibration"

// PredictionFromResult converts a composite Result into a
// calibration.Prediction, following the same bridge idiom
// pkg/moat/calibration/bridges.go already established for intent
// verdicts and hypothesis distributions — so TBML/sanctions-evasion
// calls enter the SAME calibration ledger, and once real law-
// enforcement or port-authority ground truth becomes available
// (CRITICAL_FINDINGS' "Missing Ground Truth" gap), Brier-scoring this
// risk model is a RecordGroundTruth call away, not new plumbing.
//
// The Distribution is a two-way SUSPECTED/LEGITIMATE split derived
// from the composite score, so calibration can multiclass-score it the
// same way it already scores contradiction-engine hypothesis
// distributions.
func PredictionFromResult(claimKey string, res Result, arbitrationSourceIDs []string, tick uint64) calibration.Prediction {
	suspected := res.Score
	legitimate := 1 - res.Score
	value := "LEGITIMATE"
	if res.Score >= 0.5 {
		value = "SUSPECTED"
	}
	return calibration.Prediction{
		ClaimKey:   claimKey,
		Value:      value,
		Confidence: max(suspected, legitimate),
		Distribution: map[string]float64{
			"SUSPECTED":  suspected,
			"LEGITIMATE": legitimate,
		},
		ArbitrationSourceIDs: arbitrationSourceIDs,
		Tick:                 tick,
	}
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
