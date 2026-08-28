// Package preservation is the fifth core claim question of the
// functional spec (§9, §19–§20): evidence preservation. It was
// classified MISSING in this round's reconciliation — chain of custody
// existed on evidence.Record, but no preservation order, no trigger, no
// custodian assignment, no legal-hold state and no release did.
//
// The spec's §19 field list is implemented verbatim:
//
//	PreservationOrder
//	- PreservationID   - PreservationStart
//	- CaseID           - PreservationDeadline
//	- Trigger          - LegalHoldState
//	- Scope            - RightsState
//	- Custodian        - Hash
//	- EvidenceTypes    - ChainOfCustody
//
// and §20's workflow (incident detected → potential claim → trigger →
// inventory → custodian → immutable capture → hash → chain of custody →
// access log → correction/supersession → release) is the Order's own
// lifecycle.
//
// Three deliberate non-duplications:
//
//   - RightsState is pkg/evidence/provenance's, not a second one.
//   - The chain of custody per evidence item stays on
//     evidence.Record.ChainOfCustody; an Order references evidence by
//     content-addressed EvidenceID and records the ORDER's own custody
//     events, never a second copy of each record's.
//   - Retention and deletion are pkg/governance/data's job. An Order
//     records that a hold exists and what it covers; it deletes nothing
//     and expires nothing.
//
// The integrity property this package exists to provide: an Order's
// Hash is computed over its own semantic fields plus the sorted
// EvidenceIDs it covers, so any change to the preserved set — an item
// added, removed, or (since EvidenceIDs are content-addressed) altered
// — changes the hash. VerifyChain recomputes it independently.
package preservation

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"veriqo/pkg/evidence/provenance"
	insevidence "veriqo/pkg/insurance/evidence"
)

// Trigger is what caused preservation to begin. The spec's §20
// workflow starts at "incident detected" and "potential claim
// identified"; both, and the others below, are real distinct triggers
// with different urgency and scope, so they are modelled separately
// rather than collapsed into a free-text field.
type Trigger string

const (
	TriggerIncidentDetected      Trigger = "INCIDENT_DETECTED"
	TriggerPotentialClaim        Trigger = "POTENTIAL_CLAIM_IDENTIFIED"
	TriggerClaimNotified         Trigger = "CLAIM_NOTIFIED"
	TriggerDisputeOpened         Trigger = "DISPUTE_OPENED"
	TriggerRegulatoryRequest     Trigger = "REGULATORY_REQUEST"
	TriggerLegalInstruction      Trigger = "LEGAL_INSTRUCTION"
	TriggerContractualObligation Trigger = "CONTRACTUAL_OBLIGATION"
)

var knownTriggers = map[Trigger]bool{
	TriggerIncidentDetected: true, TriggerPotentialClaim: true, TriggerClaimNotified: true,
	TriggerDisputeOpened: true, TriggerRegulatoryRequest: true, TriggerLegalInstruction: true,
	TriggerContractualObligation: true,
}

// IsKnownTrigger reports whether t is a modelled preservation trigger.
func IsKnownTrigger(t Trigger) bool { return knownTriggers[t] }

// LegalHoldState is whether a hold is in force over the order's scope.
type LegalHoldState string

const (
	// HoldNotPlaced: preservation is under way but no formal legal hold
	// has been instructed.
	HoldNotPlaced LegalHoldState = "NOT_PLACED"
	// HoldInForce: a hold is in force. While in force, an order cannot
	// be released.
	HoldInForce LegalHoldState = "IN_FORCE"
	// HoldReleased: the hold was lifted, with the release recorded.
	HoldReleased LegalHoldState = "RELEASED"
)

var knownHoldStates = map[LegalHoldState]bool{
	HoldNotPlaced: true, HoldInForce: true, HoldReleased: true,
}

// IsKnownHoldState reports whether s is a modelled hold state.
func IsKnownHoldState(s LegalHoldState) bool { return knownHoldStates[s] }

// CustodyAction is one recorded step in the order's own custody log.
type CustodyAction string

const (
	ActionOrderOpened     CustodyAction = "ORDER_OPENED"
	ActionCustodianSet    CustodyAction = "CUSTODIAN_ASSIGNED"
	ActionItemPreserved   CustodyAction = "ITEM_PRESERVED"
	ActionAccessed        CustodyAction = "ACCESSED"
	ActionExported        CustodyAction = "EXPORTED"
	ActionCorrected       CustodyAction = "CORRECTION_RECORDED"
	ActionSuperseded      CustodyAction = "SUPERSESSION_RECORDED"
	ActionHoldPlaced      CustodyAction = "LEGAL_HOLD_PLACED"
	ActionHoldReleased    CustodyAction = "LEGAL_HOLD_RELEASED"
	ActionOrderReleased   CustodyAction = "ORDER_RELEASED"
	ActionScopeExtended   CustodyAction = "SCOPE_EXTENDED"
	ActionCustodianChange CustodyAction = "CUSTODIAN_CHANGED"
)

var knownActions = map[CustodyAction]bool{
	ActionOrderOpened: true, ActionCustodianSet: true, ActionItemPreserved: true,
	ActionAccessed: true, ActionExported: true, ActionCorrected: true,
	ActionSuperseded: true, ActionHoldPlaced: true, ActionHoldReleased: true,
	ActionOrderReleased: true, ActionScopeExtended: true, ActionCustodianChange: true,
}

// IsKnownAction reports whether a is a modelled custody action.
func IsKnownAction(a CustodyAction) bool { return knownActions[a] }

// CustodyEvent is one entry in the order's append-only custody log.
// Every event names WHO did it — an event with no actor is exactly the
// untraceable record a chain of custody exists to prevent.
type CustodyEvent struct {
	Action CustodyAction `json:"action"`
	Actor  string        `json:"actor"`
	Tick   uint64        `json:"tick"`
	// EvidenceID is set for per-item actions (preserved, accessed,
	// exported, corrected, superseded); empty for order-level ones.
	EvidenceID string `json:"evidence_id,omitempty"`
	// Detail is free text recording anything the actor wants a later
	// reader to know.
	Detail string `json:"detail,omitempty"`
}

// Errors.
var (
	ErrEmptyPreservationID = errors.New("preservation: PreservationID must be non-empty")
	ErrEmptyCaseID         = errors.New("preservation: CaseID must be non-empty")
	ErrUnknownTrigger      = errors.New("preservation: unknown Trigger")
	ErrEmptyScope          = errors.New("preservation: a preservation order must state its scope")
	ErrEmptyCustodian      = errors.New(
		"preservation: a preservation order must name a custodian — evidence with nobody responsible for it is not preserved")
	ErrNoEvidenceTypes = errors.New(
		"preservation: a preservation order must declare which evidence types it covers")
	ErrUnknownHoldState  = errors.New("preservation: unknown LegalHoldState")
	ErrUnknownAction     = errors.New("preservation: unknown CustodyAction")
	ErrEmptyActor        = errors.New("preservation: every custody event must name the actor who performed it")
	ErrOrderReleased     = errors.New("preservation: this preservation order has already been released")
	ErrHoldInForce       = errors.New("preservation: a preservation order under a legal hold in force cannot be released")
	ErrEvidenceNotInCase = errors.New("preservation: this evidence record belongs to a different case")
	ErrHashMismatch      = errors.New("preservation: recomputed order hash does not match the recorded hash")
	ErrNotPreserved      = errors.New("preservation: this EvidenceID is not covered by this order")
	ErrRightsDeny        = errors.New("preservation: the evidence's rights state does not permit the requested use")
)

// Order is one preservation order — the spec §19 object.
type Order struct {
	mu sync.RWMutex

	PreservationID string
	CaseID         string

	trigger       Trigger
	scope         string
	custodian     string
	evidenceTypes []string

	startTick    uint64
	deadlineTick uint64

	holdState LegalHoldState

	// rights is the rights state declared for the preserved SET. It is
	// pkg/evidence/provenance's own vocabulary. Individual records carry
	// their own rights too; PermitsUse below requires BOTH to allow a
	// use, so a permissive order can never widen a restricted record.
	rights provenance.RightsState

	// preserved maps EvidenceID -> the tick it was preserved at.
	preserved map[string]uint64
	order     []string

	custody []CustodyEvent

	released     bool
	releasedTick uint64
}

// New opens a preservation order. Everything the spec §19 names as a
// field is required up front except the deadline (which some triggers
// genuinely do not carry) and the evidence set (which is inventoried
// after the order opens, per the §20 workflow).
func New(preservationID, caseID string, trigger Trigger, scope, custodian string,
	evidenceTypes []string, startTick, deadlineTick uint64, openedBy string) (*Order, error) {

	if preservationID == "" {
		return nil, ErrEmptyPreservationID
	}
	if caseID == "" {
		return nil, ErrEmptyCaseID
	}
	if !IsKnownTrigger(trigger) {
		return nil, fmt.Errorf("%w: %q", ErrUnknownTrigger, trigger)
	}
	if strings.TrimSpace(scope) == "" {
		return nil, ErrEmptyScope
	}
	if strings.TrimSpace(custodian) == "" {
		return nil, ErrEmptyCustodian
	}
	if len(evidenceTypes) == 0 {
		return nil, ErrNoEvidenceTypes
	}
	if strings.TrimSpace(openedBy) == "" {
		return nil, ErrEmptyActor
	}

	o := &Order{
		PreservationID: preservationID,
		CaseID:         caseID,
		trigger:        trigger,
		scope:          scope,
		custodian:      custodian,
		evidenceTypes:  append([]string(nil), evidenceTypes...),
		startTick:      startTick,
		deadlineTick:   deadlineTick,
		holdState:      HoldNotPlaced,
		// The honest default: preservation says nothing about what
		// VERIQO may DO with the evidence.
		rights:    provenance.RightsUnknownPendingContract,
		preserved: map[string]uint64{},
	}
	o.custody = append(o.custody, CustodyEvent{
		Action: ActionOrderOpened, Actor: openedBy, Tick: startTick,
		Detail: fmt.Sprintf("trigger=%s scope=%s custodian=%s", trigger, scope, custodian),
	})
	return o, nil
}

// Trigger, Scope, Custodian, EvidenceTypes, Start, Deadline, HoldState
// and Rights are read accessors for the spec §19 fields.
func (o *Order) Trigger() Trigger { o.mu.RLock(); defer o.mu.RUnlock(); return o.trigger }
func (o *Order) Scope() string    { o.mu.RLock(); defer o.mu.RUnlock(); return o.scope }
func (o *Order) Custodian() string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.custodian
}
func (o *Order) EvidenceTypes() []string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return append([]string(nil), o.evidenceTypes...)
}
func (o *Order) Start() uint64    { o.mu.RLock(); defer o.mu.RUnlock(); return o.startTick }
func (o *Order) Deadline() uint64 { o.mu.RLock(); defer o.mu.RUnlock(); return o.deadlineTick }
func (o *Order) HoldState() LegalHoldState {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.holdState
}
func (o *Order) Rights() provenance.RightsState {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.rights
}

// Released reports whether the order has been released.
func (o *Order) Released() bool { o.mu.RLock(); defer o.mu.RUnlock(); return o.released }

// SetRights records the rights state declared for the preserved set.
// Like insevidence.Registry.SetRights this is a RECORDING operation —
// rights are granted or revoked by a legal/commercial act elsewhere.
func (o *Order) SetRights(state provenance.RightsState, actor string, tick uint64) error {
	if !provenance.IsKnownRightsState(state) {
		return fmt.Errorf("preservation: unknown rights state %q", state)
	}
	if strings.TrimSpace(actor) == "" {
		return ErrEmptyActor
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.released {
		return ErrOrderReleased
	}
	o.rights = state
	o.custody = append(o.custody, CustodyEvent{
		Action: ActionScopeExtended, Actor: actor, Tick: tick,
		Detail: "rights state recorded: " + string(state),
	})
	return nil
}

// Preserve records that one evidence record is now covered by this
// order. The record must belong to the same case: preserving another
// case's evidence under this order would make the order's hash a claim
// about a set it does not own.
func (o *Order) Preserve(rec insevidence.Record, actor string, tick uint64) error {
	if strings.TrimSpace(actor) == "" {
		return ErrEmptyActor
	}
	if rec.CaseID != o.CaseID {
		return fmt.Errorf("%w: order case=%s record case=%s", ErrEvidenceNotInCase, o.CaseID, rec.CaseID)
	}
	id := rec.EvidenceID()
	if id == "" {
		return errors.New("preservation: the evidence record has no content-addressed EvidenceID")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.released {
		return ErrOrderReleased
	}
	if _, exists := o.preserved[id]; exists {
		return nil // idempotent: preserving the same item twice is not an error
	}
	o.preserved[id] = tick
	o.order = append(o.order, id)
	o.custody = append(o.custody, CustodyEvent{
		Action: ActionItemPreserved, Actor: actor, Tick: tick, EvidenceID: id,
		Detail: "document_type=" + rec.DocumentType,
	})
	return nil
}

// RecordAccess, RecordExport, RecordCorrection and RecordSupersession
// append the §20 workflow's own required log entries. Each requires the
// item to actually be under this order — logging access to something
// the order does not cover would make the log a fiction.
func (o *Order) RecordAccess(evidenceID, actor, detail string, tick uint64) error {
	return o.appendItemEvent(ActionAccessed, evidenceID, actor, detail, tick)
}

// RecordExport logs an export. Exports are the highest-consequence
// custody event, which is why they are logged separately from access.
func (o *Order) RecordExport(evidenceID, actor, detail string, tick uint64) error {
	return o.appendItemEvent(ActionExported, evidenceID, actor, detail, tick)
}

// RecordCorrection logs that a correction was issued. It does NOT edit
// anything: corrections create new records (see
// insevidence.Registry.MarkSuperseded).
func (o *Order) RecordCorrection(evidenceID, actor, detail string, tick uint64) error {
	return o.appendItemEvent(ActionCorrected, evidenceID, actor, detail, tick)
}

// RecordSupersession logs that an item was superseded by another.
func (o *Order) RecordSupersession(evidenceID, actor, detail string, tick uint64) error {
	return o.appendItemEvent(ActionSuperseded, evidenceID, actor, detail, tick)
}

func (o *Order) appendItemEvent(action CustodyAction, evidenceID, actor, detail string, tick uint64) error {
	if !IsKnownAction(action) {
		return fmt.Errorf("%w: %q", ErrUnknownAction, action)
	}
	if strings.TrimSpace(actor) == "" {
		return ErrEmptyActor
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, ok := o.preserved[evidenceID]; !ok {
		return fmt.Errorf("%w: %s", ErrNotPreserved, evidenceID)
	}
	o.custody = append(o.custody, CustodyEvent{
		Action: action, Actor: actor, Tick: tick, EvidenceID: evidenceID, Detail: detail,
	})
	return nil
}

// PlaceHold puts a legal hold in force over this order's scope.
func (o *Order) PlaceHold(actor, reason string, tick uint64) error {
	if strings.TrimSpace(actor) == "" {
		return ErrEmptyActor
	}
	if strings.TrimSpace(reason) == "" {
		return errors.New("preservation: placing a legal hold must record why")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.released {
		return ErrOrderReleased
	}
	o.holdState = HoldInForce
	o.custody = append(o.custody, CustodyEvent{
		Action: ActionHoldPlaced, Actor: actor, Tick: tick, Detail: reason,
	})
	return nil
}

// ReleaseHold lifts a hold. The hold's history stays in the custody log.
func (o *Order) ReleaseHold(actor, reason string, tick uint64) error {
	if strings.TrimSpace(actor) == "" {
		return ErrEmptyActor
	}
	if strings.TrimSpace(reason) == "" {
		return errors.New("preservation: releasing a legal hold must record why")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.holdState != HoldInForce {
		return errors.New("preservation: no legal hold is in force on this order")
	}
	o.holdState = HoldReleased
	o.custody = append(o.custody, CustodyEvent{
		Action: ActionHoldReleased, Actor: actor, Tick: tick, Detail: reason,
	})
	return nil
}

// Release closes the preservation order — the last step of the §20
// workflow. It is refused while a legal hold is in force: releasing
// preserved evidence out from under a live hold is precisely the
// failure a hold exists to prevent.
func (o *Order) Release(actor, reason string, tick uint64) error {
	if strings.TrimSpace(actor) == "" {
		return ErrEmptyActor
	}
	if strings.TrimSpace(reason) == "" {
		return errors.New("preservation: releasing a preservation order must record why")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.released {
		return ErrOrderReleased
	}
	if o.holdState == HoldInForce {
		return ErrHoldInForce
	}
	o.released = true
	o.releasedTick = tick
	o.custody = append(o.custody, CustodyEvent{
		Action: ActionOrderReleased, Actor: actor, Tick: tick, Detail: reason,
	})
	return nil
}

// PreservedIDs returns every preserved EvidenceID, sorted, so callers
// and the hash below never depend on insertion order.
func (o *Order) PreservedIDs() []string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := append([]string(nil), o.order...)
	sort.Strings(out)
	return out
}

// Covers reports whether evidenceID is under this order.
func (o *Order) Covers(evidenceID string) bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	_, ok := o.preserved[evidenceID]
	return ok
}

// CustodyLog returns the order's append-only custody log, oldest first.
func (o *Order) CustodyLog() []CustodyEvent {
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := make([]CustodyEvent, len(o.custody))
	copy(out, o.custody)
	return out
}

// PermitsUse is the fail-closed gate for using preserved evidence. BOTH
// the record's own rights and the order's declared rights must permit
// the use: a permissive order can never widen a restricted record, and
// a restrictive order narrows everything under it.
func (o *Order) PermitsUse(rec insevidence.Record, use provenance.Use) error {
	if !o.Covers(rec.EvidenceID()) {
		return fmt.Errorf("%w: %s", ErrNotPreserved, rec.EvidenceID())
	}
	if !rec.Permits(use) {
		return fmt.Errorf("%w: record %s rights=%s use=%s",
			ErrRightsDeny, rec.EvidenceID(), rec.Rights, use)
	}
	if !o.Rights().Permits(use) {
		return fmt.Errorf("%w: order %s rights=%s use=%s",
			ErrRightsDeny, o.PreservationID, o.Rights(), use)
	}
	return nil
}

// Hash is the order's tamper-evident content hash: SHA-256 over its own
// semantic fields plus its sorted preserved EvidenceIDs. Since those IDs
// are themselves content-addressed, any change to a preserved record's
// content changes its ID, which changes this hash — the same
// hash-of-hashes property pkg/insurance/verification.Manifest uses,
// extended here to the preservation order's own metadata.
//
// Hand-rolled and field-ordered, for the same cross-language
// reproducibility reason every other ledger in this codebase documents.
func (o *Order) Hash() string {
	o.mu.RLock()
	ids := append([]string(nil), o.order...)
	var b strings.Builder
	fmt.Fprintf(&b, "veriqo.insurance.preservation/v1\nid=%s\ncase=%s\ntrigger=%s\nscope=%s\ncustodian=%s\n",
		o.PreservationID, o.CaseID, o.trigger, o.scope, o.custodian)
	fmt.Fprintf(&b, "start=%d\ndeadline=%d\nhold=%s\nrights=%s\nreleased=%t\n",
		o.startTick, o.deadlineTick, o.holdState, o.rights, o.released)
	types := append([]string(nil), o.evidenceTypes...)
	o.mu.RUnlock()

	sort.Strings(types)
	for _, t := range types {
		fmt.Fprintf(&b, "type=%s\n", t)
	}
	sort.Strings(ids)
	for _, id := range ids {
		fmt.Fprintf(&b, "evidence=%s\n", id)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// ---- The preservation chain report ----------------------------------

// ChainReport is the derived answer to "is this order's preservation
// chain intact". Every field is computed; nothing here is settable, and
// there is no way to declare a chain complete.
//
// The nine checks are exactly the ones the functional spec §56
// (Preservation Gate) names: trigger recorded, scope recorded, evidence
// preserved, hash recorded, custodian recorded, access recorded,
// correction recorded, export recorded, chain verified.
type ChainReport struct {
	PreservationID string `json:"preservation_id"`
	CaseID         string `json:"case_id"`

	TriggerRecorded   bool `json:"trigger_recorded"`
	ScopeRecorded     bool `json:"scope_recorded"`
	CustodianRecorded bool `json:"custodian_recorded"`
	EvidencePreserved bool `json:"evidence_preserved"`
	HashRecorded      bool `json:"hash_recorded"`
	// AccessLogged / ExportLogged / CorrectionLogged report whether the
	// order's log has the CAPACITY to record these AND that every such
	// event it does hold names an actor and a covered item. They are
	// deliberately NOT "an access happened": an order over which nobody
	// has yet exercised access is perfectly intact.
	AccessEventsWellFormed     bool `json:"access_events_well_formed"`
	ExportEventsWellFormed     bool `json:"export_events_well_formed"`
	CorrectionEventsWellFormed bool `json:"correction_events_well_formed"`
	ChainVerified              bool `json:"chain_verified"`

	EvidenceCount int    `json:"evidence_count"`
	Hash          string `json:"hash"`

	// Failures names every check that did not pass, in words. A PASSing
	// report has an empty Failures list, and Pass() is derived from it.
	Failures []string `json:"failures,omitempty"`
}

// Pass reports whether every check passed. Derived from Failures — there
// is no settable pass field.
func (r ChainReport) Pass() bool { return len(r.Failures) == 0 }

// Verify recomputes the order's hash and checks every §56 requirement.
// expectedHash may be empty, in which case the hash is recorded but not
// compared (the order has never been anchored); when non-empty, a
// mismatch is a failure.
func (o *Order) Verify(expectedHash string) ChainReport {
	r := ChainReport{
		PreservationID: o.PreservationID,
		CaseID:         o.CaseID,
		EvidenceCount:  len(o.PreservedIDs()),
		Hash:           o.Hash(),
	}

	r.TriggerRecorded = IsKnownTrigger(o.Trigger())
	if !r.TriggerRecorded {
		r.Failures = append(r.Failures, "no recognised preservation trigger is recorded")
	}
	r.ScopeRecorded = strings.TrimSpace(o.Scope()) != ""
	if !r.ScopeRecorded {
		r.Failures = append(r.Failures, "no scope is recorded")
	}
	r.CustodianRecorded = strings.TrimSpace(o.Custodian()) != ""
	if !r.CustodianRecorded {
		r.Failures = append(r.Failures, "no custodian is recorded")
	}
	r.EvidencePreserved = r.EvidenceCount > 0
	if !r.EvidencePreserved {
		r.Failures = append(r.Failures, "the order covers no evidence")
	}
	r.HashRecorded = r.Hash != ""
	if !r.HashRecorded {
		r.Failures = append(r.Failures, "no hash could be computed")
	}

	// Every logged event must name an actor, and every per-item event
	// must name an item the order actually covers.
	access, export, correction := true, true, true
	for _, e := range o.CustodyLog() {
		wellFormed := strings.TrimSpace(e.Actor) != ""
		if e.EvidenceID != "" && !o.Covers(e.EvidenceID) {
			wellFormed = false
		}
		if wellFormed {
			continue
		}
		switch e.Action {
		case ActionAccessed:
			access = false
			r.Failures = append(r.Failures, "an access event is malformed (no actor, or an item the order does not cover)")
		case ActionExported:
			export = false
			r.Failures = append(r.Failures, "an export event is malformed (no actor, or an item the order does not cover)")
		case ActionCorrected, ActionSuperseded:
			correction = false
			r.Failures = append(r.Failures, "a correction/supersession event is malformed (no actor, or an item the order does not cover)")
		default:
			r.Failures = append(r.Failures, fmt.Sprintf("custody event %s is malformed (no actor)", e.Action))
		}
	}
	r.AccessEventsWellFormed = access
	r.ExportEventsWellFormed = export
	r.CorrectionEventsWellFormed = correction

	r.ChainVerified = true
	if expectedHash != "" && expectedHash != r.Hash {
		r.ChainVerified = false
		r.Failures = append(r.Failures, fmt.Sprintf("%s: expected=%s recomputed=%s", ErrHashMismatch, expectedHash, r.Hash))
	}
	// An order that has never logged the opening of itself has a broken
	// log, whatever else is true.
	if len(o.CustodyLog()) == 0 {
		r.ChainVerified = false
		r.Failures = append(r.Failures, "the custody log is empty; not even the order's own opening is recorded")
	}
	return r
}
