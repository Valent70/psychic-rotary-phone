package rafttcp

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"path/filepath"
	"testing"
	"time"

	"veriqo/pkg/consensus/raftlite"
)

// parseLeafForTest parses the leaf x509.Certificate out of a
// tls.Certificate returned by CA.IssueLeaf, for tests that need to call
// PeerVerifier.Verify directly rather than through a live TLS handshake.
func parseLeafForTest(leaf tls.Certificate) (*x509.Certificate, error) {
	return x509.ParseCertificate(leaf.Certificate[0])
}

// --- CA persistence: SavePEM / LoadCAPEM round-trip -------------------

func TestCASavePEMAndLoadCAPEMRoundTrip(t *testing.T) {
	ca, err := NewCA()
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}
	dir := t.TempDir()
	certPath := filepath.Join(dir, "ca.crt")
	keyPath := filepath.Join(dir, "ca.key")
	if err := ca.SavePEM(certPath, keyPath); err != nil {
		t.Fatalf("SavePEM: %v", err)
	}

	loaded, err := LoadCAPEM(certPath, keyPath)
	if err != nil {
		t.Fatalf("LoadCAPEM: %v", err)
	}

	// A leaf minted by the loaded CA must be trusted by a verifier
	// built from the ORIGINAL CA object — proving the loaded CA is
	// truly the same root of trust, not a look-alike, which is the
	// entire point SavePEM/LoadCAPEM exist for (see rafttcp.go's own
	// doc comment on the cross-process cluster gap this closes).
	leaf, err := loaded.IssueLeaf("node-x")
	if err != nil {
		t.Fatalf("IssueLeaf from loaded CA: %v", err)
	}
	cert, err := parseLeafForTest(leaf)
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	verifier := NewPeerVerifier(ca, []string{"node-x"})
	id, err := verifier.Verify(cert)
	if err != nil {
		t.Fatalf("expected the original CA to trust a leaf minted by its PEM-round-tripped copy: %v", err)
	}
	if id != "node-x" {
		t.Fatalf("Verify returned id %q, want %q", id, "node-x")
	}
}

func TestLoadCAPEMErrorsOnMissingFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadCAPEM(filepath.Join(dir, "nope.crt"), filepath.Join(dir, "nope.key")); err == nil {
		t.Fatalf("expected an error loading a nonexistent cert file")
	}
	ca, err := NewCA()
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}
	certPath := filepath.Join(dir, "ca.crt")
	keyPath := filepath.Join(dir, "ca.key")
	if err := ca.SavePEM(certPath, keyPath); err != nil {
		t.Fatalf("SavePEM: %v", err)
	}
	if _, err := LoadCAPEM(certPath, filepath.Join(dir, "missing.key")); err == nil {
		t.Fatalf("expected an error loading a nonexistent key file")
	}
}

// --- PeerVerifier.SetMembers: membership changes take effect ---------

func TestPeerVerifierSetMembersUpdatesAuthorization(t *testing.T) {
	ca, err := NewCA()
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}
	leaf, err := ca.IssueLeaf("node-y")
	if err != nil {
		t.Fatalf("IssueLeaf: %v", err)
	}
	cert, err := parseLeafForTest(leaf)
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}

	verifier := NewPeerVerifier(ca, []string{"someone-else"})
	if _, err := verifier.Verify(cert); err == nil {
		t.Fatalf("expected rejection: node-y is not yet a member")
	}

	verifier.SetMembers([]string{"node-y"})
	if _, err := verifier.Verify(cert); err != nil {
		t.Fatalf("expected acceptance after SetMembers added node-y: %v", err)
	}

	verifier.SetMembers([]string{"someone-else"})
	if _, err := verifier.Verify(cert); err == nil {
		t.Fatalf("expected rejection after SetMembers removed node-y again")
	}
}

// --- Real-socket InstallSnapshot / TimeoutNow RPCs, plus dropConn ----

func TestRealSocket_SendInstallSnapshotAndSendTimeoutNow(t *testing.T) {
	ca, err := NewCA()
	if err != nil {
		t.Fatalf("ca: %v", err)
	}
	members := []string{"A", "B"}
	_, srvB, addrB := setupNode(t, ca, "B", members)
	defer srvB.Close()

	leafA, err := ca.IssueLeaf("A")
	if err != nil {
		t.Fatalf("issue leaf A: %v", err)
	}
	client := NewClient("A", ca, leafA, map[string]string{"B": addrB})
	defer client.CloseAll()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	isReply, err := client.SendInstallSnapshot(ctx, "B", raftlite.InstallSnapshotArgs{
		Term: 1, LeaderID: "A", LastIncludedIndex: 0, LastIncludedTerm: 0, Data: []byte("snapshot-bytes"),
	})
	if err != nil {
		t.Fatalf("real-socket SendInstallSnapshot failed: %v", err)
	}
	// B starts at term 0; receiving Term:1 must bump it to follower-at-1
	// and echo that back (raftlite.Node.HandleInstallSnapshot). Term is
	// a uint64, so "not negative" was never a real check (staticcheck
	// SA4003) — the real invariant is that the RPC actually moved B's
	// term, proving this reached the real handler over the real socket.
	if isReply.Term != 1 {
		t.Fatalf("expected InstallSnapshotReply.Term == 1, got %d", isReply.Term)
	}

	tnReply, err := client.SendTimeoutNow(ctx, "B", raftlite.TimeoutNowArgs{Term: 1, LeaderID: "A"})
	if err != nil {
		t.Fatalf("real-socket SendTimeoutNow failed: %v", err)
	}
	_ = tnReply

	// dropConn: force the client to discard its cached connection to
	// B, then issue one more real RPC to prove the client transparently
	// re-dials rather than failing after a drop — the exact situation
	// dropConn exists to handle after any I/O error.
	client.dropConn("B")
	if _, err := client.SendTimeoutNow(ctx, "B", raftlite.TimeoutNowArgs{Term: 1, LeaderID: "A"}); err != nil {
		t.Fatalf("expected transparent re-dial after dropConn, got: %v", err)
	}
}

func TestSendInstallSnapshotUnknownTargetErrors(t *testing.T) {
	ca, err := NewCA()
	if err != nil {
		t.Fatalf("ca: %v", err)
	}
	leafA, err := ca.IssueLeaf("A")
	if err != nil {
		t.Fatalf("issue leaf: %v", err)
	}
	client := NewClient("A", ca, leafA, map[string]string{})
	defer client.CloseAll()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.SendInstallSnapshot(ctx, "ghost", raftlite.InstallSnapshotArgs{}); err == nil {
		t.Fatalf("expected an error sending to a target with no known address")
	}
}
