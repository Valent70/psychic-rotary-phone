// Package timeline is VICE's Timeline Reconstruction Engine — blueprint
// §13. It reconstructs, from independently sourced accounts, WHEN the
// events of an insurance case are claimed to have happened, and (in
// conflict.go) automatically surfaces where those accounts disagree.
//
// A deliberate architectural choice, driven by blueprint §36's Golden
// Rule ("Never convert uncertainty into certainty"): when two sources
// disagree about the time of what might be the same real-world
// occurrence (e.g. a claim narrative says damage was discovered at
// 10:00, a photo's EXIF metadata says 11:45), this package does NOT
// silently pick one, average them, or collapse them into a single
// resolved Event. Each independently sourced account becomes its OWN
// Event, carrying only ITS OWN claimed time and ITS OWN source
// evidence. Reconciling — or refusing to reconcile — competing
// accounts is conflict.go's job (DetectConflicts), not this file's.
//
// Time representation: the rest of pkg/insurance (case, policy,
// evidence) uses an opaque uint64 "tick" purely for ordering, with no
// committed unit (see pkg/evidence/ontology's own words: "Ticks, not
// timestamps"). This package cannot stay that abstract: blueprint §14
// demands real arithmetic over real gaps ("a difference of 1h45m", "a
// 7-hour timezone offset"). EventTimeUTC is therefore documented to be
// Unix epoch seconds — the one field in pkg/insurance that commits to a
// concrete time unit, because its own name ("event_time_utc") already
// commits to one. EventTimeOriginal and Timezone stay opaque strings:
// exactly what the source said, never reparsed or "corrected" by this
// package.
package timeline

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"veriqo/pkg/insurance/party"
)

// EventType is one of the fourteen timeline event types blueprint §13
// enumerates. The set is closed, matching how party.Role and
// evidence.Origin are modelled elsewhere in pkg/insurance: an
// unrecognised event type is a construction error, not silently
// accepted freeform text.
type EventType string

const (
	TypeLoading         EventType = "LOADING"
	TypeHandover        EventType = "HANDOVER"
	TypeDeparture       EventType = "DEPARTURE"
	TypeVoyage          EventType = "VOYAGE"
	TypeIncident        EventType = "INCIDENT"
	TypeNotice          EventType = "NOTICE"
	TypeArrival         EventType = "ARRIVAL"
	TypeDischarge       EventType = "DISCHARGE"
	TypeDelivery        EventType = "DELIVERY"
	TypeDamageDiscovery EventType = "DAMAGE_DISCOVERY"
	TypeSurvey          EventType = "SURVEY"
	TypeMitigation      EventType = "MITIGATION"
	TypeClaim           EventType = "CLAIM"
	TypeRecovery        EventType = "RECOVERY"
)

// canonicalOrder is the blueprint's own §13 event-type list order,
// treated by this package as the canonical expected chronological
// order of a claim's lifecycle. It is used ONLY as the basis for
// impossible-sequence detection (conflict.go) — see the extensive
// comment there on what "impossible" is defined to mean and its
// deliberate limitations.
var canonicalOrder = []EventType{
	TypeLoading, TypeHandover, TypeDeparture, TypeVoyage, TypeIncident,
	TypeNotice, TypeArrival, TypeDischarge, TypeDelivery, TypeDamageDiscovery,
	TypeSurvey, TypeMitigation, TypeClaim, TypeRecovery,
}

var canonicalRank = func() map[EventType]int {
	m := make(map[EventType]int, len(canonicalOrder))
	for i, t := range canonicalOrder {
		m[t] = i
	}
	return m
}()

var knownEventTypes = func() map[EventType]bool {
	m := make(map[EventType]bool, len(canonicalOrder))
	for _, t := range canonicalOrder {
		m[t] = true
	}
	return m
}()

// IsKnownEventType reports whether t is one of the modelled event types.
func IsKnownEventType(t EventType) bool { return knownEventTypes[t] }

// KnownEventTypes returns every modelled event type in the blueprint's
// own §13 canonical order.
func KnownEventTypes() []EventType {
	out := make([]EventType, len(canonicalOrder))
	copy(out, canonicalOrder)
	return out
}

// Certainty is how confident the timeline reconstruction is in THIS
// event's claimed time — deliberately a field on the Event, not a
// property this package derives and overwrites, because only the
// party asserting the event (or the human reviewer) can honestly know
// how firm that assertion is. A zero-value (empty) Certainty is
// refused by Validate — matching evidence.Strength's zero-value
// refusal, "not yet assessed" must never silently pass as "assessed".
type Certainty string

const (
	CertaintyConfirmed Certainty = "CONFIRMED" // directly observed/recorded contemporaneously by a reliable instrument or process
	CertaintyProbable  Certainty = "PROBABLE"  // asserted by a party, plausible, not independently corroborated
	CertaintyEstimated Certainty = "ESTIMATED" // approximate/reconstructed (e.g. "sometime that afternoon")
	CertaintyDisputed  Certainty = "DISPUTED"  // the parties themselves disagree about this event's time
	CertaintyUnknown   Certainty = "UNKNOWN"   // explicitly not knowable from available evidence
)

var knownCertainties = map[Certainty]bool{
	CertaintyConfirmed: true, CertaintyProbable: true, CertaintyEstimated: true,
	CertaintyDisputed: true, CertaintyUnknown: true,
}

// IsKnownCertainty reports whether c is a modelled certainty value.
func IsKnownCertainty(c Certainty) bool { return knownCertainties[c] }

var (
	ErrEmptyEventID     = errors.New("timeline: EventID must be non-empty")
	ErrUnknownEventType = errors.New("timeline: unknown EventType")
	ErrNoSourceEvidence = errors.New("timeline: an event must cite at least one source_evidence ID — VICE must never invent an event without evidence (blueprint §0)")
	ErrUnknownCertainty = errors.New("timeline: unknown or unset Certainty")
	ErrEmptyCaseID      = errors.New("timeline: CaseID must be non-empty")
	ErrDuplicateEventID = errors.New("timeline: EventID already present in this timeline")
)

// Event is one reconstructed timeline event — blueprint §13's own
// worked JSON example, field for field:
//
//	{
//	  "event_id": "EVT-001",
//	  "event_type": "DAMAGE_DISCOVERY",
//	  "event_time_original": "...",
//	  "timezone": "...",
//	  "event_time_utc": "...",
//	  "source_evidence": [],
//	  "certainty": "...",
//	  "location": "...",
//	  "actor": "..."
//	}
type Event struct {
	EventID string `json:"event_id"`

	EventType EventType `json:"event_type"`

	// EventTimeOriginal is exactly what the source stated — never
	// reparsed, never "corrected" by this package.
	EventTimeOriginal string `json:"event_time_original,omitempty"`
	// Timezone is exactly what the source declared (e.g. "+07:00",
	// "UTC", "Asia/Jakarta"). Only offsets this package's own small
	// parser (conflict.go) can read are used in timezone-conflict
	// detection; anything else is treated as insufficient data for
	// that specific check, never guessed.
	Timezone string `json:"timezone,omitempty"`
	// EventTimeUTC is this event's resolved absolute time, in Unix
	// epoch seconds — see the package doc comment for why this one
	// field commits to a concrete unit where the rest of pkg/insurance
	// stays with an opaque tick.
	EventTimeUTC uint64 `json:"event_time_utc"`

	// SourceEvidence is the list of EvidenceIDs (content-addressed IDs
	// from pkg/insurance/evidence.Record.EvidenceID()) this event's
	// claimed time rests on. Never a duplicated/invented identity —
	// always a real evidence.Record's own ID.
	SourceEvidence []string `json:"source_evidence"`

	Certainty Certainty `json:"certainty"`
	Location  string    `json:"location,omitempty"`
	// Actor is who is claimed to have caused, witnessed, or recorded
	// this event, expressed as a case-scoped party.PartyID (blueprint
	// §3's party model) rather than a free-text name.
	Actor party.PartyID `json:"actor,omitempty"`
}

// Validate reports whether e satisfies the structural rules every
// Event must satisfy before it can enter a Timeline.
func (e Event) Validate() error {
	if e.EventID == "" {
		return ErrEmptyEventID
	}
	if !IsKnownEventType(e.EventType) {
		return fmt.Errorf("%w: %q", ErrUnknownEventType, e.EventType)
	}
	if len(e.SourceEvidence) == 0 {
		return ErrNoSourceEvidence
	}
	for _, id := range e.SourceEvidence {
		if id == "" {
			return errors.New("timeline: source_evidence must not contain an empty EvidenceID")
		}
	}
	if !IsKnownCertainty(e.Certainty) {
		return ErrUnknownCertainty
	}
	return nil
}

// New constructs and validates an Event.
func New(eventID string, eventType EventType, eventTimeOriginal, timezone string, eventTimeUTC uint64,
	sourceEvidence []string, certainty Certainty, location string, actor party.PartyID) (Event, error) {
	e := Event{
		EventID:           eventID,
		EventType:         eventType,
		EventTimeOriginal: eventTimeOriginal,
		Timezone:          timezone,
		EventTimeUTC:      eventTimeUTC,
		SourceEvidence:    append([]string(nil), sourceEvidence...),
		Certainty:         certainty,
		Location:          location,
		Actor:             actor,
	}
	if err := e.Validate(); err != nil {
		return Event{}, err
	}
	return e, nil
}

// Timeline is the ordered, case-scoped collection of Events — safe for
// concurrent use, matching the sync.RWMutex-guarded registry pattern
// used throughout pkg/insurance (party.Registry, evidence.Registry).
type Timeline struct {
	mu     sync.RWMutex
	caseID string
	events map[string]Event
	order  []string // insertion order, for deterministic All()
}

// NewTimeline returns an empty timeline for caseID.
func NewTimeline(caseID string) (*Timeline, error) {
	if caseID == "" {
		return nil, ErrEmptyCaseID
	}
	return &Timeline{caseID: caseID, events: make(map[string]Event)}, nil
}

// CaseID returns the case this timeline belongs to.
func (tl *Timeline) CaseID() string { return tl.caseID }

// AddEvent validates and adds e to the timeline. Refuses a duplicate
// EventID — two accounts of the same real occurrence must be added as
// two DISTINCT Events with distinct EventIDs (see the package doc
// comment), never merged under one ID.
func (tl *Timeline) AddEvent(e Event) error {
	if err := e.Validate(); err != nil {
		return err
	}
	tl.mu.Lock()
	defer tl.mu.Unlock()
	if _, exists := tl.events[e.EventID]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateEventID, e.EventID)
	}
	tl.events[e.EventID] = e
	tl.order = append(tl.order, e.EventID)
	return nil
}

// Get returns the event for eventID.
func (tl *Timeline) Get(eventID string) (Event, bool) {
	tl.mu.RLock()
	defer tl.mu.RUnlock()
	e, ok := tl.events[eventID]
	return e, ok
}

// All returns every event in insertion order.
func (tl *Timeline) All() []Event {
	tl.mu.RLock()
	defer tl.mu.RUnlock()
	out := make([]Event, 0, len(tl.order))
	for _, id := range tl.order {
		out = append(out, tl.events[id])
	}
	return out
}

// Sorted returns every event ordered by EventTimeUTC ascending, tied
// broken by EventID for a fully deterministic result regardless of
// insertion order — this is the "reconstructed timeline" a caller or
// the dossier generator should read.
func (tl *Timeline) Sorted() []Event {
	out := tl.All()
	sort.Slice(out, func(i, j int) bool {
		if out[i].EventTimeUTC != out[j].EventTimeUTC {
			return out[i].EventTimeUTC < out[j].EventTimeUTC
		}
		return out[i].EventID < out[j].EventID
	})
	return out
}

// ByType returns every event of type t, in the same order as Sorted.
func (tl *Timeline) ByType(t EventType) []Event {
	var out []Event
	for _, e := range tl.Sorted() {
		if e.EventType == t {
			out = append(out, e)
		}
	}
	return out
}

// Count returns the number of events in the timeline.
func (tl *Timeline) Count() int {
	tl.mu.RLock()
	defer tl.mu.RUnlock()
	return len(tl.events)
}
