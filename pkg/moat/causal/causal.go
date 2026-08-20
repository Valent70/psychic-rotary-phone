// Package causal implements Veriqo's Causal Intelligence layer — the
// piece Yang_masih_kurang.docx names as the gap between "chaining of
// evidence" (what pkg/moat/fusion already does) and "chaining of causes
// and effects" (root-cause explanation, not just fact aggregation).
//
// It sits above fusion/kg: causal edges are asserted observations about
// how events relate (AIS_OFF -> STS -> CARGO_CHANGE -> ...), each with a
// strength (how often cause precedes effect), a lag (ticks between them),
// and an optional condition. Two capabilities follow directly from that
// graph:
//
//   - Why(effect): root-cause explanation — every path of causes leading
//     to an effect, ranked by combined strength.
//   - WhatIf(effect, removedCause): counterfactual — how much causal
//     support for an effect would remain if one specific cause were
//     removed, computed as a pure read (the graph itself is never
//     mutated by a counterfactual query — append-only discipline).
//
// Same invariants as fusion/kg: explicit Tick (no time.Now()), content-
// derived edge keys (no UUID), hash-chained observation log, deterministic
// replay independent of map iteration order.
package causal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"

	"veriqo/pkg/platform/telemetry"
)

var (
	ErrInvalidStrength = errors.New("causal: strength must be in (0,1]")
	ErrTamperDetected  = errors.New("causal: hash chain verification failed")
	ErrCycleDetected   = errors.New("causal: cycle detected in causal graph")
)

// NodeID identifies a causal event/state node — either an abstract event
// type ("AIS_OFF") or an entity-scoped one ("VESSEL:IMO123::AIS_OFF").
type NodeID string

// Edge is one asserted cause -> effect relationship.
type Edge struct {
	Cause     NodeID
	Effect    NodeID
	Strength  float64 // (0,1], roughly P(effect follows | cause occurred)
	Lag       uint64  // ticks typically observed between cause and effect
	Condition string  // optional context this edge holds under, "" = unconditional
}

func edgeKey(cause, effect NodeID, condition string) string {
	return string(cause) + "->" + string(effect) + "|" + condition
}

// ObservationRecord is one hash-chained log entry — the causal-layer
// analogue of fusion.FusionRecord.
type ObservationRecord struct {
	Index     uint64
	ActorID   string
	Cause     NodeID
	Effect    NodeID
	Strength  float64
	Lag       uint64
	Condition string
	Tick      uint64
	PrevHash  string
	Hash      string
}

// CausalPath is one traced chain of causes leading to an effect, with
// its combined strength (product of edge strengths along the path).
type CausalPath struct {
	Chain    []NodeID // Chain[0] = root cause ... Chain[len-1] = the effect
	Strength float64
}

// Graph is the live causal graph. Safe for concurrent use.
type Graph struct {
	mu    sync.RWMutex
	edges map[string]Edge // edgeKey -> latest observed edge
	log   []ObservationRecord
	head  string
}

func NewGraph() *Graph {
	return &Graph{edges: make(map[string]Edge)}
}

// Observe asserts (or updates, if the same cause/effect/condition was
// observed before) one causal edge. Re-observation reflects "latest
// belief", mirroring fusion.Arbitrate's re-arbitration semantics.
func (g *Graph) Observe(actorID string, cause, effect NodeID, strength float64, lag uint64, condition string, tick uint64) (Edge, error) {
	if !(strength > 0 && strength <= 1) {
		return Edge{}, ErrInvalidStrength
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	e := Edge{Cause: cause, Effect: effect, Strength: strength, Lag: lag, Condition: condition}
	g.edges[edgeKey(cause, effect, condition)] = e

	rec := ObservationRecord{
		Index: uint64(len(g.log)), ActorID: actorID,
		Cause: cause, Effect: effect, Strength: strength, Lag: lag,
		Condition: condition, Tick: tick, PrevHash: g.head,
	}
	rec.Hash = hashObservation(rec)
	g.log = append(g.log, rec)
	g.head = rec.Hash
	return e, nil
}

// directCausesOf returns edges whose Effect == node, sorted deterministically.
func (g *Graph) directCausesOf(node NodeID) []Edge {
	var out []Edge
	for _, e := range g.edges {
		if e.Effect == node {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Cause != out[j].Cause {
			return out[i].Cause < out[j].Cause
		}
		return out[i].Condition < out[j].Condition
	})
	return out
}

// maxDepth bounds Why()'s traversal to keep it terminating deterministically
// even if the caller's data has an accidental cycle (see ErrCycleDetected
// for the case Why detects and reports rather than silently truncates).
const maxDepth = 12

// Why performs root-cause explanation: it walks backward from effect
// through the causal graph, returning every path found (up to maxDepth
// hops), ranked by combined strength (product of edge strengths along
// the path), highest first. This is read-only and side-effect free.
func (g *Graph) Why(effect NodeID) ([]CausalPath, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	var paths []CausalPath
	var walk func(node NodeID, chain []NodeID, strength float64, visited map[NodeID]bool) error
	walk = func(node NodeID, chain []NodeID, strength float64, visited map[NodeID]bool) error {
		if visited[node] {
			return fmt.Errorf("%w: revisited %s", ErrCycleDetected, node)
		}
		if len(chain) > maxDepth {
			return nil // depth-bounded, not an error — just stop extending this branch
		}
		visited = cloneVisited(visited)
		visited[node] = true

		causes := g.directCausesOf(node)
		if len(causes) == 0 {
			if len(chain) > 1 { // a real path with at least one hop
				full := append([]NodeID{}, chain...)
				paths = append(paths, CausalPath{Chain: full, Strength: strength})
			}
			return nil
		}
		for _, e := range causes {
			newChain := append([]NodeID{e.Cause}, chain...)
			if err := walk(e.Cause, newChain, strength*e.Strength, visited); err != nil {
				return err
			}
		}
		return nil
	}

	if err := walk(effect, []NodeID{effect}, 1.0, map[NodeID]bool{}); err != nil {
		return nil, err
	}
	sort.Slice(paths, func(i, j int) bool {
		if paths[i].Strength != paths[j].Strength {
			return paths[i].Strength > paths[j].Strength
		}
		return fmt.Sprint(paths[i].Chain) < fmt.Sprint(paths[j].Chain)
	})
	return paths, nil
}

func cloneVisited(v map[NodeID]bool) map[NodeID]bool {
	out := make(map[NodeID]bool, len(v)+1)
	for k, val := range v {
		out[k] = val
	}
	return out
}

// AggregateSupport combines every direct cause of an effect into one
// confidence figure via noisy-OR (same math as fusion.fuseGroup — several
// independent contributing causes strictly increase overall support).
func (g *Graph) AggregateSupport(effect NodeID) float64 {
	_, span := telemetry.StartSpan(context.Background(), "causal.AggregateSupport",
		telemetry.Attribute{Key: "effect", Value: string(effect)})
	defer span.End()

	g.mu.RLock()
	defer g.mu.RUnlock()
	causes := g.directCausesOf(effect)
	if len(causes) == 0 {
		return 0
	}
	logComplement := 0.0
	for _, e := range causes {
		logComplement += math.Log(1 - e.Strength)
	}
	return 1 - math.Exp(logComplement)
}

// WhatIf answers a counterfactual: "how much causal support would
// `effect` retain if `removedCause` were not a factor?" It never mutates
// the graph — both figures are computed from the same snapshot.
func (g *Graph) WhatIf(effect NodeID, removedCause NodeID) (before, after float64) {
	g.mu.RLock()
	causes := g.directCausesOf(effect)
	g.mu.RUnlock()

	logBefore, logAfter := 0.0, 0.0
	for _, e := range causes {
		logBefore += math.Log(1 - e.Strength)
		if e.Cause != removedCause {
			logAfter += math.Log(1 - e.Strength)
		}
	}
	before = 1 - math.Exp(logBefore)
	if len(causes) == 0 {
		return 0, 0
	}
	after = 1 - math.Exp(logAfter)
	return before, after
}

func hashObservation(r ObservationRecord) string {
	raw := fmt.Sprintf("idx=%d|actor=%s|cause=%s|effect=%s|strength=%.6f|lag=%d|cond=%s|tick=%d|prev=%s",
		r.Index, r.ActorID, r.Cause, r.Effect, r.Strength, r.Lag, r.Condition, r.Tick, r.PrevHash)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// VerifyChain re-derives every observation's hash and confirms the chain
// is unbroken.
func (g *Graph) VerifyChain() error {
	g.mu.RLock()
	defer g.mu.RUnlock()
	prev := ""
	for _, r := range g.log {
		if r.PrevHash != prev {
			return ErrTamperDetected
		}
		if hashObservation(r) != r.Hash {
			return ErrTamperDetected
		}
		prev = r.Hash
	}
	return nil
}

// ReplayAll returns a defensive copy of the observation log.
func (g *Graph) ReplayAll() []ObservationRecord {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]ObservationRecord, len(g.log))
	copy(out, g.log)
	return out
}

// Head returns the current chain head hash ("" if no observations yet).
func (g *Graph) Head() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.head
}

// Rebuild reconstructs a Graph purely from an observation log, verifying
// the chain as it goes — the causal-layer replay/audit entrypoint.
func Rebuild(log []ObservationRecord) (*Graph, error) {
	g := NewGraph()
	prev := ""
	for i, r := range log {
		if r.Index != uint64(i) {
			return nil, fmt.Errorf("%w: index gap at %d", ErrTamperDetected, i)
		}
		if r.PrevHash != prev {
			return nil, fmt.Errorf("%w: broken chain linkage at index %d", ErrTamperDetected, r.Index)
		}
		if hashObservation(r) != r.Hash {
			return nil, fmt.Errorf("%w: hash mismatch at index %d", ErrTamperDetected, r.Index)
		}
		if _, err := g.Observe(r.ActorID, r.Cause, r.Effect, r.Strength, r.Lag, r.Condition, r.Tick); err != nil {
			return nil, err
		}
		prev = r.Hash
	}
	return g, nil
}
