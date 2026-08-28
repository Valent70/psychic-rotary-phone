package evidence

import (
	"testing"

	"veriqo/pkg/core/trustcalc"
)

func TestFuseWeightedMajority(t *testing.T) {
	trust := NewSourceTrust()
	trust.Set("radar-1", 0.9)
	trust.Set("tip-anonymous", 0.2)
	e := New(trust, 0)

	if _, err := e.Ingest(Observation{ClaimKey: "vessel/IMO1", SourceID: "radar-1", Value: "at-sea", Confidence: 0.95, Tick: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Ingest(Observation{ClaimKey: "vessel/IMO1", SourceID: "tip-anonymous", Value: "in-port", Confidence: 0.9, Tick: 1}); err != nil {
		t.Fatal(err)
	}

	res, err := e.Fuse("vessel/IMO1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.WinningValue != "at-sea" {
		t.Fatalf("expected trusted source to win, got %s", res.WinningValue)
	}
	if res.ContestedValues != 1 {
		t.Fatalf("expected 1 contested value, got %d", res.ContestedValues)
	}
}

func TestFuseDecayReducesStaleWeight(t *testing.T) {
	e := New(nil, 4)
	if _, err := e.Ingest(Observation{ClaimKey: "c1", SourceID: "s1", Value: "v1", Confidence: 1.0, Tick: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Ingest(Observation{ClaimKey: "c1", SourceID: "s2", Value: "v2", Confidence: 0.6, Tick: 8}); err != nil {
		t.Fatal(err)
	}
	res, err := e.Fuse("c1", 8)
	if err != nil {
		t.Fatal(err)
	}
	if res.WinningValue != "v2" {
		t.Fatalf("expected decay to let fresher evidence win, got %s", res.WinningValue)
	}
}

func TestFuseNoObservationsErrors(t *testing.T) {
	e := New(nil, 0)
	if _, err := e.Fuse("missing", 0); err == nil {
		t.Fatal("expected error for claim with no observations")
	}
}

func TestIngestValidation(t *testing.T) {
	e := New(nil, 0)
	if _, err := e.Ingest(Observation{ClaimKey: "", SourceID: "s", Value: "v", Confidence: 0.5}); err == nil {
		t.Fatal("expected error for missing ClaimKey")
	}
	if _, err := e.Ingest(Observation{ClaimKey: "c", SourceID: "s", Value: "v", Confidence: 1.5}); err == nil {
		t.Fatal("expected error for out-of-range Confidence")
	}
}

func TestProvenanceRoundTrip(t *testing.T) {
	e := New(nil, 0)
	parent, err := e.Ingest(Observation{ClaimKey: "c1", SourceID: "s1", Value: "v1", Confidence: 0.8, Tick: 1})
	if err != nil {
		t.Fatal(err)
	}
	child, err := e.Ingest(Observation{ClaimKey: "c1", SourceID: "s1", Value: "v1-corrected", Confidence: 0.85, Tick: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Link(child, parent, "derived_from"); err != nil {
		t.Fatal(err)
	}
	lineage, err := e.Provenance(child)
	if err != nil {
		t.Fatal(err)
	}
	if len(lineage) != 1 || lineage[0].ID != parent {
		t.Fatalf("expected lineage to contain parent node, got %+v", lineage)
	}
}

// TestUsesSharedTrustCalculus proves the cross-system wiring the
// critique asked for: a trustcalc.Calculus updated elsewhere (e.g. by
// veriqo/core/intelligence) changes Evidence's fusion weighting the
// next time that source is observed, because both read the same
// Calculus object.
func TestUsesSharedTrustCalculus(t *testing.T) {
	calc := trustcalc.New(1, 1)
	e := New(calc, 0)
	if _, err := e.Ingest(Observation{ClaimKey: "c1", SourceID: "s1", Value: "a", Confidence: 1.0, Tick: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Ingest(Observation{ClaimKey: "c1", SourceID: "s2", Value: "b", Confidence: 1.0, Tick: 1}); err != nil {
		t.Fatal(err)
	}
	res, err := e.Fuse("c1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Confidence != 0.5 {
		t.Fatalf("expected a tie at equal prior trust, got confidence=%v winner=%s", res.Confidence, res.WinningValue)
	}

	// Now simulate Intelligence having learned s1 is far more reliable.
	for i := 0; i < 10; i++ {
		_, _ = calc.Observe("s1", true, 1.0)
		_, _ = calc.Observe("s2", false, 1.0)
	}
	if _, err := e.Ingest(Observation{ClaimKey: "c2", SourceID: "s1", Value: "a", Confidence: 1.0, Tick: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Ingest(Observation{ClaimKey: "c2", SourceID: "s2", Value: "b", Confidence: 1.0, Tick: 2}); err != nil {
		t.Fatal(err)
	}
	res2, err := e.Fuse("c2", 2)
	if err != nil {
		t.Fatal(err)
	}
	if res2.WinningValue != "a" {
		t.Fatalf("expected s1 (now trusted by the shared calculus) to win, got %s (confidence %v)", res2.WinningValue, res2.Confidence)
	}
}
