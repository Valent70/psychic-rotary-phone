// Package recovery is VICE's Subrogation / Recovery Engine — blueprint
// §23. After a claim is analysed, VICE identifies which third parties
// might bear responsibility for the loss (so the insurer/insured may
// pursue recovery from them) and tracks each such avenue as a Target.
//
// The blueprint's own worked list of potential responsible parties
// (§23): Carrier, Terminal, Warehouse, Forwarder, Manufacturer,
// Surveyor, Other third party. See PotentialResponsiblePartyCategories.
//
// The blueprint's own required per-target fields (§23), verbatim:
//
//	party, basis, supporting_evidence, potential_loss,
//	notice_status, limitation_status, recovery_status
//
// Golden rule this package exists under, quoted directly from the
// blueprint's absolute principles (§0): "VICE MUST NOT: ... determine
// legal liability finally". A recovery Target NEVER states that the
// named party IS responsible for the loss — it only records that VICE
// has IDENTIFIED a potential recovery avenue, on what legal/factual
// theory it rests, what evidence currently supports pursuing it, and
// the avenue's own procedural state (has notice been given? is the
// limitation period still open? is the case for pursuing it still
// live?). Nowhere in this package is there a field that could be read
// as "liability: true/confirmed" — see TestNoLiabilityDeterminationField
// in recovery_test.go, which checks this mechanically by reflecting
// over every exported field of every exported type in this package,
// not merely by convention or comment.
//
// Relationship to pkg/insurance/party: a Target names its target via
// party.PartyID. Where the blueprint's category already has a matching
// party.Role in pkg/insurance/party (Carrier, Terminal, Forwarder,
// Surveyor, and the catch-all Other-third-party -> Role
// OtherResponsibleParty), CategoryPartyRole returns it so callers can
// tag the Target with the SAME role vocabulary the rest of VICE uses.
// Warehouse and Manufacturer have NO corresponding party.Role constant
// in pkg/insurance/party today — that package is a frozen foundation
// this package must not add roles to — so for those two categories
// CategoryPartyRole honestly reports "no known role" (its second return
// value is false) and a Target for them is identified by party.PartyID
// alone, with PartyRole left the zero value. This is a real gap in
// pkg/insurance/party, not silently worked around here.
package recovery

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"veriqo/pkg/insurance/party"
)

// ---- Basis: the legal/factual theory for pursuing recovery (§23 "basis") ----
//
// Design decision: the blueprint gives no vocabulary for "basis" beyond
// the field name itself, and explicitly leaves the shape open ("basis
// (free-text or a small enum)"). This package uses BOTH: a small closed
// Category naming the general kind of legal/factual theory (so the
// same handful of theories can be queried/aggregated across a case),
// plus a free-text Detail for the claim-specific facts a reviewer
// actually needs to evaluate the theory ("container door seal was
// broken on receipt at the terminal per survey S-004"). Category alone
// would lose the facts; Detail alone would lose queryability. Neither
// Category nor Detail is ever a liability finding — both describe an
// AVENUE worth investigating, not a conclusion that it succeeded.

// BasisCategory is the general kind of legal/factual theory a recovery
// Target is being pursued under.
type BasisCategory string

const (
	// BasisContractualBreach: the responsible party allegedly breached a
	// contract it was party to (e.g. a contract of carriage, a storage
	// agreement, a forwarding agreement).
	BasisContractualBreach BasisCategory = "CONTRACTUAL_BREACH"
	// BasisNegligence: the responsible party allegedly failed to
	// exercise reasonable care (a tort theory, not contract-specific).
	BasisNegligence BasisCategory = "NEGLIGENCE"
	// BasisBaileeLiability: the responsible party held the cargo as
	// bailee (carrier, terminal, warehouse) during the relevant
	// custody interval and bailee liability principles may apply.
	BasisBaileeLiability BasisCategory = "BAILEE_LIABILITY"
	// BasisStatutoryDuty: a statutory or regulatory duty (e.g. a
	// carriage-of-goods statute, port authority regulation) is alleged
	// to have been breached.
	BasisStatutoryDuty BasisCategory = "STATUTORY_DUTY"
	// BasisProductOrWorkDefect: the loss is allegedly attributable to a
	// defect in a manufactured product or in work performed (e.g. a
	// container manufacturing defect, a defective survey).
	BasisProductOrWorkDefect BasisCategory = "PRODUCT_OR_WORK_DEFECT"
	// BasisIndemnityOrContribution: a contractual indemnity or a
	// contribution claim among co-liable parties, rather than a
	// first-instance liability theory.
	BasisIndemnityOrContribution BasisCategory = "INDEMNITY_OR_CONTRIBUTION"
	// BasisOther: a theory not covered by the above categories. Detail
	// should explain it — this category exists so the enum stays
	// closed without forcing every real-world theory into a poor fit.
	BasisOther BasisCategory = "OTHER"
)

var knownBasisCategories = map[BasisCategory]bool{
	BasisContractualBreach: true, BasisNegligence: true, BasisBaileeLiability: true,
	BasisStatutoryDuty: true, BasisProductOrWorkDefect: true, BasisIndemnityOrContribution: true,
	BasisOther: true,
}

// IsKnownBasisCategory reports whether c is a modelled basis category.
func IsKnownBasisCategory(c BasisCategory) bool { return knownBasisCategories[c] }

// KnownBasisCategories returns every modelled basis category in
// deterministic order.
func KnownBasisCategories() []BasisCategory {
	out := make([]BasisCategory, 0, len(knownBasisCategories))
	for c := range knownBasisCategories {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Basis is the recovery Target's stated legal/factual theory (§23
// "basis"). It is a THEORY worth investigating, never a liability
// finding: Validate only checks that Category is one of the modelled
// values, and neither Category nor Detail is ever read by this package
// as "and therefore the party is liable".
type Basis struct {
	Category BasisCategory `json:"category"`
	Detail   string        `json:"detail,omitempty"`
}

var ErrUnknownBasisCategory = errors.New("recovery: unknown Basis.Category")

// Validate reports whether b's Category is a recognised BasisCategory.
func (b Basis) Validate() error {
	if !IsKnownBasisCategory(b.Category) {
		return fmt.Errorf("%w: %q", ErrUnknownBasisCategory, b.Category)
	}
	return nil
}

// ---- Money: potential_loss (§23 "potential_loss") ----
//
// Integer minor units (cents), never float64 — matching this project's
// determinism discipline for financial figures (the same reasoning the
// blueprint applies to quantum figures in §20: every number must be
// exactly reproducible, and float64 arithmetic is not).

// Money is an amount of potential loss, expressed in integer minor
// units (e.g. cents for USD) plus an explicit currency code.
type Money struct {
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
}

var (
	ErrNegativeAmount = errors.New("recovery: potential_loss AmountMinor must be >= 0")
	ErrEmptyCurrency  = errors.New("recovery: potential_loss Currency must be non-empty")
)

// Validate reports whether m is a well-formed amount: non-negative (a
// potential loss is a magnitude, never negative) and carries an
// explicit, non-empty currency (a bare integer is meaningless without
// one — this package never assumes a default currency).
func (m Money) Validate() error {
	if m.AmountMinor < 0 {
		return ErrNegativeAmount
	}
	if m.Currency == "" {
		return ErrEmptyCurrency
	}
	return nil
}

// ---- notice_status ----
//
// Design reasoning: notice to a potentially responsible party is a
// simple, linear procedural fact independent of whether recovery is
// ultimately pursued or succeeds — has VICE's user told the third
// party they may be pursued? PENDING (not yet sent) -> SENT (notice
// given, e.g. a notice-of-claim/notice-of-recourse letter) ->
// ACKNOWLEDGED (the third party has confirmed receipt). This says
// nothing about whether the third party accepts responsibility —
// "acknowledged" here means "acknowledged receipt of notice", not
// "acknowledged liability".

// NoticeStatus is the procedural state of notice given to a potential
// recovery target.
type NoticeStatus string

const (
	NoticeStatusPending      NoticeStatus = "PENDING"
	NoticeStatusSent         NoticeStatus = "SENT"
	NoticeStatusAcknowledged NoticeStatus = "ACKNOWLEDGED"
)

var knownNoticeStatuses = map[NoticeStatus]bool{
	NoticeStatusPending: true, NoticeStatusSent: true, NoticeStatusAcknowledged: true,
}

// IsKnownNoticeStatus reports whether s is a modelled notice status.
func IsKnownNoticeStatus(s NoticeStatus) bool { return knownNoticeStatuses[s] }

// KnownNoticeStatuses returns every modelled notice status in
// deterministic order.
func KnownNoticeStatuses() []NoticeStatus {
	out := make([]NoticeStatus, 0, len(knownNoticeStatuses))
	for s := range knownNoticeStatuses {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ---- limitation_status ----
//
// Design reasoning: WITHIN_LIMITATION / EXPIRED are the two facts that
// actually matter procedurally (can this avenue still be pursued in
// time, or not), and UNKNOWN is the golden-rule-compliant answer
// (blueprint §36: "never convert uncertainty into certainty") for when
// no deadline has been established yet — this package NEVER defaults
// an unestablished deadline to WITHIN_LIMITATION just because nothing
// has expired yet. See ComputeLimitationStatus below for the real
// computed logic behind this field.

// LimitationStatus is whether the limitation/prescription period for
// pursuing a recovery Target is still open.
type LimitationStatus string

const (
	LimitationWithin  LimitationStatus = "WITHIN_LIMITATION"
	LimitationExpired LimitationStatus = "EXPIRED"
	LimitationUnknown LimitationStatus = "UNKNOWN"
)

var knownLimitationStatuses = map[LimitationStatus]bool{
	LimitationWithin: true, LimitationExpired: true, LimitationUnknown: true,
}

// IsKnownLimitationStatus reports whether s is a modelled limitation status.
func IsKnownLimitationStatus(s LimitationStatus) bool { return knownLimitationStatuses[s] }

// KnownLimitationStatuses returns every modelled limitation status in
// deterministic order.
func KnownLimitationStatuses() []LimitationStatus {
	out := make([]LimitationStatus, 0, len(knownLimitationStatuses))
	for s := range knownLimitationStatuses {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ComputeLimitationStatus is the real computed logic behind
// limitation_status: given the tick at which the limitation period for
// a Target expires (deadlineTick; 0 means no deadline has been
// established yet — e.g. the deadline engine from blueprint §24 has not
// run for this target) and the current tick (nowTick), it returns which
// of the three LimitationStatus values actually follows.
//
//   - deadlineTick == 0            -> LimitationUnknown (nothing to compare against)
//   - nowTick <  deadlineTick      -> LimitationWithin
//   - nowTick >= deadlineTick      -> LimitationExpired (deadline is exclusive: the
//     tick AT the deadline itself is already expired)
//
// This function never guesses: it is a pure, three-way comparison of
// two ticks, nothing more.
func ComputeLimitationStatus(deadlineTick, nowTick uint64) LimitationStatus {
	if deadlineTick == 0 {
		return LimitationUnknown
	}
	if nowTick >= deadlineTick {
		return LimitationExpired
	}
	return LimitationWithin
}

// ---- recovery_status ----
//
// Design reasoning: this is a PROCEDURAL/WORKFLOW status about VICE's
// own tracking of the avenue, deliberately not a liability or success
// verdict. IDENTIFIED (VICE/the user has flagged this as a potential
// avenue, nothing pursued yet) -> PURSUING (recovery action is
// underway) -> RECOVERED (funds/value were actually recovered from
// this party — a FACT about money received, never a statement that the
// party admitted or was found liable; recovery can and does happen via
// settlement without any liability admission) or ABANDONED (the avenue
// is no longer being pursued, for any reason — expired limitation,
// insufficient evidence, commercial decision, etc. — this package does
// not need to know why) or UNRESOLVED (pursued but not yet concluded
// either way). UNRESOLVED and IDENTIFIED are deliberately distinct:
// IDENTIFIED is the starting state, UNRESOLVED is what a Target that
// WAS pursued sits in while still open.

// RecoveryStatus is the procedural state of one recovery Target.
type RecoveryStatus string

const (
	RecoveryStatusIdentified RecoveryStatus = "IDENTIFIED"
	RecoveryStatusPursuing   RecoveryStatus = "PURSUING"
	RecoveryStatusRecovered  RecoveryStatus = "RECOVERED"
	RecoveryStatusAbandoned  RecoveryStatus = "ABANDONED"
	RecoveryStatusUnresolved RecoveryStatus = "UNRESOLVED"
)

var knownRecoveryStatuses = map[RecoveryStatus]bool{
	RecoveryStatusIdentified: true, RecoveryStatusPursuing: true, RecoveryStatusRecovered: true,
	RecoveryStatusAbandoned: true, RecoveryStatusUnresolved: true,
}

// IsKnownRecoveryStatus reports whether s is a modelled recovery status.
func IsKnownRecoveryStatus(s RecoveryStatus) bool { return knownRecoveryStatuses[s] }

// KnownRecoveryStatuses returns every modelled recovery status in
// deterministic order.
func KnownRecoveryStatuses() []RecoveryStatus {
	out := make([]RecoveryStatus, 0, len(knownRecoveryStatuses))
	for s := range knownRecoveryStatuses {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ---- Target: one recovery avenue (§23) ----

// Target is one potential recovery/subrogation avenue against a third
// party. Its fields are exactly the blueprint's own required per-target
// fields (§23) — party, basis, supporting_evidence, potential_loss,
// notice_status, limitation_status, recovery_status — plus a small set
// of identifiers/support fields this package adds for the same reason
// claim.Claim adds ClaimID/CaseID on top of its own blueprint fields:
//
//   - TargetID, CaseID: identity, not judgement.
//   - PartyRole: OPTIONAL. Records which party.Role (already defined in
//     pkg/insurance/party) this pursuit corresponds to, when one
//     exists — see CategoryPartyRole. Left the zero value when no
//     matching role exists (Warehouse, Manufacturer targets).
//   - LimitationDeadlineTick: the tick ComputeLimitationStatus needs to
//     do real work instead of limitation_status being a purely
//     decorative field; 0 means "not yet established".
//
// No field on Target, at any point, asserts that Party IS responsible
// for the loss. RecoveryStatus tracks VICE's own pursuit workflow, and
// even RecoveryStatusRecovered records a FACT about funds received, not
// a legal finding of liability (recovery routinely happens via
// commercial settlement with no liability admission at all).
type Target struct {
	TargetID string `json:"target_id"`
	CaseID   string `json:"case_id"`

	Party     party.PartyID `json:"party"`
	PartyRole party.Role    `json:"party_role,omitempty"`

	Basis Basis `json:"basis"`

	SupportingEvidence []string `json:"supporting_evidence,omitempty"`

	PotentialLoss Money `json:"potential_loss"`

	NoticeStatus     NoticeStatus     `json:"notice_status"`
	LimitationStatus LimitationStatus `json:"limitation_status"`
	RecoveryStatus   RecoveryStatus   `json:"recovery_status"`

	// LimitationDeadlineTick is the tick at which this Target's
	// limitation/prescription period expires. 0 means not yet
	// established. See ComputeLimitationStatus and LimitationStatusAt.
	LimitationDeadlineTick uint64 `json:"limitation_deadline_tick,omitempty"`
}

var (
	ErrEmptyTargetID           = errors.New("recovery: TargetID must be non-empty")
	ErrEmptyCaseID             = errors.New("recovery: CaseID must be non-empty")
	ErrEmptyParty              = errors.New("recovery: Party must be non-empty")
	ErrUnknownNoticeStatus     = errors.New("recovery: unknown NoticeStatus")
	ErrUnknownLimitationStatus = errors.New("recovery: unknown LimitationStatus")
	ErrUnknownRecoveryStatus   = errors.New("recovery: unknown RecoveryStatus")
)

// Validate reports whether t is well-formed: identifiers present, a
// valid Basis and Money, and every status field one of its modelled
// values.
func (t Target) Validate() error {
	if t.TargetID == "" {
		return ErrEmptyTargetID
	}
	if t.CaseID == "" {
		return ErrEmptyCaseID
	}
	if t.Party == "" {
		return ErrEmptyParty
	}
	if err := t.Basis.Validate(); err != nil {
		return err
	}
	if err := t.PotentialLoss.Validate(); err != nil {
		return err
	}
	if !IsKnownNoticeStatus(t.NoticeStatus) {
		return fmt.Errorf("%w: %q", ErrUnknownNoticeStatus, t.NoticeStatus)
	}
	if !IsKnownLimitationStatus(t.LimitationStatus) {
		return fmt.Errorf("%w: %q", ErrUnknownLimitationStatus, t.LimitationStatus)
	}
	if !IsKnownRecoveryStatus(t.RecoveryStatus) {
		return fmt.Errorf("%w: %q", ErrUnknownRecoveryStatus, t.RecoveryStatus)
	}
	return nil
}

// LimitationStatusAt is the real computed check for whether t is still
// within its limitation period at nowTick — see ComputeLimitationStatus.
// It does NOT read or mutate t.LimitationStatus; callers who want the
// stored field to reflect this computation call
// Registry.RefreshLimitationStatus (see below), which is the honest,
// explicit way this package keeps a caller-set snapshot field in sync
// with computed reality: LimitationStatus is never silently recomputed
// on every read.
func (t Target) LimitationStatusAt(nowTick uint64) LimitationStatus {
	return ComputeLimitationStatus(t.LimitationDeadlineTick, nowTick)
}

// New constructs a Target in its natural starting state: NOTICE PENDING
// (nothing sent yet), LIMITATION UNKNOWN (no deadline established —
// golden rule: never assume WITHIN_LIMITATION by default), and RECOVERY
// STATUS IDENTIFIED (this avenue has just been flagged, nothing pursued
// yet). Callers set LimitationDeadlineTick and refresh LimitationStatus
// separately once a deadline is known (blueprint §24's Deadline Engine
// is the source of that, not this package).
func New(targetID, caseID string, p party.PartyID, basis Basis, potentialLoss Money) (Target, error) {
	t := Target{
		TargetID:         targetID,
		CaseID:           caseID,
		Party:            p,
		Basis:            basis,
		PotentialLoss:    potentialLoss,
		NoticeStatus:     NoticeStatusPending,
		LimitationStatus: LimitationUnknown,
		RecoveryStatus:   RecoveryStatusIdentified,
	}
	if err := t.Validate(); err != nil {
		return Target{}, err
	}
	return t, nil
}

// ---- Registry: case-scoped set of recovery targets ----

var (
	ErrCaseIDMismatch  = errors.New("recovery: Target.CaseID does not match this registry's CaseID")
	ErrDuplicateTarget = errors.New("recovery: TargetID already registered")
	ErrTargetNotFound  = errors.New("recovery: TargetID not found")
	ErrEmptyEvidenceID = errors.New("recovery: EvidenceID must be non-empty")
)

// Registry holds every recovery Target identified for ONE case.
// Thread-safe: recovery analysis can run concurrently with the rest of
// case processing.
type Registry struct {
	mu      sync.RWMutex
	caseID  string
	targets map[string]Target
	order   []string // registration order, for deterministic iteration
}

// NewRegistry returns an empty recovery-target registry scoped to caseID.
func NewRegistry(caseID string) (*Registry, error) {
	if caseID == "" {
		return nil, ErrEmptyCaseID
	}
	return &Registry{caseID: caseID, targets: make(map[string]Target)}, nil
}

// CaseID returns the case this registry is scoped to.
func (r *Registry) CaseID() string { return r.caseID }

// Register adds a new, already-valid Target to the registry. Refuses a
// Target whose CaseID doesn't match this registry's own CaseID (a
// recovery target from a different case must never end up mixed into
// this one), a Target that fails Validate, and a duplicate TargetID.
func (r *Registry) Register(t Target) error {
	if err := t.Validate(); err != nil {
		return err
	}
	if t.CaseID != r.caseID {
		return fmt.Errorf("%w: target CaseID %q, registry CaseID %q", ErrCaseIDMismatch, t.CaseID, r.caseID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.targets[t.TargetID]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateTarget, t.TargetID)
	}
	r.targets[t.TargetID] = t
	r.order = append(r.order, t.TargetID)
	return nil
}

// Get returns the Target for targetID.
func (r *Registry) Get(targetID string) (Target, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.targets[targetID]
	return t, ok
}

// All returns every registered Target in registration order.
func (r *Registry) All() []Target {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Target, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.targets[id])
	}
	return out
}

// ByParty returns every Target naming p, in registration order.
func (r *Registry) ByParty(p party.PartyID) []Target {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Target
	for _, id := range r.order {
		t := r.targets[id]
		if t.Party == p {
			out = append(out, t)
		}
	}
	return out
}

// ByRecoveryStatus returns every Target currently in status, in
// registration order.
func (r *Registry) ByRecoveryStatus(status RecoveryStatus) []Target {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Target
	for _, id := range r.order {
		t := r.targets[id]
		if t.RecoveryStatus == status {
			out = append(out, t)
		}
	}
	return out
}

// SetNoticeStatus updates a Target's notice status.
func (r *Registry) SetNoticeStatus(targetID string, status NoticeStatus) error {
	if !IsKnownNoticeStatus(status) {
		return fmt.Errorf("%w: %q", ErrUnknownNoticeStatus, status)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.targets[targetID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrTargetNotFound, targetID)
	}
	t.NoticeStatus = status
	r.targets[targetID] = t
	return nil
}

// SetRecoveryStatus updates a Target's recovery status. This is a
// procedural workflow update ONLY — see the RecoveryStatus doc comment
// above for why even RecoveryStatusRecovered is not a liability finding.
func (r *Registry) SetRecoveryStatus(targetID string, status RecoveryStatus) error {
	if !IsKnownRecoveryStatus(status) {
		return fmt.Errorf("%w: %q", ErrUnknownRecoveryStatus, status)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.targets[targetID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrTargetNotFound, targetID)
	}
	t.RecoveryStatus = status
	r.targets[targetID] = t
	return nil
}

// SetLimitationStatus sets a Target's limitation_status DIRECTLY, as a
// caller-asserted snapshot (e.g. a human reviewer or an external
// deadline engine has determined the answer through means this package
// doesn't model). For the package's OWN computed derivation from a
// deadline tick, use RefreshLimitationStatus instead.
func (r *Registry) SetLimitationStatus(targetID string, status LimitationStatus) error {
	if !IsKnownLimitationStatus(status) {
		return fmt.Errorf("%w: %q", ErrUnknownLimitationStatus, status)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.targets[targetID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrTargetNotFound, targetID)
	}
	t.LimitationStatus = status
	r.targets[targetID] = t
	return nil
}

// SetLimitationDeadline records the tick at which targetID's limitation
// period expires (0 clears it back to "not established").
func (r *Registry) SetLimitationDeadline(targetID string, deadlineTick uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.targets[targetID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrTargetNotFound, targetID)
	}
	t.LimitationDeadlineTick = deadlineTick
	r.targets[targetID] = t
	return nil
}

// RefreshLimitationStatus is the registry-level entry point for the
// package's REAL computed limitation logic: it recomputes targetID's
// LimitationStatus from its stored LimitationDeadlineTick and nowTick
// via ComputeLimitationStatus, stores the result back onto the Target,
// and returns it. This is the mechanism by which limitation_status
// becomes a genuinely computed field rather than a purely decorative
// one — callers who want a live answer without persisting it should
// call Target.LimitationStatusAt directly instead.
func (r *Registry) RefreshLimitationStatus(targetID string, nowTick uint64) (LimitationStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.targets[targetID]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrTargetNotFound, targetID)
	}
	status := ComputeLimitationStatus(t.LimitationDeadlineTick, nowTick)
	t.LimitationStatus = status
	r.targets[targetID] = t
	return status, nil
}

// AddSupportingEvidence appends an evidence.Record EvidenceID to a
// Target's supporting_evidence list. Refuses an empty evidenceID; does
// NOT deduplicate against evidence already listed, so the caller who
// wants uniqueness enforces it (this package does not know whether a
// second submission of the same ID is a caller mistake or an
// intentional re-assertion for a different purpose).
func (r *Registry) AddSupportingEvidence(targetID, evidenceID string) error {
	if evidenceID == "" {
		return ErrEmptyEvidenceID
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.targets[targetID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrTargetNotFound, targetID)
	}
	t.SupportingEvidence = append(t.SupportingEvidence, evidenceID)
	r.targets[targetID] = t
	return nil
}

// Count returns the number of targets in the registry.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.targets)
}

// ---- The blueprint's own worked list of potential responsible parties (§23) ----

// ResponsiblePartyCategory is one entry of the blueprint's own worked
// example list of potential responsible parties (§23): Carrier,
// Terminal, Warehouse, Forwarder, Manufacturer, Surveyor, Other third
// party. This is a documentation/classification vocabulary for callers
// and tests — it is NOT itself a party.Role, and a Target does not
// carry a ResponsiblePartyCategory field: see CategoryPartyRole for how
// (and how incompletely) it maps onto pkg/insurance/party.Role.
type ResponsiblePartyCategory string

const (
	CategoryCarrier         ResponsiblePartyCategory = "CARRIER"
	CategoryTerminal        ResponsiblePartyCategory = "TERMINAL"
	CategoryWarehouse       ResponsiblePartyCategory = "WAREHOUSE"
	CategoryForwarder       ResponsiblePartyCategory = "FORWARDER"
	CategoryManufacturer    ResponsiblePartyCategory = "MANUFACTURER"
	CategorySurveyor        ResponsiblePartyCategory = "SURVEYOR"
	CategoryOtherThirdParty ResponsiblePartyCategory = "OTHER_THIRD_PARTY"
)

// PotentialResponsiblePartyCategories returns the blueprint's own §23
// worked list, in the blueprint's own verbatim order: Carrier,
// Terminal, Warehouse, Forwarder, Manufacturer, Surveyor, Other third
// party.
func PotentialResponsiblePartyCategories() []ResponsiblePartyCategory {
	return []ResponsiblePartyCategory{
		CategoryCarrier,
		CategoryTerminal,
		CategoryWarehouse,
		CategoryForwarder,
		CategoryManufacturer,
		CategorySurveyor,
		CategoryOtherThirdParty,
	}
}

// CategoryPartyRole returns the pkg/insurance/party.Role that
// corresponds to c, and true, when one already exists in that package's
// known-role set. It returns ("", false) for CategoryWarehouse and
// CategoryManufacturer, which have NO corresponding party.Role constant
// today (party.go is a frozen foundation this package does not modify
// to add one) — callers must not invent a role for those two; identify
// such a Target by party.PartyID alone, leaving Target.PartyRole the
// zero value.
func CategoryPartyRole(c ResponsiblePartyCategory) (party.Role, bool) {
	switch c {
	case CategoryCarrier:
		return party.RoleCarrier, true
	case CategoryTerminal:
		return party.RoleTerminal, true
	case CategoryForwarder:
		return party.RoleForwarder, true
	case CategorySurveyor:
		return party.RoleSurveyor, true
	case CategoryOtherThirdParty:
		return party.RoleOtherResponsibleParty, true
	case CategoryWarehouse, CategoryManufacturer:
		return "", false
	default:
		return "", false
	}
}
