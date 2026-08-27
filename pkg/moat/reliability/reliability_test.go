package reliability

import (
	"errors"
	"math"
	"testing"
)

func mk(p float64, out bool) Sample { return Sample{ClaimKey: "c", Predicted: p, Outcome: out} }

func perfectlyCalibrated() []Sample {
	// 100 samples: for each of ten deciles, k of 10 are positive where
	// k matches the bin's predicted probability.
	var s []Sample
	for b := 0; b < 10; b++ {
		p := float64(b)/10 + 0.05
		pos := b // deliberately slightly off-centre; observed = b/10
		for i := 0; i < 10; i++ {
			s = append(s, mk(p, i < pos))
		}
	}
	return s
}

func TestLogLossPunishesConfidentWrongnessMoreThanBrier(t *testing.T) {
	confidentWrong := []Sample{mk(0.99, false)}
	mildlyWrong := []Sample{mk(0.6, false)}

	llC, _ := LogLoss(confidentWrong)
	llM, _ := LogLoss(mildlyWrong)
	bC, _ := BrierScore(confidentWrong)
	bM, _ := BrierScore(mildlyWrong)

	ratioLL := llC / llM
	ratioB := bC / bM
	if ratioLL <= ratioB {
		t.Fatalf("log loss must penalise confident wrongness harder than Brier: llRatio=%v brierRatio=%v", ratioLL, ratioB)
	}
}

func TestLogLossFiniteAtProbabilityZeroAndOne(t *testing.T) {
	v, err := LogLoss([]Sample{mk(0, true), mk(1, false)})
	if err != nil {
		t.Fatal(err)
	}
	if math.IsInf(v, 0) || math.IsNaN(v) {
		t.Fatalf("log loss must be finite at the boundaries, got %v", v)
	}
}

func TestEmptySamplesAreAnErrorNotAPerfectScore(t *testing.T) {
	for name, fn := range map[string]func() (float64, error){
		"logloss": func() (float64, error) { return LogLoss(nil) },
		"brier":   func() (float64, error) { return BrierScore(nil) },
		"ece":     func() (float64, error) { return ExpectedCalibrationError(nil, 10) },
	} {
		if _, err := fn(); !errors.Is(err, ErrNoSamples) {
			t.Fatalf("%s: expected ErrNoSamples, got %v", name, err)
		}
	}
}

func TestProbabilityOutOfRangeRejected(t *testing.T) {
	if _, err := LogLoss([]Sample{mk(1.4, true)}); !errors.Is(err, ErrProbabilityRange) {
		t.Fatalf("expected range error, got %v", err)
	}
	if _, err := LogLoss([]Sample{mk(math.NaN(), true)}); !errors.Is(err, ErrProbabilityRange) {
		t.Fatalf("expected range error for NaN, got %v", err)
	}
}

func TestECENearZeroForCalibratedAndLargeForOverconfident(t *testing.T) {
	good, err := ExpectedCalibrationError(perfectlyCalibrated(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if good > 0.06 {
		t.Fatalf("calibrated model should have small ECE, got %v", good)
	}

	var bad []Sample
	for i := 0; i < 100; i++ {
		bad = append(bad, mk(0.95, i < 50)) // claims 95%, delivers 50%
	}
	badECE, err := ExpectedCalibrationError(bad, 10)
	if err != nil {
		t.Fatal(err)
	}
	if badECE < 0.4 {
		t.Fatalf("overconfident model should have large ECE, got %v", badECE)
	}
}

func TestReliabilityCurveSeparatesOverAndUnderConfidence(t *testing.T) {
	var over []Sample
	for i := 0; i < 100; i++ {
		over = append(over, mk(0.9, i < 40))
	}
	c, err := BuildReliabilityCurve(over, 10)
	if err != nil {
		t.Fatal(err)
	}
	if c.Overconf <= c.Underconf {
		t.Fatalf("expected overconfidence to dominate: over=%v under=%v", c.Overconf, c.Underconf)
	}
	if c.MCE < c.ECE {
		t.Fatalf("MCE must be >= ECE, got mce=%v ece=%v", c.MCE, c.ECE)
	}
}

func TestSparseBinIsNotMarkedStatisticallySignificant(t *testing.T) {
	// One sample at p=0.5 that came out true. Observed rate 1.0, gap
	// 0.5 — but with n=1 the Wilson interval swallows 0.5, so this must
	// NOT be reported as a significant miscalibration.
	c, err := BuildReliabilityCurve([]Sample{mk(0.5, true)}, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range c.Bins {
		if b.Count > 0 && b.StatisticallyO {
			t.Fatalf("single-sample bin must not be statistically significant: %+v", b)
		}
	}
}

func TestReliabilityCurveHashIsDeterministicAndSensitive(t *testing.T) {
	a, _ := BuildReliabilityCurve(perfectlyCalibrated(), 10)
	b, _ := BuildReliabilityCurve(perfectlyCalibrated(), 10)
	if a.Hash != b.Hash {
		t.Fatal("curve hash must be deterministic")
	}
	s := perfectlyCalibrated()
	s[0].Outcome = !s[0].Outcome
	c, _ := BuildReliabilityCurve(s, 10)
	if c.Hash == a.Hash {
		t.Fatal("flipping one outcome must change the curve hash")
	}
}

func TestPlattScalingCorrectsSystematicOverconfidence(t *testing.T) {
	var s []Sample
	for i := 0; i < 200; i++ {
		s = append(s, mk(0.9, i < 100)) // says 0.9, truth 0.5
	}
	cal, err := FitPlatt(s, 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	got, err := cal.Apply(0.9)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got-0.5) > 0.08 {
		t.Fatalf("platt should map 0.9 near the observed 0.5, got %v", got)
	}
	before, _ := ExpectedCalibrationError(s, 10)
	after := make([]Sample, len(s))
	for i, x := range s {
		p, _ := cal.Apply(x.Predicted)
		after[i] = Sample{Predicted: p, Outcome: x.Outcome}
	}
	afterECE, _ := ExpectedCalibrationError(after, 10)
	if afterECE >= before {
		t.Fatalf("calibration must reduce ECE: before=%v after=%v", before, afterECE)
	}
}

func TestIsotonicIsMonotoneAndOrderIndependent(t *testing.T) {
	s := []Sample{
		{ClaimKey: "a", Predicted: 0.1, Outcome: false},
		{ClaimKey: "b", Predicted: 0.2, Outcome: true},
		{ClaimKey: "c", Predicted: 0.3, Outcome: false},
		{ClaimKey: "d", Predicted: 0.4, Outcome: true},
		{ClaimKey: "e", Predicted: 0.5, Outcome: true},
		{ClaimKey: "f", Predicted: 0.6, Outcome: true},
	}
	cal, err := FitIsotonic(s, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(cal.Y); i++ {
		if cal.Y[i] < cal.Y[i-1] {
			t.Fatalf("isotonic output must be non-decreasing: %v", cal.Y)
		}
	}
	shuffled := []Sample{s[3], s[0], s[5], s[1], s[4], s[2]}
	cal2, err := FitIsotonic(shuffled, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if cal.ArtifactID != cal2.ArtifactID {
		t.Fatalf("isotonic fit must not depend on input ordering: %s vs %s", cal.ArtifactID, cal2.ArtifactID)
	}
}

func TestCalibratorArtifactIDChangesWithParameters(t *testing.T) {
	a := Identity(1, 1)
	b := Identity(1, 2)
	if a.ArtifactID == b.ArtifactID {
		t.Fatal("a different calibration version must be a different artifact")
	}
}

func TestUnfittedIsotonicRefusesToApply(t *testing.T) {
	c := Calibrator{Kind: KindIsotonic}
	if _, err := c.Apply(0.5); !errors.Is(err, ErrNotFitted) {
		t.Fatalf("expected ErrNotFitted, got %v", err)
	}
}

func TestPSIAndJSDetectShiftAndStayQuietOnStability(t *testing.T) {
	ref, _ := NewDistribution(map[string]float64{"a": 50, "b": 30, "c": 20})
	same, _ := NewDistribution(map[string]float64{"a": 100, "b": 60, "c": 40})
	shifted, _ := NewDistribution(map[string]float64{"a": 5, "b": 15, "c": 80})

	p0, _ := PSI(ref, same)
	if p0 > 1e-9 {
		t.Fatalf("PSI on a rescaled identical distribution must be ~0, got %v", p0)
	}
	p1, _ := PSI(ref, shifted)
	if p1 < 0.25 {
		t.Fatalf("PSI must flag a major shift, got %v", p1)
	}
	js, _ := JSDivergence(ref, shifted)
	if js <= 0 || js > math.Ln2+1e-9 {
		t.Fatalf("JS must be in (0, ln2], got %v", js)
	}
	jsSelf, _ := JSDivergence(ref, same)
	if jsSelf > 1e-9 {
		t.Fatalf("JS of a distribution with itself must be 0, got %v", jsSelf)
	}
}

func TestJSIsSymmetricButKLIsNot(t *testing.T) {
	a, _ := NewDistribution(map[string]float64{"x": 90, "y": 10})
	b, _ := NewDistribution(map[string]float64{"x": 10, "y": 90})
	ab, _ := JSDivergence(a, b)
	ba, _ := JSDivergence(b, a)
	if math.Abs(ab-ba) > 1e-12 {
		t.Fatalf("JS must be symmetric: %v vs %v", ab, ba)
	}
	kab, _ := KLDivergence(a, b)
	c, _ := NewDistribution(map[string]float64{"x": 50, "y": 50})
	kac, _ := KLDivergence(a, c)
	if kab <= kac {
		t.Fatalf("KL should grow with divergence: %v vs %v", kab, kac)
	}
}

func TestMismatchedSupportIsRejectedNotSilentlyPadded(t *testing.T) {
	a, _ := NewDistribution(map[string]float64{"x": 1, "y": 1})
	b, _ := NewDistribution(map[string]float64{"x": 1, "z": 1})
	if _, err := PSI(a, b); !errors.Is(err, ErrDistributionShape) {
		t.Fatalf("expected shape error, got %v", err)
	}
}

func TestKSDetectsContinuousShift(t *testing.T) {
	var ref, cur []float64
	for i := 0; i < 200; i++ {
		ref = append(ref, float64(i)/200)
		cur = append(cur, 0.5+float64(i)/400)
	}
	d, p, err := KSStatistic(ref, cur)
	if err != nil {
		t.Fatal(err)
	}
	if d < 0.4 || p > 0.01 {
		t.Fatalf("KS should detect the shift: d=%v p=%v", d, p)
	}
	d2, p2, _ := KSStatistic(ref, ref)
	if d2 > 1e-12 || p2 < 0.99 {
		t.Fatalf("KS of a sample with itself must be ~0/1: d=%v p=%v", d2, p2)
	}
}

func TestDriftReportHashCoversVerdict(t *testing.T) {
	ref, _ := NewDistribution(map[string]float64{"a": 50, "b": 50})
	cur, _ := NewDistribution(map[string]float64{"a": 5, "b": 95})
	r1, err := DetectDrift(DriftPrediction, "risk_model", "ref-1", ref, cur, nil, nil, DefaultThresholds(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if !r1.Drifted || len(r1.Reasons) == 0 {
		t.Fatalf("expected drift, got %+v", r1)
	}
	r2, _ := DetectDrift(DriftPrediction, "risk_model", "ref-1", ref, cur, nil, nil, DefaultThresholds(), 7)
	if r1.Hash != r2.Hash {
		t.Fatal("drift report hash must be deterministic")
	}
	r3, _ := DetectDrift(DriftPrediction, "risk_model", "ref-2", ref, cur, nil, nil, DefaultThresholds(), 7)
	if r3.Hash == r1.Hash {
		t.Fatal("a different reference must produce a different drift hash")
	}
}

func TestDegradedCalibrationIsNotSilentlyHealthy(t *testing.T) {
	var bad []Sample
	for i := 0; i < 200; i++ {
		bad = append(bad, mk(0.95, i < 100))
	}
	a, err := Assess("risk", "v1", bad, 10, "cal-0", nil, DefaultThresholds(), 50, 9)
	if err != nil {
		t.Fatal(err)
	}
	if a.Health != HealthDegraded {
		t.Fatalf("expected DEGRADED, got %s", a.Health)
	}
	if a.Err() == nil || !errors.Is(a.Err(), ErrDegraded) {
		t.Fatal("a degraded assessment must surface as an error")
	}
}

func TestInsufficientDataIsNeitherHealthyNorDegraded(t *testing.T) {
	a, err := Assess("risk", "v1", []Sample{mk(0.9, false)}, 10, "cal-0", nil, DefaultThresholds(), 50, 1)
	if err != nil {
		t.Fatal(err)
	}
	if a.Health != HealthInsufficient {
		t.Fatalf("expected INSUFFICIENT_DATA, got %s", a.Health)
	}
	if a.Err() != nil {
		t.Fatal("insufficient data is not a degradation error")
	}
}

func TestDriftInAssessmentForcesDegraded(t *testing.T) {
	ref, _ := NewDistribution(map[string]float64{"a": 50, "b": 50})
	cur, _ := NewDistribution(map[string]float64{"a": 1, "b": 99})
	d, _ := DetectDrift(DriftFeature, "draught", "ref", ref, cur, nil, nil, DefaultThresholds(), 3)

	good := perfectlyCalibrated()
	a, err := Assess("risk", "v1", good, 10, "cal-0", []DriftReport{d}, DefaultThresholds(), 50, 3)
	if err != nil {
		t.Fatal(err)
	}
	if a.Health != HealthDegraded {
		t.Fatalf("drift alone must degrade the model even when ECE is fine, got %s", a.Health)
	}
}

func TestAssessmentHashIsOrderIndependentAcrossDriftReports(t *testing.T) {
	ref, _ := NewDistribution(map[string]float64{"a": 50, "b": 50})
	cur, _ := NewDistribution(map[string]float64{"a": 40, "b": 60})
	d1, _ := DetectDrift(DriftFeature, "f1", "r", ref, cur, nil, nil, DefaultThresholds(), 1)
	d2, _ := DetectDrift(DriftFeature, "f2", "r", ref, cur, nil, nil, DefaultThresholds(), 1)
	s := perfectlyCalibrated()
	a, _ := Assess("m", "v1", s, 10, "c", []DriftReport{d1, d2}, DefaultThresholds(), 50, 1)
	b, _ := Assess("m", "v1", s, 10, "c", []DriftReport{d2, d1}, DefaultThresholds(), 50, 1)
	if a.Hash != b.Hash {
		t.Fatal("assessment hash must not depend on drift report ordering")
	}
}
