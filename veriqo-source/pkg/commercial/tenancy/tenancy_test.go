package tenancy

import "testing"

func TestGrantThenIsAuthorized(t *testing.T) {
	m := New()
	if m.IsAuthorized("alice", "tenant-A") {
		t.Fatal("expected no authorization before any Grant")
	}
	m.Grant("alice", "tenant-A")
	if !m.IsAuthorized("alice", "tenant-A") {
		t.Fatal("expected alice to be authorized for tenant-A after Grant")
	}
	if m.IsAuthorized("alice", "tenant-B") {
		t.Fatal("expected alice to NOT be authorized for a tenant she was never granted")
	}
	if m.IsAuthorized("bob", "tenant-A") {
		t.Fatal("expected bob to NOT be authorized for a tenant granted only to alice")
	}
}

func TestGrantIsIdempotent(t *testing.T) {
	m := New()
	m.Grant("alice", "tenant-A")
	m.Grant("alice", "tenant-A")
	tenants := m.TenantsFor("alice")
	if len(tenants) != 1 {
		t.Fatalf("expected exactly 1 tenant after a duplicate Grant, got %v", tenants)
	}
}

func TestRevoke(t *testing.T) {
	m := New()
	m.Grant("alice", "tenant-A")
	m.Revoke("alice", "tenant-A")
	if m.IsAuthorized("alice", "tenant-A") {
		t.Fatal("expected authorization to be gone after Revoke")
	}
}

func TestRevokeOfUngrantedIsANoOp(t *testing.T) {
	m := New()
	m.Revoke("alice", "tenant-A") // must not panic
	if m.IsAuthorized("alice", "tenant-A") {
		t.Fatal("expected no authorization")
	}
}

func TestMultiTenantSubject(t *testing.T) {
	m := New()
	m.Grant("alice", "tenant-A")
	m.Grant("alice", "tenant-B")
	if !m.IsAuthorized("alice", "tenant-A") || !m.IsAuthorized("alice", "tenant-B") {
		t.Fatal("expected alice to be authorized for both granted tenants")
	}
	tenants := m.TenantsFor("alice")
	if len(tenants) != 2 {
		t.Fatalf("expected 2 tenants, got %v", tenants)
	}
}

func TestEmptySubjectOrTenantIsNeverAuthorized(t *testing.T) {
	m := New()
	m.Grant("alice", "tenant-A")
	if m.IsAuthorized("", "tenant-A") {
		t.Fatal("expected an empty subject to never be authorized")
	}
	if m.IsAuthorized("alice", "") {
		t.Fatal("expected an empty tenantID to never be authorized")
	}
}
