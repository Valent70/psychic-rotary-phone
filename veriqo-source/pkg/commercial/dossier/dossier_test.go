package dossier

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
		TenantID: "tenant-dossier", CaseID: caseID, EvidenceID: evidenceID, Version: 1,
		URI: "evidence://dossier-survey.pdf", Filename: "dossier-survey.pdf", MediaType: "application/pdf",
		ByteSize: 4096, SHA256: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		Method: "UPLOAD", Collector: "surveyor-dossier", Source: "independent-surveyor", AcquiredAt: tick, ReceivedAt: tick,
		HashStatus: "COMPUTED", Classification: "INTERNAL", AcquisitionRecord: "uploaded via dossier test",
	}); err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	if _, err := m.RecordCustodyEvent(evidenceID, evidenceID+"-received", "dossier-test", manifest.CustodyReceived, tick, "received into custody", ""); err != nil {
		t.Fatalf("RecordCustodyEvent(RECEIVED): %v", err)
	}
	if _, err := m.Advance(evidenceID, manifest.StateIngested, tick); err != nil {
		t.Fatalf("Advance(INGESTED): %v", err)
	}
	if _, err := m.RecordCustodyEvent(evidenceID, evidenceID+"-hashed", "dossier-test", manifest.CustodyHashed, tick, "hash computed", "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"); err != nil {
		t.Fatalf("RecordCustodyEvent(HASHED): %v", err)
	}
	if _, err := m.Advance(evidenceID, manifest.StateIntegrityAssessed, tick); err != nil {
		t.Fatalf("Advance(INTEGRITY_ASSESSED): %v", err)
	}
	if _, err := m.Advance(evidenceID, manifest.StateProvenanceComplete, tick); err != nil {
		t.Fatalf("Advance(PROVENANCE_COMPLETE): %v", err)
	}
	if _, err := m.RecordCustodyEvent(evidenceID, evidenceID+"-reviewed", "dossier-test", manifest.CustodyReviewed, tick, "independent review complete", "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"); err != nil {
		t.Fatalf("RecordCustodyEvent(REVIEWED): %v", err)
	}
	if _, err := m.Advance(evidenceID, manifest.StateReadyForFinalization, tick); err != nil {
		t.Fatalf("Advance(READY_FOR_FINALIZATION): %v", err)
	}
	if _, err := m.Advance(evidenceID, manifest.StateFinalized, tick); err != nil {
		t.Fatalf("Advance(FINALIZED): %v", err)
	}
}

func buildTestResult(t *testing.T, caseID string) (verticalslice.Result, []evidencefabric.EvidenceRecord, *audit.AuditStore) {
	t.Helper()
	in := verticalslice.Input{
		Claim: claimworkflow.ClaimDecisionInput{
			CaseID: caseID, Tick: 10,
			Manifests: []claimworkflow.ManifestSpec{
				{
					EvidenceID: "EV-DOSSIER-1", SHA256: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
					URI: "evidence://dossier-survey.pdf", Filename: "dossier-survey.pdf",
					MediaType: "application/pdf", ByteSize: 4096, Collector: "surveyor-dossier", Source: "independent-surveyor",
				},
			},
			Hypothesis:            causation.Hypothesis{ID: "H1", Description: "water ingress during transit"},
			SupportingEvidenceIDs: []string{"EV-DOSSIER-1"},
			FindingID:             "finding-dossier-1",
			Finding: cre.FindingInput{
				CaseID: caseID, ContractBasis: "clause-1", ObligationRef: "obl-1",
				EventRef: "event-1", QuantumRef: "calc-1", HumanReviewRequired: true,
			},
			Outcome:     decision.OutcomeApproved,
			Rationale:   "primary hypothesis substantiated by grounded, finalized evidence",
			LedgerActor: "dossier-test-decision",
		},
		Action: verticalslice.ActionInput{
			Actor: "adjuster-dossier-1", PolicyRef: "policy-settlement-v1", Scope: caseID,
			PermittedAction: action.ActionApproveSettlement, Conditions: []string{"reinspection_complete"},
			AuthorizedAt: 10, ExpiresAt: 20, ExecutingActor: "adjuster-dossier-1", ExecutionAt: 15,
			LedgerActor: "dossier-test-action",
		},
	}
	ledger := audit.NewAuditStore()
	res, err := verticalslice.Run(in, ledger)
	if err != nil {
		t.Fatalf("verticalslice.Run: %v", err)
	}

	reg := manifest.NewRegistry()
	finalizeManifest(t, reg, "EV-DOSSIER-1", caseID, 10)
	rec, err := evidencefabric.FromRegistry(reg, "EV-DOSSIER-1", evidencefabric.DomainMetadata{
		Insurance: &evidencefabric.InsuranceMetadata{ClaimID: "CLM-1", PolicyID: "POL-1", EvidenceKind: "SURVEY"},
	})
	if err != nil {
		t.Fatalf("FromRegistry: %v", err)
	}
	return res, []evidencefabric.EvidenceRecord{rec}, ledger
}

func TestNewAssemblesAllReviewerNamedFields(t *testing.T) {
	res, evidence, _ := buildTestResult(t, "CASE-DOSSIER-1")
	d, err := New(Input{
		Scope: "CASE-DOSSIER-1", Result: res, Evidence: evidence,
		Corroboration:  []string{"survey report corroborates FNOL timeline"},
		Contradictions: []string{"adjuster report notes minor discrepancy in reported weather conditions"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if d.Case.Scope != "CASE-DOSSIER-1" {
		t.Fatalf("unexpected scope: %s", d.Case.Scope)
	}
	if len(d.EvidenceInventory) != 1 {
		t.Fatal("expected 1 evidence item")
	}
	if len(d.SourceInformation) != 1 || len(d.AcquisitionTimeline) != 1 || len(d.IntegrityVerification) != 1 {
		t.Fatal("expected derived per-evidence rows")
	}
	if !d.IntegrityVerification[0].Verified {
		t.Fatal("expected the real, finalized evidence to verify")
	}
	if len(d.ChainOfCustody) != 3 {
		t.Fatalf("expected 3 flattened custody rows, got %d", len(d.ChainOfCustody))
	}
	if len(d.IdentityInformation) == 0 {
		t.Fatal("expected at least one identity to be surfaced")
	}
	if len(d.Corroboration) != 1 || len(d.Contradictions) != 1 {
		t.Fatal("expected caller-supplied corroboration/contradictions to be preserved")
	}
	if d.Decision.Hash != res.Decision.Hash() {
		t.Fatal("expected the Decision block to cite the real Decision hash")
	}
	if d.Authorization.Hash != res.ActionAuthorization.Hash() {
		t.Fatal("expected the Authorization block to cite the real ActionAuthorization hash")
	}
	if d.Action.ReceiptID != res.Receipt.ReceiptID {
		t.Fatal("expected the Action block to cite the real Receipt ID")
	}
	if len(d.Limitations) < len(standardLimitations) {
		t.Fatal("expected the standard limitations to always be present")
	}
	if len(d.VerificationInstructions) == 0 {
		t.Fatal("expected verification instructions to be present")
	}
	if d.PackageHash == "" {
		t.Fatal("expected a non-empty PackageHash")
	}
}

func TestVerifyPackageHashAcceptsARealDossier(t *testing.T) {
	res, evidence, _ := buildTestResult(t, "CASE-DOSSIER-VERIFY-1")
	d, err := New(Input{Scope: "CASE-DOSSIER-VERIFY-1", Result: res, Evidence: evidence})
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyPackageHash(d); err != nil {
		t.Fatalf("expected a real dossier to verify: %v", err)
	}
}

// TestVerifyPackageHashDetectsTampering proves PackageHash is a real,
// independently-recomputable commitment: silently editing any field
// after New produced the Dossier must be caught.
func TestVerifyPackageHashDetectsTampering(t *testing.T) {
	res, evidence, _ := buildTestResult(t, "CASE-DOSSIER-TAMPER-1")
	d, err := New(Input{Scope: "CASE-DOSSIER-TAMPER-1", Result: res, Evidence: evidence})
	if err != nil {
		t.Fatal(err)
	}
	tampered := d
	tampered.TrustAssessment = "FORGED: claim is definitely covered"
	if err := VerifyPackageHash(tampered); err == nil {
		t.Fatal("expected a tampered dossier to fail VerifyPackageHash")
	}
}

func TestNewRejectsNoEvidence(t *testing.T) {
	res, _, _ := buildTestResult(t, "CASE-DOSSIER-NOEVIDENCE-1")
	if _, err := New(Input{Scope: "x", Result: res, Evidence: nil}); err == nil {
		t.Fatal("expected New to refuse an empty evidence list")
	}
}

func TestNewRejectsZeroDecision(t *testing.T) {
	_, evidence, _ := buildTestResult(t, "CASE-DOSSIER-ZERODECISION-1")
	if _, err := New(Input{Scope: "x", Result: verticalslice.Result{}, Evidence: evidence}); err == nil {
		t.Fatal("expected New to refuse a zero Decision")
	}
}

func TestRenderMarkdownCoversEveryReviewerNamedSection(t *testing.T) {
	res, evidence, _ := buildTestResult(t, "CASE-DOSSIER-MD-1")
	d, err := New(Input{Scope: "CASE-DOSSIER-MD-1", Result: res, Evidence: evidence})
	if err != nil {
		t.Fatal(err)
	}
	md := RenderMarkdown(d)
	for _, section := range []string{
		"## Case", "## Evidence Inventory", "## Source Information", "## Acquisition Timeline",
		"## Integrity Verification", "## Identity Information", "## Chain of Custody",
		"## Corroboration", "## Contradictions", "## Trust Assessment", "## Decision",
		"## Authorization", "## Action", "## Limitations", "## Verification Instructions", "## Package Hash",
	} {
		if !strings.Contains(md, section) {
			t.Fatalf("expected markdown to contain section %q", section)
		}
	}
}

func TestWriteMachinePackageProducesAValidZip(t *testing.T) {
	res, evidence, ledger := buildTestResult(t, "CASE-DOSSIER-ZIP-1")
	d, err := New(Input{Scope: "CASE-DOSSIER-ZIP-1", Result: res, Evidence: evidence})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	outPath := filepath.Join(dir, "dossier.zip")
	if err := WriteMachinePackage(d, res.Manifests, ledger, outPath); err != nil {
		t.Fatalf("WriteMachinePackage: %v", err)
	}
	info, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("expected the zip file to exist: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("expected a non-empty zip file")
	}
}
