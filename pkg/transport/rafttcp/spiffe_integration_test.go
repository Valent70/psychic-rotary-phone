// This file proves VERIQO's real Raft transport (pkg/transport/rafttcp)
// actually uses SPIFFE identity, not merely that SVID issuance/validation
// works in isolation (that isolated qualification already lives in
// pkg/blockers/spiffe). Every test here dials or accepts a real TCP
// connection secured by a real crypto/tls handshake using certificates
// minted by spiffe.TestIdentityProvider, and every negative case proves
// the live handshake/connection attempt itself is rejected -- not just
// that a standalone validation function returns an error.
//
// Bridging mechanism: rafttcp.NewServer/NewClient already accept an
// externally-supplied tls.Certificate as a plain parameter (see
// rafttcp.go) -- that hook already existed and needed no change. The
// only piece rafttcp did not expose a way to build was a *rafttcp.CA
// from certificate/key material that did not come from rafttcp's own
// NewCA(); grepping this package before writing this file confirmed
// the only two ways to obtain a *rafttcp.CA are NewCA() (mints a fresh
// root, no external material accepted) and LoadCAPEM(certPath, keyPath)
// (reads a CA cert+key from PEM files -- format-agnostic about who
// produced them). LoadCAPEM is therefore an already-pluggable hook, so
// this file uses it as-is: spiffe.TestIdentityProvider gained two new,
// additive export methods (CACertPEM/CAKeyPEM) purely to hand its root
// to that existing hook via PEM files written to t.TempDir(), and one
// more (TLSCertificate) to turn a minted SVID into the tls.Certificate
// shape NewServer/NewClient already accepted. No existing rafttcp
// signature changed and no new optional Config field was needed.
package rafttcp

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"veriqo/pkg/blockers/spiffe"
	"veriqo/pkg/consensus/raftlite"
)

// spiffeCAToRafttcpCA writes provider's self-signed root (cert + key)
// to PEM files under t.TempDir() and loads them back through rafttcp's
// own real LoadCAPEM, producing a *rafttcp.CA whose Pool() is exactly
// this SPIFFE provider's trust bundle -- the real hook described in
// this file's header comment, exercised for real rather than assumed.
func spiffeCAToRafttcpCA(t *testing.T, provider *spiffe.TestIdentityProvider) *CA {
	t.Helper()
	dir := t.TempDir()
	certPath := filepath.Join(dir, "ca-cert.pem")
	keyPath := filepath.Join(dir, "ca-key.pem")

	if err := os.WriteFile(certPath, provider.CACertPEM(), 0o600); err != nil {
		t.Fatalf("write ca cert pem: %v", err)
	}
	keyPEM, err := provider.CAKeyPEM()
	if err != nil {
		t.Fatalf("CAKeyPEM: %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write ca key pem: %v", err)
	}

	ca, err := LoadCAPEM(certPath, keyPath)
	if err != nil {
		t.Fatalf("LoadCAPEM: %v", err)
	}
	return ca
}

// spiffeLeaf mints a real SPIFFE SVID for nodeID (in the
// spiffe://veriqo.global/node/<id> shape rafttcp's own Client dial-side
// verification expects) and returns it as a crypto/tls.Certificate via
// the new TLSCertificate bridge method.
func spiffeLeaf(t *testing.T, provider *spiffe.TestIdentityProvider, nodeID string, ttl time.Duration) tls.Certificate {
	t.Helper()
	svid, err := provider.IssueWithTTL(fmt.Sprintf("spiffe://veriqo.global/node/%s", nodeID), ttl)
	if err != nil {
		t.Fatalf("IssueWithTTL(%s): %v", nodeID, err)
	}
	leaf, err := provider.TLSCertificate(svid)
	if err != nil {
		t.Fatalf("TLSCertificate(%s): %v", nodeID, err)
	}
	return leaf
}

// TestSPIFFEIdentitySecuresRealMTLSRaftConnection is the positive
// proof: two real rafttcp endpoints, secured entirely by certificates
// minted by spiffe.TestIdentityProvider (not rafttcp's own NewCA()),
// complete a real TLS handshake over a real TCP socket and exchange a
// real raftlite RPC (RequestVote) end to end.
func TestSPIFFEIdentitySecuresRealMTLSRaftConnection(t *testing.T) {
	provider, err := spiffe.NewTestIdentityProvider("veriqo.global")
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	ca := spiffeCAToRafttcpCA(t, provider)

	members := []string{"A", "B"}
	nodeB := raftlite.NewNode(raftlite.Config{ID: "B", FSM: nullFSM{}})
	leafB := spiffeLeaf(t, provider, "B", time.Hour)
	verifier := NewPeerVerifier(ca, members)
	srvB := NewServer("B", nodeB, ca, leafB, verifier)
	addrB, err := srvB.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer srvB.Close()

	leafA := spiffeLeaf(t, provider, "A", time.Hour)
	client := NewClient("A", ca, leafA, map[string]string{"B": addrB})
	defer client.CloseAll()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	reply, err := client.SendRequestVote(ctx, "B", raftlite.RequestVoteArgs{Term: 1, CandidateID: "A"})
	if err != nil {
		t.Fatalf("real mTLS RPC secured by SPIFFE-minted identity failed: %v", err)
	}
	if !reply.VoteGranted {
		t.Fatalf("expected vote granted, got %+v", reply)
	}

	// AppendEntries too, for good measure -- the heartbeat RPC rafttcp's
	// own package doc calls out as the transport's steady-state traffic.
	aeReply, err := client.SendAppendEntries(ctx, "B", raftlite.AppendEntriesArgs{Term: 1, LeaderID: "A"})
	if err != nil {
		t.Fatalf("real mTLS AppendEntries secured by SPIFFE-minted identity failed: %v", err)
	}
	if !aeReply.Success {
		t.Fatalf("expected AppendEntries success, got %+v", aeReply)
	}
}

// TestSPIFFEExpiredSVID_ConnectionRejected proves a live TLS dial using
// an already-expired SPIFFE SVID fails the actual handshake (Go's own
// x509 expiry check inside crypto/tls, exercised for real), not merely
// that spiffe.ValidateSVID would return an error if called standalone.
func TestSPIFFEExpiredSVID_ConnectionRejected(t *testing.T) {
	provider, err := spiffe.NewTestIdentityProvider("veriqo.global")
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	ca := spiffeCAToRafttcpCA(t, provider)

	members := []string{"A", "B"}
	nodeB := raftlite.NewNode(raftlite.Config{ID: "B", FSM: nullFSM{}})
	leafB := spiffeLeaf(t, provider, "B", time.Hour)
	srvB := NewServer("B", nodeB, ca, leafB, NewPeerVerifier(ca, members))
	addrB, err := srvB.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer srvB.Close()

	expiredLeafA := spiffeLeaf(t, provider, "A", -time.Hour) // already expired
	client := NewClient("A", ca, expiredLeafA, map[string]string{"B": addrB})
	defer client.CloseAll()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = client.SendRequestVote(ctx, "B", raftlite.RequestVoteArgs{Term: 1, CandidateID: "A"})
	if err == nil {
		t.Fatal("expected the real TLS handshake to reject an expired SPIFFE-minted client certificate, got success")
	}
}

// TestSPIFFERevokedSVID_ConnectionRejected proves a live connection
// using an SVID the SPIFFE provider has revoked is refused end to end
// once rafttcp's PeerVerifier is wired to the CRL rafttcp already
// supports (revocation.go / P1-06) -- i.e. revocation, not just chain
// validity, actually gates a real socket.
//
// spiffe.TestIdentityProvider.Revoke tracks revocation by the SVID's
// own serial (a decimal string of the certificate's SerialNumber); the
// same *big.Int serial is what rafttcp.RevocationList.Revoke expects,
// so this test revokes the identical serial on rafttcp's own CRL --
// proving the two systems agree on what "this leaf" means, which is
// exactly the claim under test.
func TestSPIFFERevokedSVID_ConnectionRejected(t *testing.T) {
	provider, err := spiffe.NewTestIdentityProvider("veriqo.global")
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	ca := spiffeCAToRafttcpCA(t, provider)

	members := []string{"A", "B"}
	nodeB := raftlite.NewNode(raftlite.Config{ID: "B", FSM: nullFSM{}})
	leafB := spiffeLeaf(t, provider, "B", time.Hour)
	verifier := NewPeerVerifier(ca, members)
	crl := NewRevocationList(ca)
	verifier.SetRevocationList(crl)
	srvB := NewServer("B", nodeB, ca, leafB, verifier)
	addrB, err := srvB.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer srvB.Close()

	svidA, err := provider.IssueWithTTL("spiffe://veriqo.global/node/A", time.Hour)
	if err != nil {
		t.Fatalf("issue A: %v", err)
	}
	leafA, err := provider.TLSCertificate(svidA)
	if err != nil {
		t.Fatalf("TLSCertificate: %v", err)
	}
	provider.Revoke(svidA.Serial) // revoked on the SPIFFE side...
	crl.Revoke(svidA.Cert.SerialNumber, "revoked for integration test")

	client := NewClient("A", ca, leafA, map[string]string{"B": addrB})
	defer client.CloseAll()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = client.SendRequestVote(ctx, "B", raftlite.RequestVoteArgs{Term: 1, CandidateID: "A"})
	if err == nil {
		t.Fatal("expected the real connection to be rejected for a revoked SPIFFE-minted client certificate, got success")
	}
}

// TestSPIFFEWrongTrustDomain_ConnectionRejected proves a certificate
// minted by a second, unrelated spiffe.TestIdentityProvider (its own
// independent self-signed root -- a different trust domain, not a
// reused CA) is rejected by the live TLS handshake against a server
// whose ClientCAs pool is the FIRST provider's root only.
func TestSPIFFEWrongTrustDomain_ConnectionRejected(t *testing.T) {
	legit, err := spiffe.NewTestIdentityProvider("veriqo.global")
	if err != nil {
		t.Fatalf("new legit provider: %v", err)
	}
	legitCA := spiffeCAToRafttcpCA(t, legit)

	members := []string{"A", "B"}
	nodeB := raftlite.NewNode(raftlite.Config{ID: "B", FSM: nullFSM{}})
	leafB := spiffeLeaf(t, legit, "B", time.Hour)
	srvB := NewServer("B", nodeB, legitCA, leafB, NewPeerVerifier(legitCA, members))
	addrB, err := srvB.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer srvB.Close()

	// A second, wholly independent SPIFFE identity provider -- a
	// different self-signed root, standing in for a foreign SPIRE trust
	// domain.
	foreign, err := spiffe.NewTestIdentityProvider("attacker.example")
	if err != nil {
		t.Fatalf("new foreign provider: %v", err)
	}
	foreignCA := spiffeCAToRafttcpCA(t, foreign)
	foreignLeafA := spiffeLeaf(t, foreign, "A", time.Hour)

	// The client dials using the FOREIGN CA's pool as its own RootCAs
	// (a real attacker would also not have the legitimate root), but
	// presents a client cert that the legitimate server (srvB, whose
	// ClientCAs is legitCA's pool) will reject because it does not
	// chain to legitCA.
	client := NewClient("A", foreignCA, foreignLeafA, map[string]string{"B": addrB})
	defer client.CloseAll()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = client.SendRequestVote(ctx, "B", raftlite.RequestVoteArgs{Term: 1, CandidateID: "A"})
	if err == nil {
		t.Fatal("expected a real connection from a foreign SPIFFE trust domain to be rejected, got success")
	}
}

// TestSPIFFEWrongIdentity_ConnectionRejected proves the live handshake
// is rejected when the peer presents a genuine, unexpired, trusted
// SPIFFE certificate -- just not for the SPIFFE ID the dialing client
// actually expects to be talking to. This exercises rafttcp's own
// VerifyPeerCertificate identity check (client dial-side, rafttcp.go)
// for real: server "B" answers with a valid cert for node "C" instead
// of "B" (e.g. the address table pointing at the wrong peer, or a
// man-in-the-middle answering in B's place).
func TestSPIFFEWrongIdentity_ConnectionRejected(t *testing.T) {
	provider, err := spiffe.NewTestIdentityProvider("veriqo.global")
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	ca := spiffeCAToRafttcpCA(t, provider)

	// The server listening on this socket answers as node "B" at the
	// transport layer, but is handed a leaf certificate for "C" -- a
	// genuine, CA-trusted, cluster-member identity, just the wrong one
	// for who the client thinks it is dialing.
	members := []string{"A", "B", "C"}
	nodeB := raftlite.NewNode(raftlite.Config{ID: "B", FSM: nullFSM{}})
	wrongLeaf := spiffeLeaf(t, provider, "C", time.Hour)
	srvB := NewServer("B", nodeB, ca, wrongLeaf, NewPeerVerifier(ca, members))
	addrB, err := srvB.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer srvB.Close()

	leafA := spiffeLeaf(t, provider, "A", time.Hour)
	client := NewClient("A", ca, leafA, map[string]string{"B": addrB}) // dials "B", expects spiffe://veriqo.global/node/B
	defer client.CloseAll()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = client.SendRequestVote(ctx, "B", raftlite.RequestVoteArgs{Term: 1, CandidateID: "A"})
	if err == nil {
		t.Fatal("expected the real connection to be rejected when the peer's SPIFFE identity does not match the expected target, got success")
	}
}

// TestSPIFFECertKeyMismatch_ConnectionRejected proves a leaf whose
// public key does not match the private key presented alongside it
// (a corrupted/tampered keypair -- constructed here by pairing a real
// SPIFFE-minted certificate with an unrelated freshly generated
// private key) fails the real TLS handshake, since TLS itself must
// prove possession of the certified key during the handshake
// signature exchange.
func TestSPIFFECertKeyMismatch_ConnectionRejected(t *testing.T) {
	provider, err := spiffe.NewTestIdentityProvider("veriqo.global")
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	ca := spiffeCAToRafttcpCA(t, provider)

	members := []string{"A", "B"}
	nodeB := raftlite.NewNode(raftlite.Config{ID: "B", FSM: nullFSM{}})
	leafB := spiffeLeaf(t, provider, "B", time.Hour)
	srvB := NewServer("B", nodeB, ca, leafB, NewPeerVerifier(ca, members))
	addrB, err := srvB.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer srvB.Close()

	svidA, err := provider.IssueWithTTL("spiffe://veriqo.global/node/A", time.Hour)
	if err != nil {
		t.Fatalf("issue A: %v", err)
	}
	// Pair the real, CA-signed certificate for "A" with a DIFFERENT,
	// unrelated private key -- the cert's embedded public key no longer
	// matches this private key.
	mismatchedKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate mismatched key: %v", err)
	}
	mismatchedLeaf := tls.Certificate{
		Certificate: [][]byte{svidA.Cert.Raw},
		PrivateKey:  mismatchedKey,
	}

	client := NewClient("A", ca, mismatchedLeaf, map[string]string{"B": addrB})
	defer client.CloseAll()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = client.SendRequestVote(ctx, "B", raftlite.RequestVoteArgs{Term: 1, CandidateID: "A"})
	if err == nil {
		t.Fatal("expected the real TLS handshake to reject a certificate/private-key mismatch, got success")
	}
}

// sanity check that this file's helper builds a genuinely different
// self-signed root per provider instance (guards against a future
// refactor of NewTestIdentityProvider accidentally sharing state that
// would silently invalidate TestSPIFFEWrongTrustDomain_ConnectionRejected).
func TestHelperProvidersHaveDistinctRoots(t *testing.T) {
	p1, err := spiffe.NewTestIdentityProvider("veriqo.global")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := spiffe.NewTestIdentityProvider("veriqo.global")
	if err != nil {
		t.Fatal(err)
	}
	c1 := spiffeCAToRafttcpCA(t, p1)
	c2 := spiffeCAToRafttcpCA(t, p2)
	if c1.cert.SerialNumber.Cmp(c2.cert.SerialNumber) == 0 && sameKey(c1.cert, c2.cert) {
		t.Fatal("expected two independently-minted providers to have distinct roots")
	}
}

func sameKey(a, b *x509.Certificate) bool {
	ap, aok := a.PublicKey.(*ecdsa.PublicKey)
	bp, bok := b.PublicKey.(*ecdsa.PublicKey)
	if !aok || !bok {
		return false
	}
	return ap.Equal(bp)
}
