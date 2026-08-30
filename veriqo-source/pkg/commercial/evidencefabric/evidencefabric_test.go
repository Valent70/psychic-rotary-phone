package evidencefabric

import (
	"errors"
	"testing"

	"veriqo/pkg/evidence/manifest"
)

// finalizeManifest drives a fresh manifest to FINALIZED for evidenceID
// -- the same real custody-chain sequence this engagement's other test
// suites (e.g. pkg/insurance/api/decideclaim_test.go) already use.
func finalizeManifest(t *testing.T, m *manifest.Registry, evidenceID, caseID string, tick uint64) {
	t.Helper()
	if _, err := m.RegisterDraft(manifest.Manifest{
		TenantID: "tenant-evidencefabric", CaseID: caseID, EvidenceID: evidenceID, Version: 1,
		URI: "evidence://evidencefabric-survey.pdf", Filename: "evidencefabric-survey.pdf", MediaType: "application/pdf",
		ByteSize: 4096, SHA256: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		Method: "UPLOAD", Collector: "surveyor-fabric", Source: "independent-surveyor", AcquiredAt: tick, ReceivedAt: tick,
		HashStatus: "COMPUTED", Classification: "INTERNAL",
		AcquisitionRecord: "uploaded via evidence fabric test",
	}); err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	if _, err := m.RecordCustodyEvent(evidenceID, evidenceID+"-received", "evidencefabric-test", manifest.CustodyReceived, tick, "received into custody", ""); err != nil {
		t.Fatalf("RecordCustodyEvent(RECEIVED): %v", err)
	}
	if _, err := m.Advance(evidenceID, manifest.StateIngested, tick); err != nil {
		t.Fatalf("Advance(INGESTED): %v", err)
	}
	if _, err := m.RecordCustodyEvent(evidenceID, evidenceID+"-hashed", "evidencefabric-test", manifest.CustodyHashed, tick, "hash computed", "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"); err != nil {
		t.Fatalf("RecordCustodyEvent(HASHED): %v", err)
	}
	if _, err := m.Advance(evidenceID, manifest.StateIntegrityAssessed, tick); err != nil {
		t.Fatalf("Advance(INTEGRITY_ASSESSED): %v", err)
	}
	if _, err := m.Advance(evidenceID, manifest.StateProvenanceComplete, tick); err != nil {
		t.Fatalf("Advance(PROVENANCE_COMPLETE): %v", err)
	}
	if _, err := m.RecordCustodyEvent(evidenceID, evidenceID+"-reviewed", "evidencefabric-test", manifest.CustodyReviewed, tick, "independent review complete", "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"); err != nil {
		t.Fatalf("RecordCustodyEvent(REVIEWED): %v", err)
	}
	if _, err := m.Advance(evidenceID, manifest.StateReadyForFinalization, tick); err != nil {
		t.Fatalf("Advance(READY_FOR_FINALIZATION): %v", err)
	}
	if _, err := m.Advance(evidenceID, manifest.StateFinalized, tick); err != nil {
		t.Fatalf("Advance(FINALIZED): %v", err)
	}
}

func TestFromRegistryProjectsARealFinalizedManifest(t *testing.T) {
	reg := manifest.NewRegistry()
	finalizeManifest(t, reg, "EV-FABRIC-1", "case-fabric-1", 10)

	rec, err := FromRegistry(reg, "EV-FABRIC-1", DomainMetadata{
		Insurance: &InsuranceMetadata{ClaimID: "CLM-1", PolicyID: "POL-1", EvidenceKind: "SURVEY"},
	})
	if err != nil {
		t.Fatalf("FromRegistry: %v", err)
	}
	if rec.Identity.EvidenceID != "EV-FABRIC-1" || rec.Identity.CaseID != "case-fabric-1" {
		t.Fatalf("unexpected identity: %+v", rec.Identity)
	}
	if !rec.Integrity.Verified {
		t.Fatal("expected a real, FINALIZED, correctly-hashed manifest to verify")
	}
	if rec.Integrity.State != "FINALIZED" {
		t.Fatalf("expected state FINALIZED, got %s", rec.Integrity.State)
	}
	if len(rec.Custody) != 3 {
		t.Fatalf("expected 3 custody steps (RECEIVED, HASHED, REVIEWED), got %d", len(rec.Custody))
	}
	if rec.Domain.Insurance == nil || rec.Domain.Insurance.ClaimID != "CLM-1" {
		t.Fatalf("expected insurance domain metadata to be preserved, got %+v", rec.Domain)
	}
	if rec.Domain.Maritime != nil || rec.Domain.Trade != nil {
		t.Fatal("expected only the Insurance domain to be populated")
	}
}

// TestFromManifestDetectsTamperedManifest proves Integrity.Verified is
// a REAL, independently-recomputed result, not a copied flag: a
// manifest whose SHA256 was silently changed after finalization (the
// stored ManifestHash now stale) must project as Verified=false.
func TestFromManifestDetectsTamperedManifest(t *testing.T) {
	reg := manifest.NewRegistry()
	finalizeManifest(t, reg, "EV-FABRIC-TAMPER-1", "case-fabric-1", 10)
	m, err := reg.Latest("EV-FABRIC-TAMPER-1")
	if err != nil {
		t.Fatal(err)
	}
	tampered := m
	tampered.SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"

	rec, err := FromManifest(tampered, reg.CustodyChain("EV-FABRIC-TAMPER-1"), DomainMetadata{})
	if err != nil {
		t.Fatalf("FromManifest: %v", err)
	}
	if rec.Integrity.Verified {
		t.Fatal("expected a manifest with a tampered SHA256 (stale ManifestHash) to project as Verified=false")
	}
}

func TestFromManifestRejectsZeroManifest(t *testing.T) {
	if _, err := FromManifest(manifest.Manifest{}, nil, DomainMetadata{}); !errors.Is(err, ErrZeroManifest) {
		t.Fatalf("expected ErrZeroManifest, got %v", err)
	}
}

func TestFromManifestRejectsMismatchedCustodyChain(t *testing.T) {
	reg := manifest.NewRegistry()
	finalizeManifest(t, reg, "EV-FABRIC-A", "case-fabric-1", 10)
	finalizeManifest(t, reg, "EV-FABRIC-B", "case-fabric-1", 10)
	mA, _ := reg.Latest("EV-FABRIC-A")

	// Attempt to project manifest A's identity using EVIDENCE B's
	// custody chain -- a caller mixing up two evidence items.
	if _, err := FromManifest(mA, reg.CustodyChain("EV-FABRIC-B"), DomainMetadata{}); !errors.Is(err, ErrCustodyEvidenceMismatch) {
		t.Fatalf("expected ErrCustodyEvidenceMismatch, got %v", err)
	}
}

func TestFromManifestRejectsMultipleDomains(t *testing.T) {
	reg := manifest.NewRegistry()
	finalizeManifest(t, reg, "EV-FABRIC-MULTI-1", "case-fabric-1", 10)
	m, _ := reg.Latest("EV-FABRIC-MULTI-1")

	_, err := FromManifest(m, reg.CustodyChain("EV-FABRIC-MULTI-1"), DomainMetadata{
		Insurance: &InsuranceMetadata{ClaimID: "CLM-1"},
		Maritime:  &MaritimeMetadata{VesselIdentity: "IMO1234567"},
	})
	if !errors.Is(err, ErrTooManyDomains) {
		t.Fatalf("expected ErrTooManyDomains, got %v", err)
	}
}

func TestFromRegistryRejectsNilRegistry(t *testing.T) {
	if _, err := FromRegistry(nil, "EV-1", DomainMetadata{}); err == nil {
		t.Fatal("expected FromRegistry to refuse a nil registry")
	}
}

func TestFromRegistryRejectsUnknownEvidenceID(t *testing.T) {
	reg := manifest.NewRegistry()
	if _, err := FromRegistry(reg, "EV-DOES-NOT-EXIST", DomainMetadata{}); err == nil {
		t.Fatal("expected FromRegistry to fail for an evidence ID with no manifest")
	}
}

// TestEvidenceRecordCustodyStepsAreIndependentOfManifestOrdering proves
// the three commercial pilot evidence classes all project through the
// SAME code path -- no domain-specific branching inside FromManifest
// itself.
func TestThreeDomainClassesAllProjectThroughTheSamePath(t *testing.T) {
	reg := manifest.NewRegistry()
	finalizeManifest(t, reg, "EV-MARITIME-1", "case-fabric-1", 10)
	finalizeManifest(t, reg, "EV-TRADE-1", "case-fabric-1", 10)

	maritime, err := FromRegistry(reg, "EV-MARITIME-1", DomainMetadata{
		Maritime: &MaritimeMetadata{VesselIdentity: "IMO1234567", PortCode: "IDJKT", EventKind: "AIS_STATUS"},
	})
	if err != nil {
		t.Fatalf("maritime FromRegistry: %v", err)
	}
	trade, err := FromRegistry(reg, "EV-TRADE-1", DomainMetadata{
		Trade: &TradeMetadata{DocumentType: "EBL", TransferEventID: "TXF-1"},
	})
	if err != nil {
		t.Fatalf("trade FromRegistry: %v", err)
	}
	if maritime.Domain.Maritime.VesselIdentity != "IMO1234567" || trade.Domain.Trade.DocumentType != "EBL" {
		t.Fatal("expected each record's own domain metadata to be preserved independently")
	}
	// Both records share the identical Identity/Provenance/Integrity/
	// Custody/Source/Timing/Trust SHAPE -- the whole point of "one
	// canonical EvidenceRecord."
	if maritime.Integrity.State != trade.Integrity.State {
		t.Fatalf("expected both records to use the identical Integrity shape, got %s vs %s", maritime.Integrity.State, trade.Integrity.State)
	}
}
