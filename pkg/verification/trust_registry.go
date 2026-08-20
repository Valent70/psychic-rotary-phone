// trust_registry.go adds external audit item A16's required "trust
// registry" concept to this package. Deliberately kept here rather than
// reusing pkg/platform/security/keys.State (A11's key lifecycle) or
// pkg/governance/qualification.TrustRegistry: both of those packages sit
// deeper in the dependency graph than this one, and pkg/verification's
// entire value (see this file's sibling doc comments) is that it can be
// imported and run with ZERO veriqo/ internal dependencies — a
// standalone verifier binary that genuinely does not need to trust or
// even build against the rest of the VERIQO runtime. Adding an import
// here to reuse another package's trust-state enum would break exactly
// the property audit item A16 asks for.
package verification

import (
	"encoding/json"
	"fmt"
)

// TrustState is this package's own minimal trust-registry vocabulary —
// deliberately smaller than pkg/platform/security/keys.State's 4-value
// lifecycle (PENDING/ACTIVE/RETIRED/REVOKED), because a standalone
// verifier only ever needs to answer one question about a key it is
// handed: is it currently trusted, explicitly revoked, or simply
// unknown to this registry.
type TrustState string

const (
	TrustActive  TrustState = "ACTIVE"
	TrustRevoked TrustState = "REVOKED"
	// TrustUnknown is returned by StateOf for any public key not present
	// in the registry at all — never confused with TrustRevoked: an
	// unknown key was never vouched for, a revoked key was vouched for
	// and then withdrawn. Both fail verification, but for a
	// distinguishable, auditable reason (INVALID vs TRUST_REVOKED — see
	// cmd/veriqo-verifier).
	TrustUnknown TrustState = "UNKNOWN"
)

// TrustRegistryEntry is one key's record.
type TrustRegistryEntry struct {
	Label string     `json:"label,omitempty"` // human-readable, e.g. "veriqo-release-signing-key-2026"
	State TrustState `json:"state"`
}

// TrustRegistry maps a hex-encoded public key (the same PublicKey format
// SignedCertificate.PublicKey and SigningKey.PublicKeyHex already use)
// to its trust state.
type TrustRegistry struct {
	Entries map[string]TrustRegistryEntry `json:"entries"`
}

// NewTrustRegistry returns an empty registry.
func NewTrustRegistry() *TrustRegistry {
	return &TrustRegistry{Entries: make(map[string]TrustRegistryEntry)}
}

// Trust marks publicKeyHex as ACTIVE.
func (t *TrustRegistry) Trust(publicKeyHex, label string) {
	t.Entries[publicKeyHex] = TrustRegistryEntry{Label: label, State: TrustActive}
}

// Revoke marks publicKeyHex as REVOKED — distinct from simply removing
// it, so a caller can tell "never trusted" apart from "trusted, then
// withdrawn" (see StateOf's doc comment).
func (t *TrustRegistry) Revoke(publicKeyHex, label string) {
	t.Entries[publicKeyHex] = TrustRegistryEntry{Label: label, State: TrustRevoked}
}

// StateOf returns publicKeyHex's registered state, or TrustUnknown if it
// was never registered.
func (t *TrustRegistry) StateOf(publicKeyHex string) TrustState {
	if e, ok := t.Entries[publicKeyHex]; ok {
		return e.State
	}
	return TrustUnknown
}

// LoadTrustRegistry parses a registry from JSON bytes — the same
// "self-contained, hand it to an auditor" property every other artifact
// in this package already has (TrustRegistry needs no custom
// MarshalJSON: its Entries field is already exported, so
// json.MarshalIndent(t, "", "  ") round-trips it directly).
func LoadTrustRegistry(data []byte) (*TrustRegistry, error) {
	var t TrustRegistry
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("verification: parse trust registry: %w", err)
	}
	if t.Entries == nil {
		t.Entries = make(map[string]TrustRegistryEntry)
	}
	return &t, nil
}
