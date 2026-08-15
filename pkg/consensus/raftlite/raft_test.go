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
	leader := waitForLeader(t, nodes, 2*time.Second)

	cmd := EncodeUpsertNode("test-actor", kg.Node{ID: "n1", Kind: "Vessel", Props: map[string]string{"name": "MV Test"}})
	idx, _, err := leader.Propose(cmd)
	if err != nil {
		t.Fatalf("propose failed: %v", err)
	}
	// 3s, not 1s -- this exact 1s budget was observed to fail on real CI
	// ("not applied: context deadline exceeded") immediately after
	// pkg/chaos's ~60s run in the same `go test ./...` invocation: the
	// cluster here runs default 150/300/50ms election/heartbeat timeouts
	// (buildCluster passes no explicit Config timeouts), which is fine
	// under normal scheduling but leaves near-zero slack under real CPU
	// contention from concurrently-running packages. Widened to match
	// the budget confchange_test.go, snapshot_test.go and
	// leadertransfer_stress_test.go already use for the same reason.
	ctx, done := context.WithTimeout(context.Background(), 3*time.Second)
	defer done()
	if err := leader.WaitApplied(ctx, idx); err != nil {
		t.Fatalf("not applied: %v", err)
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
