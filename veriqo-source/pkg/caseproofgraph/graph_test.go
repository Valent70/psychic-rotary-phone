package caseproofgraph

import (
	"errors"
	"strings"
	"testing"

	"veriqo/pkg/casefabric"
	"veriqo/pkg/disclosure/access"
	"veriqo/pkg/ontology"
	"veriqo/pkg/proof"
	"veriqo/pkg/qualification/reverseproof"
	"veriqo/pkg/qualification/state"
)

// --- Canonicality -----------------------------------------------------

// TestEveryNodeKindResolvesToACanonicalObjectType is what makes this a
// canonical graph rather than a private data structure with nice names.
// A node kind the rest of VERIQO cannot address is not canonical.
func TestEveryNodeKindResolvesToACanonicalObjectType(t *testing.T) {
	for _, k := range NodeKinds() {
		ty, ok := ObjectTypeFor(k)
		if !ok {
			t.Fatalf("node kind %s maps to no ontology object type", k)
		}
		if !ontology.IsKnownObjectType(ty) {
			t.Fatalf("node kind %s maps to %q, which is not a registered object type", k, ty)
		}
		if _, ok := ontology.ContractForType(ty); !ok {
			t.Fatalf("node kind %s maps to %q, which has no object contract", k, ty)
		}
	}
}

func TestTheVocabulariesAreClosed(t *testing.T) {
	g := mustGraph(t)
	if err := g.AddNode(Node{ID: "x", Kind: NodeKind("INVENTED"),
		Classification: Classification{RequiredRight: access.View}}); !errors.Is(err, ErrUnknownNodeKind) {
		t.Fatalf("expected ErrUnknownNodeKind, got %v", err)
	}
	if err := g.AddEdge("case:CASE-1", "case:CASE-1", EdgeKind("INVENTED")); !errors.Is(err, ErrUnknownEdgeKind) {
		t.Fatalf("expected ErrUnknownEdgeKind, got %v", err)
	}
	if len(EdgeKinds()) < 10 {
		t.Fatalf("the edge vocabulary looks incomplete: %d kinds", len(EdgeKinds()))
	}
}

// --- Hashing ----------------------------------------------------------

func TestSealAndVerify(t *testing.T) {
	g := buildFull(t)
	if g.RootHash == "" {
		t.Fatal("Build must seal the graph")
	}
	if err := VerifyGraph(g); err != nil {
		t.Fatalf("VerifyGraph: %v", err)
	}
}

// TestAnyAlterationBreaksTheRoot is the property the whole structure
// rests on: one root hash covering every node and every edge.
func TestAnyAlterationBreaksTheRoot(t *testing.T) {
	for _, tc := range []struct {
		name string
		mut  func(*Graph)
	}{
		{"node label", func(g *Graph) {
			n := g.nodes["case:CASE-1"]
			n.Label = "a different matter"
			g.nodes["case:CASE-1"] = n
		}},
		{"node attribute", func(g *Graph) {
			n := g.nodes["case:CASE-1"]
			n.Attributes["phase"] = "SOMETHING-ELSE"
			g.nodes["case:CASE-1"] = n
		}},
		{"node classification", func(g *Graph) {
			n := g.nodes["case:CASE-1"]
			n.Classification.Content = access.C5PrivilegedEnclave
			g.nodes["case:CASE-1"] = n
		}},
		{"edge kind", func(g *Graph) { g.edges[0].Kind = EdgeSupersededBy }},
		{"edge endpoint", func(g *Graph) { g.edges[0].To = "case:CASE-1" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := buildFull(t)
			tc.mut(g)
			if err := VerifyGraph(g); err == nil {
				t.Fatalf("altering the %s must break verification", tc.name)
			}
		})
	}
}

func TestAnUnsealedGraphFailsVerification(t *testing.T) {
	g := mustGraph(t)
	if err := VerifyGraph(g); !errors.Is(err, ErrNotSealed) {
		t.Fatalf("expected ErrNotSealed, got %v", err)
	}
}

// TestAGraphWithoutACaseCannotSeal: a proof graph with no case is a pile
// of assertions about nothing in particular.
func TestAGraphWithoutACaseCannotSeal(t *testing.T) {
	g, _ := New("CASE-1", "tenant-a")
	if err := g.AddNode(Node{ID: "claim:1", Kind: NodeClaim,
		Classification: Classification{RequiredRight: access.View}}); err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	if err := g.Seal(); !errors.Is(err, ErrNoCaseRoot) {
		t.Fatalf("expected ErrNoCaseRoot, got %v", err)
	}
}

// TestDanglingEdgesAreRefused: a graph referencing a node it does not
// contain cannot be verified by anybody holding only the graph.
func TestDanglingEdgesAreRefused(t *testing.T) {
	g := mustGraph(t)
	if err := g.AddEdge("case:CASE-1", "nowhere", EdgeHasClaim); !errors.Is(err, ErrDanglingEdge) {
		t.Fatalf("expected ErrDanglingEdge, got %v", err)
	}
	if err := g.AddEdge("nowhere", "case:CASE-1", EdgeHasClaim); !errors.Is(err, ErrDanglingEdge) {
		t.Fatalf("expected ErrDanglingEdge, got %v", err)
	}
}

func TestEveryNodeMustCarryAClassification(t *testing.T) {
	g := mustGraph(t)
	if err := g.AddNode(Node{ID: "unclassified", Kind: NodeClaim}); !errors.Is(err, ErrNoClassification) {
		t.Fatalf("expected ErrNoClassification, got %v", err)
	}
}

func TestDuplicateNodeIsRefused(t *testing.T) {
	g := mustGraph(t)
	err := g.AddNode(Node{ID: "case:CASE-1", Kind: NodeCase,
		Classification: Classification{RequiredRight: access.View}})
	if !errors.Is(err, ErrDuplicateNode) {
		t.Fatalf("expected ErrDuplicateNode, got %v", err)
	}
}

// --- Structure --------------------------------------------------------

// TestTheGraphHoldsTheWholeStructure walks the shape the architecture
// specified: case, claim, proof object, and the nine things behind it.
func TestTheGraphHoldsTheWholeStructure(t *testing.T) {
	g := buildFull(t)
	for _, k := range []NodeKind{
		NodeCase, NodeClaim, NodeProofObject, NodeEvidence, NodeTrust,
		NodeQualification, NodeIndependence, NodeMissingEvidence, NodeReverseProof,
		NodeNextBestEvidence, NodeFinding, NodeTimeline, NodeActor, NodeOutcome,
	} {
		if len(g.NodesOfKind(k)) == 0 {
			t.Fatalf("the graph holds no %s node", k)
		}
	}
}

// TestTheGraphReferencesEvidenceAndNeverCopiesIt is a disclosure control
// at the structural level: the graph is safe to hand around precisely
// because it holds hashes, not content.
func TestTheGraphReferencesEvidenceAndNeverCopiesIt(t *testing.T) {
	g := buildFull(t)
	for _, n := range g.NodesOfKind(NodeEvidence) {
		if n.ContentHash == "" {
			t.Fatalf("evidence node %q carries no content hash", n.ID)
		}
		for k, v := range n.Attributes {
			if len(v) > 200 {
				t.Fatalf("evidence node %q attribute %q looks like content, not a reference", n.ID, k)
			}
		}
	}
}

// TestAnInsufficientProofFoundsNoFindingNode: absence, not an empty node.
func TestAnInsufficientProofFoundsNoFindingNode(t *testing.T) {
	c, proofs := caseAndProofs(t, false)
	g, err := Build(c, proofs, 50)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(g.NodesOfKind(NodeFinding)) != 0 {
		t.Fatal("an insufficient proof object must found no finding node")
	}
	if len(g.NodesOfKind(NodeProofObject)) != 1 {
		t.Fatal("the proof object itself is still in the graph")
	}
}

// TestDecisionCarriesItsWholeLineage: a decision node that cannot be
// traced back is a decision nobody can check.
func TestDecisionCarriesItsWholeLineage(t *testing.T) {
	c, proofs := caseAndProofs(t, true)
	g, err := Build(c, proofs, 50)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	o := proofs["CL-1"]
	f, _ := proof.NewFinding(o, 20)
	a, _ := proof.Authorize(f, o, "partner-1", "partner", "policy-v1", "adopted", 30)
	d, _ := proof.Decide(a, "refer_to_tribunal", "package complete", map[string]string{"forum": "SIAC"}, 40)

	if err := AddDecision(g, d); err != nil {
		t.Fatalf("AddDecision: %v", err)
	}
	nodes := g.NodesOfKind(NodeDecision)
	if len(nodes) != 1 {
		t.Fatalf("expected one decision node, got %d", len(nodes))
	}
	for _, k := range []string{"authorized_hash", "finding_hash", "proof_hash", "authorizer"} {
		if nodes[0].Attributes[k] == "" {
			t.Fatalf("the decision node must carry %s", k)
		}
	}
	if nodes[0].Attributes["proof_hash"] != o.CanonicalHash {
		t.Fatal("the decision must trace back to the proof object")
	}
	if err := VerifyGraph(g); err != nil {
		t.Fatalf("the graph must re-verify after the decision: %v", err)
	}
}

// --- Rights-aware projection ------------------------------------------

// TestProjectionWithholdsEvidenceFromAMetadataOnlyRecipient is the
// property that makes the graph safe to share: a recipient gets a real
// subgraph, not a rendering with parts hidden.
func TestProjectionWithholdsEvidenceFromAMetadataOnlyRecipient(t *testing.T) {
	g := buildFull(t)
	limited := access.Grant{
		EvidenceVersionID: "EV-1-v1", RecipientID: "observer-1", RecipientRole: "observer",
		Procedural: access.P2ProcessVisible, Content: access.C2Redacted,
		Rights: []access.Right{access.View}, PolicyVersion: "policy-v1",
		Privilege: access.PrivilegeNotClaimed,
	}
	p, err := Project(g, limited, "observer-1", 60)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if len(p.Graph.NodesOfKind(NodeEvidence)) != 0 {
		t.Fatal("a C2 recipient must not receive C3 evidence nodes")
	}
	if len(p.Excluded) == 0 {
		t.Fatal("the withheld nodes must be reported")
	}
	if p.EdgesWithheld == 0 {
		t.Fatal("edges to withheld nodes must be dropped, not left dangling")
	}
	// The projection is itself a real, sealed, verifiable graph.
	if err := VerifyGraph(p.Graph); err != nil {
		t.Fatalf("the projection must verify on its own: %v", err)
	}
	if !strings.Contains(p.Summary(), "withheld") {
		t.Fatalf("the summary must disclose that something was withheld: %q", p.Summary())
	}
}

// TestProjectionNeverContainsWhatItWithholds is the difference between a
// projection and a redaction.
func TestProjectionNeverContainsWhatItWithholds(t *testing.T) {
	g := buildFull(t)
	limited := access.Grant{
		EvidenceVersionID: "EV-1-v1", RecipientID: "observer-1", RecipientRole: "observer",
		Procedural: access.P2ProcessVisible, Content: access.C2Redacted,
		Rights: []access.Right{access.View}, PolicyVersion: "policy-v1",
		Privilege: access.PrivilegeNotClaimed,
	}
	p, err := Project(g, limited, "observer-1", 60)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	for _, n := range p.Graph.Nodes() {
		if n.Kind == NodeEvidence {
			t.Fatal("a withheld node must not be present in any form")
		}
	}
	for _, e := range p.Graph.Edges() {
		if strings.HasPrefix(e.To, "evidence:") || strings.HasPrefix(e.From, "evidence:") {
			t.Fatalf("an edge to a withheld node leaks its existence: %+v", e)
		}
	}
}

// TestAFullyEntitledRecipientGetsTheWholeGraph proves the projection is
// not simply lossy.
func TestAFullyEntitledRecipientGetsTheWholeGraph(t *testing.T) {
	g := buildFull(t)
	full := access.Grant{
		EvidenceVersionID: "EV-1-v1", RecipientID: "tribunal-1", RecipientRole: "authority",
		Procedural: access.P5AuthorityVisible, Content: access.C4Export,
		Rights: []access.Right{access.View}, PolicyVersion: "policy-v1",
		Privilege: access.PrivilegeNotClaimed,
	}
	p, err := Project(g, full, "tribunal-1", 60)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if len(p.Excluded) != 0 || p.EdgesWithheld != 0 {
		t.Fatalf("a fully entitled recipient should get everything, withheld: %+v", p.Excluded)
	}
	if len(p.Graph.Nodes()) != len(g.Nodes()) {
		t.Fatalf("expected %d nodes, got %d", len(g.Nodes()), len(p.Graph.Nodes()))
	}
	if !strings.Contains(p.Summary(), "nothing was withheld") {
		t.Fatalf("unexpected summary: %q", p.Summary())
	}
}

// TestAnUnverifiedGraphIsNotProjected: projecting a tampered graph would
// launder the tampering into a fresh, correctly-sealed subgraph.
func TestAnUnverifiedGraphIsNotProjected(t *testing.T) {
	g := buildFull(t)
	n := g.nodes["case:CASE-1"]
	n.Label = "tampered"
	g.nodes["case:CASE-1"] = n

	if _, err := Project(g, access.Grant{
		EvidenceVersionID: "EV-1-v1", RecipientID: "x",
		Procedural: access.P5AuthorityVisible, Content: access.C4Export,
		Rights: []access.Right{access.View}, PolicyVersion: "p", Privilege: access.PrivilegeNotClaimed,
	}, "x", 60); err == nil {
		t.Fatal("a tampered graph must not be projectable")
	}
}

// TestAccessorsReturnCopies stops a holder editing the graph through a
// returned value.
func TestAccessorsReturnCopies(t *testing.T) {
	g := buildFull(t)
	g.Nodes()[0].Attributes["phase"] = "forged"
	if g.Nodes()[0].Attributes["phase"] == "forged" {
		t.Fatal("Nodes must return copies")
	}
	g.Edges()[0].Kind = EdgeSupersededBy
	if g.Edges()[0].Kind == EdgeSupersededBy {
		t.Fatal("Edges must return copies")
	}
}

// --- fixtures ---------------------------------------------------------

func mustGraph(t *testing.T) *Graph {
	t.Helper()
	g, err := New("CASE-1", "tenant-a")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := g.AddNode(Node{ID: "case:CASE-1", Kind: NodeCase, Label: "matter",
		Classification: Classification{Procedural: access.P1Metadata, Content: access.C1Existence,
			RequiredRight: access.View}}); err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	return g
}

func buildFull(t *testing.T) *Graph {
	t.Helper()
	c, proofs := caseAndProofs(t, true)
	g, err := Build(c, proofs, 50)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return g
}

func caseAndProofs(t *testing.T, sufficient bool) (*casefabric.Case, map[string]proof.Object) {
	t.Helper()
	o := sealProof(t, sufficient)

	c, err := casefabric.Open(casefabric.Identity{
		CaseID: "CASE-1", TenantID: "tenant-a", Domain: casefabric.DomainInsurance,
	}, "analyst-1", 1)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	steps := []func() error{
		func() error {
			return c.SetScope(o.Scope, o.Jurisdiction, o.TimeWindow, casefabric.Mission{
				Statement: "establish the cause", Intent: "quantify", SetBy: "analyst-1", SetAtTick: 2},
				"analyst-1", 2)
		},
		func() error { return c.AddEvidence(o.EvidenceSet, "analyst-1", 3) },
		func() error {
			return c.AddHypothesis(casefabric.Hypothesis{ID: "H-1", Description: "in transit", Tested: true}, "analyst-1", 4)
		},
		func() error {
			return c.RegisterClaim(casefabric.Claim{ID: "CL-1", Material: true, Proposition: o.Proposition}, "analyst-1", 5)
		},
		func() error { return c.BeginQualification("analyst-1", 6) },
		func() error { return c.AttachProof("CL-1", o, "analyst-1", 7) },
	}
	for _, s := range steps {
		if err := s(); err != nil {
			t.Fatalf("case step: %v", err)
		}
	}
	if sufficient {
		if _, err := c.Resolve("evidence_package_delivered", "established", "partner-1", 8); err != nil {
			t.Fatalf("Resolve: %v", err)
		}
	}
	return c, map[string]proof.Object{"CL-1": o}
}

func sealProof(t *testing.T, sufficient bool) proof.Object {
	t.Helper()
	claim := reverseproof.Claim{ID: "CL-1", Description: "contamination before loading",
		Conditions: []reverseproof.Condition{{ID: "cond-1", Description: "pre-load contamination"}}}
	reqs := []reverseproof.Requirement{{ID: "R-1", ConditionID: "cond-1", Description: "pre-load sample",
		ExpectedIfTrue: "contaminant present", ContradictsIfShows: "clean sample",
		Status: reverseproof.Obtained, DiagnosticValue: 0.9}}
	alts := []reverseproof.AlternativeHypothesis{{ID: "A-1", Description: "in transit", Tested: true}}
	rs, err := reverseproof.Build(claim, reqs, alts, 10)
	if err != nil {
		t.Fatalf("reverseproof.Build: %v", err)
	}
	q, err := state.New("CL-1", state.Supported, "policy-v1", "qualified", nil, 10)
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}
	o := proof.Object{
		Proposition:  proof.Proposition{ID: "P-1", Statement: "the cargo was contaminated before loading"},
		Scope:        proof.Scope{CaseID: "CASE-1", Matter: "cargo damage"},
		Jurisdiction: proof.Jurisdiction{Code: "SG", Forum: "SIAC"},
		TimeWindow:   proof.TimeWindow{FromTick: 1, ToTick: 500},
		EvidenceSet: []proof.EvidenceRef{
			{EvidenceID: "E-1", EvidenceVersionID: "EV-1-v1", SHA256: "abc", SourceID: "lab-a"},
		},
		Quality:      proof.Quality{Assessed: true, Grade: "primary"},
		ReverseProof: rs, ReverseProofGap: reverseproof.Analyze(rs, map[string]bool{"cond-1": true}),
		MissingEvidence: []proof.MissingEvidence{
			{ConditionID: "cond-1", Description: "terminal CCTV", Obtainable: false, Reason: "retention expired"},
		},
		Contradictions: []proof.Contradiction{
			{ID: "X-1", Description: "a typo in a covering letter", Material: false},
		},
		NextBestEvidence: []string{"independent re-assay"},
		Trust:            proof.TrustAssessment{Assessed: true, EffectiveSourceCount: 2},
		Qualification:    q,
		Authority:        proof.Authority{AuthorityID: "analyst-1", Role: "analyst", PolicyVersion: "policy-v1"},
		Limitations:      []string{"covers the sampled parcel only"},
		Provenance:       proof.Provenance{GeneratedBy: "pipeline-1", GeneratedAtTick: 10, PipelineVersion: "fref-v1"},
	}
	if !sufficient {
		o.Trust.Assessed = false
	}
	sealed, err := proof.Seal(o)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	return sealed
}
