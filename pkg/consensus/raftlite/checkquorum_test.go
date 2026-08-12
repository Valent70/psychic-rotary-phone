package raftlite

import (
	"context"
	"testing"
	"time"
)

func newCheckQuorumCluster(t *testing.T, timeout time.Duration) (*MemTransport, map[string]*Node, context.CancelFunc) {
	t.Helper()
	return newCheckQuorumClusterWithTimeouts(t, timeout, 40*time.Millisecond, 80*time.Millisecond, 10*time.Millisecond)
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
				return n
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
