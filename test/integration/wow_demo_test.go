// TestWowDemoFullIntelligenceChain is the investor-facing "WOW" scenario
// the audit doc asks for explicitly (gap #10): not just
// "Scenario -> Risk -> Decision", but the full chain:
//
//	multi-source evidence -> KG -> Truth & Contradiction Engine
//	-> Hierarchical Bayesian correlation -> causal reasoning
//	-> Decision Intelligence (utility + % explanation)
//	-> Digital Twin -> future prediction -> economic impact
//
// The scenario data is synthetic and explicitly labeled as such (same
// discipline as dark_vessel_test.go) — this proves the layers COMPOSE
// correctly end-to-end, not that any specific number is a real-world
// forecast.
package integration

import (
	"testing"

	"veriqo/pkg/moat/contradiction"
	"veriqo/pkg/moat/decision"
	"veriqo/pkg/moat/digitaltwin"
	"veriqo/pkg/moat/fusion"
	"veriqo/pkg/moat/fusion/hbayes"
	"veriqo/pkg/moat/kg"
	"veriqo/pkg/moat/temporal"
)

func TestWowDemoFullIntelligenceChain(t *testing.T) {
	const vessel = "VESSEL:IMO9998887"

	// --- Step 1: multiple sources assert conflicting AIS status -------------
	graph := kg.NewGraph()
	fusionEngine := fusion.NewEngine(graph)
	must(t, fusionEngine.RegisterSource(fusion.SourceProfile{ID: "ais-vendor-a", BaseReliability: 0.8, Provider: "sat-op-1"}))
	must(t, fusionEngine.RegisterSource(fusion.SourceProfile{ID: "ais-vendor-b", BaseReliability: 0.8, Provider: "sat-op-1"})) // same upstream feed as vendor-a
	must(t, fusionEngine.RegisterSource(fusion.SourceProfile{ID: "port-authority", BaseReliability: 0.95, Provider: ""}))

	aisClaim := fusion.Claim{Subject: vessel, Predicate: "AIS_STATUS"}
	_, err := fusionEngine.Submit("ais-vendor-a", aisClaim, "OFF", 1)
	must(t, err)
	_, err = fusionEngine.Submit("ais-vendor-b", aisClaim, "OFF", 1)
	must(t, err)
	_, err = fusionEngine.Submit("port-authority", aisClaim, "ON", 1)
	must(t, err)

	arb, err := fusionEngine.Arbitrate("collector", aisClaim, 1)
	must(t, err)
	if !arb.Contradiction {
		t.Fatalf("expected a genuine contradiction between AIS vendors and port authority")
	}

	// --- Step 2: Truth & Contradiction Intelligence Engine ------------------
	// Feed the arbitration outcome into the contradiction engine to build
	// the conflict graph / source arbitration ledger / truth evolution.
	contra := contradiction.NewEngine()
	evidence := fusionEngine.EvidenceFor(aisClaim)
	var winningSources, losingSources []string
	for _, ev := range evidence {
		if ev.Value == arb.Winner {
			winningSources = append(winningSources, string(ev.Source))
		} else {
			losingSources = append(losingSources, string(ev.Source))
		}
	}
	_, err = contra.Ingest(contradiction.Observation{
		ClaimKey: aisClaim.Key(), Winner: arb.Winner, WinnerConfidence: arb.WinnerConfidence,
		RunnerUp: arb.RunnerUp, RunnerUpConfidence: arb.RunnerUpConfidence, Contradiction: arb.Contradiction,
		WinningSources: winningSources, LosingSources: losingSources, Tick: 1,
	})
	must(t, err)
	active := contra.ActiveConflicts(0.3)
	if len(active) != 1 {
		t.Fatalf("expected the AIS_STATUS claim to be an active conflict, got %d", len(active))
	}

	// --- Step 3: Hierarchical Bayesian correlation ---------------------------
	// Two AIS vendors resell the SAME upstream feed (sat-op-1) — a naive
	// count would treat them as 2 independent confirmations; the
	// hierarchical model must discount that.
	model, err := hbayes.NewModel(
		[]hbayes.ReliabilityPrior{{GroupID: "AIS_RESELLERS", Mean: 0.8, Variance: 0.02}},
		hbayes.CorrelationMatrix{Groups: []hbayes.ProviderGroupID{"AIS_RESELLERS"}, Values: [][]float64{{1.0}}},
	)
	must(t, err)
	risk, err := model.ComputeEventRisk(
		[]hbayes.Signal{
			{SourceID: "ais-vendor-a", Group: "AIS_RESELLERS", Confirms: true, Reliability: 0.8},
			{SourceID: "ais-vendor-b", Group: "AIS_RESELLERS", Confirms: true, Reliability: 0.8},
		},
		map[hbayes.ProviderGroupID]float64{"AIS_RESELLERS": 0.9}, // near-total intra-group correlation
	)
	must(t, err)
	if risk.EffectiveIndependentSignals >= 2.0 {
		t.Fatalf("expected correlated vendor signals to saturate below 2.0 effective independent signals, got %f",
			risk.EffectiveIndependentSignals)
	}

	// --- Step 4: bitemporal correction ---------------------------------------
	// Port authority later issues a correction: the AIS_STATUS claim they
	// backed was itself based on stale data — Veriqo must preserve BOTH
	// what was believed and the correction (time-travel query).
	temporalEngine := temporal.NewEngine()
	factID := temporal.NewFactID(vessel, "AIS_STATUS")
	_, err = temporalEngine.Assert(temporal.Version{Fact: factID, Value: arb.Winner, ValidFrom: 1, TxTick: 1, ActorID: "collector"})
	must(t, err)
	_, err = temporalEngine.Assert(temporal.Version{Fact: factID, Value: "OFF", ValidFrom: 1, TxTick: 3, ActorID: "port-authority-correction"})
	must(t, err)
	corrected := temporalEngine.AsOf(1)
	if corrected[factID].Value != "OFF" {
		t.Fatalf("expected corrected belief OFF after temporal correction, got %+v", corrected[factID])
	}
	if len(temporalEngine.Corrections(factID)) != 1 {
		t.Fatalf("expected the correction to be detected in the bitemporal history")
	}

	// --- Step 5: Decision Intelligence — risk score + % explanation ---------
	policy := decision.Policy{
		Name: "dark_vessel_wow",
		Factors: []decision.FactorWeight{
			{Name: "ais_anomaly", Weight: 0.45},
			{Name: "correlation_adjusted_confirmation", Weight: 0.30},
			{Name: "historical_behavior", Weight: 0.25},
		},
		FlagThreshold: 0.4, EscalateThreshold: 0.7,
	}
	decisionEngine := decision.NewEngine()
	must(t, decisionEngine.RegisterPolicy(policy))
	values := map[string]float64{
		"ais_anomaly":                       arb.WinnerConfidence,
		"correlation_adjusted_confirmation": risk.Probability,
		"historical_behavior":               0.4,
	}
	dec, err := decisionEngine.Decide("wow-demo", vessel, policy.Name, values, 1)
	must(t, err)
	breakdown := decision.ExplainBreakdown(policy, values)
	if len(breakdown) != 3 {
		t.Fatalf("expected 3-factor explanation breakdown, got %d", len(breakdown))
	}
	sumPct := 0.0
	for _, b := range breakdown {
		sumPct += b.PercentOfTotal
	}
	if sumPct < 99.9 || sumPct > 100.1 {
		t.Fatalf("explanation percentages should sum to ~100, got %f", sumPct)
	}

	// --- Step 6: Digital Twin — update, then simulate future + economic impact
	twinRegistry := digitaltwin.NewRegistry()
	entity := digitaltwin.EntityID(vessel)
	must(t, twinRegistry.ApplyArbitration("wow-demo", entity, arb, 1))
	must(t, twinRegistry.ApplyDecision("wow-demo", entity, dec, 1))
	twin, ok := twinRegistry.Get(entity)
	if !ok {
		t.Fatalf("expected twin to exist after updates")
	}

	projection := digitaltwin.Simulate(twin, policy.Name, digitaltwin.SimulationParams{
		TargetRiskScore: 0.95, ApproachRate: 0.4, HorizonTicks: 6,
	})
	if len(projection) != 6 {
		t.Fatalf("expected 6-tick future projection, got %d", len(projection))
	}
	if projection[len(projection)-1].RiskScore <= twin.Risk[policy.Name].RiskScore {
		t.Fatalf("expected future projection to trend toward higher target risk")
	}

	econ := digitaltwin.ComputeEconomicImpact(digitaltwin.EconomicImpactInput{
		Channels: map[string]float64{
			"commodity_flow_usd": 2_500_000,
			"shipping_cost_usd":  150_000,
		},
		RiskScore: twin.Risk[policy.Name].RiskScore,
	})
	if econ.TotalExpectedImpact <= 0 {
		t.Fatalf("expected nonzero expected economic impact given nonzero risk score")
	}

	// --- Final assertion: every layer's replay/verify chain is clean --------
	must(t, fusionEngine.VerifyChain())
	must(t, contra.VerifyChain())
	must(t, temporalEngine.VerifyChain())
	must(t, decisionEngine.VerifyChain())
	must(t, twinRegistry.VerifyChain())

	t.Logf("WOW demo result: winner=%s confidence=%.3f contradiction=%t hbayes_prob=%.3f effective_signals=%.2f decision_risk=%.3f action=%s future_risk=%.3f expected_econ_impact=%.0f",
		arb.Winner, arb.WinnerConfidence, arb.Contradiction, risk.Probability, risk.EffectiveIndependentSignals,
		dec.RiskScore, dec.Action, projection[len(projection)-1].RiskScore, econ.TotalExpectedImpact)
}
