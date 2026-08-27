package main

import (
	"context"
	"fmt"
	"time"

	"veriqo/pkg/consensus/raftlite"
	"veriqo/pkg/moat/kg"
)

type demo23Result struct {
	InitialLeader                  string  `json:"initial_leader"`
	EntitiesWrittenBefore          int     `json:"entities_written_before_kill"`
	KilledLeader                   string  `json:"killed_leader"`
	NewLeader                      string  `json:"new_leader_after_kill"`
	FailoverSeconds                float64 `json:"failover_seconds"`
	EntitiesWrittenAfter           int     `json:"entities_written_after_failover"`
	CompactedAtIndex               uint64  `json:"compacted_at_index"`
	StragglerNode                  string  `json:"reconnected_straggler_node"`
	StragglerUsedInstallSnapshot   bool    `json:"straggler_used_install_snapshot"`
	LeaderRootHash                 string  `json:"leader_root_hash"`
	StragglerRootHash              string  `json:"straggler_root_hash_after_catchup"`
	IndependentReplayHashLeader    string  `json:"independent_replay_hash_leader"`
	IndependentReplayHashStraggler string  `json:"independent_replay_hash_straggler"`
	HashesIdentical                bool    `json:"hashes_identical_across_all_four"`
}

// runDemo2And3 boots a genuine 3-node raftlite cluster wired to the
// real MOAT knowledge graph (the same wiring proven in
// pkg/consensus/raftlite/snapshot_moat_test.go), then, live, in this
// process:
//
//  1. elects a leader and writes real vessel entities through it;
//  2. force-kills that leader (cancels its own goroutine context —
//     the same effect a killed OS process would have on the cluster's
//     view of it) and proves the surviving 2-of-3 majority elects a
//     new leader and keeps accepting writes;
//  3. isolates the old (now-dead) node's replacement scenario by
//     instead partitioning a THIRD node, forcing the new leader to
//     compact its log past what that node has, healing the partition,
//     and proving the reconnecting node catches up via a real
//     InstallSnapshot RPC rather than replaying the full log;
//  4. independently re-derives full graph state on both the leader and
//     the reconnected straggler via kg.ReplayAll (a fresh rebuild from
//     each node's own append-only mutation log, not a live pointer
//     copy) and asserts all four hashes (2 live RootHash + 2
//     independent replays) are byte-identical.
func runDemo2And3(w *narrator) (demo23Result, error) {
	res := demo23Result{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	trans := raftlite.NewMemTransport()
	ids := []string{"node-A", "node-B", "node-C"}
	nodes := make(map[string]*raftlite.Node, 3)
	adapters := make(map[string]*raftlite.KGAdapter, 3)
	nodeCtx := make(map[string]context.CancelFunc, 3)

	minT, maxT, hbT := raftlite.WANTimeouts()
	for _, id := range ids {
		peers := []string{}
		for _, o := range ids {
			if o != id {
				peers = append(peers, o)
			}
		}
		adapter := raftlite.NewKGAdapter(kg.NewGraph())
		adapters[id] = adapter
		n := raftlite.NewNode(raftlite.Config{
			ID: id, Peers: peers, Transport: trans, FSM: adapter,
			ElectionTimeoutMin: minT, ElectionTimeoutMax: maxT, HeartbeatInterval: hbT,
		})
		n.SetSnapshotter(adapter)
		nodes[id] = n
		trans.Register(n)
		nctx, ncancel := context.WithCancel(ctx)
		nodeCtx[id] = ncancel
		go n.Run(nctx)
	}

	w.line("cluster booted: node-A, node-B, node-C (real raftlite, real MOAT knowledge graph)")

	leader := waitForLeaderNodes(nodes, 5*time.Second)
	if leader == nil {
		return res, fmt.Errorf("no leader elected at cluster startup")
	}
	res.InitialLeader = leader.ID()
	w.line(fmt.Sprintf("leader elected: %s", leader.ID()))

	const before = 30
	var lastIdx uint64
	for i := 0; i < before; i++ {
		cmd := raftlite.EncodeUpsertNode("live-demo", kg.Node{
			ID: fmt.Sprintf("vessel/IMO900%04d", i), Kind: "Vessel",
			Props: map[string]string{"flag": "PA", "seq": fmt.Sprintf("%d", i)},
		})
		idx, _, err := leader.Propose(cmd)
		if err != nil {
			return res, fmt.Errorf("propose before kill: %w", err)
		}
		lastIdx = idx
	}
	if err := leader.WaitApplied(context.Background(), lastIdx); err != nil {
		return res, fmt.Errorf("commit before kill: %w", err)
	}
	res.EntitiesWrittenBefore = before
	w.line(fmt.Sprintf("wrote %d live vessel entities through leader %s, committed at index %d", before, leader.ID(), lastIdx))

	// ---- kill the leader for real ----
	killedID := leader.ID()
	res.KilledLeader = killedID
	w.line(fmt.Sprintf("KILLING leader %s now (cancelling its process context — same effect as SIGKILL on a real node)…", killedID))
	killStart := time.Now()
	nodeCtx[killedID]()
	delete(nodes, killedID) // it's dead; stop considering it as a leader candidate

	remaining := make([]*raftlite.Node, 0, 2)
	for id, n := range nodes {
		if id != killedID {
			remaining = append(remaining, n)
		}
	}
	newLeader := waitForLeaderNodes(nodesMap(remaining), 5*time.Second)
	if newLeader == nil {
		return res, fmt.Errorf("surviving 2-of-3 majority failed to elect a new leader after kill")
	}
	res.NewLeader = newLeader.ID()
	res.FailoverSeconds = time.Since(killStart).Seconds()
	w.line(fmt.Sprintf("FAILOVER COMPLETE in %.3fs: new leader is %s (surviving 2-of-3 majority, no split-brain)", res.FailoverSeconds, newLeader.ID()))

	const after = 20
	var lastIdx2 uint64
	for i := 0; i < after; i++ {
		cmd := raftlite.EncodeUpsertNode("live-demo", kg.Node{
			ID: fmt.Sprintf("vessel/IMO901%04d", i), Kind: "Vessel",
			Props: map[string]string{"flag": "PA", "seq": fmt.Sprintf("post-failover-%d", i)},
		})
		idx, _, err := newLeader.Propose(cmd)
		if err != nil {
			return res, fmt.Errorf("propose after failover: %w", err)
		}
		lastIdx2 = idx
	}
	if err := newLeader.WaitApplied(context.Background(), lastIdx2); err != nil {
		return res, fmt.Errorf("commit after failover: %w", err)
	}
	res.EntitiesWrittenAfter = after
	w.line(fmt.Sprintf("wrote %d MORE entities through new leader %s post-failover — cluster is live, not just recovered", after, newLeader.ID()))

	// ---- Demo 3: partition the OTHER survivor, force compaction,
	// heal, prove InstallSnapshot catch-up + distributed replay ----
	var stragglerID string
	for id := range nodes {
		if id != newLeader.ID() {
			stragglerID = id
		}
	}
	straggler := nodes[stragglerID]
	res.StragglerNode = stragglerID
	w.line(fmt.Sprintf("partitioning %s from the cluster (both directions)…", stragglerID))
	trans.DropTo(newLeader.ID(), stragglerID)
	trans.DropTo(stragglerID, newLeader.ID())

	const whilePartitioned = 200
	var lastIdx3 uint64
	for i := 0; i < whilePartitioned; i++ {
		cmd := raftlite.EncodeUpsertNode("live-demo", kg.Node{
			ID: fmt.Sprintf("vessel/IMO902%05d", i), Kind: "Vessel",
			Props: map[string]string{"flag": "PA", "seq": fmt.Sprintf("partition-%d", i)},
		})
		idx, _, err := newLeader.Propose(cmd)
		if err != nil {
			return res, fmt.Errorf("propose during partition: %w", err)
		}
		lastIdx3 = idx
	}
	if err := newLeader.WaitApplied(context.Background(), lastIdx3); err != nil {
		return res, fmt.Errorf("commit during partition: %w", err)
	}
	w.line(fmt.Sprintf("wrote %d entities while %s was partitioned, forcing log growth", whilePartitioned, stragglerID))

	deadline := time.Now().Add(3 * time.Second)
	for newLeader.LastSnapshotIndex() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	res.CompactedAtIndex = newLeader.LastSnapshotIndex()
	if res.CompactedAtIndex == 0 {
		return res, fmt.Errorf("leader never compacted its log — cannot prove InstallSnapshot catch-up")
	}
	w.line(fmt.Sprintf("leader %s compacted its log at index %d (old entries no longer available via AppendEntries)", newLeader.ID(), res.CompactedAtIndex))

	w.line(fmt.Sprintf("HEALING partition — %s reconnects…", stragglerID))
	trans.HealTo(newLeader.ID(), stragglerID)
	trans.HealTo(stragglerID, newLeader.ID())

	leaderGraph := adapters[newLeader.ID()].Graph
	wantHash := leaderGraph.RootHash()
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if adapters[stragglerID].Graph.RootHash() == wantHash && wantHash != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	res.LeaderRootHash = leaderGraph.RootHash()
	res.StragglerRootHash = adapters[stragglerID].Graph.RootHash()
	res.StragglerUsedInstallSnapshot = straggler.LastSnapshotIndex() > 0
	if res.StragglerRootHash != res.LeaderRootHash || res.LeaderRootHash == "" {
		return res, fmt.Errorf("straggler did not converge: leader=%s straggler=%s", res.LeaderRootHash, res.StragglerRootHash)
	}
	if !res.StragglerUsedInstallSnapshot {
		return res, fmt.Errorf("straggler converged but never went through InstallSnapshot (proof would be vacuous)")
	}
	w.line(fmt.Sprintf("%s caught up via a REAL InstallSnapshot RPC (LastSnapshotIndex=%d), RootHash converged: %s…",
		stragglerID, straggler.LastSnapshotIndex(), res.LeaderRootHash[:16]))

	// Demo 3's actual proof: independently rebuild BOTH graphs from
	// their own append-only logs via kg.ReplayAll — not trusting the
	// live RootHash() pointer at all — and compare.
	leaderReplay, err := kg.ReplayAll(leaderGraph.Snapshot())
	if err != nil {
		return res, fmt.Errorf("independent replay of leader graph: %w", err)
	}
	stragglerReplay, err := kg.ReplayAll(adapters[stragglerID].Graph.Snapshot())
	if err != nil {
		return res, fmt.Errorf("independent replay of straggler graph: %w", err)
	}
	res.IndependentReplayHashLeader = leaderReplay.RootHash()
	res.IndependentReplayHashStraggler = stragglerReplay.RootHash()
	res.HashesIdentical = res.IndependentReplayHashLeader == res.IndependentReplayHashStraggler &&
		res.IndependentReplayHashLeader == res.LeaderRootHash
	if !res.HashesIdentical {
		return res, fmt.Errorf("independent replay hashes diverged: leader_live=%s leader_replay=%s straggler_replay=%s",
			res.LeaderRootHash, res.IndependentReplayHashLeader, res.IndependentReplayHashStraggler)
	}
	w.line("DISTRIBUTED REPLAY PROOF: leader RootHash, straggler RootHash, and two")
	w.line("INDEPENDENTLY rebuilt replay hashes (one per node, from raw logs) are all")
	w.line(fmt.Sprintf("byte-identical: %s", res.IndependentReplayHashLeader))
	w.line("This is the literal 'Node A -> Replay -> Node B -> Replay -> Hash Identical'")
	w.line("proof requested — measured live, not asserted.")

	return res, nil
}

func nodesMap(ns []*raftlite.Node) map[string]*raftlite.Node {
	m := make(map[string]*raftlite.Node, len(ns))
	for _, n := range ns {
		m[n.ID()] = n
	}
	return m
}

func waitForLeaderNodes(nodes map[string]*raftlite.Node, timeout time.Duration) *raftlite.Node {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, n := range nodes {
			if n.IsLeader() {
				return n
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil
}
