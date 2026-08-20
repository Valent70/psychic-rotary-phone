package hbayes

import (
	"math"
	"testing"
)

// adversarial_test.go — P5 adversarial epistemic tests plus direct
// proofs of the P3 hardening claims from
// "Hasil_audit_brutal_Professional_Chief_Technical_Software_Auditor.docx".

// --- PSD validation (§14) --------------------------------------------------

func TestAdversarial_PairwiseValidButNotPositiveSemidefiniteRejected(t *testing.T) {
	// The audit's own example: pairwise correlations of +0.9, +0.9,
	// -0.9 among three variables are each individually within [-1,1]
	// and the matrix is symmetric with a unit diagonal, but no real
	// random vector can have this covariance structure (eigenvalues
	// are approximately {1.9, 1.9, -0.8} — the negative eigenvalue is
	// the mathematical impossibility). Validate must reject it.
	groups := []ProviderGroupID{"G1", "G2", "G3"}
	m := CorrelationMatrix{Groups: groups, Values: [][]float64{
		{1.0, 0.9, -0.9},
		{0.9, 1.0, 0.9},
		{-0.9, 0.9, 1.0},
	}}
	if err := m.Validate(); err != ErrNotPositiveSemidefinite {
		t.Fatalf("expected ErrNotPositiveSemidefinite, got %v", err)
	}
	if _, err := NewModel([]ReliabilityPrior{
		{GroupID: "G1", Mean: 0.8, Variance: 0.02},
		{GroupID: "G2", Mean: 0.8, Variance: 0.02},
		{GroupID: "G3", Mean: 0.8, Variance: 0.02},
	}, m); err != ErrNotPositiveSemidefinite {
		t.Fatalf("NewModel must also reject a non-PSD matrix, got %v", err)
	}
}

func TestAdversarial_ValidPSDMatrixAccepted(t *testing.T) {
	// A genuinely realizable structure (e.g. groups correlated through
	// a common factor) must NOT be rejected by the new PSD check.
	groups := []ProviderGroupID{"G1", "G2", "G3"}
	m := CorrelationMatrix{Groups: groups, Values: [][]float64{
		{1.0, 0.5, 0.5},
		{0.5, 1.0, 0.5},
		{0.5, 0.5, 1.0},
	}}
	if err := m.Validate(); err != nil {
		t.Fatalf("expected a valid equicorrelation matrix to pass PSD check, got %v", err)
	}
}

// --- order invariance (§15) ------------------------------------------------

func TestAdversarial_EventRiskIsOrderInvariantAcrossGroupLabeling(t *testing.T) {
	// Two labelings of the identical correlation STRUCTURE, chosen so
	// alphabetical sort (the only ordering combineDirectional uses, for
	// stable output — see hbayes.go) processes the groups in a
	// different sequence. Under the pre-hardening sequential-discount
	// algorithm this produced different probabilities for the same
	// underlying evidence; the hardened, order-invariant combination
	// must not.
	build := func(g1, g2, g3 ProviderGroupID) (*Model, []Signal) {
		priors := []ReliabilityPrior{
			{GroupID: g1, Mean: 0.8, Variance: 0.02},
			{GroupID: g2, Mean: 0.75, Variance: 0.02},
			{GroupID: g3, Mean: 0.7, Variance: 0.02},
		}
		corr := CorrelationMatrix{
			Groups: []ProviderGroupID{g1, g2, g3},
			Values: [][]float64{
				{1.0, 0.5, 0.3},
				{0.5, 1.0, 0.2},
				{0.3, 0.2, 1.0},
			},
		}
		m, err := NewModel(priors, corr)
		if err != nil {
			t.Fatalf("NewModel: %v", err)
		}
		signals := []Signal{
			{SourceID: "s1", Group: g1, Confirms: true, Reliability: 0.85},
			{SourceID: "s2", Group: g2, Confirms: true, Reliability: 0.80},
			{SourceID: "s3", Group: g3, Confirms: true, Reliability: 0.75},
		}
		return m, signals
	}
	intra := map[ProviderGroupID]float64{}

	// Sorts as constructed order: A_GROUP, B_GROUP, C_GROUP.
	mA, sigA := build("A_GROUP", "B_GROUP", "C_GROUP")
	// Sorts REVERSED relative to construction: F_GROUP, M_GROUP, Z_GROUP
	// (i.e. what was "first" in the correlation structure is now
	// processed last alphabetically, and vice versa).
	mB, sigB := build("Z_GROUP", "M_GROUP", "F_GROUP")

	rA, err := mA.ComputeEventRisk(sigA, intra)
	if err != nil {
		t.Fatalf("ComputeEventRisk A: %v", err)
	}
	rB, err := mB.ComputeEventRisk(sigB, intra)
	if err != nil {
		t.Fatalf("ComputeEventRisk B: %v", err)
	}
	if math.Abs(rA.Probability-rB.Probability) > 1e-9 {
		t.Fatalf("probability must not depend on group iteration/sort order: A=%v B=%v",
			rA.Probability, rB.Probability)
	}
}

// --- explicit negative evidence (§13) --------------------------------------

func TestAdversarial_ContradictingSignalsExplainAwayRisk(t *testing.T) {
	priors := []ReliabilityPrior{{GroupID: "AIS", Mean: 0.8, Variance: 0.02}}
	corr := CorrelationMatrix{Groups: []ProviderGroupID{"AIS"}, Values: [][]float64{{1.0}}}
	m, err := NewModel(priors, corr)
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}
	confirmOnly := []Signal{
		{SourceID: "s1", Group: "AIS", Confirms: true, Reliability: 0.85},
		{SourceID: "s2", Group: "AIS", Confirms: true, Reliability: 0.85},
	}
	withContradiction := []Signal{
		{SourceID: "s1", Group: "AIS", Confirms: true, Reliability: 0.85},
		{SourceID: "s2", Group: "AIS", Confirms: true, Reliability: 0.85},
		{SourceID: "s3", Group: "AIS", Confirms: false, Reliability: 0.85},
		{SourceID: "s4", Group: "AIS", Confirms: false, Reliability: 0.85},
	}
	intra := map[ProviderGroupID]float64{"AIS": 0.0}

	rConfirmOnly, err := m.ComputeEventRisk(confirmOnly, intra)
	if err != nil {
		t.Fatalf("ComputeEventRisk confirmOnly: %v", err)
	}
	rWithContra, err := m.ComputeEventRisk(withContradiction, intra)
	if err != nil {
		t.Fatalf("ComputeEventRisk withContradiction: %v", err)
	}
	if rWithContra.NegativeEvidenceDiscount <= 0 {
		t.Fatalf("expected a positive NegativeEvidenceDiscount when contradicting signals are present, got %v",
			rWithContra.NegativeEvidenceDiscount)
	}
	if rWithContra.Probability >= rConfirmOnly.Probability {
		t.Fatalf("contradicting evidence must explain away risk: withContra=%v confirmOnly=%v",
			rWithContra.Probability, rConfirmOnly.Probability)
	}
}

func TestAdversarial_NoContradictionMeansZeroNegativeDiscount(t *testing.T) {
	// Backward-compatibility guarantee: a signal set with no
	// Confirms=false entries must behave exactly as before (the field
	// simply zero-valued), since this is what every pre-existing test
	// and caller supplies.
	priors := []ReliabilityPrior{{GroupID: "AIS", Mean: 0.8, Variance: 0.02}}
	corr := CorrelationMatrix{Groups: []ProviderGroupID{"AIS"}, Values: [][]float64{{1.0}}}
	m, _ := NewModel(priors, corr)
	signals := []Signal{{SourceID: "s1", Group: "AIS", Confirms: true, Reliability: 0.9}}
	r, err := m.ComputeEventRisk(signals, map[ProviderGroupID]float64{"AIS": 0})
	if err != nil {
		t.Fatalf("ComputeEventRisk: %v", err)
	}
	if r.NegativeEvidenceDiscount != 0 {
		t.Fatalf("expected zero negative evidence discount with no contradicting signals, got %v", r.NegativeEvidenceDiscount)
	}
}

// --- posterior reliability actually integrated (§13) -----------------------

func TestAdversarial_EventRiskUsesPosteriorNotRawSignalAverage(t *testing.T) {
	// A group with a LOW prior (0.3) but a long, consistent run of
	// confirming signals should have a posterior mean noticeably above
	// both the prior and the raw per-signal reliability average, if
	// (and only if) ComputeEventRisk is actually consulting the
	// posterior rather than just averaging the raw Reliability field
	// (which is constant at 0.5 in every signal below, so a raw-average
	// implementation would produce a DIFFERENT, lower group weight than
	// the posterior-aware one does).
	priors := []ReliabilityPrior{{GroupID: "G", Mean: 0.3, Variance: 0.01}}
	corr := CorrelationMatrix{Groups: []ProviderGroupID{"G"}, Values: [][]float64{{1.0}}}
	m, err := NewModel(priors, corr)
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}
	var signals []Signal
	for i := 0; i < 30; i++ {
		signals = append(signals, Signal{SourceID: "s", Group: "G", Confirms: true, Reliability: 0.5})
	}
	posts := m.ComputePosteriorReliabilities(signals)
	if len(posts) != 1 {
		t.Fatalf("expected 1 posterior, got %d", len(posts))
	}
	posteriorMean := posts[0].PosteriorMean
	if posteriorMean <= 0.5 {
		t.Fatalf("30 confirms should push the posterior well above the raw 0.5 reliability, got %v", posteriorMean)
	}

	r, err := m.ComputeEventRisk(signals, map[ProviderGroupID]float64{"G": 0})
	if err != nil {
		t.Fatalf("ComputeEventRisk: %v", err)
	}
	// A raw-average implementation (avgRel=0.5, effectiveN=30) would
	// give weight = 1-(1-0.5)^30 ≈ 1.0 regardless of the posterior —
	// that would make this assertion vacuous. Use a SHORT signal run
	// instead where the effect is distinguishable from saturation.
	var fewSignals []Signal
	for i := 0; i < 2; i++ {
		fewSignals = append(fewSignals, Signal{SourceID: "s", Group: "G", Confirms: true, Reliability: 0.5})
	}
	rFew, err := m.ComputeEventRisk(fewSignals, map[ProviderGroupID]float64{"G": 0})
	if err != nil {
		t.Fatalf("ComputeEventRisk fewSignals: %v", err)
	}
	rawAverageWeight := 1 - math.Pow(1-0.5, 2) // what a raw-average implementation would compute: 0.75
	if math.Abs(rFew.Probability-rawAverageWeight) < 1e-9 {
		t.Fatalf("ComputeEventRisk probability (%v) matches the RAW-average formula (%v) exactly — "+
			"posterior reliability does not appear to be integrated", rFew.Probability, rawAverageWeight)
	}
	_ = r
}

// --- correlated evidence saturation (extends existing coverage) ------------

func TestAdversarial_FullyCorrelatedGroupSaturatesRegardlessOfSignalCount(t *testing.T) {
	priors := []ReliabilityPrior{{GroupID: "G", Mean: 0.8, Variance: 0.02}}
	corr := CorrelationMatrix{Groups: []ProviderGroupID{"G"}, Values: [][]float64{{1.0}}}
	m, _ := NewModel(priors, corr)
	sig3 := []Signal{
		{SourceID: "a", Group: "G", Confirms: true, Reliability: 0.85},
		{SourceID: "b", Group: "G", Confirms: true, Reliability: 0.85},
		{SourceID: "c", Group: "G", Confirms: true, Reliability: 0.85},
	}
	sig10 := make([]Signal, 0, 10)
	for i := 0; i < 10; i++ {
		sig10 = append(sig10, Signal{SourceID: "x", Group: "G", Confirms: true, Reliability: 0.85})
	}
	rho1 := map[ProviderGroupID]float64{"G": 1.0} // fully redundant
	r3, err := m.ComputeEventRisk(sig3, rho1)
	if err != nil {
		t.Fatalf("ComputeEventRisk sig3: %v", err)
	}
	r10, err := m.ComputeEventRisk(sig10, rho1)
	if err != nil {
		t.Fatalf("ComputeEventRisk sig10: %v", err)
	}
	if math.Abs(r3.EffectiveIndependentSignals-1.0) > 1e-9 || math.Abs(r10.EffectiveIndependentSignals-1.0) > 1e-9 {
		t.Fatalf("rho=1.0 must saturate to EXACTLY 1 effective independent signal regardless of raw count: r3=%v r10=%v",
			r3.EffectiveIndependentSignals, r10.EffectiveIndependentSignals)
	}
}
