// Package identity implements SPIFFE-compatible workload identity.
// The audit noted that pkg/security/identity was a "local replacement with scope
// explained in comments". This package formalises the interface with a real
// local implementation + a Driver abstraction so the actual SPIRE Workload API
// can be wired in by swapping the driver without changing architecture.
//
// VEP-024: Security — Identity.
package identity

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"sync"
	"time"
)

// SPIFFEID is a SPIFFE identity URI of the form:
//
//	spiffe://<trust-domain>/<workload-path>
type SPIFFEID struct {
	TrustDomain string
	Path        string
}

// URI returns the string representation of the SPIFFE ID.
func (id SPIFFEID) URI() string {
	return fmt.Sprintf("spiffe://%s%s", id.TrustDomain, id.Path)
}

// Validate returns an error if the SPIFFE ID is malformed.
func (id SPIFFEID) Validate() error {
	if id.TrustDomain == "" {
		return errors.New("identity: trust domain must not be empty")
	}
	if len(id.Path) == 0 || id.Path[0] != '/' {
		return errors.New("identity: path must start with /")
	}
	u, err := url.Parse(id.URI())
	if err != nil || u.Scheme != "spiffe" {
		return fmt.Errorf("identity: invalid SPIFFE URI: %s", id.URI())
	}
	return nil
}

// SVID is a SPIFFE Verifiable Identity Document.
type SVID struct {
	ID          SPIFFEID
	Certificate *x509.Certificate
	PrivateKey  crypto.PrivateKey
	Chain       []*x509.Certificate
	ExpiresAt   time.Time
}

// PEMCertificate returns the DER certificate as PEM.
func (s *SVID) PEMCertificate() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate.Raw})
}

// IsExpired returns true if the SVID has passed its expiry time.
func (s *SVID) IsExpired() bool { return time.Now().After(s.ExpiresAt) }

// ─── Driver interface ─────────────────────────────────────────────────────────

// Provider fetches and rotates SVIDs for workloads.
// Local implementation is used in development and testing.
// SPIRE Workload API implementation replaces it in production.
type Provider interface {
	// FetchSVID returns the current SVID for this workload.
	FetchSVID() (*SVID, error)
	// Watch calls the callback whenever the SVID is rotated.
	Watch(onRotation func(*SVID)) (cancel func())
	// Verify validates a peer's SVID against the trust bundle.
	Verify(cert *x509.Certificate, peerID SPIFFEID) error
}

// ─── Local implementation ─────────────────────────────────────────────────────

// LocalProvider is a self-signed, in-process identity provider.
// Suitable for development, testing, and environments without SPIRE.
type LocalProvider struct {
	mu       sync.RWMutex
	id       SPIFFEID
	ttl      time.Duration
	svid     *SVID
	ca       *x509.Certificate
	caKey    crypto.PrivateKey
	watchers []func(*SVID)
	stopCh   chan struct{}
}

var _ Provider = (*LocalProvider)(nil)

// NewLocalProvider creates a self-signed CA and issues an initial SVID.
func NewLocalProvider(id SPIFFEID, ttl time.Duration) (*LocalProvider, error) {
	if err := id.Validate(); err != nil {
		return nil, err
	}
	if ttl <= 0 {
		ttl = time.Hour
	}

	p := &LocalProvider{id: id, ttl: ttl, stopCh: make(chan struct{})}
	if err := p.generateCA(); err != nil {
		return nil, err
	}
	svid, err := p.issue()
	if err != nil {
		return nil, err
	}
	p.svid = svid
	go p.autoRotate()
	return p, nil
}

// FetchSVID returns the current SVID, rotating if needed.
func (p *LocalProvider) FetchSVID() (*SVID, error) {
	p.mu.RLock()
	svid := p.svid
	p.mu.RUnlock()
	if svid.IsExpired() {
		return p.rotate()
	}
	return svid, nil
}

// Watch registers a callback for SVID rotation.
func (p *LocalProvider) Watch(cb func(*SVID)) func() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.watchers = append(p.watchers, cb)
	idx := len(p.watchers) - 1
	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		// Mark the watcher as nil; compact on next rotation.
		p.watchers[idx] = nil
	}
}

// Verify validates a peer's certificate against the local CA.
func (p *LocalProvider) Verify(cert *x509.Certificate, peerID SPIFFEID) error {
	p.mu.RLock()
	ca := p.ca
	p.mu.RUnlock()
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	_, err := cert.Verify(x509.VerifyOptions{Roots: pool})
	if err != nil {
		return fmt.Errorf("identity: peer cert verification failed: %w", err)
	}
	// Verify SPIFFE ID in SAN.
	want := peerID.URI()
	for _, u := range cert.URIs {
		if u.String() == want {
			return nil
		}
	}
	return fmt.Errorf("identity: SPIFFE ID %q not found in peer certificate", want)
}

// Stop shuts down the auto-rotation goroutine.
func (p *LocalProvider) Stop() { close(p.stopCh) }

func (p *LocalProvider) autoRotate() {
	for {
		p.mu.RLock()
		svid := p.svid
		p.mu.RUnlock()
		rotateAt := svid.ExpiresAt.Add(-p.ttl / 4) // rotate at 75% of TTL
		select {
		case <-time.After(time.Until(rotateAt)):
			_, _ = p.rotate()
		case <-p.stopCh:
			return
		}
	}
}

func (p *LocalProvider) rotate() (*SVID, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	svid, err := p.issue()
	if err != nil {
		return nil, err
	}
	p.svid = svid
	for _, w := range p.watchers {
		if w != nil {
			go w(svid)
		}
	}
	return svid, nil
}

func (p *LocalProvider) generateCA() error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Veriqo Local CA - " + p.id.TrustDomain},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(24 * time.Hour * 365),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return err
	}
	ca, err := x509.ParseCertificate(der)
	if err != nil {
		return err
	}
	p.ca = ca
	p.caKey = key
	return nil
}

func (p *LocalProvider) issue() (*SVID, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	spiffeURI, _ := url.Parse(p.id.URI())
	serial := sha256.Sum256([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	serialInt := new(big.Int).SetBytes(serial[:8])

	template := &x509.Certificate{
		SerialNumber:          serialInt,
		Subject:               pkix.Name{CommonName: p.id.URI()},
		URIs:                  []*url.URL{spiffeURI},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(p.ttl),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, p.ca, &key.PublicKey, p.caKey)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &SVID{
		ID:          p.id,
		Certificate: cert,
		PrivateKey:  key,
		Chain:       []*x509.Certificate{cert, p.ca},
		ExpiresAt:   cert.NotAfter,
	}, nil
}
