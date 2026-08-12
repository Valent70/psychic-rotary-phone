// Package integration contains Veriqo's first real-world-style,
// end-to-end MOAT demonstration — the deliverable Yang_masih_kurang.docx
// asks for explicitly (§4.5): "a storyboard that runs ingest -> fusion
// -> arbitration -> causal analysis -> decision recommendation -> twin
// update" as one executable trace, not just a report claim.
//
// Scenario: a vessel goes dark, reappears under SAR imagery in a
// different location, changes beneficial owner, and lets its insurance
// lapse — four independently-sourced signals that, chained causally,
// should raise a single vessel from "unremarkable" to "escalate for
// interdiction review". The data here is synthetic (clearly labeled),
// not a live feed — that distinction is stated honestly rather than
// implied.
package integration

import (
	"testing"

	"veriqo/pkg/moat/causal"
	"veriqo/pkg/moat/decision"
	"veriqo/pkg/moat/digitaltwin"
	"veriqo/pkg/moat/fusion"
	"veriqo/pkg/moat/kg"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDarkVesselEndToEndScenario(t *testing.T) {
	const vessel = digitaltwin.EntityID("VESSEL:IMO7778889")

	// --- Layer 1: fusion + KG -------------------------------------------------
	graph := kg.NewGraph()
	fusionEngine := fusion.NewEngine(graph)
	must(t, fusionEngine.RegisterSource(fusion.SourceProfile{ID: "ais-vendor-a", BaseReliability: 0.85}))
	must(t, fusionEngine.RegisterSource(fusion.SourceProfile{ID: "port-monitor", BaseReliability: 0.95}))
	must(t, fusionEngine.RegisterSource(fusion.SourceProfile{ID: "sar-imagery", BaseReliability: 0.85}))
	must(t, fusionEngine.RegisterSource(fusion.SourceProfile{ID: "sanctions-registry", BaseReliability: 0.97}))
	must(t, fusionEngine.RegisterSource(fusion.SourceProfile{ID: "insurance-registry", BaseReliability: 0.97}))

	aisStatus := fusion.Claim{Subject: string(vessel), Predicate: "AIS_STATUS"}
	_, err := fusionEngine.Submit("port-monitor", aisStatus, "OFF", 5)
	must(t, err)
	aisRes, err := fusionEngine.Arbitrate("collector-agent", aisStatus, 5)
	must(t, err)
	if aisRes.Winner != "OFF" {
		t.Fatalf("expected AIS_STATUS=OFF, got %q", aisRes.Winner)
	}

	sarClaim := fusion.Claim{Subject: string(vessel), Predicate: "SAR_POSITION_MATCH"}
	_, err = fusionEngine.Submit("sar-imagery", sarClaim, "MISMATCH", 6)
	must(t, err)
	sarRes, err := fusionEngine.Arbitrate("collector-agent", sarClaim, 6)
	must(t, err)

	ownerClaim := fusion.Claim{Subject: string(vessel), Predicate: "BENEFICIAL_OWNER"}
	_, err = fusionEngine.Submit("sanctions-registry", ownerClaim, "SHELLCORP-Y", 10)
	must(t, err)
	ownerRes, err := fusionEngine.Arbitrate("collector-agent", ownerClaim, 10)
	must(t, err)

	insClaim := fusion.Claim{Subject: string(vessel), Predicate: "INSURANCE_STATUS"}
	_, err = fusionEngine.Submit("insurance-registry", insClaim, "EXPIRED", 12)
	must(t, err)
	insRes, err := fusionEngine.Arbitrate("collector-agent", insClaim, 12)
	must(t, err)

	if _, err := kg.ReplayAll(graph.Snapshot()); err != nil {
		t.Fatalf("kg chain broken: %v", err)
	}

	// --- Layer 2: causal chain --------------------------------------------------
	causalGraph := causal.NewGraph()
	_, err = causalGraph.Observe("causal-agent", "AIS_OFF", "SAR_MISMATCH", 0.8, 1, "", 6)
	must(t, err)
	_, err = causalGraph.Observe("causal-agent", "SAR_MISMATCH", "OWNERSHIP_CHANGE", 0.6, 4, "", 10)
	must(t, err)
	_, err = causalGraph.Observe("causal-agent", "OWNERSHIP_CHANGE", "INSURANCE_LAPSE", 0.5, 2, "", 12)
	must(t, err)

	paths, err := causalGraph.Why("INSURANCE_LAPSE")
	must(t, err)
	if len(paths) != 1 || paths[0].Chain[0] != "AIS_OFF" {
		t.Fatalf("expected one causal chain rooted at AIS_OFF, got %+v", paths)
	}
	t.Logf("causal root-cause chain: %v (combined strength %.4f)", paths[0].Chain, paths[0].Strength)

	// --- Layer 3: decision -------------------------------------------------------
	decisionEngine := decision.NewEngine()
	must(t, decisionEngine.RegisterPolicy(decision.Policy{
		Name: "dark_vessel_risk",
		Factors: []decision.FactorWeight{
			{Name: "ais_anomaly", Weight: 3},
			{Name: "sar_mismatch", Weight: 2},
			{Name: "ownership_change", Weight: 2},
			{Name: "insurance_lapse", Weight: 2},
			{Name: "causal_chain_strength", Weight: 3},
		},
		FlagThreshold:     0.4,
		EscalateThreshold: 0.7,
	}))

	aisAnomalySignal := 0.0
	if aisRes.Winner == "OFF" {
		aisAnomalySignal = aisRes.WinnerConfidence
	}
	sarSignal := 0.0
	if sarRes.Winner == "MISMATCH" {
		sarSignal = sarRes.WinnerConfidence
	}

	d, err := decisionEngine.Decide("decision-agent", string(vessel), "dark_vessel_risk", map[string]float64{
		"ais_anomaly":           aisAnomalySignal,
		"sar_mismatch":          sarSignal,
		"ownership_change":      ownerRes.WinnerConfidence,
		"insurance_lapse":       insRes.WinnerConfidence,
		"causal_chain_strength": paths[0].Strength,
	}, 15)
	must(t, err)

	t.Logf("decision: risk_score=%.4f action=%s", d.RiskScore, d.Action)
	for _, line := range d.Explanation {
		t.Log(line)
	}
	if d.Action != decision.ActionEscalate {
		t.Fatalf("expected converging evidence to escalate, got action=%s risk=%.4f", d.Action, d.RiskScore)
	}

	// --- Layer 4: digital twin ----------------------------------------------------
	twinRegistry := digitaltwin.NewRegistry()
	must(t, twinRegistry.ApplyArbitration("twin-agent", vessel, aisRes, 5))
	must(t, twinRegistry.ApplyArbitration("twin-agent", vessel, sarRes, 6))
	must(t, twinRegistry.ApplyArbitration("twin-agent", vessel, ownerRes, 10))
	must(t, twinRegistry.ApplyArbitration("twin-agent", vessel, insRes, 12))
	must(t, twinRegistry.ApplyDecision("twin-agent", vessel, d, 15))

	twin, ok := twinRegistry.Get(vessel)
	if !ok {
		t.Fatalf("expected twin to exist for %s", vessel)
	}
	if twin.Attributes["AIS_STATUS"].Value != "OFF" {
		t.Fatalf("twin AIS_STATUS = %+v, want OFF", twin.Attributes["AIS_STATUS"])
	}
	if twin.Attributes["BENEFICIAL_OWNER"].Value != "SHELLCORP-Y" {
		t.Fatalf("twin BENEFICIAL_OWNER = %+v, want SHELLCORP-Y", twin.Attributes["BENEFICIAL_OWNER"])
	}
	if twin.Risk["dark_vessel_risk"].Action != decision.ActionEscalate {
		t.Fatalf("twin risk state = %+v, want ESCALATE", twin.Risk["dark_vessel_risk"])
	}

	// --- Replay consistency across every layer, end to end ------------------------
	rebuiltTwins, err := digitaltwin.Rebuild(twinRegistry.ReplayAll())
	must(t, err)
	rebuiltTwin, ok := rebuiltTwins.Get(vessel)
	if !ok || rebuiltTwin.Risk["dark_vessel_risk"] != twin.Risk["dark_vessel_risk"] {
		t.Fatalf("twin state not reconstructable from log alone: rebuilt=%+v orig=%+v", rebuiltTwin, twin)
	}
	rebuiltCausal, err := causal.Rebuild(causalGraph.ReplayAll())
	must(t, err)
	if rebuiltCausal.Head() != causalGraph.Head() {
		t.Fatalf("causal graph not reconstructable: rebuilt head %s != %s", rebuiltCausal.Head(), causalGraph.Head())
	}
	if err := decisionEngine.VerifyChain(); err != nil {
		t.Fatalf("decision chain corrupted: %v", err)
	}
}
