// Package runtime implements Veriqo's Kernel Runtime — gap #2 in
// "Kritik_terbesar_nomor_1.docx": "Masih belum ada Kernel Runtime.
// Saya ingin melihat sesuatu seperti Kernel Boot -> Discover Engine ->
// Dependency Resolution -> Initialize -> Run -> Monitor -> Recover ->
// Shutdown." This is also the piece that answers critique #10 ("masih
// level Microkernel, belum Enterprise Distributed Intelligence OS"):
// Runtime is what elevates the existing pkg/engine.Kernel (a uniform
// per-engine lifecycle) into an actual boot sequence over MANY engines
// with dependency ordering, live health, and self-healing recovery.
//
// Composition, not reinvention: Runtime uses pkg/kernel/execgraph for
// dependency resolution (Discover+Dependency Resolution stages) and
// pkg/kernel/selfheal for Recover — this package is the orchestrator
// that sequences those two plus each Component's own lifecycle calls,
// exactly the "Real integration" the critique's closing point asks for
// rather than a fresh microkernel bolted on beside the existing one.
package runtime

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"veriqo/pkg/kernel/execgraph"
	"veriqo/pkg/kernel/selfheal"
)

var (
	ErrDuplicateComponent = errors.New("runtime: component already registered")
	ErrNotBooted          = errors.New("runtime: Run called before Boot")
)

// Phase is the Kernel Runtime's own global lifecycle state — the
// critique's exact 8 named stages plus an initial NEW.
type Phase string

const (
	PhaseNew        Phase = "NEW"
	PhaseBoot       Phase = "BOOT"
	PhaseDiscover   Phase = "DISCOVER"
	PhaseResolve    Phase = "DEPENDENCY_RESOLUTION"
	PhaseInitialize Phase = "INITIALIZE"
	PhaseRun        Phase = "RUN"
	PhaseMonitor    Phase = "MONITOR"
	PhaseRecover    Phase = "RECOVER"
	PhaseShutdown   Phase = "SHUTDOWN"
)

// Component is anything the Kernel Runtime can boot, run, monitor, and
// recover. It satisfies selfheal.Unit directly (Name/HealthCheck/
// Restart/Replay) plus the extra Init/Start/Stop/Dependencies methods
// the boot sequence itself needs.
type Component interface {
	Name() string
	Dependencies() []string
	Init() error
	Start() error
	HealthCheck() selfheal.Health
	Restart() error
	Replay() error
	Stop() error
}

// Runtime is the Kernel Runtime: boots a set of Components through the
// full named lifecycle and keeps live health + a self-heal watcher.
type Runtime struct {
	mu         sync.Mutex
	phase      Phase
	components map[string]Component
	graph      *execgraph.Graph
	watcher    *selfheal.Watcher
	order      []string // topological boot order, fixed at Boot time
}

func New() *Runtime {
	return &Runtime{
		phase:      PhaseNew,
		components: make(map[string]Component),
		graph:      execgraph.NewGraph(),
		watcher:    selfheal.NewWatcher(3),
	}
}

// Phase returns the Runtime's current global lifecycle phase.
func (r *Runtime) Phase() Phase {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.phase
}

// Boot runs the full named sequence over the given components:
// Discover (register) -> Dependency Resolution (build execgraph,
// rejecting cycles/missing deps) -> Initialize (Init(), in topological
// order) -> Run (Start(), in topological order). Components must be
// passed with their dependencies already resolvable, i.e. any
// component named in another's Dependencies() must also be present in
// this call — matching execgraph.AddNode's own topological-add
// requirement (Discover naturally orders them for the caller if it
// passes dependency-free components first, same as any DAG builder).
func (r *Runtime) Boot(components ...Component) error {
	r.mu.Lock()
	r.phase = PhaseBoot
	r.mu.Unlock()

	// --- Discover ---
	r.mu.Lock()
	r.phase = PhaseDiscover
	for _, c := range components {
		if _, exists := r.components[c.Name()]; exists {
			r.mu.Unlock()
			return fmt.Errorf("%w: %s", ErrDuplicateComponent, c.Name())
		}
		r.components[c.Name()] = c
	}
	r.mu.Unlock()

	// --- Dependency Resolution ---
	r.mu.Lock()
	r.phase = PhaseResolve
	r.mu.Unlock()
	order, err := topoOrder(components)
	if err != nil {
		return fmt.Errorf("runtime: dependency resolution: %w", err)
	}
	for _, name := range order {
		c := r.components[name]
		if err := r.graph.AddNode(name, c.Dependencies()...); err != nil {
			return fmt.Errorf("runtime: dependency resolution: %w", err)
		}
	}
	r.mu.Lock()
	r.order = order
	r.mu.Unlock()

	// --- Initialize ---
	r.mu.Lock()
	r.phase = PhaseInitialize
	r.mu.Unlock()
	for i, name := range order {
		if err := r.components[name].Init(); err != nil {
			return fmt.Errorf("runtime: initialize %q: %w", name, err)
		}
		r.watcher.Register(r.components[name])
		_ = i
	}

	// --- Run ---
	r.mu.Lock()
	r.phase = PhaseRun
	r.mu.Unlock()
	for tick, name := range order {
		if err := r.components[name].Start(); err != nil {
			return fmt.Errorf("runtime: start %q: %w", name, err)
		}
		if err := r.graph.SetStatus(name, execgraph.Running, uint64(tick)+1); err != nil {
			return fmt.Errorf("runtime: mark %q running: %w", name, err)
		}
		if err := r.graph.SetStatus(name, execgraph.Done, uint64(tick)+1); err != nil {
			return fmt.Errorf("runtime: mark %q done: %w", name, err)
		}
	}

	r.mu.Lock()
	r.phase = PhaseMonitor
	r.mu.Unlock()
	return nil
}

// topoOrder computes a deterministic (name-tiebroken) topological order
// over components by their declared Dependencies(), independent of
// execgraph (so Boot can validate before mutating the live graph).
func topoOrder(components []Component) ([]string, error) {
	byName := make(map[string]Component, len(components))
	for _, c := range components {
		byName[c.Name()] = c
	}
	indegree := make(map[string]int)
	adj := make(map[string][]string)
	for _, c := range components {
		if _, ok := indegree[c.Name()]; !ok {
			indegree[c.Name()] = 0
		}
		for _, d := range c.Dependencies() {
			if _, ok := byName[d]; !ok {
				return nil, fmt.Errorf("component %q depends on unregistered %q", c.Name(), d)
			}
			adj[d] = append(adj[d], c.Name())
			indegree[c.Name()]++
		}
	}
	var ready []string
	for n, deg := range indegree {
		if deg == 0 {
			ready = append(ready, n)
		}
	}
	sort.Strings(ready)
	var order []string
	for len(ready) > 0 {
		n := ready[0]
		ready = ready[1:]
		order = append(order, n)
		next := append([]string(nil), adj[n]...)
		sort.Strings(next)
		for _, m := range next {
			indegree[m]--
			if indegree[m] == 0 {
				ready = append(ready, m)
				sort.Strings(ready)
			}
		}
	}
	if len(order) != len(components) {
		return nil, errors.New("cycle detected among component dependencies")
	}
	return order, nil
}

// Monitor returns a snapshot of every component's live health, keyed
// by name.
func (r *Runtime) Monitor() map[string]selfheal.Health {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]selfheal.Health, len(r.components))
	for name, c := range r.components {
		out[name] = c.HealthCheck()
	}
	return out
}

// Recover checks every component's health and, for any unhealthy one,
// drives it through the Self-Healing Runtime's Restart -> Replay ->
// Recovery -> Continue sequence. Returns the set of components that
// were found unhealthy and whether each was successfully recovered.
func (r *Runtime) Recover(tick uint64) map[string]bool {
	r.mu.Lock()
	r.phase = PhaseRecover
	order := append([]string(nil), r.order...)
	r.mu.Unlock()

	results := make(map[string]bool)
	for _, name := range order {
		r.mu.Lock()
		c := r.components[name]
		r.mu.Unlock()
		if c.HealthCheck() == selfheal.Healthy {
			continue
		}
		_, err := r.watcher.Check(name, tick)
		results[name] = err == nil
	}
	r.mu.Lock()
	r.phase = PhaseMonitor
	r.mu.Unlock()
	return results
}

// Shutdown stops every component in reverse boot order.
func (r *Runtime) Shutdown() error {
	r.mu.Lock()
	r.phase = PhaseShutdown
	order := append([]string(nil), r.order...)
	r.mu.Unlock()

	var firstErr error
	for i := len(order) - 1; i >= 0; i-- {
		c := r.components[order[i]]
		if err := c.Stop(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("runtime: stop %q: %w", order[i], err)
		}
	}
	return firstErr
}

// Graph exposes the underlying execution dependency graph (for Mission
// Controller's Dependency Graph / Live Graph / Execution Timeline
// views).
func (r *Runtime) Graph() *execgraph.Graph { return r.graph }

// Watcher exposes the underlying self-heal watcher (for Mission
// Controller's Critical Alert / Recovery views).
func (r *Runtime) Watcher() *selfheal.Watcher { return r.watcher }

// BootOrder returns the fixed topological order computed at Boot.
func (r *Runtime) BootOrder() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.order...)
}
