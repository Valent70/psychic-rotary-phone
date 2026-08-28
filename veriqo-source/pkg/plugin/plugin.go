// Package plugin implements the Veriqo Plugin Lifecycle Manager.
// Priority 6 in Consensus_Architecture_Review: "Current Plugin Registry is static.
// Implement: Install, Register, Enable, Disable, Upgrade, Rollback, Compatibility
// Check, Capability Discovery, Semantic Version, Dependency Resolution."
//
// The kernel remains unchanged — plugins execute strictly in user space and
// interact only through the declared Capability interface. No plugin may import
// pkg/kernel directly.
package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// ─── Version (Semantic Versioning) ───────────────────────────────────────────

// Version is a semantic version triple.
type Version struct {
	Major, Minor, Patch int
}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// Compare returns -1, 0, or +1.
func (v Version) Compare(o Version) int {
	if v.Major != o.Major {
		return sign(v.Major - o.Major)
	}
	if v.Minor != o.Minor {
		return sign(v.Minor - o.Minor)
	}
	return sign(v.Patch - o.Patch)
}

// Compatible returns true if v is API-compatible with required.
// Two versions are compatible when their Major versions are equal and
// v.Minor >= required.Minor (semver public API promise).
func (v Version) Compatible(required Version) bool {
	return v.Major == required.Major && v.Compare(required) >= 0
}

func sign(n int) int {
	if n < 0 {
		return -1
	}
	if n > 0 {
		return 1
	}
	return 0
}

// ─── Capability ───────────────────────────────────────────────────────────────

// CapabilityKind classifies what a plugin provides.
type CapabilityKind string

const (
	CapabilityRiskModel      CapabilityKind = "risk_model"
	CapabilityDataSource     CapabilityKind = "data_source"
	CapabilityReasoningHook  CapabilityKind = "reasoning_hook"
	CapabilityAuditExporter  CapabilityKind = "audit_exporter"
	CapabilityDomainPipeline CapabilityKind = "domain_pipeline"
)

// Capability is a declared feature a plugin provides.
type Capability struct {
	Kind    CapabilityKind
	Name    string
	Version Version
}

// ─── Plugin Manifest ──────────────────────────────────────────────────────────

// State is the lifecycle state of an installed plugin.
type State string

const (
	StateInstalled State = "installed"
	StateEnabled   State = "enabled"
	StateDisabled  State = "disabled"
	StateError     State = "error"
)

// Dependency declares a required plugin (name + minimum version).
type Dependency struct {
	PluginName string
	MinVersion Version
}

// Manifest is the metadata descriptor for a plugin.
type Manifest struct {
	Name         string
	Version      Version
	Description  string
	Author       string
	Capabilities []Capability
	Dependencies []Dependency
	// Checksum is the SHA-256 hex of the plugin binary/source for supply-chain verification.
	Checksum string
}

// Plugin is a live installed plugin record inside the registry.
type Plugin struct {
	Manifest    Manifest
	State       State
	InstalledAt time.Time
	EnabledAt   time.Time
	Error       string

	// handler is the actual runtime hook — validated and set on Enable.
	handler Handler
}

// ─── Handler interface ────────────────────────────────────────────────────────

// Handler is the runtime interface every plugin must implement.
// The kernel never calls this directly — only the plugin manager does,
// after lifecycle checks pass.
type Handler interface {
	// Init is called once when the plugin is enabled.
	Init(cfg map[string]string) error
	// Execute runs the plugin's primary logic for a given input.
	Execute(input []byte) ([]byte, error)
	// Shutdown is called when the plugin is disabled or upgraded.
	Shutdown() error
}

// ─── Registry ─────────────────────────────────────────────────────────────────

// ErrPluginNotFound is returned when a plugin is not in the registry.
type ErrPluginNotFound struct{ Name string }

func (e *ErrPluginNotFound) Error() string {
	return fmt.Sprintf("plugin %q not found", e.Name)
}

// ErrIncompatibleVersion is returned when a dependency version is unsatisfied.
type ErrIncompatibleVersion struct {
	Plugin   string
	Required Version
	Got      Version
}

func (e *ErrIncompatibleVersion) Error() string {
	return fmt.Sprintf("plugin %q requires %s >= %s, got %s",
		e.Plugin, e.Plugin, e.Required, e.Got)
}

// ErrDependencyNotEnabled is returned when a required plugin is not enabled.
type ErrDependencyNotEnabled struct{ Dep string }

func (e *ErrDependencyNotEnabled) Error() string {
	return fmt.Sprintf("required plugin %q is not enabled", e.Dep)
}

// Event is a lifecycle event emitted by the registry.
type EventKind string

const (
	EventInstalled  EventKind = "installed"
	EventEnabled    EventKind = "enabled"
	EventDisabled   EventKind = "disabled"
	EventUpgraded   EventKind = "upgraded"
	EventRolledBack EventKind = "rolled_back"
	EventError      EventKind = "error"
)

// Event carries lifecycle notification.
type Event struct {
	Kind      EventKind
	Plugin    string
	Version   Version
	Timestamp time.Time
}

// Registry manages the full plugin lifecycle.
type Registry struct {
	mu        sync.RWMutex
	plugins   map[string]*Plugin
	snapshots map[string][]*Plugin // rollback history per plugin name
	listeners []func(Event)
}

// NewRegistry creates an empty plugin registry.
func NewRegistry() *Registry {
	return &Registry{
		plugins:   make(map[string]*Plugin),
		snapshots: make(map[string][]*Plugin),
	}
}

// OnEvent registers a lifecycle event listener.
func (r *Registry) OnEvent(fn func(Event)) {
	r.mu.Lock()
	r.listeners = append(r.listeners, fn)
	r.mu.Unlock()
}

func (r *Registry) emit(e Event) {
	for _, fn := range r.listeners {
		fn(e)
	}
}

// Install registers a plugin manifest (without enabling it).
// Verifies the checksum of the provided source bytes and checks for
// duplicate registration.
func (r *Registry) Install(m Manifest, source []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if m.Name == "" {
		return errors.New("plugin: manifest name must not be empty")
	}

	// Checksum verification (supply-chain assurance).
	if m.Checksum != "" {
		sum := sha256.Sum256(source)
		got := hex.EncodeToString(sum[:])
		if got != m.Checksum {
			return fmt.Errorf("plugin: checksum mismatch for %q: expected %s got %s",
				m.Name, m.Checksum, got)
		}
	}

	if existing, ok := r.plugins[m.Name]; ok {
		if existing.Manifest.Version.Compare(m.Version) >= 0 {
			return fmt.Errorf("plugin: %q v%s already installed (installed: v%s)",
				m.Name, m.Version, existing.Manifest.Version)
		}
	}

	r.plugins[m.Name] = &Plugin{
		Manifest:    m,
		State:       StateInstalled,
		InstalledAt: time.Now(),
	}
	r.emit(Event{Kind: EventInstalled, Plugin: m.Name, Version: m.Version, Timestamp: time.Now()})
	return nil
}

// Enable activates a plugin after verifying all its dependencies are met.
func (r *Registry) Enable(name string, handler Handler, cfg map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	p, ok := r.plugins[name]
	if !ok {
		return &ErrPluginNotFound{Name: name}
	}
	if p.State == StateEnabled {
		return fmt.Errorf("plugin %q is already enabled", name)
	}

	// Dependency resolution.
	for _, dep := range p.Manifest.Dependencies {
		dp, exists := r.plugins[dep.PluginName]
		if !exists {
			return &ErrDependencyNotEnabled{Dep: dep.PluginName}
		}
		if dp.State != StateEnabled {
			return &ErrDependencyNotEnabled{Dep: dep.PluginName}
		}
		if !dp.Manifest.Version.Compatible(dep.MinVersion) {
			return &ErrIncompatibleVersion{
				Plugin:   dep.PluginName,
				Required: dep.MinVersion,
				Got:      dp.Manifest.Version,
			}
		}
	}

	if err := handler.Init(cfg); err != nil {
		p.State = StateError
		p.Error = err.Error()
		r.emit(Event{Kind: EventError, Plugin: name, Version: p.Manifest.Version, Timestamp: time.Now()})
		return fmt.Errorf("plugin %q init failed: %w", name, err)
	}

	p.handler = handler
	p.State = StateEnabled
	p.EnabledAt = time.Now()
	p.Error = ""
	r.emit(Event{Kind: EventEnabled, Plugin: name, Version: p.Manifest.Version, Timestamp: time.Now()})
	return nil
}

// Disable shuts down a plugin gracefully.
func (r *Registry) Disable(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	p, ok := r.plugins[name]
	if !ok {
		return &ErrPluginNotFound{Name: name}
	}
	if p.State != StateEnabled {
		return fmt.Errorf("plugin %q is not enabled", name)
	}

	// Check no other enabled plugins depend on this one.
	for n, dep := range r.plugins {
		if dep.State != StateEnabled {
			continue
		}
		for _, d := range dep.Manifest.Dependencies {
			if d.PluginName == name {
				return fmt.Errorf("cannot disable %q: plugin %q depends on it", name, n)
			}
		}
	}

	if err := p.handler.Shutdown(); err != nil {
		p.State = StateError
		p.Error = err.Error()
		return fmt.Errorf("plugin %q shutdown error: %w", name, err)
	}
	p.handler = nil
	p.State = StateDisabled
	r.emit(Event{Kind: EventDisabled, Plugin: name, Version: p.Manifest.Version, Timestamp: time.Now()})
	return nil
}

// Upgrade installs a new version of an already-installed plugin.
// The old version is saved for rollback before the upgrade proceeds.
func (r *Registry) Upgrade(m Manifest, source []byte, handler Handler, cfg map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	old, ok := r.plugins[m.Name]
	if !ok {
		return &ErrPluginNotFound{Name: m.Name}
	}
	if m.Version.Compare(old.Manifest.Version) <= 0 {
		return fmt.Errorf("plugin: upgrade version %s must be greater than installed %s",
			m.Version, old.Manifest.Version)
	}

	// Save snapshot for rollback.
	snapshot := *old
	r.snapshots[m.Name] = append(r.snapshots[m.Name], &snapshot)

	// Shutdown old handler if enabled.
	if old.State == StateEnabled && old.handler != nil {
		_ = old.handler.Shutdown()
	}

	// Checksum verification.
	if m.Checksum != "" {
		sum := sha256.Sum256(source)
		got := hex.EncodeToString(sum[:])
		if got != m.Checksum {
			return fmt.Errorf("plugin: checksum mismatch for upgrade %q", m.Name)
		}
	}

	// Install new version.
	r.plugins[m.Name] = &Plugin{
		Manifest:    m,
		State:       StateInstalled,
		InstalledAt: time.Now(),
	}

	// Re-enable with new handler.
	if err := handler.Init(cfg); err != nil {
		r.plugins[m.Name].State = StateError
		return fmt.Errorf("plugin %q upgrade init failed: %w", m.Name, err)
	}
	r.plugins[m.Name].handler = handler
	r.plugins[m.Name].State = StateEnabled
	r.plugins[m.Name].EnabledAt = time.Now()

	r.emit(Event{Kind: EventUpgraded, Plugin: m.Name, Version: m.Version, Timestamp: time.Now()})
	return nil
}

// Rollback reverts to the previous installed version.
func (r *Registry) Rollback(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	snaps, ok := r.snapshots[name]
	if !ok || len(snaps) == 0 {
		return fmt.Errorf("plugin: no rollback snapshot for %q", name)
	}

	// Shutdown current handler if enabled.
	if current, exists := r.plugins[name]; exists && current.State == StateEnabled && current.handler != nil {
		_ = current.handler.Shutdown()
	}

	prev := snaps[len(snaps)-1]
	r.snapshots[name] = snaps[:len(snaps)-1]
	r.plugins[name] = prev

	r.emit(Event{Kind: EventRolledBack, Plugin: name, Version: prev.Manifest.Version, Timestamp: time.Now()})
	return nil
}

// Execute calls the named plugin's handler with the given input.
func (r *Registry) Execute(name string, input []byte) ([]byte, error) {
	r.mu.RLock()
	p, ok := r.plugins[name]
	r.mu.RUnlock()
	if !ok {
		return nil, &ErrPluginNotFound{Name: name}
	}
	if p.State != StateEnabled {
		return nil, fmt.Errorf("plugin %q is not enabled (state: %s)", name, p.State)
	}
	return p.handler.Execute(input)
}

// DiscoverCapabilities returns all capabilities provided by enabled plugins.
func (r *Registry) DiscoverCapabilities(kind CapabilityKind) []Capability {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var caps []Capability
	for _, p := range r.plugins {
		if p.State != StateEnabled {
			continue
		}
		for _, c := range p.Manifest.Capabilities {
			if c.Kind == kind {
				caps = append(caps, c)
			}
		}
	}
	sort.Slice(caps, func(i, j int) bool { return caps[i].Name < caps[j].Name })
	return caps
}

// List returns all plugins sorted by name.
func (r *Registry) List() []*Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*Plugin, 0, len(r.plugins))
	for _, p := range r.plugins {
		cp := *p
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Manifest.Name < out[j].Manifest.Name
	})
	return out
}

// Get returns a single plugin by name.
func (r *Registry) Get(name string) (*Plugin, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.plugins[name]
	if !ok {
		return nil, &ErrPluginNotFound{Name: name}
	}
	cp := *p
	return &cp, nil
}
