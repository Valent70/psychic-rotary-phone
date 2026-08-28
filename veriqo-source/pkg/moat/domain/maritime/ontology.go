// Package maritime implements Veriqo's first domain ontology — the gap
// both "Masih_kurang_dan_harus_ditambahi (Palantir)" and
// "Masalah_terbesar_nomor_1" name as the single biggest missing piece:
// reasoning today runs on generic strings ("VESSEL:IMO9876543",
// "FLAG_STATE"), not a typed domain model. Without an ontology the
// fusion/causal/decision layers cannot be described as "maritime
// intelligence" to an investor — they are domain-agnostic plumbing.
//
// This package does NOT replace fusion/causal/decision. It sits above
// them: typed entities with a canonical, deterministic identity scheme
// (SHA-256 of normalized natural keys — no UUIDs, same invariant as the
// rest of the kernel), plus adapter methods that project each entity
// into the Claim/NodeID vocabulary the existing MOAT engines already
// consume. A caller builds a maritime.Registry, registers entities, and
// gets back fusion.Claim / causal.NodeID values that are guaranteed
// consistent across the whole pipeline instead of hand-typed strings.
//
// Design invariants (same discipline as kg/fusion/causal/decision):
//   - No time.Now(); every mutation carries an explicit logical Tick.
//   - No UUIDs; every EntityID is content-derived and reproducible.
//   - Append-only registration log, hash-chained, replayable.
//   - Deterministic: two independently-built registries fed the same
//     entities in different orders converge to the same entity set and
//     the same relationship graph (verified by ReplayAll + resort).
package maritime

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"veriqo/pkg/moat/causal"
	"veriqo/pkg/moat/fusion"
)

var (
	ErrUnknownEntityKind = errors.New("maritime: unknown entity kind")
	ErrEmptyNaturalKey   = errors.New("maritime: natural key fields must be non-empty")
	ErrDuplicateEntity   = errors.New("maritime: entity already registered")
	ErrUnknownEntity     = errors.New("maritime: entity not registered")
	ErrTamperDetected    = errors.New("maritime: hash chain verification failed")
)

// EntityKind enumerates the ontology's typed node classes. This is the
// concrete answer to the audit's "Vessel Voyage Port Cargo Commodity
// Broker Sanction Insurance Ownership" list.
type EntityKind string

const (
	KindVessel        EntityKind = "VESSEL"
	KindVoyage        EntityKind = "VOYAGE"
	KindPort          EntityKind = "PORT"
	KindCargo         EntityKind = "CARGO"
	KindCommodity     EntityKind = "COMMODITY"
	KindBroker        EntityKind = "BROKER"
	KindSanctionEntry EntityKind = "SANCTION_ENTRY"
	KindInsurance     EntityKind = "INSURANCE_POLICY"
	KindOwnership     EntityKind = "OWNERSHIP_RECORD"
	KindCountry       EntityKind = "COUNTRY"
	KindInsurer       EntityKind = "INSURER"
)

// KnownEntityKinds returns every entity kind this ontology models, in
// a stable order.
//
// Added for PHASE B (P0-2) of the pre-insurance closure program, which
// requires maritime identity mappings to be EXPLICIT and any unknown
// mapping to be marked UNMAPPED rather than silently guessed. That
// check (pkg/governance/entityconsistency.MaritimeMapping) is only
// meaningful if it can enumerate what it must cover -- otherwise a kind
// added here later would be silently unmapped, which is exactly the
// failure mode the requirement names. It is a pure enumeration of the
// constants above; it introduces no new vocabulary.
func KnownEntityKinds() []EntityKind {
	return []EntityKind{
		KindVessel, KindVoyage, KindPort, KindCargo, KindCommodity,
		KindBroker, KindSanctionEntry, KindInsurance, KindOwnership,
		KindCountry, KindInsurer,
	}
}

// RelationKind enumerates the ontology's typed edges — the multi-hop
// vocabulary the audit asks for explicitly: Ship->Company->Broker->
// Insurance->Country->Sanction.
type RelationKind string

const (
	RelOwnedBy        RelationKind = "OWNED_BY"        // Vessel -> Ownership
	RelFlaggedIn      RelationKind = "FLAGGED_IN"      // Vessel -> Country
	RelBrokeredBy     RelationKind = "BROKERED_BY"     // Voyage -> Broker
	RelInsuredBy      RelationKind = "INSURED_BY"      // Vessel -> Insurance
	RelUnderwrittenBy RelationKind = "UNDERWRITTEN_BY" // Insurance -> Insurer
	RelCalledAt       RelationKind = "CALLED_AT"       // Voyage -> Port
	RelCarries        RelationKind = "CARRIES"         // Voyage -> Cargo
	RelOfCommodity    RelationKind = "OF_COMMODITY"    // Cargo -> Commodity
	RelSanctioned     RelationKind = "SANCTIONED"      // Entity -> SanctionEntry
	RelDomiciledIn    RelationKind = "DOMICILED_IN"    // Broker/Insurer -> Country
)

// Entity is one typed ontology node. NaturalKey fields are the
// human-meaningful identity (e.g. IMO number for a Vessel); EntityID is
// derived from them deterministically.
type Entity struct {
	ID         string // SHA-256(kind|sorted natural key fields)
	Kind       EntityKind
	NaturalKey map[string]string // e.g. {"imo": "9876543"} for a Vessel
	Attributes map[string]string // descriptive, non-identity fields
	Tick       uint64
}

// Relation is one typed, directed edge between two registered entities.
type Relation struct {
	ID     string
	FromID string
	Kind   RelationKind
	ToID   string
	Tick   uint64
}

// registrationRecord is the hash-chained audit log entry.
type registrationRecord struct {
	Index    uint64
	Kind     string // "ENTITY" or "RELATION"
	Payload  string // canonical description
	Tick     uint64
	PrevHash string
	Hash     string
}

// Registry is the live maritime domain ontology store. Safe for
// concurrent use.
type Registry struct {
	mu        sync.RWMutex
	entities  map[string]Entity
	relations map[string]Relation
	byKind    map[EntityKind][]string // entity IDs, insertion order per kind
	log       []registrationRecord
	head      string
}

// NewRegistry constructs an empty domain ontology registry.
func NewRegistry() *Registry {
	return &Registry{
		entities:  make(map[string]Entity),
		relations: make(map[string]Relation),
		byKind:    make(map[EntityKind][]string),
	}
}

// DeriveEntityID computes the deterministic, content-addressed ID for an
// entity from its kind and natural key. Exported so callers (and the
// domain CLI/demo) can compute an ID before registration, e.g. to look
// up an entity that may already exist.
func DeriveEntityID(kind EntityKind, naturalKey map[string]string) (string, error) {
	if len(naturalKey) == 0 {
		return "", ErrEmptyNaturalKey
	}
	keys := make([]string, 0, len(naturalKey))
	for k, v := range naturalKey {
		if strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			return "", ErrEmptyNaturalKey
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(string(kind))
	for _, k := range keys {
		fmt.Fprintf(&b, "|%s=%s", k, naturalKey[k])
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:]), nil
}

func (r *Registry) appendLog(kind, payload string, tick uint64) registrationRecord {
	rec := registrationRecord{
		Index:    uint64(len(r.log)),
		Kind:     kind,
		Payload:  payload,
		Tick:     tick,
		PrevHash: r.head,
	}
	raw := fmt.Sprintf("index=%d|kind=%s|payload=%s|tick=%d|prev=%s",
		rec.Index, rec.Kind, rec.Payload, rec.Tick, rec.PrevHash)
	sum := sha256.Sum256([]byte(raw))
	rec.Hash = hex.EncodeToString(sum[:])
	r.log = append(r.log, rec)
	r.head = rec.Hash
	return rec
}

// RegisterEntity adds a new typed entity. Re-registering the same
// (kind, natural key) is an error — use GetEntity to check first, since
// this ontology treats entities as append-only facts about identity
// (attributes may still evolve via fusion/decision layers upstream).
func (r *Registry) RegisterEntity(kind EntityKind, naturalKey, attributes map[string]string, tick uint64) (Entity, error) {
	id, err := DeriveEntityID(kind, naturalKey)
	if err != nil {
		return Entity{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.entities[id]; exists {
		return Entity{}, ErrDuplicateEntity
	}
	e := Entity{ID: id, Kind: kind, NaturalKey: copyMap(naturalKey), Attributes: copyMap(attributes), Tick: tick}
	r.entities[id] = e
	r.byKind[kind] = append(r.byKind[kind], id)
	r.appendLog("ENTITY", fmt.Sprintf("id=%s|kind=%s", id, kind), tick)
	return e, nil
}

// RegisterRelation adds a new typed, directed edge between two already
// registered entities.
func (r *Registry) RegisterRelation(fromID string, kind RelationKind, toID string, tick uint64) (Relation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entities[fromID]; !ok {
		return Relation{}, ErrUnknownEntity
	}
	if _, ok := r.entities[toID]; !ok {
		return Relation{}, ErrUnknownEntity
	}
	raw := fmt.Sprintf("from=%s|kind=%s|to=%s", fromID, kind, toID)
	sum := sha256.Sum256([]byte(raw))
	relID := hex.EncodeToString(sum[:])
	rel := Relation{ID: relID, FromID: fromID, Kind: kind, ToID: toID, Tick: tick}
	r.relations[relID] = rel
	r.appendLog("RELATION", fmt.Sprintf("id=%s|from=%s|kind=%s|to=%s", relID, fromID, kind, toID), tick)
	return rel, nil
}

// GetEntity returns a registered entity by ID.
func (r *Registry) GetEntity(id string) (Entity, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entities[id]
	return e, ok
}

// EntitiesOfKind returns all registered entity IDs of a given kind, in
// registration order.
func (r *Registry) EntitiesOfKind(kind EntityKind) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.byKind[kind]))
	copy(out, r.byKind[kind])
	return out
}

// RelationsFrom returns all relations whose FromID matches, sorted by
// relation ID for determinism.
func (r *Registry) RelationsFrom(fromID string) []Relation {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Relation
	for _, rel := range r.relations {
		if rel.FromID == fromID {
			out = append(out, rel)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// MultiHop walks the ontology graph breadth-first from an entity up to
// maxHops, returning every reachable entity ID with the relation path
// that reached it — the concrete implementation of the audit's demand
// for "Ship -> Company -> Broker -> Insurance -> Country -> Sanction"
// multi-hop traversal that causal.Why() alone cannot do because causal
// nodes are untyped strings.
func (r *Registry) MultiHop(startID string, maxHops int) map[string][]RelationKind {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make(map[string][]RelationKind)
	type frontierItem struct {
		id   string
		path []RelationKind
	}
	visited := map[string]bool{startID: true}
	frontier := []frontierItem{{id: startID, path: nil}}
	for hop := 0; hop < maxHops && len(frontier) > 0; hop++ {
		var next []frontierItem
		for _, item := range frontier {
			for _, rel := range r.relations {
				if rel.FromID != item.id || visited[rel.ToID] {
					continue
				}
				visited[rel.ToID] = true
				path := append(append([]RelationKind{}, item.path...), rel.Kind)
				result[rel.ToID] = path
				next = append(next, frontierItem{id: rel.ToID, path: path})
			}
		}
		frontier = next
	}
	return result
}

// ToClaim projects an entity attribute slot into the fusion package's
// Claim vocabulary, so evidence about this ontology entity flows through
// the existing Truth Arbitration Engine unchanged.
func (e Entity) ToClaim(predicate string) fusion.Claim {
	return fusion.Claim{Subject: string(e.Kind) + ":" + e.ID, Predicate: predicate}
}

// ToCausalNode projects an entity into the causal package's NodeID
// vocabulary, so domain entities can be causes/effects in causal.Graph
// without hand-typed strings drifting from the ontology's own IDs.
func (e Entity) ToCausalNode() causal.NodeID {
	return causal.NodeID(string(e.Kind) + ":" + e.ID)
}

// VerifyChain re-derives every hash in the registration log and confirms
// it matches, detecting any tampering of the append-only log.
func (r *Registry) VerifyChain() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	prev := ""
	for _, rec := range r.log {
		raw := fmt.Sprintf("index=%d|kind=%s|payload=%s|tick=%d|prev=%s",
			rec.Index, rec.Kind, rec.Payload, rec.Tick, prev)
		sum := sha256.Sum256([]byte(raw))
		want := hex.EncodeToString(sum[:])
		if want != rec.Hash {
			return ErrTamperDetected
		}
		prev = rec.Hash
	}
	return nil
}

// Head returns the current hash chain head.
func (r *Registry) Head() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.head
}

// EntityCount returns the number of registered entities.
func (r *Registry) EntityCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.entities)
}

func copyMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
