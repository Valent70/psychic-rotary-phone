// Package entity closes the P1 "Entity Resolution" gap the V7.10.1
// technical audit named explicitly: "Vessel A / IMO123 / Callsign XYZ
// / owned by Company B" arriving from different sources as different
// strings must resolve to ONE canonical entity before Evidence,
// Provenance, Digital Twin, or Ownership graphs can reason about it
// consistently — otherwise every downstream layer silently
// double-counts or mis-links the same real-world thing.
//
// Design, kept consistent with this repo's existing DARI discipline
// (no UUIDs, no time.Now(), hash-chained, replay-provable):
//
//   - An Alias is a (Kind, Value) pair — e.g. {"IMO","9998887"},
//     {"CALLSIGN","ABCD1"}, {"NAME","MV EXAMPLE"}.
//   - Registry is a weighted, path-compressed union-find over alias
//     keys. Merge(a,b) declares two aliases denote the same
//     real-world entity; this is the ONLY equivalence primitive —
//     Register is just Merge(alias, alias) as a convenience for a
//     brand-new single-alias entity.
//   - CanonicalID is content-addressed: SHA-256 over the sorted set
//     of every alias key currently in the entity's equivalence class.
//     This makes it a pure function of set MEMBERSHIP, not of
//     insertion order or which two aliases were merged last — proven
//     by TestCanonicalIDIsMergeOrderInvariant. It is deliberately NOT
//     a random UUID (this repo's stated invariant) and deliberately
//     NOT the first-registered alias (which would make the ID
//     insertion-order-dependent and break replay-from-different-order
//     determinism).
//   - Every Merge is hash-chained and append-only, exactly like
//     pkg/moat/provenance and pkg/moat/causal, so entity-resolution
//     decisions are themselves auditable and replayable — Rebuild
//     reconstructs the exact same canonical IDs purely from the
//     ledger.
package entity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"

	"veriqo/pkg/platform/telemetry"
)

// ErrTamperDetected mirrors the sibling MOAT packages' hash-chain
// verification failure.
var ErrTamperDetected = errors.New("entity: hash chain verification failed")

// Alias identifies one real-world entity by one external system's
// naming convention.
type Alias struct {
	Kind  string // e.g. "IMO", "CALLSIGN", "NAME", "MMSI", "DUNS"
	Value string
}

func (a Alias) key() string { return a.Kind + "|" + a.Value }

// CanonicalID is the content-addressed identifier for a resolved
// entity — see package comment for why this is SHA-256-derived rather
// than a UUID or the first alias seen.
type CanonicalID string

// MergeRecord is one hash-chained equivalence declaration.
type MergeRecord struct {
	Index       uint64
	ActorID     string
	AliasA      Alias
	AliasB      Alias
	CanonicalID CanonicalID // resulting canonical ID for the merged set, post-merge
	Tick        uint64
	PrevHash    string
	Hash        string
}

// Registry is a union-find over alias keys, safe for concurrent use.
type Registry struct {
	mu     sync.RWMutex
	parent map[string]string // alias key -> parent alias key
	rank   map[string]int
	log    []MergeRecord
	head   string
}

func NewRegistry() *Registry {
	return &Registry{parent: make(map[string]string), rank: make(map[string]int)}
}

func (r *Registry) find(key string) string {
	if _, ok := r.parent[key]; !ok {
		r.parent[key] = key
		r.rank[key] = 0
		return key
	}
	root := key
	for r.parent[root] != root {
		root = r.parent[root]
	}
	// path compression
	for r.parent[key] != root {
		next := r.parent[key]
		r.parent[key] = root
		key = next
	}
	return root
}

func (r *Registry) union(a, b string) {
	ra, rb := r.find(a), r.find(b)
	if ra == rb {
		return
	}
	if r.rank[ra] < r.rank[rb] {
		ra, rb = rb, ra
	}
	r.parent[rb] = ra
	if r.rank[ra] == r.rank[rb] {
		r.rank[ra]++
	}
}

// membersOf returns every alias key in the same equivalence class as
// root, plus a reverse lookup back to full Alias values via the
// caller-maintained aliasByKey table.
func (r *Registry) membersOf(root string) []string {
	var out []string
	for k := range r.parent {
		if r.find(k) == root {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func canonicalIDFromMembers(members []string) CanonicalID {
	h := sha256.New()
	for i, m := range members {
		if i > 0 {
			h.Write([]byte{0})
		}
		h.Write([]byte(m))
	}
	return CanonicalID(hex.EncodeToString(h.Sum(nil)))
}

func hashMerge(r MergeRecord) string {
	raw := fmt.Sprintf("idx=%d|actor=%s|a=%s|b=%s|canon=%s|tick=%d|prev=%s",
		r.Index, r.ActorID, r.AliasA.key(), r.AliasB.key(), r.CanonicalID, r.Tick, r.PrevHash)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// Merge declares that aliasA and aliasB denote the same real-world
// entity (creating either alias if it is new) and returns the
// resulting canonical ID for the merged equivalence class. A no-op
// merge (both aliases already in the same class) is still recorded —
// audit trails should show the assertion was made, not silently drop
// it.
func (r *Registry) Merge(actorID string, aliasA, aliasB Alias, tick uint64) (CanonicalID, error) {
	_, span := telemetry.StartSpan(context.Background(), "entity.Merge",
		telemetry.Attribute{Key: "alias_a", Value: aliasA.key()}, telemetry.Attribute{Key: "alias_b", Value: aliasB.key()})
	defer span.End()

	if aliasA.Kind == "" || aliasA.Value == "" || aliasB.Kind == "" || aliasB.Value == "" {
		return "", errors.New("entity: alias Kind and Value must be non-empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	r.union(aliasA.key(), aliasB.key())
	root := r.find(aliasA.key())
	members := r.membersOf(root)
	canon := canonicalIDFromMembers(members)

	rec := MergeRecord{
		Index: uint64(len(r.log)), ActorID: actorID, AliasA: aliasA, AliasB: aliasB,
		CanonicalID: canon, Tick: tick, PrevHash: r.head,
	}
	rec.Hash = hashMerge(rec)
	r.log = append(r.log, rec)
	r.head = rec.Hash
	return canon, nil
}

// Register is a convenience for introducing a brand-new single-alias
// entity (Merge(alias, alias)) — the trivial degenerate case of Merge.
func (r *Registry) Register(actorID string, alias Alias, tick uint64) (CanonicalID, error) {
	return r.Merge(actorID, alias, alias, tick)
}

// Resolve returns the current canonical ID for an alias, if known.
func (r *Registry) Resolve(alias Alias) (CanonicalID, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := alias.key()
	if _, ok := r.parent[key]; !ok {
		return "", false
	}
	root := r.find(key)
	members := r.membersOf(root)
	return canonicalIDFromMembers(members), true
}

// AliasesFor returns every alias currently resolved to the same
// canonical entity as the given alias.
func (r *Registry) AliasesFor(alias Alias) []Alias {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := alias.key()
	if _, ok := r.parent[key]; !ok {
		return nil
	}
	root := r.find(key)
	members := r.membersOf(root)
	out := make([]Alias, 0, len(members))
	for _, m := range members {
		out = append(out, aliasFromKey(m))
	}
	return out
}

func aliasFromKey(key string) Alias {
	for i := 0; i < len(key); i++ {
		if key[i] == '|' {
			return Alias{Kind: key[:i], Value: key[i+1:]}
		}
	}
	return Alias{Kind: key}
}

// VerifyChain re-derives every merge record's hash and confirms the
// chain is unbroken.
func (r *Registry) VerifyChain() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	prev := ""
	for _, rec := range r.log {
		if rec.PrevHash != prev {
			return ErrTamperDetected
		}
		if hashMerge(rec) != rec.Hash {
			return ErrTamperDetected
		}
		prev = rec.Hash
	}
	return nil
}

// ReplayAll returns a defensive copy of the full merge log.
func (r *Registry) ReplayAll() []MergeRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]MergeRecord, len(r.log))
	copy(out, r.log)
	return out
}

// Head returns the current chain head hash ("" if no merges yet).
func (r *Registry) Head() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.head
}

// Rebuild reconstructs a full Registry purely by replaying a merge
// log's Merge operations in order, verifying the hash chain as it
// goes and confirming each replayed CanonicalID matches what the
// original ledger recorded — the proof that entity resolution is a
// pure, deterministic function of the append-only log alone.
func Rebuild(log []MergeRecord) (*Registry, error) {
	r := NewRegistry()
	prev := ""
	for i, rec := range log {
		if rec.Index != uint64(i) {
			return nil, fmt.Errorf("%w: index gap at %d", ErrTamperDetected, i)
		}
		if rec.PrevHash != prev {
			return nil, fmt.Errorf("%w: broken chain linkage at index %d", ErrTamperDetected, rec.Index)
		}
		if hashMerge(rec) != rec.Hash {
			return nil, fmt.Errorf("%w: hash mismatch at index %d", ErrTamperDetected, rec.Index)
		}
		got, err := r.Merge(rec.ActorID, rec.AliasA, rec.AliasB, rec.Tick)
		if err != nil {
			return nil, err
		}
		if got != rec.CanonicalID {
			return nil, fmt.Errorf("%w: canonical ID mismatch replaying index %d: got %s want %s", ErrTamperDetected, rec.Index, got, rec.CanonicalID)
		}
		prev = rec.Hash
		// Rebuild via r.Merge already appended a new record with a
		// fresh index/prevHash matching the original since we replay
		// in order — no further bookkeeping needed.
	}
	return r, nil
}
