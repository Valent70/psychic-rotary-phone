package rafttcp

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"veriqo/pkg/consensus/raftlite"
)

// writeSVIDFiles writes a real, CA-issued leaf cert + key + a bundle
// (containing caForBundle, which may differ from the issuer to
// exercise the "does not verify" fail-closed path) into dir using the
// same three-file layout `spire-agent api fetch x509 -write` produces.
func writeSVIDFiles(t *testing.T, dir string, leaf tls.Certificate, caForBundle *CA) (svidPath, keyPath, bundlePath string) {
	t.Helper()
	svidPath = filepath.Join(dir, "svid.0.pem")
	keyPath = filepath.Join(dir, "svid.0.key.pem")
	bundlePath = filepath.Join(dir, "bundle.0.pem")

	var certPEM []byte
	for _, der := range leaf.Certificate {
		certPEM = append(certPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	if err := os.WriteFile(svidPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(leaf.PrivateKey.(*ecdsa.PrivateKey))
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if caForBundle != nil {
		bundlePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caForBundle.der})
		if err := os.WriteFile(bundlePath, bundlePEM, 0o600); err != nil {
			t.Fatal(err)
		}
	} else {
		if err := os.WriteFile(bundlePath, []byte{}, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return svidPath, keyPath, bundlePath
}

// mintLeaf mints a leaf cert signed by ca with full control over
// NotBefore/NotAfter and whether a SPIFFE URI SAN is present, so tests
// can construct exactly the adversarial shapes PART 9's "negative
// identity test" and fail-closed requirements name.
func mintLeaf(t *testing.T, ca *CA, nodeID string, notBefore, notAfter time.Time, includeSPIFFESAN bool) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: nodeID},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
	}
	if includeSPIFFESAN {
		spiffeID, err := url.Parse(fmt.Sprintf("spiffe://veriqo.global/node/%s", nodeID))
		if err != nil {
			t.Fatal(err)
		}
		tmpl.URIs = []*url.URL{spiffeID}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der, ca.der}, PrivateKey: key}
}

func TestFileCertSourceFailsClosedBeforeFirstReload(t *testing.T) {
	src := NewFileCertSource("/nonexistent/svid.pem", "/nonexistent/key.pem", "/nonexistent/bundle.pem")
	if _, err := src.Current(); !errors.Is(err, ErrWorkloadAPINotYetLoaded) {
		t.Fatalf("expected ErrWorkloadAPINotYetLoaded, got %v", err)
	}
	if _, err := src.CurrentRoots(); !errors.Is(err, ErrWorkloadAPINotYetLoaded) {
		t.Fatalf("expected ErrWorkloadAPINotYetLoaded, got %v", err)
	}
}

func TestFileCertSourceLoadsAValidSVIDAndBundle(t *testing.T) {
	ca, err := NewCA()
	if err != nil {
		t.Fatal(err)
	}
	leaf := mintLeaf(t, ca, "node-A", time.Now().Add(-time.Hour), time.Now().Add(time.Hour), true)
	dir := t.TempDir()
	svidPath, keyPath, bundlePath := writeSVIDFiles(t, dir, leaf, ca)

	src := NewFileCertSource(svidPath, keyPath, bundlePath)
	if err := src.Reload(); err != nil {
		t.Fatalf("expected Reload to succeed: %v", err)
	}
	cert, err := src.Current()
	if err != nil {
		t.Fatal(err)
	}
	spiffeID, err := ExtractSPIFFEID(cert.Leaf)
	if err != nil || spiffeID != "spiffe://veriqo.global/node/node-A" {
		t.Fatalf("unexpected SPIFFE ID: %q err=%v", spiffeID, err)
	}
	roots, err := src.CurrentRoots()
	if err != nil || roots == nil {
		t.Fatalf("expected usable roots: %v", err)
	}
	if _, err := cert.Leaf.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}); err != nil {
		t.Fatalf("loaded cert must verify against loaded roots: %v", err)
	}
}

func TestFileCertSourceRejectsExpiredSVID(t *testing.T) {
	ca, err := NewCA()
	if err != nil {
		t.Fatal(err)
	}
	leaf := mintLeaf(t, ca, "node-A", time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour), true) // already expired
	dir := t.TempDir()
	svidPath, keyPath, bundlePath := writeSVIDFiles(t, dir, leaf, ca)

	src := NewFileCertSource(svidPath, keyPath, bundlePath)
	err = src.Reload()
	if !errors.Is(err, ErrSVIDExpired) {
		t.Fatalf("expected ErrSVIDExpired, got %v", err)
	}
	if _, err := src.Current(); !errors.Is(err, ErrWorkloadAPINotYetLoaded) {
		t.Fatal("an expired SVID must never become the loaded cert")
	}
}

func TestFileCertSourceRejectsMissingSPIFFESAN(t *testing.T) {
	ca, err := NewCA()
	if err != nil {
		t.Fatal(err)
	}
	leaf := mintLeaf(t, ca, "node-A", time.Now().Add(-time.Hour), time.Now().Add(time.Hour), false) // no SPIFFE SAN
	dir := t.TempDir()
	svidPath, keyPath, bundlePath := writeSVIDFiles(t, dir, leaf, ca)

	src := NewFileCertSource(svidPath, keyPath, bundlePath)
	if err := src.Reload(); !errors.Is(err, ErrSVIDMissingSPIFFEID) {
		t.Fatalf("expected ErrSVIDMissingSPIFFEID, got %v", err)
	}
}

func TestFileCertSourceRejectsKeyMismatch(t *testing.T) {
	ca, err := NewCA()
	if err != nil {
		t.Fatal(err)
	}
	leaf := mintLeaf(t, ca, "node-A", time.Now().Add(-time.Hour), time.Now().Add(time.Hour), true)
	dir := t.TempDir()
	svidPath, keyPath, bundlePath := writeSVIDFiles(t, dir, leaf, ca)

	// Overwrite the key file with an unrelated, freshly generated key.
	wrongKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wrongDER, err := x509.MarshalECPrivateKey(wrongKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: wrongDER}), 0o600); err != nil {
		t.Fatal(err)
	}

	src := NewFileCertSource(svidPath, keyPath, bundlePath)
	if err := src.Reload(); !errors.Is(err, ErrSVIDKeyMismatch) {
		t.Fatalf("expected ErrSVIDKeyMismatch, got %v", err)
	}
}

func TestFileCertSourceRejectsSVIDNotVerifiableAgainstBundle(t *testing.T) {
	ca1, err := NewCA()
	if err != nil {
		t.Fatal(err)
	}
	ca2, err := NewCA() // a different, unrelated trust root
	if err != nil {
		t.Fatal(err)
	}
	leaf := mintLeaf(t, ca1, "node-A", time.Now().Add(-time.Hour), time.Now().Add(time.Hour), true)
	dir := t.TempDir()
	// Bundle deliberately does not include ca1, the leaf's real issuer.
	svidPath, keyPath, bundlePath := writeSVIDFiles(t, dir, leaf, ca2)

	src := NewFileCertSource(svidPath, keyPath, bundlePath)
	if err := src.Reload(); err == nil {
		t.Fatal("expected Reload to fail closed when the SVID does not verify against the bundle")
	}
}

func TestFileCertSourceStartWatchingDetectsRotation(t *testing.T) {
	ca, err := NewCA()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	leafV1 := mintLeaf(t, ca, "node-A-v1", time.Now().Add(-time.Hour), time.Now().Add(time.Hour), true)
	svidPath, keyPath, bundlePath := writeSVIDFiles(t, dir, leafV1, ca)

	src := NewFileCertSource(svidPath, keyPath, bundlePath)
	if err := src.Reload(); err != nil {
		t.Fatal(err)
	}
	certV1, _ := src.Current()

	done := make(chan struct{})
	defer close(done)
	errs := src.StartWatching(done, 20*time.Millisecond)
	go func() {
		for range errs {
			// drained; unexpected reload errors would fail the
			// eventual assertion below by never rotating.
		}
	}()

	time.Sleep(60 * time.Millisecond)
	leafV2 := mintLeaf(t, ca, "node-A-v2", time.Now().Add(-time.Hour), time.Now().Add(time.Hour), true)
	if _, _, _ = writeSVIDFiles(t, dir, leafV2, ca); false {
	} // rewrite same paths with new content

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		cur, err := src.Current()
		if err == nil && string(cur.Certificate[0]) != string(certV1.Certificate[0]) {
			return // rotation detected -- test passes
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("expected StartWatching to detect the rewritten SVID files and reload within the deadline")
}

// --- PART 9's "negative identity test" + revocation, at the real
// production PeerVerifier code path -------------------------------

func TestNegativeIdentityTest_PeerNoLongerTrustedAfterBundleRotatesOutItsIssuer(t *testing.T) {
	ca1, err := NewCA() // originally trusted
	if err != nil {
		t.Fatal(err)
	}
	ca2, err := NewCA() // the ROTATED-TO trust root, unrelated to ca1
	if err != nil {
		t.Fatal(err)
	}
	peerLeaf, err := ca1.IssueLeaf("peer-1")
	if err != nil {
		t.Fatal(err)
	}
	peerCert, err := x509.ParseCertificate(peerLeaf.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}

	current := ca1
	verifier := NewPeerVerifierFromTrustSource(func() (*x509.CertPool, error) { return current.Pool(), nil }, []string{"peer-1"})

	if _, err := verifier.Verify(peerCert); err != nil {
		t.Fatalf("expected the peer to verify while its issuer is still trusted: %v", err)
	}

	// Simulate a real bundle rotation that drops ca1 (e.g. the peer's
	// node was compromised and its issuing CA revoked) -- trust now
	// comes entirely from ca2, which never issued peerCert.
	current = ca2
	if _, err := verifier.Verify(peerCert); err == nil {
		t.Fatal("expected the SAME peer certificate to be REJECTED once its issuing CA is rotated out of the trust bundle")
	}
}

func TestPeerVerifierFromTrustSourceFailsClosedWhenSourceErrors(t *testing.T) {
	ca, err := NewCA()
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := ca.IssueLeaf("peer-1")
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(leaf.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	boom := errors.New("trust source temporarily unavailable")
	verifier := NewPeerVerifierFromTrustSource(func() (*x509.CertPool, error) { return nil, boom }, []string{"peer-1"})
	if _, err := verifier.Verify(cert); err == nil {
		t.Fatal("expected Verify to fail closed when the dynamic trust source itself errors, not fall back to any implicit default")
	}
}

// --- Real end-to-end wire-level proof: SPIFFE identity genuinely
// participates in a live rafttcp TLS handshake, not merely "an SVID
// was issued" -----------------------------------------------------

func TestServerAndClientFromSource_RealHandshakeOverFileBackedWorkloadAPI(t *testing.T) {
	ca, err := NewCA()
	if err != nil {
		t.Fatal(err)
	}
	dirA, dirB := t.TempDir(), t.TempDir()
	leafA := mintLeaf(t, ca, "A", time.Now().Add(-time.Hour), time.Now().Add(time.Hour), true)
	leafB := mintLeaf(t, ca, "B", time.Now().Add(-time.Hour), time.Now().Add(time.Hour), true)
	svidA, keyA, bundleA := writeSVIDFiles(t, dirA, leafA, ca)
	svidB, keyB, bundleB := writeSVIDFiles(t, dirB, leafB, ca)

	srcA := NewFileCertSource(svidA, keyA, bundleA)
	srcB := NewFileCertSource(svidB, keyB, bundleB)
	if err := srcA.Reload(); err != nil {
		t.Fatal(err)
	}
	if err := srcB.Reload(); err != nil {
		t.Fatal(err)
	}

	members := []string{"A", "B"}
	verifierA := NewPeerVerifierFromTrustSource(srcA.CurrentRoots, members)

	node := raftlite.NewNode(raftlite.Config{ID: "A", FSM: nullFSM{}})
	srv := NewServerFromSource("A", node, srcA, verifierA)
	addr, err := srv.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	client := NewClientFromSource("B", srcB, map[string]string{"A": addr})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := client.SendRequestVote(ctx, "A", raftlite.RequestVoteArgs{Term: 1, CandidateID: "B"}); err != nil {
		t.Fatalf("expected a real TLS handshake + RPC using file-backed Workload API identities to succeed: %v", err)
	}
}

func TestClientFromSource_RejectsServerPresentingWrongIdentity(t *testing.T) {
	ca, err := NewCA()
	if err != nil {
		t.Fatal(err)
	}
	dirWrong, dirB := t.TempDir(), t.TempDir()
	// Server is configured to be reached as "A" but its actual SVID
	// says "IMPOSTER" -- the exact "negative identity test" shape.
	leafImposter := mintLeaf(t, ca, "IMPOSTER", time.Now().Add(-time.Hour), time.Now().Add(time.Hour), true)
	leafB := mintLeaf(t, ca, "B", time.Now().Add(-time.Hour), time.Now().Add(time.Hour), true)
	svidWrong, keyWrong, bundleWrong := writeSVIDFiles(t, dirWrong, leafImposter, ca)
	svidB, keyB, bundleB := writeSVIDFiles(t, dirB, leafB, ca)

	srcImposter := NewFileCertSource(svidWrong, keyWrong, bundleWrong)
	srcB := NewFileCertSource(svidB, keyB, bundleB)
	if err := srcImposter.Reload(); err != nil {
		t.Fatal(err)
	}
	if err := srcB.Reload(); err != nil {
		t.Fatal(err)
	}

	node := raftlite.NewNode(raftlite.Config{ID: "IMPOSTER", FSM: nullFSM{}})
	verifier := NewPeerVerifierFromTrustSource(srcImposter.CurrentRoots, []string{"A", "B", "IMPOSTER"})
	srv := NewServerFromSource("IMPOSTER", node, srcImposter, verifier)
	addr, err := srv.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	// Client dials expecting to reach "A" at this address.
	client := NewClientFromSource("B", srcB, map[string]string{"A": addr})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := client.SendRequestVote(ctx, "A", raftlite.RequestVoteArgs{Term: 1, CandidateID: "B"}); err == nil {
		t.Fatal("expected the client to reject a server presenting a SPIFFE identity different from the one it dialed")
	}
}

// TestServerAndClientFromSource_RealSPIREIssuedSVIDs is
// TestServerAndClientFromSource_RealHandshakeOverFileBackedWorkloadAPI's
// real-infrastructure counterpart: instead of a leaf minted in-process
// by this package's own test CA (mintLeaf), it loads SVID/key/bundle
// files actually written to disk by `spire-agent api fetch x509
// -write`, from a REAL, separately-running open-source SPIRE server +
// agent (real node attestation over a real join token, real workload
// registration, real X.509-SVID issuance) -- the literal
// spire-agent-in-the-loop drill the closure matrix names as the
// remaining gap after this package's own FileCertSource/CertSource
// machinery was proven against synthetic certs.
//
// Gated behind VERIQO_SPIRE_LIVE_SVID_DIR (a directory containing
// node-a/{svid.pem,svid_key.pem,bundle.pem} and node-b/{...}, produced
// by a real SPIRE deployment -- see
// evidence/spire_mtls-rafttcp-live-integration.txt for exactly how).
// Unset by default, so ordinary `go test ./...` and CI skip it exactly
// like VERIQO_SCALE_DOCKER_ADDRS and VERIQO_AWS_KMS_INTEGRATION_TEST:
// this is the harness, not a fabricated pass, and a live SPIRE SVID's
// short TTL (minutes, not the years a self-hosted CA can choose) makes
// it unsuitable for CI's own long-lived synthetic-cert tests above.
func TestServerAndClientFromSource_RealSPIREIssuedSVIDs(t *testing.T) {
	dir := os.Getenv("VERIQO_SPIRE_LIVE_SVID_DIR")
	if dir == "" {
		t.Skip("VERIQO_SPIRE_LIVE_SVID_DIR not set -- skipping the real SPIRE-issued-SVID drill (see this test's doc comment)")
	}

	srcA := NewFileCertSource(filepath.Join(dir, "node-a", "svid.pem"), filepath.Join(dir, "node-a", "svid_key.pem"), filepath.Join(dir, "node-a", "bundle.pem"))
	srcB := NewFileCertSource(filepath.Join(dir, "node-b", "svid.pem"), filepath.Join(dir, "node-b", "svid_key.pem"), filepath.Join(dir, "node-b", "bundle.pem"))
	if err := srcA.Reload(); err != nil {
		t.Fatalf("loading real SPIRE-issued SVID for node-a (check VERIQO_SPIRE_LIVE_SVID_DIR is fresh -- real SVIDs expire in minutes): %v", err)
	}
	if err := srcB.Reload(); err != nil {
		t.Fatalf("loading real SPIRE-issued SVID for node-b (check VERIQO_SPIRE_LIVE_SVID_DIR is fresh -- real SVIDs expire in minutes): %v", err)
	}

	members := []string{"node-a", "node-b"}
	verifierA := NewPeerVerifierFromTrustSource(srcA.CurrentRoots, members)

	node := raftlite.NewNode(raftlite.Config{ID: "node-a", FSM: nullFSM{}})
	srv := NewServerFromSource("node-a", node, srcA, verifierA)
	addr, err := srv.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	client := NewClientFromSource("node-b", srcB, map[string]string{"node-a": addr})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := client.SendRequestVote(ctx, "node-a", raftlite.RequestVoteArgs{Term: 1, CandidateID: "node-b"}); err != nil {
		t.Fatalf("expected a real TLS handshake + RPC using REAL SPIRE-issued Workload API identities to succeed: %v", err)
	}
	t.Logf("real mTLS handshake + RequestVote RPC succeeded using SVIDs issued by a live, separately-running SPIRE server")
}
