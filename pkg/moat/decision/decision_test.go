package decision

import (
	"errors"
	"math"
	"sync"
	"testing"
)

const eps = 1e-9

func almostEqual(a, b float64) bool { return math.Abs(a-b) < eps }

func mustDecision(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRegisterPolicyValidation(t *testing.T) {
	e := NewEngine()
	if err := e.RegisterPolicy(Policy{Name: "p"}); !errors.Is(err, ErrNoFactorsInPolicy) {
		t.Fatalf("want ErrNoFactorsInPolicy, got %v", err)
	}
	if err := e.RegisterPolicy(Policy{Name: "p", Factors: []FactorWeight{{Name: "f", Weight: 0}}}); !errors.Is(err, ErrInvalidWeight) {
		t.Fatalf("want ErrInvalidWeight, got %v", err)
	}
	if err := e.RegisterPolicy(Policy{Name: "p", Factors: []FactorWeight{{Name: "f", Weight: 1}}}); err != nil {
		t.Fatalf("valid policy rejected: %v", err)
	}
}

func TestDecideUnknownPolicy(t *testing.T) {
	e := NewEngine()
	_, err := e.Decide("a", "subj", "ghost", nil, 1)
	if !errors.Is(err, ErrUnknownPolicy) {
		t.Fatalf("want ErrUnknownPolicy, got %v", err)
	}
}

// TestDecideWeightedAverageCorrect proves the risk-score math against a
// hand-computed value.
func TestDecideWeightedAverageCorrect(t *testing.T) {
	e := NewEngine()
	mustDecision(t, e.RegisterPolicy(Policy{
		Name: "dark_vessel_risk",
		Factors: []FactorWeight{
			{Name: "ais_anomaly", Weight: 3},
			{Name: "sar_mismatch", Weight: 2},
			{Name: "ownership_change", Weight: 1},
		},
		FlagThreshold:     0.4,
		EscalateThreshold: 0.75,
	}))
	d, err := e.Decide("analyst-1", "VESSEL:IMO1", "dark_vessel_risk", map[string]float64{
		"ais_anomaly":      0.9,
		"sar_mismatch":     0.8,
		"ownership_change": 0.5,
	}, 10)
	mustDecision(t, err)
	want := (3*0.9 + 2*0.8 + 1*0.5) / 6.0
	if !almostEqual(d.RiskScore, want) {
		t.Fatalf("risk score = %v, want %v", d.RiskScore, want)
	}
	if d.Action != ActionEscalate {
		t.Fatalf("action = %v, want ESCALATE (score %v >= 0.75)", d.Action, d.RiskScore)
	}
	if len(d.Explanation) == 0 {
		t.Fatalf("expected non-empty explanation")
	}
}

func TestDecideMissingFactorTreatedAsZeroWithNote(t *testing.T) {
	e := NewEngine()
	mustDecision(t, e.RegisterPolicy(Policy{
		Name:              "p",
		Factors:           []FactorWeight{{Name: "a", Weight: 1}, {Name: "b", Weight: 1}},
		FlagThreshold:     0.3,
		EscalateThreshold: 0.7,
	}))
	d, err := e.Decide("actor", "subj", "p", map[string]float64{"a": 1.0}, 1) // "b" missing
	mustDecision(t, err)
	want := (1.0*1.0 + 1.0*0.0) / 2.0
	if !almostEqual(d.RiskScore, want) {
		t.Fatalf("risk score = %v, want %v", d.RiskScore, want)
	}
	foundNote := false
	for _, line := range d.Explanation {
		if line == "  factor=\"b\" weight=1.000 value=0.0000 contribution=0.0000 (no signal supplied, treated as 0)" {
			foundNote = true
		}
	}
	if !foundNote {
		t.Fatalf("expected explanation to flag missing factor 'b', got: %v", d.Explanation)
	}
}

func TestDecideActionTiers(t *testing.T) {
	e := NewEngine()
	mustDecision(t, e.RegisterPolicy(Policy{
		Name:              "p",
		Factors:           []FactorWeight{{Name: "risk", Weight: 1}},
		FlagThreshold:     0.4,
		EscalateThreshold: 0.8,
	}))
	cases := []struct {
		value float64
		want  Action
	}{
		{0.1, ActionMonitor},
		{0.4, ActionFlag},
		{0.6, ActionFlag},
		{0.8, ActionEscalate},
		{0.99, ActionEscalate},
	}
	for _, c := range cases {
		d, err := e.Decide("a", "s", "p", map[string]float64{"risk": c.value}, 1)
		mustDecision(t, err)
		if d.Action != c.want {
			t.Fatalf("value=%v: action = %v, want %v", c.value, d.Action, c.want)
		}
	}
}

// TestDecideDeterministicRegardlessOfMapOrder proves that Go's random
// map iteration order (values map[string]float64) never leaks into the
// hash chain — factors are always sorted before hashing/explaining.
func TestDecideDeterministicRegardlessOfMapOrder(t *testing.T) {
	e1 := NewEngine()
	e2 := NewEngine()
	policy := Policy{
		Name: "p",
		Factors: []FactorWeight{
			{Name: "z_factor", Weight: 1}, {Name: "a_factor", Weight: 2}, {Name: "m_factor", Weight: 3},
		},
		FlagThreshold: 0.3, EscalateThreshold: 0.7,
	}
	mustDecision(t, e1.RegisterPolicy(policy))
	mustDecision(t, e2.RegisterPolicy(policy))

	values := map[string]float64{"z_factor": 0.5, "a_factor": 0.8, "m_factor": 0.2}
	for i := 0; i < 5; i++ {
		_, err1 := e1.Decide("a", "s", "p", values, 1)
		_, err2 := e2.Decide("a", "s", "p", values, 1)
		mustDecision(t, err1)
		mustDecision(t, err2)
	}
	if e1.Head() != e2.Head() {
		t.Fatalf("chain heads diverged: %s vs %s", e1.Head(), e2.Head())
	}
}

func TestVerifyChainDetectsTamper(t *testing.T) {
	e := NewEngine()
	mustDecision(t, e.RegisterPolicy(Policy{Name: "p", Factors: []FactorWeight{{Name: "f", Weight: 1}}, FlagThreshold: 0.3, EscalateThreshold: 0.7}))
	_, err := e.Decide("a", "s", "p", map[string]float64{"f": 0.5}, 1)
	mustDecision(t, err)
	if err := e.VerifyChain(); err != nil {
		t.Fatalf("untampered chain failed verify: %v", err)
	}
	e.log[0].RiskScore = 0.99
	if err := e.VerifyChain(); !errors.Is(err, ErrTamperDetected) {
		t.Fatalf("expected ErrTamperDetected, got %v", err)
	}
}

func TestConcurrentDecideRace(t *testing.T) {
	e := NewEngine()
	mustDecision(t, e.RegisterPolicy(Policy{Name: "p", Factors: []FactorWeight{{Name: "f", Weight: 1}}, FlagThreshold: 0.3, EscalateThreshold: 0.7}))
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, _ = e.Decide("a", "s", "p", map[string]float64{"f": float64(n) / 20.0}, uint64(n))
		}(i)
	}
	wg.Wait()
	if err := e.VerifyChain(); err != nil {
		t.Fatalf("chain corrupted under concurrent load: %v", err)
	}
}
