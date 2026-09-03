package caseproofgraph

import (
	"fmt"
	"sort"

	"veriqo/pkg/disclosure/access"
)

// Rights-aware projection.
//
// A recipient does not get the graph. They get the subgraph their grant
// permits — a real graph, sealed and verifiable on its own, not a
// rendering with parts hidden. The difference matters: a redacted view
// still contains what it hides, and anything that contains it can leak
// it. A projection never held the excluded nodes at all.
//
// Edges follow their endpoints. An edge whose other end was excluded
// does not appear, because an edge to a node you cannot see tells you
// that node exists and what relation it bears — which is often the whole
// disclosure.

// Projection is what one recipient may see.
type Projection struct {
	Graph *Graph
	// Excluded lists the node ids withheld, with the reason. The count
	// is disclosed even where the content is not: a recipient is
	// entitled to know that something was withheld, and on what basis.
	Excluded []Exclusion
	// EdgesWithheld is how many edges dropped because an endpoint was.
	EdgesWithheld int
}

// Exclusion is one withheld node.
type Exclusion struct {
	NodeID string
	Kind   NodeKind
	Reason string
}

// Project builds the subgraph a grant permits.
//
// Every node is put to pkg/disclosure/access, which is the single
// authority on disclosure. This package supplies the classification and
// assembles the result; it does not decide.
func Project(g *Graph, grant access.Grant, recipientID string, tick uint64) (Projection, error) {
	if err := VerifyGraph(g); err != nil {
		return Projection{}, fmt.Errorf("caseproofgraph: refusing to project an unverified graph: %w", err)
	}

	out, err := New(g.CaseID, g.TenantID)
	if err != nil {
		return Projection{}, err
	}
	p := Projection{}
	kept := map[string]bool{}

	for _, n := range g.Nodes() {
		allowed, reason := permits(grant, n, recipientID, tick)
		if !allowed {
			p.Excluded = append(p.Excluded, Exclusion{NodeID: n.ID, Kind: n.Kind, Reason: reason})
			continue
		}
		if err := out.AddNode(n); err != nil {
			return Projection{}, err
		}
		kept[n.ID] = true
	}

	for _, e := range g.Edges() {
		if !kept[e.From] || !kept[e.To] {
			p.EdgesWithheld++
			continue
		}
		if err := out.AddEdge(e.From, e.To, e.Kind); err != nil {
			return Projection{}, err
		}
	}

	// A projection with no CASE node cannot seal, which is correct: a
	// recipient who may not see the case may not have a case graph.
	if err := out.Seal(); err != nil {
		return Projection{}, fmt.Errorf("caseproofgraph: the projection is not a case graph: %w", err)
	}
	sort.Slice(p.Excluded, func(i, j int) bool { return p.Excluded[i].NodeID < p.Excluded[j].NodeID })
	p.Graph = out
	return p, nil
}

// permits decides whether this recipient may see this node.
//
// Two questions, kept apart because they are different questions.
//
// A node standing for a disclosable evidence version is an evidence
// disclosure, and pkg/disclosure/access is the sole authority on those.
// The grant supplied here is a template — recipient, levels, rights,
// privilege, protective order, policy — and permits specializes it to
// the node's own version before asking, because a grant names a version
// and this one grant covers a whole graph.
//
// Every other node is structural: a claim, a verdict, an actor. There is
// no evidence version to grant against, so the question is the two
// disclosure levels and the right, checked directly. Routing those
// through access.Evaluate would mean inventing a fake evidence version
// id, and a fake id in a disclosure decision is worse than no decision.
func permits(grant access.Grant, n Node, recipientID string, tick uint64) (bool, string) {
	if n.EvidenceVersionID != "" {
		perNode := grant
		perNode.EvidenceVersionID = n.EvidenceVersionID
		d, err := access.Evaluate(perNode, access.Request{
			EvidenceVersionID: n.EvidenceVersionID, RecipientID: recipientID,
			Right: n.Classification.RequiredRight, Tick: tick,
		})
		if err != nil {
			return false, err.Error()
		}
		if !d.Allowed {
			return false, d.Reason
		}
	} else if !hasRight(grant.Rights, n.Classification.RequiredRight) {
		return false, fmt.Sprintf("right %s was not granted; access does not imply use",
			n.Classification.RequiredRight)
	}

	// Both dimensions, for every node. The two levels never collapse
	// into one, here or anywhere else in this codebase.
	if grant.Content < n.Classification.Content {
		return false, fmt.Sprintf("content level %s below the node's required %s",
			grant.Content, n.Classification.Content)
	}
	if grant.Procedural < n.Classification.Procedural {
		return false, fmt.Sprintf("procedural level %s below the node's required %s",
			grant.Procedural, n.Classification.Procedural)
	}
	return true, ""
}

func hasRight(granted []access.Right, want access.Right) bool {
	for _, r := range granted {
		if r == want {
			return true
		}
	}
	return false
}

// Summary describes a projection for the recipient, disclosing what was
// withheld without disclosing it.
func (p Projection) Summary() string {
	if len(p.Excluded) == 0 && p.EdgesWithheld == 0 {
		return "Complete graph: nothing was withheld."
	}
	byKind := map[NodeKind]int{}
	for _, e := range p.Excluded {
		byKind[e.Kind]++
	}
	kinds := make([]string, 0, len(byKind))
	for k, n := range byKind {
		kinds = append(kinds, fmt.Sprintf("%d %s", n, k))
	}
	sort.Strings(kinds)
	return fmt.Sprintf("Partial graph: %d node(s) withheld (%v) and %d edge(s) dropped because an endpoint was withheld.",
		len(p.Excluded), kinds, p.EdgesWithheld)
}
