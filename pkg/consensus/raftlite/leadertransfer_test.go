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
	leader := waitForLeaderMap(t, nodes, 2*time.Second)

	var follower *Node
	var followerID string
	for id, n := range nodes {
		if n != leader {
			follower = n
			followerID = id
			break
		}
	}
	ctx, c := context.WithTimeout(context.Background(), time.Second)
	defer c()
	err := follower.TransferLeadership(ctx, followerID)
	if err != ErrNotLeader {
		t.Fatalf("expected ErrNotLeader from a non-leader, got: %v", err)
	}
}
