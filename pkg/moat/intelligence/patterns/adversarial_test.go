package patterns

import (
	"math"
	"testing"
)

// TestAdversarial_InvalidSignalInputRejected covers v7.9 P1 "Numeric
// input validation": NaN/Inf/negative signal fields must be rejected
// before any pattern scoring runs, not silently propagated into a
// misleading score.
func TestAdversarial_InvalidSignalInputRejected(t *testing.T) {
	lib := NewLibrary(StandardPatterns())
	cases := []Signal{
		{AISGapHours: math.NaN()},
		{AISGapHours: math.Inf(1)},
		{AISGapHours: -5},
		{DarkPortCallCount: -1},
		{TransshipmentChainLength: -3},
	}
	for i, s := range cases {
		if _, err := lib.Evaluate("V", s, 1); err == nil {
			t.Errorf("case %d: expected error for invalid signal %+v", i, s)
		}
	}
}

// TestAdversarial_CorrelatedFamilyNoArtificialConfidenceExplosion is the
// exact v7.9 audit §24 ask ("same evidence -> 7 patterns -> ensure no
// artificial confidence explosion") and reproduces the audit's own
// worked example (§6): P001=0.8, P004=0.7, P007=0.6, all driven by one
// underlying AIS/behavioral data gap. The naive noisy-OR the audit
// criticized would report 1-(0.2*0.3*0.4) = 0.976 (97.6%) from what
// might be a single fact read three ways. The corrected, family-aware
// AggregateScore must NOT report anywhere near that.
func TestAdversarial_CorrelatedFamilyNoArtificialConfidenceExplosion(t *testing.T) {
	matches := []Match{
		{PatternID: "P001_DARK_ACTIVITY", Hit: true, Score: 0.8},
		{PatternID: "P004_STS_TRANSSHIPMENT", Hit: true, Score: 0.7},
		{PatternID: "P007_DRAFT_ANOMALY", Hit: true, Score: 0.6},
	}
	got := AggregateScore(matches)
	naive := AggregateScoreRaw(matches)

	if naive < 0.97 {
		t.Fatalf("test setup check failed: expected naive noisy-OR ~0.976, got %v", naive)
	}
	// Family-aware score should equal the family's single strongest
	// signal (max = 0.8), not the noisy-OR explosion.
	if got != 0.8 {
		t.Errorf("AggregateScore (family-aware) = %v, want 0.8 (max of correlated family)", got)
	}
	if got >= naive {
		t.Errorf("family-aware score (%v) should be strictly less than naive noisy-OR (%v) for correlated evidence", got, naive)
	}
}

// TestAdversarial_IndependentFamiliesStillCombine proves the fix isn't
// a blanket dampener: hits from GENUINELY different evidence families
// (flag-state registry timing + ownership registry timing) should
// still combine via noisy-OR across families, since those really are
// separate evidence.
func TestAdversarial_IndependentFamiliesStillCombine(t *testing.T) {
	matches := []Match{
		{PatternID: "P002_FLAG_HOPPING", Hit: true, Score: 0.6},
		{PatternID: "P003_OWNERSHIP_TIMING", Hit: true, Score: 0.7},
	}
	got := AggregateScore(matches)
	want := 1 - (0.4 * 0.3) // cross-family noisy-OR, unchanged
	if diff := got - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("AggregateScore across independent families = %v, want %v", got, want)
	}
}

// TestAdversarial_AllSevenPatternsHit runs the full shipped detector
// set simultaneously (the literal "7 patterns" from the same
// underlying evidence scenario) and checks the aggregate stays below
// the naive noisy-OR figure the audit flagged as an inflated result.
func TestAdversarial_AllSevenPatternsHit(t *testing.T) {
	s := Signal{
		AISGapHours:                      72,
		DarkPortCallCount:                2,
		STSTransferCount:                 2,
		STSNearSanctionedJurisdiction:    true,
		FlagChangeCount:                  3,
		FlagChangeNearSanctionEvent:      true,
		OwnershipChangeCount:             2,
		OwnershipChangeNearSanctionEvent: true,
		DraftChangeWithoutPortCall:       true,
		IdentitySpoofingSuspected:        true,
		TransshipmentChainLength:         5,
	}
	lib := NewLibrary(StandardPatterns())
	matches, err := lib.Evaluate("TEST_VESSEL", s, 1)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}

	hitCount := 0
	for _, m := range matches {
		if m.Hit {
			hitCount++
		}
	}
	if hitCount != 7 {
		t.Fatalf("expected all 7 patterns to hit for this saturated scenario, got %d", hitCount)
	}

	got := AggregateScore(matches)
	naive := AggregateScoreRaw(matches)
	if got > naive {
		t.Fatalf("family-aware aggregate (%v) must never exceed naive noisy-OR (%v)", got, naive)
	}
	// With 5 independent families ALSO saturated (flag/ownership/chain/
	// identity/AIS-cluster), the overall aggregate is expected to stay
	// high even after correction — 5 genuinely independent high signals
	// legitimately combine toward 1.0. The meaningful discount shows up
	// within the correlated AIS_BEHAVIORAL family specifically: 3 hits
	// (0.8-class each) collapsed to their single max instead of
	// noisy-OR'd together. Verify that narrower, decisive claim instead
	// of the necessarily-small effect on an already-saturated overall
	// aggregate.
	aisOnly := []Match{}
	for _, m := range matches {
		if familyOf(m.PatternID) == familyAISBehavioral {
			aisOnly = append(aisOnly, m)
		}
	}
	familyScore := AggregateScore(aisOnly)
	familyNaive := AggregateScoreRaw(aisOnly)
	if familyNaive-familyScore < 0.1 {
		t.Errorf("expected a meaningful correlation discount within the AIS_BEHAVIORAL family alone; naive=%v corrected=%v", familyNaive, familyScore)
	}
}
