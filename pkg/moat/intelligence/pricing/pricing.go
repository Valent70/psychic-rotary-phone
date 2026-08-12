// Package pricing implements commodity price-anomaly detection — one
// concrete piece of CRITICAL_FINDINGS.docx's "Missing Commodity
// Intelligence" category, specifically "Price anomaly detection" as an
// input to trade-based money-laundering (TBML) risk scoring (a
// documented TBML technique is over/under-invoicing a shipment's
// declared value against fair market price to move value across a
// border disguised as trade).
//
// Honest scope: this package implements the statistical detection
// *algorithm* — robust to outliers, deterministic, zero external
// dependencies — over a reference price series the caller supplies. It
// does NOT source live commodity market data (no market-data feed is
// reachable from this environment); a real deployment would feed
// ReferenceSeries from a commodity price index subscription. The
// algorithm itself is real and independently testable with any
// numeric series, synthetic or real.
package pricing

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

var (
	ErrEmptyReference               = errors.New("pricing: reference series must be non-empty")
	ErrInvalidObservedPrice         = errors.New("pricing: observed price is NaN, +/-Inf, or negative")
	ErrInvalidReferencePrice        = errors.New("pricing: reference series contains NaN, +/-Inf, or negative value")
	ErrInvalidDetectorConfiguration = errors.New("pricing: detector threshold must be finite and positive")
)

// DataQuality states how much a price-anomaly result's input should be
// trusted, independent of the computed score — closes v7.9 audit P1
// "Data quality state" and §10's constant-reference-series problem: a
// mathematically-valid +Inf z-score from a degenerate reference series
// is a DATA problem, not automatic maximum confidence.
type DataQuality string

const (
	// DataQualityValid: reference series has enough distinct points
	// and passed all checks.
	DataQualityValid DataQuality = "VALID"
	// DataQualityInsufficient: fewer than minSeriesForConfidence points
	// — too little data for a robust statistic to be meaningful.
	DataQualityInsufficient DataQuality = "INSUFFICIENT"
	// DataQualityDegraded: the reference series is constant or
	// near-constant (MAD == 0 or IQR == 0) — a known data-quality
	// symptom (stale/broken feed), not genuine distributional
	// information; scores computed from it should be treated as LOW
	// confidence even when mathematically extreme.
	DataQualityDegraded DataQuality = "DEGRADED"
)

// minSeriesForConfidence is the minimum reference series length below
// which a percentile/MAD/IQR computation is not considered reliable
// enough to call VALID — an honestly-documented threshold, not a
// silently buried magic number (5 points is the minimum needed for a
// non-degenerate IQR + MAD read in practice).
const minSeriesForConfidence = 5

// AnomalyResult is the output of evaluating one observed price against
// a reference series.
type AnomalyResult struct {
	ObservedPrice   float64
	ReferenceMedian float64
	MAD             float64 // median absolute deviation
	ModifiedZScore  float64 // 0.6745*(x-median)/MAD, Iglewicz & Hoya's robust z-score
	IQRLowerFence   float64
	IQRUpperFence   float64
	IsAnomaly       bool
	Direction       string // "UNDER", "OVER", or "" if not anomalous
	Explanation     string
	// DataQuality states how much this result's input should be
	// trusted (v7.9 P1) — a consumer (e.g. pkg/moat/intelligence/risk)
	// should discount or ignore a DEGRADED/INSUFFICIENT result rather
	// than treating a saturated AnomalyScore as high confidence.
	DataQuality DataQuality
}

// Detector evaluates observed prices against a reference series using
// two independent, well-established robust-statistics methods (median
// absolute deviation, and Tukey's IQR fences) so a single distributional
// quirk in one method doesn't solely drive the anomaly call.
type Detector struct {
	// ZScoreThreshold is the modified-z-score magnitude above which a
	// price is flagged by the MAD method. Iglewicz & Hoya (1993)
	// recommend 3.5 as a standard threshold for outlier detection;
	// exposed as a field so a caller can tune it per-commodity.
	ZScoreThreshold float64
	// IQRMultiplier is Tukey's fence multiplier; 1.5 is the standard
	// "mild outlier" convention, 3.0 is "extreme outlier".
	IQRMultiplier float64
}

// NewDetector returns a Detector configured with the standard,
// well-documented default thresholds (modified z-score 3.5, IQR
// multiplier 1.5).
func NewDetector() Detector {
	return Detector{ZScoreThreshold: 3.5, IQRMultiplier: 1.5}
}

// validateConfig rejects a non-finite or non-positive threshold —
// v7.9 P1's explicit ErrInvalidDetectorConfiguration ask.
func (d Detector) validateConfig() error {
	if math.IsNaN(d.ZScoreThreshold) || math.IsInf(d.ZScoreThreshold, 0) || d.ZScoreThreshold <= 0 {
		return fmt.Errorf("%w: ZScoreThreshold=%v", ErrInvalidDetectorConfiguration, d.ZScoreThreshold)
	}
	if math.IsNaN(d.IQRMultiplier) || math.IsInf(d.IQRMultiplier, 0) || d.IQRMultiplier <= 0 {
		return fmt.Errorf("%w: IQRMultiplier=%v", ErrInvalidDetectorConfiguration, d.IQRMultiplier)
	}
	return nil
}

// Evaluate compares observedPrice against reference using both the
// median-absolute-deviation modified z-score and Tukey's IQR fences.
// A price is flagged IsAnomaly if EITHER method independently flags it
// — the disjunction is intentional: MAD is more sensitive to a single
// far outlier, IQR is more sensitive to distributional skew; requiring
// only one to fire keeps recall high for a first-pass screen (a human
// or downstream calibration step judges precision from there, per this
// package's stated non-goal of being a finished, validated model).
//
// v7.9 P1 hardening: observedPrice and every reference element are
// validated (reject NaN/+Inf/-Inf/negative) BEFORE any statistics run
// — the audit's exact concern that sort.Float64s() with a NaN present
// silently produces a meaningless order, and a NaN observedPrice could
// make an anomaly comparison spuriously evaluate false ("not
// anomalous") for genuinely invalid data.
func (d Detector) Evaluate(observedPrice float64, reference []float64) (AnomalyResult, error) {
	if err := d.validateConfig(); err != nil {
		return AnomalyResult{}, err
	}
	if len(reference) == 0 {
		return AnomalyResult{}, ErrEmptyReference
	}
	if math.IsNaN(observedPrice) || math.IsInf(observedPrice, 0) || observedPrice < 0 {
		return AnomalyResult{}, fmt.Errorf("%w: %v", ErrInvalidObservedPrice, observedPrice)
	}
	for i, v := range reference {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return AnomalyResult{}, fmt.Errorf("%w: reference[%d]=%v", ErrInvalidReferencePrice, i, v)
		}
	}

	quality := DataQualityValid
	if len(reference) < minSeriesForConfidence {
		quality = DataQualityInsufficient
	}

	sorted := append([]float64{}, reference...)
	sort.Float64s(sorted)

	median := percentile(sorted, 0.5)
	deviations := make([]float64, len(sorted))
	for i, v := range sorted {
		deviations[i] = math.Abs(v - median)
	}
	sort.Float64s(deviations)
	mad := percentile(deviations, 0.5)

	var modZ float64
	if mad == 0 {
		// Degenerate case: reference series is constant. Fall back to
		// a hard equality check rather than dividing by zero.
		if observedPrice != median {
			modZ = math.Inf(1) * sign(observedPrice-median)
		}
		// v7.9 §10: a constant/degenerate reference series is a data
		// quality symptom (stale or broken feed), not genuine
		// distributional confirmation — do not let it silently pass as
		// VALID just because the math is well-defined.
		quality = DataQualityDegraded
	} else {
		modZ = 0.6745 * (observedPrice - median) / mad
	}

	q1 := percentile(sorted, 0.25)
	q3 := percentile(sorted, 0.75)
	iqr := q3 - q1
	lower := q1 - d.IQRMultiplier*iqr
	upper := q3 + d.IQRMultiplier*iqr

	res := AnomalyResult{
		ObservedPrice:   observedPrice,
		ReferenceMedian: median,
		MAD:             mad,
		ModifiedZScore:  modZ,
		IQRLowerFence:   lower,
		IQRUpperFence:   upper,
		DataQuality:     quality,
	}

	madFlag := math.Abs(modZ) >= d.ZScoreThreshold
	iqrFlag := observedPrice < lower || observedPrice > upper
	res.IsAnomaly = madFlag || iqrFlag

	if res.IsAnomaly {
		if observedPrice < median {
			res.Direction = "UNDER"
		} else {
			res.Direction = "OVER"
		}
	}

	switch {
	case !res.IsAnomaly:
		res.Explanation = "Observed price within normal range of reference distribution (both MAD and IQR checks pass)."
	case madFlag && iqrFlag:
		res.Explanation = "Observed price flagged by both robust-z-score and IQR-fence methods — strong statistical anomaly, consistent with potential over/under-invoicing (TBML technique) if not explained by legitimate market conditions (spot volatility, quality grade, freight terms)."
	case madFlag:
		res.Explanation = "Observed price flagged by robust modified-z-score only — a distant single-point deviation from the reference median."
	default:
		res.Explanation = "Observed price flagged by IQR fence only — outside Tukey's fences even though not an extreme single-point outlier, suggesting the whole distribution may be shifting."
	}
	return res, nil
}

// percentile returns the linear-interpolated percentile p (0..1) of an
// already-sorted slice. Deterministic, stdlib-only.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 1 {
		return sorted[0]
	}
	idx := p * float64(len(sorted)-1)
	lo := int(math.Floor(idx))
	hi := int(math.Ceil(idx))
	if lo == hi {
		return sorted[lo]
	}
	frac := idx - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

func sign(v float64) float64 {
	if v < 0 {
		return -1
	}
	return 1
}

// AnomalyScore maps an AnomalyResult onto a bounded 0..1 score suitable
// for feeding into pkg/moat/intelligence/risk's composite model or
// directly into a decision.Engine factor. The mapping saturates rather
// than growing unbounded with extreme z-scores, so one wildly mispriced
// outlier cannot single-handedly dominate a composite score.
func (r AnomalyResult) AnomalyScore() float64 {
	return r.EffectiveConfidence()
}

// RawAnomalyScore is the magnitude of the observed deviation itself —
// deliberately UNAFFECTED by DataQuality. Auditor's explicit
// distinction (§12): "DataQuality = DEGRADED" must not be implemented
// as "the phenomenon itself is smaller" — a constant reference series
// does not make a 200-vs-100 price gap any less real as a deviation;
// what's uncertain is how MUCH CONFIDENCE to place in that deviation
// given weak supporting data. Two separate numbers for two separate
// questions.
func (r AnomalyResult) RawAnomalyScore() float64 {
	if !r.IsAnomaly {
		return 0
	}
	z := math.Abs(r.ModifiedZScore)
	if math.IsInf(z, 1) {
		return 1
	}
	score := 1 - math.Exp(-z/7.0)
	if score > 1 {
		score = 1
	}
	if score < 0.3 {
		score = 0.3
	}
	return score
}

// EffectiveConfidence is RawAnomalyScore discounted by DataQuality —
// "how much should a downstream consumer actually trust this number,"
// as distinct from "how large is the observed deviation." This is what
// AnomalyScore() returns (kept for backward compatibility with
// existing callers); RawAnomalyScore() is available separately for any
// caller that specifically needs the undiscounted phenomenon magnitude
// (e.g. displaying "a 900% price gap was observed" even while also
// showing "confidence: LOW, reference feed appears stale").
func (r AnomalyResult) EffectiveConfidence() float64 {
	score := r.RawAnomalyScore()
	if score == 0 {
		return 0
	}
	switch r.DataQuality {
	case DataQualityDegraded:
		score *= 0.4
	case DataQualityInsufficient:
		score *= 0.6
	}
	return score
}
