package independence

import (
	"errors"
	"strings"
	"testing"
)

// wireStory is the audit's worked example: one wire service, four
// outlets, and a naive count of five.
func wireStory(t *testing.T) *Graph {
	t.Helper()
	g, err := NewGraph(
		Node{ID: "reuters-report", Producer: "reuters"},
		Node{ID: "outlet-a", Producer: "outlet-a", DerivedFrom: []string{"reuters-report"}},
		Node{ID: "outlet-b", Producer: "outlet-b", DerivedFrom: []string{"reuters-report"}},
		Node{ID: "outlet-c", Producer: "outlet-c", DerivedFrom: []string{"reuters-report"}},
		Node{ID: "outlet-d", Producer: "outlet-d", DerivedFrom: []string{"reuters-report"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// TestFiveObservedSourcesAreOneObservation.
func TestFiveObservedSourcesAreOneObservation(t *testing.T) {
	g := wireStory(t)
	c, err := g.CountProducers("reuters-report", "outlet-a", "outlet-b", "outlet-c", "outlet-d")
	if err != nil {
		t.Fatal(err)
	}
	if c.Observed != 5 {
		t.Fatalf("observed = %d", c.Observed)
	}
	if c.IndependentProducers != 1 {
		t.Fatalf("five outlets carrying one wire story resolved to %d producers",
			c.IndependentProducers)
	}
	if c.Unassessable {
		t.Fatal("a fully attributed structure was reported unassessable")
	}
	if c.SatisfiesCorroboration(2) {
		t.Fatal("one producer satisfied a requirement for two")
	}
	if !c.SatisfiesCorroboration(1) {
		t.Fatal("one producer did not satisfy a requirement for one")
	}
	// The statement must explain the gap, not just report it.
	s := c.Statement()
	if !strings.Contains(s, "structure, not disagreement") {
		t.Fatalf("the statement does not explain the collapse: %s", s)
	}
	if !strings.Contains(s, "reuters produced:") {
		t.Fatalf("the statement does not show which sources collapsed: %s", s)
	}
}

// TestThreeAnonymousPostsAreNotThreeSourcesAndNotOne.
//
// Both tempting answers are wrong: 3 treats "we do not know" as "they
// differ"; 1 asserts they are the same, which is equally unfounded.
func TestThreeAnonymousPostsAreNotThreeSourcesAndNotOne(t *testing.T) {
	g, err := NewGraph(
		Node{ID: "post-a", Producer: UnknownProducer},
		Node{ID: "post-b", Producer: UnknownProducer},
		Node{ID: "post-c", Producer: UnknownProducer},
	)
	if err != nil {
		t.Fatal(err)
	}
	c, err := g.CountProducers("post-a", "post-b", "post-c")
	if err != nil {
		t.Fatal(err)
	}
	if c.IndependentProducers != 0 {
		t.Fatalf("unattributed posts produced a count of %d", c.IndependentProducers)
	}
	if !c.Unassessable {
		t.Fatal("an unattributable structure was reported assessable")
	}
	if len(c.UnattributedSources) != 3 {
		t.Fatalf("%d sources reported unattributed", len(c.UnattributedSources))
	}
	// UNASSESSABLE satisfies nothing, whatever the number beside it.
	for n := 0; n <= 3; n++ {
		if c.SatisfiesCorroboration(n) {
			t.Fatalf("an unassessable structure satisfied a requirement for %d", n)
		}
	}
	if !strings.Contains(c.Statement(), "not a low count, it is the absence of one") {
		t.Fatalf("the statement lets UNASSESSABLE read as a small number: %s", c.Statement())
	}
}

// TestOneUnknownContaminatesTheWholeAnswer.
//
// A count that quietly ignored the unattributable source would report
// a confident number over an unresolved structure.
func TestOneUnknownContaminatesTheWholeAnswer(t *testing.T) {
	g, err := NewGraph(
		Node{ID: "registry", Producer: "companies-house"},
		Node{ID: "inspector", Producer: "inspector-a"},
		Node{ID: "leak", Producer: UnknownProducer},
	)
	if err != nil {
		t.Fatal(err)
	}
	c, err := g.CountProducers("registry", "inspector", "leak")
	if err != nil {
		t.Fatal(err)
	}
	if c.IndependentProducers != 2 {
		t.Fatalf("known producers = %d", c.IndependentProducers)
	}
	if !c.Unassessable {
		t.Fatal("an unknown producer did not make the structure unassessable")
	}
	// Two known producers would satisfy a requirement for two, and it
	// must not, because the third source's origin could be either of
	// them.
	if c.SatisfiesCorroboration(2) {
		t.Fatal("a structure containing an unresolvable source satisfied a corroboration " +
			"requirement its known part happens to meet")
	}
	if !strings.Contains(c.Statement(), "lower bound") {
		t.Fatalf("the statement does not say the number is a lower bound: %s", c.Statement())
	}
}

// TestAnAggregatorDoesNotMultiplyItsInputs.
func TestAnAggregatorDoesNotMultiplyItsInputs(t *testing.T) {
	g, err := NewGraph(
		Node{ID: "registry", Producer: "companies-house"},
		Node{ID: "wire", Producer: "reuters"},
		Node{ID: "aggregator", Producer: "vendor-x",
			DerivedFrom: []string{"registry", "wire"}},
		Node{ID: "reseller", Producer: "vendor-y", DerivedFrom: []string{"aggregator"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	c, err := g.CountProducers("aggregator", "reseller")
	if err != nil {
		t.Fatal(err)
	}
	// Two products, both resolving to the same two origins.
	if c.IndependentProducers != 2 {
		t.Fatalf("two resellers of the same two feeds gave %d producers",
			c.IndependentProducers)
	}
	roots, err := g.Roots("reseller")
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 2 {
		t.Fatalf("the reseller resolves to %d roots", len(roots))
	}
}

// TestMutualCitationIsRefusedRatherThanLoopingForever.
//
// Two outlets citing each other is a real phenomenon, and it makes the
// origin genuinely unresolvable.
func TestMutualCitationIsRefusedRatherThanLoopingForever(t *testing.T) {
	_, err := NewGraph(
		Node{ID: "a", Producer: "outlet-a", DerivedFrom: []string{"b"}},
		Node{ID: "b", Producer: "outlet-b", DerivedFrom: []string{"a"}},
	)
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("a citation cycle was accepted: %v", err)
	}
	if !strings.Contains(err.Error(), "citing each other is a real phenomenon") {
		t.Fatalf("the refusal does not explain why: %v", err)
	}
}

// TestAnEmptyProducerFieldIsRefused. An unset field and a deliberate
// unknown must not look alike.
func TestAnEmptyProducerFieldIsRefused(t *testing.T) {
	_, err := NewGraph(Node{ID: "x"})
	if err == nil {
		t.Fatal("a source with no producer field was accepted")
	}
	if !strings.Contains(err.Error(), "indistinguishable from a field somebody forgot") {
		t.Fatalf("the refusal does not explain the distinction: %v", err)
	}
	if _, err := NewGraph(Node{ID: "x", Producer: UnknownProducer}); err != nil {
		t.Fatalf("an explicitly unknown producer was refused: %v", err)
	}
}

// TestADanglingDerivationIsRefused.
func TestADanglingDerivationIsRefused(t *testing.T) {
	_, err := NewGraph(Node{ID: "a", Producer: "p", DerivedFrom: []string{"missing"}})
	if !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("a dangling derivation edge was accepted: %v", err)
	}
}

// TestSourcesThatAssertNothingAboutTheirOriginAreReported.
func TestSourcesThatAssertNothingAboutTheirOriginAreReported(t *testing.T) {
	g, err := NewGraph(
		Node{ID: "registry", Producer: "companies-house"},
		Node{ID: "post", Producer: UnknownProducer},
	)
	if err != nil {
		t.Fatal(err)
	}
	u := g.Unattributed()
	if len(u) != 1 || u[0] != "post" {
		t.Fatalf("unattributed = %v", u)
	}
	if !strings.Contains(g.Report(), "assert nothing about their origin") {
		t.Fatalf("the report does not surface them:\n%s", g.Report())
	}
}

// TestTheSameSourceOfferedTwiceIsRefused.
func TestTheSameSourceOfferedTwiceIsRefused(t *testing.T) {
	g := wireStory(t)
	if _, err := g.CountProducers("outlet-a", "outlet-a"); err == nil {
		t.Fatal("the same source counted twice")
	}
	if _, err := NewGraph(
		Node{ID: "a", Producer: "p"}, Node{ID: "a", Producer: "q"},
	); err == nil {
		t.Fatal("a duplicate node was accepted")
	}
}
