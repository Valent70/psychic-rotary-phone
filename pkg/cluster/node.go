// Package cluster implements the distributed runtime layer for Veriqo.
// This is the "NEW: distributed runtime" layer from the enterprise repo structure doc.
// It wires multiple Raft nodes into a cohesive cluster with node metadata,
// health tracking, and placement algorithms.
package cluster

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"veriqo/pkg/core"
)

// ─── Node metadata ────────────────────────────────────────────────────────────

// NodeStatus is the health status of a cluster member.
type NodeStatus string

const (
	NodeStatusHealthy     NodeStatus = "healthy"
	NodeStatusSuspect     NodeStatus = "suspect"
	NodeStatusUnreachable NodeStatus = "unreachable"
	NodeStatusLeft        NodeStatus = "left"
)

// Region is a geographic region tag (e.g. "us-east-1", "eu-west-1").
type Region string

// Shard is a data partition identifier.
type Shard uint32

// NodeInfo describes a cluster member.
type NodeInfo struct {
	ID       core.NodeID
	Addr     string // host:port for gRPC
	Region   Region
	Shard    Shard
	Status   NodeStatus
	JoinedAt time.Time
	LastSeen time.Time
	Tags     map[string]string // arbitrary metadata
	// Capacity is a 0.0–1.0 load factor; 1.0 = fully loaded.
	Capacity float64
}

// IsVoter returns true if this node is a Raft voter (not a learner).
func (n *NodeInfo) IsVoter() bool {
	return n.Tags["role"] != "learner"
}

// ─── Registry ─────────────────────────────────────────────────────────────────

// Registry is the cluster member registry.
// It is updated by the gossip/membership layer and read by the placement engine.
type Registry struct {
	mu      sync.RWMutex
	nodes   map[core.NodeID]*NodeInfo
	version atomic.Uint64
}

// NewRegistry creates an empty cluster registry.
func NewRegistry() *Registry {
	return &Registry{nodes: make(map[core.NodeID]*NodeInfo)}
}

// Upsert adds or updates a node in the registry.
func (r *Registry) Upsert(info NodeInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := info
	if cp.Tags == nil {
		cp.Tags = make(map[string]string)
	}
	r.nodes[info.ID] = &cp
	r.version.Add(1)
}

// Remove marks a node as having left the cluster.
func (r *Registry) Remove(id core.NodeID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n, ok := r.nodes[id]; ok {
		n.Status = NodeStatusLeft
	}
	r.version.Add(1)
}

// Get returns the NodeInfo for the given ID.
func (r *Registry) Get(id core.NodeID) (*NodeInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n, ok := r.nodes[id]
	return n, ok
}

// List returns all nodes matching the predicate.
func (r *Registry) List(pred func(*NodeInfo) bool) []*NodeInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*NodeInfo
	for _, n := range r.nodes {
		if pred == nil || pred(n) {
			cp := *n
			out = append(out, &cp)
		}
	}
	return out
}

// Healthy returns all currently healthy nodes.
func (r *Registry) Healthy() []*NodeInfo {
	return r.List(func(n *NodeInfo) bool { return n.Status == NodeStatusHealthy })
}

// Version returns a monotone version counter that increments on every change.
func (r *Registry) Version() uint64 { return r.version.Load() }

// ─── Health monitor ───────────────────────────────────────────────────────────

// HealthConfig controls the health monitor.
type HealthConfig struct {
	CheckInterval    time.Duration
	SuspectAfter     time.Duration
	UnreachableAfter time.Duration
}

// HealthMonitor continuously scans the registry and downgrades stale nodes.
type HealthMonitor struct {
	cfg      HealthConfig
	registry *Registry
	stopCh   chan struct{}
}

// NewHealthMonitor creates and starts a HealthMonitor.
func NewHealthMonitor(cfg HealthConfig, reg *Registry) *HealthMonitor {
	if cfg.CheckInterval == 0 {
		cfg.CheckInterval = 5 * time.Second
	}
	if cfg.SuspectAfter == 0 {
		cfg.SuspectAfter = 10 * time.Second
	}
	if cfg.UnreachableAfter == 0 {
		cfg.UnreachableAfter = 30 * time.Second
	}
	m := &HealthMonitor{cfg: cfg, registry: reg, stopCh: make(chan struct{})}
	go m.run()
	return m
}

// Stop shuts down the monitor.
func (m *HealthMonitor) Stop() { close(m.stopCh) }

func (m *HealthMonitor) run() {
	tk := time.NewTicker(m.cfg.CheckInterval)
	defer tk.Stop()
	for {
		select {
		case <-m.stopCh:
			return
		case <-tk.C:
			m.scan()
		}
	}
}

func (m *HealthMonitor) scan() {
	now := time.Now()
	nodes := m.registry.List(nil)
	for _, n := range nodes {
		switch {
		case now.Sub(n.LastSeen) >= m.cfg.UnreachableAfter:
			n.Status = NodeStatusUnreachable
		case now.Sub(n.LastSeen) >= m.cfg.SuspectAfter:
			n.Status = NodeStatusSuspect
		}
		m.registry.Upsert(*n)
	}
}

// ─── Placement ────────────────────────────────────────────────────────────────

// PlacementStrategy determines how processes are assigned to nodes.
type PlacementStrategy string

const (
	PlacementRoundRobin     PlacementStrategy = "round-robin"
	PlacementLeastLoaded    PlacementStrategy = "least-loaded"
	PlacementRegionAffinity PlacementStrategy = "region-affinity"
)

// PlacementEngine selects a cluster node for a new process.
type PlacementEngine struct {
	registry *Registry
	strategy PlacementStrategy
	rrIdx    atomic.Uint64
}

// NewPlacementEngine creates a PlacementEngine.
func NewPlacementEngine(reg *Registry, strategy PlacementStrategy) *PlacementEngine {
	return &PlacementEngine{registry: reg, strategy: strategy}
}

// Select returns the best node for a new process given the hint.
func (pe *PlacementEngine) Select(preferRegion Region) (*NodeInfo, error) {
	healthy := pe.registry.Healthy()
	if len(healthy) == 0 {
		return nil, fmt.Errorf("cluster: no healthy nodes available")
	}

	switch pe.strategy {
	case PlacementRoundRobin:
		idx := pe.rrIdx.Add(1) % uint64(len(healthy))
		return healthy[idx], nil

	case PlacementLeastLoaded:
		best := healthy[0]
		for _, n := range healthy[1:] {
			if n.Capacity < best.Capacity {
				best = n
			}
		}
		return best, nil

	case PlacementRegionAffinity:
		for _, n := range healthy {
			if n.Region == preferRegion {
				return n, nil
			}
		}
		// Fallback: any healthy node.
		return healthy[0], nil
	}
	return healthy[0], nil
}
