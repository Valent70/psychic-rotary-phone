// Package datarights is the rights-and-provenance registry's status
// vocabulary: what a data source's legal rights currently allow, and a
// checked state machine for how that status may legitimately change
// over time.
//
// The one invariant this package exists to make structurally impossible
// to violate: a source cannot be treated as QUALIFIED (or VERIFIED)
// evidence just because a contract exists (CONTRACT_PENDING) or was
// signed (CONTRACTED). Real evidence -- a pilot actually run, evidence
// actually submitted and actually validated -- must happen first, in
// order, every time. Advance (and Record.Advance) enforce this on every
// transition, not just at the point a status is first assigned.
package datarights

import (
	"errors"
	"fmt"
)

// RightsStatus is a data source's position in the rights-acquisition
// lifecycle, from first public documentation of a capability through a
// fully verified, production-usable evidence pipeline.
type RightsStatus string

const (
	PubliclyDocumented  RightsStatus = "PUBLICLY_DOCUMENTED"
	CapabilityConfirmed RightsStatus = "CAPABILITY_CONFIRMED"
	AccessPending       RightsStatus = "ACCESS_PENDING"
	ContractPending     RightsStatus = "CONTRACT_PENDING"
	Contracted          RightsStatus = "CONTRACTED"
	PilotExecuted       RightsStatus = "PILOT_EXECUTED"
	EvidenceSubmitted   RightsStatus = "EVIDENCE_SUBMITTED"
	EvidenceValidated   RightsStatus = "EVIDENCE_VALIDATED"
	Qualified           RightsStatus = "QUALIFIED"
	Verified            RightsStatus = "VERIFIED"
)

// statusOrder is the full legal forward progression. Its index IS the
// rank every transition check is computed from -- there is exactly one
// place in this package that defines "what comes after what."
var statusOrder = []RightsStatus{
	PubliclyDocumented, CapabilityConfirmed, AccessPending, ContractPending,
	Contracted, PilotExecuted, EvidenceSubmitted, EvidenceValidated,
	Qualified, Verified,
}

var statusRank = func() map[RightsStatus]int {
	m := make(map[RightsStatus]int, len(statusOrder))
	for i, s := range statusOrder {
		m[s] = i
	}
	return m
}()

// Errors.
var (
	ErrUnknownStatus     = errors.New("datarights: unknown rights status")
	ErrIllegalTransition = errors.New("datarights: illegal rights-status transition")
)

// DataRights is the concrete set of things a source's current rights
// status actually permits doing with its data. Two records at the same
// RightsStatus can still carry different DataRights -- status describes
// how the rights were established; DataRights describes what they cover.
type DataRights struct {
	ReceiveRawData    bool
	StoreRawData      bool
	RetainHash        bool
	CreateDerivedData bool
	CustomerDisplay   bool
	CustomerExport    bool
	DisputeUse        bool
	CalibrationUse    bool
	TrainingUse       bool
	AuditUse          bool
}

// Advance validates a transition from current to target without
// mutating anything -- Record.Advance below is the stateful wrapper
// most callers want.
//
// Forward moves must proceed exactly one step at a time through
// statusOrder: skipping any intermediate status is rejected, which as a
// direct consequence makes it impossible to reach QUALIFIED or VERIFIED
// from CONTRACT_PENDING (or anything earlier) without legitimately
// passing through CONTRACTED, PILOT_EXECUTED, EVIDENCE_SUBMITTED and
// EVIDENCE_VALIDATED first.
//
// Backward moves (downgrade / revocation) are always permitted, to any
// earlier status, in a single call. This is a deliberate design choice:
// a real registry must be able to un-qualify a source whose rights
// lapsed -- a contract expiring, a license being revoked, a legal hold
// -- and the reason for a downgrade can invalidate several stages of
// progress at once (e.g. a revoked license drops a VERIFIED source
// straight back to ACCESS_PENDING, not step by step through QUALIFIED,
// EVIDENCE_VALIDATED, ... in reverse). Re-earning QUALIFIED/VERIFIED
// status after a downgrade is subject to exactly the same forward-path
// rule as the first time: no shortcut is ever granted for having held a
// higher status before.
func Advance(current, target RightsStatus) error {
	cr, ok := statusRank[current]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownStatus, current)
	}
	tr, ok := statusRank[target]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownStatus, target)
	}
	if tr <= cr {
		return nil // downgrade, or a no-op re-assertion of the same status
	}
	if tr != cr+1 {
		return fmt.Errorf("%w: %s -> %s skips required intermediate status(es)", ErrIllegalTransition, current, target)
	}
	return nil
}

// IsDowngrade reports whether target is strictly earlier than current
// in the ordered progression -- true for a revocation/downgrade, false
// for a forward advance or a no-op re-assertion of the same status.
func IsDowngrade(current, target RightsStatus) bool {
	cr, ok1 := statusRank[current]
	tr, ok2 := statusRank[target]
	return ok1 && ok2 && tr < cr
}

// Record binds one source/subject ID to its current rights status and
// the concrete DataRights that status has earned, plus a full audit
// trail of every status it has ever held (advances and downgrades
// alike).
type Record struct {
	SourceID string
	Status   RightsStatus
	Rights   DataRights
	History  []RightsStatus
}

// NewRecord starts a source at PUBLICLY_DOCUMENTED, the bottom of the
// progression -- every source's rights acquisition starts from nothing
// more than "we know this capability publicly exists."
func NewRecord(sourceID string) *Record {
	return &Record{SourceID: sourceID, Status: PubliclyDocumented, History: []RightsStatus{PubliclyDocumented}}
}

// Advance moves this record to target, enforcing the same rule as the
// package-level Advance function, and appends target to History on
// success. The record is left unchanged if the transition is rejected.
func (r *Record) Advance(target RightsStatus) error {
	if err := Advance(r.Status, target); err != nil {
		return fmt.Errorf("datarights: source %s: %w", r.SourceID, err)
	}
	r.Status = target
	r.History = append(r.History, target)
	return nil
}

// AtLeast reports whether this record's current status is at or beyond
// threshold in the ordered progression -- the check a caller wanting to
// gate a capability on "has this source at least reached QUALIFIED"
// should use, rather than an exact equality comparison.
func (r *Record) AtLeast(threshold RightsStatus) bool {
	cr, ok1 := statusRank[r.Status]
	tr, ok2 := statusRank[threshold]
	return ok1 && ok2 && cr >= tr
}
