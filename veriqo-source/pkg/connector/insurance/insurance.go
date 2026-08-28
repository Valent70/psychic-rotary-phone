// Package insurance is R-050's insurance claims/policy-evidence
// ingestion contract — one of the five real-world source types the
// requirement traceability matrix named as the genuine engineering gap
// in pkg/connector. Honest scope, stated once here rather than
// buried: this sandbox has no reachable path to any real insurer/
// claims-management-system feed, so this package cannot and does not
// claim to be "live" in this session. What it DOES provide is the
// real, tested machinery a live provider integration would plug
// straight into:
//
//  1. Contract — parses raw claim/policy-evidence submission
//     payloads, structurally validates them, semantically validates
//     them (document hash shape, currency/amount consistency), and
//     canonicalizes a valid submission into
//     pkg/evidence/ontology.Evidence{Type: TypeDocument} — an
//     insurance claim or policy artifact is, at the ontology level, a
//     hashed document; this package's job is to prove it is a
//     STRUCTURALLY VALID one before that hash is trusted anywhere
//     downstream. Domain-specific insurance semantics beyond that
//     (origin classification, verification-status lifecycle,
//     multi-dimensional strength, chain of custody) already live in
//     pkg/insurance/evidence, whose own Record wraps exactly this
//     package's ontology.Evidence output — this package does not
//     duplicate any of that, it only feeds it real, validated input.
//  2. SimulatedConnector — a deterministic, seeded fixture connector
//     implementing pkg/blockers/livedata.FeedConnector, always tagged
//     ModeSynthetic.
package insurance

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"veriqo/pkg/connector"
	"veriqo/pkg/evidence/ontology"
)

var (
	ErrMalformed           = errors.New("insurance: malformed claim-evidence record")
	ErrSemanticallyInvalid = errors.New("insurance: semantically invalid claim-evidence record")
)

// documentHashRE matches a lowercase-hex SHA-256 digest — the exact
// shape every document_hash this package accepts must have, since
// that value becomes ontology.Evidence.Attributes["document_hash"],
// ontology's own required attribute for TypeDocument.
var documentHashRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// knownDocumentTypes is the closed set this contract accepts. Not an
// exhaustive real-world taxonomy -- a deliberately explicit allow-list,
// matching this repo's fail-closed-on-the-unknown convention.
var knownDocumentTypes = map[string]bool{
	"CLAIM_FORM":      true,
	"SURVEY_REPORT":   true,
	"POLICY_SCHEDULE": true,
	"PROOF_OF_LOSS":   true,
	"MEDICAL_RECORD":  true,
	"REPAIR_INVOICE":  true,
	"POLICE_REPORT":   true,
	"CORRESPONDENCE":  true,
}

// wireClaimEvidence is this package's insurance claim/policy evidence
// wire schema: one submitted document, plus the identifying and
// financial context an adjuster needs before touching the document
// itself.
type wireClaimEvidence struct {
	ClaimID      string   `json:"claim_id"`
	PolicyNumber string   `json:"policy_number"`
	DocumentHash string   `json:"document_hash"` // sha256 hex of the underlying artifact
	DocumentType string   `json:"document_type"`
	InsuredParty string   `json:"insured_party"`
	ClaimAmount  *float64 `json:"claim_amount,omitempty"`
	Currency     string   `json:"currency,omitempty"` // required iff ClaimAmount present
	SubmittedUTC int64    `json:"submitted_utc"`
}

func decodeWire(payload string) (wireClaimEvidence, error) {
	var w wireClaimEvidence
	dec := json.NewDecoder(strings.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil {
		return wireClaimEvidence{}, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	for field, v := range map[string]string{
		"claim_id": w.ClaimID, "policy_number": w.PolicyNumber, "document_hash": w.DocumentHash,
		"document_type": w.DocumentType, "insured_party": w.InsuredParty,
	} {
		if strings.TrimSpace(v) == "" {
			return wireClaimEvidence{}, fmt.Errorf("%w: missing %s", ErrMalformed, field)
		}
	}
	if w.SubmittedUTC <= 0 {
		return wireClaimEvidence{}, fmt.Errorf("%w: missing or non-positive submitted_utc", ErrMalformed)
	}
	return w, nil
}

func (w wireClaimEvidence) validateSemantics() error {
	if !documentHashRE.MatchString(w.DocumentHash) {
		return fmt.Errorf("%w: document_hash is not a 64-hex-character sha256 digest", ErrSemanticallyInvalid)
	}
	if !knownDocumentTypes[w.DocumentType] {
		return fmt.Errorf("%w: unknown document_type %q", ErrSemanticallyInvalid, w.DocumentType)
	}
	if w.ClaimAmount != nil {
		if *w.ClaimAmount < 0 {
			return fmt.Errorf("%w: claim_amount %v must be >= 0", ErrSemanticallyInvalid, *w.ClaimAmount)
		}
		if w.Currency == "" {
			return fmt.Errorf("%w: claim_amount present without currency", ErrSemanticallyInvalid)
		}
	}
	if w.Currency != "" && !connector.KnownCurrency(w.Currency) {
		return fmt.Errorf("%w: unrecognized currency %q", ErrSemanticallyInvalid, w.Currency)
	}
	return nil
}

func (w wireClaimEvidence) toOntologyEvidence(source string, receivedAtTick uint64) (ontology.Evidence, error) {
	attrs := map[string]string{
		"document_hash": w.DocumentHash,
		"claim_id":      w.ClaimID,
		"policy_number": w.PolicyNumber,
		"insured_party": w.InsuredParty,
	}
	if w.ClaimAmount != nil {
		attrs["claim_amount"] = fmt.Sprintf("%.2f", *w.ClaimAmount)
		attrs["currency"] = strings.ToUpper(w.Currency)
	}
	return ontology.New(ontology.Evidence{
		Type:       ontology.TypeDocument,
		Subject:    "insurance_claim:" + w.ClaimID,
		Predicate:  "claim_evidence_submitted",
		Object:     w.DocumentType,
		Source:     source,
		ObservedAt: uint64(w.SubmittedUTC), // #nosec G115 -- decodeWire already rejects SubmittedUTC <= 0
		ReceivedAt: receivedAtTick,
		// A submitted document's existence and hash are certain; whether
		// its CONTENT is trustworthy is pkg/insurance/evidence.Strength's
		// job, not this ingestion contract's. Confidence here is
		// deliberately fixed near-1.0, never claim-outcome-derived.
		Confidence: 0.95,
		Attributes: attrs,
	})
}

// Contract is this package's pkg/connector.Decoder implementation.
type Contract struct {
	Source string
}

func (c Contract) SourceType() string { return "Insurance" }

func (c Contract) Decode(payload string, receivedAtTick uint64) (ontology.Evidence, error) {
	w, err := decodeWire(payload)
	if err != nil {
		return ontology.Evidence{}, err
	}
	if err := w.validateSemantics(); err != nil {
		return ontology.Evidence{}, err
	}
	source := c.Source
	if source == "" {
		source = "insurance:unspecified"
	}
	return w.toOntologyEvidence(source, receivedAtTick)
}
