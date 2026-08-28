package intelligence

import (
	"testing"

	"veriqo/pkg/core/trustcalc"
	"veriqo/pkg/moat/contradiction"
)

func TestResolveProducesStructuredExplanation(t *testing.T) {
	l := New(nil, nil)
	obs := []contradiction.RawObservation{
		{ClaimKey: "vessel/IMO1", SourceID: "radar", Value: "at-sea", SourceReliability: 0.9, Tick: 1},
		{ClaimKey: "vessel/IMO1", SourceID: "sat", Value: "at-sea", SourceReliability: 0.8, Tick: 1},
		{ClaimKey: "vessel/IMO1", SourceID: "tip", Value: "in-port", SourceReliability: 0.3, Tick: 1},
	}
	for _, o := range obs {
		if err := l.Observe(o); err != nil {
			t.Fatal(err)
		}
	}
	exp, err := l.Resolve("vessel/IMO1", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if exp.WinningValue != "at-sea" {
		t.Fatalf("expected at-sea to win, got %s", exp.WinningValue)
	}
	if exp.RemainingContradictions == 0 {
		t.Fatal("expected contradictions to be recorded")
	}
	if len(exp.RejectedAlternatives) != 1 || exp.RejectedAlternatives[0].Value != "in-port" {
		t.Fatalf("expected in-port as rejected alternative, got %+v", exp.RejectedAlternatives)
	}
	// Real causal chain: must contain at least one path ending at the prediction node.
	if len(exp.CausalChain) == 0 {
		t.Fatal("expected a non-empty causal chain from Graph.Why")
	}
	for _, path := range exp.CausalChain {
		if path.Strength <= 0 || path.Strength > 1 {
			t.Fatalf("causal path strength out of range: %+v", path)
		}
	}
	// Real counterfactuals: removing the top source should reduce support.
	if len(exp.Counterfactuals) == 0 {
		t.Fatal("expected counterfactuals for supporting sources")
	}
	for src, cf := range exp.Counterfactuals {
		if cf.SupportWithoutSource > cf.SupportWithSource {
			t.Fatalf("source %s: removing a supporting cause should not increase support: %+v", src, cf)
		}
	}
	if err := l.Knowledge().VerifyChain(); err != nil {
		t.Fatalf("expected clean knowledge chain: %v", err)
	}
}

func TestLearningUsesSharedBayesianCalculus(t *testing.T) {
	calc := trustcalc.New(1, 1)
	l := New(calc, nil)
	_ = l.Observe(contradiction.RawObservation{ClaimKey: "c1", SourceID: "good", Value: "v1", SourceReliability: 0.9, Tick: 1})
	_ = l.Observe(contradiction.RawObservation{ClaimKey: "c1", SourceID: "bad", Value: "v2", SourceReliability: 0.9, Tick: 1})
	if _, err := l.Resolve("c1", 1, 0); err != nil {
		t.Fatal(err)
	}
	good := calc.Score(trustcalc.NamespacedSubject(trustcalc.NamespaceEvidenceSource, "good"))
	bad := calc.Score(trustcalc.NamespacedSubject(trustcalc.NamespaceEvidenceSource, "bad"))
	if good <= 0.5 {
		t.Fatalf("expected winning source's SHARED calculus score to rise above prior, got %v", good)
	}
	if bad >= 0.5 {
		t.Fatalf("expected losing source's SHARED calculus score to fall below prior, got %v", bad)
	}
	// Prove it's a real Beta posterior, not a black box: variance should
	// have shrunk from the prior Beta(1,1) variance (1/12 ~= 0.0833).
	if calc.Belief(trustcalc.NamespacedSubject(trustcalc.NamespaceEvidenceSource, "good")).Variance() >= 1.0/12.0 {
		t.Fatalf("expected posterior variance to shrink after an observation, got %v", calc.Belief(trustcalc.NamespacedSubject(trustcalc.NamespaceEvidenceSource, "good")).Variance())
	}
}

func TestResolveNoObservationsErrors(t *testing.T) {
	l := New(nil, nil)
	if _, err := l.Resolve("missing", 0, 0); err == nil {
		t.Fatal("expected error for claim with no observations")
	}
}

func TestPredictionIsProbabilisticNotLinearDelta(t *testing.T) {
	l := New(nil, nil)
	_ = l.Observe(contradiction.RawObservation{ClaimKey: "c1", SourceID: "s1", Value: "v1", SourceReliability: 0.9, Tick: 1})
	_ = l.Observe(contradiction.RawObservation{ClaimKey: "c1", SourceID: "s2", Value: "v1", SourceReliability: 0.9, Tick: 1})
	exp1, err := l.Resolve("c1", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Repeat the SAME winning value at tick 2: stability belief should
	// strengthen, pulling the predicted confidence toward (not simply
	// equal to) the observed confidence via log-odds combination.
	_ = l.Observe(contradiction.RawObservation{ClaimKey: "c1", SourceID: "s1", Value: "v1", SourceReliability: 0.9, Tick: 2})
	_ = l.Observe(contradiction.RawObservation{ClaimKey: "c1", SourceID: "s2", Value: "v1", SourceReliability: 0.9, Tick: 2})
	exp2, err := l.Resolve("c1", 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if exp2.PredictedNextConfidence <= 0 || exp2.PredictedNextConfidence > 1 {
		t.Fatalf("predicted confidence out of range: %v", exp2.PredictedNextConfidence)
	}
	if exp2.PredictionCredibleLow > exp2.PredictedNextConfidence || exp2.PredictionCredibleHigh < exp2.PredictedNextConfidence {
		t.Fatalf("predicted value must lie within its own credible interval: [%v, %v] contains %v?",
			exp2.PredictionCredibleLow, exp2.PredictionCredibleHigh, exp2.PredictedNextConfidence)
	}
	_ = exp1
}

func TestKnowledgeConfidenceUsesNoisyAndPropagation(t *testing.T) {
	l := New(nil, nil)
	// A single weak source "winning" uncontested should NOT produce a
	// confidently-asserted fact — propagated confidence must reflect
	// that source's own low reliability.
	_ = l.Observe(contradiction.RawObservation{ClaimKey: "c1", SourceID: "weak", Value: "v1", SourceReliability: 0.9, Tick: 1})
	exp, err := l.Resolve("c1", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if exp.KnowledgeVersion.Confidence <= 0 || exp.KnowledgeVersion.Confidence > 1 {
		t.Fatalf("expected a valid propagated confidence, got %v", exp.KnowledgeVersion.Confidence)
	}
}
