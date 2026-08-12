// Package simulation closes PHASE 6 of the v7.10.3 production-readiness
// audit: "causal dan digital twin sudah ada tetapi integrasinya belum
// penuh."
//
// The audit's required chain is
//
//	Evidence -> Truth -> Causal Graph -> Counterfactual -> Digital Twin
//	-> Simulation -> Economic Consequence -> Decision
//
// and its required constraint is absolute:
//
//	"Tidak boleh ada simulation result tanpa lineage."
//
// That constraint is why this package exists as a type rather than as
// three helper functions. Result carries Cause, Intervention,
// Assumptions, AffectedNodes, PredictedOutcome, Confidence, Evidence
// and ReplayID, and there is no constructor that can produce a Result
// without them: Simulate* all route through finalize(), which refuses
// to return a result whose lineage is incomplete
// (ErrLineageIncomplete). A simulation you cannot trace is not a
// weaker simulation; it is not a simulation VERIQO will emit.
//
// The three entry points answer three genuinely different questions:
//
//   - SimulateDecision      : "if we take this action, what happens?"
//     (forward projection under the twin's current
//     causal state)
//   - SimulateCounterfactual: "what would have happened WITHOUT this
//     cause?" (Pearl rung 3 — uses the causal
//     graph's WhatIf to remove a cause and
//     re-project)
//   - SimulateCausalIntervention: "if we do(X), what happens?" (Pearl
//     rung 2 — an intervention that ADDS or
//     strengthens a cause rather than removing it)
package simulation

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"

	"veriqo/pkg/moat/causal"
	"veriqo/pkg/moat/digitaltwin"
)

// Errors.
var (
	ErrLineageIncomplete = errors.New("simulation: refusing to emit a result without complete lineage")
	ErrUnknownEntity     = errors.New("simulation: entity has no digital twin")
	ErrNoHorizon         = errors.New("simulation: horizon must be at least one step")
	ErrEmptyCause        = errors.New("simulation: an intervention must name a cause")
)

// Kind distinguishes the three rungs of the causal ladder this package
// supports.
type Kind string

const (
	KindDecision       Kind = "DECISION_PROJECTION" // association/prediction
	KindIntervention   Kind = "CAUSAL_INTERVENTION" // do(X)
	KindCounterfactual Kind = "COUNTERFACTUAL"      // what if X had not happened
)

// Point is one projected step.
type Point struct {
	Step  uint64  `json:"step"`
	Value float64 `json:"value"`
}

// Result is the lineage-complete output of one simulation.
type Result struct {
	Kind         Kind   `json:"kind"`
	Entity       string `json:"entity"`
	Cause        string `json:"cause"`
	Intervention string `json:"intervention"`

	Assumptions      []string `json:"assumptions"`
	AffectedNodes    []string `json:"affected_nodes"`
	PredictedOutcome []Point  `json:"predicted_outcome"`
	BaselineOutcome  []Point  `json:"baseline_outcome"`
	// Delta is the end-of-horizon difference between the simulated and
	// baseline trajectories — the number a decision layer consumes.
	Delta      float64 `json:"delta"`
	Confidence float64 `json:"confidence"`
	// Evidence lists the evidence/record identifiers this simulation
	// rests on (causal observation hashes, twin ledger head).
	Evidence []string `json:"evidence"`
	// ReplayID is content-addressed over every field above, so a
	// simulation is reproducible and tamper-evident like every other
	// VERIQO artifact.
	ReplayID string `json:"replay_id"`
}

// Params configures a projection.
type Params struct {
	Horizon uint64
	// BaseTarget is the starting value being projected (e.g. an
	// expected compliance score, a projected exposure).
	BaseTarget float64
	// DecayPerStep in [0,1) dampens the causal effect over the horizon.
	DecayPerStep float64
}

// Engine composes the causal graph and the twin registry. It owns
// neither: both are the live shared instances from the canonical
// pipeline, so a simulation reflects exactly the same causal beliefs
// the decision path used.
type Engine struct {
	Causal *causal.Graph
	Twins  *digitaltwin.Registry
}

// New constructs a simulation engine over live engines.
func New(c *causal.Graph, t *digitaltwin.Registry) *Engine { return &Engine{Causal: c, Twins: t} }

// SimulateDecision projects the consequence of acting on a decision
// under the entity's current causal state.
func (e *Engine) SimulateDecision(entity digitaltwin.EntityID, policyName string, effect causal.NodeID, p Params) (Result, error) {
	if p.Horizon == 0 {
		return Result{}, ErrNoHorizon
	}
	twin, ok := e.Twins.Get(entity)
	if !ok {
		return Result{}, fmt.Errorf("%w: %s", ErrUnknownEntity, entity)
	}
	support := e.Causal.AggregateSupport(effect)
	base := project(p.BaseTarget, 0, p)
	pred := project(p.BaseTarget, support, p)
	nodes, evidence, assumptions := e.lineage(effect, twin)
	return e.finalize(Result{
		Kind: KindDecision, Entity: string(entity), Cause: string(effect),
		Intervention:  "apply policy " + policyName,
		Assumptions:   append(assumptions, fmt.Sprintf("aggregate causal support for %s held at %.6f over the horizon", effect, support)),
		AffectedNodes: nodes, PredictedOutcome: pred, BaselineOutcome: base,
		Confidence: confidenceFromSupport(support), Evidence: evidence,
	})
}

// SimulateCausalIntervention answers do(X): a cause is forced active
// at a given strength and the projection is recomputed.
func (e *Engine) SimulateCausalIntervention(entity digitaltwin.EntityID, cause, effect causal.NodeID, strength float64, p Params) (Result, error) {
	if p.Horizon == 0 {
		return Result{}, ErrNoHorizon
	}
	if cause == "" {
		return Result{}, ErrEmptyCause
	}
	twin, ok := e.Twins.Get(entity)
	if !ok {
		return Result{}, fmt.Errorf("%w: %s", ErrUnknownEntity, entity)
	}
	baseSupport := e.Causal.AggregateSupport(effect)
	// do(X) at strength s composes with existing support by noisy-OR:
	// an intervention corroborates, it does not simply replace.
	doSupport := baseSupport + clamp01(strength)*(1-baseSupport)
	base := project(p.BaseTarget, baseSupport, p)
	pred := project(p.BaseTarget, doSupport, p)
	nodes, evidence, assumptions := e.lineage(effect, twin)
	return e.finalize(Result{
		Kind: KindIntervention, Entity: string(entity), Cause: string(cause),
		Intervention: fmt.Sprintf("do(%s := %.4f)", cause, clamp01(strength)),
		Assumptions: append(assumptions,
			"the intervention is exogenous: setting the cause does not itself change the other edges",
			fmt.Sprintf("support moves %.6f -> %.6f under noisy-OR composition", baseSupport, doSupport)),
		AffectedNodes: nodes, PredictedOutcome: pred, BaselineOutcome: base,
		Confidence: confidenceFromSupport(doSupport), Evidence: evidence,
	})
}

// SimulateCounterfactual answers "what would have happened without
// this cause" by removing it from the causal graph's aggregation.
func (e *Engine) SimulateCounterfactual(entity digitaltwin.EntityID, removedCause, effect causal.NodeID, p Params) (Result, error) {
	if p.Horizon == 0 {
		return Result{}, ErrNoHorizon
	}
	if removedCause == "" {
		return Result{}, ErrEmptyCause
	}
	twin, ok := e.Twins.Get(entity)
	if !ok {
		return Result{}, fmt.Errorf("%w: %s", ErrUnknownEntity, entity)
	}
	before, after := e.Causal.WhatIf(effect, removedCause)
	base := project(p.BaseTarget, before, p)
	pred := project(p.BaseTarget, after, p)
	nodes, evidence, assumptions := e.lineage(effect, twin)
	return e.finalize(Result{
		Kind: KindCounterfactual, Entity: string(entity), Cause: string(removedCause),
		Intervention: fmt.Sprintf("remove(%s)", removedCause),
		Assumptions: append(assumptions,
			"all other causes and their strengths are held exactly as observed",
			fmt.Sprintf("removing %s moves aggregate support %.6f -> %.6f", removedCause, before, after)),
		AffectedNodes: nodes, PredictedOutcome: pred, BaselineOutcome: base,
		Confidence: confidenceFromSupport(after), Evidence: evidence,
	})
}

// lineage collects the affected causal nodes, the evidence identifiers
// and the standing assumptions for an effect.
func (e *Engine) lineage(effect causal.NodeID, twin digitaltwin.Twin) (nodes, evidence, assumptions []string) {
	paths, err := e.Causal.Why(effect)
	if err == nil {
		seen := map[string]bool{}
		for _, p := range paths {
			for _, n := range p.Chain {
				if !seen[string(n)] {
					seen[string(n)] = true
					nodes = append(nodes, string(n))
				}
			}
		}
	}
	sort.Strings(nodes)
	if len(nodes) == 0 {
		nodes = []string{string(effect)}
	}
	for _, rec := range e.Causal.ReplayAll() {
		if rec.Effect == effect {
			evidence = append(evidence, "causal:"+rec.Hash)
		}
	}
	evidence = append(evidence, "twin_head:"+e.Twins.Head(), "twin_entity:"+string(twin.Entity))
	sort.Strings(evidence)
	assumptions = []string{
		"the causal graph is complete for this effect: unobserved confounders are assumed absent",
		"projection is deterministic given the recorded causal strengths; no stochastic sampling is used",
	}
	return nodes, evidence, assumptions
}

// finalize enforces the lineage rule and content-addresses the result.
func (e *Engine) finalize(r Result) (Result, error) {
	if r.Cause == "" || r.Intervention == "" || len(r.Assumptions) == 0 ||
		len(r.AffectedNodes) == 0 || len(r.PredictedOutcome) == 0 || len(r.Evidence) == 0 {
		return Result{}, ErrLineageIncomplete
	}
	if n := len(r.PredictedOutcome); n > 0 && len(r.BaselineOutcome) == n {
		r.Delta = r.PredictedOutcome[n-1].Value - r.BaselineOutcome[n-1].Value
	}
	r.ReplayID = replayID(r)
	return r, nil
}

func replayID(r Result) string {
	h := sha256.New()
	fmt.Fprintf(h, "veriqo.simulation/v1|kind=%s|entity=%s|cause=%s|iv=%s|conf=%.9f|delta=%.9f|",
		r.Kind, r.Entity, r.Cause, r.Intervention, r.Confidence, r.Delta)
	for _, n := range r.AffectedNodes {
		fmt.Fprintf(h, "node=%s|", n)
	}
	for _, a := range r.Assumptions {
		fmt.Fprintf(h, "assume=%s|", a)
	}
	for _, ev := range r.Evidence {
		fmt.Fprintf(h, "ev=%s|", ev)
	}
	for _, p := range r.PredictedOutcome {
		fmt.Fprintf(h, "p%d=%.9f|", p.Step, p.Value)
	}
	for _, p := range r.BaselineOutcome {
		fmt.Fprintf(h, "b%d=%.9f|", p.Step, p.Value)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// VerifyResult independently recomputes a result's ReplayID.
func VerifyResult(r Result) error {
	if replayID(r) != r.ReplayID {
		return errors.New("simulation: result hash does not match its own fields")
	}
	return nil
}

// project applies causal support to a base target over a horizon with
// per-step decay. Pure and deterministic.
func project(base, support float64, p Params) []Point {
	out := make([]Point, 0, p.Horizon)
	effect := clamp01(support)
	for step := uint64(1); step <= p.Horizon; step++ {
		decay := 1.0
		for i := uint64(1); i < step; i++ {
			decay *= (1 - clamp01(p.DecayPerStep))
		}
		out = append(out, Point{Step: step, Value: base * (1 + effect*decay)})
	}
	return out
}

func confidenceFromSupport(s float64) float64 {
	// Confidence in a projection is bounded by how much causal support
	// actually backs it, and never reaches 1: a model is never certain.
	return 0.5 + 0.49*clamp01(s)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
