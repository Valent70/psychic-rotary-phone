package provenance

import "testing"

// TestPartialKnowledge_OneDeclaredOneUndeclared_NeverIndependent is the
// exact regression proof for the v7.10 audit finding: Assess()
// previously granted StatusDeclaredIndependent whenever the ratio hit
// 1.0 as long as ANY of the queried sources was declared. A single
// declared source paired with a source that was NEVER registered in
// the graph at all trivially has "no shared ancestors" (the
// undeclared source has zero ancestors by definition), so the old
// logic misreported this pure partial-knowledge gap as a verified
// independence finding.
func TestPartialKnowledge_OneDeclaredOneUndeclared_NeverIndependent(t *testing.T) {
	p := New()
	if err := p.Declare("AIS_VENDOR_A", "SATELLITE_OPERATOR_X"); err != nil {
		t.Fatal(err)
	}
	// "GHOST_SOURCE" is intentionally never Declare()'d — it has no
	// entry in p.nodeIDs at all.
	got, err := p.Assess([]string{"AIS_VENDOR_A", "GHOST_SOURCE"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status == StatusDeclaredIndependent {
		t.Fatalf("partial-knowledge pair must never be reported DECLARED_INDEPENDENT, got status=%s score=%v", got.Status, got.Score)
	}
	if got.Status != StatusPartialUnknown {
		t.Fatalf("expected StatusPartialUnknown for one declared + one undeclared source, got %s", got.Status)
	}
}

// TestPartialKnowledge_AllDeclaredNoSharedAncestors_StillIndependent
// proves the fix did not regress the legitimate case: when EVERY
// source in the query is actually declared and genuinely has no
// shared ancestor, DECLARED_INDEPENDENT must still be granted.
func TestPartialKnowledge_AllDeclaredNoSharedAncestors_StillIndependent(t *testing.T) {
	p := New()
	if err := p.Declare("AIS_VENDOR_A", "ROOT_A"); err != nil {
		t.Fatal(err)
	}
	if err := p.Declare("AIS_VENDOR_B", "ROOT_B"); err != nil {
		t.Fatal(err)
	}
	got, err := p.Assess([]string{"AIS_VENDOR_A", "AIS_VENDOR_B"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusDeclaredIndependent {
		t.Fatalf("expected StatusDeclaredIndependent for two fully-declared sources with disjoint roots, got %s", got.Status)
	}
}

// TestPartialKnowledge_NoneDeclared_IsUnknownNotPartial proves the
// three-way distinction actually holds: zero declared sources must
// stay StatusUnknown, distinct from the new StatusPartialUnknown.
func TestPartialKnowledge_NoneDeclared_IsUnknownNotPartial(t *testing.T) {
	p := New()
	got, err := p.Assess([]string{"GHOST_1", "GHOST_2"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusUnknown {
		t.Fatalf("expected StatusUnknown when no source is declared, got %s", got.Status)
	}
}
