// Session update — closes further roadmap-adjacent coverage in
// pkg/kernel/execgraph. The existing suite only ever called AddEdge
// once, to prove cycle rejection — its unknown-from/unknown-to
// branches, and the successful-new-edge path that actually calls
// recomputeReadyLocked with a real effect on readiness, were never
// exercised. NodeIDs(), previously 0% covered, is also closed here.
package execgraph

import "testing"

func TestAddEdge_UnknownNodes(t *testing.T) {
	g := buildABC(t)
	if err := g.AddEdge("does-not-exist", "A"); err == nil {
		t.Fatal("expected an error when 'from' does not exist")
	}
	if err := g.AddEdge("A", "does-not-exist"); err == nil {
		t.Fatal("expected an error when 'to' does not exist")
	}
}

func TestAddEdge_NewDependencyBlocksThenUnblocksReadiness(t *testing.T) {
	g := NewGraph()
	if err := g.AddNode("X"); err != nil {
		t.Fatalf("add X: %v", err)
	}
	if err := g.AddNode("Y"); err != nil {
		t.Fatalf("add Y: %v", err)
	}
	// Y starts independent (no deps at AddNode time) so it is Ready.
	if st, _ := g.Status("Y"); st != Ready {
		t.Fatalf("expected Y Ready before any edge, got %s", st)
	}

	// Add a real, non-cyclic new dependency: Y now depends on X.
	if err := g.AddEdge("X", "Y"); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	// recomputeReadyLocked must have downgraded Y back out of Ready
	// since it now has an incomplete dependency (X is still Pending).
	if st, _ := g.Status("Y"); st == Ready {
		t.Fatal("expected Y to no longer be Ready once a new incomplete dependency was added")
	}
	if deps := g.Dependencies("Y"); len(deps) != 1 || deps[0] != "X" {
		t.Fatalf("expected Y to depend on X after AddEdge, got %v", deps)
	}

	// Complete X; Y should now become Ready via the same recompute path.
	if err := g.SetStatus("X", Running, 1); err != nil {
		t.Fatalf("SetStatus X Running: %v", err)
	}
	if err := g.SetStatus("X", Done, 2); err != nil {
		t.Fatalf("SetStatus X Done: %v", err)
	}
	if st, _ := g.Status("Y"); st != Ready {
		t.Fatalf("expected Y Ready once its added dependency X completed, got %s", st)
	}
}

func TestNodeIDs_SortedAndComplete(t *testing.T) {
	g := buildABC(t)
	got := g.NodeIDs()
	want := []string{"A", "B", "C"}
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("want sorted %v, got %v", want, got)
		}
	}
}
