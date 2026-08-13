// Package spiffe is the spire_mtls blocker's qualification machinery:
// an IdentityProvider abstraction (FetchSVID/RefreshSVID/ValidateSVID/
// WatchRotation/GetTrustBundle/ValidatePeer) plus TestIdentityProvider,
// a fixture that mints real X.509 SPIFFE-format identities the same way
// pkg/transport/rafttcp's production CA does -- real ECDSA keys, real
// x509.CreateCertificate, a real SPIFFE URI SAN, and real chain
// verification via x509.Verify. It is deliberately a separate CA from
// rafttcp's, not a reuse of it: this package exists to qualify identity
// *validation logic* (expiry, revocation, untrusted-bundle and
// wrong-identity rejection) in isolation, the same way a real SPIRE
// Workload API client would be qualified against a fixture SPIRE server
// before being pointed at a real one. A prior round already ran a real
// single-node SPIRE v1.10.1 server+agent once in this session (see
// evidence/spire_mtls-local-integration.txt); that proved the real
// binary's attestation/issuance/revocation flow. This package proves
// the client-side validation logic this codebase would run against it.
package spiffe

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"sync"
	"time"

	"veriqo/pkg/blockers"
)

// SVID is an X.509 SPIFFE Verifiable Identity Document.
type SVID struct {
	SpiffeID string
	Cert     *x509.Certificate
	Serial   string
	NotAfter time.Time
}

// TrustBundle is the set of root CAs an IdentityProvider trusts.
type TrustBundle struct {
	Certs []*x509.Certificate
}

func (b TrustBundle) pool() *x509.CertPool {
	p := x509.NewCertPool()
	for _, c := range b.Certs {
		p.AddCert(c)
	}
	return p
}

// IdentityProvider is the Workload API surface this codebase depends
// on. Real implementations talk to a SPIRE agent's Workload API socket;
// TestIdentityProvider is a fixture with its own self-signed root.
type IdentityProvider interface {
	Mode() string
	FetchSVID(ctx context.Context, spiffeID string) (SVID, error)
	RefreshSVID(ctx context.Context, spiffeID string) (SVID, error)
	ValidateSVID(ctx context.Context, svid SVID) error
	GetTrustBundle(ctx context.Context) (TrustBundle, error)
	ValidatePeer(ctx context.Context, peerCert *x509.Certificate) (spiffeID string, err error)
}

// TestIdentityProvider mints real SPIFFE-format X.509 identities under
// its own self-signed root, tracks the current SVID per workload ID for
// rotation, and supports revocation -- so validation failure modes can
// be exercised deliberately rather than only ever hitting the happy
// path.
type TestIdentityProvider struct {
	mu        sync.Mutex
	caCert    *x509.Certificate
	caKey     *ecdsa.PrivateKey
	trustDom  string
	current   map[string]SVID
	revoked   map[string]bool
	watchers  map[string][]chan SVID
	nextIndex int64
}

// NewTestIdentityProvider mints a fresh self-signed root for trustDomain
// (e.g. "veriqo.test").
func NewTestIdentityProvider(trustDomain string) (*TestIdentityProvider, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "spiffe fixture root: " + trustDomain},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &TestIdentityProvider{
		caCert: cert, caKey: key, trustDom: trustDomain,
		current: make(map[string]SVID), revoked: make(map[string]bool),
		watchers: make(map[string][]chan SVID),
	}, nil
}

func (p *TestIdentityProvider) Mode() string { return "SIMULATED" }

// IssueWithTTL mints a new SVID for spiffeID with a caller-chosen TTL --
// including a negative TTL, so an already-expired SVID can be produced
// deterministically for qualification instead of sleeping in a test.
func (p *TestIdentityProvider) IssueWithTTL(spiffeID string, ttl time.Duration) (SVID, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return SVID{}, err
	}
	uri, err := url.Parse(spiffeID)
	if err != nil || uri.Scheme != "spiffe" {
		return SVID{}, fmt.Errorf("spiffe: %q is not a valid spiffe:// ID", spiffeID)
	}

	p.mu.Lock()
	p.nextIndex++
	serial := big.NewInt(p.nextIndex)
	p.mu.Unlock()

	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: spiffeID},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(ttl),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		URIs:         []*url.URL{uri},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, p.caCert, &key.PublicKey, p.caKey)
	if err != nil {
		return SVID{}, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return SVID{}, err
	}

	svid := SVID{SpiffeID: spiffeID, Cert: cert, Serial: serial.String(), NotAfter: tmpl.NotAfter}
	p.mu.Lock()
	p.current[spiffeID] = svid
	watchers := append([]chan SVID(nil), p.watchers[spiffeID]...)
	p.mu.Unlock()

	for _, ch := range watchers {
		select {
		case ch <- svid:
		default:
		}
	}
	return svid, nil
}

// FetchSVID returns the current SVID for spiffeID, minting one with a
// 1-hour TTL if none exists yet.
func (p *TestIdentityProvider) FetchSVID(ctx context.Context, spiffeID string) (SVID, error) {
	p.mu.Lock()
	svid, ok := p.current[spiffeID]
	p.mu.Unlock()
	if ok {
		return svid, nil
	}
	return p.IssueWithTTL(spiffeID, time.Hour)
}

// RefreshSVID always mints a new SVID (new serial), simulating rotation
// ahead of expiry.
func (p *TestIdentityProvider) RefreshSVID(ctx context.Context, spiffeID string) (SVID, error) {
	return p.IssueWithTTL(spiffeID, time.Hour)
}

// WatchRotation returns a channel that receives every SVID minted for
// spiffeID from this call onward (via FetchSVID's first issuance or any
// RefreshSVID), plus a cancel function to stop watching.
func (p *TestIdentityProvider) WatchRotation(spiffeID string) (<-chan SVID, func()) {
	ch := make(chan SVID, 8)
	p.mu.Lock()
	p.watchers[spiffeID] = append(p.watchers[spiffeID], ch)
	p.mu.Unlock()
	cancel := func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		list := p.watchers[spiffeID]
		for i, c := range list {
			if c == ch {
				p.watchers[spiffeID] = append(list[:i], list[i+1:]...)
				break
			}
		}
	}
	return ch, cancel
}

// Revoke marks a previously-issued serial as revoked.
func (p *TestIdentityProvider) Revoke(serial string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.revoked[serial] = true
}

// GetTrustBundle returns this provider's self-signed root.
func (p *TestIdentityProvider) GetTrustBundle(ctx context.Context) (TrustBundle, error) {
	return TrustBundle{Certs: []*x509.Certificate{p.caCert}}, nil
}

// ValidateSVID checks svid.Cert against this provider's own trust
// bundle via real x509 chain verification, confirms the cert's own
// embedded SPIFFE URI SAN -- not the caller-supplied SpiffeID field --
// matches what's claimed, checks expiry, and checks revocation.
func (p *TestIdentityProvider) ValidateSVID(ctx context.Context, svid SVID) error {
	if svid.Cert == nil {
		return errors.New("spiffe: SVID has no certificate")
	}
	bundle, _ := p.GetTrustBundle(ctx)
	opts := x509.VerifyOptions{
		Roots: bundle.pool(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
	}
	if _, err := svid.Cert.Verify(opts); err != nil {
		return fmt.Errorf("spiffe: chain does not verify against trust bundle: %w", err)
	}
	actualID, err := extractSPIFFEID(svid.Cert)
	if err != nil {
		return err
	}
	if actualID != svid.SpiffeID {
		return fmt.Errorf("spiffe: claimed ID %q does not match certificate's actual SPIFFE URI SAN %q", svid.SpiffeID, actualID)
	}
	if time.Now().After(svid.Cert.NotAfter) {
		return fmt.Errorf("spiffe: SVID expired at %s", svid.Cert.NotAfter)
	}
	p.mu.Lock()
	revoked := p.revoked[svid.Cert.SerialNumber.String()]
	p.mu.Unlock()
	if revoked {
		return errors.New("spiffe: SVID serial is revoked")
	}
	return nil
}

// ValidatePeer verifies peerCert the same way ValidateSVID does, and on
// success returns the SPIFFE ID actually embedded in the certificate --
// never a caller-supplied value.
func (p *TestIdentityProvider) ValidatePeer(ctx context.Context, peerCert *x509.Certificate) (string, error) {
	id, err := extractSPIFFEID(peerCert)
	if err != nil {
		return "", err
	}
	if err := p.ValidateSVID(ctx, SVID{SpiffeID: id, Cert: peerCert}); err != nil {
		return "", err
	}
	return id, nil
}

func extractSPIFFEID(cert *x509.Certificate) (string, error) {
	for _, u := range cert.URIs {
		if u.Scheme == "spiffe" {
			return u.String(), nil
		}
	}
	return "", errors.New("spiffe: no SPIFFE URI SAN present on certificate")
}

// RunQualification exercises the happy path (fetch, validate, rotate,
// watch) and the failure matrix (expired, revoked, untrusted bundle,
// claimed-vs-actual ID mismatch), recording the outcome on contract.
func RunQualification(contract *blockers.Contract) (blockers.RunResult, error) {
	ctx := context.Background()
	measurements := map[string]string{}
	pass := true
	var failures []string
	record := func(name string, ok bool) {
		measurements[name] = fmt.Sprintf("blocked_or_passed=%v", ok)
		if !ok {
			pass = false
			failures = append(failures, name+" did not behave as required")
		}
	}

	provider, err := NewTestIdentityProvider("veriqo.test")
	if err != nil {
		return blockers.RunResult{}, fmt.Errorf("spiffe: new provider: %w", err)
	}
	workloadID := "spiffe://veriqo.test/workload/a"

	svid1, err := provider.FetchSVID(ctx, workloadID)
	if err != nil {
		return blockers.RunResult{}, fmt.Errorf("spiffe: fetch: %w", err)
	}
	record("fetch_then_validate", provider.ValidateSVID(ctx, svid1) == nil)

	spiffeID, err := provider.ValidatePeer(ctx, svid1.Cert)
	record("validate_peer_returns_actual_id", err == nil && spiffeID == workloadID)

	watchCh, cancel := provider.WatchRotation(workloadID)
	defer cancel()
	svid2, err := provider.RefreshSVID(ctx, workloadID)
	if err != nil {
		return blockers.RunResult{}, fmt.Errorf("spiffe: refresh: %w", err)
	}
	rotated := svid2.Serial != svid1.Serial
	select {
	case got := <-watchCh:
		rotated = rotated && got.Serial == svid2.Serial
	case <-time.After(time.Second):
		rotated = false
	}
	record("rotation_observed_via_watch", rotated)
	record("old_svid_still_structurally_valid_until_revoked", provider.ValidateSVID(ctx, svid1) == nil)

	expired, err := provider.IssueWithTTL(workloadID, -time.Hour)
	if err != nil {
		return blockers.RunResult{}, fmt.Errorf("spiffe: issue expired: %w", err)
	}
	record("expired_svid_rejected", provider.ValidateSVID(ctx, expired) != nil)

	toRevoke, err := provider.IssueWithTTL(workloadID, time.Hour)
	if err != nil {
		return blockers.RunResult{}, fmt.Errorf("spiffe: issue for revocation: %w", err)
	}
	provider.Revoke(toRevoke.Serial)
	record("revoked_svid_rejected", provider.ValidateSVID(ctx, toRevoke) != nil)

	foreign, err := NewTestIdentityProvider("attacker.test")
	if err != nil {
		return blockers.RunResult{}, fmt.Errorf("spiffe: new foreign provider: %w", err)
	}
	foreignSVID, err := foreign.IssueWithTTL(workloadID, time.Hour)
	if err != nil {
		return blockers.RunResult{}, fmt.Errorf("spiffe: issue foreign: %w", err)
	}
	record("untrusted_bundle_rejected", provider.ValidateSVID(ctx, foreignSVID) != nil)

	genuine, err := provider.IssueWithTTL(workloadID, time.Hour)
	if err != nil {
		return blockers.RunResult{}, fmt.Errorf("spiffe: issue for mismatch test: %w", err)
	}
	mismatched := genuine
	mismatched.SpiffeID = "spiffe://veriqo.test/workload/b" // claim a different identity than the cert actually carries
	record("claimed_id_mismatch_rejected", provider.ValidateSVID(ctx, mismatched) != nil)

	result := blockers.RunResult{BlockerID: contract.ID, Mode: "FIXTURE", Pass: pass, Measurements: measurements}
	if !pass {
		result.FailureReason = fmt.Sprintf("%v", failures)
	}
	if err := contract.RecordFixtureRun(result); err != nil {
		return result, err
	}
	return result, nil
}
