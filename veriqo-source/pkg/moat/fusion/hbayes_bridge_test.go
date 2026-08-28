package fusion

import (
	"testing"

	"veriqo/pkg/moat/fusion/hbayes"
)

// TestComputeHierarchicalRiskDiscountsCorrelatedResellers is the
// load-bearing test for PRIORITAS #4: three sources at high
// reliability, all in the SAME correlated provider group, must NOT
// combine to near-certainty the way three fully independent sources
// would.
func TestComputeHierarchicalRiskDiscountsCorrelatedResellers(t *testing.T) {
	eng := NewEngine(nil)
	must(t, eng.RegisterSource(SourceProfile{ID: "ais-a", BaseReliability: 0.9}))
	must(t, eng.RegisterSource(SourceProfile{ID: "ais-b", BaseReliability: 0.9}))
	must(t, eng.RegisterSource(SourceProfile{ID: "ais-c", BaseReliability: 0.9}))
	claim := Claim{Subject: "VESSEL:1", Predicate: "SANCTION_EVASION"}
	var err error
	_, err = eng.Submit("ais-a", claim, "TRUE", 1)
	must(t, err)
	_, err = eng.Submit("ais-b", claim, "TRUE", 1)
	must(t, err)
	_, err = eng.Submit("ais-c", claim, "TRUE", 1)
	must(t, err)

	model, err := hbayes.NewModel(
		[]hbayes.ReliabilityPrior{{GroupID: "SATFEED_X", Mean: 0.9, Variance: 0.02}},
		hbayes.CorrelationMatrix{Groups: []hbayes.ProviderGroupID{"SATFEED_X"}, Values: [][]float64{{1.0}}},
	)
	must(t, err)

	group := func(SourceID) hbayes.ProviderGroupID { return "SATFEED_X" }

	// Fully correlated within the group (rho=1): three resellers of the
	// same feed should be worth ~ONE independent signal, not three.
	correlated, err := eng.ComputeHierarchicalRisk(claim, "TRUE", model, group,
		map[hbayes.ProviderGroupID]float64{"SATFEED_X": 1.0})
	must(t, err)

	// Fully independent within the group (rho=0): same three signals
	// should be worth close to three independent confirmations.
	independent, err := eng.ComputeHierarchicalRisk(claim, "TRUE", model, group,
		map[hbayes.ProviderGroupID]float64{"SATFEED_X": 0.0})
	must(t, err)

	if correlated.EffectiveIndependentSignals >= independent.EffectiveIndependentSignals {
		t.Fatalf("expected correlated evidence to count as fewer effective independent signals: correlated=%f independent=%f",
			correlated.EffectiveIndependentSignals, independent.EffectiveIndependentSignals)
	}
	if correlated.Probability >= independent.Probability {
		t.Fatalf("expected correlated evidence to yield lower fused probability than independent evidence: correlated=%f independent=%f",
			correlated.Probability, independent.Probability)
	}
	// ~1 effective signal for 3 fully-correlated (rho=1) resellers.
	if diff := correlated.EffectiveIndependentSignals - 1.0; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("expected ~1 effective independent signal for fully correlated group, got %f", correlated.EffectiveIndependentSignals)
	}
}

func TestComputeHierarchicalRiskIsReadOnly(t *testing.T) {
	eng := NewEngine(nil)
	must(t, eng.RegisterSource(SourceProfile{ID: "s1", BaseReliability: 0.8}))
	claim := Claim{Subject: "VESSEL:2", Predicate: "FLAG"}
	_, err := eng.Submit("s1", claim, "PANAMA", 1)
	must(t, err)

	before := len(eng.evidence[claim.Key()])
	model, err := hbayes.NewModel(
		[]hbayes.ReliabilityPrior{{GroupID: "G", Mean: 0.8, Variance: 0.02}},
		hbayes.CorrelationMatrix{Groups: []hbayes.ProviderGroupID{"G"}, Values: [][]float64{{1.0}}},
	)
	must(t, err)
	_, err = eng.ComputeHierarchicalRisk(claim, "PANAMA", model, func(SourceID) hbayes.ProviderGroupID { return "G" }, nil)
	must(t, err)
	after := len(eng.evidence[claim.Key()])
	if before != after {
		t.Fatalf("expected ComputeHierarchicalRisk not to mutate evidence log: before=%d after=%d", before, after)
	}
}
