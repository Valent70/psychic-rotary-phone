package economic

import (
	"errors"
	"math"
	"testing"
)

func base() Input {
	return Input{
		DecisionID: "dec-1", Subject: "VESSEL:IMO9999999", Currency: "USD", Tick: 10,
		ConfidenceLevel: 0.95,
		Scenarios: []Scenario{
			{ID: "clean", Probability: 0.90, Outcome: 100, EvidenceRefs: []string{"e1"}},
			{ID: "detained", Probability: 0.07, Outcome: -2000, EvidenceRefs: []string{"e2"}},
			{ID: "sanctioned", Probability: 0.03, Outcome: -50000, EvidenceRefs: []string{"e3"}},
		},
	}
}

func TestExpectedValueAndDecomposition(t *testing.T) {
	c, err := Evaluate(base())
	if err != nil {
		t.Fatal(err)
	}
	want := 0.90*100 + 0.07*-2000 + 0.03*-50000
	if math.Abs(c.ExpectedValue-want) > 1e-9 {
		t.Fatalf("EV = %v, want %v", c.ExpectedValue, want)
	}
	if math.Abs(c.ExpectedGain-90) > 1e-9 {
		t.Fatalf("expected gain = %v", c.ExpectedGain)
	}
	if math.Abs(c.ExpectedLoss-(0.07*2000+0.03*50000)) > 1e-9 {
		t.Fatalf("expected loss = %v", c.ExpectedLoss)
	}
	if c.WorstCase != -50000 || c.BestCase != 100 {
		t.Fatalf("worst/best = %v/%v", c.WorstCase, c.BestCase)
	}
	if c.ExpectedValue != c.ExpectedGain-c.ExpectedLoss {
		t.Fatal("EV must decompose exactly into gain minus loss")
	}
}

func TestCVaRIsNeverBelowVaR(t *testing.T) {
	for _, level := range []float64{0.5, 0.9, 0.95, 0.99} {
		in := base()
		in.ConfidenceLevel = level
		c, err := Evaluate(in)
		if err != nil {
			t.Fatal(err)
		}
		if c.CVaR < c.VaR-1e-12 {
			t.Fatalf("level %v: CVaR %v < VaR %v", level, c.CVaR, c.VaR)
		}
	}
}

func TestVaRAndCVaRPickUpTheTail(t *testing.T) {
	in := base()
	in.ConfidenceLevel = 0.99 // alpha = 0.01, inside the 3% sanction mass
	c, err := Evaluate(in)
	if err != nil {
		t.Fatal(err)
	}
	if c.VaR != 50000 {
		t.Fatalf("VaR at 99%% should be the sanction loss, got %v", c.VaR)
	}
	if math.Abs(c.CVaR-50000) > 1e-9 {
		t.Fatalf("CVaR at 99%% should equal the clipped tail mean, got %v", c.CVaR)
	}
}

func TestVaRIsReportedAsAPositiveLossMagnitude(t *testing.T) {
	c, _ := Evaluate(base())
	if c.VaR < 0 || c.CVaR < 0 {
		t.Fatalf("VaR/CVaR must be positive magnitudes: %v/%v", c.VaR, c.CVaR)
	}
}

func TestAllGainScenariosYieldZeroVaR(t *testing.T) {
	c, err := Evaluate(Input{
		DecisionID: "d", Subject: "s", Tick: 1,
		Scenarios: []Scenario{
			{ID: "a", Probability: 0.5, Outcome: 10, EvidenceRefs: []string{"e"}},
			{ID: "b", Probability: 0.5, Outcome: 20, EvidenceRefs: []string{"e"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.VaR != 0 || c.CVaR != 0 {
		t.Fatalf("no loss scenario should mean zero VaR/CVaR, got %v/%v", c.VaR, c.CVaR)
	}
}

func TestProbabilityMassIsNotSilentlyNormalised(t *testing.T) {
	in := base()
	in.Scenarios = in.Scenarios[:2] // drops the 3% sanction tail
	if _, err := Evaluate(in); !errors.Is(err, ErrProbabilityMass) {
		t.Fatalf("dropping the tail must be rejected, got %v", err)
	}
}

func TestDuplicateScenarioRejected(t *testing.T) {
	in := base()
	in.Scenarios = []Scenario{
		{ID: "x", Probability: 0.5, Outcome: -1},
		{ID: "x", Probability: 0.5, Outcome: -1},
	}
	if _, err := Evaluate(in); !errors.Is(err, ErrDuplicateScenario) {
		t.Fatalf("expected duplicate rejection, got %v", err)
	}
}

func TestMissingLineageRefused(t *testing.T) {
	in := base()
	in.DecisionID = ""
	if _, err := Evaluate(in); !errors.Is(err, ErrNoLineage) {
		t.Fatalf("expected lineage error, got %v", err)
	}
	in = base()
	in.Scenarios[0].ID = ""
	if _, err := Evaluate(in); !errors.Is(err, ErrNoLineage) {
		t.Fatalf("expected lineage error for unnamed scenario, got %v", err)
	}
}

func TestEmptyScenarioSetIsNotFree(t *testing.T) {
	in := base()
	in.Scenarios = nil
	if _, err := Evaluate(in); !errors.Is(err, ErrNoScenarios) {
		t.Fatalf("expected ErrNoScenarios, got %v", err)
	}
}

func TestInvalidConfidenceLevelRejected(t *testing.T) {
	for _, l := range []float64{0.0000, -0.1, 1, 1.2} {
		in := base()
		in.ConfidenceLevel = l
		_, err := Evaluate(in)
		if l == 0 {
			if err != nil {
				t.Fatalf("zero must default to 0.95, got %v", err)
			}
			continue
		}
		if !errors.Is(err, ErrConfidenceLevel) {
			t.Fatalf("level %v should be rejected, got %v", l, err)
		}
	}
}

func TestNonFiniteOutcomeRejected(t *testing.T) {
	in := base()
	in.Scenarios[0].Outcome = math.Inf(-1)
	if _, err := Evaluate(in); err == nil {
		t.Fatal("an infinite outcome must be rejected")
	}
}

func TestHashIsDeterministicAndOrderIndependent(t *testing.T) {
	a, _ := Evaluate(base())
	in := base()
	in.Scenarios[0], in.Scenarios[2] = in.Scenarios[2], in.Scenarios[0]
	b, _ := Evaluate(in)
	if a.Hash != b.Hash {
		t.Fatal("scenario input ordering must not change the consequence hash")
	}
}

func TestHashChangesWhenTailProbabilityChanges(t *testing.T) {
	a, _ := Evaluate(base())
	in := base()
	in.Scenarios[1].Probability = 0.06
	in.Scenarios[2].Probability = 0.04
	b, _ := Evaluate(in)
	if a.Hash == b.Hash {
		t.Fatal("moving probability mass into the tail must change the hash")
	}
	if b.ExpectedLoss <= a.ExpectedLoss {
		t.Fatal("moving mass to the worse tail must raise expected loss")
	}
}

func TestVerifyDetectsTampering(t *testing.T) {
	c, _ := Evaluate(base())
	if err := Verify(c); err != nil {
		t.Fatalf("fresh consequence must verify: %v", err)
	}
	c.ExpectedValue += 1
	if err := Verify(c); err == nil {
		t.Fatal("edited expected value must fail verification")
	}
	c2, _ := Evaluate(base())
	c2.Scenarios[0].Outcome = 999999
	if err := Verify(c2); err == nil {
		t.Fatal("edited scenario must fail verification")
	}
}

func TestUnjustifiedScenariosAreNamed(t *testing.T) {
	in := base()
	in.Scenarios[1].EvidenceRefs = nil
	c, err := Evaluate(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Unjustified) != 1 || c.Unjustified[0] != "detained" {
		t.Fatalf("unjustified scenario must be reported, got %v", c.Unjustified)
	}
}

func TestChannelExpectationAggregates(t *testing.T) {
	in := base()
	in.Scenarios[2].Channels = map[string]float64{"sanctions": -40000, "freight": -10000}
	c, err := Evaluate(in)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(c.ChannelExpected["sanctions"]-(0.03*-40000)) > 1e-9 {
		t.Fatalf("channel expectation wrong: %v", c.ChannelExpected)
	}
}

func TestTailRiskAnswersTheUnderwritersQuestion(t *testing.T) {
	c, _ := Evaluate(base())
	if p := c.TailRisk(-1000); math.Abs(p-0.10) > 1e-9 {
		t.Fatalf("P(outcome <= -1000) should be 0.10, got %v", p)
	}
	if p := c.TailRisk(-100000); p != 0 {
		t.Fatalf("nothing is worse than -50000, got %v", p)
	}
}

func TestDominanceRequiresBothBetterMeanAndNoWorseTail(t *testing.T) {
	safe, _ := Evaluate(Input{DecisionID: "d", Subject: "s", Scenarios: []Scenario{
		{ID: "a", Probability: 1, Outcome: 10, EvidenceRefs: []string{"e"}},
	}})
	risky, _ := Evaluate(Input{DecisionID: "d", Subject: "s", Scenarios: []Scenario{
		{ID: "a", Probability: 0.99, Outcome: 20, EvidenceRefs: []string{"e"}},
		{ID: "b", Probability: 0.01, Outcome: -100000, EvidenceRefs: []string{"e"}},
	}})
	if risky.Dominates(safe) {
		t.Fatal("a higher mean with a catastrophic tail must not dominate")
	}
	if safe.Dominates(risky) == false && safe.ExpectedValue > risky.ExpectedValue {
		t.Fatal("inconsistent dominance")
	}
}

func TestVarianceIsNonNegativeAndZeroForCertainty(t *testing.T) {
	c, _ := Evaluate(Input{DecisionID: "d", Subject: "s", Scenarios: []Scenario{
		{ID: "only", Probability: 1, Outcome: -42, EvidenceRefs: []string{"e"}},
	}})
	if c.Variance != 0 || c.StdDev != 0 {
		t.Fatalf("a certain outcome has zero variance, got %v", c.Variance)
	}
	d, _ := Evaluate(base())
	if d.Variance <= 0 {
		t.Fatal("a spread distribution must have positive variance")
	}
}
