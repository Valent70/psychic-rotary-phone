package packageverify

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	commercialapi "veriqo/pkg/commercial/api"
	"veriqo/pkg/commercial/dossier"
	"veriqo/pkg/commercial/evidencefabric"
	"veriqo/pkg/insurance/action"
	"veriqo/pkg/insurance/causation"
	"veriqo/pkg/insurance/cre"
	"veriqo/pkg/insurance/decision"
	"veriqo/pkg/platform/security/keys"
)

// This file proves Independent Verifier v2 (Commercialization Sprint
// P0-E): real signature verification against a caller-supplied trusted
// key registry, honest SKIP with no registry, FAIL on a revoked key or
// a tampered signature, and the new lineage cross-reference checks --
// closing the prior round's own named gap ("verifier sekarang secara
// jujur SKIP signature verification ... dan bukan SKIP signature
// lagi").

// buildSignedPackage builds a real, signed Machine Package through the
// actual Commercial API Store (not a hand-assembled fixture), and
// returns the package path plus the real trusted-key registry entry an
// independent verifier would be given out-of-band.
func buildSignedPackage(t *testing.T) (pkgPath string, keyID string, trustedKeys TrustedKeyRegistry) {
	t.Helper()
	provider := keys.NewMemoryKeyProvider()
	md, err := provider.Generate("pkgverify-signing-key-1", keys.PurposeEvidence, 0, 0)
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

	const tenant, caseID = "tenant-pkgverify-signing", "CASE-PKGVERIFY-SIGNING-1"
	if err := s.CreateCase(tenant, caseID, 0); err != nil {
		t.Fatalf("CreateCase: %v", err)
	}
	if _, err := s.SubmitEvidence(commercialapi.EvidenceInput{
		TenantID: tenant, CaseID: caseID, EvidenceID: "EV-API-1",
		SHA256: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		URI:    "evidence://pkgverify-signing-survey.pdf", Filename: "pkgverify-signing-survey.pdf",
		MediaType: "application/pdf", ByteSize: 4096, Collector: "surveyor-pkgverify-signing", Source: "independent-surveyor",
		Domain: evidencefabric.DomainMetadata{Insurance: &evidencefabric.InsuranceMetadata{ClaimID: "CLM-1", PolicyID: "POL-1", EvidenceKind: "SURVEY"}},
		Tick:   10,
	}); err != nil {
		t.Fatalf("SubmitEvidence: %v", err)
	}
	if _, err := s.DecideCase(commercialapi.DecideInput{
		TenantID: tenant, CaseID: caseID,
		Hypothesis:            causation.Hypothesis{ID: "H1", Description: "water ingress during transit"},
		SupportingEvidenceIDs: []string{"EV-API-1"},
		FindingID:             "finding-pkgverify-signing-1",
		Finding: cre.FindingInput{
			CaseID: caseID, ContractBasis: "clause-1", ObligationRef: "obl-1",
			EventRef: "event-1", QuantumRef: "calc-1", HumanReviewRequired: true,
		},
		Outcome: decision.OutcomeApproved, Rationale: "grounded, finalized evidence", LedgerActor: "pkgverify-signing-decision", Tick: 10,
	}); err != nil {
		t.Fatalf("DecideCase: %v", err)
	}
	if _, _, err := s.ActOnCase(commercialapi.ActionInput{
		TenantID: tenant, CaseID: caseID, Actor: "adjuster-pkgverify-signing-1", PolicyRef: "policy-settlement-v1", Scope: caseID,
		PermittedAction: action.ActionApproveSettlement, Conditions: []string{"reinspection_complete"},
		AuthorizedAt: 10, ExpiresAt: 20, ExecutingActor: "adjuster-pkgverify-signing-1", ExecutionAt: 15,
		LedgerActor: "pkgverify-signing-action",
	}); err != nil {
		t.Fatalf("ActOnCase: %v", err)
	}

	outPath := filepath.Join(t.TempDir(), "signed-package.zip")
	if _, err := s.WriteDossierPackage(tenant, caseID, outPath); err != nil {
		t.Fatalf("WriteDossierPackage: %v", err)
	}
	return outPath, md.KeyID, TrustedKeyRegistry{
		md.KeyID: {PublicKey: md.PublicKey, Revoked: false},
	}
}

func openZip(t *testing.T, path string) *zip.ReadCloser {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("opening package: %v", err)
	}
	return r
}

// rewriteDossierJSON copies every entry from srcPath into a new zip at
// destPath, except dossier.json, which is replaced with mutate's
// result applied to the real, parsed Dossier -- a type-safe way to
// tamper one field without hand-editing JSON bytes.
func rewriteDossierJSON(t *testing.T, srcPath, destPath string, mutate func(d *dossier.Dossier)) {
	t.Helper()
	r := openZip(t, srcPath)
	defer r.Close()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("opening %s: %v", f.Name, err)
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("reading %s: %v", f.Name, err)
		}
		if f.Name == "dossier.json" {
			var d dossier.Dossier
			if err := json.Unmarshal(content, &d); err != nil {
				t.Fatalf("parsing dossier.json: %v", err)
			}
			mutate(&d)
			newContent, err := json.MarshalIndent(d, "", "  ")
			if err != nil {
				t.Fatalf("re-marshaling dossier.json: %v", err)
			}
			content = newContent
		}
		w, err := zw.Create(f.Name)
		if err != nil {
			t.Fatalf("creating %s in tampered zip: %v", f.Name, err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatalf("writing %s in tampered zip: %v", f.Name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("closing tampered zip writer: %v", err)
	}
	if err := os.WriteFile(destPath, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("writing tampered package: %v", err)
	}
}

func TestVerifyZipRealSignatureVerifiesAgainstTrustedRegistry(t *testing.T) {
	pkgPath, keyID, trustedKeys := buildSignedPackage(t)
	r := openZip(t, pkgPath)
	defer r.Close()

	results, err := VerifyZip(&r.Reader, trustedKeys)
	if err != nil {
		t.Fatalf("VerifyZip: %v", err)
	}
	if !AllPassed(results) {
		t.Fatalf("expected all checks to pass for a real, correctly signed package with a valid trusted registry, got: %+v", results)
	}
	sawSignaturePass := false
	sawKeyStatePass := false
	for _, res := range results {
		if res.Name == "package_signature" && res.Status == Pass {
			sawSignaturePass = true
		}
		if res.Name == "key_state["+keyID+"]" && res.Status == Pass {
			sawKeyStatePass = true
		}
	}
	if !sawSignaturePass {
		t.Fatalf("expected a real PASS on package_signature, got: %+v", results)
	}
	if !sawKeyStatePass {
		t.Fatalf("expected a real PASS on key_state, got: %+v", results)
	}
}

func TestVerifyZipSkipsSignatureHonestlyWithNoTrustedRegistry(t *testing.T) {
	pkgPath, _, _ := buildSignedPackage(t)
	r := openZip(t, pkgPath)
	defer r.Close()

	results, err := VerifyZip(&r.Reader, nil)
	if err != nil {
		t.Fatalf("VerifyZip: %v", err)
	}
	if !AllPassed(results) {
		t.Fatalf("expected Skip (not Fail) with no trusted registry supplied, got: %+v", results)
	}
	sawSkip := false
	for _, res := range results {
		if res.Name == "package_signature" && res.Status == Skip {
			sawSkip = true
		}
	}
	if !sawSkip {
		t.Fatal("expected package_signature to be SKIP, not silently PASS, when no trusted key registry is supplied")
	}
}

func TestVerifyZipFailsOnARevokedKey(t *testing.T) {
	pkgPath, keyID, trustedKeys := buildSignedPackage(t)
	trustedKeys[keyID] = TrustedKey{PublicKey: trustedKeys[keyID].PublicKey, Revoked: true}
	r := openZip(t, pkgPath)
	defer r.Close()

	results, err := VerifyZip(&r.Reader, trustedKeys)
	if err != nil {
		t.Fatalf("VerifyZip: %v", err)
	}
	if AllPassed(results) {
		t.Fatalf("expected a revoked key to fail verification, got: %+v", results)
	}
	sawFail := false
	for _, res := range results {
		if res.Name == "key_state["+keyID+"]" && res.Status == Fail {
			sawFail = true
		}
	}
	if !sawFail {
		t.Fatalf("expected key_state to FAIL for a revoked key, got: %+v", results)
	}
}

func TestVerifyZipDetectsATamperedSignature(t *testing.T) {
	pkgPath, _, trustedKeys := buildSignedPackage(t)
	tamperedPath := filepath.Join(t.TempDir(), "tampered-signature.zip")

	rewriteDossierJSON(t, pkgPath, tamperedPath, func(d *dossier.Dossier) {
		if d.PackageSignature == nil || len(d.PackageSignature.Signature) == 0 {
			t.Fatal("expected a real PackageSignature to tamper")
		}
		b := []byte(d.PackageSignature.Signature)
		// Flip one hex character -- a genuine single-bit-class tamper on
		// the signature bytes, independent of PackageHash (unaffected:
		// PackageSignature is excluded from PackageHash's own input).
		if b[0] == '0' {
			b[0] = '1'
		} else {
			b[0] = '0'
		}
		d.PackageSignature.Signature = string(b)
	})

	r := openZip(t, tamperedPath)
	defer r.Close()
	results, err := VerifyZip(&r.Reader, trustedKeys)
	if err != nil {
		t.Fatalf("VerifyZip: %v", err)
	}
	if AllPassed(results) {
		t.Fatalf("expected a tampered signature to be detected, got: %+v", results)
	}
	sawFail := false
	for _, res := range results {
		if res.Name == "package_signature" && res.Status == Fail {
			sawFail = true
		}
	}
	if !sawFail {
		t.Fatalf("expected package_signature to FAIL for a tampered signature, got: %+v", results)
	}
	// PackageHash itself must still verify -- proving this is a real,
	// independent signature check, not a re-run of the hash check.
	for _, res := range results {
		if res.Name == "package_hash" && res.Status != Pass {
			t.Fatalf("expected package_hash to remain PASS (PackageSignature is excluded from it), got: %+v", res)
		}
	}
}

func TestVerifyZipDetectsAForgedLineage(t *testing.T) {
	pkgPath, _, trustedKeys := buildSignedPackage(t)
	tamperedPath := filepath.Join(t.TempDir(), "forged-lineage.zip")

	rewriteDossierJSON(t, pkgPath, tamperedPath, func(d *dossier.Dossier) {
		if d.Decision.Hash == "" {
			t.Fatal("expected a real Decision.Hash to tamper")
		}
		// A plausible-looking but entirely fabricated hash: the ledger
		// (untouched) will never contain this string.
		d.Decision.Hash = "0000000000000000000000000000000000000000000000000000000000000000forged"
	})

	r := openZip(t, tamperedPath)
	defer r.Close()
	results, err := VerifyZip(&r.Reader, trustedKeys)
	if err != nil {
		t.Fatalf("VerifyZip: %v", err)
	}
	if AllPassed(results) {
		t.Fatalf("expected a forged Decision.Hash to fail lineage verification, got: %+v", results)
	}
	sawLineageFail := false
	for _, res := range results {
		if res.Name == "lineage_decision" && res.Status == Fail {
			sawLineageFail = true
		}
	}
	if !sawLineageFail {
		t.Fatalf("expected lineage_decision to FAIL when dossier.json's Decision.Hash does not appear in the real, untouched ledger, got: %+v", results)
	}
}
