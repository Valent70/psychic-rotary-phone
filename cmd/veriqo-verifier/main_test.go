package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"veriqo/pkg/verification"
)

func writeJSON(t *testing.T, dir, name string, v any) string {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", name, err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func realBundleAndCert(t *testing.T) (verification.VerificationBundle, verification.SigningKey, verification.SignedCertificate) {
	t.Helper()
	rp := verification.ReplayPackage{
		Evidence: verification.EvidencePackage{
			Subject: "test-subject",
			Records: []verification.EvidenceRecord{
				{Kind: "test.Record", Index: 0, Payload: []byte(`{"a":1}`)},
				{Kind: "test.Record", Index: 1, Payload: []byte(`{"b":2}`)},
			},
		},
		ClaimedResult: "PASS",
	}
	bundle, err := verification.NewBundle(rp)
	if err != nil {
		t.Fatalf("NewBundle: %v", err)
	}
	key, err := verification.GenerateSigningKey()
	if err != nil {
		t.Fatalf("GenerateSigningKey: %v", err)
	}
	auditCert := verification.IssueCertificate(bundle.Manifest)
	signed := key.Sign(auditCert)
	return bundle, key, signed
}

// TestVerifyValid proves the genuinely-honest-path outcome: a real
// signed certificate, whose signing key is ACTIVE in the trust
// registry, whose bundle independently recomputes the same root, must
// be reported VALID.
func TestVerifyValid(t *testing.T) {
	dir := t.TempDir()
	bundle, _, signed := realBundleAndCert(t)

	registry := verification.NewTrustRegistry()
	registry.Trust(signed.PublicKey, "test-key")

	certPath := writeJSON(t, dir, "cert.json", signed)
	registryPath := writeJSON(t, dir, "registry.json", registry)
	bundlePath := writeJSON(t, dir, "bundle.json", bundle)

	report, outcome := verify(certPath, registryPath, bundlePath, "", "")
	if outcome != OutcomeValid {
		t.Fatalf("got %s, want VALID (reasons=%v)", outcome, report.Reasons)
	}
	if report.BundleRootMatch == nil || !*report.BundleRootMatch {
		t.Fatalf("expected BundleRootMatch=true, got %+v", report.BundleRootMatch)
	}
}

// TestVerifyTrustRevoked proves revocation takes priority: even a
// cryptographically perfect signature from a REVOKED key must never
// pass.
func TestVerifyTrustRevoked(t *testing.T) {
	dir := t.TempDir()
	_, _, signed := realBundleAndCert(t)

	registry := verification.NewTrustRegistry()
	registry.Revoke(signed.PublicKey, "compromised-key")

	certPath := writeJSON(t, dir, "cert.json", signed)
	registryPath := writeJSON(t, dir, "registry.json", registry)

	_, outcome := verify(certPath, registryPath, "", "", "")
	if outcome != OutcomeTrustRevoked {
		t.Fatalf("got %s, want TRUST_REVOKED", outcome)
	}
}

// TestVerifyInvalidUntrustedKey proves an unregistered (never trusted,
// never revoked) key's signature is rejected too — a valid signature
// alone is not sufficient; the key must be affirmatively trusted.
func TestVerifyInvalidUntrustedKey(t *testing.T) {
	dir := t.TempDir()
	_, _, signed := realBundleAndCert(t)

	registry := verification.NewTrustRegistry() // empty -- key never registered

	certPath := writeJSON(t, dir, "cert.json", signed)
	registryPath := writeJSON(t, dir, "registry.json", registry)

	_, outcome := verify(certPath, registryPath, "", "", "")
	if outcome != OutcomeInvalid {
		t.Fatalf("got %s, want INVALID", outcome)
	}
}

// TestVerifyInvalidTamperedCertificate proves a certificate whose
// fields were altered after signing is rejected.
func TestVerifyInvalidTamperedCertificate(t *testing.T) {
	dir := t.TempDir()
	_, _, signed := realBundleAndCert(t)
	signed.Certificate.ClaimedResult = "TAMPERED-RESULT" // mutate after signing

	registry := verification.NewTrustRegistry()
	registry.Trust(signed.PublicKey, "test-key")

	certPath := writeJSON(t, dir, "cert.json", signed)
	registryPath := writeJSON(t, dir, "registry.json", registry)

	_, outcome := verify(certPath, registryPath, "", "", "")
	if outcome != OutcomeInvalid {
		t.Fatalf("got %s, want INVALID", outcome)
	}
}

// TestVerifyInvalidBundleRootMismatch proves a bundle that does NOT
// independently recompute to the certificate's claimed root is
// rejected — the certificate alone being internally consistent is not
// enough; it must actually correspond to the supplied evidence.
func TestVerifyInvalidBundleRootMismatch(t *testing.T) {
	dir := t.TempDir()
	_, _, signed := realBundleAndCert(t)

	// A DIFFERENT bundle (different evidence) than what was actually signed.
	otherRP := verification.ReplayPackage{
		Evidence:      verification.EvidencePackage{Subject: "different-subject", Records: []verification.EvidenceRecord{{Kind: "x", Index: 0, Payload: []byte(`{}`)}}},
		ClaimedResult: "PASS",
	}
	otherBundle, err := verification.NewBundle(otherRP)
	if err != nil {
		t.Fatalf("NewBundle: %v", err)
	}

	registry := verification.NewTrustRegistry()
	registry.Trust(signed.PublicKey, "test-key")

	certPath := writeJSON(t, dir, "cert.json", signed)
	registryPath := writeJSON(t, dir, "registry.json", registry)
	bundlePath := writeJSON(t, dir, "bundle.json", otherBundle)

	report, outcome := verify(certPath, registryPath, bundlePath, "", "")
	if outcome != OutcomeInvalid {
		t.Fatalf("got %s, want INVALID", outcome)
	}
	if report.BundleRootMatch == nil || *report.BundleRootMatch {
		t.Fatalf("expected BundleRootMatch=false, got %+v", report.BundleRootMatch)
	}
}

// TestVerifyIncompleteMissingFiles proves missing required inputs are
// reported INCOMPLETE, distinctly from INVALID.
func TestVerifyIncompleteMissingFiles(t *testing.T) {
	dir := t.TempDir()
	_, outcome := verify(filepath.Join(dir, "does-not-exist.json"), filepath.Join(dir, "also-missing.json"), "", "", "")
	if outcome != OutcomeIncomplete {
		t.Fatalf("got %s, want INCOMPLETE", outcome)
	}
}

// TestVerifyArtifactHashes proves the artifact-hash cross-check works
// both ways: a matching real file passes, a tampered one fails.
func TestVerifyArtifactHashes(t *testing.T) {
	dir := t.TempDir()
	_, _, signed := realBundleAndCert(t)

	registry := verification.NewTrustRegistry()
	registry.Trust(signed.PublicKey, "test-key")

	artifactPath := filepath.Join(dir, "release-binary")
	content := []byte("this is the real, unmodified release artifact")
	if err := os.WriteFile(artifactPath, content, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := shaHex(content)

	certPath := writeJSON(t, dir, "cert.json", signed)
	registryPath := writeJSON(t, dir, "registry.json", registry)
	artifactsPath := writeJSON(t, dir, "artifacts.json", map[string]artifactSpec{
		"release-binary": {Path: artifactPath, ExpectedSHA256: sum},
	})

	report, outcome := verify(certPath, registryPath, "", "", artifactsPath)
	if outcome != OutcomeValid {
		t.Fatalf("got %s, want VALID for matching artifact (reasons=%v)", outcome, report.Reasons)
	}
	if report.ArtifactResults["release-binary"] != "match" {
		t.Fatalf("expected match, got %v", report.ArtifactResults)
	}

	// Now tamper with the artifact on disk after the hash was declared.
	if err := os.WriteFile(artifactPath, []byte("TAMPERED CONTENT"), 0o644); err != nil {
		t.Fatal(err)
	}
	report2, outcome2 := verify(certPath, registryPath, "", "", artifactsPath)
	if outcome2 != OutcomeInvalid {
		t.Fatalf("got %s, want INVALID for tampered artifact", outcome2)
	}
	if report2.ArtifactResults["release-binary"] != "mismatch" {
		t.Fatalf("expected mismatch, got %v", report2.ArtifactResults)
	}
}

func shaHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
