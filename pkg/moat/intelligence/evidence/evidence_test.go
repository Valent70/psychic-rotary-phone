package evidence

import (
	"math"
	"testing"
)

func TestNew_DefaultsAreHonest(t *testing.T) {
	e := New("VESSEL_1", "risk:tbml_composite", "SUSPECTED", 0.82, 10)
	if e.CalibrationStatus != "UNCALIBRATED" {
		t.Fatalf("expected UNCALIBRATED default, got %s", e.CalibrationStatus)
	}
	if e.CalibratedProbability != nil {
		t.Fatalf("expected nil CalibratedProbability before real calibration, got %v", *e.CalibratedProbability)
	}
	if e.RiskScore != 0.82 {
		t.Errorf("RiskScore = %v, want 0.82", e.RiskScore)
	}
	if e.ID == "" {
		t.Fatalf("expected non-empty content-addressed ID")
	}
}

func TestWithCalibration_ChangesID(t *testing.T) {
	base := New("VESSEL_1", "risk:tbml_composite", "SUSPECTED", 0.82, 10)
	calibrated := base.WithCalibration(0.67, "calib_v3")
	if calibrated.ID == base.ID {
		t.Fatalf("expected ID to change once calibration state is attached")
	}
	if calibrated.CalibrationStatus != "CALIBRATED" {
		t.Errorf("expected CALIBRATED status")
	}
	if *calibrated.CalibratedProbability != 0.67 {
		t.Errorf("CalibratedProbability = %v, want 0.67", *calibrated.CalibratedProbability)
	}
	// Score != Probability: RiskScore must remain the original heuristic
	// value, untouched by calibration.
	if calibrated.RiskScore != 0.82 {
		t.Errorf("calibration must not overwrite RiskScore; got %v", calibrated.RiskScore)
	}
}

func TestValidateFinite_RejectsNaNAndInf(t *testing.T) {
	cases := []float64{math.NaN(), math.Inf(1), math.Inf(-1)}
	for _, v := range cases {
		if err := ValidateFinite(v, "x"); err == nil {
			t.Errorf("expected error for %v", v)
		}
	}
	if err := ValidateFinite(1.23, "x"); err != nil {
		t.Errorf("unexpected error for finite value: %v", err)
	}
}

func TestValidateNonNegative_RejectsNegative(t *testing.T) {
	if err := ValidateNonNegative(-5, "price"); err == nil {
		t.Fatalf("expected error for negative price")
	}
	if err := ValidateNonNegative(0, "price"); err != nil {
		t.Errorf("zero should be valid: %v", err)
	}
}

func TestValidateRatio_RejectsOutOfRange(t *testing.T) {
	if err := ValidateRatio(1.5, "independence"); err == nil {
		t.Fatalf("expected error for ratio > 1")
	}
	if err := ValidateRatio(-0.1, "independence"); err == nil {
		t.Fatalf("expected error for ratio < 0")
	}
	if err := ValidateRatio(0.5, "independence"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateSeries_FindsFirstOffender(t *testing.T) {
	series := []float64{1, 2, math.NaN(), 4}
	err := ValidateSeries(series, "reference")
	if err == nil {
		t.Fatalf("expected error for series containing NaN")
	}
}
