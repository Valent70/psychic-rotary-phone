package os

import (
	"crypto/sha256"
	"encoding/json"
	"sync"

	"veriqo/pkg/core"
)

// ─── Digital Twin ─────────────────────────────────────────────────────────────

// DigitalTwin maintains a deterministic world-state model.
// State changes are hashed to produce a TwinStateHash for replay verification.
type DigitalTwin struct {
	mu     sync.RWMutex
	states map[string]map[string]any // tenantID → entityID → properties
}

// NewDigitalTwin creates an empty digital twin.
func NewDigitalTwin() *DigitalTwin {
	return &DigitalTwin{states: make(map[string]map[string]any)}
}

// Apply updates the entity properties for the given tenant and returns the new hash.
func (dt *DigitalTwin) Apply(tenant core.TenantID, delta TwinDelta) TwinStateHash {
	dt.mu.Lock()
	defer dt.mu.Unlock()

	tid := string(tenant)
	if dt.states[tid] == nil {
		dt.states[tid] = make(map[string]any)
	}
	entity, ok := dt.states[tid][delta.EntityID]
	if !ok {
		entity = make(map[string]any)
	}
	props, _ := entity.(map[string]any)
	for k, v := range delta.Properties {
		props[k] = v
	}
	dt.states[tid][delta.EntityID] = props

	return dt.hash(tid)
}

// Get returns the current properties for an entity.
func (dt *DigitalTwin) Get(tenant core.TenantID, entityID string) (map[string]any, bool) {
	dt.mu.RLock()
	defer dt.mu.RUnlock()
	s, ok := dt.states[string(tenant)]
	if !ok {
		return nil, false
	}
	e, ok := s[entityID]
	if !ok {
		return nil, false
	}
	props, _ := e.(map[string]any)
	// Return a copy to prevent mutation.
	out := make(map[string]any, len(props))
	for k, v := range props {
		out[k] = v
	}
	return out, true
}

// Hash returns the deterministic hash of the entire world state for a tenant.
func (dt *DigitalTwin) Hash(tenant core.TenantID) TwinStateHash {
	dt.mu.RLock()
	defer dt.mu.RUnlock()
	return dt.hash(string(tenant))
}

func (dt *DigitalTwin) hash(tid string) TwinStateHash {
	data, _ := json.Marshal(dt.states[tid])
	return TwinStateHash(sha256.Sum256(data))
}
