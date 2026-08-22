// Package party models the insurance case's parties and their roles —
// VICE blueprint §3 ("Insurance Parties Model"). A single real-world
// entity commonly holds more than one role in a maritime claim (a
// cargo owner is very often also the insured), so a Party carries a
// SET of roles rather than one, and the registry never assumes role
// exclusivity.
//
// This package does not perform entity resolution — it does not decide
// whether "Acme Shipping Ltd" in one document is the same legal entity
// as "ACME SHIPPING" in another. That is pkg/identity's job. A Party's
// optional EntityRef links it to a canonical pkg/identity entity when
// that resolution has been performed; VICE never re-implements identity
// resolution inside the insurance domain (blueprint §1: "Jangan
// duplikasi ... Identity ... yang sudah ada").
package party

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Role is one of the fixed maritime-insurance party roles the
// blueprint enumerates (§3). The set is closed and versioned the same
// way pkg/evidence/ontology.Type is: adding a role is a deliberate,
// auditable act.
type Role string

const (
	RoleInsured               Role = "INSURED"
	RoleClaimant              Role = "CLAIMANT"
	RoleCarrier               Role = "CARRIER"
	RoleShipowner             Role = "SHIPOWNER"
	RoleCharterer             Role = "CHARTERER"
	RoleCargoOwner            Role = "CARGO_OWNER"
	RoleConsignee             Role = "CONSIGNEE"
	RoleShipper               Role = "SHIPPER"
	RoleTerminal              Role = "TERMINAL"
	RoleForwarder             Role = "FORWARDER"
	RoleBroker                Role = "BROKER"
	RoleInsurer               Role = "INSURER"
	RolePAndIClub             Role = "P&I_CLUB"
	RoleHullInsurer           Role = "HULL_INSURER"
	RoleCargoInsurer          Role = "CARGO_INSURER"
	RoleSurveyor              Role = "SURVEYOR"
	RoleLossAdjuster          Role = "LOSS_ADJUSTER"
	RoleReinsurer             Role = "REINSURER"
	RoleLegalCounsel          Role = "LEGAL_COUNSEL"
	RolePortAuthority         Role = "PORT_AUTHORITY"
	RoleOtherResponsibleParty Role = "OTHER_RESPONSIBLE_PARTY"

	// The roles below were added when the synthetic case pack
	// (pkg/insurance/casepack) needed them. Each is named explicitly by
	// one of the two frozen design documents, so adding them is the
	// deliberate, auditable act this type's doc comment describes rather
	// than an ad-hoc widening:
	//
	//   - Warehouse, Stevedore, ShipManager, Manufacturer,
	//     LogisticsProvider and Counterparty are all named in the
	//     functional spec §30's own list of potential responsible parties
	//     for recovery, and none of them existed here.
	//   - Respondent is the counterpart to the existing RoleClaimant once
	//     a claim becomes a dispute (I-08). Without it, a respondent had
	//     to be recorded as OTHER_RESPONSIBLE_PARTY, which reads as a
	//     recovery target rather than a party to a dispute.
	//   - Regulator and Auditor are required by I-08's regulatory half
	//     and by the functional spec §77's independent-review model. A
	//     supervisory authority recorded as a PORT_AUTHORITY would be a
	//     mislabel, and mislabelling a party to fit an existing enum is
	//     exactly the kind of quiet inaccuracy this codebase's governance
	//     exists to prevent.
	RoleWarehouse         Role = "WAREHOUSE"
	RoleStevedore         Role = "STEVEDORE"
	RoleShipManager       Role = "SHIP_MANAGER"
	RoleManufacturer      Role = "MANUFACTURER"
	RoleLogisticsProvider Role = "LOGISTICS_PROVIDER"
	RoleCounterparty      Role = "COUNTERPARTY"
	RoleRespondent        Role = "RESPONDENT"
	RoleRegulator         Role = "REGULATOR"
	RoleAuditor           Role = "AUDITOR"
)

var knownRoles = map[Role]bool{
	RoleInsured: true, RoleClaimant: true, RoleCarrier: true, RoleShipowner: true,
	RoleCharterer: true, RoleCargoOwner: true, RoleConsignee: true, RoleShipper: true,
	RoleTerminal: true, RoleForwarder: true, RoleBroker: true, RoleInsurer: true,
	RolePAndIClub: true, RoleHullInsurer: true, RoleCargoInsurer: true, RoleSurveyor: true,
	RoleLossAdjuster: true, RoleReinsurer: true, RoleLegalCounsel: true,
	RolePortAuthority: true, RoleOtherResponsibleParty: true,
	RoleWarehouse: true, RoleStevedore: true, RoleShipManager: true,
	RoleManufacturer: true, RoleLogisticsProvider: true, RoleCounterparty: true,
	RoleRespondent: true, RoleRegulator: true, RoleAuditor: true,
}

// KnownRoles returns every modelled role in deterministic order.
func KnownRoles() []Role {
	out := make([]Role, 0, len(knownRoles))
	for r := range knownRoles {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// IsKnownRole reports whether r is one of the modelled roles.
func IsKnownRole(r Role) bool { return knownRoles[r] }

var (
	ErrEmptyPartyID     = errors.New("party: PartyID must be non-empty")
	ErrEmptyName        = errors.New("party: Name must be non-empty")
	ErrUnknownRole      = errors.New("party: role is not in the known role set")
	ErrNoRoles          = errors.New("party: a party must declare at least one role")
	ErrDuplicatePartyID = errors.New("party: PartyID already registered")
	ErrPartyNotFound    = errors.New("party: PartyID not found")
)

// PartyID identifies a party WITHIN ONE CASE. It is caller-assigned
// (typically the case's own sequence, e.g. "PTY-001") and is not a
// claim of global entity identity — that claim, if made, flows through
// EntityRef into pkg/identity instead.
type PartyID string

// Party is one participant in an insurance case, possibly holding
// several roles.
type Party struct {
	PartyID PartyID `json:"party_id"`
	Name    string  `json:"name"`
	// EntityRef optionally links this party to a canonical
	// pkg/identity entity once resolved. Empty means "not yet
	// resolved" — never treated as "resolved to nothing".
	EntityRef string            `json:"entity_ref,omitempty"`
	Roles     map[Role]bool     `json:"-"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// RoleList returns p's roles in deterministic order.
func (p Party) RoleList() []Role {
	out := make([]Role, 0, len(p.Roles))
	for r := range p.Roles {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// HasRole reports whether p holds role r.
func (p Party) HasRole(r Role) bool { return p.Roles[r] }

// Registry is the case-scoped set of parties. Thread-safe: evidence
// ingestion and party registration can happen concurrently during
// intake.
type Registry struct {
	mu      sync.RWMutex
	parties map[PartyID]Party
	order   []PartyID // registration order, for deterministic iteration
}

// NewRegistry returns an empty party registry.
func NewRegistry() *Registry {
	return &Registry{parties: make(map[PartyID]Party)}
}

// Register adds a new party with an initial set of roles. Every role
// must be a known role (fail-closed — an unrecognised role string is
// refused, not silently accepted as freeform text) and at least one
// role is required (blueprint §3 gives no notion of a roleless party).
func (r *Registry) Register(id PartyID, name string, roles ...Role) (Party, error) {
	if id == "" {
		return Party{}, ErrEmptyPartyID
	}
	if name == "" {
		return Party{}, ErrEmptyName
	}
	if len(roles) == 0 {
		return Party{}, ErrNoRoles
	}
	roleSet := make(map[Role]bool, len(roles))
	for _, ro := range roles {
		if !IsKnownRole(ro) {
			return Party{}, fmt.Errorf("%w: %q", ErrUnknownRole, ro)
		}
		roleSet[ro] = true
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.parties[id]; exists {
		return Party{}, fmt.Errorf("%w: %s", ErrDuplicatePartyID, id)
	}
	p := Party{PartyID: id, Name: name, Roles: roleSet}
	r.parties[id] = p
	r.order = append(r.order, id)
	return p, nil
}

// AddRole adds an additional role to an already-registered party — the
// blueprint's own worked example (§3: "Entity X: CARGO_OWNER + INSURED").
func (r *Registry) AddRole(id PartyID, role Role) error {
	if !IsKnownRole(role) {
		return fmt.Errorf("%w: %q", ErrUnknownRole, role)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.parties[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrPartyNotFound, id)
	}
	p.Roles[role] = true
	return nil
}

// SetEntityRef links a party to a resolved pkg/identity entity.
func (r *Registry) SetEntityRef(id PartyID, entityRef string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.parties[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrPartyNotFound, id)
	}
	p.EntityRef = entityRef
	r.parties[id] = p
	return nil
}

// Get returns the party for id.
func (r *Registry) Get(id PartyID) (Party, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.parties[id]
	return p, ok
}

// All returns every registered party in registration order.
func (r *Registry) All() []Party {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Party, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.parties[id])
	}
	return out
}

// WithRole returns every party holding role, in registration order.
func (r *Registry) WithRole(role Role) []Party {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Party
	for _, id := range r.order {
		p := r.parties[id]
		if p.Roles[role] {
			out = append(out, p)
		}
	}
	return out
}

// Count returns the number of registered parties.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.parties)
}
