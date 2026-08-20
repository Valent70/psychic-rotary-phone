package causal

import "testing"

func TestApplyFeedbackReinforcesConfirmedOutcome(t *testing.T) {
	g := NewGraph()
	_, err := g.Observe("collector", "AIS_OFF", "STS", 0.5, 1, "", 1)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	updated, err := g.ApplyFeedback("analyst", "AIS_OFF", "STS", "", true, 0.5, 2)
	if err != nil {
		t.Fatalf("apply feedback: %v", err)
	}
	if updated.Strength <= 0.5 {
		t.Fatalf("expected strength to increase toward confirmed outcome, got %f", updated.Strength)
	}
}

func TestApplyFeedbackWeakensDisconfirmedOutcome(t *testing.T) {
	g := NewGraph()
	g.Observe("collector", "AIS_OFF", "STS", 0.9, 1, "", 1)
	updated, err := g.ApplyFeedback("analyst", "AIS_OFF", "STS", "", false, 0.5, 2)
	if err != nil {
		t.Fatalf("apply feedback: %v", err)
	}
	if updated.Strength >= 0.9 {
		t.Fatalf("expected strength to decrease toward disconfirmed outcome, got %f", updated.Strength)
	}
}

func TestApplyFeedbackRequiresExistingEdge(t *testing.T) {
	g := NewGraph()
	if _, err := g.ApplyFeedback("a", "X", "Y", "", true, 0.5, 1); err == nil {
		t.Fatalf("expected error applying feedback to a nonexistent edge")
	}
}

func TestApplyFeedbackInvalidLearningRate(t *testing.T) {
	g := NewGraph()
	g.Observe("collector", "A", "B", 0.5, 1, "", 1)
	if _, err := g.ApplyFeedback("a", "A", "B", "", true, 0, 2); err == nil {
		t.Fatalf("expected error for zero learning rate")
	}
	if _, err := g.ApplyFeedback("a", "A", "B", "", true, 1.5, 2); err == nil {
		t.Fatalf("expected error for learning rate > 1")
	}
}

func TestFeedbackHistoryTracksEvolution(t *testing.T) {
	g := NewGraph()
	g.Observe("collector", "A", "B", 0.5, 1, "", 1)
	g.ApplyFeedback("analyst", "A", "B", "", true, 0.5, 2)
	g.ApplyFeedback("analyst", "A", "B", "", true, 0.5, 3)

	hist := g.FeedbackHistory("A", "B", "")
	if len(hist) != 3 {
		t.Fatalf("expected 3 history entries, got %d: %v", len(hist), hist)
	}
	if hist[2] <= hist[1] || hist[1] <= hist[0] {
		t.Fatalf("expected monotonically increasing strength history with repeated confirmation, got %v", hist)
	}
}

func TestApplyFeedbackGoesThroughReplayableLog(t *testing.T) {
	g := NewGraph()
	g.Observe("collector", "A", "B", 0.5, 1, "", 1)
	g.ApplyFeedback("analyst", "A", "B", "", true, 0.3, 2)

	if err := g.VerifyChain(); err != nil {
		t.Fatalf("expected clean chain after feedback: %v", err)
	}
	replayed, err := Rebuild(g.ReplayAll())
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if replayed.Head() != g.Head() {
		t.Fatalf("expected identical head after replay: %s vs %s", replayed.Head(), g.Head())
	}
}
