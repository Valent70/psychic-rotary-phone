package engine

import (
	"testing"

	"veriqo/pkg/moat/contradiction"
	"veriqo/pkg/moat/decision"
)

func TestKernelRegisterRejectsDuplicate(t *testing.T) {
	k := NewKernel()
	adapter, err := NewDecisionPolicyEngine(testPolicy(), "test")
	must(t, err)
	must(t, k.Register(adapter))
	if err := k.Register(adapter); err == nil {
		t.Fatalf("expected duplicate registration error")
	}
}

func TestKernelRunUnknownEngineErrors(t *testing.T) {
	k := NewKernel()
	if _, err := k.Run("nonexistent", Context{Subject: "x"}); err == nil {
		t.Fatalf("expected ErrUnknownEngine")
	}
}

func testPolicy() decision.Policy {
	return decision.Policy{
		Name: "test_policy",
		Factors: []decision.FactorWeight{
			{Name: "ais_anomaly", Weight: 0.6}, {Name: "insurance_mismatch", Weight: 0.4},
		},
		FlagThreshold: 0.4, EscalateThreshold: 0.7,
	}
}

func TestDecisionAdapterRunsThroughFullLifecycle(t *testing.T) {
	k := NewKernel()
	adapter, err := NewDecisionPolicyEngine(testPolicy(), "orchestrator")
	must(t, err)
	must(t, k.Register(adapter))
	must(t, k.InitializeAll())

	rec, err := k.Run("decision", Context{
		Subject: "VESSEL:1", Tick: 1,
		Values: map[string]float64{"ais_anomaly": 0.9, "insurance_mismatch": 0.8},
	})
	must(t, err)
	if rec.Result.Score <= 0 {
		t.Fatalf("expected nonzero risk score, got %f", rec.Result.Score)
	}
	if !rec.Replayable {
		t.Fatalf("expected decision engine to report replayable after a clean run")
	}
	if len(k.Runs()) != 1 {
		t.Fatalf("expected 1 run recorded in kernel audit trail")
	}
}

func TestContradictionAdapterRunsThroughFullLifecycle(t *testing.T) {
	k := NewKernel()
	adapter := NewContradictionArbitrationEngine(50)
	must(t, k.Register(adapter))

	must(t, adapter.Observe(contradiction.RawObservation{
		ClaimKey: "VESSEL:1|FLAG", SourceID: "ais-a", Value: "PANAMA", SourceReliability: 0.9, Tick: 1,
	}))
	must(t, adapter.Observe(contradiction.RawObservation{
		ClaimKey: "VESSEL:1|FLAG", SourceID: "ais-b", Value: "PANAMA", SourceReliability: 0.85, Tick: 1,
	}))
	must(t, adapter.Observe(contradiction.RawObservation{
		ClaimKey: "VESSEL:1|FLAG", SourceID: "port", Value: "LIBERIA", SourceReliability: 0.95, Tick: 1,
	}))

	rec, err := k.Run("contradiction", Context{Subject: "VESSEL:1|FLAG", Tick: 1})
	must(t, err)
	if rec.Result.Score <= 0 {
		t.Fatalf("expected nonzero confidence score, got %f", rec.Result.Score)
	}
	if len(rec.Result.Evidence) != 1 {
		t.Fatalf("expected 1 evidence record in result, got %d", len(rec.Result.Evidence))
	}
	if !rec.Replayable {
		t.Fatalf("expected contradiction engine to report replayable after a clean run")
	}
}

func TestRunAllExecutesEveryRegisteredEngine(t *testing.T) {
	k := NewKernel()
	decisionAdapter, err := NewDecisionPolicyEngine(testPolicy(), "orchestrator")
	must(t, err)
	must(t, k.Register(decisionAdapter))
	contraAdapter := NewContradictionArbitrationEngine(50)
	must(t, k.Register(contraAdapter))
	must(t, contraAdapter.Observe(contradiction.RawObservation{
		ClaimKey: "VESSEL:1", SourceID: "s1", Value: "A", SourceReliability: 0.9, Tick: 1,
	}))

	records, errs := k.RunAll(Context{
		Subject: "VESSEL:1", Tick: 1,
		Values: map[string]float64{"ais_anomaly": 0.5, "insurance_mismatch": 0.5},
	})
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 engines to run, got %d", len(records))
	}
	names := k.EngineNames()
	if len(names) != 2 || names[0] != "contradiction" || names[1] != "decision" {
		t.Fatalf("expected sorted engine names [contradiction decision], got %v", names)
	}
}

func TestRunAllContinuesPastOneEngineFailure(t *testing.T) {
	k := NewKernel()
	// contradiction adapter with NO observations fed -> will fail Evaluate
	failingAdapter := NewContradictionArbitrationEngine(50)
	must(t, k.Register(failingAdapter))
	decisionAdapter, err := NewDecisionPolicyEngine(testPolicy(), "orchestrator")
	must(t, err)
	must(t, k.Register(decisionAdapter))

	records, errs := k.RunAll(Context{
		Subject: "VESSEL:1", Tick: 1,
		Values: map[string]float64{"ais_anomaly": 0.5, "insurance_mismatch": 0.5},
	})
	if len(errs) != 1 {
		t.Fatalf("expected exactly 1 engine to fail, got %v", errs)
	}
	if len(records) != 1 {
		t.Fatalf("expected the other engine to still succeed, got %d records", len(records))
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
