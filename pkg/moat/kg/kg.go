// Package kg implements Veriqo's MOAT Knowledge Graph: a deterministic,
// hash-chained graph store that is the commercial differentiator layer
// sitting on top of the Raft trust substrate (see raftlite.FSM adapter
// in this same module, wired via Adapter in fsm_adapter.go).
package kg

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	ErrNodeNotFound   = errors.New("kg: node not found")
	ErrNodeExists     = errors.New("kg: node already exists")
	ErrEdgeExists     = errors.New("kg: edge already exists")
	ErrEndpointMiss   = errors.New("kg: edge endpoint does not exist")
	ErrTamperDetected = errors.New("kg: hash chain verification failed")
)

type OpType string

const (
	OpUpsertNode OpType = "UPSERT_NODE"
	OpDeleteNode OpType = "DELETE_NODE"
	OpUpsertEdge OpType = "UPSERT_EDGE"
	OpDeleteEdge OpType = "DELETE_EDGE"
)

// Node is a single vertex in the knowledge graph.
type Node struct {
	ID    string            `json:"id"`
	Kind  string            `json:"kind"` // e.g. "ClusterNode", "Vessel", "Document"
	Props map[string]string `json:"props"`
}

// Edge is a directed, typed relationship between two nodes.
type Edge struct {
	ID    string            `json:"id"`
	From  string            `json:"from"`
	To    string            `json:"to"`
	Kind  string            `json:"kind"` // e.g. "HasRole", "ReplicatesFrom"
	Props map[string]string `json:"props"`
}

// Mutation is one hash-chained log entry.
type Mutation struct {
	Index    uint64 `json:"index"`
	ActorID  string `json:"actor_id"`
	Op       OpType `json:"op"`
	Node     *Node  `json:"node,omitempty"`
	Edge     *Edge  `json:"edge,omitempty"`
	PrevHash string `json:"prev_hash"`
	Hash     string `json:"hash"`
}

// Graph is the live, in-memory MOAT knowledge graph.
type Graph struct {
	mu    sync.RWMutex
	nodes map[string]Node
	edges map[string]Edge
	log   []Mutation
	head  string // current chain head hash ("" = genesis)
}

func NewGraph() *Graph {
	return &Graph{
		nodes: make(map[string]Node),
		edges: make(map[string]Edge),
	}
}

// canonicalBytes produces a deterministic byte encoding of a mutation,
// independent of Go's randomized map iteration order — this is the exact
// class of nondeterminism bug the transport layer's SimNetwork hit
// previously, so every field here is explicitly sorted/ordered.
func canonicalBytes(index uint64, actorID string, op OpType, n *Node, e *Edge, prevHash string) []byte {
	b := []byte(fmt.Sprintf("idx=%d|actor=%s|op=%s|prev=%s|", index, actorID, op, prevHash))
	if n != nil {
		b = append(b, []byte(fmt.Sprintf("node.id=%s|node.kind=%s|node.props=%s|", n.ID, n.Kind, canonicalProps(n.Props)))...)
	}
	if e != nil {
		b = append(b, []byte(fmt.Sprintf("edge.id=%s|edge.from=%s|edge.to=%s|edge.kind=%s|edge.props=%s|",
			e.ID, e.From, e.To, e.Kind, canonicalProps(e.Props)))...)
	}
	return b
}

func canonicalProps(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := ""
	for _, k := range keys {
		out += k + "=" + m[k] + ";"
	}
	return out
}

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// apply is the single mutation entrypoint — every write path (UpsertNode,
// DeleteNode, UpsertEdge, DeleteEdge, and the raftlite FSM adapter) funnels
// through here so the hash chain is always consistent.
func (g *Graph) apply(actorID string, op OpType, n *Node, e *Edge) (Mutation, error) {
	switch op {
	case OpUpsertNode:
		if n == nil {
			return Mutation{}, errors.New("kg: nil node")
		}
	case OpDeleteNode:
		if n == nil {
			return Mutation{}, errors.New("kg: nil node")
		}
		if _, ok := g.nodes[n.ID]; !ok {
			return Mutation{}, ErrNodeNotFound
		}
	case OpUpsertEdge:
		if e == nil {
			return Mutation{}, errors.New("kg: nil edge")
		}
		if _, ok := g.nodes[e.From]; !ok {
			return Mutation{}, ErrEndpointMiss
		}
		if _, ok := g.nodes[e.To]; !ok {
			return Mutation{}, ErrEndpointMiss
		}
	case OpDeleteEdge:
		if e == nil {
			return Mutation{}, errors.New("kg: nil edge")
		}
		if _, ok := g.edges[e.ID]; !ok {
			return Mutation{}, errors.New("kg: edge not found")
		}
	default:
		return Mutation{}, fmt.Errorf("kg: unknown op %q", op)
	}

	index := uint64(len(g.log)) + 1
	prev := g.head
	cb := canonicalBytes(index, actorID, op, n, e, prev)
	h := hashOf(cb)

	m := Mutation{Index: index, ActorID: actorID, Op: op, Node: n, Edge: e, PrevHash: prev, Hash: h}

	// mutate live state
	switch op {
	case OpUpsertNode:
		g.nodes[n.ID] = *n
	case OpDeleteNode:
		delete(g.nodes, n.ID)
		// cascade delete: remove edges touching this node
		for id, edge := range g.edges {
			if edge.From == n.ID || edge.To == n.ID {
				delete(g.edges, id)
			}
		}
	case OpUpsertEdge:
		g.edges[e.ID] = *e
	case OpDeleteEdge:
		delete(g.edges, e.ID)
	}

	g.log = append(g.log, m)
	g.head = h
	return m, nil
}

func (g *Graph) Apply(actorID string, op OpType, n *Node, e *Edge) (Mutation, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.apply(actorID, op, n, e)
}

func (g *Graph) UpsertNode(actorID string, n Node) (Mutation, error) {
	return g.Apply(actorID, OpUpsertNode, &n, nil)
}

func (g *Graph) DeleteNode(actorID, id string) (Mutation, error) {
	return g.Apply(actorID, OpDeleteNode, &Node{ID: id}, nil)
}

func (g *Graph) UpsertEdge(actorID string, e Edge) (Mutation, error) {
	return g.Apply(actorID, OpUpsertEdge, nil, &e)
}

func (g *Graph) DeleteEdge(actorID, id string) (Mutation, error) {
	return g.Apply(actorID, OpDeleteEdge, nil, &Edge{ID: id})
}

func (g *Graph) GetNode(id string) (Node, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	n, ok := g.nodes[id]
	return n, ok
}

func (g *Graph) RootHash() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.head
}

func (g *Graph) LogLen() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.log)
}

// Snapshot returns a defensive copy of the mutation log for replay/export.
func (g *Graph) Snapshot() []Mutation {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]Mutation, len(g.log))
	copy(out, g.log)
	return out
}

// ReplayAll rebuilds a fresh graph from a mutation log, re-deriving every
// hash from canonical bytes rather than trusting the stored Hash field.
// Returns ErrTamperDetected on any mismatch or broken chain linkage.
func ReplayAll(log []Mutation) (*Graph, error) {
	g := NewGraph()
	prev := ""
	for i, m := range log {
		wantIndex := uint64(i) + 1
		if m.Index != wantIndex {
			return nil, fmt.Errorf("%w: index gap at %d", ErrTamperDetected, i)
		}
		if m.PrevHash != prev {
			return nil, fmt.Errorf("%w: broken chain linkage at index %d", ErrTamperDetected, m.Index)
		}
		cb := canonicalBytes(m.Index, m.ActorID, m.Op, m.Node, m.Edge, m.PrevHash)
		recomputed := hashOf(cb)
		if recomputed != m.Hash {
			return nil, fmt.Errorf("%w: hash mismatch at index %d", ErrTamperDetected, m.Index)
		}
		if _, err := g.apply(m.ActorID, m.Op, m.Node, m.Edge); err != nil {
			return nil, fmt.Errorf("kg: replay apply failed at index %d: %w", m.Index, err)
		}
		prev = recomputed
	}
	return g, nil
}

// ClusterNode / cluster-topology convenience view — the concrete "KG as a
// live view of cluster topology" example: raftlite emits ClusterEvent on
// leader/term/membership change (see fsm_adapter.go), which upserts these.
type ClusterStatus struct {
	Nodes []Node
	Edges []Edge
}

func (g *Graph) GetClusterStatus() ClusterStatus {
	g.mu.RLock()
	defer g.mu.RUnlock()
	cs := ClusterStatus{}
	for _, n := range g.nodes {
		if n.Kind == "ClusterNode" {
			cs.Nodes = append(cs.Nodes, n)
		}
	}
	for _, e := range g.edges {
		if e.Kind == "HasRole" || e.Kind == "ReplicatesFrom" {
			cs.Edges = append(cs.Edges, e)
		}
	}
	sort.Slice(cs.Nodes, func(i, j int) bool { return cs.Nodes[i].ID < cs.Nodes[j].ID })
	sort.Slice(cs.Edges, func(i, j int) bool { return cs.Edges[i].ID < cs.Edges[j].ID })
	return cs
}
