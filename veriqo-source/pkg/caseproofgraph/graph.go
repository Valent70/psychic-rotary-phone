// Package caseproofgraph is the VERIQO Case Proof Graph: one canonical,
// hashed, rights-aware object holding everything behind a case.
//
// The five fabrics each answer part of a question. TECP holds the
// evidence and who may touch it, EQF decides what it supports, the
// Intelligent Fabric proposes explanations, the Case Resolution Fabric
// carries the case, and FREF runs both directions over it. Each is
// coherent on its own, and until this package none of them produced a
// single thing you could hand somebody.
//
// The graph is that thing:
//
//	CASE
//	 ├── CLAIM
//	 │     └── PROOF OBJECT
//	 │            ├── Evidence          ├── Missing Evidence
//	 │            ├── Trust             ├── Reverse Proof
//	 │            ├── Qualification     ├── Next Best Evidence
//	 │            ├── Contradiction     └── Finding
//	 │            └── Independence
//	 ├── TIMELINE   ├── ENTITIES   ├── DECISIONS
//	 ├── ACTORS     ├── EVENTS     └── OUTCOME
//
// Every node and edge is canonical, hashed, auditable, replayable and
// rights-aware. Those are not adjectives; each is a property the code
// enforces:
//
//   - canonical: node and edge kinds come from a closed vocabulary, and
//     every node id resolves to a registered ontology object type.
//   - hashed: each node hashes its own content; each edge hashes its
//     endpoints; the graph root hashes the sorted node and edge hashes,
//     so any change anywhere changes the root.
//   - auditable: the root is emitted to the one audit ledger.
//   - replayable: VerifyGraph recomputes every hash from the content.
//   - rights-aware: every node carries a disclosure classification, and
//     Project returns only what a given grant permits — the graph a
//     recipient sees is a real subgraph, not a filtered rendering.
//
// This package composes; it decides nothing. Sufficiency belongs to
// pkg/proof, phases to pkg/casefabric, disclosure to
// pkg/disclosure/access. A second opinion computed here would be the
// duplicate authority the architecture forbids.
package caseproofgraph

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"veriqo/pkg/canonical/jcs"
	"veriqo/pkg/disclosure/access"
	"veriqo/pkg/ontology"
)

// NodeKind is the closed vocabulary of graph nodes.
type NodeKind string

const (
	NodeCase             NodeKind = "CASE"
	NodeClaim            NodeKind = "CLAIM"
	NodeProofObject      NodeKind = "PROOF_OBJECT"
	NodeEvidence         NodeKind = "EVIDENCE"
	NodeTrust            NodeKind = "TRUST"
	NodeQualification    NodeKind = "QUALIFICATION"
	NodeContradiction    NodeKind = "CONTRADICTION"
	NodeIndependence     NodeKind = "INDEPENDENCE"
	NodeMissingEvidence  NodeKind = "MISSING_EVIDENCE"
	NodeReverseProof     NodeKind = "REVERSE_PROOF"
	NodeNextBestEvidence NodeKind = "NEXT_BEST_EVIDENCE"
	NodeFinding          NodeKind = "FINDING"
	NodeTimeline         NodeKind = "TIMELINE"
	NodeActor            NodeKind = "ACTOR"
	NodeEntity           NodeKind = "ENTITY"
	NodeEvent            NodeKind = "EVENT"
	NodeDecision         NodeKind = "DECISION"
	NodeOutcome          NodeKind = "OUTCOME"
)

// nodeObjectType maps each node kind onto the canonical ontology type it
// instantiates.
//
// This is what makes the graph canonical rather than a private data
// structure that happens to have nice names: a node kind with no
// ontology type would be a concept the rest of VERIQO cannot address.
var nodeObjectType = map[NodeKind]ontology.ObjectType{
	NodeCase:             ontology.ObjectCase,
	NodeClaim:            ontology.ObjectClaim,
	NodeProofObject:      ontology.ObjectProofObject,
	NodeEvidence:         ontology.ObjectEvidenceVersion,
	NodeTrust:            ontology.ObjectQualification,
	NodeQualification:    ontology.ObjectQualification,
	NodeContradiction:    ontology.ObjectContradiction,
	NodeIndependence:     ontology.ObjectQualification,
	NodeMissingEvidence:  ontology.ObjectProofObligation,
	NodeReverseProof:     ontology.ObjectProofObligation,
	NodeNextBestEvidence: ontology.ObjectNextBestEvidence,
	NodeFinding:          ontology.ObjectFinding,
	NodeTimeline:         ontology.ObjectTimeline,
	NodeActor:            ontology.ObjectParty,
	NodeEntity:           ontology.ObjectEvidence,
	NodeEvent:            ontology.ObjectEvent,
	NodeDecision:         ontology.ObjectDecision,
	NodeOutcome:          ontology.ObjectResolution,
}

// NodeKinds returns the closed vocabulary, in graph order.
func NodeKinds() []NodeKind {
	return []NodeKind{
		NodeCase, NodeClaim, NodeProofObject, NodeEvidence, NodeTrust,
		NodeQualification, NodeContradiction, NodeIndependence, NodeMissingEvidence,
		NodeReverseProof, NodeNextBestEvidence, NodeFinding, NodeTimeline,
		NodeActor, NodeEntity, NodeEvent, NodeDecision, NodeOutcome,
	}
}

// ObjectTypeFor returns the canonical ontology type a node kind
// instantiates.
func ObjectTypeFor(k NodeKind) (ontology.ObjectType, bool) {
	t, ok := nodeObjectType[k]
	return t, ok
}

// EdgeKind is the closed vocabulary of graph edges.
type EdgeKind string

const (
	EdgeHasClaim       EdgeKind = "HAS_CLAIM"
	EdgeProvenBy       EdgeKind = "PROVEN_BY"
	EdgeRestsOn        EdgeKind = "RESTS_ON"
	EdgeAssessedBy     EdgeKind = "ASSESSED_BY"
	EdgeQualifiedBy    EdgeKind = "QUALIFIED_BY"
	EdgeContradictedBy EdgeKind = "CONTRADICTED_BY"
	EdgeMissing        EdgeKind = "MISSING"
	EdgeObligatedBy    EdgeKind = "OBLIGATED_BY"
	EdgeSuggests       EdgeKind = "SUGGESTS"
	EdgeFounds         EdgeKind = "FOUNDS"
	EdgeRecordedIn     EdgeKind = "RECORDED_IN"
	EdgeInvolves       EdgeKind = "INVOLVES"
	EdgeConcerns       EdgeKind = "CONCERNS"
	EdgeAuthorizedBy   EdgeKind = "AUTHORIZED_BY"
	EdgeResolvedAs     EdgeKind = "RESOLVED_AS"
	EdgeSupersededBy   EdgeKind = "SUPERSEDED_BY"
)

var knownEdgeKinds = map[EdgeKind]bool{
	EdgeHasClaim: true, EdgeProvenBy: true, EdgeRestsOn: true, EdgeAssessedBy: true,
	EdgeQualifiedBy: true, EdgeContradictedBy: true, EdgeMissing: true,
	EdgeObligatedBy: true, EdgeSuggests: true, EdgeFounds: true, EdgeRecordedIn: true,
	EdgeInvolves: true, EdgeConcerns: true, EdgeAuthorizedBy: true,
	EdgeResolvedAs: true, EdgeSupersededBy: true,
}

// EdgeKinds returns the closed edge vocabulary, sorted.
func EdgeKinds() []EdgeKind {
	out := make([]EdgeKind, 0, len(knownEdgeKinds))
	for k := range knownEdgeKinds {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

var (
	ErrUnknownNodeKind  = errors.New("caseproofgraph: unknown node kind")
	ErrUnknownEdgeKind  = errors.New("caseproofgraph: unknown edge kind")
	ErrNoNodeID         = errors.New("caseproofgraph: a node requires an id")
	ErrDuplicateNode    = errors.New("caseproofgraph: the node is already in the graph")
	ErrDanglingEdge     = errors.New("caseproofgraph: the edge names a node that is not in the graph")
	ErrNoCaseRoot       = errors.New("caseproofgraph: a graph requires exactly one CASE node")
	ErrHashMismatch     = errors.New("caseproofgraph: the graph has been altered since sealing")
	ErrNotSealed        = errors.New("caseproofgraph: the graph has not been sealed")
	ErrNoClassification = errors.New("caseproofgraph: every node must carry a disclosure classification")
)

// Classification is a node's disclosure standing, in the two dimensions
// pkg/disclosure/access defines. Two integers, never collapsed into one.
type Classification struct {
	Procedural access.Procedural
	Content    access.Content
	// RequiredRight is the right a recipient must hold to see this node
	// at all.
	RequiredRight access.Right
}

// Node is one vertex.
type Node struct {
	ID   string
	Kind NodeKind
	// Label is human-readable and carries no evidence content.
	Label string
	// ContentHash pins whatever the node stands for — an evidence
	// version's content hash, a proof object's canonical hash, a
	// decision's hash. The graph does not copy content; it references it.
	ContentHash string
	// EvidenceVersionID is set on nodes that stand for a disclosable
	// evidence version. It is what a disclosure grant is issued against,
	// and it is deliberately separate from ContentHash: a grant names a
	// version, not a hash of bytes.
	EvidenceVersionID string
	// Attributes are canonical, sorted key/value pairs. They carry
	// verdicts and identifiers, never evidence bytes.
	Attributes map[string]string
	// Classification is the node's disclosure standing.
	Classification Classification
	// NodeHash covers everything above.
	NodeHash string
}

// Edge is one directed relationship.
type Edge struct {
	From     string
	To       string
	Kind     EdgeKind
	EdgeHash string
}

// Graph is the whole case, as one object.
type Graph struct {
	CaseID   string
	TenantID string

	nodes     map[string]Node
	nodeOrder []string
	edges     []Edge

	// RootHash covers every node hash and every edge hash. It is the one
	// value that identifies this graph.
	RootHash string
}

// New starts an empty graph.
func New(caseID, tenantID string) (*Graph, error) {
	if strings.TrimSpace(caseID) == "" || strings.TrimSpace(tenantID) == "" {
		return nil, errors.New("caseproofgraph: a graph requires a case id and a tenant")
	}
	return &Graph{CaseID: caseID, TenantID: tenantID, nodes: map[string]Node{}}, nil
}

// AddNode adds a vertex and computes its hash.
func (g *Graph) AddNode(n Node) error {
	if strings.TrimSpace(n.ID) == "" {
		return ErrNoNodeID
	}
	if _, ok := nodeObjectType[n.Kind]; !ok {
		return fmt.Errorf("%w: %q", ErrUnknownNodeKind, n.Kind)
	}
	if _, exists := g.nodes[n.ID]; exists {
		return fmt.Errorf("%w: %q", ErrDuplicateNode, n.ID)
	}
	if n.Classification.RequiredRight == "" {
		return fmt.Errorf("%w: node %q", ErrNoClassification, n.ID)
	}

	attrs := make(map[string]string, len(n.Attributes))
	for k, v := range n.Attributes {
		attrs[k] = v
	}
	n.Attributes = attrs
	n.NodeHash = hashNode(n)

	g.nodes[n.ID] = n
	g.nodeOrder = append(g.nodeOrder, n.ID)
	g.RootHash = ""
	return nil
}

// AddEdge adds a directed relationship between two nodes already in the
// graph. A dangling edge is refused: a graph that references a node it
// does not contain cannot be verified by anybody holding only the graph.
func (g *Graph) AddEdge(from, to string, kind EdgeKind) error {
	if !knownEdgeKinds[kind] {
		return fmt.Errorf("%w: %q", ErrUnknownEdgeKind, kind)
	}
	if _, ok := g.nodes[from]; !ok {
		return fmt.Errorf("%w: %q", ErrDanglingEdge, from)
	}
	if _, ok := g.nodes[to]; !ok {
		return fmt.Errorf("%w: %q", ErrDanglingEdge, to)
	}
	e := Edge{From: from, To: to, Kind: kind}
	e.EdgeHash = jcs.MustHash(map[string]any{"from": from, "to": to, "kind": string(kind)})
	g.edges = append(g.edges, e)
	g.RootHash = ""
	return nil
}

func hashNode(n Node) string {
	keys := make([]string, 0, len(n.Attributes))
	for k := range n.Attributes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	attrs := make(map[string]any, len(keys))
	for _, k := range keys {
		attrs[k] = n.Attributes[k]
	}
	return jcs.MustHash(map[string]any{
		"id": n.ID, "kind": string(n.Kind), "label": n.Label,
		"content_hash": n.ContentHash, "evidence_version_id": n.EvidenceVersionID,
		"attributes": attrs,
		"procedural": int(n.Classification.Procedural),
		"content":    int(n.Classification.Content),
		"right":      string(n.Classification.RequiredRight),
	})
}

// Seal computes the root hash over every node and edge.
//
// It requires exactly one CASE node: a proof graph without a case is a
// pile of assertions about nothing in particular.
func (g *Graph) Seal() error {
	caseNodes := 0
	for _, id := range g.nodeOrder {
		if g.nodes[id].Kind == NodeCase {
			caseNodes++
		}
	}
	if caseNodes != 1 {
		return fmt.Errorf("%w: found %d", ErrNoCaseRoot, caseNodes)
	}

	nodeHashes := make([]string, 0, len(g.nodeOrder))
	for _, id := range g.nodeOrder {
		nodeHashes = append(nodeHashes, g.nodes[id].NodeHash)
	}
	sort.Strings(nodeHashes)

	edgeHashes := make([]string, 0, len(g.edges))
	for _, e := range g.edges {
		edgeHashes = append(edgeHashes, e.EdgeHash)
	}
	sort.Strings(edgeHashes)

	g.RootHash = jcs.MustHash(map[string]any{
		"case_id": g.CaseID, "tenant_id": g.TenantID,
		"nodes": toAny(nodeHashes), "edges": toAny(edgeHashes),
	})
	return nil
}

// VerifyGraph recomputes every node hash, every edge hash and the root.
// Any alteration anywhere breaks it.
func VerifyGraph(g *Graph) error {
	if strings.TrimSpace(g.RootHash) == "" {
		return ErrNotSealed
	}
	claimed := g.RootHash

	for _, id := range g.nodeOrder {
		n := g.nodes[id]
		if want := hashNode(n); want != n.NodeHash {
			return fmt.Errorf("%w: node %q", ErrHashMismatch, id)
		}
	}
	for i, e := range g.edges {
		want := jcs.MustHash(map[string]any{"from": e.From, "to": e.To, "kind": string(e.Kind)})
		if want != e.EdgeHash {
			return fmt.Errorf("%w: edge %d", ErrHashMismatch, i)
		}
	}
	if err := g.Seal(); err != nil {
		return err
	}
	if g.RootHash != claimed {
		g.RootHash = claimed
		return ErrHashMismatch
	}
	return nil
}

// Nodes returns a copy of every node, in insertion order.
func (g *Graph) Nodes() []Node {
	out := make([]Node, 0, len(g.nodeOrder))
	for _, id := range g.nodeOrder {
		n := g.nodes[id]
		attrs := make(map[string]string, len(n.Attributes))
		for k, v := range n.Attributes {
			attrs[k] = v
		}
		n.Attributes = attrs
		out = append(out, n)
	}
	return out
}

// Edges returns a copy of every edge.
func (g *Graph) Edges() []Edge { return append([]Edge(nil), g.edges...) }

// NodesOfKind returns the nodes of one kind.
func (g *Graph) NodesOfKind(k NodeKind) []Node {
	var out []Node
	for _, n := range g.Nodes() {
		if n.Kind == k {
			out = append(out, n)
		}
	}
	return out
}

func toAny(s []string) []any {
	a := make([]any, 0, len(s))
	for _, v := range s {
		a = append(a, v)
	}
	return a
}
