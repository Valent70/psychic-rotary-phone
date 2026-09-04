// Package tenant implements the VERIQO tenant anchor.
//
// # Why a tenant_id column is not tenant isolation
//
// The design law is stated in the specification as a prohibition:
//
//	Tidak boleh hanya: tenant_id column.
//
// A tenant_id column isolates data exactly as well as every query that
// touches it remembers to filter on it. One forgotten WHERE clause,
// one cache key that omits the tenant, one search index shared across
// tenants, one agent context carried between cases -- and the
// isolation was never there. The failure is silent, it is discovered
// by the customer, and it is unrecoverable once data has crossed.
//
// So the anchor here is cryptographic. Every tenant holds a distinct
// key-derivation root; a Scope derives per-purpose subkeys from it;
// and any identifier that leaves the tenant boundary is bound to that
// derivation. Reading another tenant's data does not merely require a
// missing filter -- it requires a key nobody in the calling context
// holds.
//
// # What is derived, and why separately
//
//	storage    object-store prefix and encryption key
//	cache      cache key namespace
//	graph      graph partition
//	search     search index namespace
//	export     export package signing context
//	agent      agent working-context namespace
//
// They are derived separately so that a compromise or a mistake in one
// surface does not yield the others. Deriving one key and reusing it
// everywhere would make the ceremony decorative.
package tenant

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"veriqo/pkg/contract"
)

var (
	ErrNoAnchor       = errors.New("tenant: no anchor; a tenant with no cryptographic root is a column")
	ErrBadTenantID    = errors.New("tenant: malformed tenant id")
	ErrUnknownSurface = errors.New("tenant: unknown isolation surface")
	ErrShortRoot      = errors.New("tenant: derivation root is too short to be a root")
	ErrScopeMismatch  = errors.New("tenant: scope does not belong to this tenant")
)

// MinRootBytes is the smallest acceptable derivation root. 32 bytes is
// the output width of SHA-256; anything shorter caps the entropy of
// every key derived from it, however long those keys are printed.
const MinRootBytes = 32

// Surface is an isolation surface. Each gets its own derived key.
type Surface string

const (
	Storage  Surface = "storage"
	Cache    Surface = "cache"
	Graph    Surface = "graph"
	Search   Surface = "search"
	Export   Surface = "export"
	AgentCtx Surface = "agent"
	Ledger   Surface = "ledger"
)

// Surfaces returns every surface that must be isolated. Gate G14
// enumerates this list rather than a hand-written one, so adding a
// surface without isolating it fails the gate.
func Surfaces() []Surface {
	return []Surface{Storage, Cache, Graph, Search, Export, AgentCtx, Ledger}
}

func (s Surface) Valid() bool {
	for _, k := range Surfaces() {
		if k == s {
			return true
		}
	}
	return false
}

var tenantIDPattern = regexp.MustCompile(`^t-[a-z0-9]([a-z0-9-]{0,60}[a-z0-9])?$`)

// Anchor is a tenant's cryptographic root.
//
// The root never leaves this type: there is no accessor for it, and
// the JSON form omits it. A caller receives derived, purpose-bound
// keys or nothing.
type Anchor struct {
	id   string
	root []byte
}

// NewAnchor builds a tenant anchor from a root supplied by the KMS.
//
// The root is supplied, not generated here, because a root this
// package invented would live only in this process's memory and could
// not be rotated, escrowed or attested. Gate G1 is about where this
// value comes from.
func NewAnchor(tenantID string, root []byte) (*Anchor, error) {
	if !tenantIDPattern.MatchString(tenantID) {
		return nil, fmt.Errorf("%w: %q", ErrBadTenantID, tenantID)
	}
	if len(root) < MinRootBytes {
		return nil, fmt.Errorf("%w: %d bytes, need at least %d", ErrShortRoot, len(root), MinRootBytes)
	}
	cp := make([]byte, len(root))
	copy(cp, root)
	return &Anchor{id: tenantID, root: cp}, nil
}

// ID returns the tenant identifier.
func (a *Anchor) ID() string {
	if a == nil {
		return ""
	}
	return a.id
}

// derive produces a purpose-bound subkey.
//
// The label binds both the tenant id and the surface, so a key derived
// for tenant A's cache cannot equal one derived for tenant B's cache
// or for tenant A's search index, even if a caller confuses them.
func (a *Anchor) derive(s Surface, extra string) ([]byte, error) {
	if a == nil || len(a.root) == 0 {
		return nil, ErrNoAnchor
	}
	if !s.Valid() {
		return nil, fmt.Errorf("%w: %q", ErrUnknownSurface, s)
	}
	mac := hmac.New(sha256.New, a.root)
	// Length-prefixed fields: without them, ("ab","c") and ("a","bc")
	// would derive the same key, which is a real collision between a
	// tenant id and a surface name.
	writeField(mac, "veriqo/tenant/v1")
	writeField(mac, a.id)
	writeField(mac, string(s))
	writeField(mac, extra)
	return mac.Sum(nil), nil
}

func writeField(h interface{ Write([]byte) (int, error) }, s string) {
	var n [4]byte
	l := len(s)
	n[0] = byte(l >> 24)
	n[1] = byte(l >> 16)
	n[2] = byte(l >> 8)
	n[3] = byte(l)
	_, _ = h.Write(n[:])
	_, _ = h.Write([]byte(s))
}

// Key returns the derived key for a surface.
func (a *Anchor) Key(s Surface) ([]byte, error) { return a.derive(s, "") }

// SubKey returns a key bound to a surface AND a further discriminator,
// which is how per-case keys are derived: a case key is a subkey of
// the tenant's storage key, so revoking a case does not require
// touching the tenant root.
func (a *Anchor) SubKey(s Surface, discriminator string) ([]byte, error) {
	if strings.TrimSpace(discriminator) == "" {
		return nil, errors.New("tenant: a subkey needs a discriminator")
	}
	return a.derive(s, discriminator)
}

// Namespace returns the opaque, stable namespace string for a surface:
// the storage prefix, the cache key prefix, the graph partition.
//
// It is a digest, not the tenant id, so a shared index cannot be
// browsed for tenant names, and so a namespace cannot be guessed by
// anyone who merely knows a customer exists.
func (a *Anchor) Namespace(s Surface) (string, error) {
	k, err := a.Key(s)
	if err != nil {
		return "", err
	}
	return string(s) + "_" + hex.EncodeToString(k[:16]), nil
}

// Scope is a capability: holding one is what permits access to a
// tenant's data on a surface. It carries the derived key, not the root.
type Scope struct {
	TenantID string
	Surface  Surface
	key      []byte
}

// Scope derives a capability for one surface.
func (a *Anchor) Scope(s Surface) (Scope, error) {
	k, err := a.Key(s)
	if err != nil {
		return Scope{}, err
	}
	return Scope{TenantID: a.id, Surface: s, key: k}, nil
}

// KeyBytes exposes a copy of the derived key to the storage layer that
// needs it. It is a copy so a caller cannot mutate the scope it holds
// into a different one.
func (s Scope) KeyBytes() []byte {
	cp := make([]byte, len(s.key))
	copy(cp, s.key)
	return cp
}

// Guard is the fail-closed cross-tenant check.
//
// Every read and write path calls it. It compares in constant time,
// not because a timing attack on a tenant id is likely, but because
// the alternative invites somebody to "optimise" it into a prefix
// comparison.
func Guard(scope Scope, requestedTenant string) error {
	if scope.TenantID == "" || len(scope.key) == 0 {
		return ErrNoAnchor
	}
	if subtle.ConstantTimeCompare([]byte(scope.TenantID), []byte(requestedTenant)) != 1 {
		return fmt.Errorf("%w: scope is %s, request is %s", contract.ErrCrossTenant, scope.TenantID, requestedTenant)
	}
	return nil
}

// Verify checks a scope really was derived from this anchor. It is the
// defence against a fabricated Scope literal: a caller can construct
// the struct, but cannot construct its key without the root.
func (a *Anchor) Verify(s Scope) error {
	want, err := a.Key(s.Surface)
	if err != nil {
		return err
	}
	if s.TenantID != a.id || subtle.ConstantTimeCompare(want, s.key) != 1 {
		return fmt.Errorf("%w: %s/%s", ErrScopeMismatch, s.TenantID, s.Surface)
	}
	return nil
}
