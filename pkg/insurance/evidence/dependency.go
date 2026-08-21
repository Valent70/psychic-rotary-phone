// Dependency graph — blueprint §10 ("Evidence Independence"). The
// blueprint's own worked example:
//
//	Photo A -> Statement A -> Survey A
//
// Three submitted items, but Survey A cites Statement A which cites
// Photo A, so this is ONE independent source, not three. Any downstream
// consumer that counts "how many independent sources support X" must
// go through IndependentRoots, never len(evidence list).
package evidence

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrDependencySelfLoop      = errors.New("evidence: an evidence item cannot depend on itself")
	ErrDependencyCycle         = errors.New("evidence: adding this dependency would create a cycle")
	ErrDependencyUnknownParent = errors.New("evidence: parent EvidenceID must be added to the registry before it can be a dependency source")
)

// DependencyGraph tracks which evidence items were derived from, or
// cite, other evidence items. A directed edge child -> parent means
// "child's content depends on / was derived from parent" (e.g.
// Survey A -> Statement A: the survey CITES the statement).
type DependencyGraph struct {
	mu       sync.RWMutex
	parents  map[string]map[string]bool // child -> set of parents it depends on
	children map[string]map[string]bool // parent -> set of children that depend on it
}

// NewDependencyGraph returns an empty dependency graph.
func NewDependencyGraph() *DependencyGraph {
	return &DependencyGraph{
		parents:  make(map[string]map[string]bool),
		children: make(map[string]map[string]bool),
	}
}

// AddDependency records that child depends on (was derived from, or
// cites) parent. Refuses a self-loop and refuses any edge that would
// create a cycle (a real corroboration/dependency graph is a DAG by
// construction — a cycle would mean "A supports B supports A", which
// is not evidence of anything).
func (g *DependencyGraph) AddDependency(child, parent string) error {
	if child == "" || parent == "" {
		return errors.New("evidence: child and parent EvidenceIDs must be non-empty")
	}
	if child == parent {
		return ErrDependencySelfLoop
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.reachable(parent, child) {
		return ErrDependencyCycle
	}

	if g.parents[child] == nil {
		g.parents[child] = make(map[string]bool)
	}
	g.parents[child][parent] = true
	if g.children[parent] == nil {
		g.children[parent] = make(map[string]bool)
	}
	g.children[parent][child] = true
	return nil
}

// reachable reports whether target is reachable from start by
// following parent edges (start depends on ... depends on target).
// Caller must hold g.mu.
func (g *DependencyGraph) reachable(start, target string) bool {
	if start == target {
		return true
	}
	visited := map[string]bool{start: true}
	stack := []string{start}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for p := range g.parents[n] {
			if p == target {
				return true
			}
			if !visited[p] {
				visited[p] = true
				stack = append(stack, p)
			}
		}
	}
	return false
}

// DirectParents returns the direct dependencies of id.
func (g *DependencyGraph) DirectParents(id string) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return sortedKeys(g.parents[id])
}

// Root reports whether id has no recorded dependencies (nothing it is
// derived from, within this graph) — i.e. it is itself an independent
// source, or its dependency chain has not been recorded.
func (g *DependencyGraph) Root(id string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.parents[id]) == 0
}

// Roots walks every id's dependency chain back to its ultimate
// ancestors and returns the DEDUPLICATED set of root (independent)
// sources that the given ids collectively rest on. This is the
// function any corroboration count must use instead of len(ids):
// three evidence items resting on the same one root count as ONE
// independent source.
func (g *DependencyGraph) Roots(ids []string) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	rootSet := make(map[string]bool)
	for _, id := range ids {
		for _, r := range g.ancestorRootsLocked(id) {
			rootSet[r] = true
		}
	}
	return sortedKeys(rootSet)
}

// ancestorRootsLocked returns the root ancestors of id (id itself if
// it has no parents). Caller must hold g.mu (read or write).
func (g *DependencyGraph) ancestorRootsLocked(id string) []string {
	parents := g.parents[id]
	if len(parents) == 0 {
		return []string{id}
	}
	rootSet := make(map[string]bool)
	visited := map[string]bool{id: true}
	stack := []string{id}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		ps := g.parents[n]
		if len(ps) == 0 {
			rootSet[n] = true
			continue
		}
		for p := range ps {
			if !visited[p] {
				visited[p] = true
				stack = append(stack, p)
			}
		}
	}
	return sortedKeys(rootSet)
}

// IndependentCount returns the number of DISTINCT independent root
// sources backing ids — the correct corroboration count, per §10.
func (g *DependencyGraph) IndependentCount(ids []string) int {
	return len(g.Roots(ids))
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
