package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	commercialapi "veriqo/pkg/commercial/api"
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
	"veriqo/pkg/platform/security/keys"
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

// buildSignedRealPackage is buildRealPackage's signed counterpart: a
// real, working keys.Manager backed by keys.MemoryKeyProvider signs
// everything via the actual Commercial API Store, and this returns
// both the package path and a real trusted-keys.json file (the exact
// shape -trusted-keys expects) containing that key's real public key.
func buildSignedRealPackage(t *testing.T, dir, caseID string) (pkgPath, trustedKeysPath string) {
	t.Helper()
	provider := keys.NewMemoryKeyProvider()
	md, err := provider.Generate("verify-cli-key-1", keys.PurposeEvidence, 0, 0)
	if err != nil {
		t.Fatalf("provider.Generate: %v", err)
	}
	mgr := keys.NewManager(provider)
	if err := mgr.Register(md); err != nil {
		t.Fatalf("mgr.Register: %v", err)
	}
	if err := mgr.Activate(md.KeyID); err != nil {
		t.Fatalf("mgr.Activate: %v", err)
	}

	s := commercialapi.NewStore()
	if err := s.EnableSigning(mgr); err != nil {
		t.Fatalf("EnableSigning: %v", err)
	}
	const tenant = "tenant-verify-cli"
	if err := s.CreateCase(tenant, caseID, 0); err != nil {
		t.Fatalf("CreateCase: %v", err)
	}
	if _, err := s.SubmitEvidence(commercialapi.EvidenceInput{
		TenantID: tenant, CaseID: caseID, EvidenceID: "EV-API-1",
		SHA256: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		URI:    "evidence://verify-cli-survey.pdf", Filename: "verify-cli-survey.pdf",
		MediaType: "application/pdf", ByteSize: 4096, Collector: "surveyor-verify-cli", Source: "independent-surveyor",
		Domain: evidencefabric.DomainMetadata{Insurance: &evidencefabric.InsuranceMetadata{ClaimID: "CLM-1", PolicyID: "POL-1", EvidenceKind: "SURVEY"}},
		Tick:   10,
	}); err != nil {
		t.Fatalf("SubmitEvidence: %v", err)
	}
	if _, err := s.DecideCase(commercialapi.DecideInput{
		TenantID: tenant, CaseID: caseID,
		Hypothesis:            causation.Hypothesis{ID: "H1", Description: "water ingress during transit"},
		SupportingEvidenceIDs: []string{"EV-API-1"},
		FindingID:             "finding-verify-cli-1",
		Finding: cre.FindingInput{
			CaseID: caseID, ContractBasis: "clause-1", ObligationRef: "obl-1",
			EventRef: "event-1", QuantumRef: "calc-1", HumanReviewRequired: true,
		},
		Outcome: decision.OutcomeApproved, Rationale: "grounded, finalized evidence", LedgerActor: "verify-cli-decision", Tick: 10,
	}); err != nil {
		t.Fatalf("DecideCase: %v", err)
	}
	if _, _, err := s.ActOnCase(commercialapi.ActionInput{
		TenantID: tenant, CaseID: caseID, Actor: "adjuster-verify-cli-1", PolicyRef: "policy-settlement-v1", Scope: caseID,
		PermittedAction: action.ActionApproveSettlement, Conditions: []string{"reinspection_complete"},
		AuthorizedAt: 10, ExpiresAt: 20, ExecutingActor: "adjuster-verify-cli-1", ExecutionAt: 15,
		LedgerActor: "verify-cli-action",
	}); err != nil {
		t.Fatalf("ActOnCase: %v", err)
	}

	pkgPath = filepath.Join(dir, "signed-package.zip")
	if _, err := s.WriteDossierPackage(tenant, caseID, pkgPath); err != nil {
		t.Fatalf("WriteDossierPackage: %v", err)
	}

	registry := map[string]map[string]any{
		md.KeyID: {"public_key": md.PublicKey, "revoked": false},
	}
	data, err := json.Marshal(registry)
	if err != nil {
		t.Fatalf("marshaling trusted-keys registry: %v", err)
	}
	trustedKeysPath = filepath.Join(dir, "trusted-keys.json")
	if err := os.WriteFile(trustedKeysPath, data, 0o600); err != nil {
		t.Fatalf("writing trusted-keys.json: %v", err)
	}
	return pkgPath, trustedKeysPath
}

// TestVerifierWithTrustedKeysVerifiesRealSignatures proves the
// standalone binary's own -trusted-keys flag works end to end: a real
// signed package, a real trusted-keys.json file, run through the
// actual compiled binary as a separate process -- signature checks
// must PASS, not SKIP, when a trusted registry is supplied.
func TestVerifierWithTrustedKeysVerifiesRealSignatures(t *testing.T) {
	bin := buildVerifierBinary(t)
	dir := t.TempDir()
	pkgPath, trustedKeysPath := buildSignedRealPackage(t, dir, "CASE-VERIFY-CLI-SIGNED-1")

	cmd := exec.Command(bin, "-package", pkgPath, "-trusted-keys", trustedKeysPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected the verifier to exit 0 for a real, correctly signed package with its real trusted keys: %v\n%s", err, out)
	}
	output := string(out)
	if !strings.Contains(output, "VERDICT: ALL CHECKS PASSED") {
		t.Fatalf("expected ALL CHECKS PASSED, got:\n%s", output)
	}
	if !strings.Contains(output, "[PASS] package_signature") {
		t.Fatalf("expected a real PASS on package_signature with a trusted registry supplied, got:\n%s", output)
	}
	if !strings.Contains(output, "[PASS] key_state") {
		t.Fatalf("expected a real PASS on key_state with a trusted registry supplied, got:\n%s", output)
	}
}

// TestVerifierWithoutTrustedKeysSkipsSignaturesHonestly proves the
// same signed package, run WITHOUT -trusted-keys, honestly reports
// SKIP on signature/key-state -- never a false PASS just because the
// package happens to carry a signature.
func TestVerifierWithoutTrustedKeysSkipsSignaturesHonestly(t *testing.T) {
	bin := buildVerifierBinary(t)
	dir := t.TempDir()
	pkgPath, _ := buildSignedRealPackage(t, dir, "CASE-VERIFY-CLI-SIGNED-2")

	cmd := exec.Command(bin, "-package", pkgPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0 (Skip is not a failure): %v\n%s", err, out)
	}
	output := string(out)
	if !strings.Contains(output, "[SKIP] package_signature") {
		t.Fatalf("expected an honest SKIP on package_signature with no -trusted-keys supplied, got:\n%s", output)
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
