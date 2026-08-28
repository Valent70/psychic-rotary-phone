package raftlite

import (
	"context"
	"fmt"
	"testing"
	"time"

	"veriqo/pkg/moat/kg"
)

// TestInstallSnapshot_RealMOATKnowledgeGraph proves the P0 ask from
// Urutan_yang_tepat.docx: snapshot compaction is wired to Veriqo's actual
// MOAT state (the hash-chained knowledge graph), not merely a Raft-layer
// test type. A straggler that catches up via InstallSnapshot must end up
// with a kg.Graph whose RootHash() is byte-identical to the leader's —
// proving the snapshot path and the deterministic-replay path are, in
// effect, the same operation.
func TestInstallSnapshot_RealMOATKnowledgeGraph(t *testing.T) {
	trans := NewMemTransport()
	ids := []string{"A", "B", "C"}
	nodes := make([]*Node, 3)
	adapters := make([]*KGAdapter, 3)
	for i, id := range ids {
		peers := []string{}
		for _, o := range ids {
			if o != id {
				peers = append(peers, o)
			}
		}
		adapter := NewKGAdapter(kg.NewGraph())
		adapters[i] = adapter
		n := NewNode(Config{ID: id, Peers: peers, Transport: trans, FSM: adapter})
		n.SetSnapshotter(adapter)
		nodes[i] = n
		trans.Register(n)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, n := range nodes {
		go n.Run(ctx)
	}

	leader := waitForLeader(t, nodes, 2*time.Second)
	var straggler *Node
	var stragglerAdapter *KGAdapter
	for i, n := range nodes {
		if n != leader {
			straggler = n
			stragglerAdapter = adapters[i]
			break
		}
	}

	// Isolate the straggler, then commit enough real MOAT graph
	// mutations (vessel-like entities, deliberately using the domain
	// shape Veriqo actually writes) to force compaction past what the
	// straggler has.
	for _, n := range nodes {
		if n == straggler {
			continue
		}
		trans.DropTo(n.id, straggler.id)
		trans.DropTo(straggler.id, n.id)
	}

	const total = 150
	var lastIdx uint64
	for i := 0; i < total; i++ {
		cmd := EncodeUpsertNode("investor-demo", kg.Node{
			ID: fmt.Sprintf("vessel/IMO%07d", 9000000+i), Kind: "Vessel",
			Props: map[string]string{"flag": "PA", "evidence_seq": fmt.Sprintf("%d", i)},
		})
		idx, _, err := leader.Propose(cmd)
		if err != nil {
			t.Fatalf("propose %d: %v", i, err)
		}
		lastIdx = idx
	}
	if err := leader.WaitApplied(context.Background(), lastIdx); err != nil {
		t.Fatalf("leader never applied: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for leader.LastSnapshotIndex() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if leader.LastSnapshotIndex() == 0 {
		t.Fatal("leader never compacted the KG-backed log")
	}

	for _, n := range nodes {
		if n == straggler {
			continue
		}
		trans.HealTo(n.id, straggler.id)
		trans.HealTo(straggler.id, n.id)
	}

	var leaderAdapter *KGAdapter
	for i, n := range nodes {
		if n == leader {
			leaderAdapter = adapters[i]
		}
	}
	wantHash := leaderAdapter.graph().RootHash()
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if stragglerAdapter.graph().RootHash() == wantHash && wantHash != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	got := stragglerAdapter.graph().RootHash()
	if got != wantHash || wantHash == "" {
		t.Fatalf("MOAT knowledge graph did not converge: straggler RootHash=%q leader RootHash=%q", got, wantHash)
	}
	if straggler.LastSnapshotIndex() == 0 {
		t.Fatal("straggler converged but never went through InstallSnapshot")
	}

	// The final, strongest proof: independently re-verify the
	// straggler's ENTIRE mutation log via kg.ReplayAll (the same
	// tamper-detection path an external auditor / IVF verifier would
	// use) — not just trust the live RootHash().
	rebuilt, err := kg.ReplayAll(stragglerAdapter.graph().Snapshot())
	if err != nil {
		t.Fatalf("independent replay verification of straggler's post-snapshot graph failed: %v", err)
	}
	if rebuilt.RootHash() != wantHash {
		t.Fatalf("independently-replayed straggler graph hash %q != leader %q", rebuilt.RootHash(), wantHash)
	}
	t.Logf("MOAT knowledge graph snapshot proof: %d vessel entities, RootHash=%s identical on leader+straggler+independent replay, LastSnapshotIndex=%d", total, wantHash, straggler.LastSnapshotIndex())
}
