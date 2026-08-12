package distributed

import "testing"

func TestJoinAndPlace(t *testing.T) {
	r := NewRegistry()
	r.Join("node1")
	r.Join("node2")
	node, err := r.Place("engineA")
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	if node != "node1" {
		t.Fatalf("expected least-loaded node1 first, got %s", node)
	}
	got, err := r.NodeOf("engineA")
	if err != nil || got != node {
		t.Fatalf("NodeOf mismatch: got=%s err=%v", got, err)
	}
}

func TestPlaceBalancesLoad(t *testing.T) {
	r := NewRegistry()
	r.Join("node1")
	r.Join("node2")
	n1, _ := r.Place("e1") // node1 (empty, lowest ID)
	n2, _ := r.Place("e2") // node2 (node1 now has 1)
	if n1 == n2 {
		t.Fatalf("expected placement to balance across nodes, got both on %s", n1)
	}
}

func TestPlaceNoNodesUp(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Place("e1"); err == nil {
		t.Fatal("expected ErrNoNodesUp")
	}
}

func TestFailoverReassignsEngines(t *testing.T) {
	r := NewRegistry()
	r.Join("node1")
	r.Join("node2")
	r.Place("e1") // -> node1
	r.MarkDown("node1")
	moves, err := r.Failover("node1")
	if err != nil {
		t.Fatalf("failover: %v", err)
	}
	if moves["e1"] != "node2" {
		t.Fatalf("expected e1 moved to node2, got %+v", moves)
	}
	got, _ := r.NodeOf("e1")
	if got != "node2" {
		t.Fatalf("expected NodeOf to reflect failover, got %s", got)
	}
}

func TestFailoverRequiresDownStatus(t *testing.T) {
	r := NewRegistry()
	r.Join("node1")
	r.Join("node2")
	r.Place("e1")
	if _, err := r.Failover("node1"); err == nil {
		t.Fatal("expected failover to require node marked DOWN first")
	}
}

func TestMarkUpRestoresEligibility(t *testing.T) {
	r := NewRegistry()
	r.Join("node1")
	r.MarkDown("node1")
	r.MarkUp("node1")
	node, err := r.Place("e1")
	if err != nil || node != "node1" {
		t.Fatalf("expected node1 eligible again: node=%s err=%v", node, err)
	}
}

func TestVerifyChainDetectsTamper(t *testing.T) {
	r := NewRegistry()
	r.Join("node1")
	r.Place("e1")
	if err := r.VerifyChain(); err != nil {
		t.Fatalf("expected clean chain: %v", err)
	}
	r.mu.Lock()
	r.log[0].Cmd.NodeID = "tampered"
	r.mu.Unlock()
	if err := r.VerifyChain(); err == nil {
		t.Fatal("expected tamper detection")
	}
}

func TestDeterministicReplayAcrossIndependentReplicas(t *testing.T) {
	build := func() *Registry {
		r := NewRegistry()
		r.Join("node1")
		r.Join("node2")
		r.Join("node3")
		r.Place("e1")
		r.Place("e2")
		r.Place("e3")
		r.MarkDown("node1")
		r.Failover("node1")
		return r
	}
	a, b := build(), build()
	if a.LogLen() != b.LogLen() {
		t.Fatalf("log length mismatch: %d vs %d", a.LogLen(), b.LogLen())
	}
	for eng := range a.placement {
		na, _ := a.NodeOf(eng)
		nb, _ := b.NodeOf(eng)
		if na != nb {
			t.Fatalf("placement diverged for %s: %s vs %s", eng, na, nb)
		}
	}
}

func TestEncodeDecodeCommandRoundTrip(t *testing.T) {
	c := Command{Kind: CmdPlace, NodeID: "node1", EngineID: "engineA"}
	decoded, err := DecodeCommand(c.Encode())
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded != c {
		t.Fatalf("round-trip mismatch: %+v vs %+v", decoded, c)
	}
}

func TestApplyCommandRejectsMalformed(t *testing.T) {
	r := NewRegistry()
	if err := r.ApplyCommand(1, []byte("not-a-valid-command")); err == nil {
		t.Fatal("expected ErrBadCommand")
	}
}
