// Package claim provides the extensible claim-type registry and Claim
// aggregate — blueprint §4. The blueprint is explicit: "Jangan
// hard-code hanya cargo damage. Buat extensible registry." — so
// TypeDefinition is data, registered at runtime, not a closed Go
// switch statement. The 24 types the blueprint enumerates are seeded
// as the default registry (DefaultTypes), not hard-coded into any
// downstream package's logic.
package claim

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"veriqo/pkg/insurance/party"
)

// Type is a claim-type identifier. Extensible: any non-empty string
// registered via Registry.Register becomes a valid Type — this package
// never maintains a closed enum of types the way party.Role or
// evidence.Origin do, precisely because the blueprint demands
// extensibility here.
type Type string

// The blueprint's own minimum seed list (§4).
const (
	TypeCargoDamage           Type = "CARGO_DAMAGE"
	TypeCargoShortage         Type = "CARGO_SHORTAGE"
	TypeCargoContamination    Type = "CARGO_CONTAMINATION"
	TypeCargoNonDelivery      Type = "CARGO_NON_DELIVERY"
	TypeCargoTheft            Type = "CARGO_THEFT"
	TypeReeferFailure         Type = "REEFER_FAILURE"
	TypeWetDamage             Type = "WET_DAMAGE"
	TypeFire                  Type = "FIRE"
	TypeCollision             Type = "COLLISION"
	TypeGrounding             Type = "GROUNDING"
	TypeContactDamage         Type = "CONTACT_DAMAGE"
	TypeMachineryDamage       Type = "MACHINERY_DAMAGE"
	TypeHullDamage            Type = "HULL_DAMAGE"
	TypePollution             Type = "POLLUTION"
	TypeGeneralAverage        Type = "GENERAL_AVERAGE"
	TypeSalvage               Type = "SALVAGE"
	TypeDelay                 Type = "DELAY"
	TypeDemurrage             Type = "DEMURRAGE"
	TypeDetention             Type = "DETENTION"
	TypeFreightLoss           Type = "FREIGHT_LOSS"
	TypeLiabilityToThirdParty Type = "LIABILITY_TO_THIRD_PARTY"
	TypeEnvironmentalDamage   Type = "ENVIRONMENTAL_DAMAGE"
	TypeWarRisk               Type = "WAR_RISK"
	TypeCyberMarine           Type = "CYBER_MARINE"
	TypeMultimodalDamage      Type = "MULTIMODAL_DAMAGE"
)

// TypeDefinition is everything the blueprint says every claim type
// must carry (§4): what evidence it needs, what timeline events are
// relevant, what causation hypotheses are worth considering by
// default, what quantum categories apply, and what questions the
// coverage/contract/deadline engines should ask.
type TypeDefinition struct {
	Type                Type     `json:"type"`
	RequiredEvidence    []string `json:"required_evidence"`
	OptionalEvidence    []string `json:"optional_evidence,omitempty"`
	RelevantEvents      []string `json:"relevant_events,omitempty"`
	CausationHypotheses []string `json:"causation_hypotheses,omitempty"`
	QuantumCategories   []string `json:"quantum_categories,omitempty"`
	PolicyQuestions     []string `json:"policy_questions,omitempty"`
	ContractQuestions   []string `json:"contract_questions,omitempty"`
	DeadlineRules       []string `json:"deadline_rules,omitempty"`
}

// TypeRegistry is the extensible claim-type registry.
type TypeRegistry struct {
	mu    sync.RWMutex
	defs  map[Type]TypeDefinition
	order []Type
}

// NewTypeRegistry returns an empty claim-type registry.
func NewTypeRegistry() *TypeRegistry {
	return &TypeRegistry{defs: make(map[Type]TypeDefinition)}
}

var (
	ErrEmptyType          = errors.New("claim: Type must be non-empty")
	ErrNoRequiredEvidence = errors.New("claim: a claim type must declare at least one required_evidence item")
	ErrDuplicateType      = errors.New("claim: this Type is already registered")
	ErrUnregisteredType   = errors.New("claim: claim Type is not registered in the type registry")
)

// Register adds a new claim-type definition. Refuses a type with no
// required evidence — the blueprint's per-type schema (§4) treats
// required_evidence as the load-bearing field every other engine
// (gap, sufficiency) reads.
func (r *TypeRegistry) Register(def TypeDefinition) error {
	if def.Type == "" {
		return ErrEmptyType
	}
	if len(def.RequiredEvidence) == 0 {
		return ErrNoRequiredEvidence
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.defs[def.Type]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateType, def.Type)
	}
	r.defs[def.Type] = def
	r.order = append(r.order, def.Type)
	return nil
}

// Get returns the definition for t.
func (r *TypeRegistry) Get(t Type) (TypeDefinition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.defs[t]
	return d, ok
}

// IsRegistered reports whether t has a registered definition.
func (r *TypeRegistry) IsRegistered(t Type) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.defs[t]
	return ok
}

// All returns every registered definition in registration order.
func (r *TypeRegistry) All() []TypeDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]TypeDefinition, 0, len(r.order))
	for _, t := range r.order {
		out = append(out, r.defs[t])
	}
	return out
}

// DefaultTypes returns TypeDefinitions for every claim type the
// blueprint names in §4, with a minimally-populated but real
// required_evidence set per type (a deployment is expected to refine
// these; DefaultTypes exists so the registry is never empty by
// default, matching "buat extensible registry" — extensible does not
// mean "starts empty and unusable").
func DefaultTypes() []TypeDefinition {
	seed := func(t Type, required ...string) TypeDefinition {
		return TypeDefinition{Type: t, RequiredEvidence: required}
	}
	return []TypeDefinition{
		seed(TypeCargoDamage, "survey_report", "bill_of_lading", "photos"),
		seed(TypeCargoShortage, "packing_list", "bill_of_lading", "delivery_receipt"),
		seed(TypeCargoContamination, "survey_report", "lab_analysis", "bill_of_lading"),
		seed(TypeCargoNonDelivery, "bill_of_lading", "delivery_receipt", "tracking_record"),
		seed(TypeCargoTheft, "police_report", "bill_of_lading", "cctv_or_seal_record"),
		seed(TypeReeferFailure, "temperature_report", "raw_logger_data", "calibration_certificate", "plug_in_out_record"),
		seed(TypeWetDamage, "survey_report", "weather_report", "photos"),
		seed(TypeFire, "incident_report", "fire_investigation_report", "photos"),
		seed(TypeCollision, "incident_report", "ais_data", "collision_report"),
		seed(TypeGrounding, "incident_report", "ais_data", "chart_extract"),
		seed(TypeContactDamage, "incident_report", "survey_report", "photos"),
		seed(TypeMachineryDamage, "engineer_report", "maintenance_log"),
		seed(TypeHullDamage, "survey_report", "classification_society_report"),
		seed(TypePollution, "incident_report", "regulatory_report", "photos"),
		seed(TypeGeneralAverage, "general_average_adjustment", "bill_of_lading"),
		seed(TypeSalvage, "salvage_agreement", "salvage_report"),
		seed(TypeDelay, "voyage_records", "notice_of_delay"),
		seed(TypeDemurrage, "notice_of_readiness", "laytime_statement"),
		seed(TypeDetention, "detention_notice", "release_record"),
		seed(TypeFreightLoss, "freight_invoice", "bill_of_lading"),
		seed(TypeLiabilityToThirdParty, "third_party_claim", "incident_report"),
		seed(TypeEnvironmentalDamage, "regulatory_report", "incident_report", "remediation_records"),
		seed(TypeWarRisk, "incident_report", "war_risk_declaration"),
		seed(TypeCyberMarine, "incident_report", "forensic_report"),
		seed(TypeMultimodalDamage, "multimodal_transport_document", "survey_report"),
	}
}

// Status is a claim's own processing status — NOT a coverage
// decision. The blueprint (§0, §29) forbids VICE from producing a
// final APPROVED/REJECTED verdict; Status only tracks where the claim
// sits in the case lifecycle (see pkg/insurance/case), never whether
// it will ultimately be paid.
type Status string

const (
	StatusRegistered          Status = "REGISTERED"
	StatusUnderAnalysis       Status = "UNDER_ANALYSIS"
	StatusAwaitingHumanReview Status = "AWAITING_HUMAN_REVIEW"
	StatusClosed              Status = "CLOSED" // closed as a VICE workflow item -- NOT "settled" or "denied"
)

var knownStatuses = map[Status]bool{
	StatusRegistered: true, StatusUnderAnalysis: true, StatusAwaitingHumanReview: true, StatusClosed: true,
}

// IsKnownStatus reports whether s is a modelled claim status.
func IsKnownStatus(s Status) bool { return knownStatuses[s] }

var (
	ErrEmptyClaimID  = errors.New("claim: ClaimID must be non-empty")
	ErrEmptyCaseID   = errors.New("claim: CaseID must be non-empty")
	ErrEmptyClaimant = errors.New("claim: ClaimantPartyID must be non-empty")
)

// Claim is one claim within a case. A case may carry more than one
// claim (e.g. a cargo-damage claim and a separate delay claim from the
// same incident); Claim is deliberately NOT the same aggregate as
// Case.
type Claim struct {
	ClaimID         string        `json:"claim_id"`
	CaseID          string        `json:"case_id"`
	Type            Type          `json:"type"`
	ClaimantPartyID party.PartyID `json:"claimant_party_id"`
	Status          Status        `json:"status"`
	PolicyVersionID string        `json:"policy_version_id,omitempty"` // set once EffectiveAt has resolved it
	Description     string        `json:"description,omitempty"`
	// ReserveID references this claim's reserve (pkg/insurance/reserve),
	// when one has been set. Empty means no reserve has been set yet —
	// this package does not construct or own the Reserve itself (that
	// would duplicate pkg/insurance/reserve's own authority/history
	// discipline); it only carries the reference.
	ReserveID string `json:"reserve_id,omitempty"`
}

// New constructs a Claim in StatusRegistered. t need not be one of the
// package's own named constants — the registry, not this type, is the
// extensibility point.
func New(claimID, caseID string, t Type, claimant party.PartyID, registry *TypeRegistry) (Claim, error) {
	if claimID == "" {
		return Claim{}, ErrEmptyClaimID
	}
	if caseID == "" {
		return Claim{}, ErrEmptyCaseID
	}
	if claimant == "" {
		return Claim{}, ErrEmptyClaimant
	}
	if registry != nil && !registry.IsRegistered(t) {
		return Claim{}, fmt.Errorf("%w: %s", ErrUnregisteredType, t)
	}
	return Claim{
		ClaimID: claimID, CaseID: caseID, Type: t, ClaimantPartyID: claimant,
		Status: StatusRegistered,
	}, nil
}

// KnownSeedTypeCount is the number of claim types the blueprint's own
// §4 enumerates — exposed for tests that pin this against DefaultTypes.
func KnownSeedTypeCount() int { return len(seedTypeOrder) }

var seedTypeOrder = []Type{
	TypeCargoDamage, TypeCargoShortage, TypeCargoContamination, TypeCargoNonDelivery, TypeCargoTheft,
	TypeReeferFailure, TypeWetDamage, TypeFire, TypeCollision, TypeGrounding, TypeContactDamage,
	TypeMachineryDamage, TypeHullDamage, TypePollution, TypeGeneralAverage, TypeSalvage, TypeDelay,
	TypeDemurrage, TypeDetention, TypeFreightLoss, TypeLiabilityToThirdParty, TypeEnvironmentalDamage,
	TypeWarRisk, TypeCyberMarine, TypeMultimodalDamage,
}

// SortedTypes returns t sorted for deterministic output.
func SortedTypes(t []Type) []Type {
	out := append([]Type(nil), t...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
