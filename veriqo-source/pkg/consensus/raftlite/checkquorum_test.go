package raftlite

import (
	"context"
	"testing"
	"time"
)

func newCheckQuorumCluster(t *testing.T, timeout time.Duration) (*MemTransport, map[string]*Node, context.CancelFunc) {
	t.Helper()
	// Election/heartbeat timing matches leadertransfer_stress_test.go's
	// own proven-under-load values (200ms/400ms/20ms), not this file's
	// original 40ms/80ms/10ms: a real GitHub Actions run of this branch
	// (go test ./... on a shared runner, many packages' goroutines
	// competing for CPU) hit "raftlite: not leader" from
	// TestReadIndex_ConfirmsWithHealthyQuorumAndWaitAppliedSucceeds, and
	// `go test -count=3` reproduced a second, different flake
	// (TestTransferLeadership_HandsOffToCaughtUpTarget) locally under the
	// same root cause: a 40ms election timeout leaves too little slack
	// for a leader that `waitForLeaderMap` just confirmed to survive
	// ordinary scheduling jitter before the test's next RPC. Widening to
	// the values leadertransfer_stress_test.go already relies on for
	// exactly this reason removes the flake without touching any
	// production default (Config's real defaults live in raft.go, not
	// here) or any test whose OWN assertion is about CheckQuorumTimeout's
	// relationship to these values (checkquorum_test.go's two tests below
	// pass their own explicit windows sized against these, and are
	// re-verified after this change).
	return newCheckQuorumClusterWithTimeouts(t, timeout, 200*time.Millisecond, 400*time.Millisecond, 20*time.Millisecond)
}

func newCheckQuorumClusterWithTimeouts(t *testing.T, checkQuorumTimeout, electionMin, electionMax, heartbeat time.Duration) (*MemTransport, map[string]*Node, context.CancelFunc) {
	t.Helper()
	trans := NewMemTransport()
	ids := []string{"A", "B", "C"}
	nodes := map[string]*Node{}
	ctx, cancel := context.WithCancel(context.Background())
	for _, id := range ids {
		var peers []string
		for _, p := range ids {
			if p != id {
				peers = append(peers, p)
			}
		}
		n := NewNode(Config{
			ID: id, Peers: peers, Transport: trans, FSM: newSumFSM(),
			ElectionTimeoutMin: electionMin,
			ElectionTimeoutMax: electionMax,
			HeartbeatInterval:  heartbeat,
			CheckQuorumTimeout: checkQuorumTimeout,
		})
		nodes[id] = n
		trans.Register(n)
		go n.Run(ctx)
	}
	return trans, nodes, cancel
}

func waitForLeaderMap(t *testing.T, nodes map[string]*Node, within time.Duration) *Node {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		for _, n := range nodes {
			if n.IsLeader() {
				// Confirm STABLE leadership before returning. Under heavy
				// concurrent load (this whole package running alongside
				// every other package's tests under a plain `go test
				// ./...`), a node can report IsLeader() true for one
				// instant and step down moments later -- before the
				// caller's very next RPC -- purely from ordinary
				// goroutine-scheduling delay, not a raft correctness bug
				// (already proven separately by the CheckQuorum/election
				// tests). This produced a real "raftlite: not leader"
				// failure both on GitHub Actions CI and locally under
				// `go test ./...`, even after newCheckQuorumCluster's
				// timing was widened for the same reason. Re-checking
				// after a short settle window turns a one-instant
				// snapshot into a much stronger signal, at the cost of at
				// most one extra 15ms wait per test (paid once a
				// candidate is found, not per poll iteration).
				time.Sleep(15 * time.Millisecond)
				if n.IsLeader() {
					return n
				}
				break // this candidate destabilized; re-scan from the top
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no leader elected within timeout")
	return nil
}

// TestCheckQuorum_NoFalsePositiveUnderPartialLoss proves the exact
// concern raised in review: CheckQuorum must NOT step a healthy leader
// down just because ONE peer link is flaky, as long as a majority
// (leader + at least one peer) stays reachable.
func TestCheckQuorum_NoFalsePositiveUnderPartialLoss(t *testing.T) {
	trans, nodes, cancel := newCheckQuorumCluster(t, 150*time.Millisecond)
	defer cancel()

	leader := waitForLeaderMap(t, nodes, 2*time.Second)
	var flaky string
	for id, n := range nodes {
		if n != leader {
			flaky = id
			break
		}
	}
	// Permanently drop ONE link (leader -> flaky peer). The OTHER peer
	// stays fully reachable, so the leader still has a majority
	// (itself + the healthy peer) the entire time.
	trans.DropTo(leader.ID(), flaky)

	time.Sleep(400 * time.Millisecond) // several checkQuorumTimeout windows
	if !leader.IsLeader() {
		t.Fatal("leader stepped down under PARTIAL loss (one peer link down, majority still reachable) — false positive")
	}
}

// TestCheckQuorum_StepsDownUnderFullIsolation proves the other
// direction: a leader cut off from BOTH peers must step down within
// checkQuorumTimeout, not continue believing it is still leader.
func TestCheckQuorum_StepsDownUnderFullIsolation(t *testing.T) {
	trans, nodes, cancel := newCheckQuorumCluster(t, 100*time.Millisecond)
	defer cancel()

	leader := waitForLeaderMap(t, nodes, 2*time.Second)
	for id := range nodes {
		if id != leader.ID() {
			trans.DropTo(leader.ID(), id)
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !leader.IsLeader() {
			return // stepped down as required
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("leader did not step down after being fully isolated from both peers")
}
