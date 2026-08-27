package patterns

import "testing"

func TestStandardPatterns_Count(t *testing.T) {
	ps := StandardPatterns()
	if len(ps) != 7 {
		t.Fatalf("expected 7 standard patterns, got %d", len(ps))
	}
	// Deterministic order.
	for i := 1; i < len(ps); i++ {
		if ps[i-1].ID() >= ps[i].ID() {
			t.Fatalf("standard patterns not in stable sorted order at %d", i)
		}
	}
}

func TestDarkActivityPattern_Hits(t *testing.T) {
	p := darkActivityPattern{}
	m := p.Evaluate(Signal{AISGapHours: 72, DarkPortCallCount: 1})
	if !m.Hit {
		t.Fatalf("expected hit for 72h AIS gap + dark port call")
	}
	if m.Score <= 0 || m.Score > 1 {
		t.Errorf("score out of range: %v", m.Score)
	}
}

func TestDarkActivityPattern_NoHitBelowThreshold(t *testing.T) {
	p := darkActivityPattern{}
	m := p.Evaluate(Signal{AISGapHours: 5, DarkPortCallCount: 0})
	if m.Hit {
		t.Fatalf("did not expect hit for minor AIS gap")
	}
}

func TestSTSTransshipmentPattern_NearSanctioned(t *testing.T) {
	p := stsTransshipmentPattern{}
	m := p.Evaluate(Signal{STSTransferCount: 2, STSNearSanctionedJurisdiction: true})
	if !m.Hit || m.Score < 0.5 {
		t.Fatalf("expected high-confidence hit near sanctioned jurisdiction, got %+v", m)
	}
}

func TestLibrary_EvaluateDeterministicOrder(t *testing.T) {
	lib := NewLibrary(StandardPatterns())
	sig := Signal{
		AISGapHours:                   60,
		DarkPortCallCount:             1,
		STSTransferCount:              1,
		STSNearSanctionedJurisdiction: true,
		FlagChangeCount:               2,
		FlagChangeNearSanctionEvent:   true,
	}
	m1, err := lib.Evaluate("VESSEL_A", sig, 1)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	lib2 := NewLibrary(StandardPatterns())
	m2, err := lib2.Evaluate("VESSEL_A", sig, 1)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(m1) != len(m2) {
		t.Fatalf("mismatched match count between independent evaluations")
	}
	for i := range m1 {
		if m1[i].PatternID != m2[i].PatternID || m1[i].Hit != m2[i].Hit || m1[i].Score != m2[i].Score {
			t.Fatalf("non-deterministic evaluation at index %d: %+v vs %+v", i, m1[i], m2[i])
		}
	}
}

func TestAggregateScore_OrderInvariant(t *testing.T) {
	matches := []Match{
		{PatternID: "A", Hit: true, Score: 0.5},
		{PatternID: "B", Hit: true, Score: 0.3},
		{PatternID: "C", Hit: false, Score: 0},
		{PatternID: "D", Hit: true, Score: 0.8},
	}
	reversed := []Match{matches[3], matches[2], matches[1], matches[0]}

	s1 := AggregateScore(matches)
	s2 := AggregateScore(reversed)
	if s1 != s2 {
		t.Fatalf("AggregateScore not order-invariant: %v vs %v", s1, s2)
	}
	// Sanity: noisy-OR of 0.5, 0.3, 0.8 = 1 - (0.5*0.7*0.2) = 1 - 0.07 = 0.93
	want := 1 - (0.5 * 0.7 * 0.2)
	if diff := s1 - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("AggregateScore = %v, want %v", s1, want)
	}
}

func TestAggregateScore_NoHitsIsZero(t *testing.T) {
	matches := []Match{{Hit: false}, {Hit: false}}
	if got := AggregateScore(matches); got != 0 {
		t.Errorf("expected 0 for no hits, got %v", got)
	}
}

func TestLibrary_VerifyChainDetectsTamper(t *testing.T) {
	lib := NewLibrary(StandardPatterns())
	if _, err := lib.Evaluate("VESSEL_TAMPER", Signal{AISGapHours: 50, DarkPortCallCount: 1}, 1); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if err := lib.VerifyChain(); err != nil {
		t.Fatalf("expected clean chain, got %v", err)
	}
	lib.log[0].Matches = "tampered"
	if err := lib.VerifyChain(); err == nil {
		t.Fatalf("expected VerifyChain to detect tampering")
	}
}

func TestIdentitySpoofingPattern_HighConfidence(t *testing.T) {
	p := identitySpoofingPattern{}
	m := p.Evaluate(Signal{IdentitySpoofingSuspected: true})
	if !m.Hit || m.Score < 0.8 {
		t.Fatalf("expected high-confidence spoofing hit, got %+v", m)
	}
}

func TestChainLengthPattern_LegitimateChainNoHit(t *testing.T) {
	p := chainLengthPattern{}
	m := p.Evaluate(Signal{TransshipmentChainLength: 1})
	if m.Hit {
		t.Fatalf("did not expect hit for a single-hop (direct) shipment")
	}
}
