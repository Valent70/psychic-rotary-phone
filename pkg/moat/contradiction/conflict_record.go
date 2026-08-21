package contradiction

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

// ConflictRecord closes PART 5 of the "Final Audit result" audit: the
// exact, literal output shape the mandate names for integrating
// external evidence (its own worked example: "AIS says arrival=T1,
// NOR says arrival=T2, SOF says arrival=T3 -- the evidence engine
// must preserve all three, do not silently resolve conflicts").
//
// This is a thin, additive projection over the ArbitrationEngine that
// already exists in this package (RankHypotheses/ArbitrateClaim/
// Observations) -- it computes nothing new about WHICH value is most
// credible, it only names the existing computation's inputs and
// outputs in the mandate's own vocabulary, and it never discards a
// disagreeing source: Sources/Values below always list EVERY source
// that reported a value for the claim, winning or not, exactly as
// Observations() (used to build this) already preserves them.
type ConflictRecord struct {
	ConflictID string `json:"conflict_id"`
	ClaimKey   string `json:"claim_key"`
	// Sources and Values are parallel arrays: Sources[i] asserted
	// Values[i]. Every source with a pending observation for this
	// claim appears here, agreeing or not -- this is the literal
	// "preserve all three" requirement, mechanically enforced (see
	// TestConflictRecordPreservesEverySourceEvenTheLosingOnes).
	Sources []string `json:"sources"`
	Values  []string `json:"values"`
	// TimeDelta is the tick spread between the earliest and latest
	// observation backing this record (max Tick - min Tick).
	TimeDelta uint64 `json:"time_delta"`

	ResolutionState     ResolutionState `json:"resolution_state"`
	Confidence          float64         `json:"confidence"`
	HumanReviewRequired bool            `json:"human_review_required"`
}

// ResolutionState is how (or whether) a claim's disagreement was
// resolved -- never silently, always visibly one of these three.
type ResolutionState string

const (
	// ResolutionUncontested: every source agreed; nothing to resolve.
	ResolutionUncontested ResolutionState = "UNCONTESTED"
	// ResolutionArbitrated: sources disagreed, but arbitration reached
	// a high-confidence winner (>= humanReviewConfidenceThreshold).
	// The disagreement is NOT hidden -- Sources/Values still list every
	// source, this state only says a confident winner was computed.
	ResolutionArbitrated ResolutionState = "ARBITRATED"
	// ResolutionContested: sources disagreed and arbitration's
	// confidence in the winner is below threshold, or the top two
	// candidates are tied -- HumanReviewRequired is always true here.
	ResolutionContested ResolutionState = "CONTESTED"
)

// humanReviewConfidenceThreshold: an arbitrated winner with LESS
// confidence than this is not confident enough to resolve without a
// human -- chosen as a clear majority (two-thirds), not a bare
// plurality, matching this repo's existing high bar for automatic
// truth decisions elsewhere (e.g. decision.Policy thresholds).
const humanReviewConfidenceThreshold = 2.0 / 3.0

// ConflictRecords is Stage 4/5's audit-facing report for one claim: it
// calls the SAME RankHypotheses/detectConflicts computation
// ArbitrateClaim already performs, and projects the result into PART
// 5's exact required fields. Fails closed (returns an error) when no
// observations are pending, exactly like RankHypotheses.
func (a *ArbitrationEngine) ConflictRecords(claimKey string, arbitrationTick uint64, halfLifeTicks uint64) (ConflictRecord, error) {
	raw := a.Observations(claimKey)
	if len(raw) == 0 {
		return ConflictRecord{}, fmt.Errorf("contradiction: no observations pending for claim %q", claimKey)
	}

	evidence := make([]Evidence, 0, len(raw))
	minTick, maxTick := raw[0].Tick, raw[0].Tick
	for _, o := range raw {
		evidence = append(evidence, computeEvidence(o, arbitrationTick, halfLifeTicks))
		if o.Tick < minTick {
			minTick = o.Tick
		}
		if o.Tick > maxTick {
			maxTick = o.Tick
		}
	}
	conflicts := detectConflicts(evidence)

	candidates, err := a.RankHypotheses(claimKey, arbitrationTick, halfLifeTicks)
	if err != nil {
		return ConflictRecord{}, err
	}

	// Preserve EVERY source/value pair, in a deterministic order
	// (sorted by SourceID) -- never just the winner's.
	type sv struct{ source, value string }
	pairs := make([]sv, 0, len(raw))
	for _, o := range raw {
		pairs = append(pairs, sv{o.SourceID, o.Value})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].source < pairs[j].source })
	sources := make([]string, len(pairs))
	values := make([]string, len(pairs))
	for i, p := range pairs {
		sources[i], values[i] = p.source, p.value
	}

	topConfidence := 0.0
	tie := false
	if len(candidates) > 0 {
		topConfidence = candidates[0].Confidence
		if len(candidates) > 1 && candidates[0].Score == candidates[1].Score {
			tie = true
		}
	}

	state := ResolutionUncontested
	humanReview := false
	if len(conflicts) > 0 {
		if tie || topConfidence < humanReviewConfidenceThreshold {
			state = ResolutionContested
			humanReview = true
		} else {
			state = ResolutionArbitrated
		}
	}

	timeDelta := uint64(0)
	if maxTick > minTick {
		timeDelta = maxTick - minTick
	}

	id := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%v|%v", claimKey, arbitrationTick, sources, values)))

	return ConflictRecord{
		ConflictID: hex.EncodeToString(id[:]), ClaimKey: claimKey,
		Sources: sources, Values: values, TimeDelta: timeDelta,
		ResolutionState: state, Confidence: topConfidence, HumanReviewRequired: humanReview,
	}, nil
}
