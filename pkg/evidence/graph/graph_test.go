package graph

import "testing"

func TestAddNodeDeduplicatesByContent(t *testing.T) {
	g := NewGraph()
	id1 := g.AddNode("raw_observation", "ais-a:PANAMA")
	id2 := g.AddNode("raw_observation", "ais-a:PANAMA")
	if id1 != id2 {
		t.Fatalf("expected identical content to dedupe to same NodeID, got %s vs %s", id1, id2)
	}
	if g.NodeCount() != 1 {
		t.Fatalf("expected 1 node after duplicate insert, got %d", g.NodeCount())
	}
}

func TestLineageTracesFullAncestry(t *testing.T) {
	g := NewGraph()
	obsA := g.AddNode("raw_observation", "ais-a:PANAMA")
	obsB := g.AddNode("raw_observation", "ais-b:PANAMA")
	claim := g.AddNode("truth_version", "VESSEL:1|FLAG=PANAMA")
	decision := g.AddNode("decision", "ESCALATE")

	must(t, g.AddEdge(claim, obsA, RelationDerivedFrom))
	must(t, g.AddEdge(claim, obsB, RelationDerivedFrom))
	must(t, g.AddEdge(decision, claim, RelationDerivedFrom))

	lineage, err := g.Lineage(decision)
	must(t, err)
	if len(lineage) != 3 {
		t.Fatalf("expected decision's lineage to include claim+obsA+obsB (3 nodes), got %d: %+v", len(lineage), lineage)
	}
	found := map[NodeID]bool{}
	for _, n := range lineage {
		found[n.ID] = true
	}
	for _, want := range []NodeID{obsA, obsB, claim} {
		if !found[want] {
			t.Fatalf("expected lineage to include %s", want)
		}
	}
}

func TestInfluencedIsInverseOfLineage(t *testing.T) {
	g := NewGraph()
	obs := g.AddNode("raw_observation", "s1:VALUE")
	claim := g.AddNode("truth_version", "CLAIM=VALUE")
	decision := g.AddNode("decision", "FLAG")

	must(t, g.AddEdge(claim, obs, RelationDerivedFrom))
	must(t, g.AddEdge(decision, claim, RelationDerivedFrom))

	influenced, err := g.Influenced(obs)
	must(t, err)
	found := map[NodeID]bool{}
	for _, n := range influenced {
		found[n.ID] = true
	}
	if !found[claim] || !found[decision] {
		t.Fatalf("expected obs's influence to reach claim and decision, got %+v", influenced)
	}
}

func TestAddEdgeRejectsCycle(t *testing.T) {
	g := NewGraph()
	a := g.AddNode("k", "a")
	b := g.AddNode("k", "b")
	must(t, g.AddEdge(b, a, RelationDerivedFrom)) // b derived from a
	if err := g.AddEdge(a, b, RelationDerivedFrom); err == nil {
		t.Fatalf("expected cycle rejection when a is made to derive from b (which derives from a)")
	}
}

func TestAddEdgeRejectsMissingNodes(t *testing.T) {
	g := NewGraph()
	a := g.AddNode("k", "a")
	if err := g.AddEdge(a, "nonexistent", RelationDerivedFrom); err == nil {
		t.Fatalf("expected error for missing To node")
	}
}

func TestProvenanceReturnsBothDirections(t *testing.T) {
	g := NewGraph()
	a := g.AddNode("k", "a")
	b := g.AddNode("k", "b")
	c := g.AddNode("k", "c")
	must(t, g.AddEdge(b, a, RelationDerivedFrom))
	must(t, g.AddEdge(c, b, RelationSupports))

	prov := g.Provenance(b)
	if len(prov) != 2 {
		t.Fatalf("expected 2 edges touching b (incoming from c, outgoing to a), got %d: %+v", len(prov), prov)
	}
}

func TestSupportsAndContradictsAreNotCycleChecked(t *testing.T) {
	g := NewGraph()
	a := g.AddNode("k", "a")
	b := g.AddNode("k", "b")
	must(t, g.AddEdge(a, b, RelationSupports))
	if err := g.AddEdge(b, a, RelationSupports); err != nil {
		t.Fatalf("SUPPORTS edges should not be cycle-checked, got error: %v", err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
