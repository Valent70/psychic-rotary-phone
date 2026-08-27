// Package transport defines the Veriqo Raft network transport interface.
// This layer sits between the Raft core (pkg/consensus/raft) and the
// network (gRPC + mTLS). The actual gRPC implementation is a concrete
// adapter that plugs in via the Transport interface without changing
// any Raft core code.
//
// Architecture:
//
//	Raft Node (pkg/consensus/raft)
//	      ↓ Transport interface
//	┌─────────────────────────────────┐
//	│ SimTransport (in-process)       │  ← chaos tests / unit tests
//	│ GRPCTransport (over network)    │  ← production; to be implemented
//	└─────────────────────────────────┘
//
// The GRPCTransport requires:
//   - A SPIFFE/SPIRE-issued x509 SVID for the local node.
//   - Peer SVIDs validated against the trust bundle.
//   - All messages encrypted end-to-end via mTLS.
//
// This package provides:
//  1. The NodeAddress registry.
//  2. The Dialer and Listener abstractions.
//  3. A TLSConfig builder (SPIFFE-compatible, OWASP-aligned).
//  4. A connection pool for outbound gRPC connections.
//  5. Stubs for GRPCTransport (implementation wires in real gRPC client).
package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"veriqo/pkg/core"
)

// ─── Address registry ─────────────────────────────────────────────────────────

// NodeAddress holds the network address for a Raft node.
type NodeAddress struct {
	ID   core.NodeID
	Host string
	Port int
	// SpiffeID is the SPIFFE URI expected in this node's mTLS certificate.
	// e.g. "spiffe://veriqo.global/node/raft-1"
	SpiffeID string
}

// Addr returns the host:port string.
func (a NodeAddress) Addr() string {
	return fmt.Sprintf("%s:%d", a.Host, a.Port)
}

// AddressBook maps node IDs to their network addresses.
type AddressBook struct {
	mu    sync.RWMutex
	addrs map[core.NodeID]NodeAddress
}

// NewAddressBook creates an empty address book.
func NewAddressBook() *AddressBook {
	return &AddressBook{addrs: make(map[core.NodeID]NodeAddress)}
}

// Register adds or updates an address.
func (b *AddressBook) Register(addr NodeAddress) {
	b.mu.Lock()
	b.addrs[addr.ID] = addr
	b.mu.Unlock()
}

// Remove deletes an address entry.
func (b *AddressBook) Remove(id core.NodeID) {
	b.mu.Lock()
	delete(b.addrs, id)
	b.mu.Unlock()
}

// Lookup returns the address for a node, or an error if not found.
func (b *AddressBook) Lookup(id core.NodeID) (NodeAddress, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	a, ok := b.addrs[id]
	if !ok {
		return NodeAddress{}, fmt.Errorf("transport: address for node %q not found", id)
	}
	return a, nil
}

// All returns all registered addresses sorted by node ID.
func (b *AddressBook) All() []NodeAddress {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]NodeAddress, 0, len(b.addrs))
	for _, a := range b.addrs {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ─── Message type ─────────────────────────────────────────────────────────────

// MessageType classifies a Raft wire message.
type MessageType uint8

const (
	MsgVoteRequest MessageType = iota + 1
	MsgVoteResponse
	MsgAppendEntries
	MsgAppendResponse
	MsgInstallSnapshot
	MsgSnapshotResponse
	MsgHeartbeat
	MsgReadIndex
	MsgReadIndexResponse
	MsgConfChange
	MsgLeadershipTransfer
)

// Message is a transport-layer envelope for any Raft RPC.
// It mirrors the structure in api/proto/raft.proto.
type Message struct {
	From    core.NodeID
	To      core.NodeID
	Term    uint64
	Type    MessageType
	Payload []byte // serialised Raft message body
	SentAt  time.Time
}

// ─── Transport interface ──────────────────────────────────────────────────────

// Transport is the network abstraction for the Raft core.
// raft.Node only knows this interface — it never touches gRPC or TLS.
type Transport interface {
	// Send delivers a message to the target node.
	// Implementations must be non-blocking: return quickly and buffer internally.
	Send(ctx context.Context, msg Message) error

	// Broadcast delivers a message to multiple nodes.
	Broadcast(ctx context.Context, msgs []Message) error

	// Receive returns a channel that yields incoming messages.
	Receive() <-chan Message

	// Close tears down all connections and goroutines.
	Close() error
}

// ─── TLS configuration builder ────────────────────────────────────────────────

// TLSRole indicates whether the TLS config is for a server or client.
type TLSRole uint8

const (
	TLSRoleServer TLSRole = iota
	TLSRoleClient
)

// TLSConfig holds the certificate material for mTLS.
type TLSConfig struct {
	// CertPEM is the PEM-encoded certificate chain (SVID + CA chain).
	CertPEM []byte
	// KeyPEM is the PEM-encoded private key.
	KeyPEM []byte
	// TrustBundlePEM is the PEM-encoded CA certificate(s) used to verify peers.
	TrustBundlePEM []byte
	// ServerName is used for SNI (client-side) or subject verification.
	ServerName string
}

// BuildServerTLS creates a *tls.Config for a gRPC server with mTLS.
// Enforces TLS 1.3, ECDHE cipher suites, and requires client certificate.
func BuildServerTLS(cfg TLSConfig) (*tls.Config, error) {
	cert, err := tls.X509KeyPair(cfg.CertPEM, cfg.KeyPEM)
	if err != nil {
		return nil, fmt.Errorf("transport: load server cert: %w", err)
	}

	pool, err := buildCAPool(cfg.TrustBundlePEM)
	if err != nil {
		return nil, fmt.Errorf("transport: build CA pool: %w", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		// TLS 1.3 minimum — stronger than OWASP TLS 1.2 recommendation,
		// appropriate for Veriqo's internal cluster-to-cluster traffic.
		MinVersion: tls.VersionTLS13,
		// No explicit cipher suite list at TLS 1.3 — Go's defaults are secure.
	}, nil
}

// BuildClientTLS creates a *tls.Config for a gRPC client with mTLS.
func BuildClientTLS(cfg TLSConfig) (*tls.Config, error) {
	cert, err := tls.X509KeyPair(cfg.CertPEM, cfg.KeyPEM)
	if err != nil {
		return nil, fmt.Errorf("transport: load client cert: %w", err)
	}

	pool, err := buildCAPool(cfg.TrustBundlePEM)
	if err != nil {
		return nil, fmt.Errorf("transport: build CA pool: %w", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		ServerName:   cfg.ServerName,
		MinVersion:   tls.VersionTLS13,
	}, nil
}

func buildCAPool(pem []byte) (*x509.CertPool, error) {
	if len(pem) == 0 {
		return nil, errors.New("transport: trust bundle is empty")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, errors.New("transport: no valid CA certificates in trust bundle")
	}
	return pool, nil
}

// ─── SPIFFE peer verifier ─────────────────────────────────────────────────────

// VerifyPeerSPIFFEID checks that a TLS peer certificate carries the expected
// SPIFFE URI in its Subject Alternative Names. Used as a tls.VerifyPeerCertificate
// hook to enforce node-level identity authorization beyond certificate validity.
//
// Usage:
//
//	cfg.VerifyPeerCertificate = transport.VerifyPeerSPIFFEID(book)
func VerifyPeerSPIFFEID(book *AddressBook) func([][]byte, [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
		if len(verifiedChains) == 0 || len(verifiedChains[0]) == 0 {
			return errors.New("transport: no verified certificate chain")
		}
		leaf := verifiedChains[0][0]

		// Collect SPIFFE URIs from the leaf certificate.
		var peerURIs []string
		for _, uri := range leaf.URIs {
			peerURIs = append(peerURIs, uri.String())
		}

		// Check against the address book.
		for _, addr := range book.All() {
			if addr.SpiffeID == "" {
				continue
			}
			for _, uri := range peerURIs {
				if uri == addr.SpiffeID {
					return nil // found a valid peer identity
				}
			}
		}

		return fmt.Errorf("transport: peer certificate SPIFFE ID not in address book: %v", peerURIs)
	}
}

// ─── Connection pool ──────────────────────────────────────────────────────────

// ConnPool manages outbound transport connections keyed by node ID.
// Actual connection objects are interface{} to avoid importing gRPC
// (which is not yet wired in). The real GRPCTransport will substitute
// *grpc.ClientConn.
type ConnPool struct {
	mu    sync.Mutex
	conns map[core.NodeID]any
	dial  func(addr NodeAddress) (any, error)
}

// NewConnPool creates a pool with the provided dialer function.
func NewConnPool(dial func(addr NodeAddress) (any, error)) *ConnPool {
	return &ConnPool{
		conns: make(map[core.NodeID]any),
		dial:  dial,
	}
}

// Get returns an existing connection or dials a new one.
func (p *ConnPool) Get(ctx context.Context, addr NodeAddress) (any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.conns[addr.ID]; ok {
		return c, nil
	}
	c, err := p.dial(addr)
	if err != nil {
		return nil, fmt.Errorf("transport: dial %v@%s: %w", addr.ID, addr.Addr(), err)
	}
	p.conns[addr.ID] = c
	return c, nil
}

// Remove evicts a connection (e.g. after a network error).
func (p *ConnPool) Remove(id core.NodeID) {
	p.mu.Lock()
	delete(p.conns, id)
	p.mu.Unlock()
}

// Close removes all connections. Callers are responsible for closing the
// underlying connection objects before calling this.
func (p *ConnPool) Close() {
	p.mu.Lock()
	p.conns = make(map[core.NodeID]any)
	p.mu.Unlock()
}

// ─── GRPCTransport stub ───────────────────────────────────────────────────────

// ErrNotImplemented is returned by stub methods.
var ErrNotImplemented = errors.New("transport: GRPCTransport not yet implemented — wire in gRPC stub")

// GRPCTransport is the production transport over gRPC + mTLS.
// Implement this adapter to replace SimTransport in production deployments.
//
// Required steps (see deployment checklist in README.md):
//  1. Deploy SPIRE and obtain an SVID for each node.
//  2. Build client/server TLS configs using BuildServerTLS/BuildClientTLS.
//  3. Create a grpc.Server with the tls.Config as credentials.
//  4. Register the RaftTransportServer (generated from api/proto/raft.proto).
//  5. Implement Send() by looking up the target in AddressBook and
//     dialling via ConnPool.
//  6. Implement Receive() by proxying the gRPC StreamServer's incoming msgs.
type GRPCTransport struct {
	local  NodeAddress
	book   *AddressBook
	tlsCfg TLSConfig
	pool   *ConnPool
	recvCh chan Message
}

// NewGRPCTransport creates a GRPCTransport.
// Call Start() to begin listening before sending messages.
func NewGRPCTransport(local NodeAddress, book *AddressBook, tlsCfg TLSConfig) *GRPCTransport {
	return &GRPCTransport{
		local:  local,
		book:   book,
		tlsCfg: tlsCfg,
		recvCh: make(chan Message, 1024),
	}
}

func (t *GRPCTransport) Send(_ context.Context, _ Message) error {
	return ErrNotImplemented
}

func (t *GRPCTransport) Broadcast(_ context.Context, _ []Message) error {
	return ErrNotImplemented
}

func (t *GRPCTransport) Receive() <-chan Message {
	return t.recvCh
}

func (t *GRPCTransport) Close() error {
	close(t.recvCh)
	if t.pool != nil {
		t.pool.Close()
	}
	return nil
}
