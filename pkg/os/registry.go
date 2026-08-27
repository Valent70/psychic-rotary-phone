package os

import (
	"fmt"
	"sync"
)

// ─── Registry ─────────────────────────────────────────────────────────────────
// The service registry enables domain modules to discover and call each other
// through well-defined interfaces — no direct coupling allowed.

// ServiceFunc is a function registered as a named OS service.
type ServiceFunc func(input any) (any, error)

// Registry is the OS service registry.
type Registry struct {
	mu       sync.RWMutex
	services map[string]ServiceFunc
	policies map[string]PolicyChecker
}

// PolicyChecker evaluates a ComplianceInput.
type PolicyChecker interface {
	Check(input ComplianceInput) (compliant bool, violations []string)
}

// defaultPolicyChecker is the no-op fallback.
type defaultPolicyChecker struct{}

func (defaultPolicyChecker) Check(_ ComplianceInput) (bool, []string) { return true, nil }

// NewRegistry creates an empty service registry.
func NewRegistry() *Registry {
	return &Registry{
		services: make(map[string]ServiceFunc),
		policies: make(map[string]PolicyChecker),
	}
}

// Register adds a named service to the registry.
func (r *Registry) Register(name string, fn ServiceFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.services[name] = fn
}

// Lookup returns a registered service or an error.
func (r *Registry) Lookup(name string) (ServiceFunc, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	fn, ok := r.services[name]
	if !ok {
		return nil, fmt.Errorf("registry: service %q not found", name)
	}
	return fn, nil
}

// RegisterPolicy registers a policy checker by ID.
func (r *Registry) RegisterPolicy(id string, checker PolicyChecker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.policies[id] = checker
}

// CheckCompliance evaluates the compliance input against the registered policy.
func (r *Registry) CheckCompliance(input ComplianceInput) (bool, []string) {
	r.mu.RLock()
	checker, ok := r.policies[input.PolicyID]
	r.mu.RUnlock()
	if !ok {
		checker = defaultPolicyChecker{}
	}
	return checker.Check(input)
}
