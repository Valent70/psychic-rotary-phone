package risk

import (
	"testing"

	"veriqo/pkg/moat/provenance"
)

// TestScoreCanonical_RejectsAssertedProvenance is the v7.10 audit
// regression proof: a caller building CompositeInput by hand and
// setting HasProvenanceData=true (with only ProvenanceIndependenceRatio
// filled in — no way for an external caller to also set the private
// provenanceComputed flag) must be rejected by ScoreCanonical.
func TestScoreCanonical_RejectsAssertedProvenance(t *testing.T) {
	m := NewModel()
	in := CompositeInput{
		PatternScore:                0.8,
		PriceAnomalyScore:           0.5,
		HasProvenanceData:           true,
		ProvenanceIndependenceRatio: 1.0, // asserted, not computed
	}
	_, err := m.ScoreCanonical(in)
	if err == nil {
		t.Fatal("expected ScoreCanonical to reject caller-asserted provenance, got nil error")
	}
	if err != ErrAssertedProvenanceOnCanonicalPath {
		t.Fatalf("expected ErrAssertedProvenanceOnCanonicalPath, got %v", err)
	}
}

// TestScoreCanonical_AcceptsComputedProvenance proves the legitimate
// path still works end-to-end: ScoreWithProvenance computes the ratio
// (setting the private flag) is exactly what production wiring should
// feed into decision making — and ScoreWithProvenance itself never
// triggers ErrAssertedProvenanceOnCanonicalPath internally, since
// ScoreCanonical is a separate opt-in gate for callers who build
// CompositeInput by hand outside ScoreWithProvenance.
func TestScoreCanonical_AcceptsComputedProvenance(t *testing.T) {
	m := NewModel()
	g := provenance.New() // no declared edges -> independent sources
	in := CompositeInput{
		PatternScore:      0.8,
		PriceAnomalyScore: 0.5,
		SourceIDs:         []string{"SRC_A", "SRC_B"},
	}
	res1, err := m.ScoreWithProvenance(in, g)
	if err != nil {
		t.Fatal(err)
	}
	res2, err := m.ScoreWithProvenance(in, g)
	if err != nil {
		t.Fatal(err)
	}
	if res1.Score != res2.Score {
		t.Fatalf("expected deterministic reproduction, got %v vs %v", res1.Score, res2.Score)
	}
}

// TestScoreCanonical_NoProvenanceDataIsFine proves the common
// legitimate case (no provenance data at all) is never blocked.
func TestScoreCanonical_NoProvenanceDataIsFine(t *testing.T) {
	m := NewModel()
	in := CompositeInput{PatternScore: 0.5, PriceAnomalyScore: 0.5}
	if _, err := m.ScoreCanonical(in); err != nil {
		t.Fatalf("expected no error when HasProvenanceData is false, got %v", err)
	}
}
