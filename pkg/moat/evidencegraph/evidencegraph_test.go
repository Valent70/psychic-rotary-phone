package evidencegraph

import "testing"

// TestSharedUpstreamDiscountsEffectiveWeight is the exact scenario the
// audit named: two sources depending on the SAME satellite provider
// must not count as fully independent evidence.
func TestSharedUpstreamDiscountsEffectiveWeight(t *testing.T) {
	g := NewGraph()
	g.DependsOn("source-A", "satellite-provider-X", DependsOnProducer)
	g.DependsOn("source-B", "satellite-provider-X", DependsOnProducer)
	g.DependsOn("source-C", "independent-feed-Y", DependsOnProducer)

	base := map[NodeID]float64{"source-A": 0.8, "source-B": 0.8, "source-C": 0.8}
	eff := g.EffectiveWeight([]NodeID{"source-A", "source-B", "source-C"}, base)

	if eff["source-C"] != base["source-C"] {
		t.Fatalf("expected fully independent source-C to keep full weight, got %f", eff["source-C"])
	}
	if eff["source-A"] >= base["source-A"] || eff["source-B"] >= base["source-B"] {
		t.Fatalf("expected shared-upstream sources A/B to be discounted below base weight, got A=%f B=%f (base=%f)",
			eff["source-A"], eff["source-B"], base["source-A"])
	}
}

// TestNoDependencyMeansNoDiscount proves the graph never penalizes
// evidence it has no correlation information about.
func TestNoDependencyMeansNoDiscount(t *testing.T) {
	g := NewGraph()
	base := map[NodeID]float64{"a": 0.5, "b": 0.5}
	eff := g.EffectiveWeight([]NodeID{"a", "b"}, base)
	if eff["a"] != base["a"] || eff["b"] != base["b"] {
		t.Fatalf("expected unchanged weights with no dependency edges, got %v", eff)
	}
}

// TestTransitiveDependencyIsDetected proves shared ancestry is found
// through multi-hop derivation chains, not just direct edges.
func TestTransitiveDependencyIsDetected(t *testing.T) {
	g := NewGraph()
	g.DependsOn("derived-A", "raw-feed-1", DependsOnDerivation)
	g.DependsOn("derived-B", "model-transform", DependsOnTransformation)
	g.DependsOn("model-transform", "raw-feed-1", DependsOnDerivation)

	base := map[NodeID]float64{"derived-A": 1.0, "derived-B": 1.0}
	eff := g.EffectiveWeight([]NodeID{"derived-A", "derived-B"}, base)
	if eff["derived-A"] >= base["derived-A"] {
		t.Fatalf("expected transitively-shared raw-feed-1 ancestry to discount derived-A, got %f", eff["derived-A"])
	}
}

// TestCorrelationFamiliesGroupsSharedAncestry checks the coarser
// grouping view matches EffectiveWeight's underlying logic.
func TestCorrelationFamiliesGroupsSharedAncestry(t *testing.T) {
	g := NewGraph()
	g.DependsOn("a", "shared", DependsOnSource)
	g.DependsOn("b", "shared", DependsOnSource)
	g.DependsOn("c", "other", DependsOnSource)

	families := g.CorrelationFamilies([]NodeID{"a", "b", "c"})
	if len(families) != 2 {
		t.Fatalf("expected 2 correlation families (a+b, c), got %d: %v", len(families), families)
	}
}

// TestCyclicGraphDoesNotHang proves ancestor traversal is cycle-safe.
func TestCyclicGraphDoesNotHang(t *testing.T) {
	g := NewGraph()
	g.DependsOn("x", "y", DependsOnSource)
	g.DependsOn("y", "x", DependsOnSource) // cycle
	base := map[NodeID]float64{"x": 1, "y": 1}
	eff := g.EffectiveWeight([]NodeID{"x", "y"}, base)
	if eff["x"] < 0 || eff["y"] < 0 {
		t.Fatalf("expected finite non-negative weights despite cycle, got %v", eff)
	}
}
