package hbayes

import (
	"math"
	"testing"
)

func flatCorrelation(groups []ProviderGroupID, off float64) CorrelationMatrix {
	n := len(groups)
	vals := make([][]float64, n)
	for i := range vals {
		vals[i] = make([]float64, n)
		for j := range vals[i] {
			if i == j {
				vals[i][j] = 1.0
			} else {
				vals[i][j] = off
			}
		}
	}
	return CorrelationMatrix{Groups: groups, Values: vals}
}

func TestCorrelationMatrixValidation(t *testing.T) {
	groups := []ProviderGroupID{"AIS", "SAR"}
	good := flatCorrelation(groups, 0.3)
	if err := good.Validate(); err != nil {
		t.Fatalf("expected valid matrix, got %v", err)
	}
	bad := CorrelationMatrix{Groups: groups, Values: [][]float64{{1, 0.3}, {0.9, 1}}}
	if err := bad.Validate(); err == nil {
		t.Fatalf("expected asymmetric matrix to fail validation")
	}
	outOfRange := flatCorrelation(groups, 1.5)
	if err := outOfRange.Validate(); err == nil {
		t.Fatalf("expected out-of-range correlation to fail validation")
	}
}

func TestNewModelRejectsUnknownGroupInMatrix(t *testing.T) {
	priors := []ReliabilityPrior{{GroupID: "AIS", Mean: 0.8, Variance: 0.02}}
	corr := flatCorrelation([]ProviderGroupID{"AIS", "SAR"}, 0.3) // SAR has no prior
	if _, err := NewModel(priors, corr); err == nil {
		t.Fatalf("expected error for group in matrix without prior")
	}
}

func TestPosteriorReliabilityUpdatesTowardEvidence(t *testing.T) {
	priors := []ReliabilityPrior{{GroupID: "AIS", Mean: 0.5, Variance: 0.02}}
	corr := flatCorrelation([]ProviderGroupID{"AIS"}, 0.0)
	m, err := NewModel(priors, corr)
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}
	signals := []Signal{
		{SourceID: "s1", Group: "AIS", Confirms: true, Reliability: 0.9},
		{SourceID: "s2", Group: "AIS", Confirms: true, Reliability: 0.9},
		{SourceID: "s3", Group: "AIS", Confirms: true, Reliability: 0.9},
		{SourceID: "s4", Group: "AIS", Confirms: true, Reliability: 0.9},
	}
	posts := m.ComputePosteriorReliabilities(signals)
	if len(posts) != 1 {
		t.Fatalf("expected 1 posterior, got %d", len(posts))
	}
	if posts[0].PosteriorMean <= posts[0].PriorMean {
		t.Fatalf("posterior should move up from 0.5 prior with 4 confirms 0 contradicts: %+v", posts[0])
	}
}

// TestCorrelatedSourcesSaturateVsIndependentSources is the exact test the
// audit doc asks for: three vendors reselling the same feed (high intra-
// group correlation) must NOT produce the same risk as three genuinely
// independent sensors.
func TestCorrelatedSourcesSaturateVsIndependentSources(t *testing.T) {
	groups := []ProviderGroupID{"AIS_RESELLERS", "INDEPENDENT_SENSORS"}
	priors := []ReliabilityPrior{
		{GroupID: "AIS_RESELLERS", Mean: 0.8, Variance: 0.02},
		{GroupID: "INDEPENDENT_SENSORS", Mean: 0.8, Variance: 0.02},
	}
	corr := flatCorrelation(groups, 0.0) // groups themselves independent of each other
	m, err := NewModel(priors, corr)
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}

	correlatedSignals := []Signal{
		{SourceID: "vendorA", Group: "AIS_RESELLERS", Confirms: true, Reliability: 0.85},
		{SourceID: "vendorB", Group: "AIS_RESELLERS", Confirms: true, Reliability: 0.85},
		{SourceID: "vendorC", Group: "AIS_RESELLERS", Confirms: true, Reliability: 0.85},
	}
	independentSignals := []Signal{
		{SourceID: "sensorA", Group: "INDEPENDENT_SENSORS", Confirms: true, Reliability: 0.85},
		{SourceID: "sensorB", Group: "INDEPENDENT_SENSORS", Confirms: true, Reliability: 0.85},
		{SourceID: "sensorC", Group: "INDEPENDENT_SENSORS", Confirms: true, Reliability: 0.85},
	}

	correlatedRisk, err := m.ComputeEventRisk(correlatedSignals, map[ProviderGroupID]float64{
		"AIS_RESELLERS": 0.9, // near-total intra-group correlation (same feed)
	})
	if err != nil {
		t.Fatalf("ComputeEventRisk correlated: %v", err)
	}
	independentRisk, err := m.ComputeEventRisk(independentSignals, map[ProviderGroupID]float64{
		"INDEPENDENT_SENSORS": 0.0, // fully independent
	})
	if err != nil {
		t.Fatalf("ComputeEventRisk independent: %v", err)
	}

	if correlatedRisk.Probability >= independentRisk.Probability {
		t.Fatalf("correlated group risk (%f) should be lower than independent group risk (%f) for same signal count/reliability",
			correlatedRisk.Probability, independentRisk.Probability)
	}
	if correlatedRisk.EffectiveIndependentSignals >= independentRisk.EffectiveIndependentSignals {
		t.Fatalf("correlated effective-signal count (%f) should be lower than independent (%f)",
			correlatedRisk.EffectiveIndependentSignals, independentRisk.EffectiveIndependentSignals)
	}
	// Independent group with 3 confirming signals should be close to the
	// full 3.0 effective count; correlated group should be well below it.
	if independentRisk.EffectiveIndependentSignals < 2.9 {
		t.Fatalf("independent signals should sum to ~3.0 effective, got %f", independentRisk.EffectiveIndependentSignals)
	}
	if correlatedRisk.EffectiveIndependentSignals > 1.5 {
		t.Fatalf("highly correlated signals should saturate well below 3.0 effective, got %f", correlatedRisk.EffectiveIndependentSignals)
	}
}

func TestCrossGroupCorrelationDiscountsCombination(t *testing.T) {
	groups := []ProviderGroupID{"AIS", "SAR"}
	priors := []ReliabilityPrior{
		{GroupID: "AIS", Mean: 0.8, Variance: 0.02},
		{GroupID: "SAR", Mean: 0.8, Variance: 0.02},
	}
	independentModel, _ := NewModel(priors, flatCorrelation(groups, 0.0))
	correlatedModel, _ := NewModel(priors, flatCorrelation(groups, 0.9))

	signals := []Signal{
		{SourceID: "ais1", Group: "AIS", Confirms: true, Reliability: 0.85},
		{SourceID: "sar1", Group: "SAR", Confirms: true, Reliability: 0.85},
	}
	indep := map[ProviderGroupID]float64{"AIS": 0, "SAR": 0}

	riskIndependentGroups, err := independentModel.ComputeEventRisk(signals, indep)
	if err != nil {
		t.Fatalf("ComputeEventRisk: %v", err)
	}
	riskCorrelatedGroups, err := correlatedModel.ComputeEventRisk(signals, indep)
	if err != nil {
		t.Fatalf("ComputeEventRisk: %v", err)
	}
	if riskCorrelatedGroups.Probability >= riskIndependentGroups.Probability {
		t.Fatalf("cross-group correlation should discount combined risk: correlated=%f independent=%f",
			riskCorrelatedGroups.Probability, riskIndependentGroups.Probability)
	}
}

func TestComputeEventRiskUnknownGroupError(t *testing.T) {
	priors := []ReliabilityPrior{{GroupID: "AIS", Mean: 0.8, Variance: 0.02}, {GroupID: "SAR", Mean: 0.8, Variance: 0.02}}
	m, _ := NewModel(priors, flatCorrelation([]ProviderGroupID{"AIS", "SAR"}, 0.2))
	signals := []Signal{
		{SourceID: "a", Group: "AIS", Confirms: true, Reliability: 0.8},
		{SourceID: "s", Group: "SAR", Confirms: true, Reliability: 0.8},
	}
	// remove SAR from correlation lookup path by using a model whose
	// matrix doesn't include a group present in signals
	badModel := &Model{Priors: m.Priors, Correlation: CorrelationMatrix{Groups: []ProviderGroupID{"AIS"}, Values: [][]float64{{1}}}}
	if _, err := badModel.ComputeEventRisk(signals, nil); err == nil {
		t.Fatalf("expected error for group missing from correlation matrix")
	}
}

func TestDeterministicAcrossRepeatedCalls(t *testing.T) {
	priors := []ReliabilityPrior{{GroupID: "AIS", Mean: 0.7, Variance: 0.03}}
	m, _ := NewModel(priors, flatCorrelation([]ProviderGroupID{"AIS"}, 0.0))
	signals := []Signal{
		{SourceID: "a", Group: "AIS", Confirms: true, Reliability: 0.9},
		{SourceID: "b", Group: "AIS", Confirms: true, Reliability: 0.7},
	}
	r1, _ := m.ComputeEventRisk(signals, map[ProviderGroupID]float64{"AIS": 0.4})
	r2, _ := m.ComputeEventRisk(signals, map[ProviderGroupID]float64{"AIS": 0.4})
	if math.Abs(r1.Probability-r2.Probability) > 1e-12 {
		t.Fatalf("expected identical probability across repeated calls: %f vs %f", r1.Probability, r2.Probability)
	}
}
