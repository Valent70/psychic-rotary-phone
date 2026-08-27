package traceability_test

import (
	"errors"
	"testing"

	"veriqo/pkg/traceability"
)

func newNode(id string, kind traceability.NodeKind, tags ...string) traceability.TraceNode {
	return traceability.TraceNode{
		ID:    id,
		Kind:  kind,
		Title: "Title: " + id,
		Tags:  tags,
	}
}

func link(from, to string, kind traceability.LinkKind) traceability.TraceLink {
	return traceability.TraceLink{From: from, To: to, Kind: kind}
}

func buildFullChain(t *testing.T) *traceability.Graph {
	t.Helper()
	g := traceability.NewGraph()
	nodes := []traceability.TraceNode{
		newNode("req-001", traceability.KindRequirement, "vep-001"),
		newNode("adr-001", traceability.KindADR),
		newNode("risk-001", traceability.KindRisk),
		newNode("pol-001", traceability.KindPolicy),
		newNode("inv-001", traceability.KindInvariant),
		newNode("alg-001", traceability.KindAlgorithm),
		newNode("impl-001", traceability.KindImplementation),
		newNode("test-001", traceability.KindTest),
		newNode("replay-001", traceability.KindReplay),
		newNode("ev-001", traceability.KindEvidence),
		newNode("artifact-001", traceability.KindArtifact),
		newNode("commit-001", traceability.KindCommit),
		newNode("release-001", traceability.KindRelease),
	}
	for _, n := range nodes {
		if err := g.AddNode(n); err != nil {
			t.Fatalf("AddNode %q: %v", n.ID, err)
		}
	}
	links := []traceability.TraceLink{
		link("req-001", "adr-001", traceability.LinkRefines),
		link("adr-001", "risk-001", traceability.LinkDerivedFrom),
		link("risk-001", "pol-001", traceability.LinkMitigates),
		link("pol-001", "inv-001", traceability.LinkSatisfies),
		link("inv-001", "alg-001", traceability.LinkImplements),
		link("alg-001", "impl-001", traceability.LinkImplements),
		link("impl-001", "test-001", traceability.LinkVerifies),
		link("test-001", "replay-001", traceability.LinkReplays),
		link("replay-001", "ev-001", traceability.LinkProduces),
		link("ev-001", "artifact-001", traceability.LinkProduces),
		link("artifact-001", "commit-001", traceability.LinkProduces),
		link("commit-001", "release-001", traceability.LinkProduces),
	}
	for _, l := range links {
		if err := g.AddLink(l); err != nil {
			t.Fatalf("AddLink %q→%q: %v", l.From, l.To, err)
		}
	}
	return g
}

func TestGraph_AddAndGetNode(t *testing.T) {
	g := traceability.NewGraph()
	n := newNode("req-1", traceability.KindRequirement)
	if err := g.AddNode(n); err != nil {
		t.Fatal(err)
	}
	got, err := g.GetNode("req-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != traceability.KindRequirement {
		t.Errorf("expected requirement kind, got %s", got.Kind)
	}
}

func TestGraph_DuplicateNode(t *testing.T) {
	g := traceability.NewGraph()
	n := newNode("dup", traceability.KindTest)
	g.AddNode(n)
	if err := g.AddNode(n); !errors.Is(err, traceability.ErrNodeExists) {
		t.Errorf("expected ErrNodeExists, got %v", err)
	}
}

func TestGraph_AddLink_MissingNode(t *testing.T) {
	g := traceability.NewGraph()
	g.AddNode(newNode("a", traceability.KindRequirement))
	err := g.AddLink(link("a", "missing", traceability.LinkImplements))
	if !errors.Is(err, traceability.ErrNodeNotFound) {
		t.Errorf("expected ErrNodeNotFound, got %v", err)
	}
}

func TestGraph_QueryPath_FullChain(t *testing.T) {
	g := buildFullChain(t)
	path, err := g.QueryPath("req-001", "release-001")
	if err != nil {
		t.Fatalf("QueryPath: %v", err)
	}
	if len(path.Nodes) == 0 {
		t.Error("expected non-empty path")
	}
	if path.Nodes[0].ID != "req-001" {
		t.Errorf("path should start at req-001, got %s", path.Nodes[0].ID)
	}
	last := path.Nodes[len(path.Nodes)-1]
	if last.ID != "release-001" {
		t.Errorf("path should end at release-001, got %s", last.ID)
	}
}

func TestGraph_QueryPath_NoPath(t *testing.T) {
	g := traceability.NewGraph()
	g.AddNode(newNode("a", traceability.KindRequirement))
	g.AddNode(newNode("b", traceability.KindRelease))
	_, err := g.QueryPath("a", "b")
	if err == nil {
		t.Error("expected error for disconnected nodes")
	}
}

func TestGraph_QueryByKind(t *testing.T) {
	g := buildFullChain(t)
	reqs := g.QueryByKind(traceability.KindRequirement)
	if len(reqs) != 1 {
		t.Errorf("expected 1 requirement, got %d", len(reqs))
	}
	tests := g.QueryByKind(traceability.KindTest)
	if len(tests) != 1 {
		t.Errorf("expected 1 test, got %d", len(tests))
	}
}

func TestGraph_QueryByTag(t *testing.T) {
	g := buildFullChain(t)
	tagged := g.QueryByTag("vep-001")
	if len(tagged) != 1 || tagged[0].ID != "req-001" {
		t.Errorf("expected req-001 tagged with vep-001, got %v", tagged)
	}
}

func TestGraph_UpdateNode(t *testing.T) {
	g := traceability.NewGraph()
	g.AddNode(newNode("n", traceability.KindTest))
	if err := g.UpdateNode("n", func(n *traceability.TraceNode) {
		n.Status = traceability.StatusVerified
	}); err != nil {
		t.Fatal(err)
	}
	n, _ := g.GetNode("n")
	if n.Status != traceability.StatusVerified {
		t.Errorf("expected verified status, got %s", n.Status)
	}
}

func TestGraph_NodeHash_Deterministic(t *testing.T) {
	n := newNode("deterministic-node", traceability.KindAlgorithm, "tag1", "tag2")
	g1 := traceability.NewGraph()
	g2 := traceability.NewGraph()
	g1.AddNode(n)
	g2.AddNode(n)
	a, _ := g1.GetNode("deterministic-node")
	b, _ := g2.GetNode("deterministic-node")
	if a.Hash != b.Hash {
		t.Errorf("non-deterministic node hash: %s vs %s", a.Hash, b.Hash)
	}
}

func TestGraph_CoverageReport_AllLinked(t *testing.T) {
	g := buildFullChain(t)
	report := g.CoverageReport()
	if report.TotalRequirements != 1 {
		t.Errorf("expected 1 requirement, got %d", report.TotalRequirements)
	}
	if report.TracedToTest != 1 {
		t.Errorf("expected requirement traced to test, got %d", report.TracedToTest)
	}
	if report.TracedToEvidence != 1 {
		t.Errorf("expected requirement traced to evidence, got %d", report.TracedToEvidence)
	}
}

func TestGraph_CoverageReport_WithGap(t *testing.T) {
	g := traceability.NewGraph()
	g.AddNode(newNode("req-x", traceability.KindRequirement))
	g.AddNode(newNode("impl-x", traceability.KindImplementation))
	// Deliberately NOT linking to test or evidence.
	g.AddLink(link("req-x", "impl-x", traceability.LinkImplements))

	report := g.CoverageReport()
	if report.TotalRequirements != 1 {
		t.Fatalf("expected 1 requirement, got %d", report.TotalRequirements)
	}
	if len(report.Gaps) == 0 {
		t.Error("expected gap for requirement not traced to test/evidence")
	}
}

func TestGraph_ForwardBackwardLinks(t *testing.T) {
	g := buildFullChain(t)
	fwd := g.ForwardLinks("req-001")
	if len(fwd) == 0 {
		t.Error("expected forward links from req-001")
	}
	bwd := g.BackwardLinks("release-001")
	if len(bwd) == 0 {
		t.Error("expected backward links to release-001")
	}
}

func BenchmarkGraph_QueryByKind(b *testing.B) {
	g := traceability.NewGraph()
	for i := range 1000 {
		kind := traceability.KindImplementation
		if i%3 == 0 {
			kind = traceability.KindTest
		}
		g.AddNode(traceability.TraceNode{
			ID:   string(rune('a'+i%26)) + b.Name() + string(rune(i)),
			Kind: kind,
		})
	}
	b.ResetTimer()
	for range b.N {
		g.QueryByKind(traceability.KindTest)
	}
}
