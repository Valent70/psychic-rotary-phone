package rafttcp

import (
	"context"
	"testing"
	"time"

	"veriqo/pkg/consensus/raftlite"
)

type nullFSM struct{}

func (nullFSM) Apply(raftlite.LogEntry) error { return nil }

func setupNode(t *testing.T, ca *CA, id string, members []string) (*raftlite.Node, *Server, string) {
	t.Helper()
	node := raftlite.NewNode(raftlite.Config{ID: id, FSM: nullFSM{}})
	leaf, err := ca.IssueLeaf(id)
	if err != nil {
		t.Fatalf("issue leaf: %v", err)
	}
	verifier := NewPeerVerifier(ca, members)
	srv := NewServer(id, node, ca, leaf, verifier)
	addr, err := srv.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return node, srv, addr
}

func TestRealBroadcast_ThreeNodeDelivery(t *testing.T) {
	ca, err := NewCA()
	if err != nil {
		t.Fatalf("ca: %v", err)
	}
	members := []string{"A", "B", "C"}
	_, srvA, addrA := setupNode(t, ca, "A", members)
	_, srvB, addrB := setupNode(t, ca, "B", members)
	_, srvC, addrC := setupNode(t, ca, "C", members)
	defer srvA.Close()
	defer srvB.Close()
	defer srvC.Close()

	addrs := map[string]string{"A": addrA, "B": addrB, "C": addrC}
	leafA, _ := ca.IssueLeaf("A")
	client := NewClient("A", ca, leafA, addrs)
	defer client.CloseAll()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	for _, target := range []string{"B", "C"} {
		reply, err := client.SendRequestVote(ctx, target, raftlite.RequestVoteArgs{
			Term: 1, CandidateID: "A", LastLogIndex: 0, LastLogTerm: 0,
		})
		if err != nil {
			t.Fatalf("real socket RPC to %s failed: %v", target, err)
		}
		if !reply.VoteGranted {
			t.Fatalf("expected vote granted from %s, got %+v", target, reply)
		}
	}
}

func TestRogueCA_Rejected(t *testing.T) {
	legitCA, err := NewCA()
	if err != nil {
		t.Fatalf("ca: %v", err)
	}
	rogueCA, err := NewCA()
	if err != nil {
		t.Fatalf("rogue ca: %v", err)
	}
	members := []string{"A", "B"}
	_, srvB, addrB := setupNode(t, legitCA, "B", members)
	defer srvB.Close()

	// attacker holds a valid-looking cert signed by a DIFFERENT CA
	rogueLeaf, err := rogueCA.IssueLeaf("A")
	if err != nil {
		t.Fatalf("rogue leaf: %v", err)
	}
	client := NewClient("A", rogueCA, rogueLeaf, map[string]string{"B": addrB})
	defer client.CloseAll()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = client.SendRequestVote(ctx, "B", raftlite.RequestVoteArgs{Term: 1, CandidateID: "A"})
	if err == nil {
		t.Fatal("expected rogue-CA client to be rejected by TLS handshake, got success")
	}
}

func TestValidCANotInMembership_Rejected(t *testing.T) {
	ca, err := NewCA()
	if err != nil {
		t.Fatalf("ca: %v", err)
	}
	// server only trusts membership {"B","C"} — "A" is a valid cert from
	// the SAME CA but not a cluster member.
	_, srvB, addrB := setupNode(t, ca, "B", []string{"B", "C"})
	defer srvB.Close()

	leafA, err := ca.IssueLeaf("A")
	if err != nil {
		t.Fatalf("leaf A: %v", err)
	}
	client := NewClient("A", ca, leafA, map[string]string{"B": addrB})
	defer client.CloseAll()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = client.SendRequestVote(ctx, "B", raftlite.RequestVoteArgs{Term: 1, CandidateID: "A"})
	if err == nil {
		t.Fatal("expected valid-CA-but-non-member client to be rejected (no auth-ack), got success")
	}
}
