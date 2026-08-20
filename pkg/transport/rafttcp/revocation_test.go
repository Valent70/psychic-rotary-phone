package rafttcp

import (
	"crypto/x509"
	"errors"
	"testing"
)

func mustRafttcpCA(t *testing.T) *CA {
	t.Helper()
	ca, err := NewCA()
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}
	return ca
}

func TestRevocationListTracksRevokedSerials(t *testing.T) {
	ca := mustRafttcpCA(t)
	crl := NewRevocationList(ca)
	leaf, err := ca.IssueLeaf("node-a")
	if err != nil {
		t.Fatalf("IssueLeaf: %v", err)
	}
	cert, err := parseLeafForTest(leaf)
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	if crl.IsRevoked(cert.SerialNumber) {
		t.Fatal("a freshly issued leaf must not already be revoked")
	}
	crl.Revoke(cert.SerialNumber, "key compromise")
	if !crl.IsRevoked(cert.SerialNumber) {
		t.Fatal("Revoke must take effect immediately")
	}

	other, err := ca.IssueLeaf("node-b")
	if err != nil {
		t.Fatalf("IssueLeaf: %v", err)
	}
	otherCert, err := parseLeafForTest(other)
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	if crl.IsRevoked(otherCert.SerialNumber) {
		t.Fatal("revoking one serial must not revoke an unrelated one")
	}
}

// TestExportCRLIsARealParseableX509CRL proves the revocation list isn't
// just a private in-process map — it round-trips through the standard
// x509.CRL wire format any external tool can parse and check, mirroring
// pkg/verification's existing CRL for its separate evidence-signing CA.
func TestExportCRLIsARealParseableX509CRL(t *testing.T) {
	ca := mustRafttcpCA(t)
	crl := NewRevocationList(ca)
	leaf, err := ca.IssueLeaf("node-to-revoke")
	if err != nil {
		t.Fatalf("IssueLeaf: %v", err)
	}
	cert, err := parseLeafForTest(leaf)
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	crl.Revoke(cert.SerialNumber, "key compromise")

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
		if rc.SerialNumber.Cmp(cert.SerialNumber) == 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("revoked serial not present in exported CRL")
	}
}

// TestPeerVerifierRejectsARevokedLeafCert is the adversarial case that
// matters: a leaf whose chain is still cryptographically valid and
// unexpired, but which the CA has since revoked (e.g. its private key
// leaked) must still be refused — the exact gap TLS's own handshake
// cannot close on its own.
func TestPeerVerifierRejectsARevokedLeafCert(t *testing.T) {
	ca := mustRafttcpCA(t)
	leaf, err := ca.IssueLeaf("node-a")
	if err != nil {
		t.Fatalf("IssueLeaf: %v", err)
	}
	cert, err := parseLeafForTest(leaf)
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}

	v := NewPeerVerifier(ca, []string{"node-a"})
	if _, err := v.Verify(cert); err != nil {
		t.Fatalf("an unrevoked, in-membership leaf must verify: %v", err)
	}

	crl := NewRevocationList(ca)
	crl.Revoke(cert.SerialNumber, "key compromise")
	v.SetRevocationList(crl)

	if _, err := v.Verify(cert); err == nil {
		t.Fatal("a revoked leaf must be refused even though its chain and expiry are still valid")
	} else if !errors.Is(err, ErrPeerCertRevoked) {
		t.Fatalf("expected ErrPeerCertRevoked, got %v", err)
	}
}

func TestNilRevocationListPreservesPreP106Behaviour(t *testing.T) {
	ca := mustRafttcpCA(t)
	leaf, err := ca.IssueLeaf("node-a")
	if err != nil {
		t.Fatalf("IssueLeaf: %v", err)
	}
	cert, err := parseLeafForTest(leaf)
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	v := NewPeerVerifier(ca, []string{"node-a"})
	if _, err := v.Verify(cert); err != nil {
		t.Fatalf("a verifier with no CRL set must behave exactly as before P1-06: %v", err)
	}
}

func TestStatusIsCASignedAndVerifiable(t *testing.T) {
	ca := mustRafttcpCA(t)
	crl := NewRevocationList(ca)
	leaf, err := ca.IssueLeaf("node-a")
	if err != nil {
		t.Fatalf("IssueLeaf: %v", err)
	}
	cert, err := parseLeafForTest(leaf)
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}

	status, err := crl.Status(cert.SerialNumber)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Revoked {
		t.Fatal("an unrevoked serial must report Revoked=false")
	}
	if err := VerifyStatus(ca, status); err != nil {
		t.Fatalf("a genuinely CA-signed status must verify: %v", err)
	}

	crl.Revoke(cert.SerialNumber, "node decommissioned")
	revokedStatus, err := crl.Status(cert.SerialNumber)
	if err != nil {
		t.Fatalf("Status after revoke: %v", err)
	}
	if !revokedStatus.Revoked || revokedStatus.Reason != "node decommissioned" {
		t.Fatalf("status must reflect the revocation immediately: %+v", revokedStatus)
	}
	if err := VerifyStatus(ca, revokedStatus); err != nil {
		t.Fatalf("the post-revocation status must also verify: %v", err)
	}
}

func TestVerifyStatusRejectsATamperedStatus(t *testing.T) {
	ca := mustRafttcpCA(t)
	crl := NewRevocationList(ca)
	leaf, err := ca.IssueLeaf("node-a")
	if err != nil {
		t.Fatalf("IssueLeaf: %v", err)
	}
	cert, err := parseLeafForTest(leaf)
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	status, err := crl.Status(cert.SerialNumber)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	status.Revoked = true // attacker flips the answer without the CA key
	if err := VerifyStatus(ca, status); err == nil {
		t.Fatal("a tampered status must fail verification")
	}
}

func TestVerifyStatusRejectsAWrongCA(t *testing.T) {
	realCA := mustRafttcpCA(t)
	otherCA := mustRafttcpCA(t)
	crl := NewRevocationList(realCA)
	leaf, err := realCA.IssueLeaf("node-a")
	if err != nil {
		t.Fatalf("IssueLeaf: %v", err)
	}
	cert, err := parseLeafForTest(leaf)
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	status, err := crl.Status(cert.SerialNumber)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if err := VerifyStatus(otherCA, status); err == nil {
		t.Fatal("a status signed by one CA must not verify against a different CA's public key")
	}
}
