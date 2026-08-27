package plugin_test

import (
	"errors"
	"fmt"
	"testing"

	"veriqo/pkg/plugin"
)

// ─── stub handler ─────────────────────────────────────────────────────────────

type stubHandler struct {
	initErr  error
	execOut  []byte
	execErr  error
	shutdown bool
}

func (h *stubHandler) Init(cfg map[string]string) error { return h.initErr }
func (h *stubHandler) Execute(in []byte) ([]byte, error) {
	if h.execErr != nil {
		return nil, h.execErr
	}
	if h.execOut != nil {
		return h.execOut, nil
	}
	return in, nil
}
func (h *stubHandler) Shutdown() error { h.shutdown = true; return nil }

func simpleManifest(name string, ver plugin.Version) plugin.Manifest {
	return plugin.Manifest{
		Name:    name,
		Version: ver,
		Capabilities: []plugin.Capability{
			{Kind: plugin.CapabilityRiskModel, Name: name + ".risk", Version: ver},
		},
	}
}

// ─── Tests ────────────────────────────────────────────────────────────────────

func TestRegistry_InstallAndEnable(t *testing.T) {
	r := plugin.NewRegistry()
	m := simpleManifest("test-plugin", plugin.Version{1, 0, 0})
	if err := r.Install(m, nil); err != nil {
		t.Fatal(err)
	}
	h := &stubHandler{execOut: []byte("ok")}
	if err := r.Enable("test-plugin", h, nil); err != nil {
		t.Fatal(err)
	}
	out, err := r.Execute("test-plugin", []byte("input"))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "ok" {
		t.Errorf("expected 'ok', got %q", out)
	}
}

func TestRegistry_Disable(t *testing.T) {
	r := plugin.NewRegistry()
	m := simpleManifest("p", plugin.Version{1, 0, 0})
	r.Install(m, nil)
	h := &stubHandler{}
	r.Enable("p", h, nil)
	if err := r.Disable("p"); err != nil {
		t.Fatal(err)
	}
	if !h.shutdown {
		t.Error("expected handler.Shutdown() called")
	}
	_, err := r.Execute("p", nil)
	if err == nil {
		t.Error("expected error executing disabled plugin")
	}
}

func TestRegistry_DuplicateInstall(t *testing.T) {
	r := plugin.NewRegistry()
	m := simpleManifest("p", plugin.Version{1, 0, 0})
	r.Install(m, nil)
	err := r.Install(m, nil)
	if err == nil {
		t.Error("expected error for duplicate install at same version")
	}
}

func TestRegistry_Upgrade(t *testing.T) {
	r := plugin.NewRegistry()
	m1 := simpleManifest("p", plugin.Version{1, 0, 0})
	r.Install(m1, nil)
	h1 := &stubHandler{execOut: []byte("v1")}
	r.Enable("p", h1, nil)

	m2 := simpleManifest("p", plugin.Version{2, 0, 0})
	h2 := &stubHandler{execOut: []byte("v2")}
	if err := r.Upgrade(m2, nil, h2, nil); err != nil {
		t.Fatal(err)
	}
	out, _ := r.Execute("p", nil)
	if string(out) != "v2" {
		t.Errorf("expected v2 output after upgrade, got %q", out)
	}
	if !h1.shutdown {
		t.Error("old handler should have been shut down on upgrade")
	}
}

func TestRegistry_Rollback(t *testing.T) {
	r := plugin.NewRegistry()
	m1 := simpleManifest("p", plugin.Version{1, 0, 0})
	r.Install(m1, nil)
	h1 := &stubHandler{execOut: []byte("v1")}
	r.Enable("p", h1, nil)

	m2 := simpleManifest("p", plugin.Version{2, 0, 0})
	h2 := &stubHandler{execOut: []byte("v2")}
	r.Upgrade(m2, nil, h2, nil)

	if err := r.Rollback("p"); err != nil {
		t.Fatal(err)
	}
	p, _ := r.Get("p")
	if p.Manifest.Version.Major != 1 {
		t.Errorf("expected v1 after rollback, got v%d", p.Manifest.Version.Major)
	}
}

func TestRegistry_DependencyResolution(t *testing.T) {
	r := plugin.NewRegistry()

	// Install base plugin.
	base := simpleManifest("base", plugin.Version{1, 2, 0})
	r.Install(base, nil)
	r.Enable("base", &stubHandler{}, nil)

	// Plugin that depends on base.
	dep := plugin.Manifest{
		Name:    "dep-plugin",
		Version: plugin.Version{1, 0, 0},
		Dependencies: []plugin.Dependency{
			{PluginName: "base", MinVersion: plugin.Version{1, 1, 0}},
		},
	}
	r.Install(dep, nil)
	if err := r.Enable("dep-plugin", &stubHandler{}, nil); err != nil {
		t.Fatalf("expected dep resolution to succeed: %v", err)
	}
}

func TestRegistry_DependencyNotMet(t *testing.T) {
	r := plugin.NewRegistry()
	dep := plugin.Manifest{
		Name:    "dep-plugin",
		Version: plugin.Version{1, 0, 0},
		Dependencies: []plugin.Dependency{
			{PluginName: "missing-base", MinVersion: plugin.Version{1, 0, 0}},
		},
	}
	r.Install(dep, nil)
	err := r.Enable("dep-plugin", &stubHandler{}, nil)
	var notEnabled *plugin.ErrDependencyNotEnabled
	if !errors.As(err, &notEnabled) {
		t.Fatalf("expected ErrDependencyNotEnabled, got %v", err)
	}
}

func TestRegistry_ChecksumVerification(t *testing.T) {
	r := plugin.NewRegistry()
	m := plugin.Manifest{
		Name:     "secured",
		Version:  plugin.Version{1, 0, 0},
		Checksum: "deadbeef", // wrong checksum
	}
	err := r.Install(m, []byte("source code"))
	if err == nil {
		t.Error("expected checksum error")
	}
}

func TestRegistry_CapabilityDiscovery(t *testing.T) {
	r := plugin.NewRegistry()
	for i := range 3 {
		m := simpleManifest(fmt.Sprintf("risk-%d", i), plugin.Version{1, 0, 0})
		r.Install(m, nil)
		r.Enable(fmt.Sprintf("risk-%d", i), &stubHandler{}, nil)
	}
	// Also install one with a different capability kind.
	m2 := plugin.Manifest{
		Name:    "audit-exporter",
		Version: plugin.Version{1, 0, 0},
		Capabilities: []plugin.Capability{
			{Kind: plugin.CapabilityAuditExporter, Name: "json-exporter", Version: plugin.Version{1, 0, 0}},
		},
	}
	r.Install(m2, nil)
	r.Enable("audit-exporter", &stubHandler{}, nil)

	riskCaps := r.DiscoverCapabilities(plugin.CapabilityRiskModel)
	if len(riskCaps) != 3 {
		t.Errorf("expected 3 risk model capabilities, got %d", len(riskCaps))
	}
	auditCaps := r.DiscoverCapabilities(plugin.CapabilityAuditExporter)
	if len(auditCaps) != 1 {
		t.Errorf("expected 1 audit exporter, got %d", len(auditCaps))
	}
}

func TestRegistry_EventEmission(t *testing.T) {
	r := plugin.NewRegistry()
	var events []plugin.EventKind
	r.OnEvent(func(e plugin.Event) { events = append(events, e.Kind) })

	m := simpleManifest("evtest", plugin.Version{1, 0, 0})
	r.Install(m, nil)
	r.Enable("evtest", &stubHandler{}, nil)
	r.Disable("evtest")

	expected := []plugin.EventKind{plugin.EventInstalled, plugin.EventEnabled, plugin.EventDisabled}
	if len(events) != len(expected) {
		t.Fatalf("expected %d events, got %d: %v", len(expected), len(events), events)
	}
	for i, e := range expected {
		if events[i] != e {
			t.Errorf("event[%d]: expected %s, got %s", i, e, events[i])
		}
	}
}

func TestRegistry_NotFoundError(t *testing.T) {
	r := plugin.NewRegistry()
	_, err := r.Execute("nonexistent", nil)
	var notFound *plugin.ErrPluginNotFound
	if !errors.As(err, &notFound) {
		t.Fatalf("expected ErrPluginNotFound, got %v", err)
	}
}

func TestRegistry_List(t *testing.T) {
	r := plugin.NewRegistry()
	for i := range 5 {
		m := simpleManifest(fmt.Sprintf("p-%d", i), plugin.Version{1, 0, 0})
		r.Install(m, nil)
	}
	list := r.List()
	if len(list) != 5 {
		t.Errorf("expected 5 plugins, got %d", len(list))
	}
}

func TestVersion_Compatible(t *testing.T) {
	tests := []struct {
		v, req plugin.Version
		want   bool
	}{
		{plugin.Version{1, 2, 3}, plugin.Version{1, 0, 0}, true},
		{plugin.Version{1, 2, 3}, plugin.Version{1, 2, 0}, true},
		{plugin.Version{1, 2, 3}, plugin.Version{1, 2, 3}, true},
		{plugin.Version{1, 2, 3}, plugin.Version{1, 3, 0}, false},
		{plugin.Version{2, 0, 0}, plugin.Version{1, 0, 0}, false}, // major differs
		{plugin.Version{1, 0, 0}, plugin.Version{2, 0, 0}, false},
	}
	for _, tt := range tests {
		got := tt.v.Compatible(tt.req)
		if got != tt.want {
			t.Errorf("%s.Compatible(%s) = %v, want %v", tt.v, tt.req, got, tt.want)
		}
	}
}

func BenchmarkRegistry_Execute(b *testing.B) {
	r := plugin.NewRegistry()
	m := simpleManifest("bench", plugin.Version{1, 0, 0})
	r.Install(m, nil)
	r.Enable("bench", &stubHandler{}, nil)
	input := []byte(`{"data":"benchmark"}`)
	b.ResetTimer()
	for range b.N {
		r.Execute("bench", input)
	}
}
