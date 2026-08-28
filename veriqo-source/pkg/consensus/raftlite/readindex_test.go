package raftlite

import (
	"context"
	"testing"
	"time"
)

func TestReadIndex_ConfirmsWithHealthyQuorumAndWaitAppliedSucceeds(t *testing.T) {
	// CheckQuorum itself should NOT fire during this test (same reasoning
	// as TestReadIndex_RejectsRatherThanReturningStaleIndexWhenQuorumUnreachable
	// below) -- this test asserts ReadIndex succeeds on a healthy quorum,
	// not that CheckQuorum fires on a particular schedule. A real CI
	// failure (not reproduced locally) showed the previous 150ms budget
	// was tight enough that a busy shared runner's goroutine scheduling
	// jitter could push the leader's CheckQuorum acks past deadline,
	// spuriously stepping it down between Propose/WaitApplied and
	// ReadIndex and making the leader genuinely, correctly return
	// ErrNotLeader -- a real timing bug in the TEST's own budget, not in
	// ReadIndex or CheckQuorum's logic.
	_, nodes, cancel := newCheckQuorumCluster(t, 5*time.Second)
	defer cancel()
	leader := waitForLeaderMap(t, nodes, 2*time.Second)

	// Propose one real command so there is a non-trivial commit index to
	// confirm a linearizable read against.
	idx, _, err := leader.Propose([]byte("set:x=1"))
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	if err := leader.WaitApplied(waitCtx, idx); err != nil {
		t.Fatalf("WaitApplied(proposed index): %v", err)
	}

	ctx, riCancel := context.WithTimeout(context.Background(), time.Second)
	defer riCancel()
	readIdx, err := leader.ReadIndex(ctx)
	if err != nil {
		t.Fatalf("ReadIndex on a healthy quorum should succeed, got: %v", err)
	}
	if readIdx < idx {
		t.Fatalf("ReadIndex returned %d, expected at least the proposed index %d", readIdx, idx)
	}
	// The stale-read guard contract: the caller must be able to
	// WaitApplied on the returned index.
	if err := leader.WaitApplied(waitCtx, readIdx); err != nil {
		t.Fatalf("WaitApplied(readIdx=%d): %v", readIdx, err)
	}
}

func TestReadIndex_RejectsRatherThanReturningStaleIndexWhenQuorumUnreachable(t *testing.T) {
	trans, nodes, cancel := newCheckQuorumCluster(t, 5*time.Second) // CheckQuorum itself should NOT fire during this short test
	defer cancel()
	leader := waitForLeaderMap(t, nodes, 2*time.Second)

	for id := range nodes {
		if id != leader.ID() {
			trans.DropTo(leader.ID(), id)
		}
	}

	ctx, riCancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer riCancel()
	_, err := leader.ReadIndex(ctx)
	if err == nil {
		t.Fatal("expected ErrLeadershipNotConfirmed when isolated from all peers, got nil (would have returned a possibly-stale index)")
	}
	if err != ErrLeadershipNotConfirmed {
		t.Fatalf("expected ErrLeadershipNotConfirmed, got: %v", err)
	}
}

func TestReadIndex_NonLeaderRejectsImmediately(t *testing.T) {
	// Same CI-timing reasoning as the two tests above: this test does not
	// exercise CheckQuorum's own timing, so give it slack rather than a
	// budget tight enough for scheduling jitter to spuriously step the
	// leader down mid-test.
	_, nodes, cancel := newCheckQuorumCluster(t, 5*time.Second)
	defer cancel()
	leader := waitForLeaderMap(t, nodes, 2*time.Second)

	var follower *Node
	for _, n := range nodes {
		if n != leader {
			follower = n
			break
		}
	}
	ctx, c := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer c()
	_, err := follower.ReadIndex(ctx)
	if err != ErrNotLeader {
		t.Fatalf("expected ErrNotLeader from a follower, got: %v", err)
	}
}
