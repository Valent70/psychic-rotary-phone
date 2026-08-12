// Package execgraph implements Veriqo's Execution Graph — closing gap
// #5 in "Kritik_terbesar_nomor_1.docx": "Belum ada Execution Graph ...
// Saya ingin nanti scheduler mengetahui Task A -> Task B -> Task C
// secara otomatis. Bukan hanya queue."
//
// Unlike pkg/workflow (which runs a fixed DAG once, sequentially, with
// checkpoint/resume), Graph here is a live, inspectable structure: the
// Kernel Runtime and Mission Controller register tasks with declared
// dependencies, query live per-node status (Pending/Running/Done/
// Failed), ask "what can run now" (its dependency frontier), and get a
// Dependents()/Dependencies() lineage view for root-cause queries — the
// same auditability the critique asks for at the whole-system level.
//
// Zero external deps. No time.Now(): status transitions are recorded
// against caller-supplied logical ticks.
package execgraph

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	ErrUnknownNode = errors.New("execgraph: unknown node")
	ErrNodeExists  = errors.New("execgraph: node already exists")
	ErrCycle       = errors.New("execgraph: adding this edge would create a cycle")
	ErrNotReady    = errors.New("execgraph: node has unmet dependencies")
)

// Status is a node's live execution state.
type Status string

const (
	Pending Status = "PENDING"
	Ready   Status = "READY" // all dependencies Done
	Running Status = "RUNNING"
	Done    Status = "DONE"
	Failed  Status = "FAILED"
)

// Node is one task in the execution graph.
type node struct {
	id     string
	status Status
	deps   map[string]struct{} // IDs this node depends on
	rdeps  map[string]struct{} // IDs that depend on this node (dependents)
	tick   uint64              // logical tick of last status change
}

// Event is one recorded status transition, for the Mission Controller's
// Execution Timeline view.
type Event struct {
	NodeID string
	From   Status
	To     Status
	Tick   uint64
}

// Graph is the live execution dependency graph.
type Graph struct {
	mu     sync.Mutex
	nodes  map[string]*node
	events []Event
}

func NewGraph() *Graph {
	return &Graph{nodes: make(map[string]*node)}
}

// AddNode registers a task with its declared dependencies. All
// dependency IDs must already exist in the graph (add in topological
// order) — this also guarantees the graph stays acyclic by
// construction, rather than needing a separate cycle-detection pass.
func (g *Graph) AddNode(id string, deps ...string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, exists := g.nodes[id]; exists {
		return fmt.Errorf("%w: %s", ErrNodeExists, id)
	}
	n := &node{id: id, status: Pending, deps: make(map[string]struct{}), rdeps: make(map[string]struct{})}
	for _, d := range deps {
		dn, ok := g.nodes[d]
		if !ok {
			return fmt.Errorf("%w: dependency %s of %s", ErrUnknownNode, d, id)
		}
		n.deps[d] = struct{}{}
		dn.rdeps[id] = struct{}{}
	}
	if len(deps) == 0 {
		n.status = Ready
	}
	g.nodes[id] = n
	return nil
}

// AddEdge declares an additional dependency between two already-added
// nodes (to..depends-on..from), rejecting the edge if it would create a
// cycle (detected via a bounded DFS from `to` back to `from`).
func (g *Graph) AddEdge(from, to string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	fn, ok := g.nodes[from]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownNode, from)
	}
	tn, ok := g.nodes[to]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownNode, to)
	}
	if g.reachableLocked(to, from) {
		return fmt.Errorf("%w: %s -> %s", ErrCycle, from, to)
	}
	tn.deps[from] = struct{}{}
	fn.rdeps[to] = struct{}{}
	g.recomputeReadyLocked(to)
	return nil
}

// reachableLocked reports whether target is reachable from start by
// following dependency edges forward (start -> ... -> target), which
// would mean adding start<-target creates a cycle.
func (g *Graph) reachableLocked(start, target string) bool {
	if start == target {
		return true
	}
	seen := map[string]bool{start: true}
	queue := []string{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for d := range g.nodes[cur].rdeps {
			if d == target {
				return true
			}
			if !seen[d] {
				seen[d] = true
				queue = append(queue, d)
			}
		}
	}
	return false
}

// recomputeReadyLocked recomputes id's readiness against its current
// dependency set, in either direction. This is called both when a
// node's dependency completes (Pending -> Ready is the only direction
// that mattered originally) and — since AddEdge can attach a brand-new,
// still-incomplete dependency to a node that was already Ready with no
// deps or fully-satisfied deps — must also be able to retract readiness
// (Ready -> Pending). The original version only ever upgraded, so a
// node made Ready before a later AddEdge call would stay incorrectly
// marked Ready forever, even after gaining an unmet dependency: a real
// scheduling-safety bug found while closing this package's coverage
// gap, not a synthetic one. Running/Done/Failed nodes are left alone —
// a dependency graph edit must never retroactively un-run or un-finish
// a node that has already started or completed.
func (g *Graph) recomputeReadyLocked(id string) {
	n := g.nodes[id]
	if n.status != Pending && n.status != Ready {
		return
	}
	ready := true
	for d := range n.deps {
		if g.nodes[d].status != Done {
			ready = false
			break
		}
	}
	if ready {
		n.status = Ready
	} else {
		n.status = Pending
	}
}

// SetStatus transitions a node's status, recording the tick, and — on
// transition to Done — recomputes readiness for its direct dependents.
func (g *Graph) SetStatus(id string, to Status, tick uint64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	n, ok := g.nodes[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownNode, id)
	}
	if to == Running && n.status != Ready {
		return fmt.Errorf("%w: %s is %s, not Ready", ErrNotReady, id, n.status)
	}
	from := n.status
	n.status = to
	n.tick = tick
	g.events = append(g.events, Event{NodeID: id, From: from, To: to, Tick: tick})
	if to == Done {
		for d := range n.rdeps {
			g.recomputeReadyLocked(d)
		}
	}
	return nil
}

// Status returns a node's current status.
func (g *Graph) Status(id string) (Status, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	n, ok := g.nodes[id]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnknownNode, id)
	}
	return n.status, nil
}

// Frontier returns the sorted IDs of every node currently Ready — what
// the scheduler can dispatch right now, computed from the live graph
// rather than a manually-maintained queue.
func (g *Graph) Frontier() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for id, n := range g.nodes {
		if n.status == Ready {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// Dependencies returns the sorted direct dependency IDs of a node.
func (g *Graph) Dependencies(id string) []string {
	return g.neighborSet(id, false)
}

// Dependents returns the sorted direct dependent IDs of a node.
func (g *Graph) Dependents(id string) []string {
	return g.neighborSet(id, true)
}

func (g *Graph) neighborSet(id string, reverse bool) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	n, ok := g.nodes[id]
	if !ok {
		return nil
	}
	src := n.deps
	if reverse {
		src = n.rdeps
	}
	out := make([]string, 0, len(src))
	for k := range src {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Lineage performs a bounded-depth BFS over dependencies (or, if
// forward is true, dependents), returning IDs in discovery order — the
// root-cause / impact-analysis query the critique's "engine dependency
// graph belum hidup sebagai graph operasional" gap asks for.
func (g *Graph) Lineage(id string, forward bool, maxDepth int) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.nodes[id]; !ok {
		return nil
	}
	var out []string
	seen := map[string]bool{id: true}
	type item struct {
		id    string
		depth int
	}
	queue := []item{{id, 0}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.depth >= maxDepth {
			continue
		}
		src := g.nodes[cur.id].deps
		if forward {
			src = g.nodes[cur.id].rdeps
		}
		ids := make([]string, 0, len(src))
		for k := range src {
			ids = append(ids, k)
		}
		sort.Strings(ids)
		for _, next := range ids {
			if !seen[next] {
				seen[next] = true
				out = append(out, next)
				queue = append(queue, item{next, cur.depth + 1})
			}
		}
	}
	return out
}

// Timeline returns a defensive copy of every recorded status-transition
// event, in occurrence order — the Mission Controller's Execution
// Timeline data source.
func (g *Graph) Timeline() []Event {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]Event, len(g.events))
	copy(out, g.events)
	return out
}

// AllDone reports whether every node in the graph is Done.
func (g *Graph) AllDone() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, n := range g.nodes {
		if n.status != Done {
			return false
		}
	}
	return true
}

// NodeIDs returns every node ID, sorted.
func (g *Graph) NodeIDs() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]string, 0, len(g.nodes))
	for id := range g.nodes {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
