package simulation

import (
	"errors"
	"testing"

	"veriqo/pkg/moat/causal"
	"veriqo/pkg/moat/decision"
	"veriqo/pkg/moat/digitaltwin"
	"veriqo/pkg/moat/fusion"
)

const entity = digitaltwin.EntityID("VESSEL:IMO5555555")

func newEngine(t *testing.T) *Engine {
	t.Helper()
	c := causal.NewGraph()
	tw := digitaltwin.NewRegistry()
	if err := tw.ApplyArbitration("a", entity, fusion.ArbitrationResult{
		Claim:  fusion.Claim{Subject: string(entity), Predicate: "AIS_STATUS"},
		Winner: "OFF", WinnerConfidence: 0.8,
	}, 1); err != nil {
		t.Fatalf("ApplyArbitration: %v", err)
	}
	if err := tw.ApplyDecision("a", entity, decision.Decision{
		Subject: string(entity), PolicyName: "p", RiskScore: 0.6, Action: decision.Action("FLAG"),
	}, 2); err != nil {
		t.Fatalf("ApplyDecision: %v", err)
	}
	if _, err := c.Observe("a", "sanctions_listing", "claim:dark", 0.7, 0, "", 1); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if _, err := c.Observe("a", "ownership_opacity", "claim:dark", 0.4, 0, "", 2); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	return New(c, tw)
}

func params() Params { return Params{Horizon: 5, BaseTarget: 100, DecayPerStep: 0.1} }

func TestEverySimulationCarriesCompleteLineage(t *testing.T) {
	e := newEngine(t)
	dec, err := e.SimulateDecision(entity, "tbml", "claim:dark", params())
	if err != nil {
		t.Fatalf("SimulateDecision: %v", err)
	}
	iv, err := e.SimulateCausalIntervention(entity, "sanctions_listing", "claim:dark", 0.9, params())
	if err != nil {
		t.Fatalf("SimulateCausalIntervention: %v", err)
	}
	cf, err := e.SimulateCounterfactual(entity, "sanctions_listing", "claim:dark", params())
	if err != nil {
		t.Fatalf("SimulateCounterfactual: %v", err)
	}
	for name, r := range map[string]Result{"decision": dec, "intervention": iv, "counterfactual": cf} {
		if r.Cause == "" || r.Intervention == "" || len(r.Assumptions) == 0 ||
			len(r.AffectedNodes) == 0 || len(r.PredictedOutcome) == 0 ||
			len(r.Evidence) == 0 || r.ReplayID == "" || r.Confidence <= 0 {
			t.Fatalf("%s simulation emitted without complete lineage: %+v", name, r)
		}
		if err := VerifyResult(r); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestLineageIncompleteIsRefused(t *testing.T) {
	e := newEngine(t)
	if _, err := e.finalize(Result{Kind: KindDecision, Entity: "x"}); !errors.Is(err, ErrLineageIncomplete) {
		t.Fatal("a lineage-less simulation result was emitted")
	}
}

func TestInterventionRaisesAndCounterfactualLowersOutcome(t *testing.T) {
	e := newEngine(t)
	iv, err := e.SimulateCausalIntervention(entity, "sanctions_listing", "claim:dark", 0.9, params())
	if err != nil {
		t.Fatalf("intervention: %v", err)
	}
	if iv.Delta <= 0 {
		t.Fatalf("do(X) did not raise the projection: delta=%.6f", iv.Delta)
	}
	cf, err := e.SimulateCounterfactual(entity, "sanctions_listing", "claim:dark", params())
	if err != nil {
		t.Fatalf("counterfactual: %v", err)
	}
	if cf.Delta >= 0 {
		t.Fatalf("removing a real cause did not lower the projection: delta=%.6f", cf.Delta)
	}
}

func TestSimulationIsDeterministic(t *testing.T) {
	e := newEngine(t)
	a, err := e.SimulateDecision(entity, "tbml", "claim:dark", params())
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	b, err := e.SimulateDecision(entity, "tbml", "claim:dark", params())
	if err != nil {
		t.Fatalf("b: %v", err)
	}
	if a.ReplayID != b.ReplayID {
		t.Fatal("simulation is not deterministic")
	}
	tampered := a
	tampered.Delta = 999
	if VerifyResult(tampered) == nil {
		t.Fatal("tampered simulation result verified")
	}
}

func TestUnknownEntityAndZeroHorizonRejected(t *testing.T) {
	e := newEngine(t)
	if _, err := e.SimulateDecision("NOPE", "p", "claim:dark", params()); !errors.Is(err, ErrUnknownEntity) {
		t.Fatal("simulated an entity with no twin")
	}
	if _, err := e.SimulateDecision(entity, "p", "claim:dark", Params{Horizon: 0}); !errors.Is(err, ErrNoHorizon) {
		t.Fatal("zero horizon accepted")
	}
	if _, err := e.SimulateCounterfactual(entity, "", "claim:dark", params()); !errors.Is(err, ErrEmptyCause) {
		t.Fatal("counterfactual without a cause accepted")
	}
}

func TestConfidenceNeverReachesCertainty(t *testing.T) {
	e := newEngine(t)
	r, err := e.SimulateCausalIntervention(entity, "sanctions_listing", "claim:dark", 1.0, params())
	if err != nil {
		t.Fatalf("intervention: %v", err)
	}
	if r.Confidence >= 1 {
		t.Fatalf("simulation claimed certainty: %.6f", r.Confidence)
	}
}
