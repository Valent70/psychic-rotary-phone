package packageverify

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"veriqo/pkg/commercial/dossier"
	"veriqo/pkg/commercial/evidencefabric"
	"veriqo/pkg/commercial/verticalslice"
	"veriqo/pkg/evidence/manifest"
	"veriqo/pkg/insurance/action"
	"veriqo/pkg/insurance/causation"
	"veriqo/pkg/insurance/claimworkflow"
	"veriqo/pkg/insurance/cre"
	"veriqo/pkg/insurance/decision"
	"veriqo/pkg/platform/audit"
)

func finalizeManifest(t *testing.T, m *manifest.Registry, evidenceID, caseID string, tick uint64) {
	t.Helper()
	if _, err := m.RegisterDraft(manifest.Manifest{
		TenantID: "tenant-pkgverify", CaseID: caseID, EvidenceID: evidenceID, Version: 1,
		URI: "evidence://pkgverify-survey.pdf", Filename: "pkgverify-survey.pdf", MediaType: "application/pdf",
		ByteSize: 4096, SHA256: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		Method: "UPLOAD", Collector: "surveyor-pkgverify", Source: "independent-surveyor", AcquiredAt: tick, ReceivedAt: tick,
		HashStatus: "COMPUTED", Classification: "INTERNAL", AcquisitionRecord: "uploaded via packageverify test",
	}); err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	if _, err := m.RecordCustodyEvent(evidenceID, evidenceID+"-received", "pkgverify-test", manifest.CustodyReceived, tick, "received into custody", ""); err != nil {
		t.Fatalf("RecordCustodyEvent(RECEIVED): %v", err)
	}
	if _, err := m.Advance(evidenceID, manifest.StateIngested, tick); err != nil {
		t.Fatalf("Advance(INGESTED): %v", err)
	}
	if _, err := m.RecordCustodyEvent(evidenceID, evidenceID+"-hashed", "pkgverify-test", manifest.CustodyHashed, tick, "hash computed", "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"); err != nil {
		t.Fatalf("RecordCustodyEvent(HASHED): %v", err)
	}
	if _, err := m.Advance(evidenceID, manifest.StateIntegrityAssessed, tick); err != nil {
		t.Fatalf("Advance(INTEGRITY_ASSESSED): %v", err)
	}
	if _, err := m.Advance(evidenceID, manifest.StateProvenanceComplete, tick); err != nil {
		t.Fatalf("Advance(PROVENANCE_COMPLETE): %v", err)
	}
	if _, err := m.RecordCustodyEvent(evidenceID, evidenceID+"-reviewed", "pkgverify-test", manifest.CustodyReviewed, tick, "independent review complete", "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"); err != nil {
		t.Fatalf("RecordCustodyEvent(REVIEWED): %v", err)
	}
	if _, err := m.Advance(evidenceID, manifest.StateReadyForFinalization, tick); err != nil {
		t.Fatalf("Advance(READY_FOR_FINALIZATION): %v", err)
	}
	if _, err := m.Advance(evidenceID, manifest.StateFinalized, tick); err != nil {
		t.Fatalf("Advance(FINALIZED): %v", err)
	}
}

func buildRealPackage(t *testing.T, caseID string) string {
	t.Helper()
	in := verticalslice.Input{
		Claim: claimworkflow.ClaimDecisionInput{
			CaseID: caseID, Tick: 10,
			Manifests: []claimworkflow.ManifestSpec{
				{
					EvidenceID: "EV-PKGVERIFY-1", SHA256: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
					URI: "evidence://pkgverify-survey.pdf", Filename: "pkgverify-survey.pdf",
					MediaType: "application/pdf", ByteSize: 4096, Collector: "surveyor-pkgverify", Source: "independent-surveyor",
				},
			},
			Hypothesis:            causation.Hypothesis{ID: "H1", Description: "water ingress during transit"},
			SupportingEvidenceIDs: []string{"EV-PKGVERIFY-1"},
			FindingID:             "finding-pkgverify-1",
			Finding: cre.FindingInput{
				CaseID: caseID, ContractBasis: "clause-1", ObligationRef: "obl-1",
				EventRef: "event-1", QuantumRef: "calc-1", HumanReviewRequired: true,
			},
			Outcome:     decision.OutcomeApproved,
			Rationale:   "primary hypothesis substantiated by grounded, finalized evidence",
			LedgerActor: "pkgverify-test-decision",
		},
		Action: verticalslice.ActionInput{
			Actor: "adjuster-pkgverify-1", PolicyRef: "policy-settlement-v1", Scope: caseID,
			PermittedAction: action.ActionApproveSettlement, Conditions: []string{"reinspection_complete"},
			AuthorizedAt: 10, ExpiresAt: 20, ExecutingActor: "adjuster-pkgverify-1", ExecutionAt: 15,
			LedgerActor: "pkgverify-test-action",
		},
	}
	ledger := audit.NewAuditStore()
	res, err := verticalslice.Run(in, ledger)
	if err != nil {
		t.Fatalf("verticalslice.Run: %v", err)
	}
	reg := manifest.NewRegistry()
	finalizeManifest(t, reg, "EV-PKGVERIFY-1", caseID, 10)
	rec, err := evidencefabric.FromRegistry(reg, "EV-PKGVERIFY-1", evidencefabric.DomainMetadata{
		Insurance: &evidencefabric.InsuranceMetadata{ClaimID: "CLM-1", PolicyID: "POL-1", EvidenceKind: "SURVEY"},
	})
	if err != nil {
		t.Fatalf("FromRegistry: %v", err)
	}
	d, err := dossier.New(dossier.Input{Scope: caseID, Result: res, Evidence: []evidencefabric.EvidenceRecord{rec}})
	if err != nil {
		t.Fatalf("dossier.New: %v", err)
	}
	outPath := filepath.Join(t.TempDir(), "package.zip")
	if err := dossier.WriteMachinePackage(d, res.Manifests, ledger, outPath); err != nil {
		t.Fatalf("WriteMachinePackage: %v", err)
	}
	return outPath
}

func TestVerifyZipAcceptsARealPackage(t *testing.T) {
	pkgPath := buildRealPackage(t, "CASE-PKGVERIFY-1")
	r, err := zip.OpenReader(pkgPath)
	if err != nil {
		t.Fatalf("opening package: %v", err)
	}
	defer r.Close()

	results, err := VerifyZip(&r.Reader, nil)
	if err != nil {
		t.Fatalf("VerifyZip: %v", err)
	}
	if !AllPassed(results) {
		t.Fatalf("expected all checks to pass (skips allowed) for a real package, got: %+v", results)
	}
	sawSkip := false
	for _, r := range results {
		if r.Status == Skip {
			sawSkip = true
		}
	}
	if !sawSkip {
		t.Fatal("expected at least one honestly-reported SKIP (signature verification)")
	}
}

func TestVerifyZipRejectsAMissingDossierEntry(t *testing.T) {
	dir := t.TempDir()
	emptyZipPath := filepath.Join(dir, "empty.zip")
	f, err := os.Create(emptyZipPath)
	if err != nil {
		t.Fatalf("creating empty zip: %v", err)
	}
	zw := zip.NewWriter(f)
	if _, err := zw.Create("unrelated.txt"); err != nil {
		t.Fatalf("adding unrelated entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("closing zip writer: %v", err)
	}
	f.Close()

	r, err := zip.OpenReader(emptyZipPath)
	if err != nil {
		t.Fatalf("opening zip: %v", err)
	}
	defer r.Close()

	if _, err := VerifyZip(&r.Reader, nil); err == nil {
		t.Fatal("expected VerifyZip to fail outright for a package with no dossier.json at all")
	}
}
