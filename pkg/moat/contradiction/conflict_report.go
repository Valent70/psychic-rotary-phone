// conflict_report.go closes the second audit document's item 4 ("Evidence
// Arbitration"): its exact required JSON output shape --
//
//	{
//	  "event": "arrival",
//	  "sources": [...],
//	  "conflict": true,
//	  "conflict_type": "TIMESTAMP_DISAGREEMENT",
//	  "resolution": "...",
//	  "confidence": 0.91,
//	  "human_review_required": false
//	}
//
// -- derived purely from a real, already-computed TruthVersion (A5's own
// Reason/WinningAuthority work), never a second decision. This package's
// ArbitrateClaim remains the one place a winner is actually chosen;
// BuildConflictReport only projects that decision into the audit's own
// named vocabulary, the same "adapter interprets, never re-decides"
// discipline pkg/rwc's InterpretVerdict already established.
package contradiction

// ConflictReport is the audit's exact required per-claim output shape.
type ConflictReport struct {
	Event               string   `json:"event"`
	Sources             []string `json:"sources"`
	Conflict            bool     `json:"conflict"`
	ConflictType        string   `json:"conflict_type,omitempty"`
	Resolution          string   `json:"resolution"`
	Confidence          float64  `json:"confidence"`
	HumanReviewRequired bool     `json:"human_review_required"`
}

// humanReviewConfidenceFloor is the confidence below which an otherwise
// automatically-resolved conflict is still flagged for human review.
// Deliberately conservative (0.75, not e.g. 0.5): a conflict this
// package already detected real disagreeing evidence for, so the bar
// for "the automated resolution is trustworthy enough to skip a human"
// is higher than an ordinary single-source confidence score would be.
const humanReviewConfidenceFloor = 0.75

// BuildConflictReport projects a real TruthVersion (produced by
// ArbitrateClaim) into the audit's ConflictReport shape. event is a
// caller-supplied label for what this claim represents (e.g. "arrival")
// -- this package's own Observation/TruthVersion types are intentionally
// domain-agnostic (ClaimKey is a free-form string), so the human-facing
// event name is not something arbitration itself can derive; the caller
// who constructed the claim (e.g. pkg/rwc, pkg/maritime) already knows
// it.
func BuildConflictReport(event string, tv TruthVersion) ConflictReport {
	obs := tv.ToObservation()
	sources := make([]string, 0, len(obs.WinningSources)+len(obs.LosingSources))
	sources = append(sources, obs.WinningSources...)
	sources = append(sources, obs.LosingSources...)

	conflict := len(tv.ConflictingEdges) > 0
	var conflictType string
	if conflict {
		conflictType = classifyConflictType(tv.ConflictingEdges)
	}

	humanReview := conflict && tv.Confidence < humanReviewConfidenceFloor

	return ConflictReport{
		Event: event, Sources: sources, Conflict: conflict,
		ConflictType: conflictType, Resolution: tv.Value,
		Confidence: tv.Confidence, HumanReviewRequired: humanReview,
	}
}

// classifyConflictType distinguishes a timestamp/tick disagreement (both
// disputed values parse as unsigned integers -- this repo's own logical-
// tick convention, see internal/determinism's package doc) from a
// general value disagreement. This is a real, checkable classification
// over the actual conflicting values, not a guess: it only ever claims
// TIMESTAMP_DISAGREEMENT when every edge's both sides genuinely parse as
// ticks.
func classifyConflictType(edges []ConflictEdge) string {
	for _, e := range edges {
		if !looksLikeTick(e.ValueA) || !looksLikeTick(e.ValueB) {
			return "VALUE_DISAGREEMENT"
		}
	}
	return "TIMESTAMP_DISAGREEMENT"
}

func looksLikeTick(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
