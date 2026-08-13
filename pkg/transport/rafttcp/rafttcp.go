// Package rafttcp is a real TCP+mTLS transport implementing
// raftlite.Transport — the network layer the gap audit's §2 asked for.
// It uses a self-hosted CA minting SPIFFE-format identities
// (spiffe://veriqo.global/node/<id>) rather than a real SPIRE server
// binary, which this sandbox cannot fetch (no network path to a SPIRE
// distribution). See the accompanying report §5 for the honest scope note.
package rafttcp

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/gob"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"sync"
	"time"

	"veriqo/pkg/consensus/raftlite"
)

// ---- Certificate authority (stands in for a real SPIRE Server) ----

type CA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	der  []byte
}

func NewCA() (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Veriqo Root CA"},
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
	return &CA{cert: cert, key: key, der: der}, nil
}

func (ca *CA) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca.cert)
	return p
}

// SavePEM writes the CA's certificate and private key to two PEM files so
// every process in a real multi-OS-process cluster can load the *same*
// root of trust. This is the fix for the gap that made cmd/veriqo-node
// unable to form a live cluster before this session: each process
// previously called NewCA() independently, so every node minted its own
// root and no two processes could ever validate each other's leaf certs.
func (ca *CA) SavePEM(certPath, keyPath string) error {
	certOut, err := os.Create(certPath) // #nosec G304 -- certPath is an operator-supplied CLI argument, not untrusted input
	if err != nil {
		return fmt.Errorf("rafttcp: create cert file: %w", err)
	}
	defer certOut.Close()
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: ca.der}); err != nil {
		return fmt.Errorf("rafttcp: encode cert pem: %w", err)
	}

	keyBytes, err := x509.MarshalECPrivateKey(ca.key)
	if err != nil {
		return fmt.Errorf("rafttcp: marshal ca key: %w", err)
	}
	keyOut, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) // #nosec G304 -- keyPath is an operator-supplied CLI argument, not untrusted input
	if err != nil {
		return fmt.Errorf("rafttcp: create key file: %w", err)
	}
	defer keyOut.Close()
	if err := pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes}); err != nil {
		return fmt.Errorf("rafttcp: encode key pem: %w", err)
	}
	return nil
}

// LoadCAPEM reads a CA previously written by SavePEM, so a fleet of
// independently-started OS processes can share one root of trust — the
// concrete precondition for a real live multi-process raft cluster.
func LoadCAPEM(certPath, keyPath string) (*CA, error) {
	certPEM, err := os.ReadFile(certPath) // #nosec G304 -- certPath is an operator-supplied CLI argument, not untrusted input
	if err != nil {
		return nil, fmt.Errorf("rafttcp: read cert file: %w", err)
	}
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, errors.New("rafttcp: no PEM block found in cert file")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("rafttcp: parse ca cert: %w", err)
	}

	keyPEM, err := os.ReadFile(keyPath) // #nosec G304 -- keyPath is an operator-supplied CLI argument, not untrusted input
	if err != nil {
		return nil, fmt.Errorf("rafttcp: read key file: %w", err)
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, errors.New("rafttcp: no PEM block found in key file")
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("rafttcp: parse ca key: %w", err)
	}

	return &CA{cert: cert, key: key, der: certBlock.Bytes}, nil
}

// IssueLeaf mints a leaf cert whose SAN URI is the node's SPIFFE-format
// identity — the same identity contract a real SPIRE Workload API issues.
func (ca *CA) IssueLeaf(nodeID string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	spiffeID, err := url.Parse(fmt.Sprintf("spiffe://veriqo.global/node/%s", nodeID))
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: nodeID},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		URIs:         []*url.URL{spiffeID},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der, ca.der}, PrivateKey: key}, nil
}

// ExtractSPIFFEID reads the SPIFFE URI SAN from a verified peer cert.
func ExtractSPIFFEID(cert *x509.Certificate) (string, error) {
	for _, u := range cert.URIs {
		if u.Scheme == "spiffe" {
			return u.String(), nil
		}
	}
	return "", errors.New("rafttcp: no SPIFFE URI SAN present")
}

// ---- Wire protocol ----

type msgType byte

const (
	msgRequestVote msgType = iota
	msgAppendEntries
	msgAuthAck
	msgInstallSnapshot
	msgPreVote
	msgTimeoutNow
)

type envelope struct {
	Type msgType
	RV   raftlite.RequestVoteArgs
	RVR  raftlite.RequestVoteReply
	AE   raftlite.AppendEntriesArgs
	AER  raftlite.AppendEntriesReply
	IS   raftlite.InstallSnapshotArgs
	ISR  raftlite.InstallSnapshotReply
	PV   raftlite.RequestVoteArgs
	PVR  raftlite.RequestVoteReply
	TN   raftlite.TimeoutNowArgs
	TNR  raftlite.TimeoutNowReply
}

// ---- PeerVerifier: authorize by SPIFFE identity + membership, not DNS ----

type PeerVerifier struct {
	ca      *CA
	members map[string]bool
	mu      sync.RWMutex
}

func NewPeerVerifier(ca *CA, members []string) *PeerVerifier {
	m := make(map[string]bool, len(members))
	for _, id := range members {
		m[id] = true
	}
	return &PeerVerifier{ca: ca, members: m}
}

func (v *PeerVerifier) SetMembers(members []string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.members = make(map[string]bool, len(members))
	for _, id := range members {
		v.members[id] = true
	}
}

// Verify independently re-checks the chain against the CA pool (Go's TLS
// stack already did this during the handshake when ClientAuth is
// RequireAndVerifyClientCert — this is a defense-in-depth second check by
// identity + cluster membership, not hostname, since nodes have no
// meaningful DNS names in this deployment shape) and returns the peer's
// node ID extracted from its SPIFFE SAN.
func (v *PeerVerifier) Verify(cert *x509.Certificate) (string, error) {
	opts := x509.VerifyOptions{Roots: v.ca.Pool(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}
	if _, err := cert.Verify(opts); err != nil {
		return "", fmt.Errorf("rafttcp: chain verification failed: %w", err)
	}
	spiffeID, err := ExtractSPIFFEID(cert)
	if err != nil {
		return "", err
	}
	u, _ := url.Parse(spiffeID)
	nodeID := ""
	if u != nil {
		parts := u.Path // "/node/<id>"
		const prefix = "/node/"
		if len(parts) > len(prefix) && parts[:len(prefix)] == prefix {
			nodeID = parts[len(prefix):]
		}
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	if !v.members[nodeID] {
		return "", fmt.Errorf("rafttcp: node %q authenticated but not in cluster membership", nodeID)
	}
	return nodeID, nil
}

// ---- Server: accepts inbound raftlite RPCs over mTLS ----

type Server struct {
	id       string
	node     *raftlite.Node
	verifier *PeerVerifier
	tlsConf  *tls.Config
	ln       net.Listener
	mu       sync.Mutex
	closed   bool
}

func NewServer(id string, node *raftlite.Node, ca *CA, leafCert tls.Certificate, verifier *PeerVerifier) *Server {
	tlsConf := &tls.Config{
		Certificates: []tls.Certificate{leafCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    ca.Pool(),
		MinVersion:   tls.VersionTLS12,
		// SPIFFE identity is re-derived from the peer cert on every
		// connection (see Client.getConn's VerifyPeerCertificate); a
		// resumed TLS session must not let a peer skip that re-check, so
		// session tickets are disabled rather than trusted to always
		// invoke the verification callback (gosec G123).
		SessionTicketsDisabled: true,
	}
	return &Server{id: id, node: node, verifier: verifier, tlsConf: tlsConf}
}

// Listen binds to addr (use "127.0.0.1:0" for an ephemeral test port) and
// returns the actual address bound, so tests can wire peers together.
func (s *Server) Listen(addr string) (string, error) {
	ln, err := tls.Listen("tcp", addr, s.tlsConf)
	if err != nil {
		return "", err
	}
	s.ln = ln
	go s.acceptLoop()
	return ln.Addr().String(), nil
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()
	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		return
	}
	if err := tlsConn.Handshake(); err != nil {
		return
	}
	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return
	}
	peerID, err := s.verifier.Verify(state.PeerCertificates[0])
	if err != nil {
		return // rejected: rogue CA already failed handshake; here it's a valid-CA-but-not-member case
	}
	_ = peerID

	// Explicit application-level auth-ack: fixes the real TLS 1.3 handshake
	// race found during testing, where a client's Handshake() can return
	// successfully before the server finishes validating the client cert
	// (the client's Finished message is the last flight message on the
	// wire). Without this, a rejected client could believe Send() succeeded.
	if _, err := conn.Write([]byte{byte(msgAuthAck)}); err != nil {
		return
	}

	dec := gob.NewDecoder(bufio.NewReader(conn))
	enc := gob.NewEncoder(conn)
	for {
		var env envelope
		if err := dec.Decode(&env); err != nil {
			return
		}
		var reply envelope
		switch env.Type {
		case msgRequestVote:
			reply = envelope{Type: msgRequestVote, RVR: s.node.HandleRequestVote(env.RV)}
		case msgAppendEntries:
			reply = envelope{Type: msgAppendEntries, AER: s.node.HandleAppendEntries(env.AE)}
		case msgInstallSnapshot:
			reply = envelope{Type: msgInstallSnapshot, ISR: s.node.HandleInstallSnapshot(env.IS)}
		case msgPreVote:
			reply = envelope{Type: msgPreVote, PVR: s.node.HandlePreVote(env.PV)}
		case msgTimeoutNow:
			reply = envelope{Type: msgTimeoutNow, TNR: s.node.HandleTimeoutNow(env.TN)}
		default:
			return
		}
		if err := enc.Encode(&reply); err != nil {
			return
		}
	}
}

func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.ln != nil {
		return s.ln.Close()
	}
	return nil
}

// ---- Client / Transport: dials peers, pools connections ----

type Client struct {
	selfID   string
	ca       *CA
	leafCert tls.Certificate
	addrs    map[string]string // peer ID -> "host:port"

	mu    sync.Mutex
	conns map[string]*peerConn
}

type peerConn struct {
	conn net.Conn
	enc  *gob.Encoder
	dec  *gob.Decoder
	mu   sync.Mutex
}

// NewClient takes an independent copy of addrs. This matters: it is
// natural for a caller to build one addrs map and pass it to several
// Clients (one per local node) — see rafttcp/cluster_test.go — which
// would otherwise alias the same underlying map across Clients that
// each have their OWN mutex, silently defeating SetAddr's locking
// (found as a real data race via -race in this session).
func NewClient(selfID string, ca *CA, leafCert tls.Certificate, addrs map[string]string) *Client {
	owned := make(map[string]string, len(addrs))
	for k, v := range addrs {
		owned[k] = v
	}
	return &Client{selfID: selfID, ca: ca, leafCert: leafCert, addrs: owned, conns: make(map[string]*peerConn)}
}

// SetAddr re-points a peer ID at a new address — used when a peer
// process restarts and comes back up on a different socket. Drops any
// pooled connection to the old address so the next call dials fresh.
func (c *Client) SetAddr(target, addr string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.addrs[target] = addr
	if pc, ok := c.conns[target]; ok {
		_ = pc.conn.Close() // best-effort; we're discarding this pooled conn regardless
		delete(c.conns, target)
	}
}

func (c *Client) getConn(target string) (*peerConn, error) {
	c.mu.Lock()
	if pc, ok := c.conns[target]; ok {
		c.mu.Unlock()
		return pc, nil
	}
	c.mu.Unlock()

	c.mu.Lock()
	addr, ok := c.addrs[target]
	c.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("rafttcp: unknown peer %q", target)
	}
	// Nodes have no meaningful DNS names in this deployment shape, so we
	// skip Go's built-in hostname-based verification (InsecureSkipVerify)
	// and instead perform explicit chain + SPIFFE-identity verification in
	// VerifyPeerCertificate — checking the server's cert chains to our CA
	// AND that its SPIFFE SAN matches the specific peer we dialed, which is
	// strictly stronger than hostname matching for this identity model.
	wantSPIFFE := fmt.Sprintf("spiffe://veriqo.global/node/%s", target)
	tlsConf := &tls.Config{
		Certificates:       []tls.Certificate{c.leafCert},
		RootCAs:            c.ca.Pool(),
		InsecureSkipVerify: true, // #nosec G402 -- intentional -- VerifyPeerCertificate below performs real chain + SPIFFE-identity verification in its place; see comment above
		MinVersion:         tls.VersionTLS12,
		// A resumed session must still hit VerifyPeerCertificate; do not
		// let ticket resumption bypass the SPIFFE identity check
		// (gosec G123).
		SessionTicketsDisabled: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return errors.New("rafttcp: no peer certificate presented")
			}
			leaf, err := x509.ParseCertificate(rawCerts[0])
			if err != nil {
				return err
			}
			opts := x509.VerifyOptions{Roots: c.ca.Pool(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}
			if _, err := leaf.Verify(opts); err != nil {
				return fmt.Errorf("rafttcp: server chain verification failed: %w", err)
			}
			spiffeID, err := ExtractSPIFFEID(leaf)
			if err != nil {
				return err
			}
			if spiffeID != wantSPIFFE {
				return fmt.Errorf("rafttcp: expected peer identity %q, got %q", wantSPIFFE, spiffeID)
			}
			return nil
		},
	}
	conn, err := tls.Dial("tcp", addr, tlsConf)
	if err != nil {
		return nil, err
	}
	// Wait for the explicit auth-ack byte before pooling — closes the
	// TLS 1.3 handshake-completion race described in Server.handleConn.
	ackBuf := make([]byte, 1)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second)) // best-effort; a failure surfaces as a Read timeout/error below regardless
	if _, err := conn.Read(ackBuf); err != nil || msgType(ackBuf[0]) != msgAuthAck {
		_ = conn.Close() // best-effort; the auth-ack rejection below is what matters
		return nil, fmt.Errorf("rafttcp: peer %q rejected connection (no auth-ack)", target)
	}
	_ = conn.SetReadDeadline(time.Time{}) // best-effort; clearing the deadline is not on any error-recovery path

	pc := &peerConn{conn: conn, enc: gob.NewEncoder(conn), dec: gob.NewDecoder(bufio.NewReader(conn))}
	c.mu.Lock()
	c.conns[target] = pc
	c.mu.Unlock()
	return pc, nil
}

func (c *Client) roundTrip(target string, req envelope) (envelope, error) {
	pc, err := c.getConn(target)
	if err != nil {
		return envelope{}, err
	}
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if err := pc.enc.Encode(&req); err != nil {
		c.dropConn(target)
		return envelope{}, err
	}
	var reply envelope
	if err := pc.dec.Decode(&reply); err != nil {
		c.dropConn(target)
		return envelope{}, err
	}
	return reply, nil
}

func (c *Client) dropConn(target string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if pc, ok := c.conns[target]; ok {
		_ = pc.conn.Close() // best-effort; we're discarding this pooled conn regardless
		delete(c.conns, target)
	}
}

func (c *Client) SendRequestVote(ctx context.Context, target string, args raftlite.RequestVoteArgs) (raftlite.RequestVoteReply, error) {
	reply, err := c.roundTrip(target, envelope{Type: msgRequestVote, RV: args})
	return reply.RVR, err
}

func (c *Client) SendAppendEntries(ctx context.Context, target string, args raftlite.AppendEntriesArgs) (raftlite.AppendEntriesReply, error) {
	reply, err := c.roundTrip(target, envelope{Type: msgAppendEntries, AE: args})
	return reply.AER, err
}

func (c *Client) SendInstallSnapshot(ctx context.Context, target string, args raftlite.InstallSnapshotArgs) (raftlite.InstallSnapshotReply, error) {
	reply, err := c.roundTrip(target, envelope{Type: msgInstallSnapshot, IS: args})
	return reply.ISR, err
}

func (c *Client) SendPreVote(ctx context.Context, target string, args raftlite.RequestVoteArgs) (raftlite.RequestVoteReply, error) {
	reply, err := c.roundTrip(target, envelope{Type: msgPreVote, PV: args})
	return reply.PVR, err
}

func (c *Client) SendTimeoutNow(ctx context.Context, target string, args raftlite.TimeoutNowArgs) (raftlite.TimeoutNowReply, error) {
	reply, err := c.roundTrip(target, envelope{Type: msgTimeoutNow, TN: args})
	return reply.TNR, err
}

func (c *Client) CloseAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, pc := range c.conns {
		_ = pc.conn.Close() // best-effort; we're tearing down the whole pool
		delete(c.conns, id)
	}
}
