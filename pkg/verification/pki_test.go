package verification

import (
	"crypto/x509"
	"testing"
)

func sampleCertificate() AuditCertificate {
	return IssueCertificate(BundleManifest{
		Subject:       "dark-vessel-scenario-1",
		RootHash:      "deadbeef",
		ClaimedResult: "PASSED",
		RecordCount:   3,
	})
}

func TestX509_SignAndVerify_Success(t *testing.T) {
	ca, err := NewIssuerCA("Veriqo Test Root CA")
	if err != nil {
		t.Fatalf("NewIssuerCA: %v", err)
	}
	signer, err := ca.IssueSigner("veriqo-verify-node-1")
	if err != nil {
		t.Fatalf("IssueSigner: %v", err)
	}
	cert := sampleCertificate()
	sc, err := signer.SignX509(cert)
	if err != nil {
		t.Fatalf("SignX509: %v", err)
	}
	if err := VerifyX509(sc, ca.RootPool()); err != nil {
		t.Fatalf("VerifyX509 should succeed against the issuing CA's root pool: %v", err)
	}
}

func TestX509_Verify_RejectsUntrustedRoot(t *testing.T) {
	caA, err := NewIssuerCA("Veriqo Root A")
	if err != nil {
		t.Fatalf("NewIssuerCA A: %v", err)
	}
	caB, err := NewIssuerCA("Veriqo Root B")
	if err != nil {
		t.Fatalf("NewIssuerCA B: %v", err)
	}
	signer, err := caA.IssueSigner("signer-under-A")
	if err != nil {
		t.Fatalf("IssueSigner: %v", err)
	}
	cert := sampleCertificate()
	sc, err := signer.SignX509(cert)
	if err != nil {
		t.Fatalf("SignX509: %v", err)
	}
	// Verifier only trusts root B — must reject a cert chain issued under A.
	if err := VerifyX509(sc, caB.RootPool()); err == nil {
		t.Fatal("expected VerifyX509 to reject a chain rooted at a different, untrusted CA")
	}
}

func TestX509_Verify_RejectsTamperedCertificateHash(t *testing.T) {
	ca, err := NewIssuerCA("Veriqo Root")
	if err != nil {
		t.Fatalf("NewIssuerCA: %v", err)
	}
	signer, err := ca.IssueSigner("signer-1")
	if err != nil {
		t.Fatalf("IssueSigner: %v", err)
	}
	cert := sampleCertificate()
	sc, err := signer.SignX509(cert)
	if err != nil {
		t.Fatalf("SignX509: %v", err)
	}
	// Tamper with the claimed result after signing, without re-signing.
	sc.Certificate.ClaimedResult = "FAILED"
	if err := VerifyX509(sc, ca.RootPool()); err == nil {
		t.Fatal("expected VerifyX509 to reject a certificate whose fields were tampered post-signature")
	}
}

func TestX509_RootPEM_RoundTrips(t *testing.T) {
	ca, err := NewIssuerCA("Veriqo Root")
	if err != nil {
		t.Fatalf("NewIssuerCA: %v", err)
	}
	pemStr := ca.RootPEM()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(pemStr)) {
		t.Fatal("RootPEM output could not be parsed as a standard PEM certificate by x509.CertPool")
	}
}
