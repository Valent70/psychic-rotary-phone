package party

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestNewRelationshipStartsPendingUnconsented(t *testing.T) {
	r, err := New("REL-1", "CASE-1", "PTY-BROKER", "PTY-INSURED", RoleBroker, 100)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if r.Status != RelationshipStatusPending {
		t.Fatalf("expected PENDING, got %s", r.Status)
	}
	if r.EffectiveAt(100) {
		t.Fatal("a PENDING (unconsented) relationship must never be effective")
	}
}

func TestValidateRejectsMalformed(t *testing.T) {
	base := func() Relationship {
		r, _ := New("REL-1", "CASE-1", "PTY-A", "PTY-B", RoleBroker, 0)
		return r
	}
	cases := []struct {
		name string
		mod  func(Relationship) Relationship
		want error
	}{
		{"empty id", func(r Relationship) Relationship { r.RelationshipID = ""; return r }, ErrEmptyRelationshipID},
		{"empty case", func(r Relationship) Relationship { r.CaseID = ""; return r }, ErrEmptyRelationshipCase},
		{"empty from", func(r Relationship) Relationship { r.FromParty = ""; return r }, ErrEmptyFromParty},
		{"empty to", func(r Relationship) Relationship { r.ToParty = ""; return r }, ErrEmptyToParty},
		{"self relationship", func(r Relationship) Relationship { r.ToParty = r.FromParty; return r }, ErrSelfRelationship},
		{"unknown role", func(r Relationship) Relationship { r.Role = "BOGUS"; return r }, ErrRelationshipUnknownRole},
		{"to before from", func(r Relationship) Relationship { r.EffectiveFrom = 100; r.EffectiveTo = 50; return r }, ErrRelationshipToBeforeFrom},
		{"unknown permission", func(r Relationship) Relationship { r.Permissions = []Permission{"BOGUS"}; return r }, ErrUnknownPermission},
		{"consent with no evidence", func(r Relationship) Relationship { r.ConsentGiven = true; return r }, ErrConsentWithNoEvidence},
		{"unknown status", func(r Relationship) Relationship { r.Status = "BOGUS"; return r }, ErrRelationshipUnknownStatus},
		{"revoked with no reason", func(r Relationship) Relationship { r.Status = RelationshipStatusRevoked; return r }, ErrRevokedNeedsReason},
		{"self delegation", func(r Relationship) Relationship { r.DelegatedFrom = r.RelationshipID; return r }, ErrSelfDelegation},
	}
	for _, c := range cases {
		if err := c.mod(base()).Validate(); !errors.Is(err, c.want) {
			t.Errorf("%s: expected %v, got %v", c.name, c.want, err)
		}
	}
}

func TestRegistryFullLifecycle(t *testing.T) {
	reg, err := NewRelationshipRegistry("CASE-1")
	if err != nil {
		t.Fatalf("NewRelationshipRegistry: %v", err)
	}
	r, err := New("REL-1", "CASE-1", "PTY-BROKER", "PTY-INSURED", RoleBroker, 100)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := reg.Register(r); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if err := reg.AddProvenance("REL-1", "EV-TOBA-DOC"); err != nil {
		t.Fatalf("AddProvenance: %v", err)
	}
	if err := reg.GrantPermissions("REL-1", PermissionSubmitClaim, PermissionViewEvidence); err != nil {
		t.Fatalf("GrantPermissions: %v", err)
	}
	// Not yet consented: still not effective.
	if reg.EffectiveAt("REL-1", 200) {
		t.Fatal("expected not effective before consent")
	}

	if err := reg.RecordConsent("REL-1", "EV-SIGNED-TOBA"); err != nil {
		t.Fatalf("RecordConsent: %v", err)
	}
	got, _ := reg.Get("REL-1")
	if got.Status != RelationshipStatusActive {
		t.Fatalf("expected ACTIVE after consent, got %s", got.Status)
	}
	if !reg.EffectiveAt("REL-1", 200) {
		t.Fatal("expected effective after consent, within window")
	}
	if !got.HasPermission(PermissionSubmitClaim) {
		t.Fatal("expected PermissionSubmitClaim to be granted")
	}

	if err := reg.Revoke("REL-1", 300, "broker relationship terminated by insured"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if reg.EffectiveAt("REL-1", 350) {
		t.Fatal("expected NOT effective after revocation")
	}
}

// TestRevocationPreservesHistoricalEffectivenessBeforeRevocationTick
// proves revocation does not rewrite history: a relationship that WAS
// effective before it was revoked stays truthfully reported as having
// been effective then, matching this codebase's immutability
// discipline (a later fact does not erase an earlier one).
func TestRevocationPreservesHistoricalEffectivenessBeforeRevocationTick(t *testing.T) {
	reg, _ := NewRelationshipRegistry("CASE-1")
	r, _ := New("REL-1", "CASE-1", "PTY-A", "PTY-B", RoleBroker, 100)
	_ = reg.Register(r)
	_ = reg.RecordConsent("REL-1", "EV-CONSENT")
	if !reg.EffectiveAt("REL-1", 150) {
		t.Fatal("expected effective before revocation")
	}
	_ = reg.Revoke("REL-1", 200, "terminated")
	if !reg.EffectiveAt("REL-1", 150) {
		t.Fatal("EffectiveAt(150) must remain true: revocation at tick 200 does not retroactively erase tick 150's truth")
	}
	if reg.EffectiveAt("REL-1", 200) || reg.EffectiveAt("REL-1", 250) {
		t.Fatal("expected NOT effective at or after the revocation tick")
	}
}

func TestOpenEndedEffectiveTo(t *testing.T) {
	reg, _ := NewRelationshipRegistry("CASE-1")
	r, _ := New("REL-1", "CASE-1", "PTY-A", "PTY-B", RoleLossAdjuster, 10)
	_ = reg.Register(r)
	_ = reg.RecordConsent("REL-1", "EV-1")
	if !reg.EffectiveAt("REL-1", 999_999) {
		t.Fatal("EffectiveTo == 0 must mean open-ended")
	}
}

func TestRegisterRejectsCaseIDMismatch(t *testing.T) {
	reg, _ := NewRelationshipRegistry("CASE-1")
	r, _ := New("REL-1", "CASE-2", "PTY-A", "PTY-B", RoleBroker, 0)
	if err := reg.Register(r); !errors.Is(err, ErrRelationshipCaseIDMismatch) {
		t.Fatalf("expected ErrRelationshipCaseIDMismatch, got %v", err)
	}
}

func TestRegisterRejectsDuplicate(t *testing.T) {
	reg, _ := NewRelationshipRegistry("CASE-1")
	r, _ := New("REL-1", "CASE-1", "PTY-A", "PTY-B", RoleBroker, 0)
	if err := reg.Register(r); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := reg.Register(r); !errors.Is(err, ErrDuplicateRelationship) {
		t.Fatalf("expected ErrDuplicateRelationship, got %v", err)
	}
}

func TestByPartyFindsBothDirections(t *testing.T) {
	reg, _ := NewRelationshipRegistry("CASE-1")
	r1, _ := New("REL-1", "CASE-1", "PTY-BROKER", "PTY-INSURED", RoleBroker, 0)
	r2, _ := New("REL-2", "CASE-1", "PTY-INSURER", "PTY-BROKER", RoleInsurer, 0)
	_ = reg.Register(r1)
	_ = reg.Register(r2)
	got := reg.ByParty("PTY-BROKER")
	if len(got) != 2 {
		t.Fatalf("expected PTY-BROKER to appear in both relationships (as From and as To), got %d", len(got))
	}
}

func TestEffectiveAtUnknownIDFailsClosed(t *testing.T) {
	reg, _ := NewRelationshipRegistry("CASE-1")
	if reg.EffectiveAt("NOPE", 100) {
		t.Fatal("an unknown relationship must never be reported as effective")
	}
}

func TestNotFoundOnEveryMutator(t *testing.T) {
	reg, _ := NewRelationshipRegistry("CASE-1")
	if err := reg.GrantPermissions("NOPE", PermissionViewEvidence); !errors.Is(err, ErrRelationshipNotFound) {
		t.Errorf("GrantPermissions: expected ErrRelationshipNotFound, got %v", err)
	}
	if err := reg.RecordConsent("NOPE", "EV-1"); !errors.Is(err, ErrRelationshipNotFound) {
		t.Errorf("RecordConsent: expected ErrRelationshipNotFound, got %v", err)
	}
	if err := reg.Revoke("NOPE", 1, "reason"); !errors.Is(err, ErrRelationshipNotFound) {
		t.Errorf("Revoke: expected ErrRelationshipNotFound, got %v", err)
	}
	if err := reg.AddProvenance("NOPE", "EV-1"); !errors.Is(err, ErrRelationshipNotFound) {
		t.Errorf("AddProvenance: expected ErrRelationshipNotFound, got %v", err)
	}
}

func TestConcurrentRelationshipRegistryAccess(t *testing.T) {
	reg, _ := NewRelationshipRegistry("CASE-1")
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := "REL-" + string(rune('A'+i%26)) + string(rune('0'+i/26))
			r, err := New(id, "CASE-1", PartyID("PTY-F"+id), PartyID("PTY-T"+id), RoleSurveyor, 1)
			if err != nil {
				t.Errorf("New: %v", err)
				return
			}
			if err := reg.Register(r); err != nil {
				t.Errorf("Register: %v", err)
				return
			}
			_ = reg.RecordConsent(id, "EV-1")
			_ = reg.GrantPermissions(id, PermissionViewEvidence)
			_, _ = reg.Get(id)
			_ = reg.All()
			_ = reg.EffectiveAt(id, 5)
		}()
	}
	wg.Wait()
	if reg.Count() != 40 {
		t.Fatalf("expected 40 relationships, got %d", reg.Count())
	}
}

// ---- Guardrail-style: no liability/authority-overreach field ----

func TestNoLiabilityOrLegalDeterminationField(t *testing.T) {
	rt := reflect.TypeOf(Relationship{})
	forbidden := []string{"liable", "liability", "guilt", "guilty", "verdict", "settlement", "fault"}
	for i := 0; i < rt.NumField(); i++ {
		name := strings.ToLower(rt.Field(i).Name)
		for _, bad := range forbidden {
			if strings.Contains(name, bad) {
				t.Errorf("Relationship.%s contains forbidden substring %q", rt.Field(i).Name, bad)
			}
		}
	}
}

func TestKnownPermissionsExhaustive(t *testing.T) {
	if len(KnownPermissions()) != 10 {
		t.Fatalf("expected 10 known permissions, got %d: %v", len(KnownPermissions()), KnownPermissions())
	}
}

// ---- Round 4: Party Authority Model additive fields ----

// TestPartyAuthorityModelFieldsRoundTrip proves organization/scope/
// tenant/jurisdiction are ordinary, settable, validated fields -- not
// merely declared in the struct.
func TestPartyAuthorityModelFieldsRoundTrip(t *testing.T) {
	r, err := New("REL-SURVEYOR-1", "CASE-1", "PTY-SURVEYOR", "PTY-INSURER", RoleSurveyor, 100)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r.Organization = "Acme Marine Surveyors Ltd"
	r.Scope = "cargo damage assessment for CLM-002 only"
	r.Tenant = "TENANT-LLOYDS-SYNDICATE-42"
	r.Jurisdiction = "England and Wales"
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate rejected a relationship with only the new optional fields set: %v", err)
	}
}

// TestDelegationRequiresAnAlreadyRegisteredPermittingSource proves the
// registry-level enforcement: DelegatedFrom must name a relationship
// that (a) already exists in this registry and (b) explicitly allows
// further delegation.
func TestDelegationRequiresAnAlreadyRegisteredPermittingSource(t *testing.T) {
	reg, err := NewRelationshipRegistry("CASE-1")
	if err != nil {
		t.Fatalf("NewRelationshipRegistry: %v", err)
	}

	sub, err := New("REL-SUB", "CASE-1", "PTY-SUB-SURVEYOR", "PTY-INSURER", RoleSurveyor, 100)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sub.DelegatedFrom = "REL-BROKER"
	if err := reg.Register(sub); !errors.Is(err, ErrDelegationSourceNotFound) {
		t.Fatalf("expected ErrDelegationSourceNotFound before the source is registered, got %v", err)
	}

	broker, err := New("REL-BROKER", "CASE-1", "PTY-BROKER", "PTY-INSURED", RoleBroker, 50)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// CanDelegate defaults false: delegation authority is never assumed.
	if err := reg.Register(broker); err != nil {
		t.Fatalf("Register(broker): %v", err)
	}
	if err := reg.Register(sub); !errors.Is(err, ErrDelegationNotPermitted) {
		t.Fatalf("expected ErrDelegationNotPermitted while CanDelegate is false, got %v", err)
	}

	broker.CanDelegate = true
	reg2, _ := NewRelationshipRegistry("CASE-1")
	if err := reg2.Register(broker); err != nil {
		t.Fatalf("Register(broker with CanDelegate): %v", err)
	}
	if err := reg2.Register(sub); err != nil {
		t.Fatalf("Register(sub) once the source permits delegation: %v", err)
	}

	chain, err := reg2.DelegationChain("REL-SUB")
	if err != nil {
		t.Fatalf("DelegationChain: %v", err)
	}
	if len(chain) != 2 || chain[0].RelationshipID != "REL-BROKER" || chain[1].RelationshipID != "REL-SUB" {
		t.Fatalf("expected root-first [REL-BROKER, REL-SUB], got %v", relationshipIDs(chain))
	}
}

// TestDelegationChainOfOneReturnsJustItself proves a relationship with
// no DelegatedFrom is its own one-element chain, not an error.
func TestDelegationChainOfOneReturnsJustItself(t *testing.T) {
	reg, _ := NewRelationshipRegistry("CASE-1")
	r, _ := New("REL-1", "CASE-1", "PTY-A", "PTY-B", RoleBroker, 0)
	if err := reg.Register(r); err != nil {
		t.Fatalf("Register: %v", err)
	}
	chain, err := reg.DelegationChain("REL-1")
	if err != nil {
		t.Fatalf("DelegationChain: %v", err)
	}
	if len(chain) != 1 || chain[0].RelationshipID != "REL-1" {
		t.Fatalf("expected a single-element chain, got %v", relationshipIDs(chain))
	}
}

func relationshipIDs(rels []Relationship) []string {
	ids := make([]string, len(rels))
	for i, r := range rels {
		ids[i] = r.RelationshipID
	}
	return ids
}

// ---- Round 5: Case Room Security matrix -- "delegation chain cannot exceed policy" ----

// TestDelegationChainCannotExceedPolicy builds a chain one link past
// SetMaxDelegationDepth's limit and proves the LAST link is refused --
// not merely that some arbitrary error appears, but exactly
// ErrDelegationChainTooLong.
func TestDelegationChainCannotExceedPolicy(t *testing.T) {
	reg, _ := NewRelationshipRegistry("CASE-1")
	if err := reg.SetMaxDelegationDepth(3); err != nil {
		t.Fatalf("SetMaxDelegationDepth: %v", err)
	}

	mustLink := func(id, delegatedFrom string) Relationship {
		r, err := New(id, "CASE-1", PartyID("PTY-"+id), "PTY-ROOT", RoleBroker, 0)
		if err != nil {
			t.Fatalf("New(%s): %v", id, err)
		}
		r.CanDelegate = true
		r.DelegatedFrom = delegatedFrom
		return r
	}

	link1 := mustLink("REL-1", "")
	if err := reg.Register(link1); err != nil {
		t.Fatalf("Register(REL-1): %v", err)
	}
	link2 := mustLink("REL-2", "REL-1")
	if err := reg.Register(link2); err != nil {
		t.Fatalf("Register(REL-2): %v", err)
	}
	link3 := mustLink("REL-3", "REL-2")
	if err := reg.Register(link3); err != nil {
		t.Fatalf("Register(REL-3): %v", err) // depth 3, exactly at policy -- must succeed
	}

	link4 := mustLink("REL-4", "REL-3")
	if err := reg.Register(link4); !errors.Is(err, ErrDelegationChainTooLong) {
		t.Fatalf("Register(REL-4) at depth 4 over a max of 3: expected ErrDelegationChainTooLong, got %v", err)
	}
}

// TestSetMaxDelegationDepthRejectsNonPositive proves the policy setter
// itself fails closed against a nonsensical (zero or negative) limit.
func TestSetMaxDelegationDepthRejectsNonPositive(t *testing.T) {
	reg, _ := NewRelationshipRegistry("CASE-1")
	if err := reg.SetMaxDelegationDepth(0); err == nil {
		t.Fatal("expected SetMaxDelegationDepth(0) to be rejected")
	}
	if err := reg.SetMaxDelegationDepth(-1); err == nil {
		t.Fatal("expected SetMaxDelegationDepth(-1) to be rejected")
	}
}

// TestDefaultMaxDelegationDepthAllowsFiveLinks proves the default
// policy is generous enough for a real-world chain before any override.
func TestDefaultMaxDelegationDepthAllowsFiveLinks(t *testing.T) {
	reg, _ := NewRelationshipRegistry("CASE-1")
	prev := ""
	for i := 1; i <= DefaultMaxDelegationDepth; i++ {
		id := fmt.Sprintf("REL-%d", i)
		r, err := New(id, "CASE-1", PartyID("PTY-"+id), "PTY-ROOT", RoleBroker, 0)
		if err != nil {
			t.Fatalf("New(%s): %v", id, err)
		}
		r.CanDelegate = true
		r.DelegatedFrom = prev
		if err := reg.Register(r); err != nil {
			t.Fatalf("Register(%s) at depth %d (== default max): %v", id, i, err)
		}
		prev = id
	}
}
