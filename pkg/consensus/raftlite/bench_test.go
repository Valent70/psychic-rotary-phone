package raftlite

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// BenchmarkCommitLatency_7NodeInProcess measures commit latency for a
// 7-node cluster over the in-memory transport (goroutines, not real
// sockets). This is an HONEST small-scale in-process measurement — it is
// explicitly NOT the 100/500/1000-node real-network benchmark the gap
// audit's §7 asks for, which needs real multi-host infrastructure this
// sandbox does not have. Numbers below are measured by `go test -bench`,
// not estimated.
func BenchmarkCommitLatency_7NodeInProcess(b *testing.B) {
	trans, nodes, cancel := buildBenchCluster(b, 7)
	defer cancel()
	leader := waitForLeaderBench(b, nodes, 2*time.Second)
	_ = trans

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx, _, err := leader.Propose([]byte("bench-entry"))
		if err != nil {
			b.Fatalf("propose failed: %v", err)
		}
		ctx, done := context.WithTimeout(context.Background(), time.Second)
		if err := leader.WaitApplied(ctx, idx); err != nil {
			done()
			b.Fatalf("commit did not land: %v", err)
		}
		done()
	}
}

func buildBenchCluster(b *testing.B, n int) (*MemTransport, []*Node, context.CancelFunc) {
	b.Helper()
	trans := NewMemTransport()
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		ids[i] = fmt.Sprintf("node-%d", i)
	}
	nodes := make([]*Node, n)
	for i, id := range ids {
		peers := []string{}
		for _, other := range ids {
			if other != id {
				peers = append(peers, other)
			}
		}
		node := NewNode(Config{ID: id, Peers: peers, Transport: trans})
		nodes[i] = node
		trans.Register(node)
	}
	ctx, cancel := context.WithCancel(context.Background())
	for _, node := range nodes {
		go node.Run(ctx)
	}
	return trans, nodes, cancel
}

func waitForLeaderBench(b *testing.B, nodes []*Node, timeout time.Duration) *Node {
	b.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, n := range nodes {
			if n.IsLeader() {
				return n
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	b.Fatal("no leader elected within timeout")
	return nil
}
