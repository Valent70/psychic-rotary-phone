// Package salvage is VICE's Salvage Engine, closing the item the
// VERIQO Master Closure Mandate §18 names explicitly and §78 forbids
// declaring the Insurance System complete without: "damaged
// asset/cargo; salvage assessment; salvage opportunity; salvage
// contractor; salvage evidence; salvage value; disposal; proceeds;
// expenses; allocation; impact on quantum/recovery."
//
// Relationship to the rest of VICE, matching the discipline every other
// package in this domain follows (Final Design §39: no duplicate
// engine): salvage does NOT re-implement evidence, money, or party
// modelling. An Operation's evidence is a set of
// pkg/insurance/evidence.Record EvidenceIDs (the same evidence graph
// every other insurance package cites into); its monetary figures are
// quantum.EvidenceBackedAmount (the same evidence-backed, integer-
// minor-unit type pkg/insurance/quantum uses, for the identical reason:
// a salvage proceeds figure that came from nowhere cannot be
// reproduced from evidence); its contractor and other participants are
// party.PartyID values tagged with party.RoleSalvageParty. This package
// adds exactly one new thing: the salvage-specific lifecycle and the
// net-value computation that feeds back into quantum as the "salvage"
// operand — see NetValue and quantum.ComputeInput.Salvage.
//
// Golden rule, restated for this package specifically because §18's
// worked list ends in "impact on quantum/recovery": recording a
// disposal proceeds figure is a FACT about money realised, never a
// statement that the salvage contractor, the party who caused the
// damage, or anyone else is liable for anything. TestNoLiabilityOrCoverageField
// checks this by reflection over every exported type here, matching
// pkg/insurance/recovery's TestNoLiabilityDeterminationField.
package salvage

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"veriqo/pkg/insurance/party"
	"veriqo/pkg/insurance/quantum"
)

// ---- Status: the salvage operation's own workflow state ----
//
// Design reasoning, matching recovery.RecoveryStatus's own: this is a
// PROCEDURAL status about VICE's tracking of the operation, never a
// judgement about whether the salvage was necessary, well executed, or
// who caused the need for it.

type Status string

const (
	// StatusIdentified: a salvage opportunity has been flagged. Nothing
	// assessed or contracted yet.
	StatusIdentified Status = "IDENTIFIED"
	// StatusAssessed: a salvage assessment (AssessedValue) has been
	// recorded, but no contractor engaged.
	StatusAssessed Status = "ASSESSED"
	// StatusContracted: a salvage contractor (RoleSalvageParty) has been
	// engaged.
	StatusContracted Status = "CONTRACTED"
	// StatusInProgress: the salvage operation is underway.
	StatusInProgress Status = "IN_PROGRESS"
	// StatusCompleted: the physical salvage operation is complete, but
	// disposal of the salvaged asset has not yet been recorded.
	StatusCompleted Status = "COMPLETED"
	// StatusDisposed: the salvaged asset has been disposed of
	// (DisposalMethod + DisposalProceeds recorded).
	StatusDisposed Status = "DISPOSED"
	// StatusAbandoned: the operation is no longer being pursued, for any
	// reason (uneconomic, physically infeasible, superseded) — this
	// package does not need to know why, matching
	// recovery.RecoveryStatusAbandoned.
	StatusAbandoned Status = "ABANDONED"
)

var knownStatuses = map[Status]bool{
	StatusIdentified: true, StatusAssessed: true, StatusContracted: true,
	StatusInProgress: true, StatusCompleted: true, StatusDisposed: true,
	StatusAbandoned: true,
}

// IsKnownStatus reports whether s is a modelled salvage status.
func IsKnownStatus(s Status) bool { return knownStatuses[s] }

// KnownStatuses returns every modelled status in deterministic order.
func KnownStatuses() []Status {
	out := make([]Status, 0, len(knownStatuses))
	for s := range knownStatuses {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ---- DisposalMethod ----

type DisposalMethod string

const (
	DisposalSold            DisposalMethod = "SOLD"
	DisposalScrapped        DisposalMethod = "SCRAPPED"
	DisposalDestroyed       DisposalMethod = "DESTROYED"
	DisposalReturnedToOwner DisposalMethod = "RETURNED_TO_OWNER"
	DisposalDonated         DisposalMethod = "DONATED"
	DisposalOther           DisposalMethod = "OTHER"
	// DisposalNotYetDisposed is the honest zero value: disposal has not
	// happened yet. Never defaulted to DisposalSold or any other real
	// method just because a proceeds figure exists — see the golden
	// rule in the package doc.
	DisposalNotYetDisposed DisposalMethod = ""
)

var knownDisposalMethods = map[DisposalMethod]bool{
	DisposalSold: true, DisposalScrapped: true, DisposalDestroyed: true,
	DisposalReturnedToOwner: true, DisposalDonated: true, DisposalOther: true,
}

// IsKnownDisposalMethod reports whether m is a modelled disposal method.
// DisposalNotYetDisposed is deliberately NOT a known method — it is the
// absence of one.
func IsKnownDisposalMethod(m DisposalMethod) bool { return knownDisposalMethods[m] }

// KnownDisposalMethods returns every modelled disposal method in
// deterministic order.
func KnownDisposalMethods() []DisposalMethod {
	out := make([]DisposalMethod, 0, len(knownDisposalMethods))
	for m := range knownDisposalMethods {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ---- Operation: one salvage record (§18) ----

// Operation is one salvage operation over one damaged asset/cargo item
// tied to one claim. Its fields are exactly §18's own worked list:
// damaged asset/cargo, salvage assessment, salvage opportunity (via
// Status), salvage contractor, salvage evidence, salvage value,
// disposal, proceeds, expenses, allocation.
type Operation struct {
	OperationID string `json:"operation_id"`
	CaseID      string `json:"case_id"`
	// ClaimID links this salvage record to the original claim — §18's
	// own closing instruction ("Link salvage records to the original
	// claim and evidence graph").
	ClaimID string `json:"claim_id"`

	// AssetDescription names the damaged asset/cargo being salvaged, in
	// the claims officer's own words (e.g. "40ft reefer container
	// CTNR-4471 and contents"). Free text, exactly like
	// recovery.Basis.Detail — this package does not attempt a closed
	// taxonomy of every possible salvaged asset.
	AssetDescription string `json:"asset_description"`
	// EvidenceIDs are the pkg/insurance/evidence.Record EvidenceIDs that
	// evidence this salvage record — survey photos, contractor reports,
	// disposal/sale documentation. This IS the "link to the evidence
	// graph" §18 requires: no separate salvage evidence store exists.
	EvidenceIDs []string `json:"evidence_ids,omitempty"`

	Status Status `json:"status"`

	// Contractor is the party (tagged party.RoleSalvageParty in the
	// case's party.Registry) engaged to perform the salvage, if one has
	// been engaged.
	Contractor            party.PartyID `json:"contractor,omitempty"`
	ContractorEngagedTick uint64        `json:"contractor_engaged_tick,omitempty"`

	// AssessedValue is the salvage assessment's own figure: what the
	// damaged asset is estimated to be worth before disposal. Evidence-
	// backed for the same reason every quantum figure in this domain is
	// (blueprint §20 lineage discipline, extended here).
	AssessedValue quantum.EvidenceBackedAmount `json:"assessed_value"`
	// DisposalProceeds is what was ACTUALLY realised on disposal — a
	// FACT about money received, recorded only once StatusDisposed.
	DisposalProceeds quantum.EvidenceBackedAmount `json:"disposal_proceeds"`
	// Expenses is the cost of the salvage/disposal operation itself
	// (contractor fees, transport, storage, disposal fees).
	Expenses quantum.EvidenceBackedAmount `json:"expenses"`

	DisposalMethod DisposalMethod `json:"disposal_method,omitempty"`
	DisposalTick   uint64         `json:"disposal_tick,omitempty"`

	// AllocatedToClaim records whether NetValue has been folded back
	// into the claim's quantum exposure (§18 "impact on quantum") —
	// see Registry.MarkAllocated. This package never performs that fold
	// itself (that is quantum.ComputeInput.Salvage's job, fed by
	// NetValue below); it only tracks whether a caller has done so, so
	// a net salvage value cannot silently be forgotten OR double-
	// counted across a claim with several salvage operations.
	AllocatedToClaim bool `json:"allocated_to_claim"`
}

var (
	ErrEmptyOperationID               = errors.New("salvage: OperationID must be non-empty")
	ErrEmptyCaseID                    = errors.New("salvage: CaseID must be non-empty")
	ErrEmptyClaimID                   = errors.New("salvage: ClaimID must be non-empty")
	ErrEmptyAssetDesc                 = errors.New("salvage: AssetDescription must be non-empty")
	ErrUnknownStatus                  = errors.New("salvage: unknown Status")
	ErrUnknownDisposal                = errors.New("salvage: unknown DisposalMethod")
	ErrDisposedWithNoMethod           = errors.New("salvage: Status is DISPOSED but no DisposalMethod is recorded")
	ErrDisposedWithNoProceedsEvidence = errors.New(
		"salvage: Status is DISPOSED with a non-zero DisposalProceeds amount that cites no evidence")
)

// Validate reports whether o is well-formed. Deliberately does NOT
// require Contractor, AssessedValue, or disposal fields to be set for
// every status — an IDENTIFIED operation legitimately has none of
// those yet; Validate instead checks the invariants that must hold
// AT o's OWN declared Status, matching the discipline
// recovery.Target.Validate and preservation.Order.Validate use.
func (o Operation) Validate() error {
	if o.OperationID == "" {
		return ErrEmptyOperationID
	}
	if o.CaseID == "" {
		return ErrEmptyCaseID
	}
	if o.ClaimID == "" {
		return ErrEmptyClaimID
	}
	if o.AssetDescription == "" {
		return ErrEmptyAssetDesc
	}
	if !IsKnownStatus(o.Status) {
		return fmt.Errorf("%w: %q", ErrUnknownStatus, o.Status)
	}
	if o.DisposalMethod != DisposalNotYetDisposed && !IsKnownDisposalMethod(o.DisposalMethod) {
		return fmt.Errorf("%w: %q", ErrUnknownDisposal, o.DisposalMethod)
	}
	if o.Status == StatusDisposed {
		if o.DisposalMethod == DisposalNotYetDisposed {
			return ErrDisposedWithNoMethod
		}
		if o.DisposalProceeds.Amount != 0 && len(o.DisposalProceeds.EvidenceIDs) == 0 {
			return ErrDisposedWithNoProceedsEvidence
		}
	}
	return nil
}

// New constructs an Operation in its natural starting state: IDENTIFIED,
// with no contractor, assessment, or disposal recorded yet.
func New(operationID, caseID, claimID, assetDescription string) (Operation, error) {
	o := Operation{
		OperationID:      operationID,
		CaseID:           caseID,
		ClaimID:          claimID,
		AssetDescription: assetDescription,
		Status:           StatusIdentified,
	}
	if err := o.Validate(); err != nil {
		return Operation{}, err
	}
	return o, nil
}

// NetValue is the REAL computed logic behind §18's "impact on
// quantum/recovery": disposal proceeds minus salvage/disposal expenses,
// with the union of both sides' EvidenceIDs (deduplicated, sorted) —
// exactly the lineage a reviewer needs to answer "why is this number
// here", matching the blueprint §20 discipline.
//
// Returns the zero EvidenceBackedAmount, unsupported, until o has
// reached StatusDisposed: a net value computed before disposal actually
// happened would be a projection dressed up as a fact, and this package
// never does that silently (golden rule, again).
func (o Operation) NetValue() quantum.EvidenceBackedAmount {
	if o.Status != StatusDisposed {
		return quantum.EvidenceBackedAmount{}
	}
	net := o.DisposalProceeds.Amount.Sub(o.Expenses.Amount)
	seen := make(map[string]bool)
	var ids []string
	for _, group := range [][]string{o.DisposalProceeds.EvidenceIDs, o.Expenses.EvidenceIDs} {
		for _, id := range group {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	sort.Strings(ids)
	return quantum.NewEvidenceBackedAmount(net, ids...)
}

// ---- Registry: case-scoped set of salvage operations ----

var (
	ErrDuplicateOperation = errors.New("salvage: OperationID already registered")
	ErrOperationNotFound  = errors.New("salvage: OperationID not found")
	ErrCaseIDMismatch     = errors.New("salvage: Operation.CaseID does not match this registry's CaseID")
	ErrEmptyEvidenceID    = errors.New("salvage: EvidenceID must be non-empty")
	ErrEmptyContractor    = errors.New("salvage: Contractor must be non-empty")
	ErrAlreadyAllocated   = errors.New("salvage: this operation's net value has already been allocated to the claim")
	ErrNotYetDisposed     = errors.New("salvage: cannot allocate a net value before Status is DISPOSED")
)

// Registry holds every salvage Operation identified for ONE case,
// matching recovery.Registry's shape and concurrency discipline.
type Registry struct {
	mu     sync.RWMutex
	caseID string
	ops    map[string]Operation
	order  []string
}

// NewRegistry returns an empty salvage-operation registry scoped to caseID.
func NewRegistry(caseID string) (*Registry, error) {
	if caseID == "" {
		return nil, ErrEmptyCaseID
	}
	return &Registry{caseID: caseID, ops: make(map[string]Operation)}, nil
}

// CaseID returns the case this registry is scoped to.
func (r *Registry) CaseID() string { return r.caseID }

// Register adds a new, already-valid Operation to the registry.
func (r *Registry) Register(o Operation) error {
	if err := o.Validate(); err != nil {
		return err
	}
	if o.CaseID != r.caseID {
		return fmt.Errorf("%w: operation CaseID %q, registry CaseID %q", ErrCaseIDMismatch, o.CaseID, r.caseID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.ops[o.OperationID]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateOperation, o.OperationID)
	}
	r.ops[o.OperationID] = o
	r.order = append(r.order, o.OperationID)
	return nil
}

// Get returns the Operation for operationID.
func (r *Registry) Get(operationID string) (Operation, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	o, ok := r.ops[operationID]
	return o, ok
}

// All returns every registered Operation in registration order.
func (r *Registry) All() []Operation {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Operation, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.ops[id])
	}
	return out
}

// ByStatus returns every Operation currently in status, in registration order.
func (r *Registry) ByStatus(status Status) []Operation {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Operation
	for _, id := range r.order {
		if o := r.ops[id]; o.Status == status {
			out = append(out, o)
		}
	}
	return out
}

// ByClaim returns every Operation linked to claimID, in registration order.
func (r *Registry) ByClaim(claimID string) []Operation {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Operation
	for _, id := range r.order {
		if o := r.ops[id]; o.ClaimID == claimID {
			out = append(out, o)
		}
	}
	return out
}

// SetStatus updates an Operation's workflow status. Re-validates the
// WHOLE operation at the new status, so (for example) moving to
// StatusDisposed with no DisposalMethod set is refused here, not just
// on original construction.
func (r *Registry) SetStatus(operationID string, status Status) error {
	if !IsKnownStatus(status) {
		return fmt.Errorf("%w: %q", ErrUnknownStatus, status)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	o, ok := r.ops[operationID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrOperationNotFound, operationID)
	}
	o.Status = status
	if err := o.Validate(); err != nil {
		return err
	}
	r.ops[operationID] = o
	return nil
}

// EngageContractor records the salvage contractor and the tick it was
// engaged, and advances Status to CONTRACTED if the operation is not
// already further along (IN_PROGRESS, COMPLETED, DISPOSED). A contractor
// can be recorded (or corrected) at any status without forcing a
// backwards status jump.
func (r *Registry) EngageContractor(operationID string, contractor party.PartyID, tick uint64) error {
	if contractor == "" {
		return ErrEmptyContractor
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	o, ok := r.ops[operationID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrOperationNotFound, operationID)
	}
	o.Contractor = contractor
	o.ContractorEngagedTick = tick
	if o.Status == StatusIdentified || o.Status == StatusAssessed {
		o.Status = StatusContracted
	}
	r.ops[operationID] = o
	return nil
}

// RecordAssessment sets AssessedValue and advances Status to ASSESSED if
// the operation has not progressed further.
func (r *Registry) RecordAssessment(operationID string, value quantum.EvidenceBackedAmount) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	o, ok := r.ops[operationID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrOperationNotFound, operationID)
	}
	o.AssessedValue = value
	if o.Status == StatusIdentified {
		o.Status = StatusAssessed
	}
	r.ops[operationID] = o
	return nil
}

// RecordDisposal sets DisposalProceeds, Expenses, DisposalMethod and
// DisposalTick, and moves Status to DISPOSED. Refuses (leaving the
// registry unchanged) if the resulting Operation would fail Validate —
// e.g. proceeds cited with no evidence.
func (r *Registry) RecordDisposal(operationID string, method DisposalMethod, proceeds, expenses quantum.EvidenceBackedAmount, tick uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	o, ok := r.ops[operationID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrOperationNotFound, operationID)
	}
	next := o
	next.DisposalMethod = method
	next.DisposalProceeds = proceeds
	next.Expenses = expenses
	next.DisposalTick = tick
	next.Status = StatusDisposed
	if err := next.Validate(); err != nil {
		return err
	}
	r.ops[operationID] = next
	return nil
}

// AddEvidence appends an evidence.Record EvidenceID to an Operation's
// EvidenceIDs — the salvage record's own link into the evidence graph.
func (r *Registry) AddEvidence(operationID, evidenceID string) error {
	if evidenceID == "" {
		return ErrEmptyEvidenceID
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	o, ok := r.ops[operationID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrOperationNotFound, operationID)
	}
	o.EvidenceIDs = append(o.EvidenceIDs, evidenceID)
	r.ops[operationID] = o
	return nil
}

// MarkAllocated records that operationID's NetValue has been folded
// into the claim's quantum exposure (as quantum.ComputeInput.Salvage),
// and refuses to mark it twice — the mechanism that prevents a salvage
// operation's net value from being double-counted across repeated
// quantum recomputation. Refuses an operation that has not reached
// StatusDisposed: there is no net value to allocate before disposal.
func (r *Registry) MarkAllocated(operationID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	o, ok := r.ops[operationID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrOperationNotFound, operationID)
	}
	if o.Status != StatusDisposed {
		return ErrNotYetDisposed
	}
	if o.AllocatedToClaim {
		return fmt.Errorf("%w: %s", ErrAlreadyAllocated, operationID)
	}
	o.AllocatedToClaim = true
	r.ops[operationID] = o
	return nil
}

// TotalNetValueForClaim sums NetValue() across every DISPOSED operation
// linked to claimID that has NOT yet been allocated (AllocatedToClaim
// == false) — the figure a caller feeds into
// quantum.ComputeInput.Salvage exactly once per operation. Also returns
// the union of every summed operation's evidence IDs, sorted, so the
// resulting quantum.EvidenceBackedAmount carries real lineage rather
// than an empty evidence list.
func (r *Registry) TotalNetValueForClaim(claimID string) quantum.EvidenceBackedAmount {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var total quantum.Amount
	seen := make(map[string]bool)
	var ids []string
	for _, id := range r.order {
		o := r.ops[id]
		if o.ClaimID != claimID || o.Status != StatusDisposed || o.AllocatedToClaim {
			continue
		}
		nv := o.NetValue()
		total = total.Add(nv.Amount)
		for _, eid := range nv.EvidenceIDs {
			if !seen[eid] {
				seen[eid] = true
				ids = append(ids, eid)
			}
		}
	}
	sort.Strings(ids)
	return quantum.NewEvidenceBackedAmount(total, ids...)
}

// Count returns the number of operations in the registry.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.ops)
}
