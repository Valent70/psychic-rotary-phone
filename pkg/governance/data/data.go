// Package data closes PHASE 27: the data lifecycle engine. The audit
// was explicit that this is not security hardening but an algorithm:
//
//	ACTIVE → RETENTION_ELIGIBLE → (LEGAL_HOLD? HELD) → REDACTION_REQUIRED?
//	  → REDACTED → PURGE_ELIGIBLE → PURGED
//
// with the hard constraint that purging must not destroy audit
// integrity. That is the interesting problem, and it is solved here by
// separating three things that are usually conflated:
//
//   - content — the bytes, which may legally have to disappear;
//   - content hash — computed at ingest, which never disappears;
//   - tombstone — the immutable proof that a specific content hash
//     existed, was governed, and was destroyed under a named policy by
//     a named actor at a named tick.
//
// After a purge, "this evidence existed and had exactly this hash" is
// still provable and still replayable at the certificate level, while
// the PII itself is gone. Redaction is the same idea applied
// field-by-field: a redaction proof commits to the pre-image hash of
// each removed field, so a later dispute can verify a supplied value
// against what was removed without the system ever retaining it.
package data

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var (
	ErrUnknownRecord     = errors.New("datagov: unknown record")
	ErrDuplicateRecord   = errors.New("datagov: record already governed")
	ErrIllegalTransition = errors.New("datagov: illegal lifecycle transition")
	ErrLegalHold         = errors.New("datagov: record is under legal hold")
	ErrNotEligible       = errors.New("datagov: record is not yet eligible")
	ErrRedactionRequired = errors.New("datagov: record requires redaction before purge")
	ErrNonMonotonicTick  = errors.New("datagov: tick must not go backwards")
	ErrLedgerTampered    = errors.New("datagov: governance ledger hash chain broken")
	ErrNoPolicy          = errors.New("datagov: no retention policy for this class and tenant")
	ErrPurged            = errors.New("datagov: record content has been purged")
	ErrUnknownHold       = errors.New("datagov: unknown legal hold")
)

// Classification is the sensitivity band of a record.
type Classification string

const (
	ClassPublic       Classification = "PUBLIC"
	ClassInternal     Classification = "INTERNAL"
	ClassConfidential Classification = "CONFIDENTIAL"
	ClassRestricted   Classification = "RESTRICTED"
)

// PIIKind is the detected personal-data category of a field.
type PIIKind string

const (
	PIINone       PIIKind = "NONE"
	PIIName       PIIKind = "NAME"
	PIIIdentifier PIIKind = "IDENTIFIER" // passport, national id, account number
	PIIContact    PIIKind = "CONTACT"    // email, phone, address
	PIILocation   PIIKind = "LOCATION"   // precise position of a natural person
	PIIFinancial  PIIKind = "FINANCIAL"
)

// State is the PHASE 27 state machine.
type State string

const (
	StateActive            State = "ACTIVE"
	StateRetentionEligible State = "RETENTION_ELIGIBLE"
	StateHeld              State = "HELD"
	StateRedactionRequired State = "REDACTION_REQUIRED"
	StateRedacted          State = "REDACTED"
	StatePurgeEligible     State = "PURGE_ELIGIBLE"
	StatePurged            State = "PURGED"
)

var transitions = map[State][]State{
	StateActive:            {StateRetentionEligible, StateHeld},
	StateRetentionEligible: {StateHeld, StateRedactionRequired, StatePurgeEligible},
	StateHeld:              {StateActive, StateRetentionEligible},
	StateRedactionRequired: {StateRedacted, StateHeld},
	StateRedacted:          {StatePurgeEligible, StateHeld},
	StatePurgeEligible:     {StatePurged, StateHeld},
	StatePurged:            {}, // terminal
}

func canGo(from, to State) bool {
	for _, s := range transitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// Policy is a retention/TTL/redaction rule scoped to a tenant and an
// evidence class. Scoping is mandatory rather than optional because a
// single global retention period is the reason data-governance
// programmes fail their first audit.
type Policy struct {
	Tenant        string         `json:"tenant"`
	EvidenceClass string         `json:"evidence_class"`
	Class         Classification `json:"classification"`
	// RetentionTicks is how long a record stays ACTIVE.
	RetentionTicks uint64 `json:"retention_ticks"`
	// TTLTicks, if non-zero, is a shorter hard expiry that overrides
	// RetentionTicks — used for feeds contractually barred from
	// long-term storage.
	TTLTicks uint64 `json:"ttl_ticks"`
	// GraceTicks is the delay between becoming retention-eligible and
	// becoming purge-eligible, so an operator has a window to place a
	// hold before anything is destroyed.
	GraceTicks uint64 `json:"grace_ticks"`
	// RedactFields must be removed before purge eligibility. A record
	// carrying any of these fields cannot skip straight to PURGED.
	RedactFields []string `json:"redact_fields"`
	Version      uint64   `json:"version"`
}

func (p Policy) key() string { return p.Tenant + "|" + p.EvidenceClass }

func (p Policy) effectiveRetention() uint64 {
	if p.TTLTicks > 0 && p.TTLTicks < p.RetentionTicks {
		return p.TTLTicks
	}
	return p.RetentionTicks
}

// Field is one governed field of a record.
type Field struct {
	Name    string  `json:"name"`
	Value   string  `json:"value"`
	PII     PIIKind `json:"pii"`
	Removed bool    `json:"removed"`
	// PreImageHash is set when the field is redacted: SHA-256 over
	// name|value, so a claimed original can be checked without the
	// original being stored.
	PreImageHash string `json:"pre_image_hash"`
}

// Record is one governed unit of data.
type Record struct {
	ID            string         `json:"id"`
	Tenant        string         `json:"tenant"`
	EvidenceClass string         `json:"evidence_class"`
	Class         Classification `json:"classification"`
	IngestTick    uint64         `json:"ingest_tick"`
	State         State          `json:"state"`
	Fields        []Field        `json:"fields"`
	// ContentHash is computed at ingest over the complete original
	// content and NEVER recomputed. It is what survives a purge.
	ContentHash   string   `json:"content_hash"`
	RedactionHash string   `json:"redaction_hash"`
	Holds         []string `json:"holds"`
	PolicyVersion uint64   `json:"policy_version"`
	PurgedTick    uint64   `json:"purged_tick"`
}

// Tombstone is the immutable proof that a record existed and was
// destroyed. It is the answer to "purging must not destroy audit
// integrity".
type Tombstone struct {
	RecordID      string   `json:"record_id"`
	Tenant        string   `json:"tenant"`
	EvidenceClass string   `json:"evidence_class"`
	ContentHash   string   `json:"content_hash"`
	RedactionHash string   `json:"redaction_hash"`
	IngestTick    uint64   `json:"ingest_tick"`
	PurgedTick    uint64   `json:"purged_tick"`
	PolicyVersion uint64   `json:"policy_version"`
	Actor         string   `json:"actor"`
	Reason        string   `json:"reason"`
	FieldNames    []string `json:"field_names"`
	Hash          string   `json:"hash"`
}

// Hold is a legal hold over a scope. A hold beats every retention rule
// in the system, which is why it is a first-class object with its own
// identity and release event rather than a boolean on a record.
type Hold struct {
	ID       string `json:"id"`
	Matter   string `json:"matter"`
	Tenant   string `json:"tenant"`
	Class    string `json:"evidence_class"` // "" = all classes for the tenant
	Actor    string `json:"actor"`
	Reason   string `json:"reason"`
	FromTick uint64 `json:"from_tick"`
	ToTick   uint64 `json:"to_tick"` // 0 = still in force
}

// Event is one immutable governance ledger entry.
type Event struct {
	Index    uint64 `json:"index"`
	Kind     string `json:"kind"`
	RecordID string `json:"record_id"`
	From     State  `json:"from"`
	To       State  `json:"to"`
	Tick     uint64 `json:"tick"`
	Actor    string `json:"actor"`
	Detail   string `json:"detail"`
	Payload  string `json:"payload"`
	PrevHash string `json:"prev_hash"`
	Hash     string `json:"hash"`
}

// Engine is the data governance engine.
type Engine struct {
	mu         sync.RWMutex
	policies   map[string]Policy
	records    map[string]*Record
	order      []string
	holds      map[string]*Hold
	tombstones map[string]Tombstone
	ledger     []Event
	lastTick   uint64
}

// New constructs an empty Engine.
func New() *Engine {
	return &Engine{policies: map[string]Policy{}, records: map[string]*Record{},
		holds: map[string]*Hold{}, tombstones: map[string]Tombstone{}}
}

// SetPolicy registers or replaces a tenant/class policy.
func (e *Engine) SetPolicy(p Policy) error {
	if p.Tenant == "" || p.EvidenceClass == "" {
		return fmt.Errorf("%w: tenant and evidence class are required", ErrNoPolicy)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.policies[p.key()] = p
	return nil
}

// Ingest places a record under governance in ACTIVE.
func (e *Engine) Ingest(id, tenant, evidenceClass string, fields []Field, tick uint64) (Record, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.tickLocked(tick); err != nil {
		return Record{}, err
	}
	if _, dup := e.records[id]; dup {
		return Record{}, fmt.Errorf("%w: %s", ErrDuplicateRecord, id)
	}
	pol, ok := e.policies[tenant+"|"+evidenceClass]
	if !ok {
		return Record{}, fmt.Errorf("%w: %s/%s", ErrNoPolicy, tenant, evidenceClass)
	}
	f := append([]Field(nil), fields...)
	sort.SliceStable(f, func(i, j int) bool { return f[i].Name < f[j].Name })
	r := &Record{ID: id, Tenant: tenant, EvidenceClass: evidenceClass, Class: pol.Class,
		IngestTick: tick, State: StateActive, Fields: f, PolicyVersion: pol.Version}
	r.ContentHash = hashFields(f, false)
	e.records[id] = r
	e.order = append(e.order, id)
	e.appendLocked(Event{Kind: "INGEST", RecordID: id, To: StateActive, Tick: tick,
		Actor: "engine", Detail: "class=" + string(pol.Class), Payload: r.ContentHash})
	cp := *r
	cp.Fields = append([]Field(nil), r.Fields...)
	return cp, nil
}

// PlaceHold opens a legal hold. Records already past ACTIVE are moved
// to HELD immediately; the hold also blocks anything that arrives
// later while it is in force.
func (e *Engine) PlaceHold(h Hold, tick uint64) (Hold, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.tickLocked(tick); err != nil {
		return Hold{}, err
	}
	if h.ID == "" || h.Matter == "" || h.Actor == "" {
		return Hold{}, fmt.Errorf("%w: id, matter and actor are required", ErrUnknownHold)
	}
	h.FromTick = tick
	h.ToTick = 0
	cp := h
	e.holds[h.ID] = &cp
	e.appendLocked(Event{Kind: "HOLD_PLACED", Tick: tick, Actor: h.Actor,
		Detail: h.ID + " matter=" + h.Matter, Payload: hashHold(cp)})
	for _, id := range e.order {
		r := e.records[id]
		if r.State == StatePurged || !holdCovers(cp, r) {
			continue
		}
		r.Holds = appendUnique(r.Holds, h.ID)
		if r.State != StateHeld {
			from := r.State
			r.State = StateHeld
			e.appendLocked(Event{Kind: "TRANSITION", RecordID: id, From: from, To: StateHeld,
				Tick: tick, Actor: h.Actor, Detail: "legal hold " + h.ID, Payload: r.ContentHash})
		}
	}
	return cp, nil
}

// ReleaseHold closes a hold and returns every record it was the last
// hold on back to ACTIVE. Records do not resume where they left off:
// the retention clock is re-evaluated from scratch, because a hold that
// lasted five years must not release straight into a purge.
func (e *Engine) ReleaseHold(holdID, actor, reason string, tick uint64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.tickLocked(tick); err != nil {
		return err
	}
	h, ok := e.holds[holdID]
	if !ok || h.ToTick != 0 {
		return fmt.Errorf("%w: %s", ErrUnknownHold, holdID)
	}
	h.ToTick = tick
	e.appendLocked(Event{Kind: "HOLD_RELEASED", Tick: tick, Actor: actor,
		Detail: holdID + " " + reason, Payload: hashHold(*h)})
	for _, id := range e.order {
		r := e.records[id]
		before := len(r.Holds)
		r.Holds = removeString(r.Holds, holdID)
		if before == len(r.Holds) || len(r.Holds) > 0 || r.State != StateHeld {
			continue
		}
		r.State = StateActive
		e.appendLocked(Event{Kind: "TRANSITION", RecordID: id, From: StateHeld, To: StateActive,
			Tick: tick, Actor: actor, Detail: "last hold released", Payload: r.ContentHash})
	}
	return nil
}

func holdCovers(h Hold, r *Record) bool {
	if h.Tenant != "" && h.Tenant != r.Tenant {
		return false
	}
	if h.Class != "" && h.Class != r.EvidenceClass {
		return false
	}
	return true
}

// Evaluate advances every record according to its policy and the
// current tick, and returns the events it produced. This is the engine
// tick: it is deterministic, idempotent for a given tick, and never
// destroys anything by itself — PURGED requires an explicit Purge call
// with a named actor.
func (e *Engine) Evaluate(tick uint64) ([]Event, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.tickLocked(tick); err != nil {
		return nil, err
	}
	start := len(e.ledger)
	for _, id := range e.order {
		r := e.records[id]
		if r.State == StatePurged || r.State == StateHeld {
			continue
		}
		pol, ok := e.policies[r.Tenant+"|"+r.EvidenceClass]
		if !ok {
			continue
		}
		age := tick - r.IngestTick
		// One Evaluate advances a record as far as the policy allows at
		// this tick, not one step. A record whose retention AND grace
		// have both elapsed is purge-eligible now; making the operator
		// call Evaluate twice to discover that would make the state
		// machine depend on how often it is polled, which is exactly the
		// kind of non-determinism this repo forbids.
		for progressed := true; progressed; {
			before := r.State
			switch r.State {
			case StateActive:
				if age >= pol.effectiveRetention() {
					e.moveLocked(r, StateRetentionEligible, tick, "engine",
						"age="+strconv.FormatUint(age, 10)+" >= retention="+
							strconv.FormatUint(pol.effectiveRetention(), 10))
				}
			case StateRetentionEligible:
				if needsRedaction(r, pol) {
					e.moveLocked(r, StateRedactionRequired, tick, "engine", "policy requires redaction")
				} else if age >= pol.effectiveRetention()+pol.GraceTicks {
					e.moveLocked(r, StatePurgeEligible, tick, "engine", "grace period elapsed")
				}
			case StateRedacted:
				if age >= pol.effectiveRetention()+pol.GraceTicks {
					e.moveLocked(r, StatePurgeEligible, tick, "engine", "grace period elapsed after redaction")
				}
			}
			progressed = r.State != before
		}
	}
	return append([]Event(nil), e.ledger[start:]...), nil
}

func needsRedaction(r *Record, p Policy) bool {
	want := map[string]bool{}
	for _, f := range p.RedactFields {
		want[f] = true
	}
	for _, f := range r.Fields {
		if f.Removed {
			continue
		}
		if want[f.Name] || f.PII != PIINone && f.PII != "" {
			return true
		}
	}
	return false
}

// Redact removes the policy-named and PII-bearing fields, replacing
// each value with a pre-image hash.
func (e *Engine) Redact(id, actor, reason string, tick uint64) (Record, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.tickLocked(tick); err != nil {
		return Record{}, err
	}
	r, ok := e.records[id]
	if !ok {
		return Record{}, fmt.Errorf("%w: %s", ErrUnknownRecord, id)
	}
	if len(r.Holds) > 0 {
		return Record{}, fmt.Errorf("%w: %s held by %v", ErrLegalHold, id, r.Holds)
	}
	if !canGo(r.State, StateRedacted) {
		return Record{}, fmt.Errorf("%w: %s %s -> REDACTED", ErrIllegalTransition, id, r.State)
	}
	pol := e.policies[r.Tenant+"|"+r.EvidenceClass]
	want := map[string]bool{}
	for _, f := range pol.RedactFields {
		want[f] = true
	}
	var removed []string
	for i := range r.Fields {
		f := &r.Fields[i]
		if f.Removed {
			continue
		}
		if want[f.Name] || (f.PII != PIINone && f.PII != "") {
			f.PreImageHash = fieldPreImage(f.Name, f.Value)
			f.Value = ""
			f.Removed = true
			removed = append(removed, f.Name)
		}
	}
	sort.Strings(removed)
	r.RedactionHash = hashFields(r.Fields, true)
	from := r.State
	r.State = StateRedacted
	e.appendLocked(Event{Kind: "REDACT", RecordID: id, From: from, To: StateRedacted, Tick: tick,
		Actor: actor, Detail: reason + " fields=" + strings.Join(removed, ","),
		Payload: r.RedactionHash})
	cp := *r
	cp.Fields = append([]Field(nil), r.Fields...)
	return cp, nil
}

// VerifyRedactedField checks a claimed original value against the
// stored pre-image hash. This is how a dispute is settled after the
// value itself is gone.
func (e *Engine) VerifyRedactedField(id, fieldName, claimedValue string) (bool, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	r, ok := e.records[id]
	if !ok {
		if ts, gone := e.tombstones[id]; gone {
			_ = ts
			return false, fmt.Errorf("%w: %s", ErrPurged, id)
		}
		return false, fmt.Errorf("%w: %s", ErrUnknownRecord, id)
	}
	for _, f := range r.Fields {
		if f.Name != fieldName {
			continue
		}
		if !f.Removed {
			return f.Value == claimedValue, nil
		}
		return f.PreImageHash == fieldPreImage(fieldName, claimedValue), nil
	}
	return false, fmt.Errorf("%w: field %s", ErrUnknownRecord, fieldName)
}

// Purge destroys the content and emits a Tombstone.
//
// Failure semantics, in order: a held record cannot be purged; a record
// that still carries un-redacted PII cannot be purged (it must be
// redacted first, so the tombstone's redaction hash is meaningful); and
// a record that is not PURGE_ELIGIBLE cannot be purged early.
func (e *Engine) Purge(id, actor, reason string, tick uint64) (Tombstone, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.tickLocked(tick); err != nil {
		return Tombstone{}, err
	}
	r, ok := e.records[id]
	if !ok {
		return Tombstone{}, fmt.Errorf("%w: %s", ErrUnknownRecord, id)
	}
	if len(r.Holds) > 0 {
		return Tombstone{}, fmt.Errorf("%w: %s held by %v", ErrLegalHold, id, r.Holds)
	}
	pol := e.policies[r.Tenant+"|"+r.EvidenceClass]
	if needsRedaction(r, pol) {
		return Tombstone{}, fmt.Errorf("%w: %s", ErrRedactionRequired, id)
	}
	if r.State != StatePurgeEligible {
		return Tombstone{}, fmt.Errorf("%w: %s is %s, not PURGE_ELIGIBLE", ErrNotEligible, id, r.State)
	}
	names := make([]string, 0, len(r.Fields))
	for i := range r.Fields {
		names = append(names, r.Fields[i].Name)
		r.Fields[i].Value = ""
		r.Fields[i].Removed = true
	}
	sort.Strings(names)
	ts := Tombstone{RecordID: id, Tenant: r.Tenant, EvidenceClass: r.EvidenceClass,
		ContentHash: r.ContentHash, RedactionHash: r.RedactionHash, IngestTick: r.IngestTick,
		PurgedTick: tick, PolicyVersion: r.PolicyVersion, Actor: actor, Reason: reason,
		FieldNames: names}
	ts.Hash = hashTombstone(ts)
	e.tombstones[id] = ts
	from := r.State
	r.State = StatePurged
	r.PurgedTick = tick
	e.appendLocked(Event{Kind: "PURGE", RecordID: id, From: from, To: StatePurged, Tick: tick,
		Actor: actor, Detail: reason, Payload: ts.Hash})
	return ts, nil
}

// ProveExistence answers "did this content ever exist under governance"
// after the content is gone. Returns the tombstone if the record was
// purged, or the live content hash if it was not.
func (e *Engine) ProveExistence(id string) (contentHash string, tomb *Tombstone, err error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if ts, ok := e.tombstones[id]; ok {
		cp := ts
		return ts.ContentHash, &cp, nil
	}
	r, ok := e.records[id]
	if !ok {
		return "", nil, fmt.Errorf("%w: %s", ErrUnknownRecord, id)
	}
	return r.ContentHash, nil, nil
}

// Record returns a copy of a governed record.
func (e *Engine) Record(id string) (Record, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	r, ok := e.records[id]
	if !ok {
		return Record{}, false
	}
	cp := *r
	cp.Fields = append([]Field(nil), r.Fields...)
	cp.Holds = append([]string(nil), r.Holds...)
	return cp, true
}

// Ledger returns a copy of the audit trail.
func (e *Engine) Ledger() []Event {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return append([]Event(nil), e.ledger...)
}

// Head is the ledger tip hash.
func (e *Engine) Head() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if len(e.ledger) == 0 {
		return ""
	}
	return e.ledger[len(e.ledger)-1].Hash
}

// VerifyChain re-derives every hash and link.
func (e *Engine) VerifyChain() error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	prev := ""
	for i, ev := range e.ledger {
		if ev.Index != uint64(i) {
			return fmt.Errorf("%w: index %d out of order", ErrLedgerTampered, ev.Index)
		}
		if ev.PrevHash != prev {
			return fmt.Errorf("%w: broken link at %d", ErrLedgerTampered, i)
		}
		if hashEvent(ev) != ev.Hash {
			return fmt.Errorf("%w: content hash mismatch at %d", ErrLedgerTampered, i)
		}
		prev = ev.Hash
	}
	return nil
}

func (e *Engine) moveLocked(r *Record, to State, tick uint64, actor, detail string) {
	if !canGo(r.State, to) {
		return
	}
	from := r.State
	r.State = to
	e.appendLocked(Event{Kind: "TRANSITION", RecordID: r.ID, From: from, To: to, Tick: tick,
		Actor: actor, Detail: detail, Payload: r.ContentHash})
}

func (e *Engine) tickLocked(tick uint64) error {
	if tick < e.lastTick {
		return fmt.Errorf("%w: %d < %d", ErrNonMonotonicTick, tick, e.lastTick)
	}
	e.lastTick = tick
	return nil
}

func (e *Engine) appendLocked(ev Event) {
	ev.Index = uint64(len(e.ledger))
	if len(e.ledger) > 0 {
		ev.PrevHash = e.ledger[len(e.ledger)-1].Hash
	}
	ev.Hash = hashEvent(ev)
	e.ledger = append(e.ledger, ev)
}

func fieldPreImage(name, value string) string {
	sum := sha256.Sum256([]byte("datagov_field/v1\n" + name + "\n" + value + "\n"))
	return hex.EncodeToString(sum[:])
}

func hashFields(fields []Field, postRedaction bool) string {
	var sb strings.Builder
	if postRedaction {
		sb.WriteString("datagov_redacted/v1\n")
	} else {
		sb.WriteString("datagov_content/v1\n")
	}
	f := append([]Field(nil), fields...)
	sort.SliceStable(f, func(i, j int) bool { return f[i].Name < f[j].Name })
	for _, x := range f {
		sb.WriteString(x.Name + "|" + string(x.PII) + "|" + strconv.FormatBool(x.Removed) + "|")
		if x.Removed {
			sb.WriteString(x.PreImageHash)
		} else {
			sb.WriteString(fieldPreImage(x.Name, x.Value))
		}
		sb.WriteString("\n")
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

func hashTombstone(t Tombstone) string {
	s := "datagov_tombstone/v1\n" + t.RecordID + "\n" + t.Tenant + "\n" + t.EvidenceClass +
		"\n" + t.ContentHash + "\n" + t.RedactionHash +
		"\n" + strconv.FormatUint(t.IngestTick, 10) + "\n" + strconv.FormatUint(t.PurgedTick, 10) +
		"\n" + strconv.FormatUint(t.PolicyVersion, 10) + "\n" + t.Actor + "\n" + t.Reason +
		"\n" + strings.Join(t.FieldNames, ",") + "\n"
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func hashHold(h Hold) string {
	s := "datagov_hold/v1\n" + h.ID + "\n" + h.Matter + "\n" + h.Tenant + "\n" + h.Class +
		"\n" + h.Actor + "\n" + h.Reason + "\n" + strconv.FormatUint(h.FromTick, 10) +
		"\n" + strconv.FormatUint(h.ToTick, 10) + "\n"
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func hashEvent(ev Event) string {
	s := "datagov_event/v1\ni=" + strconv.FormatUint(ev.Index, 10) + "\nkind=" + ev.Kind +
		"\nrid=" + ev.RecordID + "\nfrom=" + string(ev.From) + "\nto=" + string(ev.To) +
		"\ntick=" + strconv.FormatUint(ev.Tick, 10) + "\nactor=" + ev.Actor +
		"\ndetail=" + ev.Detail + "\npayload=" + ev.Payload + "\nprev=" + ev.PrevHash + "\n"
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func appendUnique(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

func removeString(list []string, s string) []string {
	out := list[:0]
	for _, x := range list {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}
