// PKI lifecycle: rotation and revocation — the P0 gap named across every
// audit in this session ("PKI rotation/revocation ... Broken / not
// done"). Builds on IssuerCA/EvidenceSigner (pki.go) rather than
// replacing them.
package verification

import (
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"
)

var (
	ErrCertRevoked   = errors.New("verification: signer certificate has been revoked")
	ErrCertExpired   = errors.New("verification: signer certificate has expired")
	ErrNoTrustedRoot = errors.New("verification: certificate does not chain to any currently-trusted root (old or new)")
)

// RevocationList is a real X.509 CRL-backed revocation registry for one
// IssuerCA. Thread-safe: a leaf can be revoked at any time (compromised
// key, decommissioned signer, employee offboarding) and every subsequent
// VerifyX509WithRevocation call sees it immediately.
type RevocationList struct {
	mu      sync.RWMutex
	ca      *IssuerCA
	revoked map[string]revocationEntry // serial (decimal string) -> entry
}

type revocationEntry struct {
	SerialNumber *big.Int
	RevokedAt    time.Time
	Reason       string
}

func NewRevocationList(ca *IssuerCA) *RevocationList {
	return &RevocationList{ca: ca, revoked: map[string]revocationEntry{}}
}

// Revoke marks a leaf certificate's serial number as revoked. reason is
// a free-text audit note (e.g. "key compromise", "signer decommissioned").
func (r *RevocationList) Revoke(serial *big.Int, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.revoked[serial.String()] = revocationEntry{SerialNumber: serial, RevokedAt: time.Now(), Reason: reason}
}

// IsRevoked reports whether serial is currently revoked.
func (r *RevocationList) IsRevoked(serial *big.Int) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.revoked[serial.String()]
	return ok
}

// ExportCRL produces a real, standards-compliant X.509 Certificate
// Revocation List (RFC 5280) signed by the issuing CA — the same format
// any CRL-checking tool (openssl crl, browsers) can parse, so a Veriqo
// deployment's revocations are independently auditable, not just a
// private in-process map.
func (r *RevocationList) ExportCRL() ([]byte, error) {
	r.mu.RLock()
	entries := make([]pkix.RevokedCertificate, 0, len(r.revoked))
	for _, e := range r.revoked {
		entries = append(entries, pkix.RevokedCertificate{SerialNumber: e.SerialNumber, RevocationTime: e.RevokedAt})
	}
	r.mu.RUnlock()

	tmpl := &x509.RevocationList{
		Number:              big.NewInt(time.Now().Unix()),
		ThisUpdate:          time.Now(),
		NextUpdate:          time.Now().Add(24 * time.Hour),
		RevokedCertificates: entries,
	}
	return x509.CreateRevocationList(rand.Reader, tmpl, r.ca.cert, r.ca.key)
}

// VerifyX509WithRevocation is VerifyX509 plus expiry and CRL checks —
// the check a production verifier should actually run (VerifyX509 alone
// only proves chain-of-trust, not that the signer is still authorized).
func VerifyX509WithRevocation(sc SignedCertificateX509, roots *x509.CertPool, crl *RevocationList) error {
	if err := VerifyX509(sc, roots); err != nil {
		return err
	}
	block, _ := pem.Decode([]byte(sc.SignerCertPEM))
	if block == nil {
		return ErrX509MalformedBundle
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrX509MalformedBundle, err)
	}
	now := time.Now()
	if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		return fmt.Errorf("%w: valid %s to %s, now %s", ErrCertExpired, leaf.NotBefore, leaf.NotAfter, now)
	}
	if crl != nil && crl.IsRevoked(leaf.SerialNumber) {
		return fmt.Errorf("%w: serial %s", ErrCertRevoked, leaf.SerialNumber.String())
	}
	return nil
}

// RotateSigner issues a fresh leaf for the SAME signerID and revokes the
// OLD one in one atomic-from-the-caller's-view operation — the standard
// "compromise or routine rotation" workflow. The old signer stops being
// accepted by any verifier using this RevocationList from this call
// onward, even though its NotAfter hasn't been reached yet.
func (ca *IssuerCA) RotateSigner(old *EvidenceSigner, signerID string, crl *RevocationList, reason string) (*EvidenceSigner, error) {
	fresh, err := ca.IssueSigner(signerID)
	if err != nil {
		return nil, fmt.Errorf("verification: rotate signer: issue replacement: %w", err)
	}
	if crl != nil && old != nil {
		crl.Revoke(old.leafCert.SerialNumber, reason)
	}
	return fresh, nil
}

// RootRotation manages CA root rotation: verifiers must keep trusting
// certificates issued by the OLD root during a transition window (every
// still-valid, not-yet-expired leaf issued by the old CA) while new
// leaves get issued by the NEW root — exactly how real PKI root
// rotation works (e.g. browsers trusting both an expiring and its
// replacement root simultaneously for a period).
type RootRotation struct {
	Old *IssuerCA
	New *IssuerCA
}

// RotateRoot mints a fresh root CA to replace old, returning a
// RootRotation bundling both. The caller re-issues active signers
// against New going forward (via New.IssueSigner) and distributes New's
// RootPEM to verifiers; Old should keep being accepted (via
// TrustedPool) until every leaf it issued has expired or been
// explicitly revoked.
func RotateRoot(old *IssuerCA, commonName string) (*RootRotation, error) {
	newCA, err := NewIssuerCA(commonName)
	if err != nil {
		return nil, err
	}
	return &RootRotation{Old: old, New: newCA}, nil
}

// TrustedPool returns a CertPool containing BOTH roots (old and new) —
// what a verifier should use DURING the rotation transition window, so
// certificates issued before the rotation keep validating right up
// until they individually expire or get revoked.
func (rr *RootRotation) TrustedPool() *x509.CertPool {
	p := x509.NewCertPool()
	if rr.Old != nil {
		p.AddCert(rr.Old.cert)
	}
	if rr.New != nil {
		p.AddCert(rr.New.cert)
	}
	return p
}
