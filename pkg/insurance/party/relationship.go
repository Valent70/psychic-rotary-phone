package party

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// This file closes the VERIQO Final Remaining Gap Closure Order's P0
// §8 ("Real-World Insurance Network") item: "The role taxonomy is not
// enough. Build the operational relationship layer." Role (party.go)
// answers WHAT a party can be; Relationship answers WHO actually
// stands in that role TOWARD WHOM, on what authority, for how long, and
// with the insured's consent — the mandate's own required fields:
// identity, role, authority, effective dates, contractual basis,
// permissions, consent, provenance, evidence, revocation, status.
//
// Deliberately not a second identity or authorization engine: a
// Relationship names two existing PartyIDs and one existing Role — it
// creates no new identity concept, and Permission below is a narrow,
// insurance-workflow-scoped vocabulary, not a re-implementation of
// pkg/authz's RBAC/ABAC (which governs API/system access, not who may
// act for whom inside one case).

// Permission is one narrow, case-scoped action a Relationship may grant
// — e.g. "this broker may submit evidence on the insured's behalf".
// Closed enum, matching every other classification vocabulary in this
// domain.
type Permission string

const (
	PermissionViewEvidence    Permission = "VIEW_EVIDENCE"
	PermissionSubmitEvidence  Permission = "SUBMIT_EVIDENCE"
	PermissionSubmitClaim     Permission = "SUBMIT_CLAIM"
	PermissionNegotiateClaim  Permission = "NEGOTIATE_CLAIM"
	PermissionBindCover       Permission = "BIND_COVER"
	PermissionApprovePayment  Permission = "APPROVE_PAYMENT"
	PermissionAccessCaseRoom  Permission = "ACCESS_CASE_ROOM"
	PermissionReceiveNotice   Permission = "RECEIVE_NOTICE"
	PermissionActInDispute    Permission = "ACT_IN_DISPUTE"
	PermissionReceiveRecovery Permission = "RECEIVE_RECOVERY"
)

var knownPermissions = map[Permission]bool{
	PermissionViewEvidence: true, PermissionSubmitEvidence: true, PermissionSubmitClaim: true,
	PermissionNegotiateClaim: true, PermissionBindCover: true, PermissionApprovePayment: true,
	PermissionAccessCaseRoom: true, PermissionReceiveNotice: true, PermissionActInDispute: true,
	PermissionReceiveRecovery: true,
}

// IsKnownPermission reports whether p is a modelled permission.
func IsKnownPermission(p Permission) bool { return knownPermissions[p] }

// KnownPermissions returns every modelled permission in deterministic order.
func KnownPermissions() []Permission {
	out := make([]Permission, 0, len(knownPermissions))
	for p := range knownPermissions {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// RelationshipStatus is the relationship's own procedural state —
// PENDING (declared, consent not yet recorded) -> ACTIVE (consented and
// within its effective window) -> REVOKED (withdrawn before its natural
// expiry) or EXPIRED (its effective window has simply closed). REVOKED
// and EXPIRED are kept distinct for the same reason
// recovery.RecoveryStatus keeps ABANDONED separate from a clean
// resolution: an auditor needs to know whether a relationship ended
// because someone withdrew it or because its own term ran out.
type RelationshipStatus string

const (
	RelationshipStatusPending RelationshipStatus = "PENDING"
	RelationshipStatusActive  RelationshipStatus = "ACTIVE"
	RelationshipStatusRevoked RelationshipStatus = "REVOKED"
	RelationshipStatusExpired RelationshipStatus = "EXPIRED"
)

var knownRelationshipStatuses = map[RelationshipStatus]bool{
	RelationshipStatusPending: true, RelationshipStatusActive: true,
	RelationshipStatusRevoked: true, RelationshipStatusExpired: true,
}

// IsKnownRelationshipStatus reports whether s is a modelled status.
func IsKnownRelationshipStatus(s RelationshipStatus) bool { return knownRelationshipStatuses[s] }

// Relationship is one permissioned connection between two parties in
// one case's insurance network — e.g. "FromParty acts as Role toward
// ToParty". Every field is the mandate's own required field, verbatim.
type Relationship struct {
	RelationshipID string `json:"relationship_id"`
	CaseID         string `json:"case_id"`

	// FromParty holds Role toward ToParty — e.g. FromParty=the broker,
	// Role=RoleBroker, ToParty=the insured.
	FromParty PartyID `json:"from_party"`
	ToParty   PartyID `json:"to_party"`
	Role      Role    `json:"role"`

	// Authority is the specific grant of authority in the claims
	// officer's own words (e.g. "binding authority under TOBA ref
	// TOBA-2024-081"). Free text, matching recovery.Basis.Detail's own
	// reasoning: a closed taxonomy of every real-world authority
	// instrument would be a worse fit than a stated reason plus a
	// cited document.
	Authority string `json:"authority,omitempty"`
	// ContractualBasis names the actual instrument (a Terms of Business
	// Agreement, a binder, a coverholder agreement, an LOA) this
	// relationship rests on.
	ContractualBasis string `json:"contractual_basis,omitempty"`

	// The four fields below are additive: the Round 4 work order's Party
	// Authority Model (§27) names organization/scope/delegation/tenant/
	// jurisdiction as required fields alongside the ones already above
	// (identity=FromParty/ToParty, role=Role, authority=Authority,
	// effective_from/to, consent, revocation). None of them changes the
	// meaning of an existing field or an existing caller's behavior —
	// every one defaults to its zero value, which Validate treats as
	// "not stated" rather than an error.

	// Organization names the organizational entity FromParty acts FOR,
	// distinct from FromParty's own individual/party identity — e.g.
	// FromParty is one named surveyor, Organization is "Acme Marine
	// Surveyors Ltd" the surveyor works for. Free text, matching
	// Authority's own convention: a closed taxonomy of every real-world
	// organization would be a worse fit than a stated name.
	Organization string `json:"organization,omitempty"`

	// Scope bounds WHAT this relationship's authority covers, in the
	// claims officer's own words (e.g. "cargo damage assessment for
	// CLM-002 only; hull is out of scope") — distinct from Permissions,
	// which is the closed system-action vocabulary. Scope is the
	// subject-matter boundary; Permissions is the operational boundary.
	Scope string `json:"scope,omitempty"`

	// DelegatedFrom, if non-empty, names the RelationshipID this
	// relationship's own authority derives from — e.g. a sub-surveyor a
	// broker's coverholder appointed under its own binding authority.
	// RelationshipRegistry.Register refuses a DelegatedFrom that does
	// not already exist in the same registry, which is what makes a
	// delegation chain real rather than a bare string: it can only ever
	// point to a relationship that was itself already validated and
	// registered, so a cycle cannot be constructed.
	DelegatedFrom string `json:"delegated_from,omitempty"`
	// CanDelegate reports whether this relationship's authority may
	// itself be delegated further (i.e. whether another Relationship may
	// cite this one's RelationshipID as its own DelegatedFrom). Defaults
	// false: delegation authority is never assumed, only granted.
	CanDelegate bool `json:"can_delegate,omitempty"`

	// Tenant names the operational unit (insurer, coverholder, MGA
	// office) this relationship's authority was granted within, for a
	// multi-tenant deployment where more than one such unit shares this
	// codebase. Free text; empty means "the case's own single tenant",
	// which remains the common case and is never treated as an error.
	Tenant string `json:"tenant,omitempty"`

	// Jurisdiction names the governing law/forum this relationship's
	// authority is granted under (e.g. "England and Wales", "Singapore
	// International Arbitration Centre") — distinct from
	// ContractualBasis (which names the instrument) and from
	// dispute.Forum (which is the forum for a DISPUTE, not for the
	// underlying relationship itself).
	Jurisdiction string `json:"jurisdiction,omitempty"`

	EffectiveFrom uint64 `json:"effective_from"`
	// EffectiveTo == 0 means open-ended, matching policy.Version's own
	// convention (and now geospatial.Geofence's) for the same reason:
	// one "unbounded" convention across the codebase, not several.
	EffectiveTo uint64 `json:"effective_to,omitempty"`

	Permissions []Permission `json:"permissions,omitempty"`

	// ConsentGiven records whether the party the relationship acts upon
	// (typically ToParty, e.g. the insured) has consented to it.
	// ConsentEvidenceID is the pkg/insurance/evidence.Record EvidenceID
	// proving that consent (a signed TOBA, an executed LOA) — a
	// Relationship claiming ConsentGiven with no cited evidence is
	// exactly the gap Validate refuses, matching this domain's
	// evidence-or-it-didn't-happen discipline.
	ConsentGiven      bool   `json:"consent_given"`
	ConsentEvidenceID string `json:"consent_evidence_id,omitempty"`

	// ProvenanceEvidenceIDs are the evidence records establishing the
	// relationship itself exists (the TOBA document, the binder, the
	// coverholder agreement) — distinct from ConsentEvidenceID, which
	// is specifically about consent; a relationship can be evidenced
	// (we can see the TOBA) without yet being consented to (the insured
	// has not signed) — that is exactly PENDING.
	ProvenanceEvidenceIDs []string `json:"provenance_evidence_ids,omitempty"`

	Status RelationshipStatus `json:"status"`

	RevokedAtTick    uint64 `json:"revoked_at_tick,omitempty"`
	RevocationReason string `json:"revocation_reason,omitempty"`
}

var (
	ErrEmptyRelationshipID       = errors.New("party: RelationshipID must be non-empty")
	ErrEmptyRelationshipCase     = errors.New("party: Relationship.CaseID must be non-empty")
	ErrEmptyFromParty            = errors.New("party: Relationship.FromParty must be non-empty")
	ErrEmptyToParty              = errors.New("party: Relationship.ToParty must be non-empty")
	ErrSelfRelationship          = errors.New("party: FromParty and ToParty must be different parties")
	ErrRelationshipUnknownRole   = errors.New("party: Relationship.Role is not a known Role")
	ErrRelationshipToBeforeFrom  = errors.New("party: Relationship.EffectiveTo must be 0 (open) or >= EffectiveFrom")
	ErrUnknownPermission         = errors.New("party: unknown Permission")
	ErrConsentWithNoEvidence     = errors.New("party: ConsentGiven is true but ConsentEvidenceID is empty -- consent without evidence did not happen")
	ErrRelationshipUnknownStatus = errors.New("party: unknown Relationship.Status")
	ErrRevokedNeedsReason        = errors.New("party: Status is REVOKED but RevocationReason is empty")
	ErrSelfDelegation            = errors.New("party: Relationship.DelegatedFrom must not be its own RelationshipID")
	ErrDelegationSourceNotFound  = errors.New("party: DelegatedFrom does not name a relationship already registered in this registry")
	ErrDelegationNotPermitted    = errors.New("party: DelegatedFrom names a relationship that does not permit further delegation (CanDelegate is false)")
)

// Validate checks r's own internal consistency. Deliberately does NOT
// require ConsentGiven for every status — a PENDING relationship
// legitimately has none yet.
func (r Relationship) Validate() error {
	if r.RelationshipID == "" {
		return ErrEmptyRelationshipID
	}
	if r.CaseID == "" {
		return ErrEmptyRelationshipCase
	}
	if r.FromParty == "" {
		return ErrEmptyFromParty
	}
	if r.ToParty == "" {
		return ErrEmptyToParty
	}
	if r.FromParty == r.ToParty {
		return ErrSelfRelationship
	}
	if !IsKnownRole(r.Role) {
		return fmt.Errorf("%w: %q", ErrRelationshipUnknownRole, r.Role)
	}
	if r.EffectiveTo != 0 && r.EffectiveTo < r.EffectiveFrom {
		return ErrRelationshipToBeforeFrom
	}
	if r.DelegatedFrom != "" && r.DelegatedFrom == r.RelationshipID {
		return ErrSelfDelegation
	}
	for _, p := range r.Permissions {
		if !IsKnownPermission(p) {
			return fmt.Errorf("%w: %q", ErrUnknownPermission, p)
		}
	}
	if r.ConsentGiven && r.ConsentEvidenceID == "" {
		return ErrConsentWithNoEvidence
	}
	if !IsKnownRelationshipStatus(r.Status) {
		return fmt.Errorf("%w: %q", ErrRelationshipUnknownStatus, r.Status)
	}
	if r.Status == RelationshipStatusRevoked && r.RevocationReason == "" {
		return ErrRevokedNeedsReason
	}
	return nil
}

// HasPermission reports whether r grants perm.
func (r Relationship) HasPermission(perm Permission) bool {
	for _, p := range r.Permissions {
		if p == perm {
			return true
		}
	}
	return false
}

// EffectiveAt reports whether r is genuinely in force at tick: within
// its declared window, consented to, and not revoked before tick.
// PENDING (no consent yet) is never effective, no matter the window —
// an unconsented relationship grants no authority, matching the
// mandate's own "permissions; consent" pairing.
func (r Relationship) EffectiveAt(tick uint64) bool {
	if !r.ConsentGiven {
		return false
	}
	if r.Status == RelationshipStatusRevoked && r.RevokedAtTick <= tick {
		return false
	}
	if tick < r.EffectiveFrom {
		return false
	}
	if r.EffectiveTo != 0 && tick > r.EffectiveTo {
		return false
	}
	return true
}

// New constructs a Relationship in its natural starting state: PENDING,
// no consent, no permissions — callers add permissions and record
// consent explicitly via the Registry below.
func New(relationshipID, caseID string, from, to PartyID, role Role, effectiveFrom uint64) (Relationship, error) {
	r := Relationship{
		RelationshipID: relationshipID, CaseID: caseID,
		FromParty: from, ToParty: to, Role: role,
		EffectiveFrom: effectiveFrom, Status: RelationshipStatusPending,
	}
	if err := r.Validate(); err != nil {
		return Relationship{}, err
	}
	return r, nil
}

// ---- RelationshipRegistry ----

var (
	ErrDuplicateRelationship      = errors.New("party: RelationshipID already registered")
	ErrRelationshipNotFound       = errors.New("party: RelationshipID not found")
	ErrRelationshipCaseIDMismatch = errors.New("party: Relationship.CaseID does not match this registry's CaseID")
	ErrEmptyConsentEvidenceID     = errors.New("party: consent EvidenceID must be non-empty")
	ErrEmptyRevocationReason      = errors.New("party: revocation reason must be non-empty")
)

// RelationshipRegistry holds every Relationship declared for ONE case,
// matching recovery.Registry's / salvage.Registry's own concurrency and
// determinism discipline.
type RelationshipRegistry struct {
	mu     sync.RWMutex
	caseID string
	rels   map[string]Relationship
	order  []string
}

// NewRelationshipRegistry returns an empty relationship registry scoped
// to caseID.
func NewRelationshipRegistry(caseID string) (*RelationshipRegistry, error) {
	if caseID == "" {
		return nil, ErrEmptyRelationshipCase
	}
	return &RelationshipRegistry{caseID: caseID, rels: make(map[string]Relationship)}, nil
}

// CaseID returns the case this registry is scoped to.
func (r *RelationshipRegistry) CaseID() string { return r.caseID }

// Register adds a new, already-valid Relationship.
func (r *RelationshipRegistry) Register(rel Relationship) error {
	if err := rel.Validate(); err != nil {
		return err
	}
	if rel.CaseID != r.caseID {
		return fmt.Errorf("%w: relationship CaseID %q, registry CaseID %q", ErrRelationshipCaseIDMismatch, rel.CaseID, r.caseID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.rels[rel.RelationshipID]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateRelationship, rel.RelationshipID)
	}
	if rel.DelegatedFrom != "" {
		source, ok := r.rels[rel.DelegatedFrom]
		if !ok {
			return fmt.Errorf("%w: %s", ErrDelegationSourceNotFound, rel.DelegatedFrom)
		}
		if !source.CanDelegate {
			return fmt.Errorf("%w: %s", ErrDelegationNotPermitted, rel.DelegatedFrom)
		}
	}
	r.rels[rel.RelationshipID] = rel
	r.order = append(r.order, rel.RelationshipID)
	return nil
}

// Get returns the Relationship for id.
func (r *RelationshipRegistry) Get(id string) (Relationship, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rel, ok := r.rels[id]
	return rel, ok
}

// All returns every registered Relationship in registration order.
func (r *RelationshipRegistry) All() []Relationship {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Relationship, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.rels[id])
	}
	return out
}

// ByParty returns every Relationship where p is FromParty or ToParty,
// in registration order.
func (r *RelationshipRegistry) ByParty(p PartyID) []Relationship {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Relationship
	for _, id := range r.order {
		rel := r.rels[id]
		if rel.FromParty == p || rel.ToParty == p {
			out = append(out, rel)
		}
	}
	return out
}

// GrantPermissions adds permissions to a Relationship's Permissions
// list, deduplicated.
func (r *RelationshipRegistry) GrantPermissions(id string, perms ...Permission) error {
	for _, p := range perms {
		if !IsKnownPermission(p) {
			return fmt.Errorf("%w: %q", ErrUnknownPermission, p)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rel, ok := r.rels[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrRelationshipNotFound, id)
	}
	have := map[Permission]bool{}
	for _, p := range rel.Permissions {
		have[p] = true
	}
	for _, p := range perms {
		if !have[p] {
			rel.Permissions = append(rel.Permissions, p)
			have[p] = true
		}
	}
	r.rels[id] = rel
	return nil
}

// RecordConsent records ToParty's consent to the relationship, citing
// the evidence proving it, and advances Status from PENDING to ACTIVE
// (if it was PENDING — a REVOKED relationship is not reactivated by
// this call; a caller who genuinely wants that registers a NEW
// relationship, so the revocation itself stays a permanent fact rather
// than being silently overwritten).
func (r *RelationshipRegistry) RecordConsent(id, evidenceID string) error {
	if evidenceID == "" {
		return ErrEmptyConsentEvidenceID
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rel, ok := r.rels[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrRelationshipNotFound, id)
	}
	rel.ConsentGiven = true
	rel.ConsentEvidenceID = evidenceID
	if rel.Status == RelationshipStatusPending {
		rel.Status = RelationshipStatusActive
	}
	if err := rel.Validate(); err != nil {
		return err
	}
	r.rels[id] = rel
	return nil
}

// Revoke withdraws a relationship at tick, with a stated reason. A
// revoked relationship's history (who it was, what it authorized, for
// how long) remains fully readable — Revoke never deletes the record,
// matching this codebase's immutability discipline (preservation legal
// holds, evidence supersession) applied here to authorization state.
func (r *RelationshipRegistry) Revoke(id string, atTick uint64, reason string) error {
	if reason == "" {
		return ErrEmptyRevocationReason
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rel, ok := r.rels[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrRelationshipNotFound, id)
	}
	rel.Status = RelationshipStatusRevoked
	rel.RevokedAtTick = atTick
	rel.RevocationReason = reason
	if err := rel.Validate(); err != nil {
		return err
	}
	r.rels[id] = rel
	return nil
}

// EffectiveAt reports whether the relationship id is genuinely in force
// at tick — see Relationship.EffectiveAt. Returns false for an unknown
// id (fail closed: an unknown relationship grants no authority).
func (r *RelationshipRegistry) EffectiveAt(id string, tick uint64) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rel, ok := r.rels[id]
	if !ok {
		return false
	}
	return rel.EffectiveAt(tick)
}

// AddProvenance appends an evidence.Record EvidenceID establishing the
// relationship itself (the TOBA, the binder, the coverholder
// agreement).
func (r *RelationshipRegistry) AddProvenance(id, evidenceID string) error {
	if evidenceID == "" {
		return errors.New("party: provenance EvidenceID must be non-empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rel, ok := r.rels[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrRelationshipNotFound, id)
	}
	rel.ProvenanceEvidenceIDs = append(rel.ProvenanceEvidenceIDs, evidenceID)
	r.rels[id] = rel
	return nil
}

// DelegationChain returns the full chain of relationships id's authority
// derives from, ROOT-FIRST, ending with id itself. A relationship with
// no DelegatedFrom returns a single-element chain (itself). Register's
// own DelegatedFrom check makes a cycle unconstructable through normal
// use (a delegation can only ever point to a relationship registered
// strictly earlier), but this still walks defensively rather than
// trusting that invariant blindly.
func (r *RelationshipRegistry) DelegationChain(id string) ([]Relationship, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var chain []Relationship
	cur := id
	seen := map[string]bool{}
	for cur != "" {
		if seen[cur] {
			return nil, fmt.Errorf("party: delegation chain for %s contains a cycle at %s", id, cur)
		}
		seen[cur] = true
		rel, ok := r.rels[cur]
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrRelationshipNotFound, cur)
		}
		chain = append(chain, rel)
		cur = rel.DelegatedFrom
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain, nil
}

// Count returns the number of relationships in the registry.
func (r *RelationshipRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.rels)
}
