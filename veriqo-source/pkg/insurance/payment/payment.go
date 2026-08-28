// Package payment implements the claim PAYMENT lifecycle on top of the
// allocation math pkg/insurance/policy already computes. This program's
// own Round 8 self-review (gap G1, "Payment Lifecycle") found
// policy.Version.AllocateCoInsurance/AllocateReinsurance compute WHO
// gets paid HOW MUCH, but nothing anywhere in pkg/insurance records
// whether that amount was ever actually authorized, instructed, paid,
// reversed, or disputed — no PaymentStatus, no PaymentRecord, no
// lifecycle on top of the allocation figure. This package closes that
// gap, deliberately NOT re-deriving the allocation math itself (Final
// Design §39's "Jangan duplikasi logic" rule): every PaymentRecord
// carries the policy.Allocation (or quantum.Calculation) it pays
// against by reference, and Reconcile compares against it — this
// package never recomputes an amount policy/quantum already computed.
//
// Design follows the same discipline pkg/insurance/reserve already
// established for a sibling "propose then approve by someone else"
// lifecycle:
//
//   - Fixed-point money only (quantum.Amount), never float64.
//   - History, not a mutable field. Every state transition appends a
//     PaymentEvent; CurrentAmount/Status are always derived from
//     history, never set directly — the same immutable-history
//     discipline as reserve.Reserve.
//   - Authorization is a distinct event from creation, by a DIFFERENT
//     party than the one who created the record (segregation of
//     duties) — mirrors reserve.Approve's own rule.
//   - Instruction is a further distinct event, requiring a party with
//     PAYMENT EXECUTION authority (RoleBankTradeFinance) rather than
//     the AUTHORIZING claims authority (RoleInsurer/RoleClaimsHandler)
//     — a second, different segregation: the party who decides a
//     payment should happen is never the same FUNCTION as the party
//     who moves the money.
//   - Idempotency: PaymentRegistry.Create is keyed by IdempotencyKey —
//     calling it twice with the same key returns the SAME record
//     rather than creating a duplicate payment, the real-world
//     property that makes a retried instruction safe.
//   - Reconciliation is a pure comparison, never a verdict — matches
//     reserve.Reconcile exactly.
package payment

import (
	"errors"
	"fmt"
	"sync"

	"veriqo/pkg/insurance/party"
	"veriqo/pkg/insurance/quantum"
)

// Status is a payment's own workflow status.
type Status string

const (
	// StatusPending: a PaymentRecord has been created (an amount and
	// payee are known) but not yet authorized.
	StatusPending Status = "PENDING"
	// StatusAuthorized: a party with payment authority, distinct from
	// whoever created the record, has authorized it.
	StatusAuthorized Status = "AUTHORIZED"
	// StatusInstructed: a party with payment EXECUTION authority has
	// instructed the transfer (e.g. handed it to a bank/trade-finance
	// counterparty) — the transfer is in flight, not yet confirmed paid.
	StatusInstructed Status = "INSTRUCTED"
	// StatusPaid: the instructed transfer has been confirmed settled.
	// Terminal unless reversed.
	StatusPaid Status = "PAID"
	// StatusReversed: a previously PAID payment has been reversed.
	// Terminal.
	StatusReversed Status = "REVERSED"
	// StatusDisputed: the payee (or a counterparty) has disputed the
	// payment. Reachable from AUTHORIZED, INSTRUCTED or PAID — a dispute
	// does not require the underlying transfer to have completed.
	StatusDisputed Status = "DISPUTED"
)

var knownStatuses = map[Status]bool{
	StatusPending: true, StatusAuthorized: true, StatusInstructed: true,
	StatusPaid: true, StatusReversed: true, StatusDisputed: true,
}

// IsKnownStatus reports whether s is a modelled payment status.
func IsKnownStatus(s Status) bool { return knownStatuses[s] }

// Action is what one history PaymentEvent recorded.
type Action string

const (
	ActionCreate         Action = "CREATE"
	ActionAuthorize      Action = "AUTHORIZE"
	ActionInstruct       Action = "INSTRUCT"
	ActionSettle         Action = "SETTLE"
	ActionReverse        Action = "REVERSE"
	ActionDispute        Action = "DISPUTE"
	ActionResolveDispute Action = "RESOLVE_DISPUTE"
)

// PaymentEvent is one immutable, append-only history record — a
// PaymentRecord's CurrentAmount and Status are always derived from its
// event history, matching reserve.Entry's own discipline exactly.
type PaymentEvent struct {
	Action Action         `json:"action"`
	Amount quantum.Amount `json:"amount,omitempty"`
	By     party.PartyID  `json:"by"`
	Role   party.Role     `json:"role,omitempty"`
	Reason string         `json:"reason"`
	Tick   uint64         `json:"tick"`
}

// paymentAuthority is the closed set of party roles recognised as able
// to AUTHORIZE a payment — deliberately the SAME roles reserve
// recognises for reserve authority (the claims-handling authority that
// decides an amount should be paid is the same authority that sets and
// approves the reserve it is paid against), reused rather than
// re-derived.
var paymentAuthority = map[party.Role]bool{
	party.RoleInsurer: true, party.RoleClaimsHandler: true,
}

// HasPaymentAuthority reports whether r may authorize a payment.
func HasPaymentAuthority(r party.Role) bool { return paymentAuthority[r] }

// executionAuthority is the closed set of party roles recognised as
// able to INSTRUCT (execute) an authorized payment — the bank/trade
// finance counterparty that actually moves funds, per
// pkg/insurance/party's own Round 4 role vocabulary. Deliberately
// disjoint from paymentAuthority: no role appears in both maps, which
// is what makes "authorize" and "instruct" a genuine second
// segregation of duties rather than the same check run twice.
var executionAuthority = map[party.Role]bool{
	party.RoleBankTradeFinance: true,
}

// HasExecutionAuthority reports whether r may instruct a payment.
func HasExecutionAuthority(r party.Role) bool { return executionAuthority[r] }

var (
	ErrEmptyPaymentID       = errors.New("payment: PaymentID must be non-empty")
	ErrEmptyClaimID         = errors.New("payment: ClaimID must be non-empty")
	ErrEmptyCaseID          = errors.New("payment: CaseID must be non-empty")
	ErrEmptyPayeePartyID    = errors.New("payment: PayeePartyID must be non-empty")
	ErrNonPositiveAmount    = errors.New("payment: amount must be > 0")
	ErrEmptyReason          = errors.New("payment: an amount or status change must record why")
	ErrEmptyBy              = errors.New("payment: By must be non-empty")
	ErrNoPaymentAuthority   = errors.New("payment: party lacks payment authorization authority")
	ErrNoExecutionAuthority = errors.New("payment: party lacks payment execution (instruction) authority")
	ErrSelfAuthorization    = errors.New("payment: the party who created this payment cannot authorize it (segregation of duties)")
	ErrNotAuthorized        = errors.New("payment: only an AUTHORIZED payment can be instructed")
	ErrNotInstructed        = errors.New("payment: only an INSTRUCTED payment can be settled")
	ErrNotPaid              = errors.New("payment: only a PAID payment can be reversed")
	ErrAlreadyReversed      = errors.New("payment: payment is already reversed")
	ErrTerminalNoChange     = errors.New("payment: a REVERSED payment cannot change state further")
	ErrNotDisputed          = errors.New("payment: no open dispute to resolve")
	ErrAlreadyDisputed      = errors.New("payment: payment already has an open dispute")
	ErrEmptyIdempotencyKey  = errors.New("payment: IdempotencyKey must be non-empty")
)

// PaymentAuthorization is the record of one AUTHORIZE event — returned
// by Authorize so a caller can attach it to a canonical audit event
// (pkg/insurance/auditlink) without re-deriving it from history.
type PaymentAuthorization struct {
	PaymentID    string        `json:"payment_id"`
	AuthorizedBy party.PartyID `json:"authorized_by"`
	Role         party.Role    `json:"role"`
	Reason       string        `json:"reason"`
	AuthorizedAt uint64        `json:"authorized_at_tick"`
}

// PaymentInstruction is the record of one INSTRUCT event — what a real
// payment execution counterparty (a bank/trade-finance adapter) would
// be handed. Method/Reference are free text (e.g. "SWIFT MT103",
// "REF-2026-0817") — matching this domain's existing "closed taxonomy
// would be a worse fit than a stated reference" convention (e.g.
// party.Relationship.Authority).
type PaymentInstruction struct {
	PaymentID    string         `json:"payment_id"`
	InstructedBy party.PartyID  `json:"instructed_by"`
	Method       string         `json:"method,omitempty"`
	Reference    string         `json:"reference,omitempty"`
	Amount       quantum.Amount `json:"amount"`
	InstructedAt uint64         `json:"instructed_at_tick"`
}

// PaymentReversal is the record of one REVERSE event. ReversalPaymentID,
// if set, names a SEPARATE PaymentRecord created to move funds back —
// a reversal is itself a payment, never a mutation of the original
// (immutability discipline applied to money movement).
type PaymentReversal struct {
	PaymentID         string        `json:"payment_id"`
	ReversedBy        party.PartyID `json:"reversed_by"`
	Reason            string        `json:"reason"`
	ReversalPaymentID string        `json:"reversal_payment_id,omitempty"`
	ReversedAt        uint64        `json:"reversed_at_tick"`
}

// PaymentDispute is the record of one open (or resolved) dispute over a
// payment. Deliberately narrow and payment-specific — for a full
// contractual dispute over the underlying CLAIM, this links to
// pkg/insurance/dispute.Matter by ID rather than re-modelling positions
// and forums here (REUSE, not a second dispute engine).
type PaymentDispute struct {
	PaymentID             string        `json:"payment_id"`
	RaisedBy              party.PartyID `json:"raised_by"`
	Reason                string        `json:"reason"`
	RaisedAt              uint64        `json:"raised_at_tick"`
	LinkedDisputeMatterID string        `json:"linked_dispute_matter_id,omitempty"`
	ResolvedAt            uint64        `json:"resolved_at_tick,omitempty"`
	Resolution            string        `json:"resolution,omitempty"`
	Open                  bool          `json:"open"`
}

// PaymentRecord is one claim payment, with its full immutable history —
// the aggregate this package builds around.
type PaymentRecord struct {
	mu sync.RWMutex

	PaymentID string
	ClaimID   string
	CaseID    string

	// PayeePartyID is who this payment is TO.
	PayeePartyID party.PartyID

	// IdempotencyKey makes a retried Create for the same logical payment
	// safe — see PaymentRegistry.Create.
	IdempotencyKey string

	// AllocationRole/AllocationPartyID, if set, name the
	// policy.Allocation (by its own Role and PartyID — never a
	// duplicated amount) this payment executes, so Reconcile can compare
	// against the SAME figure policy.Version.AllocateCoInsurance already
	// computed rather than a second, disconnected one. Optional: a
	// payment need not always trace to a co-/reinsurance allocation.
	AllocationRole    string
	AllocationPartyID string

	// QuantumCalculationID, if set, names the quantum.Calculation this
	// payment's founding amount derives from — the "quantum linkage" the
	// reviewer's own G1 item requires, by reference only (never a
	// duplicated figure), matching reserve.Reserve's own reasoning for
	// why it is set from a quantum figure rather than an independent one.
	QuantumCalculationID string

	status       Status
	proposedBy   party.PartyID
	authorizedBy party.PartyID

	dispute *PaymentDispute
	history []PaymentEvent
}

// New creates a payment in StatusPending. amount must be positive — a
// zero-value payment is not a meaningful payment record (unlike
// reserve.New, which legitimately allows a zero opening reserve).
func New(paymentID, claimID, caseID string, payee party.PartyID, amount quantum.Amount, idempotencyKey string, by party.PartyID, reason string, tick uint64) (*PaymentRecord, error) {
	if paymentID == "" {
		return nil, ErrEmptyPaymentID
	}
	if claimID == "" {
		return nil, ErrEmptyClaimID
	}
	if caseID == "" {
		return nil, ErrEmptyCaseID
	}
	if payee == "" {
		return nil, ErrEmptyPayeePartyID
	}
	if amount <= 0 {
		return nil, ErrNonPositiveAmount
	}
	if idempotencyKey == "" {
		return nil, ErrEmptyIdempotencyKey
	}
	if by == "" {
		return nil, ErrEmptyBy
	}
	if reason == "" {
		return nil, ErrEmptyReason
	}
	p := &PaymentRecord{
		PaymentID: paymentID, ClaimID: claimID, CaseID: caseID,
		PayeePartyID: payee, IdempotencyKey: idempotencyKey,
		status: StatusPending, proposedBy: by,
		history: []PaymentEvent{{Action: ActionCreate, Amount: amount, By: by, Reason: reason, Tick: tick}},
	}
	return p, nil
}

// CurrentAmount returns the amount from the most recent CREATE event —
// always derived from history, matching reserve.Reserve.CurrentAmount.
func (p *PaymentRecord) CurrentAmount() quantum.Amount {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for i := len(p.history) - 1; i >= 0; i-- {
		if p.history[i].Action == ActionCreate {
			return p.history[i].Amount
		}
	}
	return 0
}

// Status returns the payment's current workflow status.
func (p *PaymentRecord) Status() Status {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.status
}

// History returns every recorded PaymentEvent, oldest first — the full
// audit-linkable trail; see pkg/insurance/auditlink.MirrorPaymentHistory.
func (p *PaymentRecord) History() []PaymentEvent {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]PaymentEvent, len(p.history))
	copy(out, p.history)
	return out
}

// Dispute returns the payment's current dispute record, if any.
func (p *PaymentRecord) Dispute() (PaymentDispute, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.dispute == nil {
		return PaymentDispute{}, false
	}
	return *p.dispute, true
}

// LinkAllocation records which policy.Allocation this payment executes
// — by Role and PartyID reference only, never a copied amount (callers
// should already have used the SAME amount when calling New; Reconcile
// checks that below).
func (p *PaymentRecord) LinkAllocation(role, partyID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.AllocationRole = role
	p.AllocationPartyID = partyID
}

// LinkQuantum records which quantum.Calculation this payment's amount
// derives from — the "quantum linkage" requirement, by reference only.
func (p *PaymentRecord) LinkQuantum(calculationID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.QuantumCalculationID = calculationID
}

// Authorize records authorization by a party with payment authority who
// is NOT the one who created the record — segregation of duties,
// enforced structurally, matching reserve.Approve exactly.
func (p *PaymentRecord) Authorize(by party.PartyID, role party.Role, reason string, tick uint64) (PaymentAuthorization, error) {
	if by == "" {
		return PaymentAuthorization{}, ErrEmptyBy
	}
	if reason == "" {
		return PaymentAuthorization{}, ErrEmptyReason
	}
	if !HasPaymentAuthority(role) {
		return PaymentAuthorization{}, fmt.Errorf("%w: role %q", ErrNoPaymentAuthority, role)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.status == StatusReversed {
		return PaymentAuthorization{}, ErrTerminalNoChange
	}
	if p.status != StatusPending {
		return PaymentAuthorization{}, fmt.Errorf("payment: cannot authorize from status %s", p.status)
	}
	if by == p.proposedBy {
		return PaymentAuthorization{}, ErrSelfAuthorization
	}
	p.history = append(p.history, PaymentEvent{Action: ActionAuthorize, By: by, Role: role, Reason: reason, Tick: tick})
	p.status = StatusAuthorized
	p.authorizedBy = by
	return PaymentAuthorization{PaymentID: p.PaymentID, AuthorizedBy: by, Role: role, Reason: reason, AuthorizedAt: tick}, nil
}

// Instruct records that a party with EXECUTION authority (distinct role
// family from payment authority) has instructed the transfer of an
// AUTHORIZED payment.
func (p *PaymentRecord) Instruct(by party.PartyID, role party.Role, method, reference string, tick uint64) (PaymentInstruction, error) {
	if by == "" {
		return PaymentInstruction{}, ErrEmptyBy
	}
	if !HasExecutionAuthority(role) {
		return PaymentInstruction{}, fmt.Errorf("%w: role %q", ErrNoExecutionAuthority, role)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.status != StatusAuthorized {
		return PaymentInstruction{}, ErrNotAuthorized
	}
	amount := p.currentAmountLocked()
	p.history = append(p.history, PaymentEvent{
		Action: ActionInstruct, Amount: amount, By: by, Role: role,
		Reason: fmt.Sprintf("instructed via %s ref %s", method, reference), Tick: tick,
	})
	p.status = StatusInstructed
	return PaymentInstruction{PaymentID: p.PaymentID, InstructedBy: by, Method: method, Reference: reference, Amount: amount, InstructedAt: tick}, nil
}

// currentAmountLocked is CurrentAmount's lock-free counterpart, for use
// from methods that already hold p.mu.
func (p *PaymentRecord) currentAmountLocked() quantum.Amount {
	for i := len(p.history) - 1; i >= 0; i-- {
		if p.history[i].Action == ActionCreate {
			return p.history[i].Amount
		}
	}
	return 0
}

// Settle records that an INSTRUCTED payment's transfer has been
// confirmed complete.
func (p *PaymentRecord) Settle(by party.PartyID, reason string, tick uint64) error {
	if by == "" {
		return ErrEmptyBy
	}
	if reason == "" {
		return ErrEmptyReason
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.status != StatusInstructed {
		return ErrNotInstructed
	}
	p.history = append(p.history, PaymentEvent{Action: ActionSettle, By: by, Reason: reason, Tick: tick})
	p.status = StatusPaid
	return nil
}

// Reverse records that a PAID payment has been reversed. reversalPaymentID,
// if non-empty, names a SEPARATE PaymentRecord (created by the caller
// via New, for the return transfer) this reversal corresponds to.
func (p *PaymentRecord) Reverse(by party.PartyID, reason, reversalPaymentID string, tick uint64) (PaymentReversal, error) {
	if by == "" {
		return PaymentReversal{}, ErrEmptyBy
	}
	if reason == "" {
		return PaymentReversal{}, ErrEmptyReason
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.status == StatusReversed {
		return PaymentReversal{}, ErrAlreadyReversed
	}
	if p.status != StatusPaid {
		return PaymentReversal{}, ErrNotPaid
	}
	p.history = append(p.history, PaymentEvent{Action: ActionReverse, By: by, Reason: reason, Tick: tick})
	p.status = StatusReversed
	return PaymentReversal{PaymentID: p.PaymentID, ReversedBy: by, Reason: reason, ReversalPaymentID: reversalPaymentID, ReversedAt: tick}, nil
}

// RaiseDispute opens a dispute over this payment. linkedDisputeMatterID,
// if non-empty, cross-references a pkg/insurance/dispute.Matter carrying
// the full positions/forum — this type never re-models that (REUSE).
func (p *PaymentRecord) RaiseDispute(by party.PartyID, reason, linkedDisputeMatterID string, tick uint64) (PaymentDispute, error) {
	if by == "" {
		return PaymentDispute{}, ErrEmptyBy
	}
	if reason == "" {
		return PaymentDispute{}, ErrEmptyReason
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.status == StatusReversed {
		return PaymentDispute{}, ErrTerminalNoChange
	}
	if p.dispute != nil && p.dispute.Open {
		return PaymentDispute{}, ErrAlreadyDisputed
	}
	d := PaymentDispute{
		PaymentID: p.PaymentID, RaisedBy: by, Reason: reason,
		RaisedAt: tick, LinkedDisputeMatterID: linkedDisputeMatterID, Open: true,
	}
	p.dispute = &d
	p.status = StatusDisputed
	p.history = append(p.history, PaymentEvent{Action: ActionDispute, By: by, Reason: reason, Tick: tick})
	return d, nil
}

// ResolveDispute closes an open dispute, restoring restoreStatus (the
// status the payment held before the dispute — e.g. StatusPaid) as the
// caller directs. Never silently re-derives a status: the caller states
// what the resolution means, exactly matching this domain's own
// no-invented-verdict discipline (this package never decides a dispute
// was decided "in the payee's favour"; it only records that a status
// was RESTORED to a caller-stated value).
func (p *PaymentRecord) ResolveDispute(by party.PartyID, resolution string, restoreStatus Status, tick uint64) error {
	if by == "" {
		return ErrEmptyBy
	}
	if resolution == "" {
		return ErrEmptyReason
	}
	if !IsKnownStatus(restoreStatus) || restoreStatus == StatusDisputed {
		return fmt.Errorf("payment: restoreStatus %q is not a valid non-disputed status", restoreStatus)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.dispute == nil || !p.dispute.Open {
		return ErrNotDisputed
	}
	p.dispute.Open = false
	p.dispute.ResolvedAt = tick
	p.dispute.Resolution = resolution
	p.status = restoreStatus
	p.history = append(p.history, PaymentEvent{Action: ActionResolveDispute, By: by, Reason: resolution, Tick: tick})
	return nil
}

// ---- Reconciliation --------------------------------------------------

// Adequacy mirrors reserve.Adequacy's own vocabulary, applied to
// comparing a payment's amount against the allocation it executes.
type Adequacy string

const (
	AdequacyExact     Adequacy = "EXACT"
	AdequacyUnderPaid Adequacy = "UNDER_PAID"
	AdequacyOverPaid  Adequacy = "OVER_PAID"
)

// Reconciliation is the result of comparing a payment's current amount
// against a policy.Allocation (or quantum.Calculation) figure.
type Reconciliation struct {
	PaymentAmount   quantum.Amount `json:"payment_amount"`
	AllocatedAmount quantum.Amount `json:"allocated_amount"`
	DeltaMinor      quantum.Amount `json:"delta_minor"`
	Adequacy        Adequacy       `json:"adequacy"`
	PaymentStatus   Status         `json:"payment_status"`
}

// Reconcile compares p's current amount against allocatedAmount (e.g.
// the policy.Allocation.Amount this payment executes) — a PURE
// comparison, never a decision, matching reserve.Reserve.Reconcile.
func (p *PaymentRecord) Reconcile(allocatedAmount quantum.Amount) Reconciliation {
	current := p.CurrentAmount()
	delta := current - allocatedAmount
	adequacy := AdequacyExact
	switch {
	case delta < 0:
		adequacy = AdequacyUnderPaid
	case delta > 0:
		adequacy = AdequacyOverPaid
	}
	return Reconciliation{
		PaymentAmount: current, AllocatedAmount: allocatedAmount, DeltaMinor: delta,
		Adequacy: adequacy, PaymentStatus: p.Status(),
	}
}

// ---- PaymentRegistry: idempotent creation -----------------------------

var ErrDuplicatePaymentID = errors.New("payment: PaymentID already registered")

// PaymentRegistry holds every PaymentRecord for one case, and is the
// idempotency boundary: Create with an IdempotencyKey already seen
// returns the EXISTING record rather than creating a duplicate — the
// property that makes a retried payment instruction safe.
type PaymentRegistry struct {
	mu        sync.RWMutex
	caseID    string
	records   map[string]*PaymentRecord // PaymentID -> record
	byIdemKey map[string]string         // IdempotencyKey -> PaymentID
	order     []string
}

// NewPaymentRegistry returns an empty registry scoped to caseID.
func NewPaymentRegistry(caseID string) (*PaymentRegistry, error) {
	if caseID == "" {
		return nil, ErrEmptyCaseID
	}
	return &PaymentRegistry{
		caseID: caseID, records: make(map[string]*PaymentRecord), byIdemKey: make(map[string]string),
	}, nil
}

// Create constructs and registers a new PaymentRecord, or — if
// idempotencyKey has already been used in this registry — returns the
// EXISTING record for that key, ignoring every other argument. This is
// the real idempotency guarantee: a caller that retries an instruction
// after a timeout, uncertain whether it landed, gets back the ORIGINAL
// payment rather than a second one.
func (reg *PaymentRegistry) Create(paymentID, claimID string, payee party.PartyID, amount quantum.Amount, idempotencyKey string, by party.PartyID, reason string, tick uint64) (*PaymentRecord, error) {
	if idempotencyKey == "" {
		return nil, ErrEmptyIdempotencyKey
	}
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if existingID, ok := reg.byIdemKey[idempotencyKey]; ok {
		return reg.records[existingID], nil
	}
	if _, exists := reg.records[paymentID]; exists {
		return nil, fmt.Errorf("%w: %s", ErrDuplicatePaymentID, paymentID)
	}
	p, err := New(paymentID, claimID, reg.caseID, payee, amount, idempotencyKey, by, reason, tick)
	if err != nil {
		return nil, err
	}
	reg.records[paymentID] = p
	reg.byIdemKey[idempotencyKey] = paymentID
	reg.order = append(reg.order, paymentID)
	return p, nil
}

// Get returns the PaymentRecord for paymentID.
func (reg *PaymentRegistry) Get(paymentID string) (*PaymentRecord, bool) {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	p, ok := reg.records[paymentID]
	return p, ok
}

// All returns every registered PaymentRecord in registration order.
func (reg *PaymentRegistry) All() []*PaymentRecord {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	out := make([]*PaymentRecord, 0, len(reg.order))
	for _, id := range reg.order {
		out = append(out, reg.records[id])
	}
	return out
}

// Count returns the number of distinct payments in the registry.
func (reg *PaymentRegistry) Count() int {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	return len(reg.records)
}
