// Package tenancy is the "Tenant Membership" stage the reviewer's own
// architecture diagram names for Commercialization Sprint P0-B:
//
//	JWT/OIDC Identity -> Verified Subject -> Tenant Membership
//	  -> Authorized Tenant Context -> Commercial API -> Store
//
// Before this package, the Commercial API v1 HTTP layer trusted
// whatever TenantID a caller's request body or query string named,
// with no check that the authenticated subject (the JWT's own
// verified `sub` claim) was ever actually granted that tenant. A
// caller holding a perfectly valid JWT for their OWN identity could
// still supply a DIFFERENT tenant's ID and reach that tenant's data,
// because nothing tied the two together -- named explicitly as an
// honest gap in this package's prior round
// ("TenantID is currently a caller-supplied field ... not yet
// cryptographically bound to the verified JWT identity").
//
// Membership closes that: it is the authoritative record of which
// authenticated subjects may act as which tenants. The invariant this
// package exists to enforce, stated in the reviewer's own terms, is
//
//	effectiveTenantID == authenticated subject's authorized tenant
//
// and a client must never be able to choose a tenant arbitrarily --
// see veriqo/gateway/rest/commercial_v1_routes.go's effectiveTenantID
// for where this Membership is actually consulted on every request.
package tenancy

import "sync"

// Membership is a real, minimal subject -> authorized-tenant-set
// registry. The zero value is not usable -- construct with New.
type Membership struct {
	mu     sync.RWMutex
	grants map[string]map[string]bool // subject -> set of tenantIDs
}

// New returns an empty Membership: no subject is authorized for any
// tenant until Grant is called.
func New() *Membership {
	return &Membership{grants: make(map[string]map[string]bool)}
}

// Grant authorizes subject to act as tenantID. Idempotent -- granting
// the same pair twice has no additional effect.
func (m *Membership) Grant(subject, tenantID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.grants[subject] == nil {
		m.grants[subject] = make(map[string]bool)
	}
	m.grants[subject][tenantID] = true
}

// Revoke removes subject's authorization for tenantID, if any.
// Idempotent -- revoking a grant that does not exist is a no-op, not
// an error.
func (m *Membership) Revoke(subject, tenantID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.grants[subject], tenantID)
}

// IsAuthorized reports whether subject has been granted tenantID. A
// subject with no grants at all (never seen by this Membership)
// returns false, not a panic or a default-allow -- this package fails
// closed.
func (m *Membership) IsAuthorized(subject, tenantID string) bool {
	if subject == "" || tenantID == "" {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.grants[subject][tenantID]
}

// TenantsFor returns every tenant subject is currently authorized for,
// in no particular order. Returns nil for a subject with no grants.
func (m *Membership) TenantsFor(subject string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	set := m.grants[subject]
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for tenantID := range set {
		out = append(out, tenantID)
	}
	return out
}
