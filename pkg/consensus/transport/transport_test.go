package transport_test

import (
	"context"
	"testing"

	"veriqo/pkg/consensus/transport"
	"veriqo/pkg/core"
)

// ─── AddressBook ─────────────────────────────────────────────────────────────

func TestAddressBook_RegisterAndLookup(t *testing.T) {
	book := transport.NewAddressBook()
	addr := transport.NodeAddress{
		ID:       core.NodeID("node-1"),
		Host:     "10.0.0.1",
		Port:     7001,
		SpiffeID: "spiffe://veriqo.global/node/node-1",
	}
	book.Register(addr)

	got, err := book.Lookup("node-1")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got.Addr() != "10.0.0.1:7001" {
		t.Errorf("expected 10.0.0.1:7001, got %s", got.Addr())
	}
	if got.SpiffeID != addr.SpiffeID {
		t.Errorf("SPIFFE ID mismatch: %s vs %s", got.SpiffeID, addr.SpiffeID)
	}
}

func TestAddressBook_LookupNotFound(t *testing.T) {
	book := transport.NewAddressBook()
	_, err := book.Lookup("nonexistent")
	if err == nil {
		t.Error("expected error for missing node")
	}
}

func TestAddressBook_Remove(t *testing.T) {
	book := transport.NewAddressBook()
	book.Register(transport.NodeAddress{ID: "n1", Host: "10.0.0.1", Port: 7001})
	book.Remove("n1")
	_, err := book.Lookup("n1")
	if err == nil {
		t.Error("expected error after remove")
	}
}

func TestAddressBook_All_SortedByID(t *testing.T) {
	book := transport.NewAddressBook()
	for _, id := range []core.NodeID{"C", "A", "B"} {
		book.Register(transport.NodeAddress{ID: id, Host: "10.0.0.1", Port: 7000})
	}
	all := book.All()
	if len(all) != 3 {
		t.Fatalf("expected 3 addresses, got %d", len(all))
	}
	if all[0].ID != "A" || all[1].ID != "B" || all[2].ID != "C" {
		t.Errorf("expected sorted order A,B,C, got %v", all)
	}
}

// ─── NodeAddress ─────────────────────────────────────────────────────────────

func TestNodeAddress_Addr(t *testing.T) {
	addr := transport.NodeAddress{Host: "raft.internal", Port: 9001}
	if addr.Addr() != "raft.internal:9001" {
		t.Errorf("expected raft.internal:9001, got %s", addr.Addr())
	}
}

// ─── ConnPool ────────────────────────────────────────────────────────────────

func TestConnPool_GetAndCache(t *testing.T) {
	dialCount := 0
	pool := transport.NewConnPool(func(addr transport.NodeAddress) (any, error) {
		dialCount++
		return "conn-" + string(addr.ID), nil
	})

	ctx := context.Background()
	addr := transport.NodeAddress{ID: "peer-1", Host: "10.0.0.2", Port: 7001}

	c1, err := pool.Get(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := pool.Get(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	if c1 != c2 {
		t.Error("expected same connection from cache")
	}
	if dialCount != 1 {
		t.Errorf("expected 1 dial, got %d", dialCount)
	}
}

func TestConnPool_Remove(t *testing.T) {
	dialCount := 0
	pool := transport.NewConnPool(func(addr transport.NodeAddress) (any, error) {
		dialCount++
		return struct{}{}, nil
	})

	ctx := context.Background()
	addr := transport.NodeAddress{ID: "peer-2", Host: "10.0.0.3", Port: 7001}
	pool.Get(ctx, addr)
	pool.Remove("peer-2")
	pool.Get(ctx, addr)
	if dialCount != 2 {
		t.Errorf("expected 2 dials after remove, got %d", dialCount)
	}
}

// ─── GRPCTransport stub ───────────────────────────────────────────────────────

func TestGRPCTransport_SendNotImplemented(t *testing.T) {
	book := transport.NewAddressBook()
	tr := transport.NewGRPCTransport(
		transport.NodeAddress{ID: "self", Host: "localhost", Port: 7001},
		book,
		transport.TLSConfig{},
	)
	err := tr.Send(context.Background(), transport.Message{})
	if err == nil {
		t.Error("expected ErrNotImplemented from stub")
	}
}

func TestGRPCTransport_ReceiveChannel(t *testing.T) {
	book := transport.NewAddressBook()
	tr := transport.NewGRPCTransport(
		transport.NodeAddress{ID: "self", Host: "localhost", Port: 7002},
		book,
		transport.TLSConfig{},
	)
	ch := tr.Receive()
	if ch == nil {
		t.Error("expected non-nil receive channel")
	}
	tr.Close()
}

// ─── TLS builder (no real certs needed for error paths) ─────────────────────

func TestBuildServerTLS_EmptyBundle(t *testing.T) {
	_, err := transport.BuildServerTLS(transport.TLSConfig{
		CertPEM:        []byte("not-a-real-cert"),
		KeyPEM:         []byte("not-a-real-key"),
		TrustBundlePEM: nil, // empty
	})
	if err == nil {
		t.Error("expected error for empty trust bundle")
	}
}
