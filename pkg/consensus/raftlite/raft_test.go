package raftlite

import (
	"context"
	"testing"
	"time"

	"veriqo/pkg/moat/kg"
)

func buildCluster(t *testing.T, n int, g *kg.Graph) (*MemTransport, []*Node, context.CancelFunc) {
	t.Helper()
	trans := NewMemTransport()
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		ids[i] = string(rune('A' + i))
	}
	var adapter *KGAdapter
	if g != nil {
		adapter = NewKGAdapter(g)
	}
	nodes := make([]*Node, n)
	for i, id := range ids {
		peers := []string{}
		for _, other := range ids {
			if other != id {
				peers = append(peers, other)
			}
		}
		cfg := Config{ID: id, Peers: peers, Transport: trans}
		if adapter != nil {
			cfg.FSM = adapter
			cfg.Sink = adapter
		}
		node := NewNode(cfg)
		nodes[i] = node
		trans.Register(node)
	}
	ctx, cancel := context.WithCancel(context.Background())
	for _, node := range nodes {
		go node.Run(ctx)
	}
	return trans, nodes, cancel
}

func waitForLeader(t *testing.T, nodes []*Node, timeout time.Duration) *Node {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, n := range nodes {
			if n.IsLeader() {
				return n
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no leader elected within timeout")
	return nil
}

// waitForCommitIndexToStabilize polls n.CommitIndex() until it stops
// changing for a short quiet window, or until timeout, and returns the
// settled value. becomeLeader appends a no-op entry immediately on
// election (see raft.go's becomeLeader doc comment) which then still
// needs one real network round-trip to actually commit -- waitForLeader
// only proves IsLeader() is true, not that this initial commit has
// already landed. A test that captures a "before" CommitIndex()
// snapshot right after waitForLeader can otherwise observe it advance
// later purely because of that in-flight no-op, for reasons having
// nothing to do with whatever the test itself proposes next. Found via
// a real, reproduced CI failure in
// TestProposeJointConfChange_RejectsInvalidBatchBeforeProposing.
func waitForCommitIndexToStabilize(t *testing.T, n *Node, timeout time.Duration) uint64 {
	t.Helper()
	deadline := time.Now().Add(timeout)
	last := n.CommitIndex()
	stableSince := time.Now()
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		cur := n.CommitIndex()
		if cur != last {
			last = cur
			stableSince = time.Now()
			continue
		}
		if time.Since(stableSince) >= 50*time.Millisecond {
			return last
		}
	}
	return last
}

func TestElection_SingleLeaderElected(t *testing.T) {
	_, nodes, cancel := buildCluster(t, 3, nil)
	defer cancel()
	leader := waitForLeader(t, nodes, 2*time.Second)
	if leader == nil {
		t.Fatal("expected a leader")
	}
	leaderCount := 0
	for _, n := range nodes {
		if n.IsLeader() {
			leaderCount++
		}
	}
	if leaderCount != 1 {
		t.Fatalf("expected exactly 1 leader, got %d", leaderCount)
	}
}

func TestReplication_CommitsAcrossMajority(t *testing.T) {
	_, nodes, cancel := buildCluster(t, 3, nil)
	defer cancel()
	leader := waitForLeader(t, nodes, 2*time.Second)

	idx, _, err := leader.Propose([]byte("hello"))
	if err != nil {
		t.Fatalf("propose failed: %v", err)
	}
	// 3s, not 1s: matches the budget confchange_test.go, snapshot_test.go
	// and leadertransfer_stress_test.go already use for the same reason --
	// a 1s WaitApplied budget on a cluster running default 150/300/50ms
	// timeouts is comfortable in isolation but has been observed to miss
	// under go test ./...'s real scheduler contention immediately
	// following pkg/chaos's ~60s run (see TestKGAdapter_EndToEndCommit
	// below, which hit exactly this on real CI).
	ctx, done := context.WithTimeout(context.Background(), 3*time.Second)
	defer done()
	if err := leader.WaitApplied(ctx, idx); err != nil {
		t.Fatalf("entry not applied: %v", err)
	}
	if leader.CommitIndex() < idx {
		t.Fatalf("commit index %d did not reach proposed index %d", leader.CommitIndex(), idx)
	}
}

func TestKGAdapter_EndToEndCommit(t *testing.T) {
	g := kg.NewGraph()
	_, nodes, cancel := buildCluster(t, 3, g)
	defer cancel()

	cmd := EncodeUpsertNode("test-actor", kg.Node{ID: "n1", Kind: "Vessel", Props: map[string]string{"name": "MV Test"}})

	// Retries a bounded number of times on ANY Propose/WaitAppliedTerm
	// error (ErrNotLeader, ErrEntryOverwritten, or a WaitAppliedTerm
	// timeout) -- the same behavior a correct raft client uses, and
	// named explicitly by Propose's own doc comment ("no forwarding --
	// caller must retry against the actual leader"). Needed for real
	// reasons, not to paper over a bug: under real CPU contention
	// immediately following pkg/chaos's ~60s run in the same
	// `go test ./...` invocation, real CI observed this cluster's
	// leader occasionally lose leadership before replicating its own
	// just-accepted proposal to a majority -- a genuine, expected raft
	// outcome (Propose succeeding never guaranteed eventual commit),
	// not a bug. Retrying is what actually closes it, exactly like the
	// LEAVE_JOINT best-effort re-propose already does elsewhere in this
	// package. An earlier version of this loop retried only on
	// ErrEntryOverwritten and fell through to t.Fatalf on a plain
	// WaitAppliedTerm timeout -- found via a real -race count=500 run
	// that still failed once with "context deadline exceeded" despite
	// 4 unused retries remaining; a real client facing a timeout would
	// retry too, so this loop now does.
	var applied bool
	var lastErr error
	for attempt := 0; attempt < 5 && !applied; attempt++ {
		leader := waitForLeader(t, nodes, 2*time.Second)
		idx, term, err := leader.Propose(cmd)
		if err != nil {
			lastErr = err
			continue // ErrNotLeader: leadership moved between waitForLeader and Propose; retry
		}
		// 3s, not 1s -- this exact 1s budget was observed to fail on
		// real CI ("not applied: context deadline exceeded") immediately
		// after pkg/chaos's ~60s run in the same `go test ./...`
		// invocation: the cluster here runs default 150/300/50ms
		// election/heartbeat timeouts (buildCluster passes no explicit
		// Config timeouts), which is fine under normal scheduling but
		// leaves near-zero slack under real CPU contention from
		// concurrently-running packages. Widened to match the budget
		// confchange_test.go, snapshot_test.go and
		// leadertransfer_stress_test.go already use for the same reason.
		ctx, done := context.WithTimeout(context.Background(), 3*time.Second)
		// WaitAppliedTerm, not WaitApplied: plain index-based WaitApplied
		// was separately observed on real CI to return nil for an entry
		// that was never actually applied, because the leader that
		// accepted Propose lost leadership before replicating it to a
		// majority and a later leader's unrelated entry committed at the
		// same numeric index first (see WaitAppliedTerm's doc comment).
		// Checking the term Propose() returned turns that silent
		// false-success into ErrEntryOverwritten, retried below same as
		// every other error this loop can see.
		err = leader.WaitAppliedTerm(ctx, idx, term)
		done()
		if err != nil {
			lastErr = err
			continue
		}
		applied = true
	}
	if !applied {
		t.Fatalf("propose+apply did not succeed within 5 retries, last error: %v", lastErr)
	}
	if _, ok := g.GetNode("n1"); !ok {
		t.Fatal("expected node n1 to be present in KG after raft commit")
	}
	if g.RootHash() == "" {
		t.Fatal("expected non-empty KG root hash after commit")
	}
	status := g.GetClusterStatus()
	if len(status.Nodes) == 0 {
		t.Fatal("expected OnLeaderChange to have populated ClusterNode entries")
	}
}

// TestChaosLite_LeaderPartitionTriggersFailover simulates a network
// partition (not Jepsen-class, but a real adversarial scenario over the
// MemTransport): the current leader is cut off from all peers, and the
// cluster is proven to elect a new leader and keep committing.
func TestChaosLite_LeaderPartitionTriggersFailover(t *testing.T) {
	trans, nodes, cancel := buildCluster(t, 5, nil)
	defer cancel()
	leader := waitForLeader(t, nodes, 2*time.Second)
	oldLeaderID := leader.ID()

	// partition the old leader from everyone, both directions
	for _, n := range nodes {
		if n.ID() == oldLeaderID {
			continue
		}
		trans.DropTo(oldLeaderID, n.ID())
		trans.DropTo(n.ID(), oldLeaderID)
	}

	deadline := time.Now().Add(3 * time.Second)
	var newLeader *Node
	for time.Now().Before(deadline) {
		for _, n := range nodes {
			if n.IsLeader() && n.ID() != oldLeaderID {
				newLeader = n
				break
			}
		}
		if newLeader != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if newLeader == nil {
		t.Fatal("expected a new leader to be elected after partitioning the old leader")
	}

	idx, _, err := newLeader.Propose([]byte("post-partition-entry"))
	if err != nil {
		t.Fatalf("propose on new leader failed: %v", err)
	}
	ctx, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	if err := newLeader.WaitApplied(ctx, idx); err != nil {
		t.Fatalf("post-partition entry not committed: %v", err)
	}

	trans.HealAll()
}

// TestChaosLite_MinorityPartitionNoSplitBrain proves that a leader cut off
// from the majority cannot keep committing (no split-brain): it should
// fail to advance its commit index once it can't reach a majority.
func TestChaosLite_MinorityPartitionNoSplitBrain(t *testing.T) {
	trans, nodes, cancel := buildCluster(t, 5, nil)
	defer cancel()
	leader := waitForLeader(t, nodes, 2*time.Second)
	leaderID := leader.ID()

	// partition leader from 3 of its 4 peers (only 2/5 reachable = minority)
	cut := 0
	for _, n := range nodes {
		if n.ID() == leaderID {
			continue
		}
		if cut >= 3 {
			break
		}
		trans.DropTo(leaderID, n.ID())
		trans.DropTo(n.ID(), leaderID)
		cut++
	}

	_, _, err := leader.Propose([]byte("should-not-commit-alone"))
	if err != nil {
		// leader may have already stepped down; acceptable outcome
		trans.HealAll()
		return
	}
	time.Sleep(500 * time.Millisecond)
	// old leader must NOT have committed this alone against a minority
	if leader.CommitIndex() > 0 && leader.IsLeader() {
		// it's fine if it committed via the 2 reachable replicas IF that's
		// still a majority of 5 (3/5) — with 3 cut it only has 2/5, so this
		// must never happen.
		t.Fatalf("leader with only minority connectivity must not report a leader role holding commit progress in isolation")
	}
	trans.HealAll()
}
