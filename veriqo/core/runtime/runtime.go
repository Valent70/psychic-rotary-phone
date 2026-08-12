// Package runtime is Veriqo's Core Runtime — one of the five modules
// "Yang_masih_saya_kritisi.docx" §1 names as the right shape: "Core
// Runtime, Knowledge, Evidence, Intelligence, Execution. Sudah cukup."
// Deliberately not called "unified engine system": the prior session's
// naming ("Unified Engine", "Unified Evidence", "Unified Intelligence",
// "Unified Knowledge", "Unified Bridge", "Unified System", "Unified
// Orchestrator" — seven "Unified"s) is exactly the smell the critique
// names — "Unified -> Unified -> Unified -> Unified padahal sebenarnya
// ... Sudah cukup" — and a real module-boundary problem, not just a
// naming one: two of those seven ("Unified Engine" and "Unified
// Bridge") were the same concern split across two packages for no
// reason. They are merged here into one: this file defines the Engine
// contract and Manager (discovery, lifecycle, dependency graph,
// health); adapters.go in this same package wraps the seven existing
// registry.Registry kernels into that contract. One package, one
// concern: "the runtime that engines plug into."
//
// The critique's single most positive point (§ "Bagian yang menurut
// saya PALING penting: Bridge... kalau benar bridge berhasil, berarti
// Trust/Evidence/Reasoning/Replay/Verification/Intent/Policy tidak
// perlu diubah. Semuanya cukup: adapter.") is unchanged by the merge:
// adapters.go still touches zero business logic in the seven kernels —
// argument marshalling only.
package runtime

import (
	"fmt"
	"sort"
	"sync"
)

// Capability is a named ability an Engine exposes, e.g. "trust.certify".
type Capability string

// Status is where an Engine sits in its lifecycle.
type Status string

const (
	StatusRegistered Status = "registered"
	StatusStarting   Status = "starting"
	StatusHealthy    Status = "healthy"
	StatusDegraded   Status = "degraded"
	StatusStopped    Status = "stopped"
	StatusFailed     Status = "failed"
)

// Metadata describes an Engine for discovery, dependency resolution,
// and versioning.
type Metadata struct {
	Name         string
	Version      string
	Capabilities []Capability
	DependsOn    []string
	Priority     int
}

// Engine is the uniform contract every Core Runtime member implements.
// Invoke is flat/primitive-typed (map[string]string), mirroring the
// shape the CLI/REST/SDK boundary already uses, so an Engine can be
// driven identically by the Execution layer, a CLI, or an HTTP gateway
// route.
type Engine interface {
	Metadata() Metadata
	Start() error
	Stop() error
	HealthCheck() Status
	Invoke(method string, args map[string]string) (map[string]string, error)
}

var (
	ErrAlreadyRegistered = fmt.Errorf("runtime: already registered")
	ErrNotFound          = fmt.Errorf("runtime: not found")
	ErrCycle             = fmt.Errorf("runtime: dependency cycle detected")
	ErrMissingDependency = fmt.Errorf("runtime: dependency not registered")
)

type entry struct {
	engine Engine
	status Status
}

// Manager is the Core Runtime's control plane: Discovery + Capability +
// Dependency + Lifecycle + Health over a set of Engines.
type Manager struct {
	mu      sync.RWMutex
	entries map[string]*entry
}

func NewManager() *Manager { return &Manager{entries: make(map[string]*entry)} }

func (m *Manager) Register(e Engine) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	name := e.Metadata().Name
	if name == "" {
		return fmt.Errorf("runtime: metadata.Name must not be empty")
	}
	if _, exists := m.entries[name]; exists {
		return fmt.Errorf("%w: %s", ErrAlreadyRegistered, name)
	}
	m.entries[name] = &entry{engine: e, status: StatusRegistered}
	return nil
}

func (m *Manager) Get(name string) (Engine, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	en, ok := m.entries[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return en.engine, nil
}

func (m *Manager) Discover(cap Capability) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var names []string
	for name, en := range m.entries {
		for _, c := range en.engine.Metadata().Capabilities {
			if c == cap {
				names = append(names, name)
				break
			}
		}
	}
	sort.Strings(names)
	return names
}

func (m *Manager) DependencyOrder() ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return dependencyOrder(m.entries)
}

func dependencyOrder(entries map[string]*entry) ([]string, error) {
	indegree := make(map[string]int, len(entries))
	dependents := make(map[string][]string)
	for name := range entries {
		indegree[name] = 0
	}
	for name, en := range entries {
		for _, dep := range en.engine.Metadata().DependsOn {
			if _, ok := entries[dep]; !ok {
				return nil, fmt.Errorf("%w: engine %q depends on unregistered %q", ErrMissingDependency, name, dep)
			}
			indegree[name]++
			dependents[dep] = append(dependents[dep], name)
		}
	}

	var ready []string
	for name, d := range indegree {
		if d == 0 {
			ready = append(ready, name)
		}
	}
	sortByPriorityThenName(ready, entries)

	var order []string
	for len(ready) > 0 {
		next := ready[0]
		ready = ready[1:]
		order = append(order, next)
		var newlyReady []string
		for _, dependent := range dependents[next] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				newlyReady = append(newlyReady, dependent)
			}
		}
		sortByPriorityThenName(newlyReady, entries)
		ready = mergeSorted(ready, newlyReady, entries)
	}
	if len(order) != len(entries) {
		return nil, ErrCycle
	}
	return order, nil
}

func sortByPriorityThenName(names []string, entries map[string]*entry) {
	sort.Slice(names, func(i, j int) bool {
		pi := entries[names[i]].engine.Metadata().Priority
		pj := entries[names[j]].engine.Metadata().Priority
		if pi != pj {
			return pi < pj
		}
		return names[i] < names[j]
	})
}

func mergeSorted(a, b []string, entries map[string]*entry) []string {
	out := append(append([]string{}, a...), b...)
	sortByPriorityThenName(out, entries)
	return out
}

func (m *Manager) StartAll() error {
	order, err := m.DependencyOrder()
	if err != nil {
		return err
	}
	for _, name := range order {
		m.mu.Lock()
		en := m.entries[name]
		en.status = StatusStarting
		m.mu.Unlock()

		if err := en.engine.Start(); err != nil {
			m.mu.Lock()
			en.status = StatusFailed
			m.mu.Unlock()
			return fmt.Errorf("runtime: engine %q: start: %w", name, err)
		}
		m.mu.Lock()
		en.status = en.engine.HealthCheck()
		m.mu.Unlock()
	}
	return nil
}

func (m *Manager) StopAll() error {
	order, err := m.DependencyOrder()
	if err != nil {
		return err
	}
	var errs []string
	for i := len(order) - 1; i >= 0; i-- {
		name := order[i]
		m.mu.Lock()
		en := m.entries[name]
		m.mu.Unlock()
		if err := en.engine.Stop(); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", name, err))
		}
		m.mu.Lock()
		en.status = StatusStopped
		m.mu.Unlock()
	}
	if len(errs) > 0 {
		return fmt.Errorf("runtime: stop errors: %v", errs)
	}
	return nil
}

func (m *Manager) Health(name string) (Status, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	en, ok := m.entries[name]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return en.status, nil
}

func (m *Manager) HealthReport() map[string]Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	report := make(map[string]Status, len(m.entries))
	for name, en := range m.entries {
		en.status = en.engine.HealthCheck()
		report[name] = en.status
	}
	return report
}

func (m *Manager) Invoke(engineName, method string, args map[string]string) (map[string]string, error) {
	en, err := m.Get(engineName)
	if err != nil {
		return nil, err
	}
	return en.Invoke(method, args)
}

func (m *Manager) Names() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.entries))
	for n := range m.entries {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
