package raftlite

import (
	"context"
	"testing"
	"time"
)

func TestTransferLeadership_HandsOffToCaughtUpTarget(t *testing.T) {
	_, nodes, cancel := newCheckQuorumCluster(t, 500*time.Millisecond)
	defer cancel()
	leader := waitForLeaderMap(t, nodes, 2*time.Second)

	// Propose a few commands and let them replicate normally first, so
	// the target is realistically caught up the way a production
	// handoff would be, not artificially.
	for i := 0; i < 3; i++ {
		idx, _, err := leader.Propose([]byte("cmd"))
		if err != nil {
			t.Fatalf("Propose: %v", err)
		}
		ctx, c := context.WithTimeout(context.Background(), time.Second)
		_ = leader.WaitApplied(ctx, idx)
		c()
	}
	time.Sleep(100 * time.Millisecond) // let followers replicate

	var targetID string
	for id, n := range nodes {
		if n != leader {
			targetID = id
			break
		}
	}

	ctx, tcancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer tcancel()
	if err := leader.TransferLeadership(ctx, targetID); err != nil {
		t.Fatalf("TransferLeadership: %v", err)
	}
	if leader.IsLeader() {
		t.Fatal("old leader should have stepped down immediately after a successful transfer")
	}

	target := nodes[targetID]
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if target.IsLeader() {
			return // success: transfer target became the new leader
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("transfer target %s never became leader after TransferLeadership succeeded", targetID)
}

func TestTransferLeadership_RejectsUnknownTarget(t *testing.T) {
	_, nodes, cancel := newCheckQuorumCluster(t, 500*time.Millisecond)
	defer cancel()
	leader := waitForLeaderMap(t, nodes, 2*time.Second)

	ctx, c := context.WithTimeout(context.Background(), time.Second)
	defer c()
	err := leader.TransferLeadership(ctx, "not-a-real-peer")
	if err != ErrUnknownTransferTarget {
		t.Fatalf("expected ErrUnknownTransferTarget, got: %v", err)
	}
	if !leader.IsLeader() {
		t.Fatal("a rejected transfer to an unknown target must not affect leadership")
	}
}

func TestTransferLeadership_NonLeaderRejects(t *testing.T) {
	_, nodes, cancel := newCheckQuorumCluster(t, 500*time.Millisecond)
	defer cancel()
	_ = waitForLeaderMap(t, nodes, 2*time.Second)

	// Pick a non-leader by checking IsLeader() FRESH, right before the
	// call below, rather than comparing against the `leader` pointer
	// waitForLeaderMap returned a moment earlier. Real bug this test
	// itself found: under scheduler contention immediately following
	// pkg/chaos's own ~60s run in a full go test ./... invocation, a
	// real, valid leadership change (check-quorum step-down + a fresh
	// election) can occur in the gap between that snapshot and this
	// line -- the node this test previously assumed was still "the
	// follower" had, by the time TransferLeadership actually ran,
	// legitimately become the new leader. TransferLeadership then took
	// its Leader branch and rejected the call for an entirely different
	// reason (targetID == its own ID, which is never present in its own
	// n.peers) -- "raftlite: transfer target is not a current cluster
	// member" instead of the expected ErrNotLeader. Not a defect in
	// TransferLeadership: a stale test snapshot asserting on a role
	// that had since, correctly, changed.
	var follower *Node
	var followerID string
	for id, n := range nodes {
		if !n.IsLeader() {
			follower = n
			followerID = id
			break
		}
	}
	if follower == nil {
		t.Fatal("expected at least one non-leader node in a 3-node cluster with a confirmed leader")
	}
	ctx, c := context.WithTimeout(context.Background(), time.Second)
	defer c()
	err := follower.TransferLeadership(ctx, followerID)
	if err != ErrNotLeader {
		t.Fatalf("expected ErrNotLeader from a non-leader, got: %v", err)
	}
}
