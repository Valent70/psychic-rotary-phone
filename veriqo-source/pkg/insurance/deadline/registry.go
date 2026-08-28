package deadline

import (
	"errors"
	"fmt"
	"sync"
)

var (
	ErrDuplicateRuleID = errors.New("deadline: RuleID already registered")
	ErrRuleNotFound    = errors.New("deadline: RuleID not found in this registry")
)

// Registry is a case- or claim-scoped store of deadline Rules, keyed by
// RuleID — the lookup table claim.TypeDefinition.DeadlineRules []string
// (rule IDs) resolves against. Thread-safe, like every other mutated
// pkg/insurance registry.
type Registry struct {
	mu    sync.RWMutex
	rules map[string]Rule
	order []string
}

// NewRegistry returns an empty deadline-rule registry.
func NewRegistry() *Registry {
	return &Registry{rules: make(map[string]Rule)}
}

// Register adds a validated rule to the registry. Refuses an invalid
// rule (see Rule.Validate) and a duplicate RuleID.
func (reg *Registry) Register(r Rule) error {
	if err := r.Validate(); err != nil {
		return err
	}
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if _, exists := reg.rules[r.RuleID]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateRuleID, r.RuleID)
	}
	reg.rules[r.RuleID] = r
	reg.order = append(reg.order, r.RuleID)
	return nil
}

// Get returns the rule for ruleID.
func (reg *Registry) Get(ruleID string) (Rule, bool) {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	r, ok := reg.rules[ruleID]
	return r, ok
}

// All returns every registered rule in registration order.
func (reg *Registry) All() []Rule {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	out := make([]Rule, 0, len(reg.order))
	for _, id := range reg.order {
		out = append(out, reg.rules[id])
	}
	return out
}

// Count returns the number of rules in the registry.
func (reg *Registry) Count() int {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	return len(reg.rules)
}

// ComputeAll resolves every rule in the registry against a single
// triggerTick, returning one Deadline per rule (in registration order).
// It stops at the first error rather than silently skipping an invalid
// rule — a Registry, by construction (Register validates), should never
// contain one, but ComputeDeadline's own validation is the actual
// guard, not an assumption about how rules got into the map.
func (reg *Registry) ComputeAll(triggerTick uint64) ([]Deadline, error) {
	reg.mu.RLock()
	ids := make([]string, len(reg.order))
	copy(ids, reg.order)
	reg.mu.RUnlock()

	out := make([]Deadline, 0, len(ids))
	for _, id := range ids {
		reg.mu.RLock()
		r, ok := reg.rules[id]
		reg.mu.RUnlock()
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrRuleNotFound, id)
		}
		d, err := ComputeDeadline(r, triggerTick)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}
