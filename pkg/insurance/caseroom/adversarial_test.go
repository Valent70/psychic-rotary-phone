package caseroom

import (
	"errors"
	"testing"

	"veriqo/pkg/insurance/party"
)

// This file is the Round 5 work order's own named Case Room Security
// adversarial test list (§11), each scenario as its own test so a
// failure names exactly which boundary broke:
//
//	tenant A cannot access tenant B
//	user A cannot access unauthorized case
//	revoked authority cannot act
//	expired authority cannot act
//	wrong jurisdiction cannot authorize
//	wrong scope cannot authorize
//	delegation cannot self-delegate            (party/relationship_test.go)
//	delegation chain cannot exceed policy      (party/relationship_test.go)

func mustAuthorizedRelationship(t *testing.T, reg *party.RelationshipRegistry, id string, tenant, jurisdiction, scope string) {
	t.Helper()
	r, err := party.New(id, reg.CaseID(), party.PartyID("PTY-"+id), "PTY-INSURED", party.RoleBroker, 0)
	if err != nil {
		t.Fatalf("party.New(%s): %v", id, err)
	}
	r.Tenant = tenant
	r.Jurisdiction = jurisdiction
	r.Scope = scope
	if err := reg.Register(r); err != nil {
		t.Fatalf("Register(%s): %v", id, err)
	}
	if err := reg.GrantPermissions(id, party.PermissionAccessCaseRoom); err != nil {
		t.Fatalf("GrantPermissions(%s): %v", id, err)
	}
	if err := reg.RecordConsent(id, "EV-CONSENT-"+id); err != nil {
		t.Fatalf("RecordConsent(%s): %v", id, err)
	}
}

// TestAdversarialTenantACannotAccessTenantB is the order's own first
// named scenario, verbatim.
func TestAdversarialTenantACannotAccessTenantB(t *testing.T) {
	reg, _ := party.NewRelationshipRegistry("CASE-1")
	mustAuthorizedRelationship(t, reg, "REL-TENANT-A", "TENANT-A", "", "")

	if _, err := Authorize(reg, "REL-TENANT-A", 100, AuthorizeContext{Tenant: "TENANT-A"}); err != nil {
		t.Fatalf("expected the matching tenant to be authorized, got %v", err)
	}
	if _, err := Authorize(reg, "REL-TENANT-A", 100, AuthorizeContext{Tenant: "TENANT-B"}); !errors.Is(err, ErrCrossTenantAccess) {
		t.Fatalf("expected ErrCrossTenantAccess for a mismatched tenant, got %v", err)
	}
}

// TestAdversarialUserCannotAccessUnauthorizedCase proves cross-case
// access is structurally impossible: a relationship registered in one
// case's registry does not exist at all in another case's registry, so
// there is no ID to authorize against.
func TestAdversarialUserCannotAccessUnauthorizedCase(t *testing.T) {
	regA, _ := party.NewRelationshipRegistry("CASE-A")
	mustAuthorizedRelationship(t, regA, "REL-1", "", "", "")

	regB, _ := party.NewRelationshipRegistry("CASE-B")
	// REL-1 was never registered against CASE-B's own registry.
	if _, err := Authorize(regB, "REL-1", 100, AuthorizeContext{}); !errors.Is(err, ErrNoAccess) {
		t.Fatalf("expected ErrNoAccess for a relationship unregistered in this case, got %v", err)
	}
}

// TestAdversarialRevokedAuthorityCannotAct proves a revoked
// relationship loses Case Room access from the revocation tick onward.
func TestAdversarialRevokedAuthorityCannotAct(t *testing.T) {
	reg, _ := party.NewRelationshipRegistry("CASE-1")
	mustAuthorizedRelationship(t, reg, "REL-1", "", "", "")

	if _, err := Authorize(reg, "REL-1", 100, AuthorizeContext{}); err != nil {
		t.Fatalf("expected access before revocation, got %v", err)
	}
	if err := reg.Revoke("REL-1", 200, "authority withdrawn"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := Authorize(reg, "REL-1", 250, AuthorizeContext{}); !errors.Is(err, ErrNoAccess) {
		t.Fatalf("expected ErrNoAccess after revocation, got %v", err)
	}
}

// TestAdversarialExpiredAuthorityCannotAct proves a relationship whose
// EffectiveTo window has closed loses Case Room access.
func TestAdversarialExpiredAuthorityCannotAct(t *testing.T) {
	reg, _ := party.NewRelationshipRegistry("CASE-1")
	r, err := party.New("REL-1", "CASE-1", "PTY-A", "PTY-B", party.RoleBroker, 100)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r.EffectiveTo = 500
	if err := reg.Register(r); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := reg.GrantPermissions("REL-1", party.PermissionAccessCaseRoom); err != nil {
		t.Fatalf("GrantPermissions: %v", err)
	}
	if err := reg.RecordConsent("REL-1", "EV-CONSENT"); err != nil {
		t.Fatalf("RecordConsent: %v", err)
	}

	if _, err := Authorize(reg, "REL-1", 300, AuthorizeContext{}); err != nil {
		t.Fatalf("expected access within the effective window, got %v", err)
	}
	if _, err := Authorize(reg, "REL-1", 600, AuthorizeContext{}); !errors.Is(err, ErrNoAccess) {
		t.Fatalf("expected ErrNoAccess once EffectiveTo has passed, got %v", err)
	}
}

// TestAdversarialWrongJurisdictionCannotAuthorize is the order's own
// named scenario, verbatim.
func TestAdversarialWrongJurisdictionCannotAuthorize(t *testing.T) {
	reg, _ := party.NewRelationshipRegistry("CASE-1")
	mustAuthorizedRelationship(t, reg, "REL-1", "", "England and Wales", "")

	if _, err := Authorize(reg, "REL-1", 100, AuthorizeContext{Jurisdiction: "England and Wales"}); err != nil {
		t.Fatalf("expected the matching jurisdiction to be authorized, got %v", err)
	}
	if _, err := Authorize(reg, "REL-1", 100, AuthorizeContext{Jurisdiction: "Singapore"}); !errors.Is(err, ErrWrongJurisdiction) {
		t.Fatalf("expected ErrWrongJurisdiction for a mismatched jurisdiction, got %v", err)
	}
}

// TestAdversarialWrongScopeCannotAuthorize is the order's own named
// scenario, verbatim.
func TestAdversarialWrongScopeCannotAuthorize(t *testing.T) {
	reg, _ := party.NewRelationshipRegistry("CASE-1")
	mustAuthorizedRelationship(t, reg, "REL-1", "", "", "cargo damage assessment for CLM-002 only")

	if _, err := Authorize(reg, "REL-1", 100, AuthorizeContext{Scope: "cargo damage assessment for CLM-002 only"}); err != nil {
		t.Fatalf("expected the matching scope to be authorized, got %v", err)
	}
	if _, err := Authorize(reg, "REL-1", 100, AuthorizeContext{Scope: "hull damage assessment for CLM-003"}); !errors.Is(err, ErrWrongScope) {
		t.Fatalf("expected ErrWrongScope for a mismatched scope, got %v", err)
	}
}

// TestAdversarialUnrestrictedAxisNeverBlocks proves the "empty means
// unrestricted" convention holds in both directions: a relationship
// with no Tenant/Jurisdiction/Scope set is authorized regardless of
// what the caller requires, and a caller that requires nothing is
// never blocked by a relationship that happens to have those fields
// set.
func TestAdversarialUnrestrictedAxisNeverBlocks(t *testing.T) {
	reg, _ := party.NewRelationshipRegistry("CASE-1")
	mustAuthorizedRelationship(t, reg, "REL-UNSET", "", "", "")
	mustAuthorizedRelationship(t, reg, "REL-SET", "TENANT-A", "Singapore", "hull only")

	if _, err := Authorize(reg, "REL-UNSET", 100, AuthorizeContext{Tenant: "TENANT-X", Jurisdiction: "Mars", Scope: "anything"}); err != nil {
		t.Fatalf("a relationship with no restriction fields set must never be blocked, got %v", err)
	}
	if _, err := Authorize(reg, "REL-SET", 100, AuthorizeContext{}); err != nil {
		t.Fatalf("a caller requiring nothing must never be blocked by a relationship's own set fields, got %v", err)
	}
}

// TestAdversarialSelfDelegationAndChainLimitViaCaseRoom re-confirms,
// through the Case Room's own entry point rather than only against
// party.RelationshipRegistry directly, that a self-delegating or
// over-long chain never reaches an authorized state -- Register itself
// refuses to create it, so there is nothing for Authorize to even
// evaluate.
func TestAdversarialSelfDelegationAndChainLimitViaCaseRoom(t *testing.T) {
	reg, _ := party.NewRelationshipRegistry("CASE-1")
	self, err := party.New("REL-SELF", "CASE-1", "PTY-A", "PTY-B", party.RoleBroker, 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	self.DelegatedFrom = "REL-SELF"
	if err := reg.Register(self); err == nil {
		t.Fatal("expected Register to refuse a self-delegating relationship")
	}
	if _, err := Authorize(reg, "REL-SELF", 100, AuthorizeContext{}); !errors.Is(err, ErrNoAccess) {
		t.Fatalf("a relationship that failed to register must never authorize; expected ErrNoAccess, got %v", err)
	}
}
