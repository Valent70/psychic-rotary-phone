package pricing

import (
	"math"
	"testing"
)

// TestAdversarial_NaNObservedRejected covers the exact v7.9 §9 concern:
// a NaN observedPrice must never silently evaluate as "not anomalous".
func TestAdversarial_NaNObservedRejected(t *testing.T) {
	d := NewDetector()
	_, err := d.Evaluate(math.NaN(), []float64{100, 101, 99, 100, 102})
	if err == nil {
		t.Fatalf("expected error for NaN observed price")
	}
}

func TestAdversarial_InfObservedRejected(t *testing.T) {
	d := NewDetector()
	if _, err := d.Evaluate(math.Inf(1), []float64{100, 101, 99, 100, 102}); err == nil {
		t.Fatalf("expected error for +Inf observed price")
	}
	if _, err := d.Evaluate(math.Inf(-1), []float64{100, 101, 99, 100, 102}); err == nil {
		t.Fatalf("expected error for -Inf observed price")
	}
}

func TestAdversarial_NegativePriceRejected(t *testing.T) {
	d := NewDetector()
	if _, err := d.Evaluate(-50, []float64{100, 101, 99, 100, 102}); err == nil {
		t.Fatalf("expected error for negative observed price")
	}
}

func TestAdversarial_ReferenceContainingNaNRejected(t *testing.T) {
	d := NewDetector()
	if _, err := d.Evaluate(100, []float64{100, math.NaN(), 99}); err == nil {
		t.Fatalf("expected error for reference series containing NaN")
	}
}

func TestAdversarial_ReferenceContainingInfRejected(t *testing.T) {
	d := NewDetector()
	if _, err := d.Evaluate(100, []float64{100, math.Inf(1), 99}); err == nil {
		t.Fatalf("expected error for reference series containing +Inf")
	}
}

func TestAdversarial_ReferenceContainingNegativeRejected(t *testing.T) {
	d := NewDetector()
	if _, err := d.Evaluate(100, []float64{100, -5, 99}); err == nil {
		t.Fatalf("expected error for reference series containing negative price")
	}
}

// TestAdversarial_ConstantFeedMarkedDegraded is the v7.9 §10 worked
// example: reference=[100,100,100,100,100], observed=200. The math
// (modZ=+Inf) is well-defined, but the result must be flagged
// DEGRADED, and AnomalyScore must be discounted below what an
// equivalent genuinely-distributed extreme outlier would score.
func TestAdversarial_ConstantFeedMarkedDegraded(t *testing.T) {
	d := NewDetector()
	res, err := d.Evaluate(200, []float64{100, 100, 100, 100, 100})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.DataQuality != DataQualityDegraded {
		t.Fatalf("expected DataQualityDegraded for constant reference series, got %s", res.DataQuality)
	}
	if !res.IsAnomaly {
		t.Fatalf("expected IsAnomaly true (math is still a real deviation)")
	}
	discounted := res.AnomalyScore()

	// A genuinely-distributed series producing the SAME +Inf-magnitude
	// signal shouldn't happen (only degenerate series produce true
	// +Inf), but a strongly-distributed series with a big finite z
	// should still score higher confidence than the degraded case,
	// proving the discount is real.
	genuine, err := d.Evaluate(200, []float64{60, 65, 58, 62, 61, 63, 59, 64, 60, 62})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if genuine.DataQuality != DataQualityValid {
		t.Fatalf("expected genuine distributed series to be VALID, got %s", genuine.DataQuality)
	}
	if discounted >= genuine.AnomalyScore() {
		t.Errorf("expected degraded-quality score (%v) to be discounted below a valid-quality equivalent (%v)", discounted, genuine.AnomalyScore())
	}
}

// TestAdversarial_TinyReferenceMarkedInsufficient covers the audit's
// "tiny reference" adversarial case.
func TestAdversarial_TinyReferenceMarkedInsufficient(t *testing.T) {
	d := NewDetector()
	res, err := d.Evaluate(500, []float64{100, 105})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.DataQuality != DataQualityInsufficient {
		t.Fatalf("expected DataQualityInsufficient for a 2-point reference series, got %s", res.DataQuality)
	}
}

// TestAdversarial_MassiveOutlierStillBounded covers the audit's
// "massive outlier" case: AnomalyScore must remain bounded in [0,1]
// regardless of how extreme the input is.
func TestAdversarial_MassiveOutlierStillBounded(t *testing.T) {
	d := NewDetector()
	res, err := d.Evaluate(1e9, []float64{60, 65, 58, 62, 61, 63, 59, 64, 60, 62})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	score := res.AnomalyScore()
	if score < 0 || score > 1 {
		t.Fatalf("AnomalyScore out of bounds for massive outlier: %v", score)
	}
}

// TestAdversarial_InvalidDetectorConfigRejected covers the
// ErrInvalidDetectorConfiguration ask.
func TestAdversarial_InvalidDetectorConfigRejected(t *testing.T) {
	bad := Detector{ZScoreThreshold: 0, IQRMultiplier: 1.5}
	if _, err := bad.Evaluate(100, []float64{1, 2, 3}); err == nil {
		t.Fatalf("expected error for zero ZScoreThreshold")
	}
	bad2 := Detector{ZScoreThreshold: 3.5, IQRMultiplier: math.NaN()}
	if _, err := bad2.Evaluate(100, []float64{1, 2, 3}); err == nil {
		t.Fatalf("expected error for NaN IQRMultiplier")
	}
}
