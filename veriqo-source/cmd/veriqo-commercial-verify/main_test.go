package main

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
		TenantID: "tenant-verify", CaseID: caseID, EvidenceID: evidenceID, Version: 1,
		URI: "evidence://verify-survey.pdf", Filename: "verify-survey.pdf", MediaType: "application/pdf",
		ByteSize: 4096, SHA256: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		Method: "UPLOAD", Collector: "surveyor-verify", Source: "independent-surveyor", AcquiredAt: tick, ReceivedAt: tick,
		HashStatus: "COMPUTED", Classification: "INTERNAL", AcquisitionRecord: "uploaded via verifier test",
	}); err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	if _, err := m.RecordCustodyEvent(evidenceID, evidenceID+"-received", "verify-test", manifest.CustodyReceived, tick, "received into custody", ""); err != nil {
		t.Fatalf("RecordCustodyEvent(RECEIVED): %v", err)
	}
	if _, err := m.Advance(evidenceID, manifest.StateIngested, tick); err != nil {
		t.Fatalf("Advance(INGESTED): %v", err)
	}
	if _, err := m.RecordCustodyEvent(evidenceID, evidenceID+"-hashed", "verify-test", manifest.CustodyHashed, tick, "hash computed", "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"); err != nil {
		t.Fatalf("RecordCustodyEvent(HASHED): %v", err)
	}
	if _, err := m.Advance(evidenceID, manifest.StateIntegrityAssessed, tick); err != nil {
		t.Fatalf("Advance(INTEGRITY_ASSESSED): %v", err)
	}
	if _, err := m.Advance(evidenceID, manifest.StateProvenanceComplete, tick); err != nil {
		t.Fatalf("Advance(PROVENANCE_COMPLETE): %v", err)
	}
	if _, err := m.RecordCustodyEvent(evidenceID, evidenceID+"-reviewed", "verify-test", manifest.CustodyReviewed, tick, "independent review complete", "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"); err != nil {
		t.Fatalf("RecordCustodyEvent(REVIEWED): %v", err)
	}
	if _, err := m.Advance(evidenceID, manifest.StateReadyForFinalization, tick); err != nil {
		t.Fatalf("Advance(READY_FOR_FINALIZATION): %v", err)
	}
	if _, err := m.Advance(evidenceID, manifest.StateFinalized, tick); err != nil {
		t.Fatalf("Advance(FINALIZED): %v", err)
	}
}

// buildRealPackage drives the real vertical slice + dossier pipeline
// and writes a genuine Machine Package .zip to dir, returning its path.
func buildRealPackage(t *testing.T, dir, caseID string) string {
	t.Helper()
	in := verticalslice.Input{
		Claim: claimworkflow.ClaimDecisionInput{
			CaseID: caseID, Tick: 10,
			Manifests: []claimworkflow.ManifestSpec{
				{
					EvidenceID: "EV-VERIFY-1", SHA256: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
					URI: "evidence://verify-survey.pdf", Filename: "verify-survey.pdf",
					MediaType: "application/pdf", ByteSize: 4096, Collector: "surveyor-verify", Source: "independent-surveyor",
				},
			},
			Hypothesis:            causation.Hypothesis{ID: "H1", Description: "water ingress during transit"},
			SupportingEvidenceIDs: []string{"EV-VERIFY-1"},
			FindingID:             "finding-verify-1",
			Finding: cre.FindingInput{
				CaseID: caseID, ContractBasis: "clause-1", ObligationRef: "obl-1",
				EventRef: "event-1", QuantumRef: "calc-1", HumanReviewRequired: true,
			},
			Outcome:     decision.OutcomeApproved,
			Rationale:   "primary hypothesis substantiated by grounded, finalized evidence",
			LedgerActor: "verify-test-decision",
		},
		Action: verticalslice.ActionInput{
			Actor: "adjuster-verify-1", PolicyRef: "policy-settlement-v1", Scope: caseID,
			PermittedAction: action.ActionApproveSettlement, Conditions: []string{"reinspection_complete"},
			AuthorizedAt: 10, ExpiresAt: 20, ExecutingActor: "adjuster-verify-1", ExecutionAt: 15,
			LedgerActor: "verify-test-action",
		},
	}
	ledger := audit.NewAuditStore()
	res, err := verticalslice.Run(in, ledger)
	if err != nil {
		t.Fatalf("verticalslice.Run: %v", err)
	}
	reg := manifest.NewRegistry()
	finalizeManifest(t, reg, "EV-VERIFY-1", caseID, 10)
	rec, err := evidencefabric.FromRegistry(reg, "EV-VERIFY-1", evidencefabric.DomainMetadata{
		Insurance: &evidencefabric.InsuranceMetadata{ClaimID: "CLM-1", PolicyID: "POL-1", EvidenceKind: "SURVEY"},
	})
	if err != nil {
		t.Fatalf("FromRegistry: %v", err)
	}
	d, err := dossier.New(dossier.Input{Scope: caseID, Result: res, Evidence: []evidencefabric.EvidenceRecord{rec}})
	if err != nil {
		t.Fatalf("dossier.New: %v", err)
	}
	outPath := filepath.Join(dir, "package.zip")
	if err := dossier.WriteMachinePackage(d, res.Manifests, ledger, outPath); err != nil {
		t.Fatalf("WriteMachinePackage: %v", err)
	}
	return outPath
}

func buildVerifierBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "veriqo-commercial-verify")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("building veriqo-commercial-verify: %v\n%s", err, out)
	}
	return bin
}

// TestVerifierAcceptsARealPackage runs the ACTUAL compiled binary, as
// a genuinely separate OS process, against a real Machine Package
// produced by the real pipeline -- the strongest possible proof that
// "independent verifier" means what it says.
func TestVerifierAcceptsARealPackage(t *testing.T) {
	bin := buildVerifierBinary(t)
	pkgPath := buildRealPackage(t, t.TempDir(), "CASE-VERIFY-1")

	cmd := exec.Command(bin, "-package", pkgPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected the verifier to exit 0 for a real, untampered package: %v\n%s", err, out)
	}
	output := string(out)
	if !strings.Contains(output, "VERDICT: ALL CHECKS PASSED") {
		t.Fatalf("expected ALL CHECKS PASSED, got:\n%s", output)
	}
	if !strings.Contains(output, "[SKIP]") {
		t.Fatalf("expected the signature check to be honestly reported as SKIP, got:\n%s", output)
	}
	if strings.Contains(output, "[FAIL]") {
		t.Fatalf("expected zero FAIL lines for a real package, got:\n%s", output)
	}
}

// TestVerifierDetectsATamperedPackageHash proves the verifier catches
// tampering: editing dossier.json inside the zip after it was written
// must be caught by the package_hash check.
func TestVerifierDetectsATamperedPackageHash(t *testing.T) {
	bin := buildVerifierBinary(t)
	dir := t.TempDir()
	pkgPath := buildRealPackage(t, dir, "CASE-VERIFY-TAMPER-1")

	tamperedPath := filepath.Join(dir, "tampered.zip")
	tamperZipEntry(t, pkgPath, tamperedPath, "dossier.json", func(content []byte) []byte {
		return bytes.Replace(content, []byte(`"case_id"`), []byte(`"CASE_ID_FORGED"`), 1)
	})

	cmd := exec.Command(bin, "-package", tamperedPath)
	out, _ := cmd.CombinedOutput()
	output := string(out)
	if !strings.Contains(output, "[FAIL]") {
		t.Fatalf("expected at least one FAIL line for a tampered package, got:\n%s", output)
	}
	if !strings.Contains(output, "VERDICT: ONE OR MORE CHECKS FAILED") {
		t.Fatalf("expected the overall verdict to be FAILED, got:\n%s", output)
	}
	if cmd.ProcessState.ExitCode() != 1 {
		t.Fatalf("expected exit code 1, got %d", cmd.ProcessState.ExitCode())
	}
}

// TestVerifierDetectsATamperedLedger proves tampering the RAW ledger
// export (not just the dossier summary) is independently caught by the
// Merkle-root / hash-chain checks.
func TestVerifierDetectsATamperedLedger(t *testing.T) {
	bin := buildVerifierBinary(t)
	dir := t.TempDir()
	pkgPath := buildRealPackage(t, dir, "CASE-VERIFY-TAMPER-LEDGER-1")

	tamperedPath := filepath.Join(dir, "tampered-ledger.zip")
	tamperZipEntry(t, pkgPath, tamperedPath, "ledger.json", func(content []byte) []byte {
		return bytes.Replace(content, []byte(`"Payload"`), []byte(`"PayloadForged"`), 1)
	})

	cmd := exec.Command(bin, "-package", tamperedPath)
	out, _ := cmd.CombinedOutput()
	output := string(out)
	if !strings.Contains(output, "ledger_hash_chain") || !strings.Contains(output, "[FAIL]") {
		t.Fatalf("expected a FAIL on ledger_hash_chain for a tampered ledger export, got:\n%s", output)
	}
}

// tamperZipEntry copies srcZip to dstZip, replacing one entry's
// content via transform.
func tamperZipEntry(t *testing.T, srcZip, dstZip, entryName string, transform func([]byte) []byte) {
	t.Helper()
	r, err := zip.OpenReader(srcZip)
	if err != nil {
		t.Fatalf("opening source zip: %v", err)
	}
	defer r.Close()

	out, err := os.Create(dstZip)
	if err != nil {
		t.Fatalf("creating dest zip: %v", err)
	}
	defer out.Close()
	zw := zip.NewWriter(out)

	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("opening entry %s: %v", f.Name, err)
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("reading entry %s: %v", f.Name, err)
		}
		if f.Name == entryName {
			content = transform(content)
		}
		w, err := zw.Create(f.Name)
		if err != nil {
			t.Fatalf("creating entry %s: %v", f.Name, err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatalf("writing entry %s: %v", f.Name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("closing tampered zip: %v", err)
	}
}
