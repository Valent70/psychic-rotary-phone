// Package provenance closes PART 1 and PART 2 of the "Final Audit
// result" external-integration mandate: an explicit, fail-closed
// record of WHERE a piece of external evidence came from and WHAT
// VERIQO is legally permitted to do with it, plus a registry that
// distinguishes the six kinds of external party VERIQO ever deals with
// and refuses to auto-trust any of them.
//
// This package is deliberately additive and does not modify
// pkg/evidence/ontology.Evidence or its content-hash (canonicalBytes):
// every existing EvidenceID computed anywhere in this repository, and
// every test/replay/certificate that depends on one, stays byte-for-
// byte unchanged. An ExternalEvidence record here is a SEPARATE,
// independently-hashed provenance envelope that WRAPS an external
// delivery before it is ever projected into an ontology.Evidence (see
// ExternalEvidence.OntologyProvenance) -- the ontology layer keeps
// asserting "what", this layer asserts "where from, and are we allowed
// to use it".
//
// Anti-false-green discipline, mechanically enforced, not by
// convention: Validate fails closed on any missing provenance or
// rights field (PART 1's explicit requirement), and Permits fails
// closed for every Use the RightsState does not explicitly enumerate
// as allowed -- there is no default-allow path anywhere in this file.
package provenance

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// SchemaVersionV1 is part of every ExternalEvidence's content hash, so
// a record produced under this schema stays independently verifiable
// even after a future schema version adds fields.
const SchemaVersionV1 = "veriqo.provenance/v1"

// OriginClass is how far a piece of evidence is from a verified,
// rights-cleared, third-party-attested external source. The order
// below is deliberately the mandate's own escalation ladder: nothing
// in this codebase may assign a class further right than the evidence
// has actually earned.
type OriginClass string

const (
	OriginSynthetic                 OriginClass = "SYNTHETIC"
	OriginReplay                    OriginClass = "REPLAY"
	OriginRealDerivedBenchmark      OriginClass = "REAL_DERIVED_BENCHMARK"
	OriginRealExternalUnverified    OriginClass = "REAL_EXTERNAL_UNVERIFIED"
	OriginRealExternalAuthorized    OriginClass = "REAL_EXTERNAL_AUTHORIZED"
	OriginQualifiedExternalEvidence OriginClass = "QUALIFIED_EXTERNAL_EVIDENCE"
	OriginVerifiedExternalEvidence  OriginClass = "VERIFIED_EXTERNAL_EVIDENCE"
)

var knownOriginClasses = map[OriginClass]bool{
	OriginSynthetic: true, OriginReplay: true, OriginRealDerivedBenchmark: true,
	OriginRealExternalUnverified: true, OriginRealExternalAuthorized: true,
	OriginQualifiedExternalEvidence: true, OriginVerifiedExternalEvidence: true,
}

// RightsState is what VERIQO is legally permitted to do with this
// evidence right now. A source being technically reachable (PART 3's
// "AIS API connectivity") never implies a RightsState beyond
// UNKNOWN_PENDING_CONTRACT -- that upgrade is a business/legal act,
// never a code path.
type RightsState string

const (
	RightsUnknownPendingContract RightsState = "UNKNOWN_PENDING_CONTRACT"
	RightsInternalOnly           RightsState = "INTERNAL_ONLY"
	RightsAuthorizedPilot        RightsState = "AUTHORIZED_PILOT"
	RightsCustomerFacingAllowed  RightsState = "CUSTOMER_FACING_ALLOWED"
	RightsDisputeUseAllowed      RightsState = "DISPUTE_USE_ALLOWED"
	RightsCalibrationAllowed     RightsState = "CALIBRATION_ALLOWED"
	RightsTrainingAllowed        RightsState = "TRAINING_ALLOWED"
	RightsRevoked                RightsState = "REVOKED"
	RightsExpired                RightsState = "EXPIRED"
)

var knownRightsStates = map[RightsState]bool{
	RightsUnknownPendingContract: true, RightsInternalOnly: true, RightsAuthorizedPilot: true,
	RightsCustomerFacingAllowed: true, RightsDisputeUseAllowed: true, RightsCalibrationAllowed: true,
	RightsTrainingAllowed: true, RightsRevoked: true, RightsExpired: true,
}

// Use is one concrete way VERIQO might want to use a piece of external
// evidence. Permits below is the single fail-closed gate every
// consumer of external evidence must call before using it that way.
type Use string

const (
	UseInternalOnly   Use = "INTERNAL_ONLY"
	UseCustomerFacing Use = "CUSTOMER_FACING"
	UseDispute        Use = "DISPUTE"
	UseCalibration    Use = "CALIBRATION"
	UseTraining       Use = "TRAINING"
)

// permittedUses is the ONLY table mapping a RightsState to what it
// allows. REVOKED and EXPIRED permit nothing, deliberately absent from
// every value list below rather than mapped to an empty slice, so a
// missing entry and an explicitly-empty entry read identically to
// Permits' default-deny lookup.
var permittedUses = map[RightsState][]Use{
	RightsUnknownPendingContract: {UseInternalOnly},
	RightsInternalOnly:           {UseInternalOnly},
	RightsAuthorizedPilot:        {UseInternalOnly},
	RightsCustomerFacingAllowed:  {UseInternalOnly, UseCustomerFacing},
	RightsDisputeUseAllowed:      {UseInternalOnly, UseDispute},
	RightsCalibrationAllowed:     {UseInternalOnly, UseCalibration},
	RightsTrainingAllowed:        {UseInternalOnly, UseTraining},
	// RightsRevoked, RightsExpired: no entry -- Permits denies every
	// Use, including UseInternalOnly, for both. RightsUnknownPending-
	// Contract (above) is the one deliberate exception: real evidence
	// awaiting a contract decision may still be used internally
	// (triage, corroboration, contradiction correlation) while staying
	// hard-denied for every externally-visible or qualification use
	// (PART 15: "AIS API connectivity cannot become live_data
	// VERIFIED") until a real contract explicitly upgrades it.
}

// CorrectionState tracks whether a later delivery has superseded or
// retracted this one -- PART 4's "never silently overwrite" applied to
// the provenance envelope itself, not just the normalized event.
type CorrectionState string

const (
	CorrectionNone       CorrectionState = "NONE"
	CorrectionSuperseded CorrectionState = "SUPERSEDED"
	CorrectionRetracted  CorrectionState = "RETRACTED"
)

// AttestationState is whether a third party (an ATTESTER, see
// Registry below) has vouched for this specific delivery, distinct
// from whether the PROVIDER itself is a registered, trusted entity.
type AttestationState string

const (
	AttestationUnattested         AttestationState = "UNATTESTED"
	AttestationSelfAsserted       AttestationState = "SELF_ASSERTED"
	AttestationThirdPartyAttested AttestationState = "THIRD_PARTY_ATTESTED"
)

var (
	ErrMissingField       = errors.New("provenance: required field missing")
	ErrUnknownOriginClass = errors.New("provenance: unknown origin_class")
	ErrUnknownRightsState = errors.New("provenance: unknown rights_state")
	ErrUnknownCorrection  = errors.New("provenance: unknown correction_state")
	ErrUnknownAttestation = errors.New("provenance: unknown attestation_state")
)

// ExternalEvidence is PART 1's required envelope, field for field.
type ExternalEvidence struct {
	SourceID   string `json:"source_id"`
	ProviderID string `json:"provider_id"`
	DatasetID  string `json:"dataset_id"`
	DeliveryID string `json:"delivery_id"`

	OriginClass OriginClass `json:"origin_class"`
	RightsState RightsState `json:"rights_state"`

	// Ticks, not wall-clock timestamps -- this package follows
	// pkg/evidence/ontology's own "no wall clock inside the kernel"
	// discipline; a deployment's ingest adapter is the one place that
	// maps real time to a tick.
	ReceivedAt uint64 `json:"received_at"`
	ObservedAt uint64 `json:"observed_at"`

	PayloadHash   string `json:"payload_hash"`
	CanonicalHash string `json:"canonical_hash"`
	SchemaVersion string `json:"schema_version"`

	TransformationChain []string         `json:"transformation_chain,omitempty"`
	CorrectionState     CorrectionState  `json:"correction_state"`
	AttestationState    AttestationState `json:"attestation_state"`
}

// Validate fails closed: every field PART 1 lists as required must be
// present and every enum field must be one of this package's own known
// values -- an unrecognized value (e.g. a typo, or a future schema's
// value fed to an old binary) is treated as missing, never as
// permissive-by-default.
func (e ExternalEvidence) Validate() error {
	if strings.TrimSpace(e.SourceID) == "" {
		return fmt.Errorf("%w: source_id", ErrMissingField)
	}
	if strings.TrimSpace(e.ProviderID) == "" {
		return fmt.Errorf("%w: provider_id", ErrMissingField)
	}
	if strings.TrimSpace(e.DatasetID) == "" {
		return fmt.Errorf("%w: dataset_id", ErrMissingField)
	}
	if strings.TrimSpace(e.DeliveryID) == "" {
		return fmt.Errorf("%w: delivery_id", ErrMissingField)
	}
	if strings.TrimSpace(e.PayloadHash) == "" {
		return fmt.Errorf("%w: payload_hash", ErrMissingField)
	}
	if strings.TrimSpace(e.CanonicalHash) == "" {
		return fmt.Errorf("%w: canonical_hash", ErrMissingField)
	}
	if strings.TrimSpace(e.SchemaVersion) == "" {
		return fmt.Errorf("%w: schema_version", ErrMissingField)
	}
	if !knownOriginClasses[e.OriginClass] {
		return fmt.Errorf("%w: %q", ErrUnknownOriginClass, e.OriginClass)
	}
	if !knownRightsStates[e.RightsState] {
		return fmt.Errorf("%w: %q", ErrUnknownRightsState, e.RightsState)
	}
	switch e.CorrectionState {
	case CorrectionNone, CorrectionSuperseded, CorrectionRetracted:
	default:
		return fmt.Errorf("%w: %q", ErrUnknownCorrection, e.CorrectionState)
	}
	switch e.AttestationState {
	case AttestationUnattested, AttestationSelfAsserted, AttestationThirdPartyAttested:
	default:
		return fmt.Errorf("%w: %q", ErrUnknownAttestation, e.AttestationState)
	}
	return nil
}

// IsKnownRightsState reports whether s is a modelled rights state. An
// unrecognised value is never treated as a permissive default -- see
// Permits, which denies every Use for it.
func IsKnownRightsState(s RightsState) bool { return knownRightsStates[s] }

// Permits reports whether this rights state, on its own, allows use.
// It is the exported view of permittedUses -- the single table in this
// package -- so that any other layer needing the same gate (e.g.
// pkg/insurance/evidence, whose records carry a RightsState but are
// NOT provenance.ExternalEvidence values) consults THIS table rather
// than restating it. Fail-closed: an unrecognised or unlisted state
// permits nothing, including UseInternalOnly.
func (s RightsState) Permits(use Use) bool {
	for _, u := range permittedUses[s] {
		if u == use {
			return true
		}
	}
	return false
}

// Permits is the single fail-closed gate: true only when this
// evidence's CURRENT RightsState explicitly lists use. A
// CorrectionState of SUPERSEDED or RETRACTED always denies every Use
// regardless of RightsState -- a corrected record must never be used
// as if it were still current.
func (e ExternalEvidence) Permits(use Use) bool {
	if e.CorrectionState == CorrectionSuperseded || e.CorrectionState == CorrectionRetracted {
		return false
	}
	return e.RightsState.Permits(use)
}

// ID is the content-addressed identifier for this provenance envelope
// -- SHA-256 over every semantic field, hand-rolled and key-ordered
// for the same cross-language-reproducibility reason
// ontology.Evidence.canonicalBytes documents.
func (e ExternalEvidence) ID() string {
	var b strings.Builder
	fmt.Fprintf(&b, "schema=%s\n", e.SchemaVersion)
	fmt.Fprintf(&b, "source_id=%s\nprovider_id=%s\ndataset_id=%s\ndelivery_id=%s\n",
		e.SourceID, e.ProviderID, e.DatasetID, e.DeliveryID)
	fmt.Fprintf(&b, "origin_class=%s\nrights_state=%s\n", e.OriginClass, e.RightsState)
	fmt.Fprintf(&b, "received_at=%d\nobserved_at=%d\n", e.ReceivedAt, e.ObservedAt)
	fmt.Fprintf(&b, "payload_hash=%s\ncanonical_hash=%s\n", e.PayloadHash, e.CanonicalHash)
	fmt.Fprintf(&b, "transformation_chain=%s\n", strings.Join(e.TransformationChain, ">"))
	fmt.Fprintf(&b, "correction_state=%s\nattestation_state=%s\n", e.CorrectionState, e.AttestationState)
	h := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(h[:])
}

// OntologyProvenance projects this envelope's identity fields into the
// shape pkg/evidence/ontology.Provenance already expects, so an
// external delivery can flow into the existing, unmodified ontology
// Evidence pipeline once it has cleared Validate and whatever Permits
// check its caller requires. The mapping is a pure field rename, never
// an upgrade of trust: an ontology.Evidence built this way still
// carries no memory of OriginClass/RightsState on its own -- callers
// that need to re-check rights before acting on a resolved
// ontology.Evidence must keep and re-consult the original
// ExternalEvidence (e.g. by EvidenceID/ID correlation), not assume the
// projection is sufficient.
func (e ExternalEvidence) OntologyProvenance() (producerID, upstreamID, transformationID string) {
	transformationID = ""
	if n := len(e.TransformationChain); n > 0 {
		transformationID = e.TransformationChain[n-1]
	}
	return e.ProviderID, e.SourceID, transformationID
}

// --- PART 2: external source registry -------------------------------

// EntityKind is the six distinct roles PART 2 requires this registry
// to keep apart -- a SOURCE (a raw feed) is not a PROVIDER (the
// organization operating it), which is not an EVIDENCE_PROVIDER
// (explicitly trusted to supply evidence VERIQO acts on), which is not
// an ATTESTER (vouches for specific deliveries) or a REVIEWER (signs
// off on qualification), and a DATASET is a specific named corpus a
// PROVIDER makes available, not the provider itself.
type EntityKind string

const (
	KindSource           EntityKind = "SOURCE"
	KindProvider         EntityKind = "PROVIDER"
	KindDataset          EntityKind = "DATASET"
	KindEvidenceProvider EntityKind = "EVIDENCE_PROVIDER"
	KindAttester         EntityKind = "ATTESTER"
	KindReviewer         EntityKind = "REVIEWER"
)

var knownEntityKinds = map[EntityKind]bool{
	KindSource: true, KindProvider: true, KindDataset: true,
	KindEvidenceProvider: true, KindAttester: true, KindReviewer: true,
}

var (
	ErrUnknownEntityKind                      = errors.New("provenance: unknown registry entity kind")
	ErrEntityExists                           = errors.New("provenance: entity already registered")
	ErrEntityNotFound                         = errors.New("provenance: entity not registered")
	ErrTrustGrantRequiresPolicyAndAttestation = errors.New(
		"provenance: granting trust to an EVIDENCE_PROVIDER requires both a policy reference and an attestation reference")
)

// Entry is one registered external party or artifact. TrustGranted is
// NEVER settable at registration time (see Register) -- it can only
// become true through a later, separate GrantTrust call that records
// who granted it, under which policy, and (for EVIDENCE_PROVIDER)
// which attestation backs it. This is the structural answer to PART
// 2's explicit list of sources that must never be auto-trusted:
// AISstream, World Monitor, GDELT, public dashboards, open-source
// projects and government websites can all be Register'd like any
// other SOURCE/PROVIDER, and every one of them starts, and stays,
// untrusted until a real, attributable GrantTrust call says otherwise.
type Entry struct {
	ID   string     `json:"id"`
	Kind EntityKind `json:"kind"`
	Name string     `json:"name"`

	TrustGranted   bool   `json:"trust_granted"`
	PolicyRef      string `json:"policy_ref,omitempty"`
	AttestationRef string `json:"attestation_ref,omitempty"`
	GrantedBy      string `json:"granted_by,omitempty"`
	GrantedAtTick  uint64 `json:"granted_at_tick,omitempty"`
}

// Registry is the append-mostly store of every external party VERIQO
// has ever registered, and the sole authority on whether one is
// currently trusted as an evidence provider.
type Registry struct {
	mu      sync.Mutex
	entries map[string]Entry
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry { return &Registry{entries: make(map[string]Entry)} }

// Register adds a new entity. TrustGranted is always false on
// registration, unconditionally, regardless of what the caller might
// have set on e -- there is no field or flag that bypasses this.
func (r *Registry) Register(e Entry) error {
	if strings.TrimSpace(e.ID) == "" {
		return fmt.Errorf("%w: id", ErrMissingField)
	}
	if !knownEntityKinds[e.Kind] {
		return fmt.Errorf("%w: %q", ErrUnknownEntityKind, e.Kind)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.entries[e.ID]; exists {
		return fmt.Errorf("%w: %s", ErrEntityExists, e.ID)
	}
	e.TrustGranted = false
	e.PolicyRef, e.AttestationRef, e.GrantedBy, e.GrantedAtTick = "", "", "", 0
	r.entries[e.ID] = e
	return nil
}

// GrantTrust is the ONLY way an entity's TrustGranted can ever become
// true. For KindEvidenceProvider it additionally requires both
// policyRef and attestationRef to be non-empty (PART 2: "trust must be
// explicitly granted through policy AND attestation") -- a
// policy-only grant is refused for that kind, not silently accepted
// with an empty attestation.
func (r *Registry) GrantTrust(id, policyRef, attestationRef, grantedBy string, tick uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrEntityNotFound, id)
	}
	if strings.TrimSpace(policyRef) == "" {
		return fmt.Errorf("%w: policy_ref", ErrMissingField)
	}
	if e.Kind == KindEvidenceProvider && strings.TrimSpace(attestationRef) == "" {
		return ErrTrustGrantRequiresPolicyAndAttestation
	}
	e.TrustGranted = true
	e.PolicyRef = policyRef
	e.AttestationRef = attestationRef
	e.GrantedBy = grantedBy
	e.GrantedAtTick = tick
	r.entries[id] = e
	return nil
}

// RevokeTrust reverts an entity to untrusted, e.g. on a contract
// lapse or a failed re-attestation. It does not delete the entry.
func (r *Registry) RevokeTrust(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrEntityNotFound, id)
	}
	e.TrustGranted = false
	r.entries[id] = e
	return nil
}

// Get returns the registered entry, if any.
func (r *Registry) Get(id string) (Entry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	return e, ok
}

// IsTrustedEvidenceProvider is the one check every consumer of
// external evidence must make before treating a delivery's producer as
// trustworthy: true only for a registered KindEvidenceProvider entry
// with TrustGranted set through a real GrantTrust call.
func (r *Registry) IsTrustedEvidenceProvider(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	return ok && e.Kind == KindEvidenceProvider && e.TrustGranted
}
