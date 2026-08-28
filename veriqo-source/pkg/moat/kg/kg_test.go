package kg

import (
	"sync"
	"testing"
)

func TestUpsertAndGet(t *testing.T) {
	g := NewGraph()
	if _, err := g.UpsertNode("actor1", Node{ID: "n1", Kind: "Vessel"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	n, ok := g.GetNode("n1")
	if !ok || n.Kind != "Vessel" {
		t.Fatalf("expected node n1 kind Vessel, got %+v ok=%v", n, ok)
	}
}

func TestEdgeRequiresBothEndpoints(t *testing.T) {
	g := NewGraph()
	_, _ = g.UpsertNode("a", Node{ID: "n1"})
	if _, err := g.UpsertEdge("a", Edge{ID: "e1", From: "n1", To: "missing", Kind: "K"}); err != ErrEndpointMiss {
		t.Fatalf("expected ErrEndpointMiss, got %v", err)
	}
}

func TestCascadeDeleteRemovesEdges(t *testing.T) {
	g := NewGraph()
	_, _ = g.UpsertNode("a", Node{ID: "n1"})
	_, _ = g.UpsertNode("a", Node{ID: "n2"})
	_, _ = g.UpsertEdge("a", Edge{ID: "e1", From: "n1", To: "n2", Kind: "K"})
	if _, err := g.DeleteNode("a", "n1"); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	snap := g.Snapshot()
	for _, m := range snap {
		if m.Op == OpDeleteEdge {
			t.Fatal("cascade delete should mutate live edge map directly, not emit a separate DeleteEdge log entry")
		}
	}
	// verify edge is actually gone from live state via replay
	replayed, err := ReplayAll(snap)
	if err != nil {
		t.Fatalf("replay failed: %v", err)
	}
	if len(replayed.edges) != 0 {
		t.Fatalf("expected cascade-deleted edge to be gone, got %d edges", len(replayed.edges))
	}
}

func TestHashChainDeterministic_MapOrderIndependent(t *testing.T) {
	// Build the same graph twice with props inserted in different map
	// literal orders — Go randomizes map iteration, so if canonicalBytes
	// weren't sorted, RootHash would differ between runs.
	build := func() string {
		g := NewGraph()
		_, _ = g.UpsertNode("a", Node{ID: "n1", Kind: "X", Props: map[string]string{"z": "1", "a": "2", "m": "3"}})
		return g.RootHash()
	}
	h1 := build()
	h2 := build()
	for i := 0; i < 20; i++ {
		if build() != h1 {
			t.Fatal("root hash not deterministic across repeated builds (map iteration order leak)")
		}
	}
	if h1 != h2 {
		t.Fatal("root hash mismatch between two identical builds")
	}
}

func TestReplayAll_ByteIdenticalToLive(t *testing.T) {
	g := NewGraph()
	_, _ = g.UpsertNode("a", Node{ID: "n1", Kind: "X"})
	_, _ = g.UpsertNode("a", Node{ID: "n2", Kind: "Y"})
	_, _ = g.UpsertEdge("a", Edge{ID: "e1", From: "n1", To: "n2", Kind: "Rel"})

	replayed, err := ReplayAll(g.Snapshot())
	if err != nil {
		t.Fatalf("replay failed: %v", err)
	}
	if replayed.RootHash() != g.RootHash() {
		t.Fatalf("replayed root hash %s != live root hash %s", replayed.RootHash(), g.RootHash())
	}
}

func TestReplayAll_RejectsTamperedPayload(t *testing.T) {
	g := NewGraph()
	_, _ = g.UpsertNode("a", Node{ID: "n1", Kind: "X"})
	snap := g.Snapshot()
	snap[0].Node.Kind = "TAMPERED" // mutate payload without recomputing hash
	if _, err := ReplayAll(snap); err == nil {
		t.Fatal("expected tamper detection to fail replay")
	}
}

func TestReplayAll_RejectsBrokenChainLinkage(t *testing.T) {
	g := NewGraph()
	_, _ = g.UpsertNode("a", Node{ID: "n1"})
	_, _ = g.UpsertNode("a", Node{ID: "n2"})
	snap := g.Snapshot()
	snap[1].PrevHash = "deadbeef"
	if _, err := ReplayAll(snap); err == nil {
		t.Fatal("expected broken chain linkage to be rejected")
	}
}

func TestReplayAll_RejectsReorderedEntries(t *testing.T) {
	g := NewGraph()
	_, _ = g.UpsertNode("a", Node{ID: "n1"})
	_, _ = g.UpsertNode("a", Node{ID: "n2"})
	snap := g.Snapshot()
	snap[0], snap[1] = snap[1], snap[0]
	if _, err := ReplayAll(snap); err == nil {
		t.Fatal("expected reordered entries to be rejected")
	}
}

func TestConcurrentApply_SerializedNoCorruption(t *testing.T) {
	g := NewGraph()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = g.UpsertNode("actor", Node{ID: "n"}) // same ID upsert race
		}(i)
	}
	wg.Wait()
	if g.LogLen() != 100 {
		t.Fatalf("expected 100 serialized mutations, got %d", g.LogLen())
	}
	// chain must still be replay-valid after concurrent writes
	if _, err := ReplayAll(g.Snapshot()); err != nil {
		t.Fatalf("chain corrupted under concurrency: %v", err)
	}
}

func TestGetClusterStatus_FiltersByKind(t *testing.T) {
	g := NewGraph()
	_, _ = g.UpsertNode("a", Node{ID: "cn1", Kind: "ClusterNode"})
	_, _ = g.UpsertNode("a", Node{ID: "other", Kind: "Vessel"})
	_, _ = g.UpsertNode("a", Node{ID: "role1", Kind: "Role"})
	_, _ = g.UpsertEdge("a", Edge{ID: "e1", From: "cn1", To: "role1", Kind: "HasRole"})
	status := g.GetClusterStatus()
	if len(status.Nodes) != 1 || status.Nodes[0].ID != "cn1" {
		t.Fatalf("expected exactly ClusterNode cn1, got %+v", status.Nodes)
	}
	if len(status.Edges) != 1 {
		t.Fatalf("expected 1 HasRole edge, got %d", len(status.Edges))
	}
}

func TestCrossGraphDeterminism_TwoIndependentGraphsSameOps(t *testing.T) {
	ops := func(g *Graph) {
		_, _ = g.UpsertNode("a", Node{ID: "n1", Kind: "K", Props: map[string]string{"x": "1"}})
		_, _ = g.UpsertNode("a", Node{ID: "n2", Kind: "K"})
		_, _ = g.UpsertEdge("a", Edge{ID: "e1", From: "n1", To: "n2", Kind: "R"})
	}
	g1, g2 := NewGraph(), NewGraph()
	ops(g1)
	ops(g2)
	if g1.RootHash() != g2.RootHash() {
		t.Fatal("two independently built graphs with identical ops must have identical root hash")
	}
}
