package main

import (
	"context"
	"fmt"
	"sort"
	"time"

	"veriqo/pkg/consensus/raftlite"
)

type benchmarkResult struct {
	Trials          int       `json:"trials"`
	ClusterSize     int       `json:"cluster_size"`
	SamplesMillis   []float64 `json:"samples_millis"`
	P50Millis       float64   `json:"p50_millis"`
	P95Millis       float64   `json:"p95_millis"`
	P99Millis       float64   `json:"p99_millis"`
	MinMillis       float64   `json:"min_millis"`
	MaxMillis       float64   `json:"max_millis"`
	FailedElections int       `json:"failed_elections"`
}

// runFailoverBenchmark directly answers the critique that no
// leader-failover latency numbers (P50/P95/P99) had ever been
// measured — every prior session's failover claims were pass/fail
// only, never a distribution. Each trial boots a genuinely fresh
// 3-node raftlite cluster (real goroutines, real timers, real
// MemTransport RPCs — not a mock), elects a leader, kills it via
// context cancellation (the same mechanism Demo 2 uses), and times
// wall-clock until a new leader from the surviving majority reports
// IsLeader()==true. Default trial count is bounded for demo runtime
// (a single investor-facing run should not take minutes); the
// -failover-trials flag exists specifically so a fuller run (e.g. the
// 500 trials asked for) can be executed separately and its manifest
// diffed against this one.
func runFailoverBenchmark(w *narrator, trials, clusterSize int) benchmarkResult {
	res := benchmarkResult{Trials: trials, ClusterSize: clusterSize}
	res.SamplesMillis = make([]float64, 0, trials)

	for t := 0; t < trials; t++ {
		d, ok := oneFailoverTrial(clusterSize)
		if !ok {
			res.FailedElections++
			continue
		}
		res.SamplesMillis = append(res.SamplesMillis, float64(d.Microseconds())/1000.0)
	}

	sorted := append([]float64(nil), res.SamplesMillis...)
	sort.Float64s(sorted)
	pct := func(p float64) float64 {
		if len(sorted) == 0 {
			return 0
		}
		idx := int(p * float64(len(sorted)-1))
		return sorted[idx]
	}
	res.P50Millis = pct(0.50)
	res.P95Millis = pct(0.95)
	res.P99Millis = pct(0.99)
	if len(sorted) > 0 {
		res.MinMillis = sorted[0]
		res.MaxMillis = sorted[len(sorted)-1]
	}

	w.line(fmt.Sprintf("failover benchmark: %d trials on %d-node clusters, %d failed to re-elect",
		trials, clusterSize, res.FailedElections))
	w.line(fmt.Sprintf("election latency:  P50=%.1fms  P95=%.1fms  P99=%.1fms  min=%.1fms  max=%.1fms",
		res.P50Millis, res.P95Millis, res.P99Millis, res.MinMillis, res.MaxMillis))
	return res
}

func oneFailoverTrial(n int) (time.Duration, bool) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	trans := raftlite.NewMemTransport()
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("bench-%c", rune('A'+i))
	}
	nodes := make(map[string]*raftlite.Node, n)
	nodeCancel := make(map[string]context.CancelFunc, n)
	for _, id := range ids {
		peers := []string{}
		for _, o := range ids {
			if o != id {
				peers = append(peers, o)
			}
		}
		node := raftlite.NewNode(raftlite.Config{ID: id, Peers: peers, Transport: trans, FSM: &nopFSM{}})
		nodes[id] = node
		trans.Register(node)
	}
	// Start every node's Run() goroutine only AFTER all nodes are
	// constructed and registered with the transport — starting a
	// node's election loop before its peers are reachable was a real
	// bug: node A could reach its election timeout and call
	// RequestVote against B/C before they were registered, and with
	// only 3 nodes a single missed round can cascade into repeated
	// split votes. Matches the pattern every other working cluster
	// helper in this repo already uses (buildCluster in
	// raft_test.go, runDemo2And3 in demo2_3.go) — this file just
	// hadn't followed it. Found via a 100%-reproducible 30/30 election
	// failure in this benchmark, isolated with a minimal repro.
	for _, id := range ids {
		nctx, ncancel := context.WithCancel(ctx)
		nodeCancel[id] = ncancel
		go nodes[id].Run(nctx)
	}

	leader := waitForLeaderNodes(nodes, 3*time.Second)
	if leader == nil {
		return 0, false
	}

	killedID := leader.ID()
	start := time.Now()
	nodeCancel[killedID]()
	delete(nodes, killedID)

	newLeader := waitForLeaderNodes(nodes, 3*time.Second)
	if newLeader == nil {
		return 0, false
	}
	return time.Since(start), true
}

// nopFSM is a minimal FSM used only for benchmark trials, where the
// point is election timing, not log-application content — a real
// FSM (like KGAdapter, used in demo2_3.go) would add unrelated
// encode/decode latency to the very number this benchmark isolates.
type nopFSM struct{}

func (nopFSM) Apply(entry raftlite.LogEntry) error { return nil }
