package party

import (
	"errors"
	"testing"
)

func TestRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	p, err := r.Register("PTY-001", "Acme Shipping Ltd", RoleCarrier)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !p.HasRole(RoleCarrier) {
		t.Fatal("expected RoleCarrier")
	}
	got, ok := r.Get("PTY-001")
	if !ok {
		t.Fatal("expected party to be found")
	}
	if got.Name != "Acme Shipping Ltd" {
		t.Fatalf("unexpected name %q", got.Name)
	}
}

func TestRegisterRejectsEmptyID(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Register("", "X", RoleInsured); err != ErrEmptyPartyID {
		t.Fatalf("expected ErrEmptyPartyID, got %v", err)
	}
}

func TestRegisterRejectsEmptyName(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Register("PTY-001", "", RoleInsured); err != ErrEmptyName {
		t.Fatalf("expected ErrEmptyName, got %v", err)
	}
}

func TestRegisterRejectsNoRoles(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Register("PTY-001", "X"); err != ErrNoRoles {
		t.Fatalf("expected ErrNoRoles, got %v", err)
	}
}

func TestRegisterRejectsUnknownRole(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Register("PTY-001", "X", Role("MADE_UP_ROLE")); err == nil {
		t.Fatal("expected error for unknown role")
	}
}

func TestRegisterRejectsDuplicateID(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Register("PTY-001", "X", RoleInsured); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if _, err := r.Register("PTY-001", "Y", RoleClaimant); err == nil {
		t.Fatal("expected duplicate PartyID to be refused")
	}
}

// TestEntityWithMultipleRoles reproduces the blueprint's own worked
// example (§3): "Entity X: CARGO_OWNER + INSURED".
func TestEntityWithMultipleRoles(t *testing.T) {
	r := NewRegistry()
	p, err := r.Register("PTY-001", "Entity X", RoleCargoOwner)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := r.AddRole(p.PartyID, RoleInsured); err != nil {
		t.Fatalf("AddRole: %v", err)
	}
	got, _ := r.Get("PTY-001")
	if !got.HasRole(RoleCargoOwner) || !got.HasRole(RoleInsured) {
		t.Fatalf("expected both roles, got %v", got.RoleList())
	}
	roles := got.RoleList()
	if len(roles) != 2 {
		t.Fatalf("expected exactly 2 roles, got %d: %v", len(roles), roles)
	}
}

func TestAddRoleRejectsUnknownRole(t *testing.T) {
	r := NewRegistry()
	p, _ := r.Register("PTY-001", "X", RoleInsured)
	if err := r.AddRole(p.PartyID, Role("NOT_A_ROLE")); err == nil {
		t.Fatal("expected error")
	}
}

func TestAddRoleRejectsUnknownParty(t *testing.T) {
	r := NewRegistry()
	if err := r.AddRole("PTY-999", RoleInsured); !errors.Is(err, ErrPartyNotFound) {
		t.Fatalf("expected ErrPartyNotFound, got %v", err)
	}
}

func TestWithRole(t *testing.T) {
	r := NewRegistry()
	r.Register("PTY-001", "Insurer A", RoleInsurer)
	r.Register("PTY-002", "Surveyor B", RoleSurveyor)
	r.Register("PTY-003", "Insurer C", RoleInsurer)

	insurers := r.WithRole(RoleInsurer)
	if len(insurers) != 2 {
		t.Fatalf("expected 2 insurers, got %d", len(insurers))
	}
	if insurers[0].PartyID != "PTY-001" || insurers[1].PartyID != "PTY-003" {
		t.Fatalf("expected registration order, got %v, %v", insurers[0].PartyID, insurers[1].PartyID)
	}
}

func TestSetEntityRef(t *testing.T) {
	r := NewRegistry()
	r.Register("PTY-001", "X", RoleCarrier)
	if err := r.SetEntityRef("PTY-001", "entity:IMO1234567"); err != nil {
		t.Fatalf("SetEntityRef: %v", err)
	}
	got, _ := r.Get("PTY-001")
	if got.EntityRef != "entity:IMO1234567" {
		t.Fatalf("unexpected EntityRef %q", got.EntityRef)
	}
}

func TestSetEntityRefRejectsUnknownParty(t *testing.T) {
	r := NewRegistry()
	if err := r.SetEntityRef("PTY-999", "x"); !errors.Is(err, ErrPartyNotFound) {
		t.Fatalf("expected ErrPartyNotFound, got %v", err)
	}
}

func TestAllPreservesRegistrationOrder(t *testing.T) {
	r := NewRegistry()
	r.Register("PTY-003", "C", RoleInsured)
	r.Register("PTY-001", "A", RoleInsured)
	r.Register("PTY-002", "B", RoleInsured)
	all := r.All()
	if len(all) != 3 {
		t.Fatalf("expected 3 parties, got %d", len(all))
	}
	if all[0].PartyID != "PTY-003" || all[1].PartyID != "PTY-001" || all[2].PartyID != "PTY-002" {
		t.Fatalf("expected registration order, got %v", all)
	}
}

func TestKnownRolesCoversTheBlueprintList(t *testing.T) {
	// The earlier blueprint (§3) enumerated exactly 21 roles, and this
	// test asserted that count. Round R21 added nine more, each named
	// explicitly by one of the two frozen Insurance design documents:
	//
	//   - WAREHOUSE, STEVEDORE, SHIP_MANAGER, MANUFACTURER,
	//     LOGISTICS_PROVIDER and COUNTERPARTY are in the functional
	//     spec §30's own list of potential responsible parties for
	//     recovery;
	//   - RESPONDENT is the counterpart to CLAIMANT once a claim becomes
	//     a dispute (I-08);
	//   - REGULATOR and AUDITOR are required by I-08's regulatory half
	//     and the functional spec §77 independent-review model.
	//
	// The count is asserted rather than dropped, so adding a role stays
	// the deliberate, auditable act this package's own doc comment
	// describes. A further round (the VERIQO Master Closure Mandate §10,
	// "the real-world insurance network") added ten more: COVERHOLDER_MGA,
	// UNDERWRITER, CO_INSURER, CLAIMS_HANDLER, AVERAGE_ADJUSTER, EXPERT,
	// SALVAGE_PARTY, RECOVERY_PARTY, REPAIRER, BANK_TRADE_FINANCE.
	const blueprintRoles, r21AddedRoles, masterAddedRoles = 21, 9, 10
	if got := len(KnownRoles()); got != blueprintRoles+r21AddedRoles+masterAddedRoles {
		t.Fatalf("expected %d known roles (%d from VICE blueprint §3 plus %d added in R21 plus %d added for the "+
			"real-world insurance network), got %d",
			blueprintRoles+r21AddedRoles+masterAddedRoles, blueprintRoles, r21AddedRoles, masterAddedRoles, got)
	}
	// Each added role must genuinely be recognised, not merely counted.
	for _, r := range []Role{
		RoleWarehouse, RoleStevedore, RoleShipManager, RoleManufacturer,
		RoleLogisticsProvider, RoleCounterparty, RoleRespondent, RoleRegulator, RoleAuditor,
		RoleCoverholderMGA, RoleUnderwriter, RoleCoInsurer, RoleClaimsHandler,
		RoleAverageAdjuster, RoleExpert, RoleSalvageParty, RoleRecoveryParty,
		RoleRepairer, RoleBankTradeFinance,
	} {
		if !IsKnownRole(r) {
			t.Fatalf("role %q is declared but not registered as known", r)
		}
	}
}

func TestCount(t *testing.T) {
	r := NewRegistry()
	if r.Count() != 0 {
		t.Fatalf("expected empty registry to have count 0, got %d", r.Count())
	}
	r.Register("PTY-001", "X", RoleInsured)
	if r.Count() != 1 {
		t.Fatalf("expected count 1, got %d", r.Count())
	}
}
