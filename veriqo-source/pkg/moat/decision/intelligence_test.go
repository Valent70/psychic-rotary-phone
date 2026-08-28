package decision

import "testing"

func demoPolicy() Policy {
	return Policy{
		Name: "dark_vessel",
		Factors: []FactorWeight{
			{Name: "ais_anomaly", Weight: 0.45},
			{Name: "insurance_mismatch", Weight: 0.30},
			{Name: "cargo_mismatch", Weight: 0.18},
			{Name: "historical_behavior", Weight: 0.07},
		},
		FlagThreshold: 0.4, EscalateThreshold: 0.7,
	}
}

func TestExplainBreakdownPercentagesSumTo100(t *testing.T) {
	policy := demoPolicy()
	values := map[string]float64{
		"ais_anomaly": 1.0, "insurance_mismatch": 1.0, "cargo_mismatch": 1.0, "historical_behavior": 1.0,
	}
	bd := ExplainBreakdown(policy, values)
	if len(bd) != 4 {
		t.Fatalf("expected 4 factors, got %d", len(bd))
	}
	sum := 0.0
	for _, c := range bd {
		sum += c.PercentOfTotal
	}
	if sum < 99.99 || sum > 100.01 {
		t.Fatalf("percentages should sum to ~100, got %f", sum)
	}
	// with equal values, weight order determines rank: ais_anomaly highest weight -> highest %
	if bd[0].Name != "ais_anomaly" {
		t.Fatalf("expected ais_anomaly to have highest contribution, got %s first", bd[0].Name)
	}
}

func TestExplainBreakdownFlagsMissingSignal(t *testing.T) {
	policy := demoPolicy()
	values := map[string]float64{"ais_anomaly": 0.9}
	bd := ExplainBreakdown(policy, values)
	found := false
	for _, c := range bd {
		if c.Name == "cargo_mismatch" {
			found = true
			if c.SignalSupplied {
				t.Fatalf("cargo_mismatch should be flagged as not supplied")
			}
		}
	}
	if !found {
		t.Fatalf("expected cargo_mismatch in breakdown even without a value")
	}
}

func TestComputeUtilityMatchesDecideRiskScore(t *testing.T) {
	policy := demoPolicy()
	e := NewEngine()
	if err := e.RegisterPolicy(policy); err != nil {
		t.Fatalf("register: %v", err)
	}
	values := map[string]float64{
		"ais_anomaly": 0.95, "insurance_mismatch": 0.8, "cargo_mismatch": 0.6, "historical_behavior": 0.3,
	}
	d, err := e.Decide("actor", "VESSEL:1", "dark_vessel", values, 1)
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	u := ComputeUtility(UtilityInput{Policy: policy, Values: values})
	if diff := d.RiskScore - u.RiskScore; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("ComputeUtility risk score (%f) should match Decide's (%f)", u.RiskScore, d.RiskScore)
	}
}

func TestComputeUtilityExpectedValueAndMultiObjective(t *testing.T) {
	policy := demoPolicy()
	values := map[string]float64{"ais_anomaly": 0.9, "insurance_mismatch": 0.8, "cargo_mismatch": 0.5, "historical_behavior": 0.2}
	u := ComputeUtility(UtilityInput{
		Policy: policy, Values: values,
		Cost: 10000, Impact: 500000, ProbabilityOfOccurrence: 0.3,
		ObjectiveWeights: map[string]float64{"risk": 0.5, "cost": 0.2, "impact": 0.3},
	})
	wantEV := 0.3*500000 - 10000
	if u.ExpectedValue != wantEV {
		t.Fatalf("expected EV %f, got %f", wantEV, u.ExpectedValue)
	}
	if u.MultiObjectiveScore == 0 {
		t.Fatalf("expected nonzero multi-objective score")
	}
}

func TestCounterfactualIsNonMutatingAndDetectsActionChange(t *testing.T) {
	policy := demoPolicy()
	base := map[string]float64{"ais_anomaly": 0.2, "insurance_mismatch": 0.1, "cargo_mismatch": 0.1, "historical_behavior": 0.1}
	override := map[string]float64{"ais_anomaly": 0.95, "insurance_mismatch": 0.9}

	cf := Counterfactual(policy, base, override)
	if cf.BaseAction != ActionMonitor {
		t.Fatalf("expected base action MONITOR for low values, got %s", cf.BaseAction)
	}
	if cf.CounterfactualAction == ActionMonitor {
		t.Fatalf("expected counterfactual action to escalate beyond MONITOR with high override values, got %s", cf.CounterfactualAction)
	}
	if cf.Delta <= 0 {
		t.Fatalf("expected positive delta when overriding toward higher-risk values, got %f", cf.Delta)
	}
	if len(cf.ChangedFactors) != 2 {
		t.Fatalf("expected 2 changed factors, got %v", cf.ChangedFactors)
	}
	// base map must remain untouched
	if base["ais_anomaly"] != 0.2 {
		t.Fatalf("Counterfactual must not mutate the base values map")
	}
}

func TestCounterfactualNoChangeYieldsZeroDelta(t *testing.T) {
	policy := demoPolicy()
	base := map[string]float64{"ais_anomaly": 0.5, "insurance_mismatch": 0.5, "cargo_mismatch": 0.5, "historical_behavior": 0.5}
	cf := Counterfactual(policy, base, map[string]float64{})
	if cf.Delta != 0 {
		t.Fatalf("expected zero delta with no overrides, got %f", cf.Delta)
	}
	if len(cf.ChangedFactors) != 0 {
		t.Fatalf("expected no changed factors, got %v", cf.ChangedFactors)
	}
}
