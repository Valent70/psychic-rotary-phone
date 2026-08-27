package cluster

import (
	"fmt"
	"sync"
	"time"

	"veriqo/pkg/core"
)

// ─── Membership ───────────────────────────────────────────────────────────────
// Membership tracks the distributed cluster membership using a gossip-style
// propagation model. Nodes exchange their view periodically and reconcile.

// Event is a cluster membership event.
type Event struct {
	Type      EventType
	NodeID    core.NodeID
	NodeInfo  NodeInfo
	Timestamp time.Time
}

// EventType classifies a membership change.
type EventType string

const (
	EventJoined  EventType = "joined"
	EventLeft    EventType = "left"
	EventUpdated EventType = "updated"
	EventFailed  EventType = "failed"
)

// EventHandler is called when a membership event fires.
type EventHandler func(Event)

// Membership manages cluster membership with event propagation.
type Membership struct {
	mu       sync.RWMutex
	local    NodeInfo
	registry *Registry
	handlers []EventHandler
}

// NewMembership creates a Membership manager for the given local node.
func NewMembership(local NodeInfo, reg *Registry) *Membership {
	local.JoinedAt = time.Now().UTC()
	local.LastSeen = local.JoinedAt
	local.Status = NodeStatusHealthy
	reg.Upsert(local)
	return &Membership{local: local, registry: reg}
}

// OnEvent registers a handler for membership events.
func (m *Membership) OnEvent(h EventHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handlers = append(m.handlers, h)
}

// Join registers a new node joining the cluster.
func (m *Membership) Join(info NodeInfo) error {
	if info.ID == "" {
		return fmt.Errorf("membership: joining node must have an ID")
	}
	info.JoinedAt = time.Now().UTC()
	info.LastSeen = info.JoinedAt
	info.Status = NodeStatusHealthy
	m.registry.Upsert(info)
	m.emit(Event{Type: EventJoined, NodeID: info.ID, NodeInfo: info, Timestamp: info.JoinedAt})
	return nil
}

// Leave gracefully removes a node from the cluster.
func (m *Membership) Leave(id core.NodeID) {
	m.registry.Remove(id)
	m.emit(Event{Type: EventLeft, NodeID: id, Timestamp: time.Now().UTC()})
}

// Heartbeat updates the LastSeen timestamp for a node, keeping it healthy.
func (m *Membership) Heartbeat(id core.NodeID) {
	info, ok := m.registry.Get(id)
	if !ok {
		return
	}
	info.LastSeen = time.Now().UTC()
	if info.Status == NodeStatusSuspect {
		info.Status = NodeStatusHealthy
	}
	m.registry.Upsert(*info)
}

// Members returns all known cluster members.
func (m *Membership) Members() []*NodeInfo {
	return m.registry.List(nil)
}

// LocalID returns the ID of the local node.
func (m *Membership) LocalID() core.NodeID { return m.local.ID }

func (m *Membership) emit(e Event) {
	m.mu.RLock()
	handlers := make([]EventHandler, len(m.handlers))
	copy(handlers, m.handlers)
	m.mu.RUnlock()
	for _, h := range handlers {
		go h(e)
	}
}
