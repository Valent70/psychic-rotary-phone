package api

// PHASE A (P0-1) — Canonical Evidence Write Authority.
//
// The program's requirement is that every production evidence writer
// travels source -> evidence facade -> canonical evidence contract ->
// adapter -> subsystem, and never source -> subsystem directly. Three
// of those four hops already existed and are NOT rebuilt here:
//
//   - the facade is this package's Facade (Submit/Validate/Resolve/
//     Link/Correlate/Arbitrate/Replay/Verify plus the Raw*/Fusion*
//     families a prior round added so the three direct-engine callers
//     could be migrated onto it);
//   - the adapters are pkg/connector/{aisstream,sar,bol,insurance,
//     payment}, each of which parses a raw wire schema, structurally
//     validates it, and canonicalizes into ontology.Evidence;
//   - the subsystems are reached only through canonical.Pipeline,
//     which internal/nobypass already proves nothing constructs
//     independently.
//
// What did NOT exist was the CONTRACT hop being declared as a thing at
// all. The evidence contract was implicit: ontology.Evidence's shape,
// ontology.SchemaVersionV1, provenance.SchemaVersionV1 and the
// canonical hash function were each individually versioned, but nothing
// said "these, together, at these versions, are the canonical evidence
// contract" — so nothing could check that a writer had honoured it, and
// a gate could not report a contract version because there was none to
// report.
//
// Contract below is that declaration. It is deliberately a composition
// of the versions that already exist rather than a new version number
// of its own invention: a contract whose version could drift
// independently of the schemas it describes would be worse than no
// contract.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"veriqo/pkg/evidence/ontology"
	"veriqo/pkg/evidence/provenance"
)

// ContractVersion is the canonical evidence contract's own version.
// It is bumped only when the SHAPE of the contract changes — a new
// mandatory hop, a new mandatory field — not when a component schema
// revises independently (that shows up in Contract().Components).
const ContractVersion = "veriqo.evidence.contract/v1"

// ContractDescriptor is the machine-readable declaration of what the
// canonical evidence contract currently is. The readiness gate
// (canonical_evidence_production_coverage) reports it, so "the
// canonical contract version is declared" is a checkable fact rather
// than an assurance in prose.
type ContractDescriptor struct {
	Version string `json:"version"`
	// Components are the independently-versioned schemas this contract
	// composes, name -> version. Changing any of them changes Hash
	// below, so a silent component bump is visible in the gate's
	// evidence artifact.
	Components map[string]string `json:"components"`
	// Path is the mandatory hop sequence every production evidence
	// write must take, in order.
	Path []string `json:"path"`
	// EvidenceTypes is every type the ontology models, so the gate's
	// evidence artifact records exactly which vocabulary was in force.
	EvidenceTypes []string `json:"evidence_types"`
	// Hash content-addresses everything above.
	Hash string `json:"hash"`
}

// Contract returns the current canonical evidence contract. It reads
// its component versions from the packages that own them rather than
// restating them, so this descriptor cannot drift from the code.
func Contract() ContractDescriptor {
	types := ontology.KnownTypes()
	names := make([]string, len(types))
	for i, t := range types {
		names[i] = string(t)
	}
	sort.Strings(names)

	d := ContractDescriptor{
		Version: ContractVersion,
		Components: map[string]string{
			"ontology":   ontology.SchemaVersionV1,
			"provenance": provenance.SchemaVersionV1,
		},
		Path: []string{
			"source",
			"adapter (pkg/connector/*: parse, structurally validate, canonicalize)",
			"canonical evidence contract (pkg/evidence/ontology.New: content-address, validate, type-check)",
			"evidence facade (pkg/evidence/api.Facade.Submit)",
			"canonical pipeline (pkg/canonical.Pipeline)",
			"subsystem (fusion / contradiction / dependency graph / decision / twin)",
		},
		EvidenceTypes: names,
	}
	d.Hash = contractHash(d)
	return d
}

func contractHash(d ContractDescriptor) string {
	var b strings.Builder
	fmt.Fprintf(&b, "veriqo.evidence.contract.descriptor/v1\nversion=%s\n", d.Version)
	keys := make([]string, 0, len(d.Components))
	for k := range d.Components {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "component.%s=%s\n", k, d.Components[k])
	}
	for i, p := range d.Path {
		fmt.Fprintf(&b, "path[%d]=%s\n", i, p)
	}
	for _, t := range d.EvidenceTypes {
		fmt.Fprintf(&b, "type=%s\n", t)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// --- lossless projection ---------------------------------------------

// Projection is one evidence record reduced to exactly the fields a
// downstream subsystem is handed, paired with everything the reduction
// dropped. It exists so "the adapters are lossless" is a property a
// test can assert on rather than a claim.
//
// The honest framing matters here. Projecting ontology.Evidence into
// canonical.SourceSubmission IS lossy in the ordinary sense — fusion
// does not need an evidence record's jurisdiction. What must NOT be
// lost is the ability to get back to the original: every projection
// carries the EvidenceID, and the EvidenceID is a content hash over
// every field, so the full record is always recoverable from the store
// and always verifiable against the projection. Project below reports
// both halves so a test can assert the recoverable-identity property
// precisely, instead of asserting a field-equality that would be false.
type Projection struct {
	EvidenceID string `json:"evidence_id"`
	// Carried are the fields that travel with the projection into the
	// subsystem.
	Carried map[string]string `json:"carried"`
	// Dropped names the fields the projection does not carry. Naming
	// them is the point: a field that is dropped without being listed
	// is exactly how a lossy adapter hides.
	Dropped []string `json:"dropped"`
}

// Project reduces an evidence record the way Arbitrate does when it
// builds canonical.SourceSubmission, and reports what it dropped. It
// is the SAME reduction — the field list below mirrors Arbitrate's own
// construction — so a divergence between them is a test failure, not a
// silent difference between what is documented and what runs.
func Project(e ontology.Evidence) Projection {
	p := Projection{
		EvidenceID: e.EvidenceID,
		Carried: map[string]string{
			"source":       e.Source,
			"object":       e.Object,
			"provider":     e.Provenance.ProducerID,
			"upstream":     e.Provenance.UpstreamID,
			"reliability":  fmt.Sprintf("%.9f", reliabilityOf(e)),
			"subject":      e.Subject,
			"predicate":    e.Predicate,
			"evidence_id":  e.EvidenceID,
			"type":         string(e.Type),
			"epistemic":    string(e.Class()),
			"observed_at":  fmt.Sprintf("%d", e.ObservedAt),
			"received_at":  fmt.Sprintf("%d", e.ReceivedAt),
			"schema":       e.SchemaVersion,
			"supersedes":   e.Supersedes,
			"transform":    e.Provenance.TransformationID,
			"jurisdiction": e.Provenance.Jurisdiction,
			"method":       e.Provenance.Method,
		},
		Dropped: []string{
			"valid_from", "valid_to", "confidence (rescaled into reliability)",
			"derived_from", "attributes", "signature", "public_key",
		},
	}
	return p
}

// Recoverable reports whether the original record can be independently
// re-identified from this projection alone: the carried EvidenceID must
// be the content hash of the record it came from. This is the property
// that makes the reduction safe — anything Project dropped is still
// reachable, and still provably the same record.
func (p Projection) Recoverable(original ontology.Evidence) bool {
	return p.EvidenceID != "" &&
		p.EvidenceID == original.EvidenceID &&
		original.ComputeID() == original.EvidenceID
}
