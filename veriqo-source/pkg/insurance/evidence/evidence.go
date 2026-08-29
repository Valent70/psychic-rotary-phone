// Package evidence is VICE's insurance-specific evidence layer —
// blueprint §7-§12. It does NOT reimplement evidence hashing, signing,
// bitemporality or epistemic classification: every Record wraps a real
// pkg/evidence/ontology.Evidence, content-addressed and validated by
// that package alone (blueprint §1: "Jangan duplikasi Evidence Engine
// ... yang sudah ada. VICE menggunakan engine yang sudah ada.").
//
// What this package adds on top, because ontology.Evidence is
// domain-neutral and insurance claims need more:
//
//   - Origin classification (§7) — which party submitted this, with the
//     hard rule the blueprint states explicitly: "Evidence origin ≠
//     evidence truth". Origin is metadata, never a trust weight applied
//     silently.
//   - A verification status lifecycle (§8).
//   - Multi-dimensional evidence strength (§9) — nine independently
//     rated dimensions, never collapsed into one confidence scalar.
//   - A dependency graph (§10) so that Photo A -> Statement A -> Survey A
//     is never double-counted as three independent sources.
//   - Declared source interest (§11) — visible to a reviewer, never used
//     by this package to mark evidence false.
package evidence

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"veriqo/pkg/evidence/ontology"
	"veriqo/pkg/evidence/provenance"
	"veriqo/pkg/insurance/party"
)

// Origin is who submitted this evidence into the case — blueprint §7.
// Deliberately NOT a trust signal: "Evidence origin ≠ evidence truth"
// is not a comment, it is the reason Status (below) is a SEPARATE
// field this package never derives from Origin.
type Origin string

const (
	OriginClaimant    Origin = "CLAIMANT_EVIDENCE"
	OriginRespondent  Origin = "RESPONDENT_EVIDENCE"
	OriginInsurer     Origin = "INSURER_EVIDENCE"
	OriginSurveyor    Origin = "SURVEYOR_EVIDENCE"
	OriginCarrier     Origin = "CARRIER_EVIDENCE"
	OriginIndependent Origin = "INDEPENDENT_EVIDENCE"
	OriginRegulatory  Origin = "REGULATORY_EVIDENCE"
	OriginSystem      Origin = "SYSTEM_EVIDENCE"
)

var knownOrigins = map[Origin]bool{
	OriginClaimant: true, OriginRespondent: true, OriginInsurer: true,
	OriginSurveyor: true, OriginCarrier: true, OriginIndependent: true,
	OriginRegulatory: true, OriginSystem: true,
}

// IsKnownOrigin reports whether o is a modelled origin classification.
func IsKnownOrigin(o Origin) bool { return knownOrigins[o] }

// KnownOrigins returns every modelled origin in deterministic order.
func KnownOrigins() []Origin {
	out := make([]Origin, 0, len(knownOrigins))
	for o := range knownOrigins {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Status is the verification-status lifecycle a Record moves through —
// blueprint §8.
type Status string

const (
	StatusUnverified            Status = "UNVERIFIED"
	StatusAuthenticitySupported Status = "AUTHENTICITY_SUPPORTED"
	StatusAuthenticityDisputed  Status = "AUTHENTICITY_DISPUTED"
	StatusAlterationDetected    Status = "ALTERATION_DETECTED"
	StatusIncomplete            Status = "INCOMPLETE"
	StatusCorroborated          Status = "CORROBORATED"
	StatusContradicted          Status = "CONTRADICTED"
)

var knownStatuses = map[Status]bool{
	StatusUnverified: true, StatusAuthenticitySupported: true, StatusAuthenticityDisputed: true,
	StatusAlterationDetected: true, StatusIncomplete: true, StatusCorroborated: true, StatusContradicted: true,
}

// IsKnownStatus reports whether s is a modelled verification status.
func IsKnownStatus(s Status) bool { return knownStatuses[s] }

// Interest is a declared conflict-of-interest classification for an
// evidence source — blueprint §11. The blueprint is explicit:
// "Interest ≠ False". This package only surfaces (Interest,
// Relationship, PotentialConflict); it never converts a declared
// interest into a truth judgement.
type Interest string

const (
	InterestClaimant    Interest = "CLAIMANT_INTEREST"
	InterestInsurer     Interest = "INSURER_INTEREST"
	InterestCarrier     Interest = "CARRIER_INTEREST"
	InterestCommercial  Interest = "COMMERCIAL_INTEREST"
	InterestIndependent Interest = "INDEPENDENT"
	InterestRegulatory  Interest = "REGULATORY"
	InterestUnknown     Interest = "UNKNOWN"
)

var knownInterests = map[Interest]bool{
	InterestClaimant: true, InterestInsurer: true, InterestCarrier: true, InterestCommercial: true,
	InterestIndependent: true, InterestRegulatory: true, InterestUnknown: true,
}

// IsKnownInterest reports whether i is a modelled interest classification.
func IsKnownInterest(i Interest) bool { return knownInterests[i] }

// SourceInterest is the declared-interest record for one evidence
// source (§11): the interest itself, its relationship to the case, and
// whether that constitutes a POTENTIAL conflict — never an assertion
// the evidence is compromised.
type SourceInterest struct {
	Interest          Interest `json:"interest"`
	Relationship      string   `json:"relationship,omitempty"`
	PotentialConflict bool     `json:"potential_conflict"`
}

// ---- Evidence strength: nine independently-rated dimensions (§9) ----
//
// The blueprint's own words: "Jangan menggunakan satu angka confidence
// yang terlalu sederhana." Each dimension below has its OWN small,
// meaningful value set — collapsing "authenticity" and "relevance"
// into the same three-value enum would silently imply they mean the
// same thing, which they do not.

type AuthenticityRating string

const (
	AuthenticitySupported AuthenticityRating = "SUPPORTED"
	AuthenticityDisputed  AuthenticityRating = "DISPUTED"
	AuthenticityUnknown   AuthenticityRating = "UNKNOWN"
)

type ProvenanceRating string

const (
	ProvenanceVerified   ProvenanceRating = "VERIFIED"
	ProvenanceUnverified ProvenanceRating = "UNVERIFIED"
	ProvenanceUnknown    ProvenanceRating = "UNKNOWN"
)

type IntegrityRating string

const (
	IntegrityVerified    IntegrityRating = "VERIFIED"
	IntegrityCompromised IntegrityRating = "COMPROMISED"
	IntegrityUnknown     IntegrityRating = "UNKNOWN"
)

type CompletenessRating string

const (
	CompletenessComplete     CompletenessRating = "COMPLETE"
	CompletenessPartial      CompletenessRating = "PARTIAL"
	CompletenessInsufficient CompletenessRating = "INSUFFICIENT"
)

type RelevanceRating string

const (
	RelevanceHigh   RelevanceRating = "HIGH"
	RelevanceMedium RelevanceRating = "MEDIUM"
	RelevanceLow    RelevanceRating = "LOW"
)

type TemporalConsistencyRating string

const (
	TemporalConsistencySupported TemporalConsistencyRating = "SUPPORTED"
	TemporalConsistencyDisputed  TemporalConsistencyRating = "DISPUTED"
	TemporalConsistencyUnknown   TemporalConsistencyRating = "UNKNOWN"
)

type EntityConsistencyRating string

const (
	EntityConsistencySupported EntityConsistencyRating = "SUPPORTED"
	EntityConsistencyDisputed  EntityConsistencyRating = "DISPUTED"
	EntityConsistencyUnknown   EntityConsistencyRating = "UNKNOWN"
)

type CorroborationRating string

const (
	CorroborationHigh   CorroborationRating = "HIGH"
	CorroborationMedium CorroborationRating = "MEDIUM"
	CorroborationLow    CorroborationRating = "LOW"
	CorroborationNone   CorroborationRating = "NONE"
)

type ContradictionLevelRating string

const (
	ContradictionLevelNone   ContradictionLevelRating = "NONE"
	ContradictionLevelLow    ContradictionLevelRating = "LOW"
	ContradictionLevelMedium ContradictionLevelRating = "MEDIUM"
	ContradictionLevelHigh   ContradictionLevelRating = "HIGH"
)

// Strength is the nine-dimensional evidence-strength assessment from
// §9's own worked example. A zero-value Strength (every field empty)
// is meaningfully different from one where every dimension is
// explicitly "UNKNOWN"/"NONE" — the former means "not yet assessed",
// which Validate refuses to let flow downstream as if it were assessed.
type Strength struct {
	Authenticity             AuthenticityRating        `json:"authenticity"`
	Provenance               ProvenanceRating          `json:"provenance"`
	Integrity                IntegrityRating           `json:"integrity"`
	Completeness             CompletenessRating        `json:"completeness"`
	Relevance                RelevanceRating           `json:"relevance"`
	TemporalConsistency      TemporalConsistencyRating `json:"temporal_consistency"`
	EntityConsistency        EntityConsistencyRating   `json:"entity_consistency"`
	IndependentCorroboration CorroborationRating       `json:"independent_corroboration"`
	ContradictionLevel       ContradictionLevelRating  `json:"contradiction_level"`
}

// ErrStrengthNotAssessed is returned by Validate when Strength is the
// zero value — every dimension unset, which this package treats as
// "not yet assessed" rather than silently defaulting to an optimistic
// rating.
var ErrStrengthNotAssessed = errors.New("evidence: strength has not been assessed (all dimensions empty)")

// Validate reports whether every populated dimension of s carries a
// recognised value, and refuses a completely-unassessed Strength
// (every field the zero value) from being treated as valid input to
// downstream analysis.
func (s Strength) Validate() error {
	if s == (Strength{}) {
		return ErrStrengthNotAssessed
	}
	return nil
}

// ---- The Record itself (§8) ----

var (
	ErrEmptyCaseID       = errors.New("evidence: CaseID must be non-empty")
	ErrEmptySourceParty  = errors.New("evidence: SourcePartyID must be non-empty")
	ErrUnknownOrigin     = errors.New("evidence: unknown Origin")
	ErrUnderlyingInvalid = errors.New("evidence: Underlying is not a valid ontology.Evidence (call ontology.New first)")
)

// Record is one piece of case evidence as VICE sees it: a real,
// content-addressed pkg/evidence/ontology.Evidence, plus the
// insurance-specific fields the blueprint requires on top of it.
type Record struct {
	CaseID string `json:"case_id"`

	// Underlying is the real Unified Evidence Engine record. Its own
	// EvidenceID (content-addressed, immutable) is this Record's
	// identity too — there is deliberately no separate insurance-local
	// evidence ID.
	Underlying ontology.Evidence `json:"underlying"`

	SourcePartyID party.PartyID `json:"source_party_id"`
	Origin        Origin        `json:"origin"`
	DocumentType  string        `json:"document_type,omitempty"`
	Originality   string        `json:"originality,omitempty"`

	// ChainOfCustody records each hand-off this evidence is known to
	// have passed through before reaching VICE, oldest first.
	ChainOfCustody []CustodyEntry `json:"chain_of_custody,omitempty"`

	RelatedEntities  []string `json:"related_entities,omitempty"`
	RelatedContracts []string `json:"related_contracts,omitempty"`
	RelatedEvents    []string `json:"related_events,omitempty"`

	Status         Status         `json:"status"`
	Strength       Strength       `json:"strength"`
	SourceInterest SourceInterest `json:"source_interest"`

	// Rights is what VERIQO is legally permitted to do with this
	// evidence right now. It is deliberately pkg/evidence/provenance's
	// OWN RightsState -- the canonical rights vocabulary already used by
	// the connector layer, the evidence envelope and the qualification
	// registry -- not a second, insurance-local rights model. The
	// functional spec's §22 list (UNKNOWN / AUTHORIZED / RESTRICTED /
	// CUSTOMER_ONLY / ... / EXPIRED / REVOKED) is that same vocabulary
	// under different words; adopting a parallel enum here would be
	// exactly the duplication §3 forbids.
	//
	// New() sets this to provenance.RightsUnknownPendingContract, which
	// permits internal use only. It is never left as the empty string on
	// a constructed Record, because an unrecognised state permits
	// NOTHING (see Permits) and a silently-empty field would therefore
	// read as a hard denial rather than as "not yet contracted".
	Rights provenance.RightsState `json:"rights"`

	// CorrectionSuperseded marks this record as replaced by a later one.
	// A superseded record permits no use at all, whatever Rights says --
	// the same rule provenance.ExternalEvidence.Permits applies. The
	// superseding record is a NEW Record with its own content-addressed
	// EvidenceID; nothing here ever edits the original (spec §72: "a
	// future correction cannot rewrite historical truth").
	CorrectionSuperseded bool `json:"correction_superseded,omitempty"`

	// SupersededBy names the EvidenceID of the record that replaced this
	// one, when CorrectionSuperseded is set.
	SupersededBy string `json:"superseded_by,omitempty"`

	// The five fields below are additive (Round 5's Real-World Evidence
	// Qualification Layer, §3): every one defaults to empty and every
	// existing caller's behavior is unchanged. Free text, matching
	// Authority/ContractualBasis's own convention in
	// party/relationship.go: a closed taxonomy of every real-world
	// acquisition method, license or access policy would be a worse fit
	// than a stated reason a reviewer can read directly.

	// SourceAuthority names the specific authority the source claims for
	// this assertion (e.g. "vessel master's own logbook entry", "port
	// authority's official berth log") -- distinct from Origin (WHO
	// submitted it into the case) and from SourcePartyID (the party
	// record): this is what STANDING that party claims to make the
	// assertion.
	SourceAuthority string `json:"source_authority,omitempty"`
	// AcquisitionMethod records HOW this evidence reached VERIQO (e.g.
	// "manual upload by claims handler", "API pull from AIS provider",
	// "email attachment forwarded by broker").
	AcquisitionMethod string `json:"acquisition_method,omitempty"`
	// LicenseReference cites the specific license/contract clause
	// permitting VERIQO's use of this record, when Rights depends on one
	// (complements Rights itself, which is the enforced provenance.
	// RightsState; this is the human-readable citation behind it).
	LicenseReference string `json:"license_reference,omitempty"`
	// AccessPolicy names who/what may see this record within VERIQO
	// (e.g. "case parties with VIEW_EVIDENCE permission only") --
	// complements party.Permission's own enforcement (the real gate);
	// this is the stated policy a reviewer can check the enforcement
	// against.
	AccessPolicy string `json:"access_policy,omitempty"`
	// RetentionPolicy states how long this record must be kept and why
	// (e.g. "7 years per regulatory retention schedule X"), distinct
	// from pkg/insurance/preservation's own case-level preservation
	// order (which records an active LEGAL HOLD overriding normal
	// retention, not the baseline policy itself).
	RetentionPolicy string `json:"retention_policy,omitempty"`

	Metadata map[string]string `json:"metadata,omitempty"`
}

// Permits is the fail-closed rights gate for one insurance evidence
// record: true only when this record's CURRENT rights state explicitly
// allows use, and never for a superseded record. It delegates the
// decision to provenance.RightsState.Permits -- the single permitted-use
// table in the repository -- so REVOKED and EXPIRED permit nothing here
// for exactly the same reason they permit nothing anywhere else, and an
// unset/unrecognised state permits nothing either.
//
// Possession is never permission: a Record sitting in a Registry says
// only that VERIQO HAS the evidence, never that it may show, derive
// from, or submit it into a dispute.
func (r Record) Permits(use provenance.Use) bool {
	if r.CorrectionSuperseded {
		return false
	}
	return r.Rights.Permits(use)
}

// EvidenceID returns the identity of the underlying ontology evidence
// — the single source of truth for this Record's ID.
func (r Record) EvidenceID() string { return r.Underlying.EvidenceID }

// CustodyEntry is one hand-off in a Record's chain of custody.
type CustodyEntry struct {
	Holder    string `json:"holder"`
	Action    string `json:"action"` // e.g. "received", "copied", "transmitted"
	Tick      uint64 `json:"tick"`
	Reference string `json:"reference,omitempty"`
}

// New constructs a Record from an already-validated ontology.Evidence
// (the caller must have gone through ontology.New — this package never
// mints an EvidenceID itself, matching the blueprint's "Semua evidence
// masuk melalui Unified Evidence Engine").
func New(caseID string, underlying ontology.Evidence, sourcePartyID party.PartyID, origin Origin) (Record, error) {
	if caseID == "" {
		return Record{}, ErrEmptyCaseID
	}
	if underlying.EvidenceID == "" || underlying.EvidenceID != underlying.ComputeID() {
		return Record{}, ErrUnderlyingInvalid
	}
	if sourcePartyID == "" {
		return Record{}, ErrEmptySourceParty
	}
	if !IsKnownOrigin(origin) {
		return Record{}, fmt.Errorf("%w: %q", ErrUnknownOrigin, origin)
	}
	return Record{
		CaseID:        caseID,
		Underlying:    underlying,
		SourcePartyID: sourcePartyID,
		Origin:        origin,
		Status:        StatusUnverified,
		Rights:        provenance.RightsUnknownPendingContract,
	}, nil
}

// ---- Registry: case-scoped evidence store ----

var ErrDuplicateEvidence = errors.New("evidence: this exact evidence (by content-addressed ID) is already in the case")
var ErrEvidenceNotFound = errors.New("evidence: EvidenceID not found in this case")

// Registry holds every Record submitted into one case, keyed by the
// underlying ontology.Evidence's content-addressed ID. Because that ID
// is a hash over every semantic field, submitting the literal same
// evidence twice (even from a different Origin claim) is refused
// outright — that is EXACTLY the double-submission blueprint §35's
// adversarial test #2 ("Duplicate evidence") and #3 ("Same evidence
// submitted by 3 parties") name.
type Registry struct {
	mu      sync.RWMutex
	records map[string]Record // EvidenceID -> Record
	order   []string
}

// NewRegistry returns an empty case evidence registry.
func NewRegistry() *Registry {
	return &Registry{records: make(map[string]Record)}
}

// Submit adds rec to the registry. Refuses an exact duplicate (same
// content-addressed EvidenceID) — see the adversarial-test note above.
// A near-duplicate that is NOT byte-identical (e.g. the same document
// re-OCR'd, blueprint adversarial test #16) gets a DIFFERENT
// EvidenceID and is accepted as a distinct Record; detecting that it
// describes the same underlying fact is the dependency graph's job
// (DependencyGraph, below), not this registry's.
func (reg *Registry) Submit(rec Record) error {
	id := rec.EvidenceID()
	if id == "" {
		return ErrUnderlyingInvalid
	}
	// Every authority-bearing field is reset to its honest starting
	// value here, exactly like New() -- never trusted from rec as
	// given. This closes a Final Authority Hardening Round finding: a
	// caller (or a deserializer, since Record has no unexported fields
	// and no accessor-only sealing the way cre.AuthorizedFinding does)
	// could otherwise hand-build a Record{Status: StatusCorroborated,
	// Rights: provenance.RightsCustomerFacingAllowed, ...} directly and
	// Submit it, bypassing New()'s defaults and every downstream
	// authority gate (VerifyStatus's derivation, SetRights's authority
	// check) entirely -- "serialized/hand-constructed objects must not
	// be sufficient to manufacture an authoritative state" applies to
	// this entry point exactly as much as to New() itself. Descriptive
	// fields the caller legitimately controls (CaseID, Origin,
	// SourcePartyID, DocumentType, ChainOfCustody -- which may
	// genuinely describe hand-offs BEFORE evidence reached VERIQO, and
	// so is not reset here -- RelatedEntities/Contracts/Events, the
	// five qualification fields, Metadata) are left exactly as
	// submitted.
	rec.Status = StatusUnverified
	rec.Strength = Strength{}
	rec.Rights = provenance.RightsUnknownPendingContract
	rec.CorrectionSuperseded = false
	rec.SupersededBy = ""
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if _, exists := reg.records[id]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateEvidence, id)
	}
	reg.records[id] = rec
	reg.order = append(reg.order, id)
	return nil
}

// Get returns the Record for evidenceID.
func (reg *Registry) Get(evidenceID string) (Record, bool) {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	r, ok := reg.records[evidenceID]
	return r, ok
}

// DeriveStatus computes the ONLY Status a Record may legitimately carry
// for a given Strength assessment -- the authoritative replacement for
// the removed Registry.SetStatus, which let any caller assign ANY
// Status (including StatusCorroborated or StatusAuthenticitySupported)
// with nothing behind it: a real, structural trust bypass -- an
// Authority Boundary Audit finding this function and VerifyStatus close.
// s must already be a real assessment (Validate rejects the zero
// value); this never accepts an unassessed Strength as license to
// guess a Status.
//
// The mapping is a decomposed priority chain, not a weighted score --
// matching this repository's own guardrail against collapsing
// multi-dimensional evidence into one opaque confidence number, and the
// same discipline causation.computeStatus already uses. Order matters:
// an integrity compromise is checked first because a tampered record's
// derived Status must never read as "supported" no matter how
// corroborated it otherwise looks; net contradiction is checked next
// for the same reason; then authenticity/temporal/entity disputes; then
// incompleteness. Only once none of those fire does corroboration or
// plain authenticity support determine the result; a Strength with
// nothing conclusive either way derives StatusUnverified -- the same
// fail-closed default New() already assigns.
func DeriveStatus(s Strength) (Status, error) {
	if err := s.Validate(); err != nil {
		return "", err
	}
	switch {
	case s.Integrity == IntegrityCompromised:
		return StatusAlterationDetected, nil
	case s.ContradictionLevel == ContradictionLevelHigh || s.ContradictionLevel == ContradictionLevelMedium:
		return StatusContradicted, nil
	case s.Authenticity == AuthenticityDisputed,
		s.TemporalConsistency == TemporalConsistencyDisputed,
		s.EntityConsistency == EntityConsistencyDisputed:
		return StatusAuthenticityDisputed, nil
	case s.Completeness == CompletenessInsufficient:
		return StatusIncomplete, nil
	case (s.IndependentCorroboration == CorroborationHigh || s.IndependentCorroboration == CorroborationMedium) &&
		s.ContradictionLevel == ContradictionLevelNone:
		return StatusCorroborated, nil
	case s.Authenticity == AuthenticitySupported:
		return StatusAuthenticitySupported, nil
	default:
		return StatusUnverified, nil
	}
}

// VerifyStatus derives evidenceID's Status from its own already-recorded
// Strength assessment via DeriveStatus, stores the result, and returns
// it -- the ONLY way a Record's Status changes after New(). This
// replaces the removed SetStatus, under which any caller could assign
// any Status with no relationship whatsoever to the record's own
// recorded evidence (pkg/insurance/api's Facade.VerifyEvidence took a
// caller-supplied map[string]Status directly, so an external caller
// could hand any evidence ID StatusCorroborated with nothing backing
// the claim). Refuses (ErrStrengthNotAssessed) when no real Strength
// assessment has been recorded yet via SetStrength -- there is nothing
// to derive a Status from. Safe to call again after a later SetStrength
// call re-assesses the same record: Status always reflects whatever
// Strength is currently on file, never a stale, independently-drifted
// value.
func (reg *Registry) VerifyStatus(evidenceID string) (Status, error) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	r, ok := reg.records[evidenceID]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrEvidenceNotFound, evidenceID)
	}
	status, err := DeriveStatus(r.Strength)
	if err != nil {
		return "", err
	}
	r.Status = status
	reg.records[evidenceID] = r
	return status, nil
}

// SetStrength updates a Record's multi-dimensional strength assessment.
func (reg *Registry) SetStrength(evidenceID string, s Strength) error {
	if err := s.Validate(); err != nil {
		return err
	}
	reg.mu.Lock()
	defer reg.mu.Unlock()
	r, ok := reg.records[evidenceID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrEvidenceNotFound, evidenceID)
	}
	r.Strength = s
	reg.records[evidenceID] = r
	return nil
}

// ErrUnknownRightsState is returned by SetRights for a value
// pkg/evidence/provenance does not model. Never coerced to a default:
// an unmodelled rights state is a fact about a broken ingest, not a
// reason to guess.
var ErrUnknownRightsState = errors.New("evidence: unknown provenance.RightsState")

// ErrRightsPermitNothing is returned by PermittedFor's strict sibling
// RequirePermitted when a record's rights do not allow the requested
// use. Naming it separately (rather than returning a bare false) lets
// callers that MUST fail closed -- dossier export, dispute submission --
// surface WHY.
var ErrRightsPermitNothing = errors.New("evidence: this record's rights state does not permit the requested use")

// ErrRightsGrantNotAuthorized is SetRights's refusal when authorityID
// does not name a genuinely trusted entity in the given
// provenance.Registry -- see SetRights's own doc comment for why this
// gate exists.
var ErrRightsGrantNotAuthorized = errors.New("evidence: rights can only be set by an entity whose trust has actually been granted in the given provenance.Registry")

// SetRights updates a Record's rights state in place -- but ONLY when
// authorityID names a real entity in provReg whose trust has actually
// been granted (Entry.TrustGranted, set only by a real, attributed
// provenance.Registry.GrantTrust call recording a policy reference, an
// actor, and a tick -- never by this function, and never by any
// caller-supplied flag). This closes an Authority Boundary Audit
// finding named explicitly in Authority Round 2
// (Perlu_ditutup_dan_ditingkatkan.docx item 9): "Kalau caller dapat
// melakukan SetRights(...), maka pertanyaan kita: Siapa yang memiliki
// authority untuk memberikan rights? Harus ada authoritative source"
// (Policy + Identity + Authorization + Governance event -- not a bare
// "caller -> SetRights()" call). Reuses pkg/evidence/provenance's own
// existing trust-grant model rather than inventing a second one:
// GrantTrust is already the sole way an Entry's TrustGranted becomes
// true, already requires a non-empty PolicyRef (and an AttestationRef
// too, for an EVIDENCE_PROVIDER), and already records who granted it
// and when.
//
// This remains, as before, a RECORDING operation: rights are still
// granted or revoked by a legal/commercial act elsewhere, and this
// function never derives a rights state from Origin, Status, or
// possession. What changes is that "elsewhere" must now be a real,
// attributed grant on file in provReg, not merely a function call any
// caller with a *Registry reference could make.
func (reg *Registry) SetRights(evidenceID string, state provenance.RightsState, provReg *provenance.Registry, authorityID string) error {
	if !provenance.IsKnownRightsState(state) {
		return fmt.Errorf("%w: %q", ErrUnknownRightsState, state)
	}
	if provReg == nil {
		return fmt.Errorf("%w: no provenance registry supplied to authorize this grant", ErrRightsGrantNotAuthorized)
	}
	authority, ok := provReg.Get(authorityID)
	if !ok || !authority.TrustGranted {
		return fmt.Errorf("%w: %s", ErrRightsGrantNotAuthorized, authorityID)
	}
	reg.mu.Lock()
	defer reg.mu.Unlock()
	r, ok := reg.records[evidenceID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrEvidenceNotFound, evidenceID)
	}
	r.Rights = state
	reg.records[evidenceID] = r
	return nil
}

var (
	// ErrEmptySupersessionActor is MarkSuperseded's refusal when actor
	// is blank -- "siapa yang melakukan supersession" (who performed
	// the supersession) must be on file, not merely inferable.
	ErrEmptySupersessionActor = errors.New("evidence: MarkSuperseded requires a non-empty actor")
	// ErrEmptySupersessionReason is MarkSuperseded's refusal when
	// reason is blank -- "mengapa" (why) must be recorded, not left to
	// be reconstructed later from context that may not survive.
	ErrEmptySupersessionReason = errors.New("evidence: MarkSuperseded requires a non-empty reason")
	// ErrAlreadySuperseded is MarkSuperseded's refusal when
	// supersededID has already been superseded once -- a second call
	// silently rewriting SupersededBy would break the very lineage
	// this function exists to keep immutable.
	ErrAlreadySuperseded = errors.New("evidence: this record has already been superseded and cannot be superseded again")
	// ErrIllegitimateSuccessor is MarkSuperseded's refusal when
	// bySupersedingID itself already reads as superseded -- "apakah B
	// legitimate successor" (is B a legitimate successor): a record
	// that is not itself current cannot be the new current version.
	ErrIllegitimateSuccessor = errors.New("evidence: the superseding record is itself already superseded, not a legitimate current successor")
)

// MarkSuperseded records that supersededID has been replaced by
// bySupersedingID, attributed to actor, for reason, at tick -- "siapa,
// mengapa, kapan" (who, why, when), the audit trail Authority Round 2
// (Perlu_ditutup_dan_ditingkatkan.docx item 10) named explicitly as
// missing: "Jangan sampai: caller -> MarkSuperseded(A) -> A disappears
// from effective evidence. Itu bisa sangat berbahaya bagi audit trail."
// It does NOT delete, edit or overwrite the superseded record's content
// -- the original Record and its content-addressed EvidenceID stay
// exactly as submitted, which is what makes the earlier state still
// replayable. Only the correction markers change (CorrectionSuperseded,
// SupersededBy, and one new entry appended to ChainOfCustody -- an
// already-existing field this package previously left unpopulated,
// reused here rather than inventing a second audit-trail mechanism),
// and from that moment Permits denies every use of the superseded
// record. A never disappears: Get and All still return it, with its
// full history intact, exactly as MarkSuperseded's own doc comment
// always promised for its content -- this closes the gap between that
// promise and the audit trail actually proving it.
//
// Refuses a record that has already been superseded (ErrAlreadySuperseded)
// -- a second call cannot silently rewrite which record replaced it --
// and refuses a successor that is itself already superseded
// (ErrIllegitimateSuccessor) -- a non-current record can never become
// the new current one. What this function does NOT do: automatically
// re-evaluate any prior decision that cited supersededID (the
// reviewer's own "apakah keputusan yang sebelumnya menggunakan A harus
// dire-evaluate" question) -- that is a downstream consumer's
// responsibility, not this registry's; a consumer that needs to know
// should check CorrectionSuperseded on every record it still holds a
// reference to before relying on it again.
func (reg *Registry) MarkSuperseded(supersededID, bySupersedingID, actor, reason string, tick uint64) error {
	if supersededID == bySupersedingID {
		return errors.New("evidence: a record cannot supersede itself")
	}
	if strings.TrimSpace(actor) == "" {
		return ErrEmptySupersessionActor
	}
	if strings.TrimSpace(reason) == "" {
		return ErrEmptySupersessionReason
	}
	reg.mu.Lock()
	defer reg.mu.Unlock()
	old, ok := reg.records[supersededID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrEvidenceNotFound, supersededID)
	}
	if old.CorrectionSuperseded {
		return fmt.Errorf("%w: %s (already superseded by %s)", ErrAlreadySuperseded, supersededID, old.SupersededBy)
	}
	successor, ok := reg.records[bySupersedingID]
	if !ok {
		return fmt.Errorf("%w: superseding record %s", ErrEvidenceNotFound, bySupersedingID)
	}
	if successor.CorrectionSuperseded {
		return fmt.Errorf("%w: %s", ErrIllegitimateSuccessor, bySupersedingID)
	}
	old.CorrectionSuperseded = true
	old.SupersededBy = bySupersedingID
	old.ChainOfCustody = append(append([]CustodyEntry(nil), old.ChainOfCustody...), CustodyEntry{
		Holder: actor, Action: "SUPERSEDED", Tick: tick,
		Reference: fmt.Sprintf("superseded by %s: %s", bySupersedingID, reason),
	})
	reg.records[supersededID] = old
	return nil
}

// RequirePermitted is the fail-closed accessor every export-shaped
// caller must use instead of Get: it returns the record only when its
// rights genuinely permit use, and a named error otherwise.
func (reg *Registry) RequirePermitted(evidenceID string, use provenance.Use) (Record, error) {
	reg.mu.RLock()
	r, ok := reg.records[evidenceID]
	reg.mu.RUnlock()
	if !ok {
		return Record{}, fmt.Errorf("%w: %s", ErrEvidenceNotFound, evidenceID)
	}
	if !r.Permits(use) {
		return Record{}, fmt.Errorf("%w: %s rights=%s superseded=%t use=%s",
			ErrRightsPermitNothing, evidenceID, r.Rights, r.CorrectionSuperseded, use)
	}
	return r, nil
}

// PermittedFor returns every Record whose rights permit use, in
// submission order. The complement is deliberately NOT returned as
// "denied evidence" -- a caller that wants to know what it may not use
// asks per record via Permits, so no code path can accidentally iterate
// a denied set and then use it.
func (reg *Registry) PermittedFor(use provenance.Use) []Record {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	var out []Record
	for _, id := range reg.order {
		r := reg.records[id]
		if r.Permits(use) {
			out = append(out, r)
		}
	}
	return out
}

// All returns every Record in submission order.
func (reg *Registry) All() []Record {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	out := make([]Record, 0, len(reg.order))
	for _, id := range reg.order {
		out = append(out, reg.records[id])
	}
	return out
}

// ByOrigin returns every Record with the given Origin, in submission order.
func (reg *Registry) ByOrigin(o Origin) []Record {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	var out []Record
	for _, id := range reg.order {
		r := reg.records[id]
		if r.Origin == o {
			out = append(out, r)
		}
	}
	return out
}

// Count returns the number of records in the registry.
func (reg *Registry) Count() int {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	return len(reg.records)
}
