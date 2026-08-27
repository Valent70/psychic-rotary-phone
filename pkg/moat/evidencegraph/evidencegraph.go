// Package evidencegraph closes the P0 gap both v7.10.2 audits named
// explicitly: evidence correlation in this repo previously relied on
// pkg/moat/intelligence/hbayes's conservative static evidenceFamily/
// max() mechanism — "if Source A -> Satellite provider X and Source B
// -> Satellite provider X, then A and B are not independent evidence
// merely because they have different source IDs" was a known
// structural weakness, not a modeled one.
//
// This is deliberately NOT another provenance/trust system —
// pkg/moat/provenance already computes source-level independence
// (declared ancestor sharing) and pkg/core/trustcalc already owns
// trust belief. EvidenceDependencyGraph generalizes ONE further step:
// dependency is not just "shares a declared upstream source", it can
// also be producer-level, derivation-level, or transformation-level
// (e.g. two analysts both running the SAME detection MODEL over
// different raw feeds are correlated even with zero shared source
// ancestry). The graph's only output is an effective-weight
// discount — it does not decide independence status (provenance still
// owns that) and it does not hold belief (trustcalc still owns that).
package evidencegraph

import (
	"context"
	"sort"
	"strconv"
	"sync"

	"veriqo/pkg/platform/telemetry"
)

// DependencyKind classifies WHY two evidence nodes might be
// correlated rather than independent.
type DependencyKind string

const (
	DependsOnSource         DependencyKind = "SOURCE"
	DependsOnProducer       DependencyKind = "PRODUCER"
	DependsOnDerivation     DependencyKind = "DERIVATION"
	DependsOnTransformation DependencyKind = "TRANSFORMATION"
)

// NodeID identifies one evidence-graph node — typically a source ID,
// but also usable for producers/derivations/transformations that
// several sources share.
type NodeID string

// Graph is a directed dependency graph: edges point from an evidence
// node to whatever upstream thing it depends on (a producer, a raw
// feed, a shared model). Multiple evidence nodes sharing an ancestor
// are correlated; the graph does not need to be a tree.
type Graph struct {
	mu sync.RWMutex
	// edges[node] = set of direct dependencies for node
	edges map[NodeID]map[NodeID]DependencyKind
	// meta[edge] = the full DependencyRecord backing that edge
	meta map[edgeKey]DependencyRecord
	// ledger is the hash-chained, replayable dependency log
	ledger []DependencyRecord
	seen   map[string]bool
}

// edgeKey identifies one directed dependency edge.
type edgeKey struct {
	node     NodeID
	upstream NodeID
}

func NewGraph() *Graph {
	return &Graph{
		edges: make(map[NodeID]map[NodeID]DependencyKind),
		meta:  make(map[edgeKey]DependencyRecord),
		seen:  make(map[string]bool),
	}
}

// DependsOn records that `node` depends on `upstream` for the given
// reason. Safe to call multiple times; a node may have several
// independent dependency edges (e.g. both a producer AND a shared raw
// feed).
// It is a convenience wrapper over Declare for the simple case
// (confidence 1.0, always valid, no provenance metadata); errors from
// the underlying Declare are intentionally swallowed here — an
// identical repeat declaration is a no-op, and the only other possible
// errors (empty/self dependency, unknown kind) are programming errors
// this legacy shape cannot express meaningfully. Production callers
// should use Declare and check the error.
func (g *Graph) DependsOn(node, upstream NodeID, kind DependencyKind) {
	_, _ = g.Declare(DependencyRecord{
		NodeID: node, UpstreamID: upstream, Kind: kind, Confidence: 1,
	})
}

// ancestors returns every node reachable upstream from `node`
// (excluding node itself), via BFS — cycle-safe.
func (g *Graph) ancestors(node NodeID) map[NodeID]bool {
	seen := map[NodeID]bool{}
	queue := []NodeID{node}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for up := range g.edges[cur] {
			if !seen[up] {
				seen[up] = true
				queue = append(queue, up)
			}
		}
	}
	return seen
}

// sharedAncestorFraction returns, for a set of nodes, how much of
// each node's ancestor set overlaps with the UNION of every other
// node's ancestor set — a [0,1] correlation signal. 0 means no shared
// ancestry with anything else in the set (fully independent); values
// approaching 1 mean this node's entire upstream lineage is also
// reachable from at least one other node in the set (fully
// correlated/redundant).
func (g *Graph) sharedAncestorFraction(nodes []NodeID) map[NodeID]float64 {
	ancestorSets := make(map[NodeID]map[NodeID]bool, len(nodes))
	for _, n := range nodes {
		ancestorSets[n] = g.ancestors(n)
	}
	out := make(map[NodeID]float64, len(nodes))
	for _, n := range nodes {
		mine := ancestorSets[n]
		if len(mine) == 0 {
			out[n] = 0 // no known ancestry at all -> nothing to be correlated via
			continue
		}
		others := map[NodeID]bool{}
		for _, m := range nodes {
			if m == n {
				continue
			}
			for a := range ancestorSets[m] {
				others[a] = true
			}
		}
		shared := 0
		for a := range mine {
			if others[a] {
				shared++
			}
		}
		out[n] = float64(shared) / float64(len(mine))
	}
	return out
}

// EffectiveWeight redistributes a set of nodes' base evidence weights
// to discount shared ancestry: a node whose entire upstream lineage
// is also reachable from other nodes in the set has its weight
// discounted toward zero (it adds little independent information);
// a node with zero shared ancestry keeps its full base weight. This
// is the concrete "effective evidence weight must account for
// dependency graph" requirement from the audit — a caller (e.g.
// pkg/canonical or the hbayes engine) uses these weights in place of
// raw base weights when combining evidence.
//
//	effective(n) = base(n) * (1 - sharedAncestorFraction(n))
func (g *Graph) EffectiveWeight(nodes []NodeID, base map[NodeID]float64) map[NodeID]float64 {
	_, span := telemetry.StartSpan(context.Background(), "evidencegraph.EffectiveWeight",
		telemetry.Attribute{Key: "node_count", Value: strconv.Itoa(len(nodes))})
	defer span.End()

	g.mu.RLock()
	defer g.mu.RUnlock()
	shared := g.sharedAncestorFraction(nodes)
	out := make(map[NodeID]float64, len(nodes))
	for _, n := range nodes {
		out[n] = base[n] * (1 - shared[n])
	}
	return out
}

// CorrelationFamilies groups nodes that share ANY ancestor into the
// same family — a coarser, human-readable view of the same
// information EffectiveWeight uses internally (e.g. for display: "3
// evidence items, but only 2 independent families").
func (g *Graph) CorrelationFamilies(nodes []NodeID) [][]NodeID {
	g.mu.RLock()
	defer g.mu.RUnlock()
	ancestorSets := make(map[NodeID]map[NodeID]bool, len(nodes))
	for _, n := range nodes {
		ancestorSets[n] = g.ancestors(n)
	}
	parent := make(map[NodeID]NodeID, len(nodes))
	for _, n := range nodes {
		parent[n] = n
	}
	var find func(NodeID) NodeID
	find = func(x NodeID) NodeID {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	union := func(a, b NodeID) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[rb] = ra
		}
	}
	for i, a := range nodes {
		for j := i + 1; j < len(nodes); j++ {
			b := nodes[j]
			if len(ancestorSets[a]) == 0 || len(ancestorSets[b]) == 0 {
				continue
			}
			for anc := range ancestorSets[a] {
				if ancestorSets[b][anc] {
					union(a, b)
					break
				}
			}
		}
	}
	groups := map[NodeID][]NodeID{}
	for _, n := range nodes {
		r := find(n)
		groups[r] = append(groups[r], n)
	}
	out := make([][]NodeID, 0, len(groups))
	for _, g := range groups {
		sort.Slice(g, func(i, j int) bool { return g[i] < g[j] })
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}
