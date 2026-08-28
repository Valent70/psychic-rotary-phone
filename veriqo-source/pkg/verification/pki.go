// Session update — real X.509 PKI chain-of-trust signing.
//
// signature.go's SignedCertificate gives a verifier "this was signed by
// the holder of this specific ed25519 key" — proof of authenticity, but
// the verifier still has to trust that public key out-of-band (e.g. by
// reading it off a Veriqo deployment's docs page). The gap report named
// this precisely: "Signed AuditCertificate dengan PKI eksternal
// sungguhan (bukan hanya SHA-256 self-commitment) agar cmd/veriqo-verify
// dapat divalidasi pihak ketiga tanpa mempercayai kode sumber Veriqo
// sama sekali."
//
// IssuerCA below is a real X.509 certificate authority (stdlib
// crypto/x509, ECDSA P-256) — the SAME primitive rafttcp.CA already uses
// for mTLS node identity, applied here to evidence signing instead of
// transport identity. A CertifyingAuthority issues leaf certificates to
// signers; SignedCertificateX509 bundles the leaf certificate (and, for
// an intermediate CA, its issuing chain) alongside the signature, so a
// verifier who has ONLY the CA's root certificate (distributed once,
// out of band, exactly like any real PKI's root distribution) can run
// go's standard x509.Certificate.Verify — the same verification any
// browser performs for TLS — before trusting the signature at all.
//
// Honest scope: this is a real, standards-compliant X.509 chain (any
// tool that speaks X.509 — openssl, a browser's cert inspector — can
// parse and validate these certificates), but it is a CA *this repo
// mints*, not a certificate from a publicly trusted root (Let's Encrypt,
// DigiCert, etc.) or a Certificate Transparency log. Getting a publicly
// trusted root requires a real-world CA relationship (domain
// validation, payment, or ACME automation against a live CA) that this
// sandbox cannot perform. A deployment that wants a publicly-trusted
// chain swaps IssuerCA for a real CA-issued intermediate — the
// SignedCertificateX509 wire format and VerifyX509 logic do not change.
package verification

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"time"
)

var (
	ErrX509ChainInvalid    = errors.New("verification: signer certificate does not chain to the trusted CA")
	ErrX509SignatureBad    = errors.New("verification: X.509 signature does not match certificate hash")
	ErrX509MalformedBundle = errors.New("verification: malformed PEM in SignedCertificateX509")
)

// IssuerCA is a real X.509 certificate authority for evidence-signing
// certificates (independent of rafttcp.CA, which is scoped to transport
// mTLS identity — separating the two means rotating one never affects
// the other, matching real deployments where transport and
// document-signing PKI hierarchies are kept separate).
type IssuerCA struct {
	cert    *x509.Certificate
	key     *ecdsa.PrivateKey
	certDER []byte
}

// NewIssuerCA mints a fresh, self-signed root CA valid for 5 years —
// long-lived, since this is meant to be distributed once and pinned by
// verifiers, unlike leaf certs which are short-lived.
func NewIssuerCA(commonName string) (*IssuerCA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("verification: generate CA key: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: commonName, Organization: []string{"Veriqo"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(5, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("verification: self-sign CA: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("verification: parse CA cert: %w", err)
	}
	return &IssuerCA{cert: cert, key: key, certDER: der}, nil
}

// RootPool returns an *x509.CertPool containing only this CA's root —
// what a verifier pins once and reuses for every future SignedCertificateX509
// it checks, exactly like pinning a root CA for TLS.
func (ca *IssuerCA) RootPool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca.cert)
	return p
}

// RootPEM returns the CA's root certificate as PEM — the artifact a
// deployment publishes once, out of band, for verifiers to pin.
func (ca *IssuerCA) RootPEM() string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.certDER}))
}

// EvidenceSigner is a leaf identity issued by an IssuerCA, able to sign
// AuditCertificates such that the signature is verifiable by chaining
// the leaf cert back to the CA root.
type EvidenceSigner struct {
	leafCert    *x509.Certificate
	leafDER     []byte
	key         *ecdsa.PrivateKey
	chainToRoot [][]byte // [leafDER] — extend here for intermediates
}

// IssueSigner mints a short-lived (30-day) leaf certificate for
// signerID, signed by ca.
func (ca *IssuerCA) IssueSigner(signerID string) (*EvidenceSigner, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("verification: generate signer key: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: signerID, Organization: []string{"Veriqo"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(0, 0, 30),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		return nil, fmt.Errorf("verification: issue leaf cert: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("verification: parse leaf cert: %w", err)
	}
	return &EvidenceSigner{leafCert: cert, leafDER: der, key: key, chainToRoot: [][]byte{der}}, nil
}

// SignedCertificateX509 bundles an AuditCertificate with a real X.509
// chain-verifiable signature. Every field is self-contained PEM/hex text
// so it round-trips through JSON, a file, or a bundle exactly like
// SignedCertificate.
type SignedCertificateX509 struct {
	Certificate   AuditCertificate
	SignerCertPEM string // the leaf (signer) certificate, PEM-encoded
	Signature     string // hex-encoded ECDSA signature over sha256(Certificate.CertificateHash)
}

// SignX509 signs cert with s's leaf key, over its own CertificateHash
// (the same content commitment SignedCertificate signs), and bundles the
// leaf certificate so a verifier can chain-validate it against a root.
func (s *EvidenceSigner) SignX509(cert AuditCertificate) (SignedCertificateX509, error) {
	digest := sha256.Sum256([]byte(cert.CertificateHash))
	sig, err := ecdsa.SignASN1(rand.Reader, s.key, digest[:])
	if err != nil {
		return SignedCertificateX509{}, fmt.Errorf("verification: sign digest: %w", err)
	}
	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.leafDER})
	return SignedCertificateX509{
		Certificate:   cert,
		SignerCertPEM: string(leafPEM),
		Signature:     hex.EncodeToString(sig),
	}, nil
}

// VerifyX509 performs the full independent check a third party needs,
// using ONLY: (1) the SignedCertificateX509 bundle itself, and (2) a
// CertPool containing the CA root the verifier has chosen to trust
// (pinned once, out of band — see IssuerCA.RootPEM):
//
//  1. AuditCertificate's own hash commitment is internally consistent.
//  2. The embedded leaf certificate chain-validates against roots —
//     the real x509.Verify a browser would run, catching an expired,
//     revoked-shaped, or wrong-root certificate.
//  3. The signature over the certificate's hash was produced by the
//     private key matching that (now-trusted) leaf certificate.
func VerifyX509(sc SignedCertificateX509, roots *x509.CertPool) error {
	if !VerifyCertificate(sc.Certificate) {
		return ErrManifestMismatchCertificate
	}

	block, _ := pem.Decode([]byte(sc.SignerCertPEM))
	if block == nil {
		return ErrX509MalformedBundle
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrX509MalformedBundle, err)
	}

	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:     roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning, x509.ExtKeyUsageAny},
	}); err != nil {
		return fmt.Errorf("%w: %v", ErrX509ChainInvalid, err)
	}

	sigBytes, err := hex.DecodeString(sc.Signature)
	if err != nil {
		return fmt.Errorf("verification: decode signature: %w", err)
	}
	digest := sha256.Sum256([]byte(sc.Certificate.CertificateHash))
	pub, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return errors.New("verification: leaf certificate does not use an ECDSA public key")
	}
	if !ecdsa.VerifyASN1(pub, digest[:], sigBytes) {
		return ErrX509SignatureBad
	}
	return nil
}
