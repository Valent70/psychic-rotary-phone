// Package digitaltwin implements Veriqo's Digital Twin layer —
// Yang_masih_kurang.docx calls this "one of VERIQO's biggest MOATs":
// a single object (e.g. one vessel) that maintains Current State,
// aggregated Risk, and a Timeline, continuously updated as fusion
// arbitrations and decision outputs arrive — the "living model" of an
// entity, as opposed to a KG that is just fact storage.
//
// A Twin is intentionally a pure aggregation view, not a new source of
// truth: it never invents data, it only reflects the latest
// fusion.ArbitrationResult per claim predicate and the latest
// decision.Decision per policy for one entity. This keeps the append-
// only, replay-deterministic discipline of the layers beneath it —
// Rebuild proves twin state is 100% reconstructable from the update log
// alone, satisfying the audit's explicit request:
// "DigitalTwinMaintainsConsistentStateAcrossReplay".
package digitaltwin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"

	"veriqo/pkg/moat/decision"
	"veriqo/pkg/moat/fusion"
	"veriqo/pkg/platform/telemetry"
)

var ErrTamperDetected = errors.New("digitaltwin: hash chain verification failed")

// EntityID identifies the real-world object a twin tracks (e.g.
// "VESSEL:IMO1234567").
type EntityID string

// AttributeState is a twin's current belief about one predicate,
// mirroring the winning side of a fusion.ArbitrationResult.
type AttributeState struct {
	Predicate     string
	Value         string
	Confidence    float64
	Contradiction bool
	UpdatedAtTick uint64
}

// RiskState is a twin's latest decision outcome for one policy.
type RiskState struct {
	PolicyName    string
	RiskScore     float64
	Action        decision.Action
	UpdatedAtTick uint64
}

// Twin is the aggregated, continuously-updated state for one entity.
type Twin struct {
	Entity     EntityID
	Attributes map[string]AttributeState // keyed by Predicate
	Risk       map[string]RiskState      // keyed by PolicyName
	Causal     map[string]CausalState    // keyed by PolicyName — v7.10.2 gap closure
}

func newTwin(entity EntityID) *Twin {
	return &Twin{
		Entity: entity, Attributes: make(map[string]AttributeState),
		Risk: make(map[string]RiskState), Causal: make(map[string]CausalState),
	}
}

// UpdateKind distinguishes the kinds of thing a twin update log can
// record.
type UpdateKind string

const (
	UpdateAttribute UpdateKind = "ATTRIBUTE"
	UpdateRisk      UpdateKind = "RISK"
	UpdateCausal    UpdateKind = "CAUSAL"
)

// UpdateRecord is one hash-chained twin update, capturing enough to
// fully reconstruct Twin state without re-deriving from fusion/decision.
type UpdateRecord struct {
	Index         uint64
	ActorID       string
	Entity        EntityID
	Kind          UpdateKind
	Key           string // predicate name (ATTRIBUTE) or policy name (RISK)
	Value         string // asserted value (ATTRIBUTE only)
	Confidence    float64
	Contradiction bool
	RiskScore     float64
	Action        string
	CausalSupport float64 // CAUSAL only — pkg/moat/causal.Graph.AggregateSupport()
	Tick          uint64
	PrevHash      string
	Hash          string
}

// Registry holds every tracked twin. Safe for concurrent use.
type Registry struct {
	mu    sync.RWMutex
	twins map[EntityID]*Twin
	log   []UpdateRecord
	head  string
}

func NewRegistry() *Registry {
	return &Registry{twins: make(map[EntityID]*Twin)}
}

// ApplyArbitration folds one fusion.ArbitrationResult into the named
// entity's twin, updating (or creating) the Attribute for that claim's
// predicate.
func (r *Registry) ApplyArbitration(actorID string, entity EntityID, res fusion.ArbitrationResult, tick uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	rec := UpdateRecord{
		Index: uint64(len(r.log)), ActorID: actorID, Entity: entity, Kind: UpdateAttribute,
		Key: res.Claim.Predicate, Value: res.Winner, Confidence: res.WinnerConfidence,
		Contradiction: res.Contradiction, Tick: tick, PrevHash: r.head,
	}
	rec.Hash = hashUpdate(rec)
	r.applyRecord(rec)
	return nil
}

// ApplyDecision folds one decision.Decision into the named entity's
// twin, updating (or creating) the Risk state for that policy.
func (r *Registry) ApplyDecision(actorID string, entity EntityID, d decision.Decision, tick uint64) error {
	_, span := telemetry.StartSpan(context.Background(), "digitaltwin.ApplyDecision",
		telemetry.Attribute{Key: "entity", Value: string(entity)})
	defer span.End()

	r.mu.Lock()
	defer r.mu.Unlock()

	rec := UpdateRecord{
		Index: uint64(len(r.log)), ActorID: actorID, Entity: entity, Kind: UpdateRisk,
		Key: d.PolicyName, RiskScore: d.RiskScore, Action: string(d.Action), Tick: tick, PrevHash: r.head,
	}
	rec.Hash = hashUpdate(rec)
	r.applyRecord(rec)
	return nil
}

// applyRecord mutates in-memory twin state from a record and appends it
// to the log. Caller must hold r.mu.
func (r *Registry) applyRecord(rec UpdateRecord) {
	twin, ok := r.twins[rec.Entity]
	if !ok {
		twin = newTwin(rec.Entity)
		r.twins[rec.Entity] = twin
	}
	switch rec.Kind {
	case UpdateAttribute:
		twin.Attributes[rec.Key] = AttributeState{
			Predicate: rec.Key, Value: rec.Value, Confidence: rec.Confidence,
			Contradiction: rec.Contradiction, UpdatedAtTick: rec.Tick,
		}
	case UpdateRisk:
		twin.Risk[rec.Key] = RiskState{
			PolicyName: rec.Key, RiskScore: rec.RiskScore, Action: decision.Action(rec.Action), UpdatedAtTick: rec.Tick,
		}
	case UpdateCausal:
		twin.Causal[rec.Key] = CausalState{
			PolicyName: rec.Key, Support: rec.CausalSupport, UpdatedAtTick: rec.Tick,
		}
	}
	r.log = append(r.log, rec)
	r.head = rec.Hash
}

// Get returns a defensive deep copy of one entity's current twin state.
func (r *Registry) Get(entity EntityID) (Twin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.twins[entity]
	if !ok {
		return Twin{}, false
	}
	return copyTwin(t), true
}

func copyTwin(t *Twin) Twin {
	out := Twin{
		Entity: t.Entity, Attributes: make(map[string]AttributeState, len(t.Attributes)),
		Risk: make(map[string]RiskState, len(t.Risk)), Causal: make(map[string]CausalState, len(t.Causal)),
	}
	for k, v := range t.Attributes {
		out.Attributes[k] = v
	}
	for k, v := range t.Risk {
		out.Risk[k] = v
	}
	for k, v := range t.Causal {
		out.Causal[k] = v
	}
	return out
}

// Timeline returns every update recorded for one entity, in
// chronological (log) order — the twin's audit trail.
func (r *Registry) Timeline(entity EntityID) []UpdateRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []UpdateRecord
	for _, rec := range r.log {
		if rec.Entity == entity {
			out = append(out, rec)
		}
	}
	return out
}

func hashUpdate(r UpdateRecord) string {
	raw := fmt.Sprintf("idx=%d|actor=%s|entity=%s|kind=%s|key=%s|value=%s|conf=%.6f|contra=%v|risk=%.6f|action=%s|causal=%.6f|tick=%d|prev=%s",
		r.Index, r.ActorID, r.Entity, r.Kind, r.Key, r.Value, r.Confidence, r.Contradiction, r.RiskScore, r.Action, r.CausalSupport, r.Tick, r.PrevHash)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// VerifyChain re-derives every record's hash and confirms the chain is
// unbroken.
func (r *Registry) VerifyChain() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	prev := ""
	for _, rec := range r.log {
		if rec.PrevHash != prev {
			return ErrTamperDetected
		}
		if hashUpdate(rec) != rec.Hash {
			return ErrTamperDetected
		}
		prev = rec.Hash
	}
	return nil
}

// ReplayAll returns a defensive copy of the full update log.
func (r *Registry) ReplayAll() []UpdateRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]UpdateRecord, len(r.log))
	copy(out, r.log)
	return out
}

// Head returns the current chain head hash ("" if no updates yet).
func (r *Registry) Head() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.head
}

// Rebuild reconstructs a full Registry (every twin, exactly as it stands
// today) purely from an update log, verifying the hash chain as it goes.
// This is the proof that twin state is a pure, deterministic function of
// the append-only log — not incidental in-memory bookkeeping.
func Rebuild(log []UpdateRecord) (*Registry, error) {
	r := NewRegistry()
	prev := ""
	for i, rec := range log {
		if rec.Index != uint64(i) {
			return nil, fmt.Errorf("%w: index gap at %d", ErrTamperDetected, i)
		}
		if rec.PrevHash != prev {
			return nil, fmt.Errorf("%w: broken chain linkage at index %d", ErrTamperDetected, rec.Index)
		}
		if hashUpdate(rec) != rec.Hash {
			return nil, fmt.Errorf("%w: hash mismatch at index %d", ErrTamperDetected, rec.Index)
		}
		r.mu.Lock()
		r.applyRecord(rec)
		r.mu.Unlock()
		prev = rec.Hash
	}
	return r, nil
}

// Entities returns every entity ID currently tracked, sorted.
func (r *Registry) Entities() []EntityID {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]EntityID, 0, len(r.twins))
	for id := range r.twins {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
