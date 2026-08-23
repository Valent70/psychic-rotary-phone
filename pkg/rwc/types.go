// Package rwc is the RWC v2 (Real-World Validation Corpus v2) native
// integration adapter: the smallest necessary code to carry real-world
// maritime/commodity corpus data (RWC-001, RWC-002) through VERIQO's
// existing native execution path (veriqo/kernel.Kernel ->
// pkg/lifecycle.Orchestrator.RunUnified -> pkg/execution.Engine ->
// pkg/canonical.Pipeline.RunCanonical -> pkg/replay.Engine) rather than
// a parallel decision engine.
//
// PROVENANCE OF THIS PACKAGE. The domain logic here (the port constraint
// table, the vessel/port constraint evaluator, the IMO check-digit
// validator, the MMSI MID table, the RWC-001/RWC-002 corpus entries) was
// authored on the sibling branch claude/l99-veriqo-development-8hmx00
// and ported onto this branch in round R22. The two branches share only
// an empty initial commit and are independent rewrites, so nothing was
// merged: the domain files were copied and every call site was re-bound
// to THIS branch's own APIs. What changed in the port is recorded in
// docs/RWC_V2_NATIVE_INTEGRATION_BASELINE.md.
//
// This package does not reimplement Evidence, Trust, Decision, Ledger,
// or Replay — it builds the inputs those engines already accept
// (canonical.CaseInput, lifecycle.Intent, lifecycle.EvidencePlan) from
// RWC corpus data, and interprets their real outputs (decision.Action,
// canonical.CanonicalCertificate, lifecycle.LifecycleCertificate) into
// the vocabulary the RWC corpus requires (PASS/FAIL/CONDITIONAL/
// INSUFFICIENT_EVIDENCE decision classes; a provenance-status vocabulary
// for claim/corroboration separation) that does not otherwise exist in
// this codebase.
//
// It constructs no evidence engine of its own: there is no
// fusion.NewEngine, contradiction.NewArbitrationEngine,
// canonical.NewPipeline or ontology.New call anywhere in this package.
// That is not a stylistic claim — internal/nobypass scans the whole
// source tree for exactly those four constructors and fails the build if
// one appears outside its audited allowlist, and this package is not on
// any of those allowlists.
package rwc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// Verdict is the RWC-domain decision class the corpus requires.
//
// HONEST SCOPE: a Verdict is computed by InterpretVerdict from the
// evidence-derived ConstraintResult, and the native decision.Decision is
// used to CROSS-CHECK it, not to produce it.
// The native engine is always run first (Run is the only way to obtain a
// decision.Decision here), and a disagreement between the two is
// surfaced, but the mapping from constraint findings to PASS/FAIL/
// CONDITIONAL is this package's own arithmetic. Do not read a Verdict as
// "the VERIQO decision engine said PASS".
type Verdict string

const (
	VerdictPass                 Verdict = "PASS"
	VerdictFail                 Verdict = "FAIL"
	VerdictConditional          Verdict = "CONDITIONAL"
	VerdictInsufficientEvidence Verdict = "INSUFFICIENT_EVIDENCE"
)

// ProvenanceStatus is the claim/corroboration separation the corpus
// requires, and which does not exist as a named type anywhere else in
// this codebase. pkg/moat/provenance.ProvenanceStatus is a different
// vocabulary answering a different question (does a set of sources share
// a declared upstream), and pkg/evidence/provenance.OriginClass answers a
// third (how far from a rights-cleared external source is this record).
// This type is about one specific claim: has anything independent of the
// claimant actually confirmed it.
//
// It is computed from the REAL outputs of pkg/moat/contradiction
// (CanonicalResult.Truth) and pkg/moat/provenance
// (CanonicalResult.Provenance) — see ClassifyProvenance in policy.go —
// never asserted by RWC input data itself.
type ProvenanceStatus string

const (
	StatusClaimed      ProvenanceStatus = "CLAIMED"
	StatusCorroborated ProvenanceStatus = "CORROBORATED"
	StatusUnverified   ProvenanceStatus = "UNVERIFIED"
	StatusContradicted ProvenanceStatus = "CONTRADICTED"
)

// EvidenceCategory encodes the typed-evidence vocabulary the corpus
// requires (VESSEL_IDENTITY, CARGO_IDENTITY, ...).
// pkg/evidence/ontology.Type is a closed, audit-fixed enum with no such
// categories; rather than editing that fixed enum for one corpus, RWC v2
// encodes the category on canonical.CaseInput.Predicate (a plain string
// field the native pipeline already treats as caller-defined claim
// identity) and records it explicitly here for the evidence bundle.
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

// canonicalJSON deterministically encodes v. For a Go struct,
// json.Marshal emits fields in struct-definition order and map keys in
// sorted order, both of which are documented, stable properties of the
// standard library — so this is a thin, explicit wrapper rather than a
// new encoding scheme, kept as one named function so every hash in this
// package goes through the same auditable path.
func canonicalJSON(v any) ([]byte, error) {
	return json.Marshal(v)
}

// sha256Hex is the one hash function every RWC-scoped hash in this
// package uses. It deliberately does not call internal/sourcehash: that
// package hashes release trees and files, not arbitrary structured
// records. Every subsystem in this repository that needs a record hash
// defines its own domain-prefixed SHA-256 (see pkg/lineage.Node.
// computeHash, pkg/governance/envelope.Envelope.ID,
// pkg/execution.Context.hash); this follows that same pattern with its
// own explicit domain prefix.
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
