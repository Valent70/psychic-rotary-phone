// Package graph is the single VERIQO evidence graph.
//
// # relationship != fact
//
// This is the sentence the package is built around. An edge here
// cannot exist without:
//
//	evidence references   what it rests on          (Law 1)
//	a temporal scope      when it holds             (temporal truth)
//	a qualification       how well established it is
//
// An edge with none of those is the graph asserting something on its
// own authority. A graph full of them is indistinguishable from a
// graph full of facts, and that is precisely how an inference silently
// becomes one.
//
// # One graph, not one per domain
//
// There is a single node and edge store. The domain views are
// projections computed from the ontology, not separate graphs. The
// alternative -- a maritime graph and a finance graph -- makes
// "which payment settled the cargo this vessel carried" a data
// integration project rather than a traversal.
//
// # Why traversal returns paths rather than nodes
//
// A reachability answer is not usable in a case. "These two
// organisations are connected" means nothing until you can see the
// four edges connecting them, what each rests on, and how well each is
// established. So Paths returns the edges, and the WEAKEST
// qualification along a path is the path's qualification -- a chain is
// no better established than its worst link.
package graph

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"veriqo/pkg/contract"
	"veriqo/pkg/entity"
	"veriqo/pkg/ontology"
)

var (
	ErrNoEvidence      = errors.New("graph: an edge with no evidence is an assertion, not a relationship")
	ErrNoScope         = errors.New("graph: an edge with no temporal scope asserts that it always held")
	ErrUnknownNode     = errors.New("graph: unknown node")
	ErrDuplicateNode   = errors.New("graph: node already present")
	ErrTypeMismatch    = errors.New("graph: node type does not match the ontology")
	ErrUnqualified     = errors.New("graph: an edge must carry a qualification")
	ErrCrossTenantEdge = errors.New("graph: an edge may not join nodes in different tenants")
)

// Qualification is how well established an edge is.
//
// It is deliberately coarse. A finer scale invites averaging, and the
// only operation this package performs on it is taking the MINIMUM
// along a path -- which needs an order, not a magnitude.
type Qualification string

const (
	// Asserted: somebody said so. The floor, not a failure.
	Asserted Qualification = "ASSERTED"
	// Documented: a document records it.
	Documented Qualification = "DOCUMENTED"
	// Corroborated: independent sources agree.
	Corroborated Qualification = "CORROBORATED"
	// Verified: checked against an authoritative register.
	Verified Qualification = "VERIFIED"
	// Contested: contradicting evidence exists. It is NOT the bottom
	// of the scale -- it is off it, because a contested edge is a
	// different thing from a weakly supported one.
	Contested Qualification = "CONTESTED"
)

var qualRank = map[Qualification]int{
	Contested: -1, Asserted: 0, Documented: 1, Corroborated: 2, Verified: 3,
}

func (q Qualification) Valid() bool { _, ok := qualRank[q]; return ok }

// Rank orders the scale. Contested sorts below everything.
func (q Qualification) Rank() int { return qualRank[q] }

// Weaker returns the lesser of two qualifications. A path is no
// better established than its worst link.
func Weaker(a, b Qualification) Qualification {
	if a.Rank() <= b.Rank() {
		return a
	}
	return b
}

// Node is an entity placed in the graph under an ontology type.
type Node struct {
	Entity     entity.Entity `json:"entity"`
	ObjectType string        `json:"object_type"`
}

// Edge is a relationship: a claim about the world, with what it rests
// on and when it holds.
type Edge struct {
	ID   contract.ID `json:"id"`
	Type string      `json:"type"`
	From contract.ID `json:"from"`
	To   contract.ID `json:"to"`

	Scope contract.Interval `json:"scope"`

	EvidenceRefs  []string      `json:"evidence_refs"`
	Qualification Qualification `json:"qualification"`

	// ResolutionRef points at the entity-resolution result when the
	// edge exists because two entities were resolved. It is how a
	// merge that turns out to be wrong can be traced to the edges it
	// created.
	ResolutionRef string `json:"resolution_ref,omitempty"`

	// Contradictions names the evidence that argues against this edge.
	// It is stored ON the edge rather than elsewhere, so an edge can
	// never be read without its counter-evidence.
	Contradictions []string `json:"contradictions,omitempty"`
}

func (e Edge) Validate() error {
	if e.ID == "" {
		return fmt.Errorf("%w: edge has no id", contract.ErrMalformedID)
	}
	if err := e.ID.Validate(); err != nil {
		return err
	}
	if e.Type == "" || e.From == "" || e.To == "" {
		return errors.New("graph: an edge needs a type and two endpoints")
	}
	if len(e.EvidenceRefs) == 0 {
		return fmt.Errorf("%w: %s (%s)", ErrNoEvidence, e.ID, e.Type)
	}
	if !e.Scope.Valid() {
		return fmt.Errorf("%w: %s (%s)", ErrNoScope, e.ID, e.Type)
	}
	if !e.Qualification.Valid() {
		return fmt.Errorf("%w: %s carries %q", ErrUnqualified, e.ID, e.Qualification)
	}
	// An edge with contradictions recorded may not claim to be
	// established: the two statements cannot both be made.
	if len(e.Contradictions) > 0 && e.Qualification != Contested {
		return fmt.Errorf("graph: %s records %d contradiction(s) and is qualified %s; "+
			"an edge with counter-evidence is CONTESTED",
			e.ID, len(e.Contradictions), e.Qualification)
	}
	return nil
}

// HoldsAt reports whether the edge's scope covers an instant.
func (e Edge) HoldsAt(t time.Time) bool { return e.Scope.Contains(t) }

// Graph is the store.
type Graph struct {
	TenantID string
	ont      *ontology.Ontology

	nodes map[contract.ID]Node
	edges map[contract.ID]Edge
	out   map[contract.ID][]contract.ID
	in    map[contract.ID][]contract.ID
}

// New builds an empty graph bound to an ontology.
func New(tenantID string, ont *ontology.Ontology) (*Graph, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, errors.New("graph: not anchored to a tenant")
	}
	if ont == nil {
		return nil, errors.New("graph: no ontology; an unschematised graph cannot be checked")
	}
	return &Graph{TenantID: tenantID, ont: ont,
		nodes: map[contract.ID]Node{}, edges: map[contract.ID]Edge{},
		out: map[contract.ID][]contract.ID{}, in: map[contract.ID][]contract.ID{}}, nil
}

// Ontology returns the schema this graph is checked against.
func (g *Graph) Ontology() *ontology.Ontology { return g.ont }

// AddNode places an entity under an ontology type.
func (g *Graph) AddNode(e entity.Entity, objectType string) error {
	if err := e.Validate(); err != nil {
		return err
	}
	if e.TenantID != g.TenantID {
		return fmt.Errorf("%w: %s is in %s", contract.ErrCrossTenant, e.ID, e.TenantID)
	}
	if _, dup := g.nodes[e.ID]; dup {
		return fmt.Errorf("%w: %s", ErrDuplicateNode, e.ID)
	}
	ot, err := g.ont.ObjectType(objectType)
	if err != nil {
		return err
	}
	if ot.Kind != e.Kind {
		return fmt.Errorf("%w: %s is entity kind %s, %s expects %s",
			ErrTypeMismatch, e.ID, e.Kind, objectType, ot.Kind)
	}
	g.nodes[e.ID] = Node{Entity: e, ObjectType: objectType}
	return nil
}

// AddEdge places a relationship, checked against the ontology.
func (g *Graph) AddEdge(e Edge) error {
	if err := e.Validate(); err != nil {
		return err
	}
	from, ok := g.nodes[e.From]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownNode, e.From)
	}
	to, ok := g.nodes[e.To]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownNode, e.To)
	}
	if from.Entity.TenantID != g.TenantID || to.Entity.TenantID != g.TenantID {
		return ErrCrossTenantEdge
	}
	if err := g.ont.CheckEdge(e.Type, from.ObjectType, to.ObjectType); err != nil {
		return err
	}
	// A relationship the ontology declares TEMPORAL may not be open:
	// "who owns it" without an end is the assertion that today's owner
	// always owned it.
	rt, err := g.ont.RelationshipType(e.Type)
	if err != nil {
		return err
	}
	if rt.Temporal && e.Scope.Open() && e.Qualification == Verified {
		return fmt.Errorf("graph: %s is a temporal relationship with an open period and is "+
			"qualified VERIFIED; an unrecorded end is missing information, not confirmation "+
			"that it continues", e.ID)
	}
	if _, dup := g.edges[e.ID]; dup {
		return fmt.Errorf("graph: edge %s already present", e.ID)
	}
	g.edges[e.ID] = e
	g.out[e.From] = append(g.out[e.From], e.ID)
	g.in[e.To] = append(g.in[e.To], e.ID)
	return nil
}

// Node returns a node.
func (g *Graph) Node(id contract.ID) (Node, error) {
	n, ok := g.nodes[id]
	if !ok {
		return Node{}, fmt.Errorf("%w: %s", ErrUnknownNode, id)
	}
	return n, nil
}

// Edge returns an edge.
func (g *Graph) Edge(id contract.ID) (Edge, bool) { e, ok := g.edges[id]; return e, ok }

// Counts reports the graph's size.
func (g *Graph) Counts() (nodes, edges int) { return len(g.nodes), len(g.edges) }

// Path is a traversal result.
type Path struct {
	Edges []Edge
	// Qualification is the WEAKEST link's, which is what the path as a
	// whole is worth.
	Qualification Qualification
	// EvidenceRefs is everything the path rests on, so a finding built
	// on it can cite all of it.
	EvidenceRefs []string
	// Contested is true when any edge on the path is contested.
	Contested bool
}

func (p Path) String() string {
	parts := make([]string, 0, len(p.Edges)+1)
	if len(p.Edges) > 0 {
		parts = append(parts, string(p.Edges[0].From))
	}
	for _, e := range p.Edges {
		parts = append(parts, fmt.Sprintf("-[%s/%s]->%s", e.Type, e.Qualification, e.To))
	}
	return strings.Join(parts, "") + fmt.Sprintf("  (path qualification: %s)", p.Qualification)
}

// Options bound a traversal.
type Options struct {
	// At restricts to edges holding at this instant. A zero At means
	// every period, which is what a historical reconstruction wants
	// and NOT what a present-tense question wants.
	At time.Time
	// MaxDepth bounds the search. Required: an unbounded traversal on
	// a real graph is a denial of service against yourself.
	MaxDepth int
	// Types, when non-empty, restricts to these relationship types.
	Types []string
	// IncludeContested admits contested edges. It defaults to false,
	// so a path through disputed evidence is not returned as though it
	// were established.
	IncludeContested bool
	// Domain, when set, restricts to relationships in that view.
	Domain string
}

// Paths finds paths from one node to another.
//
// It returns them sorted by qualification (best first) then by length,
// so a caller taking the first result gets the best-established
// connection rather than the shortest one -- which are different, and
// the shorter one is often the weaker.
func (g *Graph) Paths(from, to contract.ID, opts Options) ([]Path, error) {
	if _, err := g.Node(from); err != nil {
		return nil, err
	}
	if _, err := g.Node(to); err != nil {
		return nil, err
	}
	if opts.MaxDepth <= 0 {
		return nil, errors.New("graph: MaxDepth must be positive; an unbounded traversal " +
			"on a real graph is a denial of service against yourself")
	}

	allowed := map[string]bool{}
	for _, t := range opts.Types {
		allowed[t] = true
	}
	var inView map[string]bool
	if opts.Domain != "" {
		inView = map[string]bool{}
		_, rels := g.ont.View(opts.Domain)
		for _, r := range rels {
			inView[r.Name] = true
		}
	}

	var out []Path
	var walk func(cur contract.ID, acc []Edge, visited map[contract.ID]bool)
	walk = func(cur contract.ID, acc []Edge, visited map[contract.ID]bool) {
		if len(acc) > 0 && cur == to {
			out = append(out, buildPath(acc))
			return
		}
		if len(acc) >= opts.MaxDepth {
			return
		}
		ids := append([]contract.ID(nil), g.out[cur]...)
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, eid := range ids {
			e := g.edges[eid]
			if len(allowed) > 0 && !allowed[e.Type] {
				continue
			}
			if inView != nil && !inView[e.Type] {
				continue
			}
			if !opts.At.IsZero() && !e.HoldsAt(opts.At) {
				continue
			}
			if e.Qualification == Contested && !opts.IncludeContested {
				continue
			}
			if visited[e.To] {
				continue
			}
			visited[e.To] = true
			walk(e.To, append(acc, e), visited)
			delete(visited, e.To)
		}
	}
	walk(from, nil, map[contract.ID]bool{from: true})

	sort.Slice(out, func(i, j int) bool {
		if out[i].Qualification.Rank() != out[j].Qualification.Rank() {
			return out[i].Qualification.Rank() > out[j].Qualification.Rank()
		}
		if len(out[i].Edges) != len(out[j].Edges) {
			return len(out[i].Edges) < len(out[j].Edges)
		}
		return out[i].String() < out[j].String()
	})
	return out, nil
}

func buildPath(edges []Edge) Path {
	p := Path{Edges: append([]Edge(nil), edges...), Qualification: Verified}
	seen := map[string]bool{}
	for _, e := range edges {
		p.Qualification = Weaker(p.Qualification, e.Qualification)
		if e.Qualification == Contested {
			p.Contested = true
		}
		for _, r := range e.EvidenceRefs {
			if !seen[r] {
				seen[r] = true
				p.EvidenceRefs = append(p.EvidenceRefs, r)
			}
		}
	}
	sort.Strings(p.EvidenceRefs)
	return p
}

// Neighbours returns the edges leaving a node, filtered.
func (g *Graph) Neighbours(id contract.ID, opts Options) ([]Edge, error) {
	if _, err := g.Node(id); err != nil {
		return nil, err
	}
	var out []Edge
	for _, eid := range g.out[id] {
		e := g.edges[eid]
		if !opts.At.IsZero() && !e.HoldsAt(opts.At) {
			continue
		}
		if e.Qualification == Contested && !opts.IncludeContested {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// EdgesHoldingAt returns every edge in force at an instant. This is
// the temporal-truth query: "who operated this vessel in March 2024"
// is a different question from "who operates it".
func (g *Graph) EdgesHoldingAt(t time.Time) []Edge {
	var out []Edge
	for _, e := range g.edges {
		if e.HoldsAt(t) {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ContestedEdges returns every edge with counter-evidence.
func (g *Graph) ContestedEdges() []Edge {
	var out []Edge
	for _, e := range g.edges {
		if e.Qualification == Contested {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// UnqualifiedAssertions returns edges resting only on assertion.
//
// It exists so a case can be asked "what here is only somebody's word"
// without a reviewer reading every edge.
func (g *Graph) UnqualifiedAssertions() []Edge {
	var out []Edge
	for _, e := range g.edges {
		if e.Qualification == Asserted {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// EvidenceRefs collects every evidence reference in the graph.
func (g *Graph) EvidenceRefs() []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range g.nodes {
		for _, r := range n.Entity.EvidenceRefs() {
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	for _, e := range g.edges {
		for _, r := range e.EvidenceRefs {
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	sort.Strings(out)
	return out
}
