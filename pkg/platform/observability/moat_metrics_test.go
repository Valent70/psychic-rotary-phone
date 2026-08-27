package observability

import "testing"

func TestMOATMetrics_RatioRoundTrip(t *testing.T) {
	m := NewMOATMetrics()
	m.SetIntentConfidence(0.83)
	m.SetTruthConfidence(0.5)
	m.SetDecisionConfidence(1.0)

	if got := m.IntentConfidence(); got < 0.829999 || got > 0.830001 {
		t.Fatalf("IntentConfidence round-trip = %v, want ~0.83", got)
	}
	if got := m.TruthConfidence(); got != 0.5 {
		t.Fatalf("TruthConfidence round-trip = %v, want 0.5", got)
	}
	if got := m.DecisionConfidence(); got != 1.0 {
		t.Fatalf("DecisionConfidence round-trip = %v, want 1.0", got)
	}
}

func TestMOATMetrics_RatioClamped(t *testing.T) {
	m := NewMOATMetrics()
	m.SetPolicyCompliance(1.7)      // out of range high
	m.SetEvidenceCompleteness(-0.4) // out of range low
	if got := m.PolicyCompliance(); got != 1.0 {
		t.Fatalf("PolicyCompliance not clamped: got %v, want 1.0", got)
	}
	if got := m.EvidenceCompleteness(); got != 0.0 {
		t.Fatalf("EvidenceCompleteness not clamped: got %v, want 0.0", got)
	}
}

func TestMOATMetrics_ReplaySuccessRatio(t *testing.T) {
	m := NewMOATMetrics()
	if got := m.ReplaySuccessRatio(); got != 0 {
		t.Fatalf("ReplaySuccessRatio with zero replays = %v, want 0 (not NaN, not fabricated)", got)
	}
	m.RecordReplay(true)
	m.RecordReplay(true)
	m.RecordReplay(false)
	m.RecordReplay(true)
	got := m.ReplaySuccessRatio()
	if got < 0.7499 || got > 0.7501 {
		t.Fatalf("ReplaySuccessRatio = %v, want 0.75 (3/4)", got)
	}
}

func TestMOATMetrics_StartStageProducesRealSpan(t *testing.T) {
	m := NewMOATMetrics()
	sp := m.StartStage(StageVerification)
	sp.SetTag("engine", "decision")
	sp.Finish()

	spans := m.Tracer.FinishedSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 finished span, got %d", len(spans))
	}
	if spans[0].Name != string(StageVerification) {
		t.Fatalf("span name = %q, want %q", spans[0].Name, StageVerification)
	}
	if spans[0].Tags["engine"] != "decision" {
		t.Fatalf("span tag not recorded: %+v", spans[0].Tags)
	}
	if spans[0].Duration() < 0 {
		t.Fatalf("span duration negative: %v", spans[0].Duration())
	}
}

func TestMOATMetrics_DashboardExposesAllNamedMetrics(t *testing.T) {
	m := NewMOATMetrics()
	m.SetIntentConfidence(0.9)
	m.SetEvidenceCompleteness(0.8)
	m.SetTruthConfidence(0.7)
	m.SetPolicyCompliance(0.6)
	m.SetVerificationScore(1.0)
	m.SetDecisionConfidence(0.65)
	m.SetTrustScore(0.55)
	m.RecordReplay(true)

	d := m.Dashboard()
	want := []string{
		"intent_confidence", "evidence_completeness", "truth_confidence",
		"policy_compliance", "verification_score", "decision_confidence",
		"trust_score", "replay_success_ratio",
	}
	for _, k := range want {
		if _, ok := d[k]; !ok {
			t.Fatalf("Dashboard missing key %q — every metric named in Kritik 8 / §6 must be present", k)
		}
	}
	if d["replay_success_ratio"] != 1.0 {
		t.Fatalf("replay_success_ratio = %v, want 1.0", d["replay_success_ratio"])
	}
}
