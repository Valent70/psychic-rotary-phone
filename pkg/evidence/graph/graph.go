// Package graph implements Veriqo's Evidence Graph — closing the
// explicit gap named in "Hal_yang_sangat_krusial.docx" section 2
// (Unified Evidence System): "Saya belum melihat: Evidence ontology,
// Evidence graph, Evidence lineage lengkap, Evidence provenance yang
// menyeluruh, Evidence dependency graph. Padahal inilah fondasi trust."
//
// This package gives every piece of evidence anywhere in Veriqo (a raw
// observation, a fused claim, a TruthVersion, a decision) a content-
// addressed node and lets callers record WHY one node exists in terms of
// others (derived_from, supports, contradicts, corrects) — so any
// decision can be traced back to its full evidentiary ancestry, and any
// piece of evidence can be traced forward to everything it influenced.
//
// Design invariants: no time.Now(), no UUIDs (NodeID is SHA-256 of
// Kind+Payload, so identical evidence submitted twice from different
// call sites collapses to the same node — deduplication is a free
// side-effect of content-addressing), append-only (edges are never
// removed, only added), deterministic traversal (children/parents always
// returned in sorted NodeID order).
package graph

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	ErrNodeNotFound = errors.New("graph: node not found")
	ErrCycle        = errors.New("graph: edge would introduce a cycle")
)

// NodeID is a content-addressed evidence node identifier.
type NodeID string

// Node is one evidence artifact — deliberately generic (Kind + Payload)
// so this package needs no dependency on fusion/contradiction/decision
// and never risks an import cycle; those packages' records are
// registered here as opaque, hashable payloads.
type Node struct {
	ID      NodeID
	Kind    string // e.g. "raw_observation", "truth_version", "decision"
	Payload string // canonical string form of the original record
}

func computeNodeID(kind, payload string) NodeID {
	sum := sha256.Sum256([]byte(kind + "|" + payload))
	return NodeID(hex.EncodeToString(sum[:]))
}

// Relation is the kind of provenance edge between two evidence nodes.
type Relation string

const (
	RelationDerivedFrom Relation = "derived_from" // child was computed FROM parent
	RelationSupports    Relation = "supports"     // node supports another claim/decision
	RelationContradicts Relation = "contradicts"  // node contradicts another
	RelationCorrects    Relation = "corrects"     // node is a later correction of another
)

// Edge is one provenance relationship: From --Relation--> To.
type Edge struct {
	From     NodeID
	To       NodeID
	Relation Relation
}

// Graph is the live, in-memory Evidence Graph.
type Graph struct {
	mu sync.RWMutex

	nodes map[NodeID]Node
	// outgoing[a] = edges where a is From (a -> b): "what did a produce/influence"
	outgoing map[NodeID][]Edge
	// incoming[b] = edges where b is To (a -> b): "what produced/influenced b"
	incoming map[NodeID][]Edge
}

func NewGraph() *Graph {
	return &Graph{
		nodes:    make(map[NodeID]Node),
		outgoing: make(map[NodeID][]Edge),
		incoming: make(map[NodeID][]Edge),
	}
}

// AddNode registers (or, if identical content already exists,
// deduplicates onto) an evidence node and returns its content-addressed
// ID.
func (g *Graph) AddNode(kind, payload string) NodeID {
	id := computeNodeID(kind, payload)
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, exists := g.nodes[id]; !exists {
		g.nodes[id] = Node{ID: id, Kind: kind, Payload: payload}
	}
	return id
}

// AddEdge records a provenance relationship. Both nodes must already
// exist. DERIVED_FROM and CORRECTS edges are checked for cycles (a node
// cannot, transitively, be derived from itself); SUPPORTS/CONTRADICTS
// are non-hierarchical and not cycle-checked.
func (g *Graph) AddEdge(from, to NodeID, relation Relation) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.nodes[from]; !ok {
		return fmt.Errorf("%w: %s", ErrNodeNotFound, from)
	}
	if _, ok := g.nodes[to]; !ok {
		return fmt.Errorf("%w: %s", ErrNodeNotFound, to)
	}
	if (relation == RelationDerivedFrom || relation == RelationCorrects) && g.reachableLocked(to, from) {
		return fmt.Errorf("%w: %s -> %s via %s", ErrCycle, from, to, relation)
	}
	e := Edge{From: from, To: to, Relation: relation}
	g.outgoing[from] = append(g.outgoing[from], e)
	g.incoming[to] = append(g.incoming[to], e)
	return nil
}

// reachableLocked reports whether `to` is reachable from `from` by
// following outgoing edges — used for cycle detection. Caller must hold
// g.mu.
func (g *Graph) reachableLocked(from, to NodeID) bool {
	if from == to {
		return true
	}
	visited := map[NodeID]bool{from: true}
	queue := []NodeID{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range g.outgoing[cur] {
			if e.To == to {
				return true
			}
			if !visited[e.To] {
				visited[e.To] = true
				queue = append(queue, e.To)
			}
		}
	}
	return false
}

func (g *Graph) Node(id NodeID) (Node, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	n, ok := g.nodes[id]
	return n, ok
}

// Lineage returns the full ancestry of a node — every node reachable by
// following DERIVED_FROM/CORRECTS edges BACKWARD (via incoming edges),
// i.e. "what evidence led to this node existing" — the concrete
// "traceable back to the underlying evidence chain" capability named in
// the audit doc. Returned in a stable, deterministic order (breadth-
// first, ties broken by NodeID).
func (g *Graph) Lineage(id NodeID) ([]Node, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if _, ok := g.nodes[id]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrNodeNotFound, id)
	}
	visited := map[NodeID]bool{id: true}
	queue := []NodeID{id}
	var order []NodeID
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		// A derived_from/corrects edge is child --relation--> parent
		// (From=child, To=parent). Lineage walks backward from a child
		// to its parents, so we look at outgoing[cur] (edges where cur
		// is the From/child) and follow their To side.
		var parents []NodeID
		for _, e := range g.outgoing[cur] {
			if e.Relation == RelationDerivedFrom || e.Relation == RelationCorrects {
				parents = append(parents, e.To)
			}
		}
		sort.Slice(parents, func(i, j int) bool { return parents[i] < parents[j] })
		for _, p := range parents {
			if !visited[p] {
				visited[p] = true
				queue = append(queue, p)
				order = append(order, p)
			}
		}
	}
	out := make([]Node, 0, len(order))
	for _, id := range order {
		out = append(out, g.nodes[id])
	}
	return out, nil
}

// Influenced returns everything this node influenced FORWARD: every
// node reachable by repeatedly finding children whose DERIVED_FROM/
// CORRECTS edge points TO the current node (i.e. the exact inverse
// traversal of Lineage). If Lineage(X) contains Y, then
// Influenced(Y) contains X.
func (g *Graph) Influenced(id NodeID) ([]Node, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if _, ok := g.nodes[id]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrNodeNotFound, id)
	}
	visited := map[NodeID]bool{id: true}
	queue := []NodeID{id}
	var order []NodeID
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		// A derived_from/corrects edge is child --relation--> parent
		// (stored as From=child, To=parent). "Influenced" walks forward
		// from a parent to its children, so we look at incoming[cur]
		// (edges where cur is the To/parent) and follow their From side.
		var children []NodeID
		for _, e := range g.incoming[cur] {
			if e.Relation == RelationDerivedFrom || e.Relation == RelationCorrects {
				children = append(children, e.From)
			}
		}
		sort.Slice(children, func(i, j int) bool { return children[i] < children[j] })
		for _, c := range children {
			if !visited[c] {
				visited[c] = true
				queue = append(queue, c)
				order = append(order, c)
			}
		}
	}
	out := make([]Node, 0, len(order))
	for _, id := range order {
		out = append(out, g.nodes[id])
	}
	return out, nil
}

// Provenance returns the full edge list for a node (both directions) —
// the raw dependency-graph view an auditor or the IVF package's
// EvidencePackage builder can consume directly.
func (g *Graph) Provenance(id NodeID) []Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := append([]Edge{}, g.outgoing[id]...)
	out = append(out, g.incoming[id]...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].To < out[j].To
	})
	return out
}

// AllNodes returns every node currently in the graph, sorted by NodeID
// for deterministic output — the full-state snapshot a persistence
// layer needs to durably reconstruct this graph after a restart.
func (g *Graph) AllNodes() []Node {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]Node, 0, len(g.nodes))
	for _, n := range g.nodes {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// AllEdges returns every edge currently in the graph exactly once
// (outgoing only — each edge is recorded in outgoing[From], so this
// avoids the From/To double count Provenance intentionally allows for
// single-node queries), sorted for deterministic output.
func (g *Graph) AllEdges() []Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var out []Edge
	for _, edges := range g.outgoing {
		out = append(out, edges...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		if out[i].To != out[j].To {
			return out[i].To < out[j].To
		}
		return out[i].Relation < out[j].Relation
	})
	return out
}

// NodeCount / EdgeCount are small introspection helpers used by tests
// and by callers building IVF evidence packages that need a stable
// count for manifest sizing.
func (g *Graph) NodeCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.nodes)
}

func (g *Graph) EdgeCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	n := 0
	for _, edges := range g.outgoing {
		n += len(edges)
	}
	return n
}
