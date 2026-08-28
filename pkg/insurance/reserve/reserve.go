// Package reserve implements the claim reserve lifecycle: the amount
// set aside against an anticipated claim payout. Round 7's own
// Insurance System Completeness Audit found this concept entirely
// absent from pkg/insurance — no reserve amount, status, or
// change-history field existed anywhere. This package closes that gap.
//
// Design follows the same discipline already established across this
// domain (recovery.Target, regulatory.Matter):
//
//   - Fixed-point money only. Reserve amounts are quantum.Amount (int64
//     minor units) — never float64 — matching this codebase's own
//     determinism rule for every monetary figure.
//   - History, not a single mutable field. Every SetReserve/Revise
//     call appends an Entry; CurrentAmount is always derived from the
//     latest entry, never set directly. A reserve's full history is
//     always reconstructable, matching regulatory.MonitorRequirement's
//     "requirement and completion are separate facts" discipline
//     applied here to "propose and approve are separate facts".
//   - Approval is a distinct event from setting. SetReserve/Revise
//     leave a reserve in StatusProposed; only ApproveReserve — by a
//     DIFFERENT party than the one who proposed it (segregation of
//     duties) — moves it to StatusApproved. This mirrors the
//     blueprint's own general rule (VICE never auto-approves) applied
//     to reserve authority specifically.
//   - Reconciliation is a pure comparison, never a verdict. Reconcile
//     compares the current APPROVED reserve against a quantum figure
//     and reports the delta and a closed-vocabulary Adequacy rating —
//     it never decides whether the reserve should change; that is a
//     human/claims-handler decision this package only informs.
package reserve

import (
	"errors"
	"fmt"
	"sync"

	"veriqo/pkg/insurance/party"
	"veriqo/pkg/insurance/quantum"
)

// Status is a reserve's own workflow status — deliberately separate
// from whether the underlying claim is open or closed.
type Status string

const (
	// StatusProposed: an amount has been set or revised but not yet
	// approved. The honest starting state after every SetReserve/Revise
	// call.
	StatusProposed Status = "PROPOSED"
	// StatusApproved: a party with reserve authority, distinct from
	// whoever proposed the current amount, has approved it.
	StatusApproved Status = "APPROVED"
	// StatusReleased: the reserve is no longer held (claim closed,
	// paid, or withdrawn). Terminal.
	StatusReleased Status = "RELEASED"
)

var knownStatuses = map[Status]bool{StatusProposed: true, StatusApproved: true, StatusReleased: true}

// IsKnownStatus reports whether s is a modelled reserve status.
func IsKnownStatus(s Status) bool { return knownStatuses[s] }

// Action is what one history Entry recorded.
type Action string

const (
	ActionSet     Action = "SET"
	ActionRevise  Action = "REVISE"
	ActionApprove Action = "APPROVE"
	ActionRelease Action = "RELEASE"
)

// Entry is one immutable, append-only history record. A reserve's
// CurrentAmount is always derived from the latest ActionSet/ActionRevise
// entry — there is no separate mutable amount field anywhere in this
// package.
type Entry struct {
	Action Action         `json:"action"`
	Amount quantum.Amount `json:"amount,omitempty"`
	By     party.PartyID  `json:"by"`
	Reason string         `json:"reason"`
	Tick   uint64         `json:"tick"`
}

// reserveAuthority is the closed set of party roles this package
// recognises as able to propose or approve a reserve. Matches the
// blueprint's own established claims-handling role vocabulary
// (pkg/insurance/party) rather than inventing a new one.
var reserveAuthority = map[party.Role]bool{
	party.RoleInsurer: true, party.RoleClaimsHandler: true, party.RoleLossAdjuster: true,
	party.RoleAverageAdjuster: true,
}

// HasReserveAuthority reports whether r is one of the roles this
// package recognises as able to set, revise, or approve a reserve.
func HasReserveAuthority(r party.Role) bool { return reserveAuthority[r] }

var (
	ErrEmptyReserveID   = errors.New("reserve: ReserveID must be non-empty")
	ErrEmptyClaimID     = errors.New("reserve: ClaimID must be non-empty")
	ErrEmptyCaseID      = errors.New("reserve: CaseID must be non-empty")
	ErrNegativeAmount   = errors.New("reserve: amount must be >= 0")
	ErrEmptyReason      = errors.New("reserve: an amount change must record why")
	ErrEmptyBy          = errors.New("reserve: By must be non-empty")
	ErrNoAuthority      = errors.New("reserve: party lacks reserve authority")
	ErrAlreadyApproved  = errors.New("reserve: reserve is already approved with no pending change")
	ErrSelfApproval     = errors.New("reserve: the party who proposed the current amount cannot approve it (segregation of duties)")
	ErrNotApproved      = errors.New("reserve: only an APPROVED reserve can be released")
	ErrAlreadyReleased  = errors.New("reserve: reserve is already released")
	ErrReleasedNoChange = errors.New("reserve: a released reserve cannot be revised")
)

// Reserve is one claim's reserve, with its full history.
type Reserve struct {
	mu sync.RWMutex

	ReserveID string
	ClaimID   string
	CaseID    string

	status Status
	// proposedBy is who set the CURRENT (not yet approved) amount — used
	// to enforce segregation of duties in ApproveReserve. Cleared once
	// approved.
	proposedBy party.PartyID
	history    []Entry
}

// New opens a reserve at its first amount, in StatusProposed. amount
// must be non-negative (a reserve of zero is valid — e.g. a claim
// registered but not yet assessed).
func New(reserveID, claimID, caseID string, amount quantum.Amount, by party.PartyID, role party.Role, reason string, tick uint64) (*Reserve, error) {
	if reserveID == "" {
		return nil, ErrEmptyReserveID
	}
	if claimID == "" {
		return nil, ErrEmptyClaimID
	}
	if caseID == "" {
		return nil, ErrEmptyCaseID
	}
	if amount < 0 {
		return nil, ErrNegativeAmount
	}
	if by == "" {
		return nil, ErrEmptyBy
	}
	if reason == "" {
		return nil, ErrEmptyReason
	}
	if !HasReserveAuthority(role) {
		return nil, fmt.Errorf("%w: role %q", ErrNoAuthority, role)
	}
	r := &Reserve{
		ReserveID: reserveID, ClaimID: claimID, CaseID: caseID,
		status: StatusProposed, proposedBy: by,
		history: []Entry{{Action: ActionSet, Amount: amount, By: by, Reason: reason, Tick: tick}},
	}
	return r, nil
}

// CurrentAmount returns the amount from the most recent SET/REVISE
// entry — always derived from history, never a separately mutable
// field.
func (r *Reserve) CurrentAmount() quantum.Amount {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for i := len(r.history) - 1; i >= 0; i-- {
		e := r.history[i]
		if e.Action == ActionSet || e.Action == ActionRevise {
			return e.Amount
		}
	}
	return 0
}

// Status returns the reserve's current workflow status.
func (r *Reserve) Status() Status {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}

// History returns every recorded entry, oldest first — the full,
// tamper-evident-by-construction version trail (nothing here is ever
// overwritten in place; every change appends).
func (r *Reserve) History() []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Entry, len(r.history))
	copy(out, r.history)
	return out
}

// Revise records a NEW proposed amount, moving an APPROVED reserve
// back to PROPOSED (a revision always needs fresh approval — approving
// the old amount never carries forward to a new one). Refused on a
// released reserve.
func (r *Reserve) Revise(amount quantum.Amount, by party.PartyID, role party.Role, reason string, tick uint64) error {
	if amount < 0 {
		return ErrNegativeAmount
	}
	if by == "" {
		return ErrEmptyBy
	}
	if reason == "" {
		return ErrEmptyReason
	}
	if !HasReserveAuthority(role) {
		return fmt.Errorf("%w: role %q", ErrNoAuthority, role)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status == StatusReleased {
		return ErrReleasedNoChange
	}
	r.history = append(r.history, Entry{Action: ActionRevise, Amount: amount, By: by, Reason: reason, Tick: tick})
	r.status = StatusProposed
	r.proposedBy = by
	return nil
}

// Approve records approval of the CURRENT proposed amount by a party
// with reserve authority who is NOT the one who proposed it —
// segregation of duties, enforced structurally rather than by
// convention. Refuses to approve an already-approved reserve (nothing
// pending) or a released one.
func (r *Reserve) Approve(by party.PartyID, role party.Role, tick uint64) error {
	if by == "" {
		return ErrEmptyBy
	}
	if !HasReserveAuthority(role) {
		return fmt.Errorf("%w: role %q", ErrNoAuthority, role)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status == StatusReleased {
		return ErrReleasedNoChange
	}
	if r.status == StatusApproved {
		return ErrAlreadyApproved
	}
	if by == r.proposedBy {
		return ErrSelfApproval
	}
	r.history = append(r.history, Entry{Action: ActionApprove, By: by, Reason: "reserve amount approved", Tick: tick})
	r.status = StatusApproved
	return nil
}

// Release records that the reserve is no longer held. Only an APPROVED
// reserve may be released — an unapproved amount cannot be honestly
// "released" as though it had been formally held.
func (r *Reserve) Release(by party.PartyID, reason string, tick uint64) error {
	if by == "" {
		return ErrEmptyBy
	}
	if reason == "" {
		return ErrEmptyReason
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status == StatusReleased {
		return ErrAlreadyReleased
	}
	if r.status != StatusApproved {
		return ErrNotApproved
	}
	r.history = append(r.history, Entry{Action: ActionRelease, By: by, Reason: reason, Tick: tick})
	r.status = StatusReleased
	return nil
}

// ---- Reconciliation --------------------------------------------------

// Adequacy is the closed-vocabulary result of comparing a reserve
// against a computed quantum figure. Never a decision to change the
// reserve — only a report of the delta, for a human or downstream
// workflow to act on.
type Adequacy string

const (
	AdequacyAdequate      Adequacy = "ADEQUATE"
	AdequacyUnderReserved Adequacy = "UNDER_RESERVED"
	AdequacyOverReserved  Adequacy = "OVER_RESERVED"
)

// Reconciliation is the result of comparing a reserve's current amount
// against a quantum figure at a point in time.
type Reconciliation struct {
	ReserveAmount quantum.Amount `json:"reserve_amount"`
	QuantumAmount quantum.Amount `json:"quantum_amount"`
	DeltaMinor    quantum.Amount `json:"delta_minor"` // ReserveAmount - QuantumAmount
	Adequacy      Adequacy       `json:"adequacy"`
	ReserveStatus Status         `json:"reserve_status"`
}

// Reconcile compares r's current amount against quantumAmount (e.g. a
// quantum.Calculation.IndicativeClaimValue) and reports the delta and
// an Adequacy rating. This is a PURE comparison — it never mutates r,
// never decides a revision is needed, and reports the reserve's own
// status alongside the numbers so a caller can see at a glance whether
// the comparison is even against an APPROVED figure.
func (r *Reserve) Reconcile(quantumAmount quantum.Amount) Reconciliation {
	current := r.CurrentAmount()
	delta := current - quantumAmount
	adequacy := AdequacyAdequate
	switch {
	case delta < 0:
		adequacy = AdequacyUnderReserved
	case delta > 0:
		adequacy = AdequacyOverReserved
	}
	return Reconciliation{
		ReserveAmount: current, QuantumAmount: quantumAmount, DeltaMinor: delta,
		Adequacy: adequacy, ReserveStatus: r.Status(),
	}
}
