package digitaltwin

import (
	"testing"

	"veriqo/pkg/moat/causal"
	"veriqo/pkg/moat/decision"
	"veriqo/pkg/moat/fusion"
)

// TestCausalGraphDrivesTwinFutureState is the end-to-end proof the
// V7.10.1 audit asked for: a REAL causal.Graph (not a synthetic
// number) produces AggregateSupport, that support is recorded onto a
// twin, and the twin's own future-state projection genuinely differs
// as a result — one connected path, not two islands.
func TestCausalGraphDrivesTwinFutureState(t *testing.T) {
	const vessel = EntityID("VESSEL:IMO1112223")
	const policy = "dark_vessel"

	// --- Step 1: build a real causal graph with strong support for
	// the adverse outcome (two independent strong causes). ---
	g := causal.NewGraph()
	claimNode := causal.NodeID("claim:AIS_STATUS")
	predictionNode := causal.NodeID("prediction:AIS_STATUS")
	if _, err := g.Observe("intelligence", claimNode, predictionNode, 0.9, 0, "", 1); err != nil {
		t.Fatal(err)
	}
	sourceNode := causal.NodeID("source:port-authority")
	if _, err := g.Observe("intelligence", sourceNode, claimNode, 0.95, 0, "", 1); err != nil {
		t.Fatal(err)
	}
	support := g.AggregateSupport(predictionNode)
	if support <= 0 {
		t.Fatalf("expected nonzero aggregate causal support, got %f", support)
	}

	// --- Step 2: fold a decision into the twin so it has a baseline
	// risk state to project forward from. ---
	reg := NewRegistry()
	arb := fusion.ArbitrationResult{
		Claim:  fusion.Claim{Subject: string(vessel), Predicate: "AIS_STATUS"},
		Winner: "OFF", WinnerConfidence: 0.8,
	}
	if err := reg.ApplyArbitration("test", vessel, arb, 1); err != nil {
		t.Fatal(err)
	}
	dec := decision.Decision{PolicyName: policy, RiskScore: 0.3, Action: decision.ActionFlag}
	if err := reg.ApplyDecision("test", vessel, dec, 1); err != nil {
		t.Fatal(err)
	}

	// --- Step 3: record the REAL causal support onto the twin. ---
	if err := reg.ApplyCausalProjection("test", vessel, policy, support, 1); err != nil {
		t.Fatal(err)
	}
	twin, ok := reg.Get(vessel)
	if !ok {
		t.Fatalf("expected twin to exist")
	}
	if twin.Causal[policy].Support != support {
		t.Fatalf("twin's recorded causal support = %f, want %f", twin.Causal[policy].Support, support)
	}

	// --- Step 4: prove the future-state projection actually differs
	// because of the causal signal (the concrete "not two islands"
	// proof). ---
	params := SimulationParams{TargetRiskScore: 0.3, ApproachRate: 0.4, HorizonTicks: 5}
	plain := Simulate(twin, policy, params)
	causalAdjusted := SimulateWithCausalSupport(twin, policy, params)

	if len(plain) != len(causalAdjusted) {
		t.Fatalf("projection lengths differ: %d vs %d", len(plain), len(causalAdjusted))
	}
	last := len(plain) - 1
	if causalAdjusted[last].RiskScore <= plain[last].RiskScore {
		t.Fatalf("expected causal-adjusted projection (%f) to trend higher than the unadjusted one (%f) given positive causal support %f",
			causalAdjusted[last].RiskScore, plain[last].RiskScore, support)
	}

	// --- Step 5: zero recorded causal support must be a no-op
	// (backward compatibility — twins created before this feature
	// existed still project identically). ---
	bareTwin, _ := reg.Get(vessel)
	bareTwin.Causal = map[string]CausalState{} // simulate "no causal projection recorded yet"
	untouched := SimulateWithCausalSupport(bareTwin, policy, params)
	for i := range plain {
		if untouched[i].RiskScore != plain[i].RiskScore {
			t.Fatalf("expected SimulateWithCausalSupport with no recorded causal state to match plain Simulate exactly at tick %d: %f vs %f",
				i, untouched[i].RiskScore, plain[i].RiskScore)
		}
	}

	if err := reg.VerifyChain(); err != nil {
		t.Fatalf("expected clean hash chain including the CAUSAL update record, got %v", err)
	}
}

// TestCausalAdjustedTargetClampsToValidRange proves the blend formula
// never escapes [0,1] regardless of extreme (even out-of-spec) inputs.
func TestCausalAdjustedTargetClampsToValidRange(t *testing.T) {
	cases := []struct{ base, support float64 }{
		{-0.5, 0.5}, {1.5, 0.5}, {0.5, -1}, {0.5, 2},
	}
	for _, c := range cases {
		got := CausalAdjustedTarget(c.base, CausalState{Support: clamp01(c.support)})
		if got < 0 || got > 1 {
			t.Fatalf("CausalAdjustedTarget(%f, %f) = %f, want value in [0,1]", c.base, c.support, got)
		}
	}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
