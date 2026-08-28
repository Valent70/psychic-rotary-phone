// Package credential implements this program's own Round 8 self-review
// gap G4 ("Real participant onboarding model"). The reviewer's own
// worked chain is:
//
//	Party -> Organization -> Jurisdiction -> License/Accreditation ->
//	Authority -> Delegation -> Credential -> Evidence -> Qualification
//	-> Revocation
//
// Four of those ten already exist elsewhere and are DELIBERATELY
// REUSED, never re-derived (Final Design §39's own rule):
//
//   - Party is party.PartyID.
//   - Organization, Jurisdiction and Authority are already fields on
//     party.Relationship (added the Round 4 work order's own Party
//     Authority Model, §27).
//   - Delegation is already party.Relationship.DelegatedFrom/CanDelegate,
//     with RelationshipRegistry.Register already enforcing the chain
//     depth and cycle-freedom a real delegation model needs.
//
// What genuinely does NOT exist yet, and this package adds:
//
//   - License/Accreditation and Credential: party.Relationship's
//     Authority field is free text ("binding authority under TOBA ref
//     ..."); nothing structured records a specific licence/
//     accreditation/certification with its own issuing authority,
//     jurisdiction, and validity window. Credential is that structured
//     record.
//   - Evidence: CredentialRegistry.Register refuses a Credential with
//     no cited evidence — matching party.Relationship's own
//     "ConsentGiven with no ConsentEvidenceID did not happen" rule,
//     applied here to "a credential with no evidence was never proven".
//   - Qualification: network.QualificationState (pkg/insurance/network)
//     is a bare enum with no record of WHO attested it, WHICH external
//     registry (network.RegistrySource) answered, or WHEN.
//     QualificationRecord is that structured, evidenced record.
//   - Revocation: CredentialRegistry.Revoke, matching
//     RelationshipRegistry.Revoke's own append-only, reason-required
//     discipline (a revoked credential's history remains fully
//     readable, never deleted).
package credential

import (
	"errors"
	"fmt"
	"sync"

	"veriqo/pkg/insurance/network"
	"veriqo/pkg/insurance/party"
)

// Kind is the type of structured credential a party may hold — the
// reviewer's own "License/Accreditation" pairing plus Certification,
// which pkg/insurance/party.RoleIndependentReviewer and
// RoleMarineSurveyCompany already imply exist in this domain without
// anywhere to record one.
type Kind string

const (
	KindLicense       Kind = "LICENSE"
	KindAccreditation Kind = "ACCREDITATION"
	KindCertification Kind = "CERTIFICATION"
)

var knownKinds = map[Kind]bool{KindLicense: true, KindAccreditation: true, KindCertification: true}

// IsKnownKind reports whether k is a modelled credential kind.
func IsKnownKind(k Kind) bool { return knownKinds[k] }

// Status is a credential's own lifecycle status, derived — never set
// directly — from IssuedAtTick/ExpiresAtTick and whether Revoke was
// called, matching party.RelationshipStatus's own PENDING/ACTIVE/
// REVOKED/EXPIRED reasoning (a credential has no PENDING state: it
// either exists as issued, or it does not exist yet at all).
type Status string

const (
	StatusActive  Status = "ACTIVE"
	StatusExpired Status = "EXPIRED"
	StatusRevoked Status = "REVOKED"
)

var knownStatuses = map[Status]bool{StatusActive: true, StatusExpired: true, StatusRevoked: true}

// IsKnownStatus reports whether s is a modelled credential status.
func IsKnownStatus(s Status) bool { return knownStatuses[s] }

// Credential is one structured License/Accreditation/Certification a
// party holds — the reviewer's own G4 chain's "License/Accreditation
// -> Credential" pairing modelled as one type (a credential IS a
// license/accreditation/certification; Kind distinguishes which).
// Organization/Jurisdiction/Authority for the SAME party are read from
// that party's own party.Relationship records elsewhere — this type
// does not repeat them.
type Credential struct {
	CredentialID string        `json:"credential_id"`
	PartyID      party.PartyID `json:"party_id"`
	Kind         Kind          `json:"kind"`

	// IssuingAuthority names the real-world body that issued this
	// credential (e.g. "International Group of P&I Clubs", a flag
	// state's own maritime authority) — free text, matching
	// party.Relationship.Authority's own reasoning: a closed taxonomy of
	// every real-world issuing authority would be a worse fit than a
	// stated name.
	IssuingAuthority string `json:"issuing_authority"`
	Jurisdiction     string `json:"jurisdiction,omitempty"`

	IssuedAtTick uint64 `json:"issued_at_tick"`
	// ExpiresAtTick == 0 means open-ended, matching
	// party.Relationship.EffectiveTo's own "0 == unbounded" convention.
	ExpiresAtTick uint64 `json:"expires_at_tick,omitempty"`

	// EvidenceIDs are the pkg/insurance/evidence.Record EvidenceIDs
	// proving this credential exists (the licence document, the
	// accreditation certificate) — required non-empty by Register,
	// matching this domain's evidence-or-it-didn't-happen discipline.
	EvidenceIDs []string `json:"evidence_ids"`

	revokedAtTick    uint64
	revocationReason string
	revoked          bool
}

var (
	ErrEmptyCredentialID     = errors.New("credential: CredentialID must be non-empty")
	ErrEmptyCredentialParty  = errors.New("credential: PartyID must be non-empty")
	ErrUnknownKind           = errors.New("credential: unknown Kind")
	ErrEmptyIssuingAuthority = errors.New("credential: IssuingAuthority must be non-empty")
	ErrNoEvidence            = errors.New("credential: a credential with no cited evidence was never proven")
	ErrExpiresBeforeIssued   = errors.New("credential: ExpiresAtTick must be 0 (open) or >= IssuedAtTick")
)

// Validate checks c's own internal consistency.
func (c Credential) Validate() error {
	if c.CredentialID == "" {
		return ErrEmptyCredentialID
	}
	if c.PartyID == "" {
		return ErrEmptyCredentialParty
	}
	if !IsKnownKind(c.Kind) {
		return fmt.Errorf("%w: %q", ErrUnknownKind, c.Kind)
	}
	if c.IssuingAuthority == "" {
		return ErrEmptyIssuingAuthority
	}
	if len(c.EvidenceIDs) == 0 {
		return ErrNoEvidence
	}
	if c.ExpiresAtTick != 0 && c.ExpiresAtTick < c.IssuedAtTick {
		return ErrExpiresBeforeIssued
	}
	return nil
}

// StatusAt derives c's Status at tick — ACTIVE, EXPIRED (past
// ExpiresAtTick with no revocation), or REVOKED (revocation always
// wins, even if it happened before a since-passed expiry).
func (c Credential) StatusAt(tick uint64) Status {
	if c.revoked && c.revokedAtTick <= tick {
		return StatusRevoked
	}
	if c.ExpiresAtTick != 0 && tick > c.ExpiresAtTick {
		return StatusExpired
	}
	return StatusActive
}

// EffectiveAt reports whether c genuinely authorises its holder at
// tick — ACTIVE and past its own IssuedAtTick.
func (c Credential) EffectiveAt(tick uint64) bool {
	return tick >= c.IssuedAtTick && c.StatusAt(tick) == StatusActive
}

// ---- QualificationRecord ------------------------------------------------

// QualificationRecord is the structured, evidenced record of one
// external qualification attestation — closing the gap that
// network.QualificationState alone is a bare enum with no record of
// who attested it. Reuses network.QualificationState and
// network.RegistrySource rather than declaring parallel vocabularies.
//
// Round 10 adds the fields the reviewer's own docx names as missing —
// "QualificationRecord harus mampu menjawab: siapa yang qualify, untuk
// apa, berdasarkan credential apa, jurisdiction mana, effective date,
// expiry, issuer, verification source, revoked, delegated authority,
// scope, evidence, requalification" (who qualifies, for what, on which
// credential, which jurisdiction, effective date, expiry, issuer,
// verification source, revoked, delegated authority, scope, evidence,
// requalification). Each is answered as follows:
//
//   - siapa/untuk apa: PartyID/Role (already present).
//   - berdasarkan credential apa: CredentialID (already present).
//   - jurisdiction: Jurisdiction (new).
//   - effective date/expiry: EffectiveAtTick/ExpiresAtTick (new).
//   - issuer: Issuer (new) — distinct from Source: Source is WHICH
//     REGISTRY attested it (network.RegistrySource); Issuer is the
//     real-world body that issued the underlying credential (e.g. "The
//     International Group of P&I Clubs"), matching
//     credential.Credential.IssuingAuthority's own reasoning applied
//     here for qualifications attested without a linked Credential.
//   - verification source: Source (already present).
//   - revoked: State == network.StateRevoked (already representable —
//     see RevocationReason below for the required reason).
//   - delegated authority: DelegatedAuthorityRelationshipID (new) —
//     REUSES party.Relationship (by RelationshipID reference) rather
//     than re-deriving a second delegation model, matching this
//     domain's own REUSE discipline exactly as pkg/insurance/credential's
//     own package doc comment already establishes for Organization/
//     Jurisdiction/Authority.
//   - scope: Scope (new).
//   - evidence: EvidenceIDs (already present).
//   - requalification: this Registry is append-only (RecordQualification
//     never overwrites) — a later QualificationRecord for the same
//     PartyID+Role IS a requalification by construction; see
//     QualificationHistory/CurrentQualification/EffectiveQualificationAt.
type QualificationRecord struct {
	PartyID party.PartyID              `json:"party_id"`
	Role    party.Role                 `json:"role"`
	State   network.QualificationState `json:"state"`
	// Source names WHICH external registry (pkg/insurance/network's own
	// six) attested this qualification — empty is valid only for
	// StateSelfAttested (a self-attestation has no external source by
	// definition).
	Source network.RegistrySource `json:"source,omitempty"`
	// CredentialID, if non-empty, names the Credential this
	// qualification rests on (e.g. the P&I Club membership certificate
	// backing a StateExternallyVerified attestation).
	CredentialID string   `json:"credential_id,omitempty"`
	EvidenceIDs  []string `json:"evidence_ids,omitempty"`

	// Jurisdiction is where this qualification is recognised (e.g. "England
	// and Wales") — free text, matching Credential.Jurisdiction's own
	// reasoning.
	Jurisdiction string `json:"jurisdiction,omitempty"`
	// Issuer is the real-world body that issued the underlying
	// qualification, distinct from Source (which registry ATTESTED it,
	// not which body ISSUED it — a corporate registry can attest a
	// qualification issued by a completely different body).
	Issuer string `json:"issuer,omitempty"`
	// EffectiveAtTick/ExpiresAtTick bound when this specific
	// QualificationRecord is in force — ExpiresAtTick == 0 means
	// open-ended, matching Credential.ExpiresAtTick's own convention.
	EffectiveAtTick uint64 `json:"effective_at_tick"`
	ExpiresAtTick   uint64 `json:"expires_at_tick,omitempty"`
	// Scope bounds WHAT this qualification covers (e.g. "cargo damage
	// surveys only; hull surveys out of scope") — free text, matching
	// party.Relationship.Scope's own reasoning.
	Scope string `json:"scope,omitempty"`
	// DelegatedAuthorityRelationshipID, if non-empty, names the
	// party.Relationship.RelationshipID this qualification's own
	// authority is delegated FROM — reused by reference, never a second
	// delegation chain (see this type's own doc comment).
	DelegatedAuthorityRelationshipID string `json:"delegated_authority_relationship_id,omitempty"`
	// RevocationReason is required non-empty when State ==
	// network.StateRevoked, matching party.Relationship's own
	// "REVOKED needs a stated reason" rule.
	RevocationReason string `json:"revocation_reason,omitempty"`

	RecordedBy     party.PartyID `json:"recorded_by"`
	RecordedAtTick uint64        `json:"recorded_at_tick"`
}

var (
	ErrEmptyQualificationParty             = errors.New("credential: QualificationRecord.PartyID must be non-empty")
	ErrUnknownQualificationRole            = errors.New("credential: unknown QualificationRecord.Role")
	ErrUnknownQualificationState           = errors.New("credential: unknown QualificationRecord.State")
	ErrExternallyVerifiedNeedsSource       = errors.New("credential: StateExternallyVerified requires a Source registry -- an external verification with no named external source did not happen")
	ErrEmptyRecordedBy                     = errors.New("credential: QualificationRecord.RecordedBy must be non-empty")
	ErrRevokedNeedsReason                  = errors.New("credential: State is StateRevoked but RevocationReason is empty")
	ErrQualificationExpiresBeforeEffective = errors.New("credential: ExpiresAtTick must be 0 (open) or >= EffectiveAtTick")
)

// Validate checks q's own internal consistency, including the same
// "an externally verified state needs a named external source" rule
// this domain applies everywhere else evidence-or-it-didn't-happen is
// enforced, and the same "revoked needs a reason" rule
// party.Relationship already enforces for relationships.
func (q QualificationRecord) Validate() error {
	if q.PartyID == "" {
		return ErrEmptyQualificationParty
	}
	if !party.IsKnownRole(q.Role) {
		return fmt.Errorf("%w: %q", ErrUnknownQualificationRole, q.Role)
	}
	if !network.IsKnownQualificationState(q.State) {
		return fmt.Errorf("%w: %q", ErrUnknownQualificationState, q.State)
	}
	if q.State == network.StateExternallyVerified {
		if q.Source == "" || !network.IsKnownRegistrySource(q.Source) {
			return ErrExternallyVerifiedNeedsSource
		}
		if !q.Source.RoutesRole(q.Role) {
			return fmt.Errorf("credential: %w: %s is not a modelled qualification authority for role %s", network.ErrRegistryMisroute, q.Source, q.Role)
		}
	}
	if q.State == network.StateRevoked && q.RevocationReason == "" {
		return ErrRevokedNeedsReason
	}
	if q.ExpiresAtTick != 0 && q.ExpiresAtTick < q.EffectiveAtTick {
		return ErrQualificationExpiresBeforeEffective
	}
	if q.RecordedBy == "" {
		return ErrEmptyRecordedBy
	}
	return nil
}

// EffectiveAt reports whether q is genuinely in force at tick: not
// StateRevoked, past its own EffectiveAtTick, and not yet past
// ExpiresAtTick (0 == unbounded) — mirrors Credential.EffectiveAt
// exactly.
func (q QualificationRecord) EffectiveAt(tick uint64) bool {
	if q.State == network.StateRevoked {
		return false
	}
	if tick < q.EffectiveAtTick {
		return false
	}
	if q.ExpiresAtTick != 0 && tick > q.ExpiresAtTick {
		return false
	}
	return true
}

// ---- Registry --------------------------------------------------------

var (
	ErrDuplicateCredentialID = errors.New("credential: CredentialID already registered")
	ErrCredentialNotFound    = errors.New("credential: CredentialID not found")
	ErrEmptyRevocationReason = errors.New("credential: revocation reason must be non-empty")
	ErrAlreadyRevoked        = errors.New("credential: credential is already revoked")
)

// Registry holds every Credential and QualificationRecord this
// deployment has recorded, across every party — deliberately NOT
// case-scoped (unlike party.RelationshipRegistry): a party's licence
// or P&I membership is a fact about the PARTY, independent of which
// case it is currently relevant to, matching how a real accreditation
// register works.
type Registry struct {
	mu              sync.RWMutex
	credentials     map[string]*Credential // CredentialID -> credential
	credentialOrder []string
	qualifications  []QualificationRecord // append-only, latest-wins on read
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{credentials: make(map[string]*Credential)}
}

// RegisterCredential validates and adds a new Credential.
func (r *Registry) RegisterCredential(c Credential) error {
	if err := c.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.credentials[c.CredentialID]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateCredentialID, c.CredentialID)
	}
	cc := c
	r.credentials[c.CredentialID] = &cc
	r.credentialOrder = append(r.credentialOrder, c.CredentialID)
	return nil
}

// Credential returns the credential for id.
func (r *Registry) Credential(id string) (Credential, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.credentials[id]
	if !ok {
		return Credential{}, false
	}
	return *c, true
}

// RevokeCredential withdraws a credential at tick, with a stated
// reason — matching RelationshipRegistry.Revoke's own append-only,
// reason-required, never-deleted discipline.
func (r *Registry) RevokeCredential(id string, atTick uint64, reason string) error {
	if reason == "" {
		return ErrEmptyRevocationReason
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.credentials[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrCredentialNotFound, id)
	}
	if c.revoked {
		return ErrAlreadyRevoked
	}
	c.revoked = true
	c.revokedAtTick = atTick
	c.revocationReason = reason
	return nil
}

// CredentialsFor returns every credential PartyID holds, in
// registration order.
func (r *Registry) CredentialsFor(p party.PartyID) []Credential {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Credential
	for _, id := range r.credentialOrder {
		c := r.credentials[id]
		if c.PartyID == p {
			out = append(out, *c)
		}
	}
	return out
}

// EffectiveCredential returns the first credential of the given Kind
// for p that is EffectiveAt tick, if any.
func (r *Registry) EffectiveCredential(p party.PartyID, kind Kind, tick uint64) (Credential, bool) {
	for _, c := range r.CredentialsFor(p) {
		if c.Kind == kind && c.EffectiveAt(tick) {
			return c, true
		}
	}
	return Credential{}, false
}

// RecordQualification validates and appends q. Qualifications are
// append-only (a party's qualification standing can change over time,
// e.g. StateSelfAttested -> StateExternallyVerified -> StateRevoked;
// each is a fact about a point in time, never overwritten).
func (r *Registry) RecordQualification(q QualificationRecord) error {
	if err := q.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.qualifications = append(r.qualifications, q)
	return nil
}

// QualificationHistory returns every recorded QualificationRecord for
// p in role, oldest first.
func (r *Registry) QualificationHistory(p party.PartyID, role party.Role) []QualificationRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []QualificationRecord
	for _, q := range r.qualifications {
		if q.PartyID == p && q.Role == role {
			out = append(out, q)
		}
	}
	return out
}

// CurrentQualification returns the MOST RECENTLY recorded
// QualificationRecord for p in role, if any.
func (r *Registry) CurrentQualification(p party.PartyID, role party.Role) (QualificationRecord, bool) {
	hist := r.QualificationHistory(p, role)
	if len(hist) == 0 {
		return QualificationRecord{}, false
	}
	return hist[len(hist)-1], true
}

// EffectiveQualificationAt finds the record most recently in effect as
// of tick — the LATEST QualificationRecord (by RecordedAtTick) that was
// itself already recorded at or before tick — and reports it only if
// THAT specific record is EffectiveAt(tick). This is the
// requalification-aware, point-in-time answer: a later record for the
// same PartyID+Role always supersedes an earlier one once its own
// RecordedAtTick has passed, and a later REVOCATION is authoritative
// going forward — it is never skipped in favour of falling back to an
// earlier, still-nominally-active record, which is exactly the
// property that makes this Registry's append-only history a real
// requalification trail rather than a set of independently-checked
// facts.
func (r *Registry) EffectiveQualificationAt(p party.PartyID, role party.Role, tick uint64) (QualificationRecord, bool) {
	hist := r.QualificationHistory(p, role)
	var current QualificationRecord
	found := false
	for _, q := range hist {
		if q.RecordedAtTick > tick {
			break
		}
		current = q
		found = true
	}
	if !found || !current.EffectiveAt(tick) {
		return QualificationRecord{}, false
	}
	return current, true
}
