// Package ontology implements VTECP-001 Capability 2 ("VERIQO Ontology
// Layer", §§11–14): a semantic identity layer OVER already-canonical
// data, deliberately NOT a Palantir clone and NOT a second evidence/
// case/party identity (VTECP-001 LAW-02, SSOT) — "Ontology provides
// semantic identity over existing canonical data."
//
// Three parts:
//
//  1. Object registry (§11-12): a minimum core set of object types
//     (Case, Evidence, Fact, Contract, Party, ...), each instance
//     carrying a stable identity (ObjectType+ObjectID+TenantID) that
//     is never database row identity. Domain packs EXTEND the type
//     registry via RegisterObjectType rather than this package
//     hard-coding every domain's own vocabulary (VTECP-001 LAW-01:
//     the kernel stays domain-generic).
//  2. Link model (§13): typed, tenant-scoped, versioned, first-class
//     relationships between objects — never a bare foreign key.
//  3. Ontology actions (§14): every semantic mutation runs the SAME
//     five-stage pipeline — Policy -> Command Validation ->
//     Deterministic Execution -> State Transition -> Audit Ledger —
//     via ExecuteAction, so no ontology action can bypass audit or
//     validation, and no caller reimplements this pipeline itself.
//
// This package holds the GENERIC object/link graph only. Where an
// action's real work already lives in another package (evidence
// finalization in pkg/evidence/manifest, model approval in
// pkg/governance/lifecycle), ExecuteAction's own execute callback
// calls that package directly — pkg/ontology never reimplements a
// second state machine for something another package already owns
// (REUSE > EXTEND > REFACTOR > CREATE), and never imports those
// packages itself (avoiding any import-cycle risk with callers that
// may need to link back into the ontology graph).
//
// # Not a duplicate of pkg/kernel/ontology
//
// The repository already has a pkg/kernel/ontology, but it solves a
// different problem: a generic meta-schema registry (EntityType,
// RelationType, ActionType, PolicyType as versioned, content-addressed
// TypeDefs, with structural field-kind validation of arbitrary
// Instances against them). It has no fixed vocabulary and no concept
// of case-domain object identity or a first-class Link model. This
// package is the opposite direction: VTECP-001 section 11-13's OWN
// concrete, named vocabulary (Case, Evidence, Fact, Contract, ...) and
// the specific tenant-scoped identity + typed-link model those
// sections mandate, wired through the section 14 action pipeline.
// Building this vocabulary as instances of pkg/kernel/ontology's
// TypeDef/Instance system was considered and rejected: that system's
// Instance.Fields is map[string]string (structural validation only,
// no typed Go structs, no first-class Link type distinct from a
// generic relation instance, and no ExecuteAction-style mandatory
// audit pipeline), so bending it to VTECP's requirements would mean
// rebuilding most of this package as a thin, type-unsafe layer on top
// of it — reuse in name only. The two packages may converge later if
// a genuine shared abstraction emerges; today they serve distinct,
// non-overlapping callers and neither imports the other.
package ontology

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"

	"veriqo/pkg/platform/audit"
)

// ---- Object types (§11) --------------------------------------------

// ObjectType is an open string vocabulary: the constants below are
// VTECP-001 §11's own named minimum core set, plus the framing memo's
// own concrete extension (its §5 entity tree) folded in as first-class
// core types rather than left as a "domain pack should add these"
// afterthought, since the memo explicitly proposed them as genuinely
// new requirements for the Case Resolution Engine this ontology
// exists to serve. Any further domain-specific type is added via
// RegisterObjectType, never a parallel enum.
type ObjectType string

// VTECP-001 §11's own 19 named core object types, verbatim.
const (
	ObjectCase            ObjectType = "Case"
	ObjectEvidence        ObjectType = "Evidence"
	ObjectEvidenceVersion ObjectType = "EvidenceVersion"
	ObjectFact            ObjectType = "Fact"
	ObjectContract        ObjectType = "Contract"
	ObjectObligation      ObjectType = "Obligation"
	ObjectParty           ObjectType = "Party"
	ObjectOrganization    ObjectType = "Organization"
	ObjectPerson          ObjectType = "Person"
	ObjectVessel          ObjectType = "Vessel"
	ObjectCargo           ObjectType = "Cargo"
	ObjectVoyage          ObjectType = "Voyage"
	ObjectTransaction     ObjectType = "Transaction"
	ObjectDocument        ObjectType = "Document"
	ObjectClaim           ObjectType = "Claim"
	ObjectPolicy          ObjectType = "Policy"
	ObjectModel           ObjectType = "Model"
	ObjectDecision        ObjectType = "Decision"
	ObjectEvent           ObjectType = "Event"
)

// Additional core types the "Bagian_dari_Case_Resolution_Engine.docx"
// framing memo (§5) names as a genuinely new, concrete addition to
// VTECP's own starting ontology — folded in as core rather than left
// for each domain pack to redeclare independently, since Case
// Resolution is this ontology's own primary named consumer.
const (
	ObjectClause            ObjectType = "Clause"
	ObjectPort              ObjectType = "Port"
	ObjectShipment          ObjectType = "Shipment"
	ObjectBreach            ObjectType = "Breach"
	ObjectCausation         ObjectType = "Causation"
	ObjectResponsibility    ObjectType = "Responsibility"
	ObjectLoss              ObjectType = "Loss"
	ObjectQuantum           ObjectType = "Quantum"
	ObjectCounterclaim      ObjectType = "Counterclaim"
	ObjectFinding           ObjectType = "Finding"
	ObjectHypothesis        ObjectType = "Hypothesis"
	ObjectResolution        ObjectType = "Resolution"
	ObjectResolutionPackage ObjectType = "ResolutionPackage"
	ObjectTimeline          ObjectType = "Timeline"
)

// The six object types the constitutional core makes first-class.
//
// Each of these was previously a value carried inside some other
// object -- a proposition lived in a claim's text, a contradiction in a
// finding's prose, a qualification as a string on a record. That is
// exactly how an object stops being addressable: you cannot link to it,
// version it, or ask what else depends on it.
//
// They are added here rather than registered from their own packages so
// the canonical vocabulary stays in one place. pkg/proof, pkg/casefabric
// and pkg/qualification create instances; none of them declares a
// competing type.
const (
	// ObjectProposition is a falsifiable statement about the world. It
	// is distinct from Claim: a claim is a party's assertion within a
	// case, a proposition is the thing that is or is not so.
	ObjectProposition ObjectType = "Proposition"
	// ObjectProofObject is the sealed, cryptographically bound record
	// behind a significant conclusion (pkg/proof).
	ObjectProofObject ObjectType = "ProofObject"
	// ObjectQualification is an epistemic verdict on a claim, with its
	// policy version and material dissent. Addressable because a
	// qualification can be superseded, and the supersession matters.
	ObjectQualification ObjectType = "Qualification"
	// ObjectContradiction is a specific conflict within or against an
	// evidence set. First-class because contradictions are carried, not
	// resolved by averaging, and a carried thing needs an identity.
	ObjectContradiction ObjectType = "Contradiction"
	// ObjectProofObligation is what would have to be shown for a claim
	// to hold (pkg/qualification/reverseproof).
	ObjectProofObligation ObjectType = "ProofObligation"
	// ObjectNextBestEvidence is a ranked, rights-filtered candidate for
	// acquisition. Addressable because "what we chose not to pursue, and
	// why" is part of the record.
	ObjectNextBestEvidence ObjectType = "NextBestEvidence"
	// ObjectAttestation is a temporal attestation over a datum: either a
	// position in VERIQO's tamper-evident chain or an independent
	// authority's token (pkg/platform/timestamp). First-class because
	// the two are cited differently and must never be conflated -- an
	// RFC 3161 token is referred to by its authority and serial, and a
	// thing referred to needs an identity.
	ObjectAttestation ObjectType = "Attestation"
)

var (
	mu               sync.RWMutex
	knownObjectTypes = func() map[ObjectType]bool {
		m := map[ObjectType]bool{}
		for _, t := range []ObjectType{
			ObjectCase, ObjectEvidence, ObjectEvidenceVersion, ObjectFact, ObjectContract,
			ObjectObligation, ObjectParty, ObjectOrganization, ObjectPerson, ObjectVessel,
			ObjectCargo, ObjectVoyage, ObjectTransaction, ObjectDocument, ObjectClaim,
			ObjectPolicy, ObjectModel, ObjectDecision, ObjectEvent,
			ObjectClause, ObjectPort, ObjectShipment, ObjectBreach, ObjectCausation,
			ObjectResponsibility, ObjectLoss, ObjectQuantum, ObjectCounterclaim,
			ObjectFinding, ObjectHypothesis, ObjectResolution, ObjectResolutionPackage,
			ObjectTimeline,
			ObjectProposition, ObjectProofObject, ObjectQualification,
			ObjectContradiction, ObjectProofObligation, ObjectNextBestEvidence,
			ObjectAttestation,
		} {
			m[t] = true
		}
		return m
	}()
	knownLinkTypes = func() map[LinkType]bool {
		m := map[LinkType]bool{}
		for _, l := range []LinkType{
			LinkCaseHasEvidence, LinkEvidenceSupportsFact, LinkFactRelatesToObligation,
			LinkContractDefinesObligation, LinkPartyOwnsVessel, LinkVesselPerformedVoyage,
			LinkCargoMovedOnVoyage, LinkTransactionRelatesToCargo, LinkClaimRelatesToEvidence,
			LinkCaseHasParty, LinkCaseGovernedByContract, LinkContractDefinesClause,
			LinkCaseContainsEvent, LinkEventOccursAtTime, LinkEvidencesBreach,
			LinkBreachCausesLoss, LinkAttributedToResponsibility, LinkQuantifiedAsQuantum,
			LinkResolvedByResolution,
		} {
			m[l] = true
		}
		return m
	}()
)

// RegisterObjectType lets a domain pack extend the object-type
// registry (VTECP-001 §11: "Domain packs extend this registry") —
// never a second, competing type vocabulary declared outside this
// package.
func RegisterObjectType(t ObjectType) {
	mu.Lock()
	defer mu.Unlock()
	knownObjectTypes[t] = true
}

// IsKnownObjectType reports whether t is a registered object type —
// either one of the core types above, or one a domain pack registered.
func IsKnownObjectType(t ObjectType) bool {
	mu.RLock()
	defer mu.RUnlock()
	return knownObjectTypes[t]
}

// KnownObjectTypes returns every registered object type, sorted.
func KnownObjectTypes() []ObjectType {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]ObjectType, 0, len(knownObjectTypes))
	for t := range knownObjectTypes {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ---- Object identity (§12) -------------------------------------------

// Object is one ontology instance. Identity is
// (ObjectType, ObjectID, TenantID) — deliberately never database row
// identity (VTECP-001 §12: "Object identity must be stable. Do not use
// database row identity as semantic identity.").
type Object struct {
	ObjectType      ObjectType `json:"object_type"`
	ObjectID        string     `json:"object_id"`
	TenantID        string     `json:"tenant_id"`
	SchemaVersion   string     `json:"schema_version"`
	SourceReference string     `json:"source_reference,omitempty"`
	CreatedAt       uint64     `json:"created_at"`
	State           string     `json:"state,omitempty"`
}

// Key is the object's own stable identity string.
func (o Object) Key() string { return string(o.ObjectType) + ":" + o.TenantID + ":" + o.ObjectID }

var (
	ErrUnknownObjectType = errors.New("ontology: unknown ObjectType")
	ErrEmptyObjectID     = errors.New("ontology: ObjectID must be non-empty")
	ErrEmptyTenantID     = errors.New("ontology: TenantID must be non-empty")
	ErrUnknownLinkType   = errors.New("ontology: unknown LinkType")
	ErrObjectNotFound    = errors.New("ontology: object not found")
	ErrDuplicateObject   = errors.New("ontology: an object with this identity is already registered")
)

// Validate checks o's own structural well-formedness.
func (o Object) Validate() error {
	if !IsKnownObjectType(o.ObjectType) {
		return fmt.Errorf("%w: %q", ErrUnknownObjectType, o.ObjectType)
	}
	if o.ObjectID == "" {
		return ErrEmptyObjectID
	}
	if o.TenantID == "" {
		return ErrEmptyTenantID
	}
	return nil
}

// ---- Link model (§13) -------------------------------------------------

// LinkType is an open string vocabulary of first-class relationship
// kinds. Every link is typed, tenant-scoped, versioned, auditable, and
// (via ExecuteAction) policy-controlled (VTECP-001 §13).
type LinkType string

// VTECP-001 §13's own 9 named example link types, verbatim.
const (
	LinkCaseHasEvidence           LinkType = "CASE_HAS_EVIDENCE"
	LinkEvidenceSupportsFact      LinkType = "EVIDENCE_SUPPORTS_FACT"
	LinkFactRelatesToObligation   LinkType = "FACT_RELATES_TO_OBLIGATION"
	LinkContractDefinesObligation LinkType = "CONTRACT_DEFINES_OBLIGATION"
	LinkPartyOwnsVessel           LinkType = "PARTY_OWNS_VESSEL"
	LinkVesselPerformedVoyage     LinkType = "VESSEL_PERFORMED_VOYAGE"
	LinkCargoMovedOnVoyage        LinkType = "CARGO_MOVED_ON_VOYAGE"
	LinkTransactionRelatesToCargo LinkType = "TRANSACTION_RELATES_TO_CARGO"
	LinkClaimRelatesToEvidence    LinkType = "CLAIM_RELATES_TO_EVIDENCE"
)

// Additional link types the framing memo's own §5 relationship graph
// names, folded in as core for the same reason the extra object types
// above are.
const (
	LinkCaseHasParty               LinkType = "CASE_HAS_PARTY"
	LinkCaseGovernedByContract     LinkType = "CASE_GOVERNED_BY_CONTRACT"
	LinkContractDefinesClause      LinkType = "CONTRACT_DEFINES_CLAUSE"
	LinkCaseContainsEvent          LinkType = "CASE_CONTAINS_EVENT"
	LinkEventOccursAtTime          LinkType = "EVENT_OCCURS_AT_TIME"
	LinkEvidencesBreach            LinkType = "EVIDENCES_BREACH"
	LinkBreachCausesLoss           LinkType = "BREACH_CAUSES_LOSS"
	LinkAttributedToResponsibility LinkType = "ATTRIBUTED_TO_RESPONSIBILITY"
	LinkQuantifiedAsQuantum        LinkType = "QUANTIFIED_AS_QUANTUM"
	LinkResolvedByResolution       LinkType = "RESOLVED_BY_RESOLUTION"
)

// RegisterLinkType lets a domain pack extend the link-type registry,
// mirroring RegisterObjectType.
func RegisterLinkType(t LinkType) {
	mu.Lock()
	defer mu.Unlock()
	knownLinkTypes[t] = true
}

// IsKnownLinkType reports whether t is a registered link type.
func IsKnownLinkType(t LinkType) bool {
	mu.RLock()
	defer mu.RUnlock()
	return knownLinkTypes[t]
}

// Link is one first-class, typed relationship between two objects.
type Link struct {
	LinkType  LinkType   `json:"link_type"`
	FromType  ObjectType `json:"from_type"`
	FromID    string     `json:"from_id"`
	ToType    ObjectType `json:"to_type"`
	ToID      string     `json:"to_id"`
	TenantID  string     `json:"tenant_id"`
	Version   int        `json:"version"`
	CreatedAt uint64     `json:"created_at"`
	CreatedBy string     `json:"created_by"`
}

// Validate checks l's own structural well-formedness.
func (l Link) Validate() error {
	if !IsKnownLinkType(l.LinkType) {
		return fmt.Errorf("%w: %q", ErrUnknownLinkType, l.LinkType)
	}
	if !IsKnownObjectType(l.FromType) || !IsKnownObjectType(l.ToType) {
		return ErrUnknownObjectType
	}
	if l.FromID == "" || l.ToID == "" {
		return ErrEmptyObjectID
	}
	if l.TenantID == "" {
		return ErrEmptyTenantID
	}
	return nil
}

// ---- Registry -----------------------------------------------------------

// Registry holds every Object and Link this deployment has recorded.
// Thread-safe. AuditStore is optional (nil performs no audit mirroring
// — an explicit, opt-in wiring exactly like
// casestate.AttachCredentialRegistry's own discipline).
type Registry struct {
	mu      sync.RWMutex
	objects map[string]Object // Object.Key() -> Object
	order   []string
	links   []Link

	AuditStore *audit.AuditStore
}

// NewRegistry returns an empty ontology registry.
func NewRegistry() *Registry {
	return &Registry{objects: map[string]Object{}}
}

// AttachAuditStore opts this registry into audit mirroring — every
// ExecuteAction call afterwards appends a canonical audit record.
func (r *Registry) AttachAuditStore(store *audit.AuditStore) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.AuditStore = store
}

// ---- Ontology actions (§14) --------------------------------------------

// ErrPolicyDenied is returned when an ExecuteAction's policy check
// refuses the action.
var ErrPolicyDenied = errors.New("ontology: policy denied this action")

// ActionResult is what ExecuteAction returns and what gets mirrored
// into the audit ledger.
type ActionResult struct {
	Action  string `json:"action"`
	Actor   string `json:"actor"`
	Tick    uint64 `json:"tick"`
	Outcome string `json:"outcome"` // free-text summary, never a verdict/liability field
}

// ExecuteAction is the ONE pipeline every ontology action runs through
// (VTECP-001 §14): Policy -> Command Validation -> Deterministic
// Execution -> State Transition -> Audit Ledger. policyCheck may be
// nil (no policy gate configured for this call site); validate and
// execute may not.
//
// execute performs BOTH "deterministic execution" and "state
// transition" as one caller-supplied step, since for most ontology
// actions (create an object, create a link) they are the same act; an
// action wrapping another package's own state machine (e.g. evidence
// finalization) passes an execute callback that calls that package
// directly, so this function never reimplements a second engine.
func (r *Registry) ExecuteAction(action, actor string, tick uint64, policyCheck func() error, validate func() error, execute func() (string, error)) (ActionResult, error) {
	if policyCheck != nil {
		if err := policyCheck(); err != nil {
			return ActionResult{}, fmt.Errorf("%w: %v", ErrPolicyDenied, err)
		}
	}
	if err := validate(); err != nil {
		return ActionResult{}, fmt.Errorf("ontology: command validation failed: %w", err)
	}
	outcome, err := execute()
	if err != nil {
		return ActionResult{}, fmt.Errorf("ontology: deterministic execution failed: %w", err)
	}
	res := ActionResult{Action: action, Actor: actor, Tick: tick, Outcome: outcome}
	if r.AuditStore != nil {
		payload, mErr := json.Marshal(res)
		if mErr != nil {
			return ActionResult{}, fmt.Errorf("ontology: encoding audit payload: %w", mErr)
		}
		if _, aErr := r.AuditStore.Append("ONTOLOGY:"+actor, action, string(payload)); aErr != nil {
			return ActionResult{}, fmt.Errorf("ontology: audit ledger append failed: %w", aErr)
		}
	}
	return res, nil
}

// CreateObject is the "CreateCase"/generic object-creation ontology
// action (§14): registers a brand-new Object identity through the
// standard ExecuteAction pipeline.
func (r *Registry) CreateObject(o Object, actor string, tick uint64, policyCheck func() error) (Object, error) {
	o.CreatedAt = tick
	_, err := r.ExecuteAction("CreateObject:"+string(o.ObjectType), actor, tick, policyCheck,
		func() error { return o.Validate() },
		func() (string, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			if _, exists := r.objects[o.Key()]; exists {
				return "", fmt.Errorf("%w: %s", ErrDuplicateObject, o.Key())
			}
			r.objects[o.Key()] = o
			r.order = append(r.order, o.Key())
			return o.Key(), nil
		})
	if err != nil {
		return Object{}, err
	}
	return o, nil
}

// Get returns the object identified by (objectType, objectID, tenantID).
func (r *Registry) Get(objectType ObjectType, objectID, tenantID string) (Object, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	o, ok := r.objects[Object{ObjectType: objectType, ObjectID: objectID, TenantID: tenantID}.Key()]
	return o, ok
}

// ByType returns every registered object of type t, in registration order.
func (r *Registry) ByType(t ObjectType) []Object {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Object
	for _, k := range r.order {
		if o := r.objects[k]; o.ObjectType == t {
			out = append(out, o)
		}
	}
	return out
}

// TransitionObjectState updates an object's own free-text State field
// through the standard ExecuteAction pipeline — the ontology-level
// state field, distinct from any domain package's own richer state
// machine (e.g. pkg/evidence/manifest's finalization states, mirrored
// here only as this generic field once that package's own transition
// has already happened).
func (r *Registry) TransitionObjectState(objectType ObjectType, objectID, tenantID, newState, actor string, tick uint64, policyCheck func() error) (Object, error) {
	key := Object{ObjectType: objectType, ObjectID: objectID, TenantID: tenantID}.Key()
	_, err := r.ExecuteAction("TransitionObjectState:"+string(objectType), actor, tick, policyCheck,
		func() error {
			r.mu.RLock()
			_, exists := r.objects[key]
			r.mu.RUnlock()
			if !exists {
				return fmt.Errorf("%w: %s", ErrObjectNotFound, key)
			}
			return nil
		},
		func() (string, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			o := r.objects[key]
			from := o.State
			o.State = newState
			r.objects[key] = o
			return fmt.Sprintf("%s -> %s", from, newState), nil
		})
	if err != nil {
		return Object{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.objects[key], nil
}

// CreateLink is the ontology action that records a first-class,
// typed relationship (§13/§14), through the standard ExecuteAction
// pipeline. Both endpoint objects must already be registered.
func (r *Registry) CreateLink(l Link, actor string, tick uint64, policyCheck func() error) (Link, error) {
	l.CreatedAt = tick
	l.CreatedBy = actor
	_, err := r.ExecuteAction("CreateLink:"+string(l.LinkType), actor, tick, policyCheck,
		func() error {
			if err := l.Validate(); err != nil {
				return err
			}
			r.mu.RLock()
			_, fromOK := r.objects[Object{ObjectType: l.FromType, ObjectID: l.FromID, TenantID: l.TenantID}.Key()]
			_, toOK := r.objects[Object{ObjectType: l.ToType, ObjectID: l.ToID, TenantID: l.TenantID}.Key()]
			r.mu.RUnlock()
			if !fromOK {
				return fmt.Errorf("%w: link source %s:%s", ErrObjectNotFound, l.FromType, l.FromID)
			}
			if !toOK {
				return fmt.Errorf("%w: link target %s:%s", ErrObjectNotFound, l.ToType, l.ToID)
			}
			return nil
		},
		func() (string, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			l.Version = len(r.linksBetweenLocked(l.FromType, l.FromID, l.ToType, l.ToID)) + 1
			r.links = append(r.links, l)
			return fmt.Sprintf("%s:%s -> %s:%s", l.FromType, l.FromID, l.ToType, l.ToID), nil
		})
	if err != nil {
		return Link{}, err
	}
	return l, nil
}

func (r *Registry) linksBetweenLocked(fromType ObjectType, fromID string, toType ObjectType, toID string) []Link {
	var out []Link
	for _, l := range r.links {
		if l.FromType == fromType && l.FromID == fromID && l.ToType == toType && l.ToID == toID {
			out = append(out, l)
		}
	}
	return out
}

// LinksFrom returns every link whose source is (objectType, objectID),
// oldest first.
func (r *Registry) LinksFrom(objectType ObjectType, objectID string) []Link {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Link
	for _, l := range r.links {
		if l.FromType == objectType && l.FromID == objectID {
			out = append(out, l)
		}
	}
	return out
}

// LinksTo returns every link whose target is (objectType, objectID),
// oldest first.
func (r *Registry) LinksTo(objectType ObjectType, objectID string) []Link {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Link
	for _, l := range r.links {
		if l.ToType == objectType && l.ToID == objectID {
			out = append(out, l)
		}
	}
	return out
}
