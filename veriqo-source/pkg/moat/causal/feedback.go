// Feedback loop for causal edges — closes the explicit gap named in
// "Yang_masih_belum.docx" section D: the causal graph had Cause, Effect,
// Probability (Strength/AggregateSupport), and Counterfactual (WhatIf),
// but no Feedback Loop — a mechanism for observed real-world outcomes to
// reinforce or weaken a causal edge's strength over time.
package causal

import "fmt"

// ApplyFeedback is Stage "Feedback Loop": given a real-world observation
// of whether the predicted effect actually followed the cause,
// reinforce (outcome confirmed) or weaken (outcome disconfirmed) the
// edge's Strength via a simple, deterministic exponential-moving-average
// update — then records the update through the SAME Observe() path
// every other causal edge change goes through, so the feedback is fully
// part of the existing hash-chained, replayable log (no special-cased
// bypass).
//
// learningRate in (0,1]: how much one feedback observation moves the
// strength. 1.0 means "fully trust this single observation"; smaller
// values average across many observations over time.
func (g *Graph) ApplyFeedback(actorID string, cause, effect NodeID, condition string, outcomeConfirmed bool, learningRate float64, tick uint64) (Edge, error) {
	if learningRate <= 0 || learningRate > 1 {
		return Edge{}, fmt.Errorf("causal: learningRate must be in (0,1], got %f", learningRate)
	}
	g.mu.RLock()
	current, ok := g.edges[edgeKey(cause, effect, condition)]
	g.mu.RUnlock()
	if !ok {
		return Edge{}, fmt.Errorf("causal: no existing edge %s->%s (condition=%q) to apply feedback to", cause, effect, condition)
	}

	target := 0.0
	if outcomeConfirmed {
		target = 1.0
	}
	newStrength := current.Strength + learningRate*(target-current.Strength)
	// clamp to Observe's valid range (0,1], never exactly 0
	if newStrength <= 0 {
		newStrength = 0.0001
	}
	if newStrength > 1 {
		newStrength = 1
	}

	return g.Observe(actorID, cause, effect, newStrength, current.Lag, condition, tick)
}

// FeedbackHistory replays the log and returns every Strength value an
// edge has had over time, in tick order — the concrete "how did our
// causal belief evolve as evidence came in" audit trail.
func (g *Graph) FeedbackHistory(cause, effect NodeID, condition string) []float64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var out []float64
	for _, rec := range g.log {
		if rec.Cause == cause && rec.Effect == effect && rec.Condition == condition {
			out = append(out, rec.Strength)
		}
	}
	return out
}
