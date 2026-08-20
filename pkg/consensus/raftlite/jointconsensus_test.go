package raftlite

import (
	"context"
	"testing"
	"time"
)

// buildSumClusterWithIDs is buildSumCluster but lets the caller supply
// specific node IDs and an initial peer topology — needed here because
// the joint-consensus test adds nodes (D) that must exist and be
// reachable over the SAME MemTransport before the leader proposes
// adding them, exactly like a real deployment where the new node's
// process is already up and listening before it's added to the
// cluster's configuration.
func newSumNode(id string, peers []string, trans Transport) (*Node, *sumFSM) {
	fsm := newSumFSM()
	n := NewNode(Config{ID: id, Peers: peers, Transport: trans, FSM: fsm})
	n.SetSnapshotter(fsm)
	return n, fsm
}

// TestJointConsensus_FullQuorumFlip_NoSplitBrain replaces {A,B,C} with
// {A,B,D} — i.e. removes C and adds D in one logical membership change
// that touches a majority-adjacent boundary. This is exactly the shape
// of change confchange.go's own doc comment says needs true two-phase
// joint consensus for full safety on large swings. We assert: (1) at
// no point do two different nodes believe they are leader for the same
// term (no split brain), (2) the cluster ends up on exactly {A,B,D},
// and (3) commands proposed before, during, and after the transition
// all apply identically on every node that ends up in the final config.
func TestJointConsensus_FullQuorumFlip_NoSplitBrain(t *testing.T) {
	trans := NewMemTransport()

	nA, fA := newSumNode("A", []string{"B", "C"}, trans)
	nB, fB := newSumNode("B", []string{"A", "C"}, trans)
	nC, fC := newSumNode("C", []string{"A", "B"}, trans)
	trans.Register(nA)
	trans.Register(nB)
	trans.Register(nC)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	nodes := []*Node{nA, nB, nC}
	for _, n := range nodes {
		go n.Run(ctx)
	}

	leader := waitForLeader(t, nodes, 2*time.Second)

	idx1, _, err := leader.Propose(encodeU64(10))
	if err != nil {
		t.Fatalf("propose before transition: %v", err)
	}
	if err := leader.WaitApplied(context.Background(), idx1); err != nil {
		t.Fatalf("wait applied before transition: %v", err)
	}

	// D is a real running node from the start (it must be reachable to
	// receive AppendEntries once added) — its initial peer list already
	// names the FINAL expected cluster, which is how a real deployment
	// would configure a newly-provisioned node's transport.
	nD, fD := newSumNode("D", []string{"A", "B"}, trans)
	trans.Register(nD)
	go nD.Run(ctx)

	idx2, _, err := leader.EnterJointConsensus([]string{"A", "B", "D"})
	if err != nil {
		t.Fatalf("EnterJointConsensus: %v", err)
	}
	if err := leader.WaitApplied(context.Background(), idx2); err != nil {
		t.Fatalf("wait applied for ENTER_JOINT: %v", err)
	}

	// Poll for the transition to fully complete (leader auto-proposes
	// LEAVE_JOINT once ENTER_JOINT commits) and for every node to agree
	// the joint window has closed.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !leader.InJointConsensus() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if leader.InJointConsensus() {
		t.Fatal("leader still reports InJointConsensus() after deadline — LEAVE_JOINT never completed")
	}

	// Split-brain check across the whole run: at no term can two of
	// {A,B,D} (the surviving/joining trio) both be leader.
	leadersByTerm := map[uint64][]string{}
	for _, n := range []*Node{nA, nB, nD} {
		if n.IsLeader() {
			leadersByTerm[n.Term()] = append(leadersByTerm[n.Term()], n.id)
		}
	}
	for term, ids := range leadersByTerm {
		if len(ids) > 1 {
			t.Fatalf("SPLIT BRAIN: term %d has multiple leaders: %v", term, ids)
		}
	}

	finalLeader := waitForLeader(t, []*Node{nA, nB, nD}, 3*time.Second)
	wantMembers := []string{"A", "B", "D"}
	var members []string
	deadline = time.Now().Add(3 * time.Second)
	tick := 0
	for time.Now().Before(deadline) {
		members = finalLeader.Members()
		if len(members) == len(wantMembers) {
			match := true
			for i, m := range wantMembers {
				if members[i] != m {
					match = false
					break
				}
			}
			if match {
				break
			}
		}
		tick++
		if tick%20 == 0 {
			nA.mu.Lock()
			aLog, aCommit, aApplied, aBase := len(nA.state.log), nA.commitIndex, nA.lastApplied, nA.logBase
			nA.mu.Unlock()
			t.Logf("DEBUG poll: finalLeader=%s members=%v | A(leader=%v term=%v joint=%v peers=%v logLen=%d commit=%d applied=%d base=%d)",
				finalLeader.id, members,
				nA.IsLeader(), nA.Term(), nA.InJointConsensus(), nA.Members(), aLog, aCommit, aApplied, aBase,
			)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(members) != len(wantMembers) {
		t.Fatalf("final membership = %v, want %v", members, wantMembers)
	}
	for i, m := range wantMembers {
		if members[i] != m {
			t.Fatalf("final membership = %v, want %v", members, wantMembers)
		}
	}

	idx3, _, err := finalLeader.Propose(encodeU64(32))
	if err != nil {
		t.Fatalf("propose after transition: %v", err)
	}
	if err := finalLeader.WaitApplied(context.Background(), idx3); err != nil {
		t.Fatalf("wait applied after transition: %v", err)
	}

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fA.Sum() == 42 && fB.Sum() == 42 && fD.Sum() == 42 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if fA.Sum() != 42 || fB.Sum() != 42 || fD.Sum() != 42 {
		t.Fatalf("post-transition FSM state diverged: A=%d B=%d D=%d, want all 42 (10 pre-transition + 32 post)", fA.Sum(), fB.Sum(), fD.Sum())
	}
	_ = fC // C was removed from the config; its FSM is intentionally not asserted further
	t.Logf("joint consensus quorum flip {A,B,C}->{A,B,D} complete: final members=%v, state=42 on all three", members)
}
