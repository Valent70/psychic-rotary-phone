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

	Metadata map[string]string `json:"metadata,omitempty"`
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
