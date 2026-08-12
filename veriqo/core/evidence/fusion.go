// Package evidence is Veriqo's Evidence module — fusion, weighting,
// decay, ranking, and provenance over observations, built on
// pkg/evidence/graph.Graph for content-addressed lineage. It does not
// connect to AIS/SAR/satellite feeds itself (see pkg/dataplatform/ingest
// for the generic schema those future connectors plug into).
//
// Weighting now runs through a TrustProvider — satisfied by
// pkg/core/trustcalc.Calculus — instead of an isolated local trust
// table, so a source's Bayesian-calibrated reliability (learned by
// veriqo/core/intelligence from arbitration outcomes) is exactly what
// this engine uses to weigh that source's next observation. That is
// the "trust calculus... bekerja lintas seluruh sistem" wiring
// "Yang_masih_saya_kritisi.docx" §Tier4 and §3 asks for: one shared
// belief object, not two disconnected reputations.
package evidence

import (
	"fmt"
	"math"
	"sort"
	"sync"

	"veriqo/pkg/core/trustcalc"
	"veriqo/pkg/evidence/graph"
)

// Observation is one piece of evidence about one claim from one
// source, at one tick.
type Observation struct {
	ClaimKey   string
	SourceID   string
	Value      string
	Confidence float64 // in (0,1]: the source's own stated confidence
	Tick       uint64
}

// TrustProvider supplies a source's current reliability score in
// [0,1]. pkg/core/trustcalc.Calculus and the local SourceTrust below
// both satisfy this.
type TrustProvider interface {
	Score(sourceID string) float64
}

// SourceTrust is a minimal static TrustProvider for callers that don't
// want a full Bayesian calculus (e.g. isolated unit tests). Production
// wiring should prefer pkg/core/trustcalc.Calculus, shared with
// veriqo/core/intelligence, so trust learned from arbitration outcomes
// actually changes fusion weighting.
type SourceTrust struct {
	mu     sync.RWMutex
	scores map[string]float64
}

func NewSourceTrust() *SourceTrust { return &SourceTrust{scores: make(map[string]float64)} }

// namespacedTrust decorates a TrustProvider so every lookup is scoped
// under a fixed trustcalc namespace before reaching the inner
// provider — v7.10 audit fix ("core/intelligence.sourceTrust masih
// perlu diaudit dan kemungkinan namespace"). This is purely additive:
// Engine, TrustProvider and SourceTrust are unchanged, so every
// existing isolated test (constructing an Engine directly against a
// raw Calculus or a raw SourceTrust) keeps working exactly as before.
// Only veriqo/kernel.Kernel's production wiring uses this decorator,
// to keep Evidence reading from the SAME namespaced identity
// veriqo/core/intelligence.Loop writes to.
type namespacedTrust struct {
	inner     TrustProvider
	namespace string
}

// NewNamespacedTrust wraps inner so Score(id) actually queries
// inner.Score(trustcalc.NamespacedSubject(namespace, id)) — see
// trustcalc.NamespacedSubject for why this prevents identity
// collisions across the repo's distinct trust domains (intent actors,
// calibration sources, evidence/intelligence sources).
func NewNamespacedTrust(inner TrustProvider, namespace string) TrustProvider {
	return &namespacedTrust{inner: inner, namespace: namespace}
}

func (n *namespacedTrust) Score(sourceID string) float64 {
	return n.inner.Score(trustcalc.NamespacedSubject(n.namespace, sourceID))
}

func (t *SourceTrust) Set(sourceID string, score float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	t.scores[sourceID] = score
}

func (t *SourceTrust) Score(sourceID string) float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if s, ok := t.scores[sourceID]; ok {
		return s
	}
	return 1.0
}

// HalfLifeDecay: a weight halves every halfLifeTicks ticks of
// staleness. 0 disables decay.
func HalfLifeDecay(base float64, ticksSinceObserved uint64, halfLifeTicks uint64) float64 {
	if halfLifeTicks == 0 {
		return base
	}
	return base * math.Pow(0.5, float64(ticksSinceObserved)/float64(halfLifeTicks))
}

type WeightedObservation struct {
	Observation
	Weight float64
	NodeID graph.NodeID
}

type FusionResult struct {
	ClaimKey        string
	WinningValue    string
	Confidence      float64
	Ranked          []WeightedObservation
	ContestedValues int
	TotalWeight     float64
}

var ErrNoObservations = fmt.Errorf("evidence: no observations for claim")

// Engine is the Evidence module: an evidence graph for provenance plus
// a per-claim observation log for fusion.
type Engine struct {
	mu           sync.RWMutex
	graph        *graph.Graph
	trust        TrustProvider
	halfLife     uint64
	observations map[string][]WeightedObservation
	nodeIndex    map[graph.NodeID]Observation
}

// New builds an Evidence Engine. trust may be nil (defaults to
// NewSourceTrust(), full trust for every source) — production callers
// should pass a shared pkg/core/trustcalc.Calculus.
func New(trust TrustProvider, halfLifeTicks uint64) *Engine {
	if trust == nil {
		trust = NewSourceTrust()
	}
	return &Engine{
		graph:        graph.NewGraph(),
		trust:        trust,
		halfLife:     halfLifeTicks,
		observations: make(map[string][]WeightedObservation),
		nodeIndex:    make(map[graph.NodeID]Observation),
	}
}

func (e *Engine) Ingest(o Observation) (graph.NodeID, error) {
	if o.ClaimKey == "" || o.SourceID == "" {
		return "", fmt.Errorf("evidence: ClaimKey and SourceID are required")
	}
	if o.Confidence <= 0 || o.Confidence > 1 {
		return "", fmt.Errorf("evidence: Confidence must be in (0,1], got %v", o.Confidence)
	}
	payload := fmt.Sprintf("%s|%s|%s|%d", o.ClaimKey, o.SourceID, o.Value, o.Tick)
	nodeID := e.graph.AddNode("observation", payload)

	e.mu.Lock()
	defer e.mu.Unlock()
	baseWeight := o.Confidence * e.trust.Score(o.SourceID)
	wo := WeightedObservation{Observation: o, Weight: baseWeight, NodeID: nodeID}
	e.observations[o.ClaimKey] = append(e.observations[o.ClaimKey], wo)
	e.nodeIndex[nodeID] = o
	return nodeID, nil
}

func (e *Engine) Fuse(claimKey string, fusionTick uint64) (FusionResult, error) {
	e.mu.RLock()
	obs := append([]WeightedObservation(nil), e.observations[claimKey]...)
	e.mu.RUnlock()
	if len(obs) == 0 {
		return FusionResult{}, fmt.Errorf("%w: %s", ErrNoObservations, claimKey)
	}

	decayed := make([]WeightedObservation, len(obs))
	valueWeight := make(map[string]float64)
	var total float64
	for i, o := range obs {
		age := uint64(0)
		if fusionTick > o.Tick {
			age = fusionTick - o.Tick
		}
		w := HalfLifeDecay(o.Weight, age, e.halfLife)
		decayed[i] = o
		decayed[i].Weight = w
		valueWeight[o.Value] += w
		total += w
	}
	sort.Slice(decayed, func(i, j int) bool { return decayed[i].Weight > decayed[j].Weight })

	winningValue, winningWeight := "", -1.0
	for v, w := range valueWeight {
		if w > winningWeight {
			winningValue, winningWeight = v, w
		}
	}
	confidence := 0.0
	if total > 0 {
		confidence = winningWeight / total
	}
	return FusionResult{
		ClaimKey: claimKey, WinningValue: winningValue, Confidence: confidence,
		Ranked: decayed, ContestedValues: len(valueWeight) - 1, TotalWeight: total,
	}, nil
}

func (e *Engine) Provenance(id graph.NodeID) ([]graph.Node, error) { return e.graph.Lineage(id) }
func (e *Engine) Link(from, to graph.NodeID, relation graph.Relation) error {
	return e.graph.AddEdge(from, to, relation)
}
func (e *Engine) Graph() *graph.Graph { return e.graph }
