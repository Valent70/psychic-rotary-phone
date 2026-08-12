package runtime

import (
	"testing"

	"veriqo/pkg/kernel/selfheal"
)

// stubComponent is a minimal real Component for boot-order and
// recovery testing.
type stubComponent struct {
	name    string
	deps    []string
	events  *[]string
	healthy bool
}

func (c *stubComponent) Name() string           { return c.name }
func (c *stubComponent) Dependencies() []string { return c.deps }
func (c *stubComponent) Init() error {
	*c.events = append(*c.events, "init:"+c.name)
	return nil
}
func (c *stubComponent) Start() error {
	*c.events = append(*c.events, "start:"+c.name)
	return nil
}
func (c *stubComponent) HealthCheck() selfheal.Health {
	if c.healthy {
		return selfheal.Healthy
	}
	return selfheal.Down
}
func (c *stubComponent) Restart() error {
	*c.events = append(*c.events, "restart:"+c.name)
	c.healthy = true
	return nil
}
func (c *stubComponent) Replay() error {
	*c.events = append(*c.events, "replay:"+c.name)
	return nil
}
func (c *stubComponent) Stop() error {
	*c.events = append(*c.events, "stop:"+c.name)
	return nil
}

func TestBootRespectsDependencyOrder(t *testing.T) {
	var events []string
	a := &stubComponent{name: "A", events: &events, healthy: true}
	b := &stubComponent{name: "B", deps: []string{"A"}, events: &events, healthy: true}
	c := &stubComponent{name: "C", deps: []string{"B"}, events: &events, healthy: true}

	rt := New()
	if err := rt.Boot(c, a, b); err != nil { // deliberately out-of-order input
		t.Fatalf("boot: %v", err)
	}
	if rt.Phase() != PhaseMonitor {
		t.Fatalf("expected phase MONITOR after boot, got %s", rt.Phase())
	}
	order := rt.BootOrder()
	if len(order) != 3 || order[0] != "A" || order[1] != "B" || order[2] != "C" {
		t.Fatalf("expected boot order [A B C], got %v", order)
	}
	// Init must happen for all before Start for any component that
	// depends on it — verify init:A precedes start:B.
	initAIdx, startBIdx := -1, -1
	for i, e := range events {
		if e == "init:A" {
			initAIdx = i
		}
		if e == "start:B" {
			startBIdx = i
		}
	}
	if initAIdx == -1 || startBIdx == -1 || initAIdx > startBIdx {
		t.Fatalf("expected init:A before start:B, events=%v", events)
	}
}

func TestBootRejectsCycle(t *testing.T) {
	var events []string
	a := &stubComponent{name: "A", deps: []string{"B"}, events: &events}
	b := &stubComponent{name: "B", deps: []string{"A"}, events: &events}
	rt := New()
	if err := rt.Boot(a, b); err == nil {
		t.Fatal("expected cycle detection error")
	}
}

func TestBootRejectsMissingDependency(t *testing.T) {
	var events []string
	a := &stubComponent{name: "A", deps: []string{"ghost"}, events: &events}
	rt := New()
	if err := rt.Boot(a); err == nil {
		t.Fatal("expected missing-dependency error")
	}
}

func TestMonitorReflectsLiveHealth(t *testing.T) {
	var events []string
	a := &stubComponent{name: "A", events: &events, healthy: true}
	rt := New()
	rt.Boot(a)
	h := rt.Monitor()
	if h["A"] != selfheal.Healthy {
		t.Fatalf("expected A healthy, got %v", h)
	}
	a.healthy = false
	h = rt.Monitor()
	if h["A"] != selfheal.Down {
		t.Fatalf("expected A down after flip, got %v", h)
	}
}

func TestRecoverDrivesSelfHealSequence(t *testing.T) {
	var events []string
	a := &stubComponent{name: "A", events: &events, healthy: false} // starts unhealthy
	rt := New()
	rt.Boot(a)
	results := rt.Recover(1)
	if !results["A"] {
		t.Fatalf("expected A recovered, got %v", results)
	}
	found := false
	for _, e := range events {
		if e == "restart:A" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected restart:A in events, got %v", events)
	}
}

func TestShutdownReverseOrder(t *testing.T) {
	var events []string
	a := &stubComponent{name: "A", events: &events, healthy: true}
	b := &stubComponent{name: "B", deps: []string{"A"}, events: &events, healthy: true}
	rt := New()
	rt.Boot(a, b)
	events = nil // reset to isolate shutdown events
	if err := rt.Shutdown(); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if len(events) != 2 || events[0] != "stop:B" || events[1] != "stop:A" {
		t.Fatalf("expected [stop:B stop:A], got %v", events)
	}
	if rt.Phase() != PhaseShutdown {
		t.Fatalf("expected phase SHUTDOWN, got %s", rt.Phase())
	}
}
