// Package evidencefabric answers Commercialization Sprint item 4
// directly: "Buat SATU canonical Evidence Object ... Claude jangan
// membuat model berbeda untuk maritime, insurance, eBL. Harus satu:
// EvidenceRecord dengan domain-specific metadata."
//
// EvidenceRecord is a read-only PROJECTION over the FROZEN
// pkg/evidence/manifest.Registry -- the tamper-evident, custody-
// chained, hash-verified evidence kernel this whole engagement has
// hardened across many prior rounds (see docs/
// VERIQO_CORE_TRUST_KERNEL_FREEZE.md). This package is new and
// additive: it never re-implements manifest state transitions,
// custody-chain verification, or hash computation -- it only reads a
// real manifest.Manifest and its real []manifest.CustodyEvent chain
// (both already produced by the frozen kernel) and re-shapes them into
// ONE commercially presentable, domain-neutral structure, with a
// single optional domain_metadata sub-object for whichever of the
// three commercial pilot evidence classes (maritime, insurance, trade)
// this particular piece of evidence belongs to.
//
// There is exactly one constructor, FromManifest, and it is a PURE
// function of its inputs -- no hidden state, no re-derivation of
// anything the frozen kernel already computed. This mirrors the exact
// "obtain only via the one real constructor" discipline
// decision.Decision and action.ActionAuthorization already use one
// layer down.
package evidencefabric

import (
	"errors"
	"fmt"

	"veriqo/pkg/evidence/manifest"
)

var (
	// ErrZeroManifest is FromManifest's refusal when m is the manifest
	// package's own zero value (no EvidenceID at all) -- there is
	// nothing real to project.
	ErrZeroManifest = errors.New("evidencefabric: cannot build an EvidenceRecord from a zero-value manifest.Manifest")

	// ErrCustodyEvidenceMismatch is FromManifest's refusal when a
	// caller passes a custody chain that does not actually belong to
	// m's own EvidenceID -- a real correctness check (a caller mixing
	// up two evidence items' custody chains), not a fabricated
	// validation.
	ErrCustodyEvidenceMismatch = errors.New("evidencefabric: a custody event's EvidenceID does not match the manifest's own EvidenceID")

	// ErrTooManyDomains is FromManifest's refusal when more than one
	// of DomainMetadata's three sub-objects is populated -- exactly
	// one domain per record, per the reviewer's own diagram.
	ErrTooManyDomains = errors.New("evidencefabric: DomainMetadata must carry exactly one of Maritime, Insurance, or Trade -- never more than one")
)

// Identity is the EvidenceRecord's identity block.
type Identity struct {
	EvidenceID string `json:"evidence_id"`
	CaseID     string `json:"case_id"`
	TenantID   string `json:"tenant_id"`
	Version    int    `json:"version"`
}

// Provenance is the EvidenceRecord's provenance block -- how this
// evidence came to be known to VERIQO and what has been done to it
// since.
type Provenance struct {
	AcquisitionRecord   string `json:"acquisition_record,omitempty"`
	TransformationCount int    `json:"transformation_count"`
	CustodyChainHead    string `json:"custody_chain_head"`
}

// Integrity is the EvidenceRecord's integrity block. Verified is
// computed by actually calling manifest.VerifyManifestHash inside
// FromManifest -- never copied from a caller-supplied flag, and never
// defaulted to true.
type Integrity struct {
	SHA256          string `json:"sha256"`
	SHA512          string `json:"sha512,omitempty"`
	HashStatus      string `json:"hash_status"`
	SignatureStatus string `json:"signature_status"`
	ManifestHash    string `json:"manifest_hash,omitempty"`
	State           string `json:"state"`
	Verified        bool   `json:"verified"`
}

// CustodyStep is one hop in the EvidenceRecord's custody chain,
// re-shaped from a real manifest.CustodyEvent.
type CustodyStep struct {
	EventID string `json:"event_id"`
	Actor   string `json:"actor"`
	Tick    uint64 `json:"tick"`
	Action  string `json:"action"`
	Reason  string `json:"reason,omitempty"`
}

// Source is the EvidenceRecord's source block -- who/what/how this
// evidence was originally acquired.
type Source struct {
	Method    string `json:"method"`
	Collector string `json:"collector"`
	Origin    string `json:"origin"`
	System    string `json:"system,omitempty"`
}

// Timing is the EvidenceRecord's timing block, all in the same
// application-tick vocabulary the rest of this repository uses (see
// this repository's own documented "Veriqo cannot claim trusted
// timestamping" caveat -- ticks are application-defined, not RFC 3161
// timestamps).
type Timing struct {
	AcquiredAt  uint64 `json:"acquired_at"`
	ReceivedAt  uint64 `json:"received_at"`
	FinalizedAt uint64 `json:"finalized_at,omitempty"`
}

// Trust is the EvidenceRecord's classification/handling block --
// deliberately NOT a trust score or confidence number (see pkg/
// insurance/guardrails' whole-tree ban on opaque confidence scores);
// this is classification/legal-hold metadata only.
type Trust struct {
	Classification string   `json:"classification"`
	Markings       []string `json:"markings,omitempty"`
	LegalHold      bool     `json:"legal_hold"`
}

// MaritimeMetadata is domain_metadata for the "Maritime operational
// evidence" pilot class (AIS/event, port event, vessel identity, time,
// location).
type MaritimeMetadata struct {
	VesselIdentity string `json:"vessel_identity,omitempty"`
	PortCode       string `json:"port_code,omitempty"`
	EventKind      string `json:"event_kind,omitempty"` // e.g. "AIS_STATUS", "PORT_EVENT"
	Location       string `json:"location,omitempty"`
}

// InsuranceMetadata is domain_metadata for the "Insurance claim
// evidence" pilot class (claim, policy, survey, adjuster evidence,
// supporting documents).
type InsuranceMetadata struct {
	ClaimID      string `json:"claim_id,omitempty"`
	PolicyID     string `json:"policy_id,omitempty"`
	PartyID      string `json:"party_id,omitempty"`
	EvidenceKind string `json:"evidence_kind,omitempty"` // e.g. "SURVEY", "ADJUSTER_REPORT", "FNOL", "INVOICE"
}

// TradeMetadata is domain_metadata for the "eBL / Electronic Trade
// Record" pilot class (source event, signature, identity, timestamp,
// hash, transfer event, custody).
type TradeMetadata struct {
	DocumentType    string `json:"document_type,omitempty"` // e.g. "EBL", "BILL_OF_LADING"
	TransferEventID string `json:"transfer_event_id,omitempty"`
	HolderIdentity  string `json:"holder_identity,omitempty"`
}

// DomainMetadata carries exactly one of the three commercial pilot
// evidence classes' own metadata -- never more than one, per the
// reviewer's own diagram (domain_metadata branches into maritime /
// insurance / trade, not all three at once for one record).
type DomainMetadata struct {
	Maritime  *MaritimeMetadata  `json:"maritime,omitempty"`
	Insurance *InsuranceMetadata `json:"insurance,omitempty"`
	Trade     *TradeMetadata     `json:"trade,omitempty"`
}

func (d DomainMetadata) populatedCount() int {
	n := 0
	if d.Maritime != nil {
		n++
	}
	if d.Insurance != nil {
		n++
	}
	if d.Trade != nil {
		n++
	}
	return n
}

// EvidenceRecord is the ONE canonical evidence object every
// commercial-layer surface (the Commercial API, the Evidence Dossier,
// the demo cases) uses -- never a maritime-specific, insurance-
// specific, or eBL-specific shape. Obtain one only via FromManifest.
type EvidenceRecord struct {
	Identity   Identity       `json:"identity"`
	Provenance Provenance     `json:"provenance"`
	Integrity  Integrity      `json:"integrity"`
	Custody    []CustodyStep  `json:"custody"`
	Source     Source         `json:"source"`
	Timing     Timing         `json:"timing"`
	Trust      Trust          `json:"trust"`
	Domain     DomainMetadata `json:"domain_metadata"`
}

// FromManifest projects a real manifest.Manifest and its real custody
// chain into ONE canonical EvidenceRecord, attaching exactly one
// domain's metadata. It independently calls manifest.VerifyManifestHash
// -- Integrity.Verified reports what THAT call actually found, never a
// caller-supplied claim.
func FromManifest(m manifest.Manifest, custody []manifest.CustodyEvent, domain DomainMetadata) (EvidenceRecord, error) {
	if m.EvidenceID == "" {
		return EvidenceRecord{}, ErrZeroManifest
	}
	for _, c := range custody {
		if c.EvidenceID != m.EvidenceID {
			return EvidenceRecord{}, fmt.Errorf("%w: manifest=%s custody_event=%s (event %s)", ErrCustodyEvidenceMismatch, m.EvidenceID, c.EvidenceID, c.EventID)
		}
	}
	if domain.populatedCount() > 1 {
		return EvidenceRecord{}, ErrTooManyDomains
	}

	steps := make([]CustodyStep, len(custody))
	for i, c := range custody {
		steps[i] = CustodyStep{EventID: c.EventID, Actor: c.Actor, Tick: c.Tick, Action: string(c.Action), Reason: c.Reason}
	}

	verifyErr := manifest.VerifyManifestHash(m)

	return EvidenceRecord{
		Identity: Identity{EvidenceID: m.EvidenceID, CaseID: m.CaseID, TenantID: m.TenantID, Version: m.Version},
		Provenance: Provenance{
			AcquisitionRecord: m.AcquisitionRecord, TransformationCount: len(m.TransformationChain),
			CustodyChainHead: m.CustodyChainHead,
		},
		Integrity: Integrity{
			SHA256: m.SHA256, SHA512: m.SHA512, HashStatus: m.HashStatus, SignatureStatus: m.SignatureStatus,
			ManifestHash: m.ManifestHash, State: string(m.State), Verified: verifyErr == nil,
		},
		Custody: steps,
		Source:  Source{Method: m.Method, Collector: m.Collector, Origin: m.Source, System: m.System},
		Timing:  Timing{AcquiredAt: m.AcquiredAt, ReceivedAt: m.ReceivedAt, FinalizedAt: m.FinalizedAt},
		Trust:   Trust{Classification: m.Classification, Markings: append([]string(nil), m.Markings...), LegalHold: m.LegalHold},
		Domain:  domain,
	}, nil
}

// FromRegistry is the convenience path most callers use: look up the
// latest manifest and its full custody chain for evidenceID directly
// from a live *manifest.Registry, then project it.
func FromRegistry(reg *manifest.Registry, evidenceID string, domain DomainMetadata) (EvidenceRecord, error) {
	if reg == nil {
		return EvidenceRecord{}, fmt.Errorf("evidencefabric: FromRegistry requires a non-nil *manifest.Registry")
	}
	m, err := reg.Latest(evidenceID)
	if err != nil {
		return EvidenceRecord{}, fmt.Errorf("evidencefabric: %w", err)
	}
	return FromManifest(m, reg.CustodyChain(evidenceID), domain)
}
