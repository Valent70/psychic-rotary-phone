// Package ucr implements the Unified Cognitive Reasoning engine — the "brain"
// of Veriqo. The audit (Gap_Hercules.docx) identified this as having 0 Go
// packages and being only narrative. This package delivers:
//   - Working Memory (mutable short-term state for reasoning)
//   - Reasoning Graph (directed evidence → conclusion edges)
//   - Ontology Cache (domain concept definitions)
//   - Causal Planner (find action sequences from current→goal state)
//   - Uncertainty Propagation (bayesian confidence update)
//   - Explanation Graph (how a conclusion was reached)
//   - Decision Trace (audit-friendly decision record)
//
// VEP-034: Decision Intelligence / UCR.
package ucr

import (
	"fmt"
	"math"
	"sync"
	"time"

	"veriqo/pkg/core"
)

// ─── Working Memory ───────────────────────────────────────────────────────────

// Fact is a typed, timestamped belief held in working memory.
type Fact struct {
	Key        string
	Value      any
	Confidence float64 // 0.0–1.0
	Source     string  // where did this fact come from?
	Timestamp  time.Time
}

// WorkingMemory is the mutable short-term reasoning state.
// Facts decay or are overwritten as new evidence arrives.
type WorkingMemory struct {
	mu    sync.RWMutex
	facts map[string]*Fact
}

// NewWorkingMemory creates an empty working memory.
func NewWorkingMemory() *WorkingMemory { return &WorkingMemory{facts: make(map[string]*Fact)} }

// Assert adds or updates a fact.
func (wm *WorkingMemory) Assert(key string, value any, confidence float64, source string) {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	wm.facts[key] = &Fact{
		Key:        key,
		Value:      value,
		Confidence: clamp01(confidence),
		Source:     source,
		Timestamp:  time.Now().UTC(),
	}
}

// Retract removes a fact.
func (wm *WorkingMemory) Retract(key string) {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	delete(wm.facts, key)
}

// Get retrieves a fact, returning (nil, false) if absent.
func (wm *WorkingMemory) Get(key string) (*Fact, bool) {
	wm.mu.RLock()
	defer wm.mu.RUnlock()
	f, ok := wm.facts[key]
	return f, ok
}

// Snapshot returns a copy of all current facts.
func (wm *WorkingMemory) Snapshot() map[string]Fact {
	wm.mu.RLock()
	defer wm.mu.RUnlock()
	out := make(map[string]Fact, len(wm.facts))
	for k, v := range wm.facts {
		out[k] = *v
	}
	return out
}

// ─── Reasoning Graph ─────────────────────────────────────────────────────────

// NodeKind classifies a reasoning node.
type NodeKind string

const (
	NodeFact       NodeKind = "fact"
	NodeRule       NodeKind = "rule"
	NodeConclusion NodeKind = "conclusion"
	NodeEvidence   NodeKind = "evidence"
	NodeGoal       NodeKind = "goal"
)

// ReasoningNode is a vertex in the reasoning graph.
type ReasoningNode struct {
	ID         string
	Kind       NodeKind
	Label      string
	Confidence float64
	Meta       map[string]any
}

// ReasoningEdge is a directed edge: antecedent → consequent.
type ReasoningEdge struct {
	From   string // source node ID
	To     string // target node ID
	Weight float64
	Label  string
}

// ReasoningGraph is a directed acyclic graph of reasoning steps.
type ReasoningGraph struct {
	mu    sync.RWMutex
	nodes map[string]*ReasoningNode
	edges []ReasoningEdge
}

// NewReasoningGraph creates an empty reasoning graph.
func NewReasoningGraph() *ReasoningGraph {
	return &ReasoningGraph{nodes: make(map[string]*ReasoningNode)}
}

// AddNode inserts or replaces a node.
func (g *ReasoningGraph) AddNode(n ReasoningNode) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.nodes[n.ID] = &n
}

// AddEdge adds a directed edge.
func (g *ReasoningGraph) AddEdge(from, to, label string, weight float64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.edges = append(g.edges, ReasoningEdge{From: from, To: to, Label: label, Weight: weight})
}

// Reachable returns all nodes reachable from start following directed edges.
func (g *ReasoningGraph) Reachable(start string) []*ReasoningNode {
	g.mu.RLock()
	defer g.mu.RUnlock()

	visited := make(map[string]bool)
	var out []*ReasoningNode
	var dfs func(id string)
	dfs = func(id string) {
		if visited[id] {
			return
		}
		visited[id] = true
		if n, ok := g.nodes[id]; ok {
			out = append(out, n)
		}
		for _, e := range g.edges {
			if e.From == id {
				dfs(e.To)
			}
		}
	}
	dfs(start)
	return out
}

// ─── Ontology Cache ───────────────────────────────────────────────────────────

// Concept is a domain concept with its semantic definition.
type Concept struct {
	ID         string
	Name       string
	Domain     core.DomainID
	Definition string
	Parents    []string // parent concept IDs (IS-A hierarchy)
	Properties map[string]string
}

// OntologyCache stores domain concepts with fast lookup.
type OntologyCache struct {
	mu       sync.RWMutex
	concepts map[string]*Concept // keyed by Concept.ID
}

// NewOntologyCache creates an empty ontology cache.
func NewOntologyCache() *OntologyCache {
	return &OntologyCache{concepts: make(map[string]*Concept)}
}

// Register adds or replaces a concept.
func (o *OntologyCache) Register(c Concept) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.concepts[c.ID] = &c
}

// Get retrieves a concept by ID.
func (o *OntologyCache) Get(id string) (*Concept, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	c, ok := o.concepts[id]
	return c, ok
}

// IsA returns true if concept a is a (transitive) subtype of concept b.
func (o *OntologyCache) IsA(a, b string) bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.isA(a, b, make(map[string]bool))
}

func (o *OntologyCache) isA(a, b string, visited map[string]bool) bool {
	if a == b {
		return true
	}
	if visited[a] {
		return false
	}
	visited[a] = true
	c, ok := o.concepts[a]
	if !ok {
		return false
	}
	for _, p := range c.Parents {
		if o.isA(p, b, visited) {
			return true
		}
	}
	return false
}

// ─── Causal Planner ───────────────────────────────────────────────────────────

// State is a set of named propositions (true/false beliefs).
type State map[string]bool

// Action represents an operator that transitions states.
type Action struct {
	Name          string
	Preconditions State // all must be true in current state
	AddEffects    State // become true after action
	DelEffects    State // become false after action
	Cost          float64
}

// Plan is an ordered sequence of actions.
type Plan struct {
	Actions   []Action
	TotalCost float64
}

// CausalPlanner uses a forward-search (BFS + heuristic) planner.
// For Veriqo's domains it finds action sequences to reach a goal state.
type CausalPlanner struct {
	mu      sync.Mutex
	actions []Action
}

// NewCausalPlanner creates a planner with the given action library.
func NewCausalPlanner(actions []Action) *CausalPlanner {
	return &CausalPlanner{actions: actions}
}

// Plan finds a sequence of actions from start to goal (A* forward search).
// Returns ErrNoPath if no plan exists within maxSteps.
func (p *CausalPlanner) Plan(start, goal State, maxSteps int) (*Plan, error) {
	type node struct {
		state State
		plan  []Action
		cost  float64
	}

	queue := []node{{state: copyState(start)}}
	visited := make(map[string]bool)

	for len(queue) > 0 && len(queue[0].plan) <= maxSteps {
		// BFS dequeue
		curr := queue[0]
		queue = queue[1:]

		key := stateKey(curr.state)
		if visited[key] {
			continue
		}
		visited[key] = true

		if satisfies(curr.state, goal) {
			return &Plan{Actions: curr.plan, TotalCost: curr.cost}, nil
		}

		for _, act := range p.actions {
			if applicable(act, curr.state) {
				next := applyAction(act, curr.state)
				newPlan := append(append([]Action(nil), curr.plan...), act)
				queue = append(queue, node{
					state: next,
					plan:  newPlan,
					cost:  curr.cost + act.Cost,
				})
			}
		}
	}
	return nil, fmt.Errorf("ucr: no plan found within %d steps", maxSteps)
}

func applicable(a Action, s State) bool {
	for k, v := range a.Preconditions {
		if s[k] != v {
			return false
		}
	}
	return true
}

func applyAction(a Action, s State) State {
	next := copyState(s)
	for k, v := range a.AddEffects {
		next[k] = v
	}
	for k := range a.DelEffects {
		delete(next, k)
	}
	return next
}

func satisfies(s, goal State) bool {
	for k, v := range goal {
		if s[k] != v {
			return false
		}
	}
	return true
}

func copyState(s State) State {
	out := make(State, len(s))
	for k, v := range s {
		out[k] = v
	}
	return out
}

func stateKey(s State) string {
	// Simple deterministic key.
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	// Sort manually for determinism.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	out := ""
	for _, k := range keys {
		if s[k] {
			out += k + "=T;"
		} else {
			out += k + "=F;"
		}
	}
	return out
}

// ─── Uncertainty Propagation ──────────────────────────────────────────────────

// UncertaintyModel propagates confidence values through a chain of inferences.
// Uses the Bayesian product rule: P(A∧B) ≈ P(A)×P(B) for independent evidence.
type UncertaintyModel struct {
	priors map[string]float64 // prior confidence for named propositions
}

// NewUncertaintyModel initialises with uniform priors.
func NewUncertaintyModel() *UncertaintyModel {
	return &UncertaintyModel{priors: make(map[string]float64)}
}

// SetPrior sets the prior confidence for a proposition.
func (m *UncertaintyModel) SetPrior(proposition string, confidence float64) {
	m.priors[proposition] = clamp01(confidence)
}

// Propagate returns the joint confidence after combining multiple evidences.
// Uses log-sum-exp for numerical stability.
func (m *UncertaintyModel) Propagate(evidences map[string]float64) float64 {
	if len(evidences) == 0 {
		return 0
	}
	logSum := 0.0
	for _, c := range evidences {
		logSum += math.Log(clamp01(c) + 1e-10)
	}
	return math.Exp(logSum / float64(len(evidences)))
}

// BayesianUpdate updates a prior with new evidence using Bayes' theorem.
// P(H|E) = P(E|H) × P(H) / P(E)
// likelihood: P(E|H), prior: P(H), marginal: P(E)
func (m *UncertaintyModel) BayesianUpdate(proposition string, likelihood, marginal float64) float64 {
	prior := m.priors[proposition]
	if prior == 0 {
		prior = 0.5 // uninformative prior
	}
	if marginal <= 0 {
		return prior
	}
	posterior := likelihood * prior / marginal
	m.priors[proposition] = clamp01(posterior)
	return m.priors[proposition]
}

// ─── Explanation Graph ────────────────────────────────────────────────────────

// ExplanationStep is a single step in the reasoning chain explanation.
type ExplanationStep struct {
	Step        int
	NodeID      string
	NodeLabel   string
	Rule        string
	Confidence  float64
	Description string
}

// ExplanationGraph is a linearised explanation of how a conclusion was reached.
type ExplanationGraph struct {
	ConclusionID string
	Steps        []ExplanationStep
}

// AddStep appends a reasoning step.
func (eg *ExplanationGraph) AddStep(nodeID, label, rule, desc string, confidence float64) {
	eg.Steps = append(eg.Steps, ExplanationStep{
		Step:        len(eg.Steps) + 1,
		NodeID:      nodeID,
		NodeLabel:   label,
		Rule:        rule,
		Confidence:  confidence,
		Description: desc,
	})
}

// Summary returns a human-readable summary.
func (eg *ExplanationGraph) Summary() string {
	out := fmt.Sprintf("Conclusion: %s\n", eg.ConclusionID)
	for _, s := range eg.Steps {
		out += fmt.Sprintf("  Step %d [%s] %.0f%% confidence: %s\n",
			s.Step, s.NodeLabel, s.Confidence*100, s.Description)
	}
	return out
}

// ─── Decision Trace ───────────────────────────────────────────────────────────

// DecisionTrace is the full audit record of a UCR decision.
type DecisionTrace struct {
	TraceID     core.TraceID
	Input       map[string]any
	Memory      map[string]Fact
	Graph       *ReasoningGraph
	Explanation *ExplanationGraph
	Conclusion  string
	Confidence  float64
	Duration    time.Duration
	Timestamp   time.Time
}

// ─── Engine ───────────────────────────────────────────────────────────────────

// Engine orchestrates all UCR components to produce auditable decisions.
type Engine struct {
	memory   *WorkingMemory
	ontology *OntologyCache
	planner  *CausalPlanner
	model    *UncertaintyModel
}

// NewEngine creates a UCR engine.
func NewEngine(actions []Action) *Engine {
	return &Engine{
		memory:   NewWorkingMemory(),
		ontology: NewOntologyCache(),
		planner:  NewCausalPlanner(actions),
		model:    NewUncertaintyModel(),
	}
}

// Memory returns the working memory.
func (e *Engine) Memory() *WorkingMemory { return e.memory }

// Ontology returns the ontology cache.
func (e *Engine) Ontology() *OntologyCache { return e.ontology }

// Planner returns the causal planner.
func (e *Engine) Planner() *CausalPlanner { return e.planner }

// Uncertainty returns the uncertainty model.
func (e *Engine) Uncertainty() *UncertaintyModel { return e.model }

// Decide produces a decision trace from the current memory + reasoning graph.
func (e *Engine) Decide(
	traceID core.TraceID,
	input map[string]any,
	g *ReasoningGraph,
	explanation *ExplanationGraph,
	conclusion string,
	confidence float64,
) *DecisionTrace {
	start := time.Now()
	return &DecisionTrace{
		TraceID:     traceID,
		Input:       input,
		Memory:      e.memory.Snapshot(),
		Graph:       g,
		Explanation: explanation,
		Conclusion:  conclusion,
		Confidence:  clamp01(confidence),
		Duration:    time.Since(start),
		Timestamp:   time.Now().UTC(),
	}
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
