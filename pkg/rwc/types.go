// Package rwc is the RWC v2 native integration adapter: the smallest
// necessary code to carry real-world maritime/commodity corpus data
// (RWC-001, RWC-002) through VERIQO's existing native execution path
// (veriqo/kernel.Kernel -> pkg/lifecycle.Orchestrator.RunUnified ->
// pkg/execution.Engine -> pkg/canonical.Pipeline.RunCanonical) rather
// than a parallel decision engine. See
// docs/RWC_V2_NATIVE_INTEGRATION_BASELINE.md for the full map of what
// already existed and why each piece here was necessary.
//
// This package does not reimplement Evidence, Trust, Decision, Ledger,
// or Replay — it builds the inputs those engines already accept
// (canonical.CaseInput, lifecycle.Intent, lifecycle.EvidencePlan) from
// RWC corpus data, and interprets their real outputs (decision.Action,
// canonical.CanonicalCertificate, lifecycle.LifecycleCertificate) into
// the vocabulary the RWC brief requires (PASS/FAIL/CONDITIONAL/
// INSUFFICIENT_EVIDENCE decision classes; CLAIMED/CORROBORATED/
// UNVERIFIED/CONTRADICTED provenance states) that did not previously
// exist anywhere in the codebase (baseline doc gaps G1, G3).
package rwc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// Verdict is the RWC-domain decision class required by the brief. It is
// a pure, deterministic projection of the native decision.Action plus
// evidence-derived hard-constraint/unresolved-constraint findings — see
// InterpretVerdict in policy.go. It is not a second decision engine: no
// Verdict is ever chosen without a prior native decision.Engine.Decide
// call having run.
type Verdict string

const (
	VerdictPass                 Verdict = "PASS"
	VerdictFail                 Verdict = "FAIL"
	VerdictConditional          Verdict = "CONDITIONAL"
	VerdictInsufficientEvidence Verdict = "INSUFFICIENT_EVIDENCE"
)

// ProvenanceStatus is the CLAIMED/CORROBORATED/UNVERIFIED/CONTRADICTED
// distinction the brief requires (§8 of the brief) and which does not
// exist as a named type anywhere in the base VERIQO codebase (baseline
// doc gap G3). It is computed from the REAL outputs of
// pkg/moat/contradiction (Truth.Contradiction) and pkg/moat/provenance
// (IndependenceAssessment) — see ClassifyProvenance in provenance.go —
// never asserted by RWC input data itself.
type ProvenanceStatus string

const (
	StatusClaimed      ProvenanceStatus = "CLAIMED"
	StatusCorroborated ProvenanceStatus = "CORROBORATED"
	StatusUnverified   ProvenanceStatus = "UNVERIFIED"
	StatusContradicted ProvenanceStatus = "CONTRADICTED"
)

// EvidenceCategory encodes the typed-evidence vocabulary the brief's §7
// requires (VESSEL_IDENTITY, CARGO_IDENTITY, ...). pkg/evidence/ontology
// .Type is a closed, audit-fixed 16-value enum with no such categories
// (baseline doc gap G4); rather than editing that fixed enum for one
// corpus, RWC v2 encodes the category on canonical.CaseInput.Predicate
// (a plain string field the native DAG already treats as caller-defined
// claim identity) and records it explicitly here for the evidence
// bundle/audit report.
type EvidenceCategory string

const (
	CategoryVesselIdentity        EvidenceCategory = "VESSEL_IDENTITY"
	CategoryVesselConstraint      EvidenceCategory = "VESSEL_CONSTRAINT"
	CategoryPortConstraint        EvidenceCategory = "PORT_CONSTRAINT"
	CategoryCargoIdentity         EvidenceCategory = "CARGO_IDENTITY"
	CategoryQuantity              EvidenceCategory = "QUANTITY"
	CategoryVoyagePosition        EvidenceCategory = "VOYAGE_POSITION_CLAIM"
	CategoryDocumentExistence     EvidenceCategory = "DOCUMENT_EXISTENCE_CLAIM"
	CategoryTransactionStep       EvidenceCategory = "TRANSACTION_STEP"
	CategorySourceClaim           EvidenceCategory = "SOURCE_CLAIM"
	CategoryExternalCorroboration EvidenceCategory = "EXTERNAL_CORROBORATION"
	CategoryContradiction         EvidenceCategory = "CONTRADICTION"
	CategoryMissingEvidence       EvidenceCategory = "MISSING_EVIDENCE"
)

// canonicalJSON deterministically encodes v: struct field order (Go
// struct definition order for non-map types) plus explicit sorted-key
// re-marshal for any map[string]... field is what json.Marshal already
// does for Go structs, so this is a thin, explicit wrapper rather than a
// new encoding scheme — kept as one named function so every hash in this
// package goes through the same, auditable path, and so a reviewer only
// has to check one function for "is this really deterministic".
func canonicalJSON(v any) ([]byte, error) {
	return json.Marshal(v)
}

// sha256Hex is the one hash function every RWC-scoped hash in this
// package uses (input_hash, canonical fixture hashes) — SHA-256, per the
// brief's §6 requirement ("SHA-256 or repository-standard canonical
// hash"). It deliberately does not call internal/sourcehash: that
// package hashes release trees/files, not arbitrary structured records
// (see baseline doc §9 finding that no single shared "canonical hash"
// function exists repo-wide; every subsystem hand-rolls its own
// versioned SHA-256 format, which is the same pattern applied here with
// its own explicit domain prefix).
func sha256Hex(domain string, data []byte) string {
	h := sha256.New()
	h.Write([]byte(domain))
	h.Write([]byte{'|'})
	h.Write(data)
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:])
}

// hashOf canonically encodes v and hashes it under domain.
func hashOf(domain string, v any) (string, error) {
	b, err := canonicalJSON(v)
	if err != nil {
		return "", err
	}
	return sha256Hex(domain, b), nil
}

// sortedStrings returns a sorted copy — used everywhere a set of names
// (violations, doc names, claim names) must serialize deterministically
// regardless of the order they were discovered in.
func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
