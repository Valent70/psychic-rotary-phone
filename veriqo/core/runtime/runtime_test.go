package runtime

import "testing"

type stubEngine struct {
	meta    Metadata
	started bool
	stopped bool
}

func (s *stubEngine) Metadata() Metadata { return s.meta }
func (s *stubEngine) Start() error       { s.started = true; return nil }
func (s *stubEngine) Stop() error        { s.stopped = true; return nil }
func (s *stubEngine) HealthCheck() Status {
	if s.started && !s.stopped {
		return StatusHealthy
	}
	return StatusStopped
}
func (s *stubEngine) Invoke(method string, args map[string]string) (map[string]string, error) {
	return map[string]string{"echo": method}, nil
}

func TestRegisterDuplicateRejected(t *testing.T) {
	m := NewManager()
	e := &stubEngine{meta: Metadata{Name: "a"}}
	if err := m.Register(e); err != nil {
		t.Fatal(err)
	}
	if err := m.Register(e); err == nil {
		t.Fatal("expected duplicate registration error")
	}
}

func TestDependencyOrderRespectsEdges(t *testing.T) {
	m := NewManager()
	_ = m.Register(&stubEngine{meta: Metadata{Name: "trust"}})
	_ = m.Register(&stubEngine{meta: Metadata{Name: "policy", DependsOn: []string{"trust"}}})
	_ = m.Register(&stubEngine{meta: Metadata{Name: "decision", DependsOn: []string{"policy", "trust"}}})

	order, err := m.DependencyOrder()
	if err != nil {
		t.Fatal(err)
	}
	pos := map[string]int{}
	for i, n := range order {
		pos[n] = i
	}
	if pos["trust"] > pos["policy"] || pos["policy"] > pos["decision"] {
		t.Fatalf("dependency order violated: %v", order)
	}
}

func TestDependencyCycleDetected(t *testing.T) {
	m := NewManager()
	_ = m.Register(&stubEngine{meta: Metadata{Name: "a", DependsOn: []string{"b"}}})
	_ = m.Register(&stubEngine{meta: Metadata{Name: "b", DependsOn: []string{"a"}}})
	if _, err := m.DependencyOrder(); err != ErrCycle {
		t.Fatalf("expected ErrCycle, got %v", err)
	}
}

func TestMissingDependencyRejected(t *testing.T) {
	m := NewManager()
	_ = m.Register(&stubEngine{meta: Metadata{Name: "a", DependsOn: []string{"ghost"}}})
	if _, err := m.DependencyOrder(); err == nil {
		t.Fatal("expected missing-dependency error")
	}
}

func TestStartAllHealthAndDiscover(t *testing.T) {
	m := NewManager()
	_ = m.Register(&stubEngine{meta: Metadata{Name: "trust", Capabilities: []Capability{"trust.certify"}}})
	_ = m.Register(&stubEngine{meta: Metadata{Name: "evidence", Capabilities: []Capability{"evidence.fuse"}, DependsOn: []string{"trust"}}})

	if err := m.StartAll(); err != nil {
		t.Fatal(err)
	}
	report := m.HealthReport()
	if report["trust"] != StatusHealthy || report["evidence"] != StatusHealthy {
		t.Fatalf("expected both healthy, got %v", report)
	}
	found := m.Discover("evidence.fuse")
	if len(found) != 1 || found[0] != "evidence" {
		t.Fatalf("discover failed: %v", found)
	}

	out, err := m.Invoke("trust", "certify", map[string]string{"x": "1"})
	if err != nil || out["echo"] != "certify" {
		t.Fatalf("invoke failed: %v %v", out, err)
	}

	if err := m.StopAll(); err != nil {
		t.Fatal(err)
	}
	if m.HealthReport()["trust"] != StatusStopped {
		t.Fatal("expected stopped after StopAll")
	}
}
