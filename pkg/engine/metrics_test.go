package engine

import (
	"testing"

	"veriqo/pkg/platform/observability"
)

func TestKernelRun_WithMetricsRecordsDecisionConfidenceAndLatency(t *testing.T) {
	k := NewKernel()
	m := observability.NewMOATMetrics()
	k.SetMetrics(m)

	adapter, err := NewDecisionPolicyEngine(testPolicy(), "orchestrator")
	must(t, err)
	must(t, k.Register(adapter))
	must(t, k.InitializeAll())

	rec, err := k.Run("decision", Context{
		Subject: "VESSEL:1", Tick: 1,
		Values: map[string]float64{"ais_anomaly": 0.9, "insurance_mismatch": 0.8},
	})
	must(t, err)

	// MOATMetrics stores ratios through a fixed-point int64 gauge (see
	// moat_metrics.go's ratioScale), so the round-trip is exact to
	// 1e-6, not bit-for-bit float equality.
	if got, want := m.DecisionConfidence(), clamp01(rec.Result.Score); got < want-1e-6 || got > want+1e-6 {
		t.Fatalf("DecisionConfidence = %v, want %v (clamped Result.Score)", got, want)
	}
	spans := m.Tracer.FinishedSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 finished latency span after Run, got %d", len(spans))
	}
	if spans[0].Name != string(observability.StageDecision) {
		t.Fatalf("span stage = %q, want %q", spans[0].Name, observability.StageDecision)
	}
	if spans[0].Tags["engine"] != "decision" {
		t.Fatalf("span missing engine tag: %+v", spans[0].Tags)
	}
}

func TestKernelRun_WithoutMetricsIsUnaffected(t *testing.T) {
	// No SetMetrics call at all — the zero-value nil metrics must not
	// change behavior or panic; this is the regression guard for every
	// pre-existing engine test that never calls SetMetrics.
	k := NewKernel()
	adapter, err := NewDecisionPolicyEngine(testPolicy(), "orchestrator")
	must(t, err)
	must(t, k.Register(adapter))
	must(t, k.InitializeAll())
	if _, err := k.Run("decision", Context{Subject: "V", Tick: 1, Values: map[string]float64{"ais_anomaly": 0.5, "insurance_mismatch": 0.5}}); err != nil {
		t.Fatalf("Run without metrics attached should behave exactly as before: %v", err)
	}
}

func TestKernelRunAndVerify_WithMetricsRecordsVerificationScoreAndReplay(t *testing.T) {
	k := NewKernel()
	m := observability.NewMOATMetrics()
	k.SetMetrics(m)

	adapter, err := NewDecisionPolicyEngine(testPolicy(), "orchestrator")
	must(t, err)
	must(t, k.Register(adapter))
	must(t, k.InitializeAll())

	vrec, err := k.RunAndVerify("decision", Context{
		Subject: "VESSEL:2", Tick: 1,
		Values: map[string]float64{"ais_anomaly": 0.9, "insurance_mismatch": 0.8},
	})
	must(t, err)
	if vrec.VerificationResult == nil {
		t.Fatalf("expected a real VerificationResult for an IVFCapable adapter")
	}
	if !vrec.VerificationResult.ManifestValid || !vrec.VerificationResult.ReplayValid {
		t.Fatalf("expected a genuine pass on an untampered run: %+v", vrec.VerificationResult)
	}
	if got := m.VerificationScore(); got != 1.0 {
		t.Fatalf("VerificationScore = %v, want 1.0 after a genuine IVF pass", got)
	}
	if got := m.ReplaySuccessRatio(); got != 1.0 {
		t.Fatalf("ReplaySuccessRatio = %v, want 1.0 after one successful replay", got)
	}

	found := false
	for _, sp := range m.Tracer.FinishedSpans() {
		if sp.Name == string(observability.StageVerification) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a finished %q stage span after RunAndVerify", observability.StageVerification)
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
