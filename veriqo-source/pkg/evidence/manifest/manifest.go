// Package manifest implements VTECP-001 Capability 1 ("Immutable
// Evidence Manifest", §§5–10): cryptographically verifiable, versioned,
// tamper-evident evidence records that are never edited in place, only
// superseded (VTECP-001 LAW-04).
//
// This package deliberately does NOT redefine evidence identity or
// content addressing — pkg/evidence/ontology.Evidence already owns
// that (EvidenceID is SHA-256 over canonical field ordering). A
// Manifest here is the ACQUISITION/INTEGRITY/CUSTODY/FINALIZATION
// record ABOUT one already-identified piece of evidence, keyed by that
// same EvidenceID — never a second, competing evidence identity
// (VTECP-001 LAW-02, SSOT).
//
// Three chained mechanisms, all built on pkg/canonical/jcs:
//
//  1. Finalization state machine (§8): DRAFT -> INGESTED ->
//     INTEGRITY_ASSESSED -> PROVENANCE_COMPLETE ->
//     READY_FOR_FINALIZATION -> FINALIZED -> SUPERSEDED. Invalid
//     transitions are refused; FINALIZED is immutable.
//  2. Custody chain (§9): every material custody event is hash-chained,
//     Hn = SHA256(Hn-1 || JCS(EventN)), genesis H0 = a fixed constant.
//  3. Transformation chain (§10): every derivation from this evidence
//     records source_version_id/transform_type/transformer/
//     transformer_version/input_hash/output_hash/timestamp/parameters,
//     so VERIQO can prove "this derived fact came from this exact
//     evidence version through these exact transformations."
package manifest

import (
	"errors"
	"fmt"
	"sync"

	"veriqo/pkg/canonical/jcs"
)

// State is one of the seven named finalization states (VTECP-001 §8).
type State string

const (
	StateDraft                State = "DRAFT"
	StateIngested             State = "INGESTED"
	StateIntegrityAssessed    State = "INTEGRITY_ASSESSED"
	StateProvenanceComplete   State = "PROVENANCE_COMPLETE"
	StateReadyForFinalization State = "READY_FOR_FINALIZATION"
	StateFinalized            State = "FINALIZED"
	StateSuperseded           State = "SUPERSEDED"
)

// validTransitions is the whole finalization state machine, as data —
// an invalid transition is structurally impossible, never merely
// undocumented (matching this repository's own established
// adjacency-table discipline, e.g. pkg/insurance/casestate).
var validTransitions = map[State][]State{
	StateDraft:                {StateIngested},
	StateIngested:             {StateIntegrityAssessed},
	StateIntegrityAssessed:    {StateProvenanceComplete},
	StateProvenanceComplete:   {StateReadyForFinalization},
	StateReadyForFinalization: {StateFinalized},
	StateFinalized:            {StateSuperseded},
	StateSuperseded:           {},
}

var knownStates = map[State]bool{
	StateDraft: true, StateIngested: true, StateIntegrityAssessed: true,
	StateProvenanceComplete: true, StateReadyForFinalization: true,
	StateFinalized: true, StateSuperseded: true,
}

// IsKnownState reports whether s is one of the seven modelled states.
func IsKnownState(s State) bool { return knownStates[s] }

// CustodyAction is one of the ten named custody event kinds
// (VTECP-001 §9 / CRE §13 — the two specs name an identical
// vocabulary, so this package uses ONE enum for both rather than
// declaring two competing ones).
type CustodyAction string

const (
	CustodyReceived    CustodyAction = "RECEIVED"
	CustodyRegistered  CustodyAction = "REGISTERED"
	CustodyHashed      CustodyAction = "HASHED"
	CustodyStored      CustodyAction = "STORED"
	CustodyAccessed    CustodyAction = "ACCESSED"
	CustodyTransformed CustodyAction = "TRANSFORMED"
	CustodyDerived     CustodyAction = "DERIVED"
	CustodyReviewed    CustodyAction = "REVIEWED"
	CustodyExported    CustodyAction = "EXPORTED"
	CustodySuperseded  CustodyAction = "SUPERSEDED"
)

var knownCustodyActions = map[CustodyAction]bool{
	CustodyReceived: true, CustodyRegistered: true, CustodyHashed: true,
	CustodyStored: true, CustodyAccessed: true, CustodyTransformed: true,
	CustodyDerived: true, CustodyReviewed: true, CustodyExported: true,
	CustodySuperseded: true,
}

// IsKnownCustodyAction reports whether a is one of the ten modelled
// custody event kinds.
func IsKnownCustodyAction(a CustodyAction) bool { return knownCustodyActions[a] }

// GenesisHash is custody chain H0 (VTECP-001 §9: "genesis = defined
// GENESIS constant") — a fixed sentinel, never a real computed hash,
// so a chain's very first event is distinguishable from a chain whose
// genesis was accidentally omitted.
const GenesisHash = "GENESIS-" + "0000000000000000000000000000000000000000000000000000000"

// CustodyEvent is one immutable, hash-chained custody record
// (VTECP-001 §9). EventHash is computed, never caller-supplied — see
// Registry.RecordCustodyEvent.
type CustodyEvent struct {
	EventID      string        `json:"event_id"`
	EvidenceID   string        `json:"evidence_id"`
	Actor        string        `json:"actor"`
	Tick         uint64        `json:"tick"`
	Action       CustodyAction `json:"action"`
	PreviousHash string        `json:"previous_hash"`
	EventHash    string        `json:"event_hash"`
	Reason       string        `json:"reason"`
}

// hashInput is exactly the fields VTECP-001 §9's formula folds into
// Hn = SHA256(Hn-1 || JCS(EventN)) — separated from CustodyEvent
// itself so EventHash is never accidentally included in its own input.
type custodyHashInput struct {
	EvidenceID   string        `json:"evidence_id"`
	Actor        string        `json:"actor"`
	Tick         uint64        `json:"tick"`
	Action       CustodyAction `json:"action"`
	PreviousHash string        `json:"previous_hash"`
	Reason       string        `json:"reason"`
}

func computeCustodyHash(e CustodyEvent) (string, error) {
	return jcs.Hash(custodyHashInput{
		EvidenceID: e.EvidenceID, Actor: e.Actor, Tick: e.Tick,
		Action: e.Action, PreviousHash: e.PreviousHash, Reason: e.Reason,
	})
}

// Transformation is one derivation step (VTECP-001 §10): "This derived
// fact came from this exact evidence version through these exact
// transformations."
type Transformation struct {
	SourceVersionID    string            `json:"source_version_id"`
	TransformType      string            `json:"transform_type"`
	Transformer        string            `json:"transformer"`
	TransformerVersion string            `json:"transformer_version"`
	InputHash          string            `json:"input_hash"`
	OutputHash         string            `json:"output_hash"`
	Tick               uint64            `json:"timestamp"`
	Parameters         map[string]string `json:"parameters,omitempty"`
}

// Manifest is the canonical evidence manifest (VTECP-001 §6), grouped
// exactly as the spec's own field groupings name them.
type Manifest struct {
	// Identity
	TenantID      string `json:"tenant_id"`
	CaseID        string `json:"case_id"`
	EvidenceID    string `json:"evidence_id"` // the SAME ontology.Evidence.EvidenceID this manifest is about
	Version       int    `json:"version"`
	ParentVersion int    `json:"parent_version,omitempty"`

	// Object
	URI       string `json:"uri"`
	Filename  string `json:"filename"`
	MediaType string `json:"media_type"`
	ByteSize  int64  `json:"byte_size"`
	SHA256    string `json:"sha256"`
	SHA512    string `json:"sha512,omitempty"`

	// Acquisition
	Method        string `json:"method"`
	Collector     string `json:"collector"`
	Source        string `json:"source"`
	AcquiredAt    uint64 `json:"acquired_at"`
	ReceivedAt    uint64 `json:"received_at"`
	System        string `json:"system"`
	SystemVersion string `json:"system_version"`

	// Integrity
	HashStatus         string `json:"hash_status"`
	SignatureStatus    string `json:"signature_status"`
	SignatureAlgorithm string `json:"signature_algorithm,omitempty"`
	Signer             string `json:"signer,omitempty"`
	SignatureTimestamp uint64 `json:"signature_timestamp,omitempty"`

	// Classification
	Classification string   `json:"classification"`
	Markings       []string `json:"markings,omitempty"`
	LegalHold      bool     `json:"legal_hold"`

	// Provenance
	AcquisitionRecord   string           `json:"acquisition_record,omitempty"`
	TransformationChain []Transformation `json:"transformation_chain,omitempty"`

	// Custody
	CustodyChainHead string `json:"custody_chain_head"`

	// Finalization
	State             State  `json:"state"`
	FinalizedBy       string `json:"finalized_by,omitempty"`
	FinalizedAt       uint64 `json:"finalized_at,omitempty"`
	ManifestHash      string `json:"manifest_hash,omitempty"`
	ManifestSignature string `json:"manifest_signature,omitempty"`
}

var (
	ErrEmptyEvidenceID      = errors.New("manifest: EvidenceID must be non-empty")
	ErrEmptyTenantID        = errors.New("manifest: TenantID must be non-empty")
	ErrEmptySHA256          = errors.New("manifest: SHA256 must be non-empty")
	ErrInvalidTransition    = errors.New("manifest: no such finalization transition is modelled from the current state")
	ErrFinalizedIsImmutable = errors.New("manifest: a FINALIZED manifest can never be edited in place -- create a new version instead (VTECP-001 LAW-04)")
	ErrUnknownCustodyAction = errors.New("manifest: unknown CustodyAction")
	ErrCustodyChainBroken   = errors.New("manifest: custody chain hash mismatch")
	ErrManifestNotFound     = errors.New("manifest: no manifest registered for this EvidenceID")
	ErrParentNotFinalized   = errors.New("manifest: a new version can only supersede an ALREADY-FINALIZED parent")
	ErrVersionAlreadyExists = errors.New("manifest: this EvidenceID+Version is already registered")
)

// Validate checks m's own structural well-formedness — not whether its
// STATE transition was legal (that is Registry's job, since legality
// depends on the PRIOR manifest, not this one alone).
func (m Manifest) Validate() error {
	if m.EvidenceID == "" {
		return ErrEmptyEvidenceID
	}
	if m.TenantID == "" {
		return ErrEmptyTenantID
	}
	if m.SHA256 == "" {
		return ErrEmptySHA256
	}
	if !IsKnownState(m.State) {
		return fmt.Errorf("manifest: unknown State %q", m.State)
	}
	return nil
}

// computeManifestHash canonicalizes every semantic field EXCEPT
// ManifestHash/ManifestSignature themselves (a hash can never include
// itself) via pkg/canonical/jcs.
func computeManifestHash(m Manifest) (string, error) {
	cp := m
	cp.ManifestHash = ""
	cp.ManifestSignature = ""
	return jcs.Hash(cp)
}

// Registry holds every Manifest and custody/transformation chain this
// deployment has recorded, keyed by EvidenceID -> ordered versions.
// Thread-safe.
type Registry struct {
	mu       sync.RWMutex
	versions map[string][]Manifest     // EvidenceID -> versions, oldest first
	custody  map[string][]CustodyEvent // EvidenceID -> custody chain, oldest first
}

// NewRegistry returns an empty manifest registry.
func NewRegistry() *Registry {
	return &Registry{versions: map[string][]Manifest{}, custody: map[string][]CustodyEvent{}}
}

// RegisterDraft opens a NEW manifest at StateDraft, Version 1, for an
// EvidenceID that has no manifest yet.
func (r *Registry) RegisterDraft(m Manifest) (Manifest, error) {
	m.State = StateDraft
	m.Version = 1
	m.ParentVersion = 0
	m.ManifestHash = ""
	m.ManifestSignature = ""
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.versions[m.EvidenceID]) != 0 {
		return Manifest{}, fmt.Errorf("%w: %s v%d", ErrVersionAlreadyExists, m.EvidenceID, m.Version)
	}
	r.versions[m.EvidenceID] = append(r.versions[m.EvidenceID], m)
	return m, nil
}

// Latest returns the most recently registered version for evidenceID.
func (r *Registry) Latest(evidenceID string) (Manifest, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	vs := r.versions[evidenceID]
	if len(vs) == 0 {
		return Manifest{}, fmt.Errorf("%w: %s", ErrManifestNotFound, evidenceID)
	}
	return vs[len(vs)-1], nil
}

// Versions returns every recorded version for evidenceID, oldest first.
func (r *Registry) Versions(evidenceID string) []Manifest {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Manifest, len(r.versions[evidenceID]))
	copy(out, r.versions[evidenceID])
	return out
}

// Advance moves evidenceID's latest manifest to a new state — the ONLY
// way a manifest's State ever changes prior to finalization. Refuses
// any transition not in validTransitions (structurally impossible, not
// merely disallowed by convention) and refuses any mutation whatsoever
// once the latest version is already FINALIZED (LAW-04) — a caller who
// wants to correct a finalized manifest must call Supersede instead.
func (r *Registry) Advance(evidenceID string, to State, tick uint64) (Manifest, error) {
	if !IsKnownState(to) {
		return Manifest{}, fmt.Errorf("manifest: unknown target state %q", to)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	vs := r.versions[evidenceID]
	if len(vs) == 0 {
		return Manifest{}, fmt.Errorf("%w: %s", ErrManifestNotFound, evidenceID)
	}
	cur := vs[len(vs)-1]
	if cur.State == StateFinalized {
		return Manifest{}, ErrFinalizedIsImmutable
	}
	allowed := validTransitions[cur.State]
	ok := false
	for _, s := range allowed {
		if s == to {
			ok = true
			break
		}
	}
	if !ok {
		return Manifest{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, cur.State, to)
	}
	cur.State = to
	if to == StateFinalized {
		// FinalizedAt must be set BEFORE the hash is computed -- the
		// hash must cover the manifest's real final state, not a
		// snapshot missing the very field that records when it became
		// final.
		cur.FinalizedAt = tick
		h, err := computeManifestHash(cur)
		if err != nil {
			return Manifest{}, err
		}
		cur.ManifestHash = h
	}
	vs[len(vs)-1] = cur
	return cur, nil
}

// Supersede creates VERSION N+1 from an ALREADY-FINALIZED VERSION N
// (LAW-04's own correction path: "VERSION N -> superseded by ->
// VERSION N+1. The historical version remains queryable and
// cryptographically verifiable."). The prior version transitions to
// StateSuperseded; the new version starts at StateDraft with
// ParentVersion set. Neither version's already-recorded fields are
// ever edited in place.
func (r *Registry) Supersede(next Manifest, tick uint64) (Manifest, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	vs := r.versions[next.EvidenceID]
	if len(vs) == 0 {
		return Manifest{}, fmt.Errorf("%w: %s", ErrManifestNotFound, next.EvidenceID)
	}
	parent := vs[len(vs)-1]
	if parent.State != StateFinalized {
		return Manifest{}, fmt.Errorf("%w: current state is %s", ErrParentNotFinalized, parent.State)
	}
	next.State = StateDraft
	next.Version = parent.Version + 1
	next.ParentVersion = parent.Version
	next.ManifestHash = ""
	next.ManifestSignature = ""
	if err := next.Validate(); err != nil {
		return Manifest{}, err
	}
	parent.State = StateSuperseded
	vs[len(vs)-1] = parent
	r.versions[next.EvidenceID] = append(vs, next)
	return next, nil
}

// RecordCustodyEvent appends the next custody event for evidenceID,
// computing EventHash from the chain's own current head — the ONLY way
// a CustodyEvent enters the chain (a caller never supplies EventHash
// directly).
func (r *Registry) RecordCustodyEvent(evidenceID, eventID, actor string, action CustodyAction, tick uint64, reason string) (CustodyEvent, error) {
	if !IsKnownCustodyAction(action) {
		return CustodyEvent{}, fmt.Errorf("%w: %q", ErrUnknownCustodyAction, action)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	chain := r.custody[evidenceID]
	prev := GenesisHash
	if len(chain) > 0 {
		prev = chain[len(chain)-1].EventHash
	}
	e := CustodyEvent{
		EventID: eventID, EvidenceID: evidenceID, Actor: actor, Tick: tick,
		Action: action, PreviousHash: prev, Reason: reason,
	}
	h, err := computeCustodyHash(e)
	if err != nil {
		return CustodyEvent{}, err
	}
	e.EventHash = h
	r.custody[evidenceID] = append(chain, e)

	if vs := r.versions[evidenceID]; len(vs) > 0 {
		cur := vs[len(vs)-1]
		cur.CustodyChainHead = h
		vs[len(vs)-1] = cur
	}
	return e, nil
}

// CustodyChain returns evidenceID's full custody chain, oldest first.
func (r *Registry) CustodyChain(evidenceID string) []CustodyEvent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]CustodyEvent, len(r.custody[evidenceID]))
	copy(out, r.custody[evidenceID])
	return out
}

// VerifyCustodyChain independently re-derives every event hash in
// evidenceID's chain and checks linkage — the concrete answer to
// "prove this custody chain was not tampered with."
func (r *Registry) VerifyCustodyChain(evidenceID string) error {
	r.mu.RLock()
	chain := append([]CustodyEvent(nil), r.custody[evidenceID]...)
	r.mu.RUnlock()

	prev := GenesisHash
	for i, e := range chain {
		if e.PreviousHash != prev {
			return fmt.Errorf("%w: event %d (%s): expected previous_hash %s, got %s", ErrCustodyChainBroken, i, e.EventID, prev, e.PreviousHash)
		}
		want, err := computeCustodyHash(e)
		if err != nil {
			return err
		}
		if want != e.EventHash {
			return fmt.Errorf("%w: event %d (%s): recorded hash does not match its own content", ErrCustodyChainBroken, i, e.EventID)
		}
		prev = e.EventHash
	}
	return nil
}

// AddTransformation appends a Transformation to evidenceID's latest
// manifest — refused once that manifest is FINALIZED (a transformation
// chain, like every other manifest field, is immutable after
// finalization; a NEW derived-evidence manifest, not an edit to this
// one, is how a later transformation gets recorded).
func (r *Registry) AddTransformation(evidenceID string, t Transformation) (Manifest, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	vs := r.versions[evidenceID]
	if len(vs) == 0 {
		return Manifest{}, fmt.Errorf("%w: %s", ErrManifestNotFound, evidenceID)
	}
	cur := vs[len(vs)-1]
	if cur.State == StateFinalized || cur.State == StateSuperseded {
		return Manifest{}, ErrFinalizedIsImmutable
	}
	cur.TransformationChain = append(append([]Transformation(nil), cur.TransformationChain...), t)
	vs[len(vs)-1] = cur
	return cur, nil
}

// VerifyManifestHash independently re-derives m's ManifestHash from its
// own semantic fields (m must be FINALIZED — an unfinalized manifest
// has no ManifestHash to check).
func VerifyManifestHash(m Manifest) error {
	if m.State != StateFinalized {
		return fmt.Errorf("manifest: cannot verify hash of a non-FINALIZED manifest (state=%s)", m.State)
	}
	want, err := computeManifestHash(m)
	if err != nil {
		return err
	}
	if want != m.ManifestHash {
		return fmt.Errorf("manifest: hash mismatch: recorded=%s recomputed=%s", m.ManifestHash, want)
	}
	return nil
}
