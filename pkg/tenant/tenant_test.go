package tenant

import (
	"bytes"
	"errors"
	"testing"

	"veriqo/pkg/contract"
)

func root(b byte) []byte {
	r := make([]byte, 32)
	for i := range r {
		r[i] = b
	}
	return r
}

func mustAnchor(t *testing.T, id string, b byte) *Anchor {
	t.Helper()
	a, err := NewAnchor(id, root(b))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// TestEverySurfaceDerivesADistinctKey is the rule that makes the
// ceremony more than decoration. One key reused across surfaces means
// a cache-key leak yields the storage key.
func TestEverySurfaceDerivesADistinctKey(t *testing.T) {
	a := mustAnchor(t, "t-acme", 0x11)
	seen := map[string]Surface{}
	for _, s := range Surfaces() {
		k, err := a.Key(s)
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		if len(k) != 32 {
			t.Fatalf("%s: key is %d bytes", s, len(k))
		}
		if prev, dup := seen[string(k)]; dup {
			t.Fatalf("surfaces %s and %s derive the same key", prev, s)
		}
		seen[string(k)] = s
	}
	if len(seen) != len(Surfaces()) {
		t.Fatalf("only %d distinct keys for %d surfaces", len(seen), len(Surfaces()))
	}
}

// TestTenantsNeverCollideOnAnySurface.
func TestTenantsNeverCollideOnAnySurface(t *testing.T) {
	a := mustAnchor(t, "t-acme", 0x11)
	b := mustAnchor(t, "t-beta", 0x22)
	for _, s := range Surfaces() {
		ka, _ := a.Key(s)
		kb, _ := b.Key(s)
		if bytes.Equal(ka, kb) {
			t.Fatalf("two tenants derive the same %s key", s)
		}
		na, _ := a.Namespace(s)
		nb, _ := b.Namespace(s)
		if na == nb {
			t.Fatalf("two tenants share the %s namespace", s)
		}
	}
}

// TestTwoTenantsWithTheSameRootStillDiffer. The tenant id is bound
// into the derivation, so a KMS misconfiguration that hands two
// tenants the same root does not silently merge them.
func TestTwoTenantsWithTheSameRootStillDiffer(t *testing.T) {
	a := mustAnchor(t, "t-acme", 0x33)
	b := mustAnchor(t, "t-beta", 0x33)
	ka, _ := a.Key(Storage)
	kb, _ := b.Key(Storage)
	if bytes.Equal(ka, kb) {
		t.Fatal("a shared root collapsed two tenants into one keyspace")
	}
}

// TestFieldsAreLengthPrefixed catches the concatenation collision:
// without length prefixes, tenant "ab" + surface "c" and tenant "a" +
// surface "bc" derive the same key. Surface names are fixed, so the
// reachable form of this is a tenant id chosen to collide.
func TestFieldsAreLengthPrefixed(t *testing.T) {
	a := mustAnchor(t, "t-ab", 0x44)
	b := mustAnchor(t, "t-a", 0x44)
	ka, _ := a.SubKey(Storage, "c")
	kb, _ := b.SubKey(Storage, "bc")
	if bytes.Equal(ka, kb) {
		t.Fatal("field concatenation collides: derivation is not length-prefixed")
	}
}

// TestNamespaceDoesNotLeakTheTenantID. A shared search index must not
// be browsable for customer names.
func TestNamespaceDoesNotLeakTheTenantID(t *testing.T) {
	a := mustAnchor(t, "t-acme", 0x55)
	ns, err := a.Namespace(Search)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains([]byte(ns), []byte("acme")) {
		t.Fatalf("namespace %q carries the tenant name", ns)
	}
}

// TestGuardFailsClosed is the cross-tenant rule.
func TestGuardFailsClosed(t *testing.T) {
	a := mustAnchor(t, "t-acme", 0x66)
	sc, err := a.Scope(Storage)
	if err != nil {
		t.Fatal(err)
	}
	if err := Guard(sc, "t-acme"); err != nil {
		t.Fatalf("same-tenant access refused: %v", err)
	}
	if err := Guard(sc, "t-beta"); !errors.Is(err, contract.ErrCrossTenant) {
		t.Fatalf("cross-tenant access error = %v, want ErrCrossTenant", err)
	}
	// The important half: an empty request must not pass. A zero value
	// reaching a guard is the classic way isolation is lost.
	if err := Guard(sc, ""); err == nil {
		t.Fatal("an empty tenant request was accepted")
	}
	if err := Guard(Scope{}, "t-acme"); !errors.Is(err, ErrNoAnchor) {
		t.Fatalf("a zero scope was accepted: %v", err)
	}
}

// TestAFabricatedScopeIsRejected. A caller can write the struct
// literal; it cannot write the key.
func TestAFabricatedScopeIsRejected(t *testing.T) {
	a := mustAnchor(t, "t-acme", 0x77)
	forged := Scope{TenantID: "t-acme", Surface: Storage}
	if err := a.Verify(forged); !errors.Is(err, ErrScopeMismatch) {
		t.Fatalf("a fabricated scope verified: %v", err)
	}
	real, _ := a.Scope(Storage)
	if err := a.Verify(real); err != nil {
		t.Fatalf("a genuine scope failed verification: %v", err)
	}
	// A genuine scope for the wrong surface must also fail.
	other, _ := a.Scope(Cache)
	other.Surface = Storage
	if err := a.Verify(other); err == nil {
		t.Fatal("a scope whose surface was relabelled verified")
	}
}

// TestAShortRootIsRefused. Accepting a 4-byte root would cap the
// entropy of every 32-byte key printed from it.
func TestAShortRootIsRefused(t *testing.T) {
	if _, err := NewAnchor("t-acme", []byte("short")); !errors.Is(err, ErrShortRoot) {
		t.Fatalf("short root accepted: %v", err)
	}
	if _, err := NewAnchor("t-acme", nil); !errors.Is(err, ErrShortRoot) {
		t.Fatalf("nil root accepted: %v", err)
	}
}

func TestMalformedTenantIDsAreRefused(t *testing.T) {
	for _, id := range []string{"", "acme", "t-", "T-ACME", "t-acme-", "t--", "t-ac me"} {
		if _, err := NewAnchor(id, root(1)); !errors.Is(err, ErrBadTenantID) {
			t.Errorf("tenant id %q accepted", id)
		}
	}
}

// TestTheRootNeverEscapes. There is no accessor, and KeyBytes returns
// a copy so a caller cannot mutate the scope it was handed.
func TestTheRootNeverEscapes(t *testing.T) {
	a := mustAnchor(t, "t-acme", 0x88)
	sc, _ := a.Scope(Storage)
	k1 := sc.KeyBytes()
	for i := range k1 {
		k1[i] = 0
	}
	k2 := sc.KeyBytes()
	if bytes.Equal(k1, k2) {
		t.Fatal("KeyBytes handed out the live slice; a caller can zero a scope in place")
	}
	if err := a.Verify(sc); err != nil {
		t.Fatalf("the scope was damaged by a caller mutating its key copy: %v", err)
	}
}

// TestPerCaseKeysAreSubkeys: revoking a case must not require rotating
// the tenant root.
func TestPerCaseKeysAreSubkeys(t *testing.T) {
	a := mustAnchor(t, "t-acme", 0x99)
	c1, err := a.SubKey(Storage, "case-1")
	if err != nil {
		t.Fatal(err)
	}
	c2, _ := a.SubKey(Storage, "case-2")
	base, _ := a.Key(Storage)
	if bytes.Equal(c1, c2) {
		t.Fatal("two cases derive the same key")
	}
	if bytes.Equal(c1, base) {
		t.Fatal("a case key equals the tenant surface key")
	}
	if _, err := a.SubKey(Storage, ""); err == nil {
		t.Fatal("an empty discriminator was accepted")
	}
}

func TestUnknownSurfaceIsRefused(t *testing.T) {
	a := mustAnchor(t, "t-acme", 0xaa)
	if _, err := a.Key(Surface("bucket")); !errors.Is(err, ErrUnknownSurface) {
		t.Fatalf("unknown surface accepted: %v", err)
	}
}
