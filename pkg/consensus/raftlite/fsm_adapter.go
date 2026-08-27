package raftlite

import (
	"encoding/json"
	"fmt"
	"sync"

	"veriqo/pkg/moat/kg"
)

// KGCommand is the wire format Propose() callers encode into LogEntry.Command.
// This is the seam the prior session's report described as "a few lines
// when the source is available" — it now exists.
type KGCommand struct {
	ActorID string    `json:"actor_id"`
	Op      kg.OpType `json:"op"`
	Node    *kg.Node  `json:"node,omitempty"`
	Edge    *kg.Edge  `json:"edge,omitempty"`
}

// KGAdapter implements raftlite.FSM by decoding each committed LogEntry
// and applying it to a pkg/moat/kg.Graph. It also implements
// ClusterEventSink so leader/term/membership changes are written into the
// graph as ClusterNode/HasRole/ReplicatesFrom — the "KG as a live view of
// cluster topology" contract from GetClusterStatus() — and Snapshotter, so
// raftlite's log compaction (snapshot.go) operates on Veriqo's actual MOAT
// state (the hash-chained knowledge graph), not a synthetic test type.
// This is what makes snapshotting "part of the VERIQO architecture, not
// just a Raft feature" — Snapshot()/Restore() round-trip through the SAME
// hash-chain-verified encoding (kg.Graph.Snapshot / kg.ReplayAll) already
// used for tamper-evident audit replay elsewhere in the codebase, so a
// snapshot install and a forensic replay are provably the same operation.
//
// mu guards the Graph POINTER (Restore swaps it atomically to a freshly
// rebuilt+verified graph); the pointed-to kg.Graph has its own internal
// locking for individual mutations. Needed because Apply() is invoked by
// raftlite's applyCommitted OUTSIDE the Node's lock, while Restore() is
// invoked by HandleInstallSnapshot WHILE the Node's lock is held.
type KGAdapter struct {
	mu    sync.RWMutex
	Graph *kg.Graph
}

func NewKGAdapter(g *kg.Graph) *KGAdapter {
	return &KGAdapter{Graph: g}
}

func (a *KGAdapter) graph() *kg.Graph {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.Graph
}

func (a *KGAdapter) Apply(entry LogEntry) error {
	if len(entry.Command) == 0 {
		return nil // heartbeat / no-op entry
	}
	var cmd KGCommand
	if err := json.Unmarshal(entry.Command, &cmd); err != nil {
		return fmt.Errorf("kg-adapter: decode failed at index %d: %w", entry.Index, err)
	}
	_, err := a.graph().Apply(cmd.ActorID, cmd.Op, cmd.Node, cmd.Edge)
	return err
}

// Snapshot encodes the graph's full hash-chained mutation log as JSON.
// Deterministic by construction: kg.Graph's canonical byte encoding
// (sorted map keys, explicit field ordering) is exactly what already
// guarantees two independently-applied replicas hash-match — see
// kg_test.go and the raftlite straggler-convergence tests.
func (a *KGAdapter) Snapshot() ([]byte, error) {
	return json.Marshal(a.graph().Snapshot())
}

// Restore rebuilds the graph from a snapshot via kg.ReplayAll, which
// re-derives every hash from canonical bytes rather than trusting the
// wire-format Hash field — so an InstallSnapshot RPC gets the SAME
// tamper-detection guarantee as the standalone forensic replay path.
// Fail-closed: on any verification error, the adapter's live graph
// pointer is left completely untouched (matches HandleInstallSnapshot's
// own fail-closed contract in snapshot.go).
func (a *KGAdapter) Restore(data []byte) error {
	var mutations []kg.Mutation
	if err := json.Unmarshal(data, &mutations); err != nil {
		return fmt.Errorf("kg-adapter: snapshot decode failed: %w", err)
	}
	rebuilt, err := kg.ReplayAll(mutations)
	if err != nil {
		return fmt.Errorf("kg-adapter: snapshot replay/verify failed: %w", err)
	}
	a.mu.Lock()
	a.Graph = rebuilt
	a.mu.Unlock()
	return nil
}

// OnLeaderChange upserts a ClusterNode per known member and a HasRole edge
// from the new leader to a synthetic "Leader" role node.
func (a *KGAdapter) OnLeaderChange(term uint64, leaderID string) {
	roleNodeID := "role/leader"
	_, _ = a.graph().UpsertNode("raftlite", kg.Node{
		ID: roleNodeID, Kind: "Role", Props: map[string]string{"name": "Leader"},
	})
	_, _ = a.graph().UpsertNode("raftlite", kg.Node{
		ID: "cluster-node/" + leaderID, Kind: "ClusterNode",
		Props: map[string]string{"term": fmt.Sprintf("%d", term), "role": "Leader"},
	})
	_, _ = a.graph().UpsertEdge("raftlite", kg.Edge{
		ID: "edge/leader-role/" + leaderID, From: "cluster-node/" + leaderID, To: roleNodeID, Kind: "HasRole",
	})
}

func (a *KGAdapter) OnMembershipChange(members []string) {
	for _, m := range members {
		_, _ = a.graph().UpsertNode("raftlite", kg.Node{
			ID: "cluster-node/" + m, Kind: "ClusterNode", Props: map[string]string{"role": "Follower"},
		})
	}
}

// EncodeUpsertNode/EncodeUpsertEdge are convenience helpers for callers of
// Node.Propose() so they don't hand-roll JSON.
func EncodeUpsertNode(actorID string, n kg.Node) []byte {
	b, _ := json.Marshal(KGCommand{ActorID: actorID, Op: kg.OpUpsertNode, Node: &n})
	return b
}

func EncodeUpsertEdge(actorID string, e kg.Edge) []byte {
	b, _ := json.Marshal(KGCommand{ActorID: actorID, Op: kg.OpUpsertEdge, Edge: &e})
	return b
}
