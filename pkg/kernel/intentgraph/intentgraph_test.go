package intentgraph

import (
	"testing"

	"veriqo/pkg/platform/observability"
)

func TestPredict_HighReliabilityActorPredictsFulfillment(t *testing.T) {
	g := NewGraph(nil)
	err := g.Register(PendingIntent{
		IntentID: "i1", ActorID: "operator-A", Subject: "vessel-9001",
		ProposedAction: "TRANSIT_NORMAL", ActorReliability: 0.92, Tick: 10,
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	res, err := g.Predict("vessel-9001")
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if !res.HasDerivation || !res.PredictedFulfilled {
		t.Fatalf("expected a fulfilled prediction for a high-reliability actor, got %+v", res)
	}
	if res.Derivation.RuleName != "high_reliability_predicts_fulfillment" {
		t.Fatalf("derivation trail missing/wrong rule: %+v", res.Derivation)
	}
	// The derivation must be auditable, not opaque: premises must
	// name the actual actor and reliability tier that fired.
	if len(res.Derivation.Premises) != 2 {
		t.Fatalf("expected 2 premises in derivation trace, got %d: %+v", len(res.Derivation.Premises), res.Derivation.Premises)
	}
}

func TestPredict_LowReliabilityActorHasNoDerivation(t *testing.T) {
	g := NewGraph(nil)
	if err := g.Register(PendingIntent{
		IntentID: "i2", ActorID: "operator-B", Subject: "vessel-9002",
		ProposedAction: "TRANSIT_NORMAL", ActorReliability: 0.3, Tick: 10,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	res, err := g.Predict("vessel-9002")
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if res.HasDerivation {
		t.Fatalf("expected no fulfillment prediction for a low-reliability actor (honest 'no prediction'), got %+v", res)
	}
}

func TestConflicts_DetectsTwoActorsDifferentActionSameSubject(t *testing.T) {
	g := NewGraph(nil)
	subject := "vessel-DARK-01"
	if err := g.Register(PendingIntent{IntentID: "i3", ActorID: "operator-A", Subject: subject, ProposedAction: "TRANSIT_NORMAL", ActorReliability: 0.9, Tick: 1}); err != nil {
		t.Fatal(err)
	}
	if g.Conflicts(subject) {
		t.Fatalf("expected no conflict with only one intent registered")
	}
	if err := g.Register(PendingIntent{IntentID: "i4", ActorID: "operator-C", Subject: subject, ProposedAction: "AIS_GAP_SUSPICIOUS", ActorReliability: 0.4, Tick: 1}); err != nil {
		t.Fatal(err)
	}
	if !g.Conflicts(subject) {
		t.Fatalf("expected a conflict once two actors proposed different actions for the same subject")
	}
	if got := len(g.PendingFor(subject)); got != 2 {
		t.Fatalf("PendingFor = %d intents, want 2", got)
	}
}

func TestArbitrate_HigherReliabilityActorWinsDeterministically(t *testing.T) {
	m := observability.NewMOATMetrics()
	g := NewGraph(m)
	subject := "vessel-DARK-02"
	// Two operators disagree about the same vessel's status — the
	// exact "dark vessel" conflict shape this project's own
	// integration tests use elsewhere (test/integration/dark_vessel_test.go).
	if err := g.Register(PendingIntent{IntentID: "i5", ActorID: "reliable-operator", Subject: subject, ProposedAction: "TRANSIT_NORMAL", ActorReliability: 0.95, Tick: 100}); err != nil {
		t.Fatal(err)
	}
	if err := g.Register(PendingIntent{IntentID: "i6", ActorID: "unreliable-operator", Subject: subject, ProposedAction: "SANCTIONS_EVASION", ActorReliability: 0.2, Tick: 100}); err != nil {
		t.Fatal(err)
	}

	tv, err := g.Arbitrate(subject, 100, 1000)
	if err != nil {
		t.Fatalf("Arbitrate: %v", err)
	}
	if tv.Value != "TRANSIT_NORMAL" {
		t.Fatalf("arbitration winner = %q, want TRANSIT_NORMAL (the higher-reliability actor's proposal)", tv.Value)
	}
	if tv.Confidence <= 0.5 {
		t.Fatalf("winner confidence = %v, expected the higher-reliability actor to clearly dominate", tv.Confidence)
	}

	// The arbitration must have actually recorded IntentConfidence to
	// the metrics surface — not just returned a value nobody reads.
	if got := m.IntentConfidence(); got <= 0 {
		t.Fatalf("MOATMetrics.IntentConfidence() = %v after a real arbitration, want > 0", got)
	}

	// And the intent latency stage must have produced a real span.
	spans := m.Tracer.FinishedSpans()
	if len(spans) == 0 {
		t.Fatalf("expected at least one finished 'intent' stage span after Arbitrate")
	}
}

func TestArbitrate_NoConflictYieldsMaximalIntentConfidence(t *testing.T) {
	m := observability.NewMOATMetrics()
	g := NewGraph(m)
	subject := "vessel-CLEAN-01"
	if err := g.Register(PendingIntent{IntentID: "i7", ActorID: "solo-operator", Subject: subject, ProposedAction: "TRANSIT_NORMAL", ActorReliability: 0.8, Tick: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Arbitrate(subject, 1, 1000); err != nil {
		t.Fatalf("Arbitrate: %v", err)
	}
	if got := m.IntentConfidence(); got != 1.0 {
		t.Fatalf("IntentConfidence with no conflict = %v, want 1.0", got)
	}
}

func TestRegister_RejectsIncompleteIntent(t *testing.T) {
	g := NewGraph(nil)
	if err := g.Register(PendingIntent{IntentID: "", ActorID: "a", Subject: "s", ProposedAction: "x", ActorReliability: 0.5}); err == nil {
		t.Fatalf("expected error for missing IntentID")
	}
	if err := g.Register(PendingIntent{IntentID: "i", ActorID: "a", Subject: "s", ProposedAction: "x", ActorReliability: 1.5}); err == nil {
		t.Fatalf("expected error for out-of-range ActorReliability")
	}
}
