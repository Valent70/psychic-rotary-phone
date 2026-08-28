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

// SetStatus updates a Record's verification status in place.
func (reg *Registry) SetStatus(evidenceID string, status Status) error {
	if !IsKnownStatus(status) {
		return fmt.Errorf("evidence: unknown status %q", status)
	}
	reg.mu.Lock()
	defer reg.mu.Unlock()
	r, ok := reg.records[evidenceID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrEvidenceNotFound, evidenceID)
	}
	r.Status = status
	reg.records[evidenceID] = r
	return nil
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

// SetRights updates a Record's rights state in place. This is a
// recording operation, exactly like SetStatus: rights are granted or
// revoked by a legal/commercial act elsewhere (see
// provenance.Registry.GrantTrust/RevokeTrust), and this package only
// carries the result. It never derives a rights state from anything --
// not from Origin, not from Status, not from possession.
func (reg *Registry) SetRights(evidenceID string, state provenance.RightsState) error {
	if !provenance.IsKnownRightsState(state) {
		return fmt.Errorf("%w: %q", ErrUnknownRightsState, state)
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

// MarkSuperseded records that supersededID has been replaced by
// bySupersedingID. It does NOT delete, edit or overwrite the superseded
// record's content -- the original Record and its content-addressed
// EvidenceID stay exactly as submitted, which is what makes the earlier
// state still replayable. Only the two correction markers change, and
// from that moment Permits denies every use of the superseded record.
func (reg *Registry) MarkSuperseded(supersededID, bySupersedingID string) error {
	if supersededID == bySupersedingID {
		return errors.New("evidence: a record cannot supersede itself")
	}
	reg.mu.Lock()
	defer reg.mu.Unlock()
	old, ok := reg.records[supersededID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrEvidenceNotFound, supersededID)
	}
	if _, ok := reg.records[bySupersedingID]; !ok {
		return fmt.Errorf("%w: superseding record %s", ErrEvidenceNotFound, bySupersedingID)
	}
	old.CorrectionSuperseded = true
	old.SupersededBy = bySupersedingID
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
