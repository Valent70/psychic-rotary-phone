package pricing

import "testing"

func TestDetector_NormalPriceNotAnomalous(t *testing.T) {
	d := NewDetector()
	ref := []float64{100, 101, 99, 102, 98, 100, 101}
	res, err := d.Evaluate(100.5, ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsAnomaly {
		t.Fatalf("expected 100.5 (near median) not anomalous: %+v", res)
	}
}

func TestDetector_UnderInvoicingFlagged(t *testing.T) {
	d := NewDetector()
	ref := []float64{100, 101, 99, 102, 98, 100, 101, 99, 100, 102}
	// Observed price is 50% below the reference cluster — classic
	// under-invoicing signature.
	res, err := d.Evaluate(50, ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsAnomaly {
		t.Fatalf("expected 50 vs ~100 cluster to be flagged anomalous: %+v", res)
	}
	if res.Direction != "UNDER" {
		t.Errorf("expected UNDER direction, got %s", res.Direction)
	}
}

func TestDetector_OverInvoicingFlagged(t *testing.T) {
	d := NewDetector()
	ref := []float64{100, 101, 99, 102, 98, 100, 101, 99, 100, 102}
	res, err := d.Evaluate(250, ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsAnomaly || res.Direction != "OVER" {
		t.Fatalf("expected OVER anomaly for 250 vs ~100 cluster: %+v", res)
	}
}

func TestDetector_EmptyReferenceErrors(t *testing.T) {
	d := NewDetector()
	if _, err := d.Evaluate(100, nil); err != ErrEmptyReference {
		t.Fatalf("expected ErrEmptyReference, got %v", err)
	}
}

func TestDetector_ConstantReferenceNoDivideByZeroPanic(t *testing.T) {
	d := NewDetector()
	ref := []float64{50, 50, 50, 50}
	res, err := d.Evaluate(50, ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsAnomaly {
		t.Fatalf("exact match to constant reference should not be anomalous: %+v", res)
	}
	res2, err := d.Evaluate(999, ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res2.IsAnomaly {
		t.Fatalf("999 vs constant reference of 50 must be anomalous: %+v", res2)
	}
}

func TestAnomalyScore_BoundedAndMonotonic(t *testing.T) {
	d := NewDetector()
	ref := []float64{100, 101, 99, 102, 98, 100, 101, 99, 100, 102}
	mild, _ := d.Evaluate(140, ref)
	extreme, _ := d.Evaluate(500, ref)

	sMild := mild.AnomalyScore()
	sExtreme := extreme.AnomalyScore()
	if sMild < 0 || sMild > 1 || sExtreme < 0 || sExtreme > 1 {
		t.Fatalf("scores out of [0,1] range: mild=%v extreme=%v", sMild, sExtreme)
	}
	if sExtreme < sMild {
		t.Errorf("expected more extreme anomaly to score higher: mild=%v extreme=%v", sMild, sExtreme)
	}
}

func TestAnomalyScore_NotAnomalousIsZero(t *testing.T) {
	d := NewDetector()
	ref := []float64{100, 101, 99, 102, 98}
	res, _ := d.Evaluate(100, ref)
	if got := res.AnomalyScore(); got != 0 {
		t.Errorf("expected 0 score for non-anomalous result, got %v", got)
	}
}
