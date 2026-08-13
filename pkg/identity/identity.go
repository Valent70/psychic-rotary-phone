// Package identity closes PHASE 4 of the v7.10.3 production-readiness
// audit: "Entity Resolution saat ini sudah masuk lifecycle untuk alias
// -> canonical entity, tetapi jangan menganggap itu sudah menjadi
// enterprise identity system."
//
// pkg/moat/entity is a union-find over alias equality with a
// hash-chained merge log. That is correct and it stays — this package
// does not replace it and does not merge into it. What it adds is the
// three things an enterprise identity system needs that union-find
// structurally cannot express:
//
//  1. UNCERTAIN identity. Real resolution is "IMO 9998887 and callsign
//     ABCD are probably the same vessel, at 0.87 confidence, because
//     an IMO-authoritative registry and a low-authority scraped list
//     both link them". Union-find can only say "same" or "not same".
//     Candidate generation + scoring + source authority live here.
//
//  2. UNMERGE with history preserved. Union-find merges are
//     irreversible; a wrong merge poisons every future resolution and
//     every past one. Here, resolution state is a FOLD over an
//     append-only event ledger, so:
//     - Unmerge is a new event, never a deletion;
//     - ResolveAt(alias, txTick) folds only events known at that
//     tick, so a historical replay of a decision made BEFORE the
//     unmerge still returns what VERIQO actually believed then.
//     That is the audit's stated invariant: "later proven incorrect ->
//     UNMERGE -> historical replay remains valid".
//
//  3. CONFLICT as a first-class outcome. Two authoritative sources
//     asserting incompatible identity is not an error to swallow; it
//     is a finding to surface (ErrIdentityConflict) and to route to
//     human approval (PHASE 49).
package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Kind is the type of an identifier. The set is fixed by the audit.
type Kind string

const (
	KindIMO          Kind = "IMO"
	KindCallsign     Kind = "CALLSIGN"
	KindMMSI         Kind = "MMSI"
	KindName         Kind = "NAME"
	KindFlag         Kind = "FLAG"
	KindOwner        Kind = "OWNER"
	KindOperator     Kind = "OPERATOR"
	KindManager      Kind = "MANAGER"
	KindRegistryID   Kind = "REGISTRY_ID"
	KindCommodity    Kind = "COMMODITY"
	KindPort         Kind = "PORT"
	KindOrganization Kind = "ORGANIZATION"
	KindPerson       Kind = "PERSON"
	// The seven kinds below close audit item P0-03's entity-type gap:
	// Charterer, Terminal, Refinery, Trade, Shipment, Bill of Lading and
	// GeographicEntity were named in the audit's required scope but had
	// no identifier Kind here yet.
	KindCharterer        Kind = "CHARTERER"
	KindTerminal         Kind = "TERMINAL"
	KindRefinery         Kind = "REFINERY"
	KindTrade            Kind = "TRADE"
	KindShipment         Kind = "SHIPMENT"
	KindBillOfLading     Kind = "BILL_OF_LADING"
	KindGeographicEntity Kind = "GEOGRAPHIC_ENTITY"
)

// discriminatingPower is how much a shared identifier of this kind
// justifies believing two records are the same entity. An IMO number
// is issued once and never reused; a vessel NAME is reused constantly
// and is near-worthless alone. Encoding this is the difference between
// an identity system and a string join.
var discriminatingPower = map[Kind]float64{
	KindIMO:          1.00,
	KindMMSI:         0.85, // reassigned on flag change
	KindRegistryID:   0.90,
	KindCallsign:     0.70,
	KindPerson:       0.60,
	KindOrganization: 0.55,
	KindPort:         0.50,
	KindOwner:        0.40,
	KindOperator:     0.35,
	KindManager:      0.35,
	KindCommodity:    0.30,
	KindFlag:         0.10,
	KindName:         0.20,
	// A Bill of Lading number is issued once per shipment by a single
	// carrier and essentially never reused -- ranked with REGISTRY_ID.
	KindBillOfLading: 0.85,
	// Shipment and trade references are issued per-transaction by a
	// counterparty system; strong but occasionally recycled across
	// systems that don't coordinate numbering, so ranked below IMO/BoL.
	KindShipment: 0.75,
	KindTrade:    0.70,
	// Terminal and refinery are named facilities, structurally the same
	// discriminating value as PORT (a shared physical-location code).
	KindTerminal: 0.50,
	KindRefinery: 0.50,
	// Charterer is a relationship role like OWNER/OPERATOR/MANAGER: a
	// real link, but one entity can charter many vessels and one vessel
	// can have many charterers over its life, so it is ranked with them.
	KindCharterer: 0.35,
	// A geographic entity (country, region) is the broadest, most
	// reused kind of all -- ranked at FLAG's level, the existing
	// broadest-geography kind.
	KindGeographicEntity: 0.10,
}

// KnownKinds returns every modelled identifier kind, deterministically.
func KnownKinds() []Kind {
	out := make([]Kind, 0, len(discriminatingPower))
	for k := range discriminatingPower {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Identifier is one (kind, value) pair.
type Identifier struct {
	Kind  Kind   `json:"kind"`
	Value string `json:"value"`
}

// Key is the deterministic string form.
func (i Identifier) Key() string {
	return string(i.Kind) + "|" + strings.ToUpper(strings.TrimSpace(i.Value))
}

// Validate rejects malformed identifiers — the failure mode a real bug
// in v7.10.3 came from (a discarded alias-registration error letting a
// Kind-less alias through).
func (i Identifier) Validate() error {
	if strings.TrimSpace(string(i.Kind)) == "" {
		return ErrEmptyKind
	}
	if _, ok := discriminatingPower[i.Kind]; !ok {
		return fmt.Errorf("%w: %q", ErrUnknownKind, i.Kind)
	}
	if strings.TrimSpace(i.Value) == "" {
		return ErrEmptyValue
	}
	return nil
}

// Authority is how much a SOURCE is trusted to assert identity. A flag
// state registry is authoritative for its own registry IDs; a
// commercial aggregator is not authoritative for anything.
type Authority struct {
	SourceID string  `json:"source_id"`
	Weight   float64 `json:"weight"` // (0,1]
	// AuthoritativeFor lists kinds this source may assert with full
	// weight. For other kinds its weight is halved.
	AuthoritativeFor []Kind `json:"authoritative_for,omitempty"`
}

func (a Authority) weightFor(k Kind) float64 {
	for _, ak := range a.AuthoritativeFor {
		if ak == k {
			return a.Weight
		}
	}
	return a.Weight / 2
}

// Errors.
var (
	ErrEmptyKind        = errors.New("identity: identifier Kind must be non-empty")
	ErrUnknownKind      = errors.New("identity: unknown identifier Kind")
	ErrEmptyValue       = errors.New("identity: identifier Value must be non-empty")
	ErrUnknownSource    = errors.New("identity: asserting source has no registered authority")
	ErrBadWeight        = errors.New("identity: authority weight must be in (0,1]")
	ErrIdentityConflict = errors.New("identity: conflicting authoritative identity assertions")
	ErrNotResolved      = errors.New("identity: alias does not resolve to any entity")
	ErrNotMerged        = errors.New("identity: the two identifiers are not currently merged")
	ErrChainBroken      = errors.New("identity: identity ledger hash chain is broken")
	ErrLowConfidence    = errors.New("identity: candidate confidence below the required threshold")
)

// EventType is the kind of identity ledger event.
type EventType string

const (
	EventAssert  EventType = "ASSERT"  // a source asserts A and B are the same entity
	EventMerge   EventType = "MERGE"   // resolution accepted; A and B now one entity
	EventUnmerge EventType = "UNMERGE" // a prior merge is retracted as incorrect
	EventCorrect EventType = "CORRECT" // an identifier value itself was wrong
)

// Event is one append-only identity ledger entry. Resolution state at
// any transaction tick is a pure fold over the prefix of events with
// TxTick <= that tick — which is what makes historical replay honest
// after an unmerge.
type Event struct {
	Index      uint64     `json:"index"`
	Type       EventType  `json:"type"`
	ActorID    string     `json:"actor_id"`
	SourceID   string     `json:"source_id"`
	A          Identifier `json:"a"`
	B          Identifier `json:"b"`
	Confidence float64    `json:"confidence"`
	Reason     string     `json:"reason"`
	TxTick     uint64     `json:"tx_tick"`
	PrevHash   string     `json:"prev_hash"`
	Hash       string     `json:"hash"`
}

func hashEvent(e Event) string {
	h := sha256.New()
	fmt.Fprintf(h, "idx=%d|type=%s|actor=%s|src=%s|a=%s|b=%s|conf=%.6f|reason=%s|tick=%d|prev=%s",
		e.Index, e.Type, e.ActorID, e.SourceID, e.A.Key(), e.B.Key(), e.Confidence, e.Reason, e.TxTick, e.PrevHash)
	return hex.EncodeToString(h.Sum(nil))
}

// Candidate is one possible resolution target with its evidence.
type Candidate struct {
	EntityID   string   `json:"entity_id"`
	Score      float64  `json:"score"`
	Confidence float64  `json:"confidence"` // normalised across candidates, [0,1]
	Matched    []string `json:"matched"`    // human-readable justification
}

// Resolution is the outcome of resolving one identifier.
type Resolution struct {
	Query      Identifier  `json:"query"`
	EntityID   string      `json:"entity_id"`
	Confidence float64     `json:"confidence"`
	Candidates []Candidate `json:"candidates"`
	Conflict   bool        `json:"conflict"`
	AsOfTick   uint64      `json:"as_of_tick"`
}

// Resolver is the identity engine. Safe for concurrent use.
type Resolver struct {
	mu          sync.RWMutex
	ledger      []Event
	authorities map[string]Authority
}

// NewResolver constructs an empty identity resolver.
func NewResolver() *Resolver {
	return &Resolver{authorities: map[string]Authority{}}
}

// RegisterAuthority declares how much a source is trusted to assert
// identity. Unregistered sources cannot assert identity at all — the
// "no unauthenticated identity claims" rule, mechanically enforced.
func (r *Resolver) RegisterAuthority(a Authority) error {
	if strings.TrimSpace(a.SourceID) == "" {
		return ErrUnknownSource
	}
	if a.Weight <= 0 || a.Weight > 1 {
		return ErrBadWeight
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.authorities[a.SourceID] = a
	return nil
}

// Assert records that a source claims two identifiers denote the same
// entity. It does NOT merge — merging is a separate, policy-gated
// decision (see ResolveAndMerge). This separation is the audit's
// "jangan auto-blacklist tanpa policy/governance" principle applied to
// identity.
func (r *Resolver) Assert(actorID, sourceID string, a, b Identifier, tick uint64, reason string) (Event, error) {
	if err := a.Validate(); err != nil {
		return Event{}, fmt.Errorf("identity: identifier A: %w", err)
	}
	if err := b.Validate(); err != nil {
		return Event{}, fmt.Errorf("identity: identifier B: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	auth, ok := r.authorities[sourceID]
	if !ok {
		return Event{}, fmt.Errorf("%w: %s", ErrUnknownSource, sourceID)
	}
	// Confidence of ONE assertion = source authority for the weaker of
	// the two identifier kinds * that kind's discriminating power.
	power := discriminatingPower[a.Kind]
	if p := discriminatingPower[b.Kind]; p < power {
		power = p
	}
	kindForAuthority := a.Kind
	if discriminatingPower[b.Kind] < discriminatingPower[a.Kind] {
		kindForAuthority = b.Kind
	}
	conf := auth.weightFor(kindForAuthority) * power
	return r.appendLocked(Event{
		Type: EventAssert, ActorID: actorID, SourceID: sourceID, A: a, B: b,
		Confidence: conf, Reason: reason, TxTick: tick,
	}), nil
}

func (r *Resolver) appendLocked(e Event) Event {
	e.Index = uint64(len(r.ledger))
	if len(r.ledger) > 0 {
		e.PrevHash = r.ledger[len(r.ledger)-1].Hash
	}
	e.Hash = hashEvent(e)
	r.ledger = append(r.ledger, e)
	return e
}

// Candidates generates and scores every entity the query identifier
// could denote, as of a transaction tick. Scoring accumulates the
// confidence of every independent assertion path, saturating (noisy-OR)
// rather than summing past 1 — two weak assertions corroborate, they do
// not become certainty.
func (r *Resolver) Candidates(q Identifier, asOfTick uint64) ([]Candidate, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	st := r.foldLocked(asOfTick)
	return st.candidatesFor(q), nil
}

// Resolve returns the best candidate above minConfidence, flagging a
// conflict when two candidates are close enough that picking either
// would be arbitrary.
func (r *Resolver) Resolve(q Identifier, asOfTick, _ uint64) (Resolution, error) {
	return r.ResolveWithThreshold(q, asOfTick, 0.5)
}

// ResolveWithThreshold is Resolve with an explicit confidence floor.
func (r *Resolver) ResolveWithThreshold(q Identifier, asOfTick uint64, minConfidence float64) (Resolution, error) {
	cands, err := r.Candidates(q, asOfTick)
	if err != nil {
		return Resolution{}, err
	}
	res := Resolution{Query: q, Candidates: cands, AsOfTick: asOfTick}
	if len(cands) == 0 {
		return res, ErrNotResolved
	}
	best := cands[0]
	res.EntityID, res.Confidence = best.EntityID, best.Confidence
	if len(cands) > 1 && cands[1].Confidence > 0 {
		// Conflict when the runner-up is within 10% relative of the
		// winner: the model cannot justify a choice.
		if (best.Confidence-cands[1].Confidence)/best.Confidence < 0.10 {
			res.Conflict = true
			return res, fmt.Errorf("%w: %s vs %s", ErrIdentityConflict, best.EntityID, cands[1].EntityID)
		}
	}
	if best.Confidence < minConfidence {
		return res, fmt.Errorf("%w: %.4f < %.4f", ErrLowConfidence, best.Confidence, minConfidence)
	}
	return res, nil
}

// Merge accepts a resolution and binds two identifiers into one
// entity from tick onwards.
func (r *Resolver) Merge(actorID, sourceID string, a, b Identifier, tick uint64, reason string) (Event, error) {
	if err := a.Validate(); err != nil {
		return Event{}, err
	}
	if err := b.Validate(); err != nil {
		return Event{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.authorities[sourceID]; !ok {
		return Event{}, fmt.Errorf("%w: %s", ErrUnknownSource, sourceID)
	}
	return r.appendLocked(Event{
		Type: EventMerge, ActorID: actorID, SourceID: sourceID, A: a, B: b,
		Confidence: 1, Reason: reason, TxTick: tick,
	}), nil
}

// Unmerge retracts a prior merge. It is an APPEND, never a deletion:
// every decision made while the merge was believed remains replayable
// exactly as it was made.
func (r *Resolver) Unmerge(actorID, sourceID string, a, b Identifier, tick uint64, reason string) (Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.foldLocked(^uint64(0))
	if st.root(a.Key()) != st.root(b.Key()) || st.root(a.Key()) == "" {
		return Event{}, fmt.Errorf("%w: %s / %s", ErrNotMerged, a.Key(), b.Key())
	}
	return r.appendLocked(Event{
		Type: EventUnmerge, ActorID: actorID, SourceID: sourceID, A: a, B: b,
		Confidence: 1, Reason: reason, TxTick: tick,
	}), nil
}

// Correct records that an identifier VALUE was wrong and is replaced.
// Historical resolutions keep the old value; new ones follow the
// correction.
func (r *Resolver) Correct(actorID, sourceID string, wrong, right Identifier, tick uint64, reason string) (Event, error) {
	if err := wrong.Validate(); err != nil {
		return Event{}, err
	}
	if err := right.Validate(); err != nil {
		return Event{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.appendLocked(Event{
		Type: EventCorrect, ActorID: actorID, SourceID: sourceID, A: wrong, B: right,
		Confidence: 1, Reason: reason, TxTick: tick,
	}), nil
}

// History returns every ledger event touching an identifier, in order
// — the "why does this entity look like this" audit trail.
func (r *Resolver) History(id Identifier) []Event {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Event
	for _, e := range r.ledger {
		if e.A.Key() == id.Key() || e.B.Key() == id.Key() {
			out = append(out, e)
		}
	}
	return out
}

// Ledger returns a defensive copy of the whole identity ledger.
func (r *Resolver) Ledger() []Event {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Event, len(r.ledger))
	copy(out, r.ledger)
	return out
}

// Head returns the latest event hash, or "" when empty.
func (r *Resolver) Head() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.ledger) == 0 {
		return ""
	}
	return r.ledger[len(r.ledger)-1].Hash
}

// VerifyChain independently re-derives every event hash and linkage.
func (r *Resolver) VerifyChain() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	prev := ""
	for i, e := range r.ledger {
		if e.Index != uint64(i) || e.PrevHash != prev || hashEvent(e) != e.Hash {
			return fmt.Errorf("%w at index %d", ErrChainBroken, i)
		}
		prev = e.Hash
	}
	return nil
}

// Rebuild reconstructs a resolver from an event ledger plus its
// authority table — the replay proof for identity.
func Rebuild(events []Event, authorities []Authority) (*Resolver, error) {
	r := NewResolver()
	for _, a := range authorities {
		if err := r.RegisterAuthority(a); err != nil {
			return nil, err
		}
	}
	prev := ""
	for i, e := range events {
		if e.Index != uint64(i) || e.PrevHash != prev || hashEvent(e) != e.Hash {
			return nil, fmt.Errorf("%w at index %d", ErrChainBroken, i)
		}
		prev = e.Hash
		r.ledger = append(r.ledger, e)
	}
	return r, nil
}
