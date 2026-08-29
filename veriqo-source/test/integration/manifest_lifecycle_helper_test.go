package integration

import (
	"testing"

	"veriqo/pkg/evidence/manifest"
)

// advanceManifestToFinalized drives evidenceID's manifest from DRAFT to
// FINALIZED, recording the real custody events Authority Round 2's
// transitionPrerequisiteLocked now requires at each step (RECEIVED,
// HASHED, REVIEWED) -- the honest way to reach FINALIZED, mirroring
// what a real caller must do. reg's RegisterDraft manifest must already
// carry a non-empty HashStatus, AcquisitionRecord, and Classification
// (the field-level prerequisites); this helper only supplies the
// custody-event-level ones, which are the same across every fixture in
// this package.
func advanceManifestToFinalized(t *testing.T, reg *manifest.Registry, evidenceID string, tick uint64) manifest.Manifest {
	t.Helper()
	// The manifest's own recorded SHA256 is what the HASHED/REVIEWED
	// custody events must bind their ContentHash to (Final Authority
	// Hardening Round: prerequisite existence is not enough, prerequisite
	// identity binding must also be proven) -- looked up here rather
	// than asked of the caller, since every fixture already set it at
	// RegisterDraft time.
	draft, err := reg.Latest(evidenceID)
	if err != nil {
		t.Fatalf("Latest(%s): %v", evidenceID, err)
	}
	if _, err := reg.RecordCustodyEvent(evidenceID, evidenceID+"-received", "cre-system", manifest.CustodyReceived, tick, "received into custody", ""); err != nil {
		t.Fatalf("RecordCustodyEvent(RECEIVED) for %s: %v", evidenceID, err)
	}
	if _, err := reg.Advance(evidenceID, manifest.StateIngested, tick); err != nil {
		t.Fatalf("Advance(%s) to INGESTED: %v", evidenceID, err)
	}
	if _, err := reg.RecordCustodyEvent(evidenceID, evidenceID+"-hashed", "cre-system", manifest.CustodyHashed, tick, "hash computed", draft.SHA256); err != nil {
		t.Fatalf("RecordCustodyEvent(HASHED) for %s: %v", evidenceID, err)
	}
	if _, err := reg.Advance(evidenceID, manifest.StateIntegrityAssessed, tick); err != nil {
		t.Fatalf("Advance(%s) to INTEGRITY_ASSESSED: %v", evidenceID, err)
	}
	if _, err := reg.Advance(evidenceID, manifest.StateProvenanceComplete, tick); err != nil {
		t.Fatalf("Advance(%s) to PROVENANCE_COMPLETE: %v", evidenceID, err)
	}
	if _, err := reg.RecordCustodyEvent(evidenceID, evidenceID+"-reviewed", "cre-system", manifest.CustodyReviewed, tick, "independent review complete", draft.SHA256); err != nil {
		t.Fatalf("RecordCustodyEvent(REVIEWED) for %s: %v", evidenceID, err)
	}
	if _, err := reg.Advance(evidenceID, manifest.StateReadyForFinalization, tick); err != nil {
		t.Fatalf("Advance(%s) to READY_FOR_FINALIZATION: %v", evidenceID, err)
	}
	m, err := reg.Advance(evidenceID, manifest.StateFinalized, tick)
	if err != nil {
		t.Fatalf("Advance(%s) to FINALIZED: %v", evidenceID, err)
	}
	return m
}
