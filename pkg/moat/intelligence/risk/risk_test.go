package risk

import (
	"testing"

	"veriqo/pkg/moat/decision"
	"veriqo/pkg/moat/provenance"
)

func TestModel_LowRiskWhenNoSignals(t *testing.T) {
	m := NewModel()
	res := m.Score(CompositeInput{})
	if res.Label != LabelLow {
		t.Fatalf("expected LOW for empty input, got %s (score=%v)", res.Label, res.Score)
	}
}

func TestModel_CriticalWhenBothSignalsHigh(t *testing.T) {
	m := NewModel()
	res := m.Score(CompositeInput{
		PatternScore:                0.95,
		PriceAnomalyScore:           0.9,
		HasProvenanceData:           true,
		ProvenanceIndependenceRatio: 1.0,
	})
	if res.Label != LabelCritical && res.Label != LabelHigh {
		t.Fatalf("expected HIGH or CRITICAL for strong dual signal, got %s (score=%v)", res.Label, res.Score)
	}
	if res.Score <= 0.6 {
		t.Errorf("expected high composite score, got %v", res.Score)
	}
}

func TestModel_LowIndependenceDiscountsScore(t *testing.T) {
	m := NewModel()
	independent := m.Score(CompositeInput{
		PatternScore: 0.9, PriceAnomalyScore: 0.9,
		HasProvenanceData: true, ProvenanceIndependenceRatio: 1.0,
	})
	dependent := m.Score(CompositeInput{
		PatternScore: 0.9, PriceAnomalyScore: 0.9,
		HasProvenanceData: true, ProvenanceIndependenceRatio: 0.0,
	})
	if dependent.Score >= independent.Score {
		t.Fatalf("expected fully-dependent-source composite to be discounted below independent: dependent=%v independent=%v",
			dependent.Score, independent.Score)
	}
	// Discount floor prevents total zeroing.
	if dependent.Score <= 0 {
		t.Errorf("discount should not zero out the score entirely, got %v", dependent.Score)
	}
}

func TestModel_MissingProvenanceDataIsNeutral(t *testing.T) {
	m := NewModel()
	withData := m.Score(CompositeInput{PatternScore: 0.8, PriceAnomalyScore: 0.2, HasProvenanceData: true, ProvenanceIndependenceRatio: 1.0})
	noData := m.Score(CompositeInput{PatternScore: 0.8, PriceAnomalyScore: 0.2, HasProvenanceData: false})
	if diff := withData.Score - noData.Score; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("missing provenance data should behave identically to fully-independent (neutral default): with=%v without=%v", withData.Score, noData.Score)
	}
}

func TestToDecisionValues_MatchesDefaultPolicyFactorNames(t *testing.T) {
	in := CompositeInput{PatternScore: 0.7, PriceAnomalyScore: 0.3, HasProvenanceData: true, ProvenanceIndependenceRatio: 0.8}
	res := NewModel().Score(in)
	values := ToDecisionValues(res)
	policy := DefaultPolicy()
	for _, f := range policy.Factors {
		if _, ok := values[f.Name]; !ok {
			t.Errorf("DefaultPolicy factor %q has no corresponding value from ToDecisionValues", f.Name)
		}
	}
}

// TestDecisionConsistency is the v7.9 audit's explicit ask (§"Decision
// consistency"): Risk.Score() must be numerically IDENTICAL to
// Decision.RiskScore for the same input — closing the A != B bug the
// audit's §3 flagged (Risk Model applied a provenance discount that
// Decision Engine silently ignored because it recomputed from raw
// factors instead of consuming the canonical Result).
func TestDecisionConsistency(t *testing.T) {
	m := NewModel()
	in := CompositeInput{
		PatternScore: 0.9, PriceAnomalyScore: 0.7,
		HasProvenanceData: true, ProvenanceIndependenceRatio: 0.2, // low independence: exercises the discount
	}
	res := m.Score(in)

	eng := decision.NewEngine()
	if err := eng.RegisterPolicy(DefaultPolicy()); err != nil {
		t.Fatalf("register policy: %v", err)
	}
	values := ToDecisionValues(res)
	dec, err := eng.Decide("auditor", "VESSEL_CONSISTENCY", DefaultPolicy().Name, values, 1)
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if diff := dec.RiskScore - res.Score; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("Decision.RiskScore (%v) must equal Risk.Score() (%v) — canonical projection broken", dec.RiskScore, res.Score)
	}
}

func TestPredictionFromResult_DistributionSumsToOne(t *testing.T) {
	m := NewModel()
	res := m.Score(CompositeInput{PatternScore: 0.6, PriceAnomalyScore: 0.4})
	pred := PredictionFromResult("VESSEL_X|tbml_risk", res, []string{"src1", "src2"}, 10)
	sum := pred.Distribution["SUSPECTED"] + pred.Distribution["LEGITIMATE"]
	if diff := sum - 1.0; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("distribution should sum to 1.0, got %v", sum)
	}
	if pred.ClaimKey != "VESSEL_X|tbml_risk" {
		t.Errorf("unexpected claim key: %s", pred.ClaimKey)
	}
}

// TestScoreWithProvenance_IsNotASecondFormula is the auditor's explicit
// §15 concern: ScoreWithProvenance must be a thin wrapper that computes
// independence then delegates to the SAME Score() formula, never a
// second, independently-diverging computation. Proven by construction:
// when independence resolves to the same ratio Score() would be given
// directly, both paths must produce bit-identical results.
func TestScoreWithProvenance_IsNotASecondFormula(t *testing.T) {
	m := NewModel()
	direct := m.Score(CompositeInput{
		PatternScore: 0.7, PriceAnomalyScore: 0.5,
		HasProvenanceData: true, ProvenanceIndependenceRatio: 1.0,
	})

	g := provenance.New() // no declared edges -> independent sources
	viaProvenance, err := m.ScoreWithProvenance(CompositeInput{
		PatternScore: 0.7, PriceAnomalyScore: 0.5,
		SourceIDs: []string{"SRC_A", "SRC_B"},
	}, g)
	if err != nil {
		t.Fatalf("ScoreWithProvenance: %v", err)
	}
	if diff := direct.Score - viaProvenance.Score; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("Score() and ScoreWithProvenance() diverged for equivalent independence: direct=%v viaProvenance=%v — two formulas exist", direct.Score, viaProvenance.Score)
	}
}
