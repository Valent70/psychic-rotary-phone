// Package provenance implements VERIQO's computed (not caller-asserted)
// evidence-source independence graph — the concrete fix for two
// findings from the "Hasil audit Professional Technical Auditor"
// series:
//
//   - v7.9 P0 "Provenance dihitung, bukan diberikan": risk.CompositeInput
//     previously accepted a bare ProvenanceIndependenceRatio float64 the
//     caller simply asserted ("independence saya 0.9") with no way for
//     the engine to verify it. An attacker (or a careless caller) could
//     send 1.0 and the model would treat clearly-dependent evidence as
//     independent.
//   - v7.8 Priority C "Provenance graph": the pre-existing mechanism
//     (calibration.Engine's sourceUpstream map[string]string) is
//     one-hop only — it catches "A's upstream == B's upstream" but
//     misses "A -> B -> C" vs "D -> C" (A and D share root C two hops
//     up, through different immediate parents).
//
// This package is a thin domain layer over the existing, already-tested
// pkg/evidence/graph.Graph (reachability + Lineage) rather than a new
// graph implementation — the same "reuse the canonical primitive, don't
// build a parallel one" discipline this repo has applied to every prior
// gap-closure pass (fusion reused by risk, decision reused by risk,
// calibration reused by intent).
//
// Honest scope: independence here means "no common registered
// upstream ancestor in the declared provenance graph." It is exactly as
// good as the RegisterUpstream declarations callers supply — this
// package computes deterministic multi-hop consequences of those
// declarations, it does not independently discover real-world corporate
// or infrastructure ownership. Verified provenance (e.g. cryptographic
// attestation that AIS_VENDOR_A truly resells SATELLITE_OPERATOR_X) is
// out of scope, same limitation the prior one-hop mechanism had.
package provenance

import (
	"context"
	"sort"

	egraph "veriqo/pkg/evidence/graph"
	"veriqo/pkg/platform/telemetry"
)

// Graph tracks declared "derives-from" edges between evidence sources
// and computes multi-hop shared ancestry deterministically. Safe for
// concurrent use (delegates all locking to the underlying evidence
// graph).
type Graph struct {
	g       *egraph.Graph
	nodeIDs map[string]egraph.NodeID // sourceID -> its graph node
}

// New builds an empty provenance Graph.
func New() *Graph {
	return &Graph{g: egraph.NewGraph(), nodeIDs: make(map[string]egraph.NodeID)}
}

func (p *Graph) nodeFor(sourceID string) egraph.NodeID {
	if id, ok := p.nodeIDs[sourceID]; ok {
		return id
	}
	id := p.g.AddNode("provenance_source", sourceID)
	p.nodeIDs[sourceID] = id
	return id
}

// Declare records that sourceID derives from (is downstream of)
// upstreamID — e.g. "AIS_VENDOR_A" derives from
// "SATELLITE_OPERATOR_X_RESELLER" which itself derives from
// "SATELLITE_OPERATOR_X". Call multiple times to build an arbitrarily
// deep chain; each call is one hop. Idempotent: declaring the same edge
// twice is a no-op (AddEdge below is itself idempotent-safe via the
// underlying graph's dedup).
func (p *Graph) Declare(sourceID, upstreamID string) error {
	from := p.nodeFor(sourceID)
	to := p.nodeFor(upstreamID)
	// Reuse the graph's own RelationDerivedFrom so Lineage() (which
	// only follows DERIVED_FROM/CORRECTS edges) and its built-in cycle
	// protection apply here for free, instead of inventing a parallel
	// relation kind this package would have to re-implement traversal
	// and cycle-checking for.
	return p.g.AddEdge(from, to, egraph.RelationDerivedFrom)
}

// Ancestors returns every declared upstream ancestor of sourceID
// (all hops), including sourceID's own node is excluded. A source with
// no declared upstream has an empty ancestor set — by default treated
// as independent of everything, matching the prior mechanism's
// fallback semantics.
func (p *Graph) Ancestors(sourceID string) (map[string]bool, error) {
	id, ok := p.nodeIDs[sourceID]
	if !ok {
		return map[string]bool{}, nil // never declared -> no known ancestry
	}
	nodes, err := p.g.Lineage(id)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		if n.ID == id {
			continue
		}
		out[string(n.Payload)] = true
	}
	return out, nil
}

// PairShared reports whether sourceA and sourceB share any ancestor
// (including the degenerate case where one IS the other, or one is a
// direct/indirect ancestor of the other) — full multi-hop epistemic
// ancestry, not one-hop ID/upstream equality.
func (p *Graph) PairShared(sourceA, sourceB string) (bool, error) {
	if sourceA == sourceB {
		return true, nil
	}
	ancA, err := p.Ancestors(sourceA)
	if err != nil {
		return false, err
	}
	ancB, err := p.Ancestors(sourceB)
	if err != nil {
		return false, err
	}
	// A is an ancestor of B, or vice versa.
	if ancA[sourceB] || ancB[sourceA] {
		return true, nil
	}
	// Shared common ancestor two-or-more hops up.
	for a := range ancA {
		if ancB[a] {
			return true, nil
		}
	}
	return false, nil
}

// ProvenanceStatus states the epistemic status of an independence
// finding — the auditor's explicit distinction between DECLARED
// provenance (what this package computes: no shared ancestor found in
// the graph of relationships callers have DECLARED) and VERIFIED
// provenance (cryptographic/third-party attestation that a declared
// edge is factually true) — this package only ever produces the
// former; conflating the two would let "independence=1.0" be
// misread as "these sources are factually independent" when what was
// actually established is narrower: "no shared ancestry was found in
// the available declared graph."
type ProvenanceStatus string

const (
	// StatusDeclaredIndependent: no shared ancestor found among
	// DECLARED edges. Honest reading: "not contradicted by what we
	// know," not "proven independent."
	StatusDeclaredIndependent ProvenanceStatus = "DECLARED_INDEPENDENT"
	// StatusDeclaredSharedOrigin: a shared ancestor WAS found among
	// declared edges.
	StatusDeclaredSharedOrigin ProvenanceStatus = "DECLARED_SHARED_ORIGIN"
	// StatusUnknown: NONE of the sources have any declared ancestry at
	// all (never registered) — treated as independent by default (see
	// Ancestors), but flagged UNKNOWN rather than DECLARED_INDEPENDENT
	// so a consumer can distinguish "we checked and found nothing" from
	// "we never had data to check."
	StatusUnknown ProvenanceStatus = "UNKNOWN"
	// StatusPartialUnknown: SOME but not all sources are registered in
	// the graph — v7.10 audit fix. Assess previously granted
	// DECLARED_INDEPENDENT whenever the ratio hit 1.0 as long as ANY
	// source was declared, so one declared source plus one entirely
	// unregistered source (which trivially has no shared ancestors,
	// since it has none at all) could be misreported as
	// "independent" — a partial-knowledge gap masquerading as a
	// verified-absence-of-shared-origin. Now Assess requires EVERY
	// source in the query to be declared before it will grant
	// DECLARED_INDEPENDENT or DECLARED_SHARED_ORIGIN; a mixed
	// declared/undeclared set is reported as StatusPartialUnknown
	// instead, so a Trust layer consumer can tell "we verified all of
	// them and found no shared origin" apart from "we could only check
	// some of them."
	StatusPartialUnknown ProvenanceStatus = "PARTIAL_UNKNOWN"
	// StatusVerified is reserved for future integration with a real
	// attestation mechanism (cryptographic proof-of-origin, a
	// third-party registry confirmation). NOT produced by this package
	// today — no such attestation source exists in this environment.
	// Naming it here documents the target end-state the audit asks for
	// rather than silently having no ceiling above DECLARED.
	StatusVerified ProvenanceStatus = "VERIFIED"
)

// IndependenceAssessment is the full epistemic result of a provenance
// check — score PLUS the status/evidence that earned it, so a Trust
// layer consumer never has to interpret a bare float in isolation.
type IndependenceAssessment struct {
	Score           float64
	Status          ProvenanceStatus
	SharedAncestors []string // ancestor IDs found in common, if any
	EvidenceIDs     []string // the source IDs this assessment was computed over
}

// Assess is ComputeIndependence's richer sibling: same computation,
// but returns the full IndependenceAssessment (score + epistemic
// status + the shared ancestors that justify it) instead of a bare
// ratio — the auditor's explicit ask that "independence=1.0" never be
// handed to a consumer without the status that earned it.
func (p *Graph) Assess(sourceIDs []string) (IndependenceAssessment, error) {
	res, err := p.ComputeIndependence(sourceIDs)
	if err != nil {
		return IndependenceAssessment{}, err
	}
	uniq := dedupeSorted(sourceIDs)
	allDeclared := len(uniq) > 0
	anyDeclared := false
	for _, id := range uniq {
		if _, ok := p.nodeIDs[id]; ok {
			anyDeclared = true
		} else {
			allDeclared = false
		}
	}
	status := StatusUnknown
	switch {
	case allDeclared:
		if res.Ratio >= 1.0 {
			status = StatusDeclaredIndependent
		} else {
			status = StatusDeclaredSharedOrigin
		}
	case anyDeclared:
		// Mixed knowledge: some sources verified against the graph,
		// some never registered at all. Never grant DECLARED_* here —
		// see StatusPartialUnknown doc.
		status = StatusPartialUnknown
	}
	// Collect the actual shared ancestor IDs (not just the pair list)
	// for the DependentPairs found, for full auditability.
	sharedSet := map[string]bool{}
	for i := 0; i < len(uniq); i++ {
		for j := i + 1; j < len(uniq); j++ {
			ancA, _ := p.Ancestors(uniq[i])
			ancB, _ := p.Ancestors(uniq[j])
			for a := range ancA {
				if ancB[a] {
					sharedSet[a] = true
				}
			}
		}
	}
	var shared []string
	for a := range sharedSet {
		shared = append(shared, a)
	}
	sort.Strings(shared)
	return IndependenceAssessment{
		Score: res.Ratio, Status: status, SharedAncestors: shared, EvidenceIDs: uniq,
	}, nil
}

// set of sources for shared provenance.
type IndependenceResult struct {
	// Ratio is the fraction of distinct source pairs with NO shared
	// ancestry: 1.0 = every pair independent, 0.0 = every pair shares
	// provenance. A single-source set is trivially 1.0 (no pairs to
	// compare, nothing to discount).
	Ratio float64
	// DependentPairs lists every pair (sorted, "a|b") found to share
	// ancestry — the explicit evidence backing Ratio, for audit.
	DependentPairs []string
	// PairCount is the total number of distinct pairs evaluated.
	PairCount int
}

// ComputeIndependence evaluates every pairwise combination of sourceIDs
// for shared declared provenance and returns a deterministic,
// order-invariant result (sorted before pairing) — replacing a
// caller-asserted ProvenanceIndependenceRatio with one the engine
// itself derives from the declared graph.
func (p *Graph) ComputeIndependence(sourceIDs []string) (IndependenceResult, error) {
	_, span := telemetry.StartSpan(context.Background(), "provenance.ComputeIndependence")
	defer span.End()

	uniq := dedupeSorted(sourceIDs)
	if len(uniq) <= 1 {
		return IndependenceResult{Ratio: 1.0}, nil
	}
	var dependent []string
	pairCount := 0
	for i := 0; i < len(uniq); i++ {
		for j := i + 1; j < len(uniq); j++ {
			pairCount++
			shared, err := p.PairShared(uniq[i], uniq[j])
			if err != nil {
				return IndependenceResult{}, err
			}
			if shared {
				dependent = append(dependent, uniq[i]+"|"+uniq[j])
			}
		}
	}
	ratio := 1.0
	if pairCount > 0 {
		ratio = 1.0 - float64(len(dependent))/float64(pairCount)
	}
	return IndependenceResult{Ratio: ratio, DependentPairs: dependent, PairCount: pairCount}, nil
}

func dedupeSorted(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
