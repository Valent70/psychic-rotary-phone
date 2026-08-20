package verification

import (
	"crypto/x509"
	"testing"
)

func mustCA(t *testing.T) *IssuerCA {
	t.Helper()
	ca, err := NewIssuerCA("Veriqo Test CA")
	if err != nil {
		t.Fatalf("NewIssuerCA: %v", err)
	}
	return ca
}

func mustCert(t *testing.T, s *EvidenceSigner) SignedCertificateX509 {
	t.Helper()
	cert := IssueCertificate(BundleManifest{Subject: "test-subject", RootHash: "root-hash-content", ClaimedResult: "VALID", RecordCount: 3})
	sc, err := s.SignX509(cert)
	if err != nil {
		t.Fatalf("SignX509: %v", err)
	}
	return sc
}

// TestLeafRotation_OldSignerRejectedAfterRotation proves the standard
// "compromise or routine rotation" workflow: a fresh leaf is issued, the
// old one is revoked in the same call, and a verifier using the
// RevocationList must accept signatures from the NEW leaf while
// rejecting anything signed by the OLD one — even though the old leaf's
// certificate itself is still cryptographically valid and unexpired.
func TestLeafRotation_OldSignerRejectedAfterRotation(t *testing.T) {
	ca := mustCA(t)
	crl := NewRevocationList(ca)

	oldSigner, err := ca.IssueSigner("evidence-signer-1")
	if err != nil {
		t.Fatalf("IssueSigner: %v", err)
	}
	oldSC := mustCert(t, oldSigner)
	if err := VerifyX509WithRevocation(oldSC, ca.RootPool(), crl); err != nil {
		t.Fatalf("old signer should verify BEFORE rotation: %v", err)
	}

	newSigner, err := ca.RotateSigner(oldSigner, "evidence-signer-1", crl, "routine rotation")
	if err != nil {
		t.Fatalf("RotateSigner: %v", err)
	}

	// Old signer's PRE-rotation signature must now be rejected.
	if err := VerifyX509WithRevocation(oldSC, ca.RootPool(), crl); err == nil {
		t.Fatal("old signer's signature was accepted AFTER rotation — revocation not enforced")
	}
	// A NEW signature from the old (now-revoked) key must also be rejected.
	oldSCAfter := mustCert(t, oldSigner)
	if err := VerifyX509WithRevocation(oldSCAfter, ca.RootPool(), crl); err == nil {
		t.Fatal("a fresh signature from the revoked old signer was accepted — revocation not enforced")
	}
	// New signer must work fine.
	newSC := mustCert(t, newSigner)
	if err := VerifyX509WithRevocation(newSC, ca.RootPool(), crl); err != nil {
		t.Fatalf("new signer should verify after rotation: %v", err)
	}
}

// TestRootRotation_DualTrustWindow proves CA root rotation keeps a
// transition window where certificates issued by BOTH the old and new
// root validate — the standard real-PKI rotation pattern (browsers
// trusting an expiring root and its replacement simultaneously).
func TestRootRotation_DualTrustWindow(t *testing.T) {
	oldCA := mustCA(t)
	oldSigner, err := oldCA.IssueSigner("signer-under-old-root")
	if err != nil {
		t.Fatal(err)
	}
	oldSC := mustCert(t, oldSigner)

	rotation, err := RotateRoot(oldCA, "Veriqo Test CA v2")
	if err != nil {
		t.Fatalf("RotateRoot: %v", err)
	}
	newSigner, err := rotation.New.IssueSigner("signer-under-new-root")
	if err != nil {
		t.Fatal(err)
	}
	newSC := mustCert(t, newSigner)

	pool := rotation.TrustedPool()
	if err := VerifyX509(oldSC, pool); err != nil {
		t.Fatalf("cert issued under OLD root should still verify during transition window: %v", err)
	}
	if err := VerifyX509(newSC, pool); err != nil {
		t.Fatalf("cert issued under NEW root should verify: %v", err)
	}

	// After the transition ends, a verifier that has moved to a
	// new-root-only pool must reject the old cert.
	newOnlyPool := x509.NewCertPool()
	newOnlyPool.AddCert(rotation.New.cert)
	if err := VerifyX509(oldSC, newOnlyPool); err == nil {
		t.Fatal("cert issued under OLD root was accepted by a new-root-only pool — rotation cutover not effective")
	}
}

// TestExportCRL_IsARealParseableX509CRL proves the revocation list isn't
// just a private in-process map — it round-trips through the standard
// x509.CRL wire format any external tool can parse and check.
func TestExportCRL_IsARealParseableX509CRL(t *testing.T) {
	ca := mustCA(t)
	crl := NewRevocationList(ca)
	signer, err := ca.IssueSigner("signer-to-revoke")
	if err != nil {
		t.Fatal(err)
	}
	crl.Revoke(signer.leafCert.SerialNumber, "key compromise")

	der, err := crl.ExportCRL()
	if err != nil {
		t.Fatalf("ExportCRL: %v", err)
	}
	parsed, err := x509.ParseRevocationList(der)
	if err != nil {
		t.Fatalf("exported CRL is not valid X.509: %v", err)
	}
	if err := parsed.CheckSignatureFrom(ca.cert); err != nil {
		t.Fatalf("CRL signature does not verify against issuing CA: %v", err)
	}
	found := false
	for _, rc := range parsed.RevokedCertificateEntries {
		if rc.SerialNumber.Cmp(signer.leafCert.SerialNumber) == 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("revoked serial not present in exported CRL")
	}
}
