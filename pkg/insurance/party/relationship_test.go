package party

import (
	"errors"
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
