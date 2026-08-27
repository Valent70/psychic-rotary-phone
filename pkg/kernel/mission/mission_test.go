package mission

import (
	"testing"

	"veriqo/pkg/kernel/resource"
	"veriqo/pkg/kernel/runtime"
	"veriqo/pkg/kernel/selfheal"
)

type mc struct {
	name    string
	deps    []string
	healthy bool
}

func (c *mc) Name() string           { return c.name }
func (c *mc) Dependencies() []string { return c.deps }
func (c *mc) Init() error            { return nil }
func (c *mc) Start() error           { return nil }
func (c *mc) HealthCheck() selfheal.Health {
	if c.healthy {
		return selfheal.Healthy
	}
	return selfheal.Down
}
func (c *mc) Restart() error { c.healthy = true; return nil }
func (c *mc) Replay() error  { return nil }
func (c *mc) Stop() error    { return nil }

func TestGlobalStateAndSystemStatus(t *testing.T) {
	a := &mc{name: "A", healthy: true}
	b := &mc{name: "B", healthy: false}
	rt := runtime.New()
	rt.Boot(a, b)
	ctrl := New(rt, nil)

	gs := ctrl.GlobalState()
	if gs.Healthy != 1 || gs.Down != 1 {
		t.Fatalf("unexpected global state: %+v", gs)
	}
	if status := ctrl.SystemStatus(); status != "CRITICAL" {
		t.Fatalf("expected CRITICAL status with a Down component, got %s", status)
	}
}

func TestCriticalAlertListsDownComponents(t *testing.T) {
	a := &mc{name: "A", healthy: true}
	b := &mc{name: "B", healthy: false}
	rt := runtime.New()
	rt.Boot(a, b)
	ctrl := New(rt, nil)

	alerts := ctrl.CriticalAlert()
	if len(alerts) != 1 || alerts[0].Component != "B" || alerts[0].Severity != "CRITICAL" {
		t.Fatalf("unexpected alerts: %+v", alerts)
	}
}

func TestRecoveryClearsAlert(t *testing.T) {
	a := &mc{name: "A", healthy: false}
	rt := runtime.New()
	rt.Boot(a)
	ctrl := New(rt, nil)

	results := ctrl.Recovery(1)
	if !results["A"] {
		t.Fatalf("expected A recovered, got %v", results)
	}
	if len(ctrl.CriticalAlert()) != 0 {
		t.Fatalf("expected no alerts after recovery, got %v", ctrl.CriticalAlert())
	}
	if status := ctrl.SystemStatus(); status != "RUNNING" {
		t.Fatalf("expected RUNNING after recovery, got %s", status)
	}
}

func TestExecutionTimelineAndDependencyGraph(t *testing.T) {
	a := &mc{name: "A", healthy: true}
	b := &mc{name: "B", deps: []string{"A"}, healthy: true}
	rt := runtime.New()
	rt.Boot(a, b)
	ctrl := New(rt, nil)

	tl := ctrl.ExecutionTimeline()
	if len(tl) == 0 {
		t.Fatal("expected non-empty execution timeline after boot")
	}
	deps := ctrl.DependencyGraph().Dependencies("B")
	if len(deps) != 1 || deps[0] != "A" {
		t.Fatalf("unexpected dependency graph result: %v", deps)
	}
	// LiveGraph must be the same underlying object as DependencyGraph.
	if ctrl.LiveGraph() != ctrl.DependencyGraph() {
		t.Fatal("expected LiveGraph and DependencyGraph to expose the same graph")
	}
}

func TestPredictionFlagsRepeatedGiveUps(t *testing.T) {
	a := &mc{name: "A", healthy: false} // never recovers on its own (Restart doesn't fix HealthCheck if we don't flip)
	rt := runtime.New()
	rt.Boot(a)
	ctrl := New(rt, nil)

	// Force two GAVE_UP incidents by directly driving the watcher past
	// max attempts twice with a unit that never reports healthy.
	stubbornUnit := &neverHealthy{name: "A"}
	w := rt.Watcher()
	w.Register(stubbornUnit)
	w.Check("A", 1)
	w.Check("A", 2)

	preds := ctrl.Prediction()
	found := false
	for _, p := range preds {
		if p.Component == "A" && p.AtRisk {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected A flagged AtRisk after repeated GAVE_UP, got %+v", preds)
	}
}

type neverHealthy struct{ name string }

func (n *neverHealthy) Name() string                 { return n.name }
func (n *neverHealthy) HealthCheck() selfheal.Health { return selfheal.Down }
func (n *neverHealthy) Restart() error               { return nil }
func (n *neverHealthy) Replay() error                { return nil }

func TestResourceUtilizationNilManager(t *testing.T) {
	a := &mc{name: "A", healthy: true}
	rt := runtime.New()
	rt.Boot(a)
	ctrl := New(rt, nil)
	if u := ctrl.ResourceUtilization(resource.Memory); u != nil {
		t.Fatalf("expected nil utilization map with no resource manager, got %v", u)
	}
}

func TestResourceUtilizationWithManager(t *testing.T) {
	a := &mc{name: "A", healthy: true}
	rt := runtime.New()
	rt.Boot(a)
	rm := resource.NewManager()
	rm.SetBudget(resource.Memory, 100)
	rm.Reserve("A", resource.Memory, 25, 1)
	ctrl := New(rt, rm)
	u := ctrl.ResourceUtilization(resource.Memory)
	if u[resource.Memory] != 0.25 {
		t.Fatalf("expected 0.25 utilization, got %v", u)
	}
}
