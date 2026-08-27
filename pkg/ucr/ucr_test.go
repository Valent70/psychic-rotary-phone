package ucr_test

import (
	"testing"

	"veriqo/pkg/core"
	"veriqo/pkg/ucr"
)

// ─── Working Memory ───────────────────────────────────────────────────────────

func TestWorkingMemory_AssertAndGet(t *testing.T) {
	wm := ucr.NewWorkingMemory()
	wm.Assert("vessel.risk", "HIGH", 0.9, "ais-feed")
	f, ok := wm.Get("vessel.risk")
	if !ok {
		t.Fatal("expected fact to exist")
	}
	if f.Value != "HIGH" || f.Confidence != 0.9 {
		t.Fatalf("unexpected fact: %+v", f)
	}
}

func TestWorkingMemory_Retract(t *testing.T) {
	wm := ucr.NewWorkingMemory()
	wm.Assert("x", 1, 1.0, "test")
	wm.Retract("x")
	_, ok := wm.Get("x")
	if ok {
		t.Fatal("retracted fact should be gone")
	}
}

func TestWorkingMemory_ConfidenceClamp(t *testing.T) {
	wm := ucr.NewWorkingMemory()
	wm.Assert("overconfident", "v", 99.0, "src")
	f, _ := wm.Get("overconfident")
	if f.Confidence > 1.0 {
		t.Fatalf("confidence should be clamped to 1.0, got %f", f.Confidence)
	}
}

func TestWorkingMemory_Snapshot(t *testing.T) {
	wm := ucr.NewWorkingMemory()
	wm.Assert("a", 1, 0.8, "s1")
	wm.Assert("b", 2, 0.5, "s2")
	snap := wm.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected 2 facts in snapshot, got %d", len(snap))
	}
}

// ─── Reasoning Graph ─────────────────────────────────────────────────────────

func TestReasoningGraph_AddAndReachable(t *testing.T) {
	g := ucr.NewReasoningGraph()
	g.AddNode(ucr.ReasoningNode{ID: "A", Kind: ucr.NodeFact, Label: "vessel-sanctioned", Confidence: 0.95})
	g.AddNode(ucr.ReasoningNode{ID: "B", Kind: ucr.NodeRule, Label: "sanction-rule", Confidence: 1.0})
	g.AddNode(ucr.ReasoningNode{ID: "C", Kind: ucr.NodeConclusion, Label: "flag-for-review", Confidence: 0.9})
	g.AddEdge("A", "B", "triggers", 1.0)
	g.AddEdge("B", "C", "produces", 1.0)

	reachable := g.Reachable("A")
	if len(reachable) != 3 {
		t.Fatalf("expected 3 reachable nodes, got %d", len(reachable))
	}
}

func TestReasoningGraph_NoCycle(t *testing.T) {
	g := ucr.NewReasoningGraph()
	g.AddNode(ucr.ReasoningNode{ID: "X", Kind: ucr.NodeFact, Label: "X"})
	g.AddNode(ucr.ReasoningNode{ID: "Y", Kind: ucr.NodeFact, Label: "Y"})
	g.AddEdge("X", "Y", "implies", 1.0)
	g.AddEdge("Y", "X", "implies", 1.0) // cycle: DFS must not infinite-loop

	reachable := g.Reachable("X")
	if len(reachable) == 0 {
		t.Fatal("expected some reachable nodes")
	}
}

// ─── Ontology Cache ───────────────────────────────────────────────────────────

func TestOntologyCache_IsA(t *testing.T) {
	o := ucr.NewOntologyCache()
	o.Register(ucr.Concept{ID: "vessel", Name: "Vessel"})
	o.Register(ucr.Concept{ID: "tanker", Name: "Tanker", Parents: []string{"vessel"}})
	o.Register(ucr.Concept{ID: "vlcc", Name: "VLCC", Parents: []string{"tanker"}})

	if !o.IsA("vlcc", "vessel") {
		t.Fatal("vlcc should be a vessel (transitive)")
	}
	if !o.IsA("tanker", "vessel") {
		t.Fatal("tanker should be a vessel")
	}
	if o.IsA("vessel", "vlcc") {
		t.Fatal("vessel should NOT be a vlcc")
	}
}

func TestOntologyCache_Get(t *testing.T) {
	o := ucr.NewOntologyCache()
	o.Register(ucr.Concept{ID: "port", Name: "Port", Domain: "maritime"})
	c, ok := o.Get("port")
	if !ok || c.Name != "Port" {
		t.Fatal("expected to get registered concept")
	}
}

// ─── Causal Planner ───────────────────────────────────────────────────────────

func TestCausalPlanner_FindsPlan(t *testing.T) {
	actions := []ucr.Action{
		{
			Name:          "flag-vessel",
			Preconditions: ucr.State{"vessel.sanctioned": true, "vessel.flagged": false},
			AddEffects:    ucr.State{"vessel.flagged": true},
			Cost:          1.0,
		},
		{
			Name:          "notify-authority",
			Preconditions: ucr.State{"vessel.flagged": true},
			AddEffects:    ucr.State{"authority.notified": true},
			Cost:          1.0,
		},
	}
	planner := ucr.NewCausalPlanner(actions)

	start := ucr.State{"vessel.sanctioned": true, "vessel.flagged": false}
	goal := ucr.State{"authority.notified": true}

	plan, err := planner.Plan(start, goal, 10)
	if err != nil {
		t.Fatalf("expected plan, got error: %v", err)
	}
	if len(plan.Actions) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(plan.Actions))
	}
	if plan.Actions[0].Name != "flag-vessel" {
		t.Errorf("first action should be flag-vessel, got %s", plan.Actions[0].Name)
	}
}

func TestCausalPlanner_NoPath(t *testing.T) {
	planner := ucr.NewCausalPlanner(nil) // no actions
	start := ucr.State{"a": true}
	goal := ucr.State{"b": true}
	_, err := planner.Plan(start, goal, 5)
	if err == nil {
		t.Fatal("expected ErrNoPath")
	}
}

func TestCausalPlanner_AlreadySatisfied(t *testing.T) {
	planner := ucr.NewCausalPlanner(nil)
	start := ucr.State{"goal": true}
	goal := ucr.State{"goal": true}
	plan, err := planner.Plan(start, goal, 5)
	if err != nil {
		t.Fatalf("expected empty plan, got error: %v", err)
	}
	if len(plan.Actions) != 0 {
		t.Fatalf("expected 0 steps, got %d", len(plan.Actions))
	}
}

// ─── Uncertainty Propagation ──────────────────────────────────────────────────

func TestUncertaintyModel_BayesianUpdate(t *testing.T) {
	m := ucr.NewUncertaintyModel()
	m.SetPrior("sanctioned", 0.3)
	posterior := m.BayesianUpdate("sanctioned", 0.9, 0.5)
	// P(sanctioned|evidence) = (0.9 × 0.3) / 0.5 = 0.54
	if posterior < 0.4 || posterior > 0.7 {
		t.Errorf("bayesian update out of expected range: %f", posterior)
	}
}

func TestUncertaintyModel_Propagate(t *testing.T) {
	m := ucr.NewUncertaintyModel()
	ev := map[string]float64{"ais": 0.9, "sar": 0.8, "manifest": 0.7}
	combined := m.Propagate(ev)
	if combined <= 0 || combined > 1 {
		t.Errorf("propagated confidence out of range: %f", combined)
	}
}

func TestUncertaintyModel_PropagateEmpty(t *testing.T) {
	m := ucr.NewUncertaintyModel()
	if m.Propagate(nil) != 0 {
		t.Fatal("empty propagation should return 0")
	}
}

// ─── Explanation Graph ────────────────────────────────────────────────────────

func TestExplanationGraph_Summary(t *testing.T) {
	eg := &ucr.ExplanationGraph{ConclusionID: "sanction-detected"}
	eg.AddStep("n1", "AIS Anomaly", "rule-sanction-port", "vessel visited sanctioned port", 0.9)
	eg.AddStep("n2", "Manifest Check", "rule-cargo-mismatch", "cargo manifest mismatch", 0.7)
	summary := eg.Summary()
	if len(summary) == 0 {
		t.Fatal("explanation summary should not be empty")
	}
	if len(eg.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(eg.Steps))
	}
}

// ─── Engine integration ───────────────────────────────────────────────────────

func TestEngine_Decide(t *testing.T) {
	e := ucr.NewEngine(nil)
	e.Memory().Assert("vessel.id", "IMO1234567", 1.0, "registry")

	g := ucr.NewReasoningGraph()
	g.AddNode(ucr.ReasoningNode{ID: "c1", Kind: ucr.NodeConclusion, Label: "high-risk"})

	eg := &ucr.ExplanationGraph{ConclusionID: "sanction-verdict"}
	eg.AddStep("c1", "Sanction DB", "sanction-match", "matched in OFAC list", 0.99)

	trace := e.Decide(
		core.NewTraceID(),
		map[string]any{"vessel": "IMO1234567"},
		g, eg,
		"SANCTIONED",
		0.99,
	)
	if trace.Conclusion != "SANCTIONED" || trace.Confidence < 0.98 {
		t.Fatalf("unexpected decision trace: %+v", trace)
	}
}

// ─── Benchmarks ───────────────────────────────────────────────────────────────

func BenchmarkWorkingMemory_Assert(b *testing.B) {
	wm := ucr.NewWorkingMemory()
	b.ResetTimer()
	for i := range b.N {
		wm.Assert(string(rune('a'+(i%26))), i, 0.9, "bench")
	}
}

func BenchmarkCausalPlanner_Plan(b *testing.B) {
	actions := make([]ucr.Action, 10)
	for i := range actions {
		actions[i] = ucr.Action{
			Name:          "action" + string(rune('a'+i)),
			Preconditions: ucr.State{string(rune('a' + i)): false},
			AddEffects:    ucr.State{string(rune('a' + i)): true},
			Cost:          1,
		}
	}
	planner := ucr.NewCausalPlanner(actions)
	start := ucr.State{"a": false}
	goal := ucr.State{"a": true}
	b.ResetTimer()
	for range b.N {
		_, _ = planner.Plan(start, goal, 5)
	}
}
