package plugin

import (
	"errors"
	"testing"
)

type fakeHost struct {
	registered map[string]any
}

func newFakeHost() *fakeHost { return &fakeHost{registered: make(map[string]any)} }

func (h *fakeHost) RegisterEngine(name string, handle any) error {
	h.registered[name] = handle
	return nil
}

type goodPlugin struct{ id string }

func (p goodPlugin) ID() string                 { return p.id }
func (p goodPlugin) Version() string            { return "1.0.0" }
func (p goodPlugin) Capabilities() []Capability { return []Capability{"forecast", "risk"} }
func (p goodPlugin) Register(h Host) error      { return h.RegisterEngine(p.id, "handle-"+p.id) }

type badPlugin struct{}

func (badPlugin) ID() string                 { return "bad" }
func (badPlugin) Version() string            { return "0.1.0" }
func (badPlugin) Capabilities() []Capability { return []Capability{"compliance"} }
func (badPlugin) Register(h Host) error      { return errors.New("simulated activation failure") }

func TestDropRegisterReady(t *testing.T) {
	host := newFakeHost()
	reg := NewRegistry(host)
	if err := reg.Drop(goodPlugin{id: "forecast-engine"}); err != nil {
		t.Fatalf("drop: %v", err)
	}
	ready, err := reg.IsReady("forecast-engine")
	if err != nil || !ready {
		t.Fatalf("expected ready, got ready=%v err=%v", ready, err)
	}
	if _, ok := host.registered["forecast-engine"]; !ok {
		t.Fatal("expected host to have received RegisterEngine call")
	}
}

func TestDropDuplicateRejected(t *testing.T) {
	reg := NewRegistry(newFakeHost())
	reg.Drop(goodPlugin{id: "x"})
	if err := reg.Drop(goodPlugin{id: "x"}); err == nil {
		t.Fatal("expected ErrDuplicatePlugin")
	}
}

func TestFailedRegisterNotReady(t *testing.T) {
	reg := NewRegistry(newFakeHost())
	err := reg.Drop(badPlugin{})
	if err == nil {
		t.Fatal("expected registration failure to propagate")
	}
	ready, _ := reg.IsReady("bad")
	if ready {
		t.Fatal("expected plugin to not be marked ready after failed Register")
	}
}

func TestByCapability(t *testing.T) {
	reg := NewRegistry(newFakeHost())
	reg.Drop(goodPlugin{id: "p1"})
	reg.Drop(goodPlugin{id: "p2"})
	ids := reg.ByCapability("forecast")
	if len(ids) != 2 || ids[0] != "p1" || ids[1] != "p2" {
		t.Fatalf("unexpected capability lookup: %v", ids)
	}
}

func TestFingerprintDeterministic(t *testing.T) {
	reg1 := NewRegistry(newFakeHost())
	reg2 := NewRegistry(newFakeHost())
	reg1.Drop(goodPlugin{id: "p1"})
	reg2.Drop(goodPlugin{id: "p1"})
	f1, _ := reg1.Fingerprint("p1")
	f2, _ := reg2.Fingerprint("p1")
	if f1 != f2 || f1 == "" {
		t.Fatalf("expected identical deterministic fingerprints, got %s vs %s", f1, f2)
	}
}
