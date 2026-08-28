// Package ontology closes PHASE 2 of the v7.10.3 production-readiness
// audit: "Jangan lagi `Kind string` sebagai satu-satunya semantic
// typing."
//
// Before this package, an evidence item in VERIQO was a fusion.Evidence
// — a (Claim, Source, Value, Reliability, Tick) tuple. That is enough
// to arbitrate a claim and nothing else. It cannot express that an SAR
// observation and a registry record are epistemically different kinds
// of thing, that a document was received three weeks after it was
// signed, that an inference is derived rather than observed, or that
// a correction supersedes an earlier statement.
//
// The ontology models evidence as a signed, bitemporal, typed
// subject-predicate-object assertion with an explicit epistemic class.
// Four properties are enforced mechanically, not by convention:
//
//  1. CONTENT ADDRESSING — EvidenceID is SHA-256 over the canonical
//     serialization of every semantic field. Two byte-identical
//     assertions have one ID; changing any field changes the ID.
//  2. BITEMPORALITY — ObservedAt (when the world was that way) is
//     distinct from ReceivedAt (when VERIQO learned it) and from
//     ValidFrom/ValidTo (when the assertion is asserted to hold).
//     "What did VERIQO know at tick T" and "what was true at tick T"
//     are different questions and this package can answer both.
//  3. EPISTEMIC CLASS — Observation/Measurement/Document/Statement are
//     not interchangeable with DerivedInference/Prediction. A derived
//     inference may never be treated as a primary observation; see
//     Class() and ErrDerivedTreatedAsPrimary.
//  4. SCHEMA VERSIONING — SchemaVersion is part of the ID. An evidence
//     record produced under v1 remains verifiable forever even after
//     v2 adds fields (PHASE 51, versioned contracts).
package ontology

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// SchemaVersionV1 is the current evidence schema version. It is part
// of every EvidenceID; bumping it is a deliberate, breaking, auditable
// act, never an implicit one.
const SchemaVersionV1 = "veriqo.evidence/v1"

// Type is the formal evidence type. The full set is fixed by the
// audit; adding one is a schema-version event.
type Type string

const (
	TypeObservation        Type = "Observation"
	TypeMeasurement        Type = "Measurement"
	TypeDocument           Type = "Document"
	TypeStatement          Type = "Statement"
	TypeSensorObservation  Type = "SensorObservation"
	TypeAISObservation     Type = "AISObservation"
	TypeSARObservation     Type = "SARObservation"
	TypeOpticalObservation Type = "OpticalObservation"
	TypeRegistryRecord     Type = "RegistryRecord"
	TypeTradeRecord        Type = "TradeRecord"
	TypeFinancialRecord    Type = "FinancialRecord"
	TypeIdentityAssertion  Type = "IdentityAssertion"
	TypeDerivedInference   Type = "DerivedInference"
	TypePrediction         Type = "Prediction"
	TypeOutcome            Type = "Outcome"
	TypeCorrection         Type = "Correction"
)

// EpistemicClass separates what VERIQO OBSERVED from what VERIQO
// CONCLUDED. Conflating the two is how a system launders its own
// output back in as independent evidence — the single most dangerous
// failure mode for a trust engine.
type EpistemicClass string

const (
	// ClassPrimary: recorded from the world by a source.
	ClassPrimary EpistemicClass = "PRIMARY"
	// ClassDerived: produced by VERIQO or another model from other
	// evidence. Never independent of its inputs.
	ClassDerived EpistemicClass = "DERIVED"
	// ClassGroundTruth: an outcome/correction that resolves an earlier
	// prediction. Must come from a source that was not an input to the
	// prediction it resolves (enforced by pkg/moat/calibration).
	ClassGroundTruth EpistemicClass = "GROUND_TRUTH"
)

var typeClass = map[Type]EpistemicClass{
	TypeObservation:        ClassPrimary,
	TypeMeasurement:        ClassPrimary,
	TypeDocument:           ClassPrimary,
	TypeStatement:          ClassPrimary,
	TypeSensorObservation:  ClassPrimary,
	TypeAISObservation:     ClassPrimary,
	TypeSARObservation:     ClassPrimary,
	TypeOpticalObservation: ClassPrimary,
	TypeRegistryRecord:     ClassPrimary,
	TypeTradeRecord:        ClassPrimary,
	TypeFinancialRecord:    ClassPrimary,
	TypeIdentityAssertion:  ClassPrimary,
	TypeDerivedInference:   ClassDerived,
	TypePrediction:         ClassDerived,
	TypeOutcome:            ClassGroundTruth,
	TypeCorrection:         ClassGroundTruth,
}

// KnownTypes returns every modelled type in deterministic order.
func KnownTypes() []Type {
	out := make([]Type, 0, len(typeClass))
	for t := range typeClass {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Errors surfaced by Validate and the class rules.
var (
	ErrUnknownType             = errors.New("ontology: unknown evidence type")
	ErrEmptySubject            = errors.New("ontology: Subject must be non-empty")
	ErrEmptyPredicate          = errors.New("ontology: Predicate must be non-empty")
	ErrEmptySource             = errors.New("ontology: Source must be non-empty")
	ErrBadConfidence           = errors.New("ontology: Confidence must be in [0,1]")
	ErrBadValidity             = errors.New("ontology: ValidTo must be 0 (open) or >= ValidFrom")
	ErrCausallyImpossible      = errors.New("ontology: ReceivedAt precedes ObservedAt — evidence cannot be received before it was observed")
	ErrDerivedWithoutBasis     = errors.New("ontology: derived evidence must declare at least one DerivedFrom evidence ID")
	ErrPrimaryWithBasis        = errors.New("ontology: primary evidence must not declare DerivedFrom — that would make it derived")
	ErrDerivedTreatedAsPrimary = errors.New("ontology: derived evidence may not be used where primary evidence is required")
	ErrIDMismatch              = errors.New("ontology: EvidenceID does not match the record's own content")
	ErrBadSignature            = errors.New("ontology: signature does not verify against the declared public key")
	ErrMissingRequiredAttr     = errors.New("ontology: required attribute for this evidence type is missing")
	ErrCorrectionWithoutTarget = errors.New("ontology: Correction must declare Supersedes")
)

// Provenance is the ontology-level provenance stub: who produced this
// assertion and through what pipeline. It intentionally mirrors the
// identity fields of evidencegraph.DependencyRecord so an Evidence can
// be projected straight into the dependency graph (see
// DependencyProjection).
type Provenance struct {
	ProducerID       string // organisation/system that produced it
	UpstreamID       string // raw feed it was derived from
	TransformationID string // model/algorithm/pipeline version
	Method           string // free-text method descriptor
	Jurisdiction     string // legal jurisdiction of the producer
}

// Evidence is the formal, signed, bitemporal evidence record.
//
// Ticks, not timestamps: VERIQO is a deterministic replayable system
// and has no wall clock. All temporal fields are VERIQO ticks, which
// a deployment maps to real time in exactly one place (its ingest
// adapter), never inside the kernel.
type Evidence struct {
	EvidenceID string `json:"evidence_id"`
	Type       Type   `json:"type"`

	// Assertion: subject-predicate-object.
	Subject   string `json:"subject"`
	Predicate string `json:"predicate"`
	Object    string `json:"object"`

	Source string `json:"source"`

	// Bitemporal fields.
	ObservedAt uint64 `json:"observed_at"` // when the world was this way
	ReceivedAt uint64 `json:"received_at"` // when VERIQO learned it
	ValidFrom  uint64 `json:"valid_from"`
	ValidTo    uint64 `json:"valid_to"` // 0 = open-ended

	Confidence float64    `json:"confidence"`
	Provenance Provenance `json:"provenance"`

	// DerivedFrom lists the EvidenceIDs this record was computed from.
	// Required for ClassDerived, forbidden for ClassPrimary.
	DerivedFrom []string `json:"derived_from,omitempty"`
	// Supersedes is the EvidenceID this record corrects, for
	// TypeCorrection.
	Supersedes string `json:"supersedes,omitempty"`

	// Attributes carries type-specific fields (e.g. unit + value for a
	// Measurement, document hash for a Document). Schema-validated per
	// type; part of the content hash.
	Attributes map[string]string `json:"attributes,omitempty"`

	SchemaVersion string `json:"schema_version"`

	// Signature and PublicKey are optional at construction and
	// mandatory at trust boundaries (see api.Submit).
	Signature string `json:"signature,omitempty"`
	PublicKey string `json:"public_key,omitempty"`
}

// requiredAttributes per type — the concrete answer to "typed evidence
// must actually mean something".
var requiredAttributes = map[Type][]string{
	TypeMeasurement:        {"unit", "value"},
	TypeDocument:           {"document_hash"},
	TypeSensorObservation:  {"sensor_id"},
	TypeAISObservation:     {"mmsi"},
	TypeSARObservation:     {"scene_id"},
	TypeOpticalObservation: {"scene_id"},
	TypeRegistryRecord:     {"registry"},
	TypeTradeRecord:        {"instrument"},
	TypeFinancialRecord:    {"currency"},
	TypeIdentityAssertion:  {"identifier_kind"},
	TypeDerivedInference:   {"method"},
	TypePrediction:         {"horizon"},
}

// Class returns the epistemic class of this evidence.
func (e Evidence) Class() EpistemicClass { return typeClass[e.Type] }

// IsPrimary reports whether this evidence may be counted as an
// independent primary observation.
func (e Evidence) IsPrimary() bool { return e.Class() == ClassPrimary }

// canonicalBytes is the deterministic serialization used for both the
// content ID and the signature. Deliberately hand-rolled and
// key-sorted rather than encoding/json: Go's map marshalling order is
// specified, but its escaping and float formatting are not stable
// contracts, and this value must be reproducible byte-for-byte by an
// independent implementation in another language.
func (e Evidence) canonicalBytes() []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "schema=%s\n", e.SchemaVersion)
	fmt.Fprintf(&b, "type=%s\n", e.Type)
	fmt.Fprintf(&b, "subject=%s\n", e.Subject)
	fmt.Fprintf(&b, "predicate=%s\n", e.Predicate)
	fmt.Fprintf(&b, "object=%s\n", e.Object)
	fmt.Fprintf(&b, "source=%s\n", e.Source)
	fmt.Fprintf(&b, "observed_at=%d\nreceived_at=%d\nvalid_from=%d\nvalid_to=%d\n",
		e.ObservedAt, e.ReceivedAt, e.ValidFrom, e.ValidTo)
	fmt.Fprintf(&b, "confidence=%.9f\n", e.Confidence)
	fmt.Fprintf(&b, "prov.producer=%s\nprov.upstream=%s\nprov.transform=%s\nprov.method=%s\nprov.jurisdiction=%s\n",
		e.Provenance.ProducerID, e.Provenance.UpstreamID, e.Provenance.TransformationID,
		e.Provenance.Method, e.Provenance.Jurisdiction)
	derived := append([]string(nil), e.DerivedFrom...)
	sort.Strings(derived)
	for _, d := range derived {
		fmt.Fprintf(&b, "derived_from=%s\n", d)
	}
	fmt.Fprintf(&b, "supersedes=%s\n", e.Supersedes)
	keys := make([]string, 0, len(e.Attributes))
	for k := range e.Attributes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "attr.%s=%s\n", k, e.Attributes[k])
	}
	return []byte(b.String())
}

// ComputeID returns the content-addressed EvidenceID.
func (e Evidence) ComputeID() string {
	sum := sha256.Sum256(e.canonicalBytes())
	return hex.EncodeToString(sum[:])
}

// New normalises and content-addresses an evidence record. It is the
// only supported constructor: it fills SchemaVersion if empty, applies
// the ID, and validates. A record that New rejects can never enter the
// pipeline.
func New(e Evidence) (Evidence, error) {
	if e.SchemaVersion == "" {
		e.SchemaVersion = SchemaVersionV1
	}
	if e.ReceivedAt == 0 {
		e.ReceivedAt = e.ObservedAt
	}
	if e.ValidFrom == 0 {
		e.ValidFrom = e.ObservedAt
	}
	e.EvidenceID = e.ComputeID()
	if err := e.Validate(); err != nil {
		return Evidence{}, err
	}
	return e, nil
}

// Validate enforces every structural and epistemic rule.
func (e Evidence) Validate() error {
	if _, ok := typeClass[e.Type]; !ok {
		return fmt.Errorf("%w: %q", ErrUnknownType, e.Type)
	}
	if strings.TrimSpace(e.Subject) == "" {
		return ErrEmptySubject
	}
	if strings.TrimSpace(e.Predicate) == "" {
		return ErrEmptyPredicate
	}
	if strings.TrimSpace(e.Source) == "" {
		return ErrEmptySource
	}
	if e.Confidence < 0 || e.Confidence > 1 {
		return ErrBadConfidence
	}
	if e.ValidTo != 0 && e.ValidTo < e.ValidFrom {
		return ErrBadValidity
	}
	if e.ReceivedAt < e.ObservedAt {
		return ErrCausallyImpossible
	}
	switch e.Class() {
	case ClassDerived:
		if len(e.DerivedFrom) == 0 {
			return ErrDerivedWithoutBasis
		}
	case ClassPrimary:
		if len(e.DerivedFrom) > 0 {
			return ErrPrimaryWithBasis
		}
	}
	if e.Type == TypeCorrection && e.Supersedes == "" {
		return ErrCorrectionWithoutTarget
	}
	for _, attr := range requiredAttributes[e.Type] {
		if v, ok := e.Attributes[attr]; !ok || strings.TrimSpace(v) == "" {
			return fmt.Errorf("%w: type=%s attr=%s", ErrMissingRequiredAttr, e.Type, attr)
		}
	}
	if e.EvidenceID != "" && e.EvidenceID != e.ComputeID() {
		return ErrIDMismatch
	}
	return nil
}

// Sign attaches an Ed25519 signature over the canonical bytes. The
// EvidenceID is NOT part of the signed payload (it is derived from the
// same bytes), so a signature verifies the semantic content directly.
func (e Evidence) Sign(priv ed25519.PrivateKey) (Evidence, error) {
	if err := e.Validate(); err != nil {
		return Evidence{}, err
	}
	sig := ed25519.Sign(priv, e.canonicalBytes())
	e.Signature = hex.EncodeToString(sig)
	e.PublicKey = hex.EncodeToString(priv.Public().(ed25519.PublicKey))
	return e, nil
}

// VerifySignature checks the attached signature. Unsigned evidence
// returns ErrBadSignature — callers that accept unsigned evidence must
// say so explicitly rather than getting it by default.
func (e Evidence) VerifySignature() error {
	if e.Signature == "" || e.PublicKey == "" {
		return ErrBadSignature
	}
	sig, err := hex.DecodeString(e.Signature)
	if err != nil {
		return ErrBadSignature
	}
	pub, err := hex.DecodeString(e.PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return ErrBadSignature
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), e.canonicalBytes(), sig) {
		return ErrBadSignature
	}
	return nil
}

// ValidAt reports whether this assertion is asserted to hold at tick t
// (valid time).
func (e Evidence) ValidAt(t uint64) bool {
	if t < e.ValidFrom {
		return false
	}
	if e.ValidTo != 0 && t > e.ValidTo {
		return false
	}
	return true
}

// KnownAt reports whether VERIQO had received this evidence by tick t
// (transaction time). Replaying "what did we know then" MUST filter on
// this, not on ValidAt.
func (e Evidence) KnownAt(t uint64) bool { return e.ReceivedAt <= t }

// RequirePrimary is the guard that stops VERIQO laundering its own
// conclusions back in as independent evidence.
func RequirePrimary(list []Evidence) error {
	for _, e := range list {
		if !e.IsPrimary() {
			return fmt.Errorf("%w: %s (%s)", ErrDerivedTreatedAsPrimary, e.EvidenceID, e.Type)
		}
	}
	return nil
}

// Sort orders evidence deterministically by (ObservedAt, Source,
// EvidenceID) — the canonical order every downstream engine consumes.
func Sort(list []Evidence) []Evidence {
	out := make([]Evidence, len(list))
	copy(out, list)
	sort.Slice(out, func(i, j int) bool {
		if out[i].ObservedAt != out[j].ObservedAt {
			return out[i].ObservedAt < out[j].ObservedAt
		}
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		return out[i].EvidenceID < out[j].EvidenceID
	})
	return out
}

// SetHash is a content-addressed commitment over an ordered evidence
// set — used by the replay engine to prove "the same evidence, in the
// same order, produced the same result".
func SetHash(list []Evidence) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|n=%d|", SchemaVersionV1, len(list))
	for _, e := range Sort(list) {
		fmt.Fprintf(h, "%s|", e.ComputeID())
	}
	return hex.EncodeToString(h.Sum(nil))
}
