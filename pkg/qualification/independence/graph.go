package independence

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// The Source Independence Graph.
//
// # Why counting sources is the wrong operation
//
// Pairwise assessment answers "are these two independent". It is the
// right question and it is not the whole question, because
// independence is not a property of pairs -- it is a property of the
// PRODUCER STRUCTURE behind them:
//
//	                    Reuters
//	                       |
//	                  wire service
//	           ________/  |  \________
//	          /           |           \
//	     Outlet A    Outlet B    Outlet C    Outlet D
//
//	observed sources     = 4
//	independent producers = 1
//
// A pairwise assessment of A and B might well return CORRELATED, and a
// caller who counts CORRELATED pairs and subtracts is doing arithmetic
// on the wrong object. What matters is that all four resolve to one
// origin, and the only way to know that is to walk the derivation
// edges to their roots.
//
// # The second case, which is worse
//
//	Anonymous Post A     Anonymous Post B     Anonymous Post C
//
// Here the producers are not shared -- they are UNKNOWN. Nothing can
// be assessed, and the tempting answers are both wrong:
//
//	3  treats "we do not know" as "they differ"
//	1  asserts they are the same, which is equally unfounded
//
// The correct answer is that the question cannot be answered at all,
// and it needs its own value. UNASSESSABLE is that value: it is not a
// low count, it is the absence of a count, and code that compares it
// numerically to 1 is code that has misunderstood the situation.
type ProducerID string

// UnknownProducer is the explicit marker for an unattributable source.
//
// It is a named constant rather than the empty string so that a source
// with no producer looks deliberate in a struct literal, and so that
// the graph can refuse to treat two unknowns as the same party.
const UnknownProducer ProducerID = "UNKNOWN"

var (
	ErrCycle        = errors.New("independence: the derivation graph contains a cycle")
	ErrUnknownNode  = errors.New("independence: an edge names a source that is not in the graph")
	ErrUnassessable = errors.New("independence: the producer structure cannot be assessed")
)

// Node is one observed source in the graph.
type Node struct {
	ID string `json:"id"`
	// Producer is the party that PRODUCED the content, which is not
	// the party that published it. An outlet that runs a wire story is
	// a publisher; the wire service is the producer.
	Producer ProducerID `json:"producer"`
	// DerivedFrom names the sources this one came from. An outlet
	// carrying a wire story derives from the wire; an aggregator
	// derives from everything it aggregates.
	//
	// It is the edge that makes this a graph rather than a list, and
	// it is the field most often left empty by accident -- so
	// Unattributed() reports nodes that claim no origin AND no
	// producer, which is the shape of a source nobody has examined.
	DerivedFrom []string `json:"derived_from,omitempty"`
	// Attributes carry the pairwise dimensions, so a node can also be
	// assessed the existing way.
	Attributes map[Dimension]string `json:"attributes,omitempty"`
}

func (n Node) Validate() error {
	if strings.TrimSpace(n.ID) == "" {
		return ErrNoSource
	}
	if strings.TrimSpace(string(n.Producer)) == "" {
		return fmt.Errorf("independence: source %s has no producer field. Use "+
			"UnknownProducer explicitly -- an empty producer is indistinguishable from a "+
			"field somebody forgot", n.ID)
	}
	for d := range n.Attributes {
		if !d.Valid() {
			return fmt.Errorf("independence: source %s carries unknown dimension %q", n.ID, d)
		}
	}
	return nil
}

// Attributable reports whether this node's producer is known.
func (n Node) Attributable() bool { return n.Producer != UnknownProducer }

// Graph is a set of observed sources and the derivation edges between
// them.
type Graph struct {
	nodes map[string]Node
	order []string
}

// NewGraph builds and validates a graph.
//
// It refuses a cycle. A derivation cycle is not a modelling nicety: it
// means A is sourced from B and B from A, which is either a data error
// or a pair of outlets citing each other -- and the second is a real
// phenomenon that would otherwise make a root-walk loop forever.
func NewGraph(nodes ...Node) (*Graph, error) {
	g := &Graph{nodes: map[string]Node{}}
	for _, n := range nodes {
		if err := n.Validate(); err != nil {
			return nil, err
		}
		if _, dup := g.nodes[n.ID]; dup {
			return nil, fmt.Errorf("independence: source %q appears twice; the same source "+
				"listed twice is one source", n.ID)
		}
		g.nodes[n.ID] = n
		g.order = append(g.order, n.ID)
	}
	sort.Strings(g.order)
	for _, id := range g.order {
		for _, from := range g.nodes[id].DerivedFrom {
			if _, ok := g.nodes[from]; !ok {
				return nil, fmt.Errorf("%w: %s derives from %s", ErrUnknownNode, id, from)
			}
		}
	}
	if cyc := g.findCycle(); cyc != "" {
		return nil, fmt.Errorf("%w: %s. Two sources citing each other is a real "+
			"phenomenon and it makes the origin unresolvable", ErrCycle, cyc)
	}
	return g, nil
}

func (g *Graph) findCycle() string {
	const (
		white = 0
		grey  = 1
		black = 2
	)
	colour := map[string]int{}
	var path []string
	var walk func(string) string
	walk = func(id string) string {
		colour[id] = grey
		path = append(path, id)
		for _, from := range g.nodes[id].DerivedFrom {
			switch colour[from] {
			case grey:
				return strings.Join(append(path, from), " -> ")
			case white:
				if c := walk(from); c != "" {
					return c
				}
			}
		}
		path = path[:len(path)-1]
		colour[id] = black
		return ""
	}
	for _, id := range g.order {
		if colour[id] == white {
			if c := walk(id); c != "" {
				return c
			}
		}
	}
	return ""
}

// Roots returns the sources a node ultimately derives from.
//
// A node with no derivation edges is its own root. The walk is what
// turns four outlets into one origin.
func (g *Graph) Roots(id string) ([]string, error) {
	if _, ok := g.nodes[id]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownNode, id)
	}
	seen := map[string]bool{}
	var out []string
	var walk func(string)
	walk = func(cur string) {
		if seen[cur] {
			return
		}
		seen[cur] = true
		n := g.nodes[cur]
		if len(n.DerivedFrom) == 0 {
			out = append(out, cur)
			return
		}
		for _, from := range n.DerivedFrom {
			walk(from)
		}
	}
	walk(id)
	sort.Strings(out)
	return out, nil
}

// OriginProducers returns the producers at the roots of a node.
func (g *Graph) OriginProducers(id string) ([]ProducerID, error) {
	roots, err := g.Roots(id)
	if err != nil {
		return nil, err
	}
	seen := map[ProducerID]bool{}
	var out []ProducerID
	for _, r := range roots {
		p := g.nodes[r].Producer
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// Count is the answer to "how many independent producers are behind
// these sources".
//
// It is a struct rather than an int because the honest answer has
// three parts, and returning only the first would recreate the problem
// the graph exists to solve.
type Count struct {
	// Observed is how many sources were presented. It is the number
	// that looks impressive and means least.
	Observed int `json:"observed"`
	// IndependentProducers is how many distinct, KNOWN producers sit
	// at the roots. This is the number a corroboration rule should use.
	IndependentProducers int `json:"independent_producers"`
	// Unassessable is true when any part of the structure cannot be
	// resolved -- an unknown producer anywhere in the roots. When it
	// is true, IndependentProducers is a LOWER BOUND on the knowable
	// and says nothing about the unknown part.
	Unassessable bool `json:"unassessable"`
	// UnattributedSources names the sources whose origin is unknown.
	UnattributedSources []string `json:"unattributed_sources,omitempty"`
	// Collapsed records which sources reduced to a shared origin, so
	// a reader can see the structure rather than trust the number.
	Collapsed map[ProducerID][]string `json:"collapsed,omitempty"`
}

// SatisfiesCorroboration reports whether this count meets a
// requirement for n independent producers.
//
// An UNASSESSABLE structure satisfies nothing, whatever the count.
// That is the rule the whole type exists for: not knowing whether
// sources are independent is not the same as knowing they are, and a
// caller that compared IndependentProducers to n directly would lose
// exactly that distinction.
func (c Count) SatisfiesCorroboration(n int) bool {
	return !c.Unassessable && c.IndependentProducers >= n
}

// Statement renders the count the way it must appear to a reader.
func (c Count) Statement() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d observed source(s); %d independent producer(s)",
		c.Observed, c.IndependentProducers)
	if c.Observed != c.IndependentProducers {
		b.WriteString(". The difference is structure, not disagreement: sources sharing an " +
			"origin are one observation however many times it is republished")
	}
	if c.Unassessable {
		fmt.Fprintf(&b, ". UNASSESSABLE: %d source(s) have no identifiable producer (%s), "+
			"so the producer structure cannot be resolved. The producer count above is a "+
			"lower bound on what is KNOWN and says nothing about the rest -- it is not a "+
			"low count, it is the absence of one",
			len(c.UnattributedSources), strings.Join(c.UnattributedSources, ", "))
	}
	for p, ids := range c.Collapsed {
		if len(ids) > 1 {
			sort.Strings(ids)
			fmt.Fprintf(&b, "\n  %s produced: %s", p, strings.Join(ids, ", "))
		}
	}
	return b.String()
}

// CountProducers resolves a set of observed sources to their origins.
//
// This replaces "how many sources do we have" with "how many parties
// actually observed anything", which is the question a corroboration
// rule is trying to ask.
func (g *Graph) CountProducers(ids ...string) (Count, error) {
	if len(ids) == 0 {
		return Count{}, errors.New("independence: no sources offered")
	}
	c := Count{Observed: len(ids), Collapsed: map[ProducerID][]string{}}
	seen := map[ProducerID]bool{}
	dedupe := map[string]bool{}

	for _, id := range ids {
		if dedupe[id] {
			return Count{}, fmt.Errorf("independence: %s offered twice", id)
		}
		dedupe[id] = true

		producers, err := g.OriginProducers(id)
		if err != nil {
			return Count{}, err
		}
		for _, p := range producers {
			c.Collapsed[p] = append(c.Collapsed[p], id)
			if p == UnknownProducer {
				c.Unassessable = true
				c.UnattributedSources = append(c.UnattributedSources, id)
				continue
			}
			if !seen[p] {
				seen[p] = true
				c.IndependentProducers++
			}
		}
	}
	sort.Strings(c.UnattributedSources)
	return c, nil
}

// Unattributed returns the sources in the graph whose producer is
// unknown AND which claim no derivation.
//
// That combination is the shape of a source nobody has examined: it
// asserts nothing about where it came from in either direction.
func (g *Graph) Unattributed() []string {
	var out []string
	for _, id := range g.order {
		n := g.nodes[id]
		if !n.Attributable() && len(n.DerivedFrom) == 0 {
			out = append(out, id)
		}
	}
	return out
}

// Report renders the graph.
func (g *Graph) Report() string {
	var b strings.Builder
	b.WriteString("SOURCE INDEPENDENCE GRAPH\n")
	b.WriteString("  observed sources are not observations. Republication, aggregation and\n")
	b.WriteString("  syndication multiply the first without adding to the second.\n\n")
	for _, id := range g.order {
		n := g.nodes[id]
		roots, _ := g.Roots(id)
		producers, _ := g.OriginProducers(id)
		var ps []string
		for _, p := range producers {
			ps = append(ps, string(p))
		}
		fmt.Fprintf(&b, "  %-22s producer %-18s origin %v via %v\n",
			n.ID, n.Producer, ps, roots)
	}
	if u := g.Unattributed(); len(u) > 0 {
		fmt.Fprintf(&b, "\n  %d source(s) assert nothing about their origin in either "+
			"direction: %s\n", len(u), strings.Join(u, ", "))
	}
	return b.String()
}
