package causal

import (
	"errors"
	"math"
	"testing"
)

const eps = 1e-9

func almostEqual(a, b float64) bool { return math.Abs(a-b) < eps }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestObserveValidatesStrength(t *testing.T) {
	g := NewGraph()
	if _, err := g.Observe("a", "X", "Y", 0, 1, "", 1); !errors.Is(err, ErrInvalidStrength) {
		t.Fatalf("want ErrInvalidStrength for 0, got %v", err)
	}
	if _, err := g.Observe("a", "X", "Y", 1.1, 1, "", 1); !errors.Is(err, ErrInvalidStrength) {
		t.Fatalf("want ErrInvalidStrength for >1, got %v", err)
	}
	if _, err := g.Observe("a", "X", "Y", 0.8, 1, "", 1); err != nil {
		t.Fatalf("valid strength rejected: %v", err)
	}
}

// TestWhySingleHop proves the baseline root-cause query.
func TestWhySingleHop(t *testing.T) {
	g := NewGraph()
	if _, err := g.Observe("analyst", "AIS_OFF", "STS", 0.8, 2, "", 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	paths, err := g.Why("STS")
	must(t, err)
	if len(paths) != 1 || paths[0].Chain[0] != "AIS_OFF" {
		t.Fatalf("unexpected paths: %+v", paths)
	}
	if !almostEqual(paths[0].Strength, 0.8) {
		t.Fatalf("strength = %v, want 0.8", paths[0].Strength)
	}
}

// TestWhyMultiHopChain proves the exact scenario named in
// Yang_masih_kurang.docx: AIS_OFF -> STS -> CARGO_CHANGE ->
// OWNERSHIP_CHANGE must be explainable as one causal chain, not isolated
// facts.
func TestWhyMultiHopChain(t *testing.T) {
	g := NewGraph()
	if _, err := g.Observe("a", "AIS_OFF", "STS", 0.8, 1, "", 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := g.Observe("a", "STS", "CARGO_CHANGE", 0.7, 1, "", 2); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := g.Observe("a", "CARGO_CHANGE", "OWNERSHIP_CHANGE", 0.6, 1, "", 3); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	paths, err := g.Why("OWNERSHIP_CHANGE")
	must(t, err)
	if len(paths) != 1 {
		t.Fatalf("expected exactly one causal chain, got %d: %+v", len(paths), paths)
	}
	want := []NodeID{"AIS_OFF", "STS", "CARGO_CHANGE", "OWNERSHIP_CHANGE"}
	got := paths[0].Chain
	if len(got) != len(want) {
		t.Fatalf("chain = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("chain[%d] = %v, want %v (full: %v)", i, got[i], want[i], got)
		}
	}
	wantStrength := 0.8 * 0.7 * 0.6
	if !almostEqual(paths[0].Strength, wantStrength) {
		t.Fatalf("combined strength = %v, want %v", paths[0].Strength, wantStrength)
	}
}

func TestWhyMultipleConvergingPathsRankedByStrength(t *testing.T) {
	g := NewGraph()
	if _, err := g.Observe("a", "AIS_ANOMALY", "RISK", 0.9, 1, "", 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := g.Observe("a", "SANCTION_HIT", "RISK", 0.3, 1, "", 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	paths, err := g.Why("RISK")
	must(t, err)
	if len(paths) != 2 {
		t.Fatalf("expected 2 paths, got %d", len(paths))
	}
	if paths[0].Strength < paths[1].Strength {
		t.Fatalf("paths must be sorted strongest-first: %+v", paths)
	}
	if paths[0].Chain[0] != "AIS_ANOMALY" {
		t.Fatalf("strongest path should start at AIS_ANOMALY, got %v", paths[0].Chain)
	}
}

func TestWhyDetectsCycle(t *testing.T) {
	g := NewGraph()
	if _, err := g.Observe("a", "X", "Y", 0.5, 1, "", 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := g.Observe("a", "Y", "X", 0.5, 1, "", 2); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err := g.Why("X")
	if !errors.Is(err, ErrCycleDetected) {
		t.Fatalf("expected ErrCycleDetected, got %v", err)
	}
}

func TestAggregateSupportNoisyOR(t *testing.T) {
	g := NewGraph()
	if _, err := g.Observe("a", "C1", "EFFECT", 0.7, 1, "", 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := g.Observe("a", "C2", "EFFECT", 0.6, 1, "", 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := g.AggregateSupport("EFFECT")
	want := 1 - (1-0.7)*(1-0.6)
	if !almostEqual(got, want) {
		t.Fatalf("aggregate = %v, want %v", got, want)
	}
	if g.AggregateSupport("NOTHING_CAUSES_THIS") != 0 {
		t.Fatalf("expected 0 support for an effect with no observed causes")
	}
}

// TestWhatIfCounterfactualDoesNotMutateGraph proves the counterfactual
// query is pure: removing AIS_OFF from consideration must lower support
// for EFFECT, but the graph's own state (and hash chain) must be
// unaffected afterward.
func TestWhatIfCounterfactualDoesNotMutateGraph(t *testing.T) {
	g := NewGraph()
	if _, err := g.Observe("a", "AIS_OFF", "EFFECT", 0.8, 1, "", 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := g.Observe("a", "SAR_MISMATCH", "EFFECT", 0.5, 1, "", 2); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	headBefore := g.Head()

	before, after := g.WhatIf("EFFECT", "AIS_OFF")
	wantBefore := 1 - (1-0.8)*(1-0.5)
	wantAfter := 0.5
	if !almostEqual(before, wantBefore) {
		t.Fatalf("before = %v, want %v", before, wantBefore)
	}
	if !almostEqual(after, wantAfter) {
		t.Fatalf("after = %v, want %v", after, wantAfter)
	}
	if after >= before {
		t.Fatalf("removing a cause must not increase support: before=%v after=%v", before, after)
	}
	if g.Head() != headBefore {
		t.Fatalf("WhatIf mutated the graph: head changed from %s to %s", headBefore, g.Head())
	}
}

func TestVerifyChainDetectsTamper(t *testing.T) {
	g := NewGraph()
	if _, err := g.Observe("a", "X", "Y", 0.5, 1, "", 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := g.VerifyChain(); err != nil {
		t.Fatalf("untampered chain failed verify: %v", err)
	}
	g.log[0].Strength = 0.99
	if err := g.VerifyChain(); !errors.Is(err, ErrTamperDetected) {
		t.Fatalf("expected ErrTamperDetected, got %v", err)
	}
}

// TestRebuildFromLogIsDeterministic proves causal graph state (as probed
// via Why()) is fully reconstructable from the observation log alone.
func TestRebuildFromLogIsDeterministic(t *testing.T) {
	g := NewGraph()
	if _, err := g.Observe("a", "AIS_OFF", "STS", 0.8, 1, "", 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := g.Observe("a", "STS", "CARGO_CHANGE", 0.7, 1, "", 2); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rebuilt, err := Rebuild(g.ReplayAll())
	must(t, err)
	if rebuilt.Head() != g.Head() {
		t.Fatalf("rebuilt head %s != original head %s", rebuilt.Head(), g.Head())
	}
	origPaths, _ := g.Why("CARGO_CHANGE")
	rebuiltPaths, _ := rebuilt.Why("CARGO_CHANGE")
	if len(origPaths) != len(rebuiltPaths) || origPaths[0].Strength != rebuiltPaths[0].Strength {
		t.Fatalf("rebuilt graph diverges: orig=%+v rebuilt=%+v", origPaths, rebuiltPaths)
	}
}

func TestRebuildRejectsTamperedLog(t *testing.T) {
	g := NewGraph()
	if _, err := g.Observe("a", "X", "Y", 0.5, 1, "", 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	log := g.ReplayAll()
	log[0].Strength = 0.9 // tamper without recomputing hash
	if _, err := Rebuild(log); !errors.Is(err, ErrTamperDetected) {
		t.Fatalf("expected ErrTamperDetected on rebuild, got %v", err)
	}
}
