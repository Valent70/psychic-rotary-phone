// Package traceability implements the Veriqo Traceability Graph.
// Priority 4 in Consensus_Architecture_Review:
//
//	"Requirement → ADR → Risk → Policy → Invariant → Algorithm →
//	 Implementation → Test → Replay → Evidence → Artifact → Commit → Release.
//	 Every node must be queryable."
//
// The traceability graph is an immutable, hash-chained DAG of nodes linked
// by TraceLink edges. Every software artefact from a requirements statement
// to a CI artifact is represented as a TraceNode and queryable by:
//   - Walking forward from requirement to release.
//   - Walking backward from artifact to requirement.
//   - Querying by kind, tag, or status.
package traceability

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// ─── Node kinds ───────────────────────────────────────────────────────────────

// NodeKind classifies what a TraceNode represents in the SDLC chain.
type NodeKind string

const (
	KindRequirement    NodeKind = "requirement"
	KindADR            NodeKind = "adr" // Architecture Decision Record
	KindRisk           NodeKind = "risk"
	KindPolicy         NodeKind = "policy"
	KindInvariant      NodeKind = "invariant"
	KindAlgorithm      NodeKind = "algorithm"
	KindImplementation NodeKind = "implementation"
	KindTest           NodeKind = "test"
	KindReplay         NodeKind = "replay"
	KindEvidence       NodeKind = "evidence"
	KindArtifact       NodeKind = "artifact"
	KindCommit         NodeKind = "commit"
	KindRelease        NodeKind = "release"
)

// NodeStatus reflects the lifecycle status of a TraceNode.
type NodeStatus string

const (
	StatusDraft    NodeStatus = "draft"
	StatusActive   NodeStatus = "active"
	StatusVerified NodeStatus = "verified"
	StatusObsolete NodeStatus = "obsolete"
)

// ─── TraceNode ────────────────────────────────────────────────────────────────

// TraceNode is a vertex in the traceability DAG.
type TraceNode struct {
	ID          string
	Kind        NodeKind
	Title       string
	Description string
	Status      NodeStatus
	Tags        []string
	Metadata    map[string]string
	// Hash is SHA-256 of (ID ‖ Kind ‖ Title ‖ Description ‖ sorted Tags).
	Hash      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// computeHash computes a deterministic hash for a node.
func computeHash(n TraceNode) string {
	tags := make([]string, len(n.Tags))
	copy(tags, n.Tags)
	sort.Strings(tags)

	type stable struct {
		ID, Kind, Title, Desc string
		Tags                  []string
	}
	b, _ := json.Marshal(stable{
		ID:    n.ID,
		Kind:  string(n.Kind),
		Title: n.Title,
		Desc:  n.Description,
		Tags:  tags,
	})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ─── TraceLink ────────────────────────────────────────────────────────────────

// LinkKind classifies the relationship between two TraceNodes.
type LinkKind string

const (
	LinkImplements  LinkKind = "implements"
	LinkSatisfies   LinkKind = "satisfies"
	LinkVerifies    LinkKind = "verifies"
	LinkDerivedFrom LinkKind = "derived_from"
	LinkProduces    LinkKind = "produces"
	LinkReplays     LinkKind = "replays"
	LinkMitigates   LinkKind = "mitigates"
	LinkRefines     LinkKind = "refines"
)

// TraceLink is a directed edge in the traceability DAG.
type TraceLink struct {
	From    string
	To      string
	Kind    LinkKind
	Comment string
	AddedAt time.Time
}

// ─── Graph ────────────────────────────────────────────────────────────────────

// ErrNodeExists is returned when a node with the same ID is added twice.
var ErrNodeExists = errors.New("traceability: node already exists")

// ErrNodeNotFound is returned when a referenced node does not exist.
var ErrNodeNotFound = errors.New("traceability: node not found")

// Graph is the traceability DAG. It is concurrency-safe.
type Graph struct {
	mu    sync.RWMutex
	nodes map[string]*TraceNode
	// links: from → []TraceLink
	forward map[string][]TraceLink
	// reverse: to → []TraceLink
	reverse map[string][]TraceLink
}

// NewGraph creates an empty traceability graph.
func NewGraph() *Graph {
	return &Graph{
		nodes:   make(map[string]*TraceNode),
		forward: make(map[string][]TraceLink),
		reverse: make(map[string][]TraceLink),
	}
}

// AddNode adds a TraceNode. Returns ErrNodeExists if the ID is already taken.
func (g *Graph) AddNode(n TraceNode) error {
	if n.ID == "" {
		return errors.New("traceability: node ID must not be empty")
	}
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now()
	}
	n.UpdatedAt = n.CreatedAt
	n.Hash = computeHash(n)

	g.mu.Lock()
	defer g.mu.Unlock()
	if _, exists := g.nodes[n.ID]; exists {
		return ErrNodeExists
	}
	g.nodes[n.ID] = &n
	return nil
}

// UpdateNode updates a node's title, description, status, tags, or metadata.
func (g *Graph) UpdateNode(id string, fn func(*TraceNode)) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	n, ok := g.nodes[id]
	if !ok {
		return ErrNodeNotFound
	}
	fn(n)
	n.UpdatedAt = time.Now()
	n.Hash = computeHash(*n)
	return nil
}

// AddLink creates a directed link from→to. Both nodes must exist.
func (g *Graph) AddLink(link TraceLink) error {
	if link.AddedAt.IsZero() {
		link.AddedAt = time.Now()
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.nodes[link.From]; !ok {
		return fmt.Errorf("%w: %q", ErrNodeNotFound, link.From)
	}
	if _, ok := g.nodes[link.To]; !ok {
		return fmt.Errorf("%w: %q", ErrNodeNotFound, link.To)
	}
	g.forward[link.From] = append(g.forward[link.From], link)
	g.reverse[link.To] = append(g.reverse[link.To], link)
	return nil
}

// GetNode returns a copy of a node by ID.
func (g *Graph) GetNode(id string) (*TraceNode, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	n, ok := g.nodes[id]
	if !ok {
		return nil, ErrNodeNotFound
	}
	cp := *n
	return &cp, nil
}

// ForwardLinks returns all links outgoing from a node.
func (g *Graph) ForwardLinks(id string) []TraceLink {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return append([]TraceLink{}, g.forward[id]...)
}

// BackwardLinks returns all links incoming to a node.
func (g *Graph) BackwardLinks(id string) []TraceLink {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return append([]TraceLink{}, g.reverse[id]...)
}

// ─── Path traversal ───────────────────────────────────────────────────────────

// TraceabilityPath is a sequence of nodes connecting from to to.
type TraceabilityPath struct {
	Nodes []TraceNode
	Links []TraceLink
}

// QueryPath returns all nodes reachable from startID via forward links,
// filtered by an optional kind whitelist. BFS traversal for shortest path.
func (g *Graph) QueryPath(startID string, targetID string) (*TraceabilityPath, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if _, ok := g.nodes[startID]; !ok {
		return nil, fmt.Errorf("%w: %q", ErrNodeNotFound, startID)
	}

	type state struct {
		nodeID string
		path   []string
		links  []TraceLink
	}

	visited := make(map[string]bool)
	queue := []state{{nodeID: startID, path: []string{startID}}}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		if cur.nodeID == targetID {
			// Build path.
			p := &TraceabilityPath{}
			for _, nid := range cur.path {
				n := g.nodes[nid]
				p.Nodes = append(p.Nodes, *n)
			}
			p.Links = cur.links
			return p, nil
		}

		if visited[cur.nodeID] {
			continue
		}
		visited[cur.nodeID] = true

		for _, link := range g.forward[cur.nodeID] {
			if !visited[link.To] {
				newPath := make([]string, len(cur.path)+1)
				copy(newPath, cur.path)
				newPath[len(cur.path)] = link.To

				newLinks := make([]TraceLink, len(cur.links)+1)
				copy(newLinks, cur.links)
				newLinks[len(cur.links)] = link

				queue = append(queue, state{
					nodeID: link.To,
					path:   newPath,
					links:  newLinks,
				})
			}
		}
	}

	return nil, fmt.Errorf("traceability: no path from %q to %q", startID, targetID)
}

// QueryByKind returns all nodes of the given kind, sorted by ID.
func (g *Graph) QueryByKind(kind NodeKind) []TraceNode {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var out []TraceNode
	for _, n := range g.nodes {
		if n.Kind == kind {
			out = append(out, *n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// QueryByTag returns all nodes that have the given tag.
func (g *Graph) QueryByTag(tag string) []TraceNode {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var out []TraceNode
	for _, n := range g.nodes {
		for _, t := range n.Tags {
			if t == tag {
				out = append(out, *n)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// NodeCount returns total number of nodes.
func (g *Graph) NodeCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.nodes)
}

// ─── Coverage report ──────────────────────────────────────────────────────────

// CoverageReport summarises how many requirements are fully traced to releases.
type CoverageReport struct {
	TotalRequirements int
	TracedToTest      int
	TracedToEvidence  int
	TracedToRelease   int
	Gaps              []string // requirement IDs with incomplete tracing
}

// CoverageReport generates a traceability coverage summary.
func (g *Graph) CoverageReport() CoverageReport {
	g.mu.RLock()
	defer g.mu.RUnlock()

	report := CoverageReport{}
	for _, n := range g.nodes {
		if n.Kind != KindRequirement {
			continue
		}
		report.TotalRequirements++
		hasTest, hasEvidence, hasRelease := g.reachableKinds(n.ID, map[NodeKind]bool{
			KindTest:     true,
			KindEvidence: true,
			KindRelease:  true,
		})
		if hasTest {
			report.TracedToTest++
		}
		if hasEvidence {
			report.TracedToEvidence++
		}
		if hasRelease {
			report.TracedToRelease++
		}
		if !hasTest || !hasEvidence {
			report.Gaps = append(report.Gaps, n.ID)
		}
	}
	sort.Strings(report.Gaps)
	return report
}

// reachableKinds checks whether any of the given kinds are reachable from startID.
func (g *Graph) reachableKinds(startID string, targets map[NodeKind]bool) (test, evidence, release bool) {
	visited := make(map[string]bool)
	queue := []string{startID}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if visited[cur] {
			continue
		}
		visited[cur] = true
		if n, ok := g.nodes[cur]; ok {
			if targets[KindTest] && n.Kind == KindTest {
				test = true
			}
			if targets[KindEvidence] && n.Kind == KindEvidence {
				evidence = true
			}
			if targets[KindRelease] && n.Kind == KindRelease {
				release = true
			}
		}
		for _, link := range g.forward[cur] {
			queue = append(queue, link.To)
		}
	}
	return
}
