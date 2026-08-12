package digitaltwin

import (
	"testing"

	"veriqo/pkg/moat/decision"
)

func TestSimulateConvergesTowardTarget(t *testing.T) {
	twin := Twin{Entity: "V1", Risk: map[string]RiskState{
		"dark_vessel": {PolicyName: "dark_vessel", RiskScore: 0.1, UpdatedAtTick: 5},
	}}
	proj := Simulate(twin, "dark_vessel", SimulationParams{TargetRiskScore: 0.9, ApproachRate: 0.5, HorizonTicks: 10})
	if len(proj) != 10 {
		t.Fatalf("expected 10 projected points, got %d", len(proj))
	}
	if proj[0].Tick != 6 {
		t.Fatalf("expected first projection at tick 6, got %d", proj[0].Tick)
	}
	last := proj[len(proj)-1]
	if last.RiskScore < 0.85 {
		t.Fatalf("expected trajectory to converge close to target 0.9, got %f", last.RiskScore)
	}
	// monotonically increasing toward target since target > start
	for i := 1; i < len(proj); i++ {
		if proj[i].RiskScore < proj[i-1].RiskScore {
			t.Fatalf("expected monotonic increase toward target, got decrease at %d", i)
		}
	}
}

func TestSimulateIsDeterministicAndNonMutating(t *testing.T) {
	twin := Twin{Entity: "V1", Risk: map[string]RiskState{
		"p": {PolicyName: "p", RiskScore: 0.3, UpdatedAtTick: 1},
	}}
	params := SimulationParams{TargetRiskScore: 0.8, ApproachRate: 0.3, HorizonTicks: 5}
	p1 := Simulate(twin, "p", params)
	p2 := Simulate(twin, "p", params)
	if len(p1) != len(p2) {
		t.Fatalf("length mismatch")
	}
	for i := range p1 {
		if p1[i] != p2[i] {
			t.Fatalf("expected deterministic identical output at %d: %+v vs %+v", i, p1[i], p2[i])
		}
	}
	if twin.Risk["p"].RiskScore != 0.3 {
		t.Fatalf("Simulate must not mutate the twin")
	}
}

func TestSimulateHandlesMissingPolicyAsZeroStart(t *testing.T) {
	twin := Twin{Entity: "V1", Risk: map[string]RiskState{}}
	proj := Simulate(twin, "unknown_policy", SimulationParams{TargetRiskScore: 0.5, ApproachRate: 1.0, HorizonTicks: 1})
	if len(proj) != 1 || proj[0].RiskScore != 0.5 {
		t.Fatalf("expected single-step jump to target from zero start, got %+v", proj)
	}
}

func TestSimulatePolicyEffectComposesCounterfactualAndProjection(t *testing.T) {
	twin := Twin{Entity: "V1", Risk: map[string]RiskState{
		"dark_vessel": {PolicyName: "dark_vessel", RiskScore: 0.1, UpdatedAtTick: 0},
	}}
	policy := decision.Policy{
		Name: "dark_vessel",
		Factors: []decision.FactorWeight{
			{Name: "ais_anomaly", Weight: 0.6}, {Name: "insurance_mismatch", Weight: 0.4},
		},
		FlagThreshold: 0.4, EscalateThreshold: 0.7,
	}
	base := map[string]float64{"ais_anomaly": 0.1, "insurance_mismatch": 0.1}
	override := map[string]float64{"ais_anomaly": 0.95, "insurance_mismatch": 0.9}

	result := SimulatePolicyEffect(twin, "dark_vessel", policy, base, override, 0.5, 4)
	if result.Counterfactual.Delta <= 0 {
		t.Fatalf("expected positive counterfactual delta, got %f", result.Counterfactual.Delta)
	}
	if len(result.Projection) != 4 {
		t.Fatalf("expected 4 projected points, got %d", len(result.Projection))
	}
	last := result.Projection[len(result.Projection)-1]
	if last.RiskScore < twin.Risk["dark_vessel"].RiskScore {
		t.Fatalf("expected projected risk to move toward higher counterfactual target, got %f", last.RiskScore)
	}
}

func TestComputeEconomicImpact(t *testing.T) {
	res := ComputeEconomicImpact(EconomicImpactInput{
		Channels: map[string]float64{
			"commodity_flow_usd": 1_000_000,
			"shipping_cost_usd":  200_000,
			"regional_trade_usd": 5_000_000,
		},
		RiskScore: 0.4,
	})
	wantExposure := 1_000_000.0 + 200_000.0 + 5_000_000.0
	if res.TotalExposure != wantExposure {
		t.Fatalf("expected total exposure %f, got %f", wantExposure, res.TotalExposure)
	}
	wantExpected := wantExposure * 0.4
	if res.TotalExpectedImpact != wantExpected {
		t.Fatalf("expected total expected impact %f, got %f", wantExpected, res.TotalExpectedImpact)
	}
	if len(res.Channels) != 3 {
		t.Fatalf("expected 3 channels, got %d", len(res.Channels))
	}
	// sorted by name
	if res.Channels[0].Channel != "commodity_flow_usd" {
		t.Fatalf("expected channels sorted alphabetically, got %s first", res.Channels[0].Channel)
	}
}

func TestComputeEconomicImpactZeroRisk(t *testing.T) {
	res := ComputeEconomicImpact(EconomicImpactInput{Channels: map[string]float64{"x": 100}, RiskScore: 0})
	if res.TotalExpectedImpact != 0 {
		t.Fatalf("expected zero expected impact at zero risk, got %f", res.TotalExpectedImpact)
	}
}
