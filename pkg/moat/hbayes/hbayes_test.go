package hbayes

import (
	"errors"
	"math"
	"testing"
)

const (
	dark   State = "DARK"
	normal State = "NORMAL"
)

func twoStateModel(t *testing.T, decay float64) *Model {
	t.Helper()
	tr := Transition{
		States: []State{dark, normal},
		P: map[State]map[State]float64{
			dark:   {dark: 0.85, normal: 0.15},
			normal: {dark: 0.05, normal: 0.95},
		},
	}
	m, err := NewTemporalModel([]State{dark, normal}, tr,
		map[State]float64{dark: 0.1, normal: 0.9}, nil, decay)
	if err != nil {
		t.Fatalf("NewTemporalModel: %v", err)
	}
	return m
}

func obs(source string, dl, nl, corr float64) Observation {
	return Observation{SourceID: source, Likelihood: map[State]float64{dark: dl, normal: nl}, Correlation: corr}
}

func TestTransitionValidationRejectsNonStochastic(t *testing.T) {
	bad := Transition{States: []State{dark, normal}, P: map[State]map[State]float64{
		dark: {dark: 0.9, normal: 0.5}, normal: {dark: 0.1, normal: 0.9},
	}}
	if err := bad.Validate(); !errors.Is(err, ErrNotStochastic) {
		t.Fatalf("non-stochastic transition accepted: %v", err)
	}
}

func TestForwardFilteringMovesTowardEvidence(t *testing.T) {
	m := twoStateModel(t, 0)
	seq := []TickObservations{
		{Tick: 1, Observations: []Observation{obs("sar", 0.9, 0.1, 0)}},
		{Tick: 2, Observations: []Observation{obs("sar", 0.9, 0.1, 0)}},
		{Tick: 3, Observations: []Observation{obs("sar", 0.9, 0.1, 0)}},
	}
	tr, err := m.Infer(seq, nil)
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	if tr.Posterior(dark) <= m.Prior[dark] {
		t.Fatalf("posterior did not move toward evidence: %.4f vs prior %.4f", tr.Posterior(dark), m.Prior[dark])
	}
	if tr.Steps[2].Filtered[dark] <= tr.Steps[0].Filtered[dark] {
		t.Fatal("repeated corroborating observations did not increase belief over time")
	}
}

func TestMissingObservationsIncreaseUncertainty(t *testing.T) {
	m := twoStateModel(t, 0)
	seq := []TickObservations{
		{Tick: 1, Observations: []Observation{obs("sar", 0.95, 0.05, 0)}},
		{Tick: 2, Observations: nil}, // gap: nothing observed
		{Tick: 3, Observations: nil},
	}
	tr, err := m.Infer(seq, nil)
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	if tr.Steps[2].Entropy <= tr.Steps[0].Entropy {
		t.Fatalf("entropy did not grow across missing observations: %.4f -> %.4f",
			tr.Steps[0].Entropy, tr.Steps[2].Entropy)
	}
	if tr.Steps[1].ObservationCount != 0 || tr.Steps[1].EffectiveEvidence != 0 {
		t.Fatal("a tick with no observations reported evidence")
	}
}

func TestTemporalDecayWeakensOldEvidence(t *testing.T) {
	noDecay := twoStateModel(t, 0)
	withDecay := twoStateModel(t, 0.05)
	seq := []TickObservations{
		{Tick: 1, Observations: []Observation{obs("sar", 0.99, 0.01, 0)}},
		{Tick: 5000, Observations: nil},
	}
	a, err := noDecay.Infer(seq, nil)
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	b, err := withDecay.Infer(seq, nil)
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	stationary := noDecay.Transition.Stationary()
	distA := math.Abs(a.Posterior(dark) - stationary[dark])
	distB := math.Abs(b.Posterior(dark) - stationary[dark])
	if distB > distA {
		t.Fatalf("temporal decay did not pull an ancient observation toward the prior: %.6f vs %.6f", distB, distA)
	}
}

func TestContradictoryObservationsCancelRatherThanLastWins(t *testing.T) {
	m := twoStateModel(t, 0)
	both := []TickObservations{{Tick: 1, Observations: []Observation{
		obs("sar", 0.9, 0.1, 0), obs("ais", 0.1, 0.9, 0),
	}}}
	onlyLast := []TickObservations{{Tick: 1, Observations: []Observation{obs("ais", 0.1, 0.9, 0)}}}
	a, err := m.Infer(both, nil)
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	b, err := m.Infer(onlyLast, nil)
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	if math.Abs(a.Posterior(dark)-b.Posterior(dark)) < 1e-9 {
		t.Fatal("contradictory observation was overwritten instead of cancelling")
	}
	if a.Posterior(dark) <= b.Posterior(dark) {
		t.Fatal("a supporting observation did not raise belief against a contradicting one")
	}
}

func TestCorrelatedObservationsCannotInflateBelief(t *testing.T) {
	m := twoStateModel(t, 0)
	independent := []TickObservations{{Tick: 1, Observations: []Observation{
		obs("a", 0.9, 0.1, 0), obs("b", 0.9, 0.1, 0),
	}}}
	correlated := []TickObservations{{Tick: 1, Observations: []Observation{
		obs("a", 0.9, 0.1, 0.9), obs("b", 0.9, 0.1, 0.9),
	}}}
	ind, err := m.Infer(independent, nil)
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	cor, err := m.Infer(correlated, nil)
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	if cor.Posterior(dark) >= ind.Posterior(dark) {
		t.Fatalf("correlated sources inflated belief: correlated=%.6f independent=%.6f",
			cor.Posterior(dark), ind.Posterior(dark))
	}
}

func TestBackwardSmoothingUsesFutureEvidence(t *testing.T) {
	m := twoStateModel(t, 0)
	seq := []TickObservations{
		{Tick: 1, Observations: []Observation{obs("sar", 0.5, 0.5, 0)}}, // uninformative
		{Tick: 2, Observations: []Observation{obs("sar", 0.99, 0.01, 0)}},
		{Tick: 3, Observations: []Observation{obs("sar", 0.99, 0.01, 0)}},
	}
	tr, err := m.Infer(seq, nil)
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	if tr.Steps[0].Smoothed[dark] <= tr.Steps[0].Filtered[dark] {
		t.Fatalf("smoothing ignored later evidence: filtered=%.6f smoothed=%.6f",
			tr.Steps[0].Filtered[dark], tr.Steps[0].Smoothed[dark])
	}
	if len(tr.MAPPath) != 3 {
		t.Fatalf("MAP path length %d", len(tr.MAPPath))
	}
}

func TestCausalInterventionShiftsDynamics(t *testing.T) {
	tr := Transition{
		States: []State{dark, normal},
		P: map[State]map[State]float64{
			dark:   {dark: 0.85, normal: 0.15},
			normal: {dark: 0.05, normal: 0.95},
		},
	}
	edges := []CausalEdge{{Cause: "sanctions_listing", Effect: dark, Strength: 0.6, Lag: 1}}
	m, err := NewTemporalModel([]State{dark, normal}, tr, map[State]float64{dark: 0.1, normal: 0.9}, edges, 0)
	if err != nil {
		t.Fatalf("NewTemporalModel: %v", err)
	}
	seq := []TickObservations{
		{Tick: 1, Observations: []Observation{obs("ais", 0.5, 0.5, 0)}},
		{Tick: 3, Observations: []Observation{obs("ais", 0.5, 0.5, 0)}},
	}
	base, err := m.Infer(seq, nil)
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	intervened, err := m.Infer(seq, []Intervention{{Cause: "sanctions_listing", Tick: 1, Magnitude: 1}})
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	if intervened.Posterior(dark) <= base.Posterior(dark) {
		t.Fatalf("do(sanctions_listing) had no causal effect: %.6f vs %.6f",
			intervened.Posterior(dark), base.Posterior(dark))
	}
	if len(intervened.Steps[1].Interventions) == 0 {
		t.Fatal("intervention was applied but not recorded in the trace")
	}
}

func TestInferenceIsDeterministicAndReplayable(t *testing.T) {
	m := twoStateModel(t, 0.01)
	seq := []TickObservations{
		{Tick: 1, Observations: []Observation{obs("a", 0.8, 0.2, 0.1), obs("b", 0.6, 0.4, 0.3)}},
		{Tick: 9, Observations: nil},
		{Tick: 17, Observations: []Observation{obs("c", 0.2, 0.8, 0)}},
	}
	a, err := m.Infer(seq, nil)
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	b, err := m.Infer(seq, nil)
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	if a.TraceHash != b.TraceHash {
		t.Fatal("inference is not deterministic")
	}
	if err := m.Replay(seq, nil, a); err != nil {
		t.Fatalf("replay failed: %v", err)
	}
	tampered := a
	tampered.TraceHash = "deadbeef"
	if err := m.Replay(seq, nil, tampered); err == nil {
		t.Fatal("replay accepted a tampered trace")
	}
}

func TestNonMonotonicTicksRejected(t *testing.T) {
	m := twoStateModel(t, 0)
	seq := []TickObservations{{Tick: 5}, {Tick: 5}}
	if _, err := m.Infer(seq, nil); !errors.Is(err, ErrNonMonotonicTicks) {
		t.Fatalf("non-monotonic ticks accepted: %v", err)
	}
	if _, err := m.Infer(nil, nil); !errors.Is(err, ErrNoObservations) {
		t.Fatalf("empty sequence accepted: %v", err)
	}
}

func TestStationaryDistributionIsAFixedPoint(t *testing.T) {
	m := twoStateModel(t, 0)
	pi := m.Transition.Stationary()
	for _, to := range m.States {
		var acc float64
		for _, from := range m.States {
			acc += pi[from] * m.Transition.P[from][to]
		}
		if math.Abs(acc-pi[to]) > 1e-9 {
			t.Fatalf("stationary distribution is not a fixed point for %s: %.12f vs %.12f", to, acc, pi[to])
		}
	}
}

func TestBadPriorAndBadLikelihoodRejected(t *testing.T) {
	tr := Transition{States: []State{dark, normal}, P: map[State]map[State]float64{
		dark: {dark: 1, normal: 0}, normal: {dark: 0, normal: 1},
	}}
	if _, err := NewTemporalModel([]State{dark, normal}, tr, map[State]float64{dark: 0.5, normal: 0.9}, nil, 0); !errors.Is(err, ErrBadPrior) {
		t.Fatal("non-normalised prior accepted")
	}
	m := twoStateModel(t, 0)
	if _, err := m.Infer([]TickObservations{{Tick: 1, Observations: []Observation{obs("a", 1.5, 0.1, 0)}}}, nil); !errors.Is(err, ErrBadLikelihood) {
		t.Fatal("out-of-range likelihood accepted")
	}
	if _, err := m.Infer([]TickObservations{{Tick: 1, Observations: []Observation{obs("a", 0.5, 0.5, 1.0)}}}, nil); !errors.Is(err, ErrBadCorrelation) {
		t.Fatal("correlation of 1.0 accepted")
	}
}
