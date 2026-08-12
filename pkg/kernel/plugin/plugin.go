// Package plugin implements Veriqo's Plugin Framework — gap #6 in
// "Kritik_terbesar_nomor_1.docx": "Kalau nanti VERIQO ingin dipasang AI
// Engine, Analytics, Forecast, Risk, Compliance seharusnya cukup Drop
// Plugin -> Register -> Ready. Belum ada."
//
// A Plugin is anything that can attach an engine.Engine-shaped
// capability to the running Kernel Runtime at any point after boot,
// without the Runtime needing to know the plugin's concrete type ahead
// of time. Host is the narrow, capability-scoped interface a plugin
// receives — it can register an engine and read the ontology's type
// registry, but cannot reach the Runtime, resource manager, or other
// plugins directly, keeping the blast radius of a misbehaving plugin
// bounded.
//
// This package intentionally does NOT implement dynamic code loading
// (no Go plugin.so, no process isolation/WASM sandboxing) — Drop here
// means "register an in-process Plugin value the host program already
// linked", which is the honest, deterministic, zero-dependency subset
// of the critique's request. True dynamically-loaded/sandboxed plugins
// are listed as an explicit open item.
package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	ErrDuplicatePlugin = errors.New("plugin: already registered under this ID")
	ErrUnknownPlugin   = errors.New("plugin: not registered")
	ErrNotReady        = errors.New("plugin: Register did not report Ready")
)

// Capability names one thing a plugin can do; purely descriptive
// metadata surfaced to operators (e.g. "forecast", "risk-scoring").
type Capability string

// Host is the bounded interface a Plugin's Register method receives.
// Concrete callers (the Kernel Runtime) implement this narrowly so a
// plugin cannot reach anything beyond engine registration.
type Host interface {
	// RegisterEngine exposes an opaque engine handle for the plugin to
	// attach; the Runtime interprets the concrete type. Kept as `any`
	// here so this package has zero dependency on pkg/engine.
	RegisterEngine(name string, handle any) error
}

// Plugin is the interface any pluggable capability implements.
type Plugin interface {
	ID() string
	Version() string
	Capabilities() []Capability
	// Register is called exactly once, with a Host, to let the plugin
	// attach itself. Returning a non-nil error means the plugin failed
	// to activate and will not be marked Ready.
	Register(h Host) error
}

// record is the registry's internal bookkeeping for one plugin.
type record struct {
	p     Plugin
	ready bool
	hash  string // content-addressed identity: ID+Version+sorted capabilities
}

func hashOf(p Plugin) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s", p.ID(), p.Version())
	caps := make([]string, len(p.Capabilities()))
	for i, c := range p.Capabilities() {
		caps[i] = string(c)
	}
	sort.Strings(caps)
	for _, c := range caps {
		fmt.Fprintf(h, "|%s", c)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Registry is the Plugin Framework's Drop -> Register -> Ready pipeline.
type Registry struct {
	mu      sync.Mutex
	plugins map[string]*record
	host    Host
}

// NewRegistry builds a Registry bound to a Host implementation (usually
// the Kernel Runtime itself).
func NewRegistry(h Host) *Registry {
	return &Registry{plugins: make(map[string]*record), host: h}
}

// Drop registers and immediately activates a plugin: Drop -> Register
// -> Ready, in one call, matching the critique's exact three-step
// pipeline name.
func (r *Registry) Drop(p Plugin) error {
	r.mu.Lock()
	id := p.ID()
	if _, exists := r.plugins[id]; exists {
		r.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrDuplicatePlugin, id)
	}
	rec := &record{p: p, hash: hashOf(p)}
	r.plugins[id] = rec
	host := r.host
	r.mu.Unlock()

	if err := p.Register(host); err != nil {
		return fmt.Errorf("plugin: register %q: %w", id, err)
	}

	r.mu.Lock()
	rec.ready = true
	r.mu.Unlock()
	return nil
}

// IsReady reports whether a registered plugin completed Register
// successfully.
func (r *Registry) IsReady(id string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.plugins[id]
	if !ok {
		return false, fmt.Errorf("%w: %s", ErrUnknownPlugin, id)
	}
	return rec.ready, nil
}

// ByCapability returns the sorted IDs of every Ready plugin declaring a
// given Capability.
func (r *Registry) ByCapability(c Capability) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for id, rec := range r.plugins {
		if !rec.ready {
			continue
		}
		for _, have := range rec.p.Capabilities() {
			if have == c {
				out = append(out, id)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// IDs returns every registered plugin ID, sorted.
func (r *Registry) IDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.plugins))
	for id := range r.plugins {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Fingerprint returns the content-addressed identity hash of a
// registered plugin — used to detect drift between what was Dropped
// and what is currently deployed across a fleet.
func (r *Registry) Fingerprint(id string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.plugins[id]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnknownPlugin, id)
	}
	return rec.hash, nil
}
