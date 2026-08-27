// Asymmetric-key signatures for AuditCertificate — closes the explicit
// "Signature" gap named in "Yang_masih_belum.docx" section E: the IVF
// previously only had a SHA-256 hash commitment (integrity), not a
// signature (authenticity/non-repudiation) — the doc explicitly asks
// "Kalau belum, maka IVF baru sekitar 45-55%".
//
// Uses stdlib crypto/ed25519 — zero external dependency, unlike a real
// PKI/CA hierarchy (X.509 chain validation, revocation, timestamping
// authority), which remains an explicit open item: this gives a
// verifier confidence "this certificate was signed by the holder of a
// specific private key", not "that key is trusted by a recognized
// authority" — the latter requires an external PKI this sandbox cannot
// provision or validate against.
package verification

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
)

var (
	ErrInvalidSignature            = errors.New("verification: signature does not match certificate")
	ErrManifestMismatchCertificate = errors.New("verification: certificate hash commitment is inconsistent with its fields")
)

// SigningKey wraps an ed25519 keypair — generated once per issuer
// (e.g. once per Veriqo deployment, or once per verifying node), not
// per certificate.
type SigningKey struct {
	Public  ed25519.PublicKey
	private ed25519.PrivateKey
}

// PublicKeyHex returns the hex-encoded public key — convenience for
// callers distributing the public key separately from a signed
// certificate (e.g. publishing it once in a deployment's docs).
func (k SigningKey) PublicKeyHex() string {
	return hex.EncodeToString(k.Public)
}

// GenerateSigningKey creates a fresh ed25519 keypair. In production this
// would be provisioned once and the private key held in a KMS/HSM/secrets
// manager (see docs/THREAT_MODEL.md) — never regenerated per process
// start, unlike this sandbox's demo usage.
func GenerateSigningKey() (SigningKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return SigningKey{}, fmt.Errorf("verification: generate signing key: %w", err)
	}
	return SigningKey{Public: pub, private: priv}, nil
}

// SignedCertificate bundles an AuditCertificate with an ed25519
// signature over its CertificateHash, plus the public key needed to
// verify it — self-contained, so a regulator can validate authenticity
// without any live connection back to Veriqo.
type SignedCertificate struct {
	Certificate AuditCertificate
	PublicKey   string // hex-encoded ed25519 public key
	Signature   string // hex-encoded ed25519 signature over Certificate.CertificateHash
}

// Sign produces a SignedCertificate: the certificate's own
// CertificateHash (already a content commitment — see IssueCertificate)
// is what gets signed, so any change to the certificate's fields
// invalidates BOTH the hash commitment AND the signature.
func (k SigningKey) Sign(cert AuditCertificate) SignedCertificate {
	sig := ed25519.Sign(k.private, []byte(cert.CertificateHash))
	return SignedCertificate{
		Certificate: cert,
		PublicKey:   hex.EncodeToString(k.Public),
		Signature:   hex.EncodeToString(sig),
	}
}

// VerifySigned performs the FULL independent check a third party needs:
// (1) the certificate's own hash commitment is internally consistent
// (VerifyCertificate), and (2) the signature over that hash was
// produced by the holder of the private key matching PublicKey. Neither
// check depends on any live Veriqo process — everything needed is in
// the SignedCertificate itself.
func VerifySigned(sc SignedCertificate) error {
	if !VerifyCertificate(sc.Certificate) {
		return ErrManifestMismatchCertificate
	}
	pubBytes, err := hex.DecodeString(sc.PublicKey)
	if err != nil {
		return fmt.Errorf("verification: decode public key: %w", err)
	}
	sigBytes, err := hex.DecodeString(sc.Signature)
	if err != nil {
		return fmt.Errorf("verification: decode signature: %w", err)
	}
	if len(pubBytes) != ed25519.PublicKeySize {
		return fmt.Errorf("verification: invalid public key length %d", len(pubBytes))
	}
	if !ed25519.Verify(ed25519.PublicKey(pubBytes), []byte(sc.Certificate.CertificateHash), sigBytes) {
		return ErrInvalidSignature
	}
	return nil
}
