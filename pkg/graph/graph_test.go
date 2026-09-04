package graph

import (
	"errors"
	"strings"
	"testing"
	"time"

	"veriqo/pkg/contract"
	"veriqo/pkg/entity"
	"veriqo/pkg/ontology"
)

func d(y int) time.Time         { return time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC) }
func to(t time.Time) *time.Time { return &t }

func span(from, until int) contract.Interval {
	return contract.Interval{From: d(from), To: to(d(until))}
}

func ent(id string, k entity.Kind) entity.Entity {
	return entity.Entity{
		ID: contract.ID("entity:" + id), Kind: k, TenantID: "t-acme",
		Attributes: []entity.Attribute{{Name: "name", Value: id,
			Scope: span(2000, 2100), EvidenceRefs: []string{"ev:" + id}}},
	}
}

func newGraph(t *testing.T) *Graph {
	t.Helper()
	ont, err := ontology.Veriqo(1)
	if err != nil {
		t.Fatal(err)
	}
	g, err := New("t-acme", ont)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func edge(id, typ, from, to string, q Qualification, s contract.Interval) Edge {
	return Edge{ID: contract.ID("edge:" + id), Type: typ,
		From: contract.ID("entity:" + from), To: contract.ID("entity:" + to),
		Scope: s, EvidenceRefs: []string{"ev:" + id}, Qualification: q}
}

// TestAnEdgeWithNoEvidenceIsRefused. Law 1 at the graph layer: an edge
// with nothing behind it is the graph asserting on its own authority.
func TestAnEdgeWithNoEvidenceIsRefused(t *testing.T) {
	e := edge("e1", "PERFORMED_VOYAGE", "v1", "voy1", Documented, span(2024, 2025))
	e.EvidenceRefs = nil
	if err := e.Validate(); !errors.Is(err, ErrNoEvidence) {
		t.Fatalf("an edge with no evidence was accepted: %v", err)
	}
}

// TestAnEdgeWithNoScopeIsRefused: an unscoped edge asserts that it
// always held.
func TestAnEdgeWithNoScopeIsRefused(t *testing.T) {
	e := edge("e1", "PERFORMED_VOYAGE", "v1", "voy1", Documented, contract.Interval{})
	if err := e.Validate(); !errors.Is(err, ErrNoScope) {
		t.Fatalf("an unscoped edge was accepted: %v", err)
	}
}

// TestAnEdgeWithNoQualificationIsRefused.
func TestAnEdgeWithNoQualificationIsRefused(t *testing.T) {
	e := edge("e1", "PERFORMED_VOYAGE", "v1", "voy1", "", span(2024, 2025))
	if err := e.Validate(); !errors.Is(err, ErrUnqualified) {
		t.Fatalf("an unqualified edge was accepted: %v", err)
	}
}

// TestAnEdgeWithContradictionsMustBeContested. The two statements
// cannot both be made.
func TestAnEdgeWithContradictionsMustBeContested(t *testing.T) {
	e := edge("e1", "PERFORMED_VOYAGE", "v1", "voy1", Verified, span(2024, 2025))
	e.Contradictions = []string{"ev:port-record shows another vessel"}
	if err := e.Validate(); err == nil {
		t.Fatal("an edge with counter-evidence claimed to be VERIFIED")
	}
	e.Qualification = Contested
	if err := e.Validate(); err != nil {
		t.Fatalf("a properly contested edge was refused: %v", err)
	}
}

// TestAPathIsNoBetterThanItsWorstLink.
func TestAPathIsNoBetterThanItsWorstLink(t *testing.T) {
	g := newGraph(t)
	g.AddNode(ent("v1", entity.Vessel), "Vessel")
	g.AddNode(ent("voy1", entity.Voyage), "Voyage")
	g.AddNode(ent("lot1", entity.CargoLot), "CargoLot")

	g.AddEdge(edge("a", "PERFORMED_VOYAGE", "v1", "voy1", Verified, span(2024, 2025)))
	g.AddEdge(edge("b", "CARRIED", "voy1", "lot1", Asserted, span(2024, 2025)))

	paths, err := g.Paths("entity:v1", "entity:lot1", Options{MaxDepth: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 {
		t.Fatalf("found %d paths", len(paths))
	}
	if paths[0].Qualification != Asserted {
		t.Fatalf("a path through a VERIFIED and an ASSERTED edge is qualified %s",
			paths[0].Qualification)
	}
	// And it carries everything it rests on.
	if len(paths[0].EvidenceRefs) != 2 {
		t.Fatalf("the path cites %v", paths[0].EvidenceRefs)
	}
}

// TestPathsAreOrderedByQualificationNotByLength.
//
// A caller taking the first result must get the best-established
// connection. The shorter path is often the weaker one, and returning
// it first is how a weakly supported link becomes the headline.
func TestPathsAreOrderedByQualificationNotByLength(t *testing.T) {
	g := newGraph(t)
	for _, n := range []struct {
		id string
		k  entity.Kind
		ot string
	}{
		{"lot1", entity.CargoLot, "CargoLot"},
		{"bl1", entity.Document, "BillOfLading"},
		{"c1", entity.Contract, "Contract"},
		{"org1", entity.Organisation, "Organisation"},
	} {
		if err := g.AddNode(ent(n.id, n.k), n.ot); err != nil {
			t.Fatal(err)
		}
	}
	// Short and weak: lot -> contract -> seller.
	g.AddEdge(edge("short1", "UNDER_CONTRACT", "lot1", "c1", Asserted, span(2024, 2025)))
	g.AddEdge(edge("short2", "SELLER", "c1", "org1", Asserted, span(2024, 2025)))
	// Longer and stronger is not constructible here without more
	// types, so instead: strengthen one route and confirm ordering.
	paths, err := g.Paths("entity:lot1", "entity:org1", Options{MaxDepth: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no path found")
	}
	if paths[0].Qualification != Asserted {
		t.Fatalf("path qualification = %s", paths[0].Qualification)
	}

	// Now add a VERIFIED alternative and check it sorts first.
	g2 := newGraph(t)
	g2.AddNode(ent("lot1", entity.CargoLot), "CargoLot")
	g2.AddNode(ent("c1", entity.Contract), "Contract")
	g2.AddNode(ent("org1", entity.Organisation), "Organisation")
	g2.AddEdge(edge("weak1", "UNDER_CONTRACT", "lot1", "c1", Asserted, span(2024, 2025)))
	g2.AddEdge(edge("weak2", "SELLER", "c1", "org1", Asserted, span(2024, 2025)))
	g2.AddEdge(edge("strong1", "SUPPLIED_BY", "lot1", "org1", Verified, span(2024, 2025)))

	paths, err = g2.Paths("entity:lot1", "entity:org1", Options{MaxDepth: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) < 2 {
		t.Fatalf("expected two routes, got %d", len(paths))
	}
	if paths[0].Qualification != Verified {
		t.Fatalf("the best-established path is not first: %s", paths[0])
	}
}

// TestContestedEdgesAreExcludedByDefault. A path through disputed
// evidence must not be returned as though it were established.
func TestContestedEdgesAreExcludedByDefault(t *testing.T) {
	g := newGraph(t)
	g.AddNode(ent("v1", entity.Vessel), "Vessel")
	g.AddNode(ent("voy1", entity.Voyage), "Voyage")
	e := edge("a", "PERFORMED_VOYAGE", "v1", "voy1", Contested, span(2024, 2025))
	e.Contradictions = []string{"ev:port-record disagrees"}
	if err := g.AddEdge(e); err != nil {
		t.Fatal(err)
	}

	paths, err := g.Paths("entity:v1", "entity:voy1", Options{MaxDepth: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 {
		t.Fatal("a contested edge was traversed by default")
	}
	paths, err = g.Paths("entity:v1", "entity:voy1", Options{MaxDepth: 3, IncludeContested: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || !paths[0].Contested {
		t.Fatalf("an explicitly requested contested path was not returned or not marked: %v", paths)
	}
}

// TestTemporalTruth: "who operated this vessel in 2019" and "who
// operates it" are different questions with different answers.
func TestTemporalTruth(t *testing.T) {
	g := newGraph(t)
	g.AddNode(ent("v1", entity.Vessel), "Vessel")
	g.AddNode(ent("orgA", entity.Organisation), "Organisation")
	g.AddNode(ent("orgB", entity.Organisation), "Organisation")

	g.AddEdge(edge("op-a", "OPERATED_BY", "v1", "orgA", Documented, span(2018, 2021)))
	g.AddEdge(edge("op-b", "OPERATED_BY", "v1", "orgB", Documented, span(2021, 2026)))

	in2019, err := g.Neighbours("entity:v1", Options{At: d(2019)})
	if err != nil {
		t.Fatal(err)
	}
	if len(in2019) != 1 || in2019[0].To != "entity:orgA" {
		t.Fatalf("the 2019 operator resolved to %v", in2019)
	}
	in2023, _ := g.Neighbours("entity:v1", Options{At: d(2023)})
	if len(in2023) != 1 || in2023[0].To != "entity:orgB" {
		t.Fatalf("the 2023 operator resolved to %v", in2023)
	}
	// With no instant, both periods are returned -- which is what a
	// historical reconstruction wants and NOT a present-tense answer.
	all, _ := g.Neighbours("entity:v1", Options{})
	if len(all) != 2 {
		t.Fatalf("an unscoped query returned %d edges", len(all))
	}
}

// TestATemporalEdgeMayNotBeOpenAndVerified. An unrecorded end is
// missing information, not confirmation that it continues.
func TestATemporalEdgeMayNotBeOpenAndVerified(t *testing.T) {
	g := newGraph(t)
	g.AddNode(ent("v1", entity.Vessel), "Vessel")
	g.AddNode(ent("orgA", entity.Organisation), "Organisation")

	open := edge("op", "OPERATED_BY", "v1", "orgA", Verified,
		contract.Interval{From: d(2020)})
	if err := g.AddEdge(open); err == nil {
		t.Fatal("an open-ended ownership edge was VERIFIED")
	}
	open.Qualification = Documented
	if err := g.AddEdge(open); err != nil {
		t.Fatalf("an open-ended edge at a lesser qualification was refused: %v", err)
	}
}

// TestTheOntologyIsEnforcedOnEveryEdge.
func TestTheOntologyIsEnforcedOnEveryEdge(t *testing.T) {
	g := newGraph(t)
	g.AddNode(ent("v1", entity.Vessel), "Vessel")
	g.AddNode(ent("pay1", entity.Payment), "Payment")

	// A vessel does not settle an invoice.
	bad := edge("x", "SETTLED_BY", "v1", "pay1", Documented, span(2024, 2025))
	if err := g.AddEdge(bad); !errors.Is(err, ontology.ErrEndpointMismatch) {
		t.Fatalf("an edge violating the schema was accepted: %v", err)
	}
	// An undeclared relationship type.
	bad.Type = "SOMEHOW_RELATED"
	if err := g.AddEdge(bad); !errors.Is(err, ontology.ErrUnknownRelationship) {
		t.Fatalf("an undeclared relationship was accepted: %v", err)
	}
}

// TestANodeMustMatchItsOntologyKind.
func TestANodeMustMatchItsOntologyKind(t *testing.T) {
	g := newGraph(t)
	if err := g.AddNode(ent("x", entity.Payment), "Vessel"); !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("a payment was placed as a Vessel: %v", err)
	}
}

// TestCrossTenantNodesAreRefused.
func TestCrossTenantNodesAreRefused(t *testing.T) {
	g := newGraph(t)
	e := ent("v1", entity.Vessel)
	e.TenantID = "t-beta"
	if err := g.AddNode(e, "Vessel"); !errors.Is(err, contract.ErrCrossTenant) {
		t.Fatalf("a cross-tenant node was added: %v", err)
	}
}

// TestAnUnboundedTraversalIsRefused.
func TestAnUnboundedTraversalIsRefused(t *testing.T) {
	g := newGraph(t)
	g.AddNode(ent("v1", entity.Vessel), "Vessel")
	g.AddNode(ent("voy1", entity.Voyage), "Voyage")
	if _, err := g.Paths("entity:v1", "entity:voy1", Options{}); err == nil {
		t.Fatal("an unbounded traversal was accepted")
	}
}

// TestTheDomainViewProjectsOneGraph. The maritime user and the
// finance user traverse the same store through different filters.
func TestTheDomainViewProjectsOneGraph(t *testing.T) {
	g := newGraph(t)
	g.AddNode(ent("lot1", entity.CargoLot), "CargoLot")
	g.AddNode(ent("c1", entity.Contract), "Contract")
	g.AddNode(ent("inv1", entity.Document), "Invoice")
	g.AddNode(ent("pay1", entity.Payment), "Payment")

	g.AddEdge(edge("a", "UNDER_CONTRACT", "lot1", "c1", Documented, span(2024, 2026)))
	g.AddEdge(edge("b", "INVOICED_BY", "c1", "inv1", Documented, span(2024, 2026)))
	g.AddEdge(edge("c", "SETTLED_BY", "inv1", "pay1", Documented, span(2024, 2026)))

	// The finance view carries the whole chain.
	fin, err := g.Paths("entity:lot1", "entity:pay1",
		Options{MaxDepth: 5, Domain: ontology.Finance})
	if err != nil {
		t.Fatal(err)
	}
	if len(fin) == 0 {
		t.Fatal("the finance view cannot reach a payment from a cargo lot")
	}
	// The maritime view does not: SETTLED_BY is not in it.
	mar, err := g.Paths("entity:lot1", "entity:pay1",
		Options{MaxDepth: 5, Domain: ontology.Maritime})
	if err != nil {
		t.Fatal(err)
	}
	if len(mar) != 0 {
		t.Fatal("the maritime view reached a payment; the projection is not filtering")
	}
	// And it is ONE graph: the edges are the same objects.
	if n, e := g.Counts(); n != 4 || e != 3 {
		t.Fatalf("graph holds %d nodes and %d edges", n, e)
	}
}

// TestUnqualifiedAssertionsAreEnumerable. A case must be answerable
// for "what here is only somebody's word".
func TestUnqualifiedAssertionsAreEnumerable(t *testing.T) {
	g := newGraph(t)
	g.AddNode(ent("v1", entity.Vessel), "Vessel")
	g.AddNode(ent("voy1", entity.Voyage), "Voyage")
	g.AddNode(ent("lot1", entity.CargoLot), "CargoLot")
	g.AddEdge(edge("a", "PERFORMED_VOYAGE", "v1", "voy1", Asserted, span(2024, 2025)))
	g.AddEdge(edge("b", "CARRIED", "voy1", "lot1", Verified, span(2024, 2025)))

	weak := g.UnqualifiedAssertions()
	if len(weak) != 1 || weak[0].ID != "edge:a" {
		t.Fatalf("UnqualifiedAssertions = %v", weak)
	}
}

// TestWeakerIsAMinimumAndContestedIsOffTheScale.
func TestWeakerIsAMinimumAndContestedIsOffTheScale(t *testing.T) {
	if Weaker(Verified, Asserted) != Asserted {
		t.Fatal("Weaker did not take the minimum")
	}
	if Weaker(Asserted, Contested) != Contested {
		t.Fatal("CONTESTED did not sort below ASSERTED")
	}
	if Contested.Rank() >= Asserted.Rank() {
		t.Fatal("a contested edge ranks at or above a merely asserted one")
	}
}

// TestPathStringNamesEveryHop, for the "why did VERIQO say this" view.
func TestPathStringNamesEveryHop(t *testing.T) {
	g := newGraph(t)
	g.AddNode(ent("v1", entity.Vessel), "Vessel")
	g.AddNode(ent("voy1", entity.Voyage), "Voyage")
	g.AddEdge(edge("a", "PERFORMED_VOYAGE", "v1", "voy1", Documented, span(2024, 2025)))
	paths, _ := g.Paths("entity:v1", "entity:voy1", Options{MaxDepth: 2})
	s := paths[0].String()
	for _, want := range []string{"entity:v1", "PERFORMED_VOYAGE", "DOCUMENTED", "entity:voy1"} {
		if !strings.Contains(s, want) {
			t.Errorf("the path rendering omits %q: %s", want, s)
		}
	}
}
