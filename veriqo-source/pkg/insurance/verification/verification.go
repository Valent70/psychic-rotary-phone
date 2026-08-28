// Package verification produces the tamper-evident Manifest that
// anchors a VICE case's dossier — blueprint §31. A Manifest is NOT the
// dossier itself; it is a small, independently reproducible
// cryptographic summary of the evidence a dossier was built from, so a
// third party (auditor, reinsurer, regulator) can verify nothing was
// added, removed, or altered after the dossier was generated, without
// re-running any of VICE's analysis packages.
package verification

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"

	"veriqo/pkg/insurance/evidence"
)

var (
	ErrEmptyCaseID  = errors.New("verification: CaseID must be non-empty")
	ErrNoEvidence   = errors.New("verification: cannot generate a manifest over zero evidence records for this case")
	ErrHashMismatch = errors.New("verification: recomputed evidence root hash does not match the manifest")
)

// Manifest is a deterministically reproducible summary of every
// evidence record a case's dossier drew on at the moment it was
// generated.
type Manifest struct {
	CaseID          string `json:"case_id"`
	GeneratedAtTick uint64 `json:"generated_at_tick"`
	EvidenceCount   int    `json:"evidence_count"`
	// EvidenceIDs are sorted lexicographically for reproducibility —
	// Generate never depends on registry iteration order.
	EvidenceIDs []string `json:"evidence_ids"`
	// EvidenceRootHash is a SHA-256 over the sorted EvidenceIDs.
	// EvidenceID is itself content-addressed (see
	// pkg/evidence/ontology.New's canonical-bytes hash), so this is a
	// hash-of-hashes: any change to an evidence record's own content
	// changes its EvidenceID, which changes this root hash too — the
	// same tamper-evidence property the underlying Unified Evidence
	// Engine already provides per-record, extended here to the whole
	// evidence set a dossier was built from.
	EvidenceRootHash string `json:"evidence_root_hash"`
}

// Generate computes a Manifest over every evidence.Record in reg whose
// CaseID equals caseID. Deterministic: two calls over an unchanged
// registry produce a byte-identical EvidenceRootHash regardless of
// registry insertion order.
func Generate(reg *evidence.Registry, caseID string, atTick uint64) (Manifest, error) {
	if caseID == "" {
		return Manifest{}, ErrEmptyCaseID
	}
	var ids []string
	if reg != nil {
		for _, rec := range reg.All() {
			if rec.CaseID != caseID {
				continue
			}
			ids = append(ids, rec.EvidenceID())
		}
	}
	if len(ids) == 0 {
		return Manifest{}, ErrNoEvidence
	}
	sort.Strings(ids)

	h := sha256.New()
	for _, id := range ids {
		h.Write([]byte(id))
		// 0x00 separator: EvidenceIDs are hex-encoded SHA-256 digests
		// (see ontology.Evidence.computeID), so this cannot itself
		// appear inside an ID and cannot introduce a
		// concatenation-boundary collision between adjacent IDs.
		h.Write([]byte{0})
	}

	return Manifest{
		CaseID:           caseID,
		GeneratedAtTick:  atTick,
		EvidenceCount:    len(ids),
		EvidenceIDs:      ids,
		EvidenceRootHash: hex.EncodeToString(h.Sum(nil)),
	}, nil
}

// Verify recomputes the evidence root hash for m.CaseID from reg's
// CURRENT contents and compares it against m.EvidenceRootHash. A
// mismatch means the case's evidence set has changed — an item added,
// removed, or (since EvidenceID is content-addressed) altered — since
// m was generated.
func Verify(m Manifest, reg *evidence.Registry) error {
	recomputed, err := Generate(reg, m.CaseID, m.GeneratedAtTick)
	if err != nil {
		return err
	}
	if recomputed.EvidenceRootHash != m.EvidenceRootHash {
		return fmt.Errorf("%w: manifest=%s recomputed=%s", ErrHashMismatch, m.EvidenceRootHash, recomputed.EvidenceRootHash)
	}
	return nil
}
