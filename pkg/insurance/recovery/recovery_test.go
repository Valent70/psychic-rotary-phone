package recovery

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"veriqo/pkg/evidence/ontology"
	insuranceevidence "veriqo/pkg/insurance/evidence"
	"veriqo/pkg/insurance/party"
)

// ---- Basis ----

func TestBasisValidateAcceptsKnownCategory(t *testing.T) {
	b := Basis{Category: BasisBaileeLiability, Detail: "held cargo as bailee during discharge"}
	if err := b.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestBasisValidateRejectsUnknownCategory(t *testing.T) {
	b := Basis{Category: BasisCategory("NOT_A_CATEGORY")}
	if err := b.Validate(); !errors.Is(err, ErrUnknownBasisCategory) {
		t.Fatalf("expected ErrUnknownBasisCategory, got %v", err)
	}
}

func TestBasisValidateRejectsZeroValue(t *testing.T) {
	if err := (Basis{}).Validate(); !errors.Is(err, ErrUnknownBasisCategory) {
		t.Fatalf("expected ErrUnknownBasisCategory for zero-value Basis, got %v", err)
	}
}

func TestKnownBasisCategoriesCount(t *testing.T) {
	// CONTRACTUAL_BREACH, NEGLIGENCE, BAILEE_LIABILITY, STATUTORY_DUTY,
	// PRODUCT_OR_WORK_DEFECT, INDEMNITY_OR_CONTRIBUTION, OTHER.
	if got := len(KnownBasisCategories()); got != 7 {
		t.Fatalf("expected 7 known basis categories, got %d", got)
	}
}

// ---- Money ----

func TestMoneyValidateAcceptsZeroAmount(t *testing.T) {
	m := Money{AmountMinor: 0, Currency: "USD"}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestMoneyValidateRejectsNegativeAmount(t *testing.T) {
	m := Money{AmountMinor: -1, Currency: "USD"}
	if err := m.Validate(); !errors.Is(err, ErrNegativeAmount) {
		t.Fatalf("expected ErrNegativeAmount, got %v", err)
	}
}

func TestMoneyValidateRejectsEmptyCurrency(t *testing.T) {
	m := Money{AmountMinor: 100, Currency: ""}
	if err := m.Validate(); !errors.Is(err, ErrEmptyCurrency) {
		t.Fatalf("expected ErrEmptyCurrency, got %v", err)
	}
}

// ---- Target construction ----

func validBasis() Basis {
	return Basis{Category: BasisBaileeLiability, Detail: "custody interval covers the damage window"}
}

func validMoney() Money {
	return Money{AmountMinor: 250000, Currency: "USD"}
}

func TestNewSucceedsWithDefaults(t *testing.T) {
	tgt, err := New("RT-001", "CASE-1", party.PartyID("PTY-CARRIER-1"), validBasis(), validMoney())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if tgt.NoticeStatus != NoticeStatusPending {
		t.Fatalf("expected default NoticeStatusPending, got %s", tgt.NoticeStatus)
	}
	if tgt.LimitationStatus != LimitationUnknown {
		t.Fatalf("expected default LimitationUnknown, got %s", tgt.LimitationStatus)
	}
	if tgt.RecoveryStatus != RecoveryStatusIdentified {
		t.Fatalf("expected default RecoveryStatusIdentified, got %s", tgt.RecoveryStatus)
	}
}

func TestNewRejectsEmptyTargetID(t *testing.T) {
	if _, err := New("", "CASE-1", "PTY-1", validBasis(), validMoney()); !errors.Is(err, ErrEmptyTargetID) {
		t.Fatalf("expected ErrEmptyTargetID, got %v", err)
	}
}

func TestNewRejectsEmptyCaseID(t *testing.T) {
	if _, err := New("RT-001", "", "PTY-1", validBasis(), validMoney()); !errors.Is(err, ErrEmptyCaseID) {
		t.Fatalf("expected ErrEmptyCaseID, got %v", err)
	}
}

func TestNewRejectsEmptyParty(t *testing.T) {
	if _, err := New("RT-001", "CASE-1", "", validBasis(), validMoney()); !errors.Is(err, ErrEmptyParty) {
		t.Fatalf("expected ErrEmptyParty, got %v", err)
	}
}

func TestNewRejectsInvalidBasis(t *testing.T) {
	bad := Basis{Category: BasisCategory("BOGUS")}
	if _, err := New("RT-001", "CASE-1", "PTY-1", bad, validMoney()); !errors.Is(err, ErrUnknownBasisCategory) {
		t.Fatalf("expected ErrUnknownBasisCategory, got %v", err)
	}
}

func TestNewRejectsInvalidMoney(t *testing.T) {
	bad := Money{AmountMinor: -5, Currency: "USD"}
	if _, err := New("RT-001", "CASE-1", "PTY-1", validBasis(), bad); !errors.Is(err, ErrNegativeAmount) {
		t.Fatalf("expected ErrNegativeAmount, got %v", err)
	}
}

func TestTargetValidateRejectsUnknownStatuses(t *testing.T) {
	tgt, err := New("RT-001", "CASE-1", "PTY-1", validBasis(), validMoney())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	bad := tgt
	bad.NoticeStatus = NoticeStatus("BOGUS")
	if err := bad.Validate(); !errors.Is(err, ErrUnknownNoticeStatus) {
		t.Fatalf("expected ErrUnknownNoticeStatus, got %v", err)
	}

	bad = tgt
	bad.LimitationStatus = LimitationStatus("BOGUS")
	if err := bad.Validate(); !errors.Is(err, ErrUnknownLimitationStatus) {
		t.Fatalf("expected ErrUnknownLimitationStatus, got %v", err)
	}

	bad = tgt
	bad.RecoveryStatus = RecoveryStatus("BOGUS")
	if err := bad.Validate(); !errors.Is(err, ErrUnknownRecoveryStatus) {
		t.Fatalf("expected ErrUnknownRecoveryStatus, got %v", err)
	}
}

// ---- Registry ----

func TestNewRegistryRejectsEmptyCaseID(t *testing.T) {
	if _, err := NewRegistry(""); !errors.Is(err, ErrEmptyCaseID) {
		t.Fatalf("expected ErrEmptyCaseID, got %v", err)
	}
}

func TestRegistryRegisterAndGet(t *testing.T) {
	reg, err := NewRegistry("CASE-1")
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	tgt, err := New("RT-001", "CASE-1", "PTY-1", validBasis(), validMoney())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := reg.Register(tgt); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, ok := reg.Get("RT-001")
	if !ok {
		t.Fatal("expected target to be found")
	}
	if got.Party != "PTY-1" {
		t.Fatalf("unexpected party %q", got.Party)
	}
}

func TestRegistryRegisterRejectsCaseIDMismatch(t *testing.T) {
	reg, _ := NewRegistry("CASE-1")
	tgt, _ := New("RT-001", "CASE-2", "PTY-1", validBasis(), validMoney())
	if err := reg.Register(tgt); !errors.Is(err, ErrCaseIDMismatch) {
		t.Fatalf("expected ErrCaseIDMismatch, got %v", err)
	}
}

func TestRegistryRegisterRejectsDuplicateTargetID(t *testing.T) {
	reg, _ := NewRegistry("CASE-1")
	tgt, _ := New("RT-001", "CASE-1", "PTY-1", validBasis(), validMoney())
	if err := reg.Register(tgt); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := reg.Register(tgt); !errors.Is(err, ErrDuplicateTarget) {
		t.Fatalf("expected ErrDuplicateTarget, got %v", err)
	}
}

func TestRegistryRegisterRejectsInvalidTarget(t *testing.T) {
	reg, _ := NewRegistry("CASE-1")
	if err := reg.Register(Target{}); err == nil {
		t.Fatal("expected error registering zero-value Target")
	}
}

func TestRegistryAllPreservesRegistrationOrder(t *testing.T) {
	reg, _ := NewRegistry("CASE-1")
	for _, id := range []string{"RT-003", "RT-001", "RT-002"} {
		tgt, _ := New(id, "CASE-1", "PTY-1", validBasis(), validMoney())
		if err := reg.Register(tgt); err != nil {
			t.Fatalf("Register(%s): %v", id, err)
		}
	}
	all := reg.All()
	if len(all) != 3 {
		t.Fatalf("expected 3 targets, got %d", len(all))
	}
	if all[0].TargetID != "RT-003" || all[1].TargetID != "RT-001" || all[2].TargetID != "RT-002" {
		t.Fatalf("expected registration order, got %v", all)
	}
}

func TestRegistryByParty(t *testing.T) {
	reg, _ := NewRegistry("CASE-1")
	t1, _ := New("RT-001", "CASE-1", "PTY-CARRIER", validBasis(), validMoney())
	t2, _ := New("RT-002", "CASE-1", "PTY-TERMINAL", validBasis(), validMoney())
	t3, _ := New("RT-003", "CASE-1", "PTY-CARRIER", validBasis(), validMoney())
	for _, tgt := range []Target{t1, t2, t3} {
		if err := reg.Register(tgt); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}
	got := reg.ByParty("PTY-CARRIER")
	if len(got) != 2 {
		t.Fatalf("expected 2 targets for PTY-CARRIER, got %d", len(got))
	}
	if got[0].TargetID != "RT-001" || got[1].TargetID != "RT-003" {
		t.Fatalf("expected registration order, got %v", got)
	}
}

func TestRegistryByRecoveryStatus(t *testing.T) {
	reg, _ := NewRegistry("CASE-1")
	tgt, _ := New("RT-001", "CASE-1", "PTY-1", validBasis(), validMoney())
	reg.Register(tgt)
	if got := reg.ByRecoveryStatus(RecoveryStatusIdentified); len(got) != 1 {
		t.Fatalf("expected 1 identified target, got %d", len(got))
	}
	if err := reg.SetRecoveryStatus("RT-001", RecoveryStatusPursuing); err != nil {
		t.Fatalf("SetRecoveryStatus: %v", err)
	}
	if got := reg.ByRecoveryStatus(RecoveryStatusIdentified); len(got) != 0 {
		t.Fatalf("expected 0 identified targets after transition, got %d", len(got))
	}
	if got := reg.ByRecoveryStatus(RecoveryStatusPursuing); len(got) != 1 {
		t.Fatalf("expected 1 pursuing target, got %d", len(got))
	}
}

func TestSetRecoveryStatusRejectsUnknown(t *testing.T) {
	reg, _ := NewRegistry("CASE-1")
	tgt, _ := New("RT-001", "CASE-1", "PTY-1", validBasis(), validMoney())
	reg.Register(tgt)
	if err := reg.SetRecoveryStatus("RT-001", RecoveryStatus("BOGUS")); !errors.Is(err, ErrUnknownRecoveryStatus) {
		t.Fatalf("expected ErrUnknownRecoveryStatus, got %v", err)
	}
}

func TestSetRecoveryStatusRejectsUnknownTarget(t *testing.T) {
	reg, _ := NewRegistry("CASE-1")
	if err := reg.SetRecoveryStatus("RT-999", RecoveryStatusPursuing); !errors.Is(err, ErrTargetNotFound) {
		t.Fatalf("expected ErrTargetNotFound, got %v", err)
	}
}

func TestSetNoticeStatus(t *testing.T) {
	reg, _ := NewRegistry("CASE-1")
	tgt, _ := New("RT-001", "CASE-1", "PTY-1", validBasis(), validMoney())
	reg.Register(tgt)
	if err := reg.SetNoticeStatus("RT-001", NoticeStatusSent); err != nil {
		t.Fatalf("SetNoticeStatus: %v", err)
	}
	got, _ := reg.Get("RT-001")
	if got.NoticeStatus != NoticeStatusSent {
		t.Fatalf("expected NoticeStatusSent, got %s", got.NoticeStatus)
	}
}

func TestSetNoticeStatusRejectsUnknown(t *testing.T) {
	reg, _ := NewRegistry("CASE-1")
	tgt, _ := New("RT-001", "CASE-1", "PTY-1", validBasis(), validMoney())
	reg.Register(tgt)
	if err := reg.SetNoticeStatus("RT-001", NoticeStatus("BOGUS")); !errors.Is(err, ErrUnknownNoticeStatus) {
		t.Fatalf("expected ErrUnknownNoticeStatus, got %v", err)
	}
}

func TestSetLimitationStatusDirect(t *testing.T) {
	reg, _ := NewRegistry("CASE-1")
	tgt, _ := New("RT-001", "CASE-1", "PTY-1", validBasis(), validMoney())
	reg.Register(tgt)
	if err := reg.SetLimitationStatus("RT-001", LimitationExpired); err != nil {
		t.Fatalf("SetLimitationStatus: %v", err)
	}
	got, _ := reg.Get("RT-001")
	if got.LimitationStatus != LimitationExpired {
		t.Fatalf("expected LimitationExpired, got %s", got.LimitationStatus)
	}
}

func TestSetLimitationStatusRejectsUnknown(t *testing.T) {
	reg, _ := NewRegistry("CASE-1")
	tgt, _ := New("RT-001", "CASE-1", "PTY-1", validBasis(), validMoney())
	reg.Register(tgt)
	if err := reg.SetLimitationStatus("RT-001", LimitationStatus("BOGUS")); !errors.Is(err, ErrUnknownLimitationStatus) {
		t.Fatalf("expected ErrUnknownLimitationStatus, got %v", err)
	}
}

func TestAddSupportingEvidence(t *testing.T) {
	reg, _ := NewRegistry("CASE-1")
	tgt, _ := New("RT-001", "CASE-1", "PTY-1", validBasis(), validMoney())
	reg.Register(tgt)

	// Build a real evidence.Record and reference it by its own EvidenceID,
	// exactly as the blueprint requires: supporting_evidence is a list of
	// evidence.Record EvidenceIDs, not free-text.
	ev, err := ontology.New(ontology.Evidence{
		Type:       ontology.TypeDocument,
		Subject:    "survey-004",
		Predicate:  "describes",
		Object:     "container_seal_condition",
		Source:     "surveyor",
		ObservedAt: 100,
		Confidence: 0.9,
		Attributes: map[string]string{"document_hash": "abc123"},
	})
	if err != nil {
		t.Fatalf("ontology.New: %v", err)
	}
	rec, err := insuranceevidence.New("CASE-1", ev, "PTY-SURVEYOR", insuranceevidence.OriginSurveyor)
	if err != nil {
		t.Fatalf("insuranceevidence.New: %v", err)
	}

	if err := reg.AddSupportingEvidence("RT-001", rec.EvidenceID()); err != nil {
		t.Fatalf("AddSupportingEvidence: %v", err)
	}
	got, _ := reg.Get("RT-001")
	if len(got.SupportingEvidence) != 1 || got.SupportingEvidence[0] != rec.EvidenceID() {
		t.Fatalf("expected supporting_evidence to contain %q, got %v", rec.EvidenceID(), got.SupportingEvidence)
	}
}

func TestAddSupportingEvidenceRejectsEmpty(t *testing.T) {
	reg, _ := NewRegistry("CASE-1")
	tgt, _ := New("RT-001", "CASE-1", "PTY-1", validBasis(), validMoney())
	reg.Register(tgt)
	if err := reg.AddSupportingEvidence("RT-001", ""); !errors.Is(err, ErrEmptyEvidenceID) {
		t.Fatalf("expected ErrEmptyEvidenceID, got %v", err)
	}
}

func TestAddSupportingEvidenceRejectsUnknownTarget(t *testing.T) {
	reg, _ := NewRegistry("CASE-1")
	if err := reg.AddSupportingEvidence("RT-999", "EV-1"); !errors.Is(err, ErrTargetNotFound) {
		t.Fatalf("expected ErrTargetNotFound, got %v", err)
	}
}

func TestRegistryCount(t *testing.T) {
	reg, _ := NewRegistry("CASE-1")
	if reg.Count() != 0 {
		t.Fatalf("expected empty registry to have count 0, got %d", reg.Count())
	}
	tgt, _ := New("RT-001", "CASE-1", "PTY-1", validBasis(), validMoney())
	reg.Register(tgt)
	if reg.Count() != 1 {
		t.Fatalf("expected count 1, got %d", reg.Count())
	}
}

// ---- Limitation: real computed logic ----

func TestComputeLimitationStatus(t *testing.T) {
	cases := []struct {
		name         string
		deadlineTick uint64
		nowTick      uint64
		want         LimitationStatus
	}{
		{"no deadline established", 0, 12345, LimitationUnknown},
		{"well within the period", 1000, 500, LimitationWithin},
		{"one tick before deadline", 1000, 999, LimitationWithin},
		{"exactly at deadline is expired", 1000, 1000, LimitationExpired},
		{"past the deadline", 1000, 1500, LimitationExpired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ComputeLimitationStatus(tc.deadlineTick, tc.nowTick); got != tc.want {
				t.Fatalf("ComputeLimitationStatus(%d, %d) = %s, want %s", tc.deadlineTick, tc.nowTick, got, tc.want)
			}
		})
	}
}

func TestTargetLimitationStatusAt(t *testing.T) {
	tgt, err := New("RT-001", "CASE-1", "PTY-1", validBasis(), validMoney())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tgt.LimitationDeadlineTick = 1000

	if got := tgt.LimitationStatusAt(500); got != LimitationWithin {
		t.Fatalf("expected LimitationWithin, got %s", got)
	}
	if got := tgt.LimitationStatusAt(1000); got != LimitationExpired {
		t.Fatalf("expected LimitationExpired, got %s", got)
	}
	// LimitationStatusAt must not mutate the stored field.
	if tgt.LimitationStatus != LimitationUnknown {
		t.Fatalf("expected stored LimitationStatus to remain unchanged (still UNKNOWN), got %s", tgt.LimitationStatus)
	}
}

func TestRegistryRefreshLimitationStatus(t *testing.T) {
	reg, _ := NewRegistry("CASE-1")
	tgt, _ := New("RT-001", "CASE-1", "PTY-1", validBasis(), validMoney())
	reg.Register(tgt)

	if err := reg.SetLimitationDeadline("RT-001", 1000); err != nil {
		t.Fatalf("SetLimitationDeadline: %v", err)
	}

	// Before refresh, the stored field is still the New() default.
	got, _ := reg.Get("RT-001")
	if got.LimitationStatus != LimitationUnknown {
		t.Fatalf("expected stored status still UNKNOWN before refresh, got %s", got.LimitationStatus)
	}

	status, err := reg.RefreshLimitationStatus("RT-001", 500)
	if err != nil {
		t.Fatalf("RefreshLimitationStatus: %v", err)
	}
	if status != LimitationWithin {
		t.Fatalf("expected LimitationWithin, got %s", status)
	}
	got, _ = reg.Get("RT-001")
	if got.LimitationStatus != LimitationWithin {
		t.Fatalf("expected stored status updated to WITHIN_LIMITATION, got %s", got.LimitationStatus)
	}

	// Advance time past the deadline: refresh must transition to EXPIRED.
	status, err = reg.RefreshLimitationStatus("RT-001", 1500)
	if err != nil {
		t.Fatalf("RefreshLimitationStatus: %v", err)
	}
	if status != LimitationExpired {
		t.Fatalf("expected LimitationExpired, got %s", status)
	}
	got, _ = reg.Get("RT-001")
	if got.LimitationStatus != LimitationExpired {
		t.Fatalf("expected stored status updated to EXPIRED, got %s", got.LimitationStatus)
	}
}

func TestRegistryRefreshLimitationStatusRejectsUnknownTarget(t *testing.T) {
	reg, _ := NewRegistry("CASE-1")
	if _, err := reg.RefreshLimitationStatus("RT-999", 100); !errors.Is(err, ErrTargetNotFound) {
		t.Fatalf("expected ErrTargetNotFound, got %v", err)
	}
}

func TestSetLimitationDeadlineRejectsUnknownTarget(t *testing.T) {
	reg, _ := NewRegistry("CASE-1")
	if err := reg.SetLimitationDeadline("RT-999", 100); !errors.Is(err, ErrTargetNotFound) {
		t.Fatalf("expected ErrTargetNotFound, got %v", err)
	}
}

// ---- The blueprint's own §23 worked list of potential responsible parties ----

func TestPotentialResponsiblePartyCategoriesMatchesBlueprintVerbatim(t *testing.T) {
	// Blueprint §23, verbatim order: Carrier, Terminal, Warehouse,
	// Forwarder, Manufacturer, Surveyor, Other third party.
	want := []ResponsiblePartyCategory{
		CategoryCarrier, CategoryTerminal, CategoryWarehouse, CategoryForwarder,
		CategoryManufacturer, CategorySurveyor, CategoryOtherThirdParty,
	}
	got := PotentialResponsiblePartyCategories()
	if len(got) != len(want) {
		t.Fatalf("expected %d categories, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("category %d: expected %s, got %s", i, want[i], got[i])
		}
	}
}

func TestCategoryPartyRoleMapping(t *testing.T) {
	// Categories with an existing party.Role, per pkg/insurance/party.go.
	roleCases := map[ResponsiblePartyCategory]party.Role{
		CategoryCarrier:         party.RoleCarrier,
		CategoryTerminal:        party.RoleTerminal,
		CategoryForwarder:       party.RoleForwarder,
		CategorySurveyor:        party.RoleSurveyor,
		CategoryOtherThirdParty: party.RoleOtherResponsibleParty,
	}
	for cat, wantRole := range roleCases {
		role, ok := CategoryPartyRole(cat)
		if !ok {
			t.Fatalf("expected %s to have a known party.Role", cat)
		}
		if role != wantRole {
			t.Fatalf("%s: expected role %s, got %s", cat, wantRole, role)
		}
		if !party.IsKnownRole(role) {
			t.Fatalf("%s: mapped role %s is not a known party.Role", cat, role)
		}
	}

	// Warehouse and Manufacturer have NO corresponding party.Role today
	// (see package doc and blueprint task instructions point 2) — the
	// gap must be reported honestly, never silently invented.
	for _, cat := range []ResponsiblePartyCategory{CategoryWarehouse, CategoryManufacturer} {
		if _, ok := CategoryPartyRole(cat); ok {
			t.Fatalf("expected %s to have NO known party.Role (this is a real gap in pkg/insurance/party, not one this package should paper over)", cat)
		}
	}
}

// TestRegisterOneTargetPerBlueprintCategory is the required worked test:
// register one recovery Target per named category from the blueprint's
// own §23 list, using party.PartyID plus whatever party.Role applies
// where one exists, and confirm the Registry correctly holds and
// returns all of them.
func TestRegisterOneTargetPerBlueprintCategory(t *testing.T) {
	reg, err := NewRegistry("CASE-SUBROGATION-1")
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	categories := PotentialResponsiblePartyCategories()
	if len(categories) != 7 {
		t.Fatalf("expected the blueprint's 7 categories, got %d", len(categories))
	}

	for i, cat := range categories {
		targetID := fmt.Sprintf("RT-%03d", i+1)
		partyID := party.PartyID(fmt.Sprintf("PTY-%s", cat))

		basis := Basis{Category: BasisBaileeLiability, Detail: fmt.Sprintf("potential recovery avenue against %s", strings.ToLower(string(cat)))}
		tgt, err := New(targetID, "CASE-SUBROGATION-1", partyID, basis, Money{AmountMinor: 500000, Currency: "USD"})
		if err != nil {
			t.Fatalf("New(%s): %v", cat, err)
		}
		if role, ok := CategoryPartyRole(cat); ok {
			tgt.PartyRole = role
		}
		if err := reg.Register(tgt); err != nil {
			t.Fatalf("Register(%s): %v", cat, err)
		}
	}

	all := reg.All()
	if len(all) != 7 {
		t.Fatalf("expected 7 registered targets, got %d", len(all))
	}

	// Every category must be present exactly once, findable by its
	// deterministic PartyID, and role-tagged (or not) exactly per
	// CategoryPartyRole.
	for i, cat := range categories {
		targetID := fmt.Sprintf("RT-%03d", i+1)
		got, ok := reg.Get(targetID)
		if !ok {
			t.Fatalf("expected target %s (category %s) to be registered", targetID, cat)
		}
		wantPartyID := party.PartyID(fmt.Sprintf("PTY-%s", cat))
		if got.Party != wantPartyID {
			t.Fatalf("%s: expected party %s, got %s", cat, wantPartyID, got.Party)
		}
		wantRole, hasRole := CategoryPartyRole(cat)
		if hasRole {
			if got.PartyRole != wantRole {
				t.Fatalf("%s: expected PartyRole %s, got %s", cat, wantRole, got.PartyRole)
			}
		} else if got.PartyRole != "" {
			t.Fatalf("%s: expected zero-value PartyRole (no known party.Role), got %s", cat, got.PartyRole)
		}
	}
}

// ---- Golden rule: no legal-liability-determination field anywhere ----
//
// Blueprint §0, absolute principle: "VICE MUST NOT: ... determine legal
// liability finally". This test enforces that mechanically, not just by
// convention: it reflects over every exported field of every exported
// struct type in this package and fails if any field name or JSON tag
// reads as a liability/fault verdict. A legitimate legal-THEORY value
// like BasisCategory's "BAILEE_LIABILITY" is fine — it names an avenue
// worth investigating, stored under a field called "Category", not a
// field called anything like "Liability" or "IsLiable". What this test
// actually forbids is a FIELD NAME suggesting a determination, e.g.
// "Liable", "LiabilityConfirmed", "IsAtFault", "Guilty", "Verdict".
func TestNoLiabilityDeterminationField(t *testing.T) {
	forbiddenFieldSubstrings := []string{
		"liab",    // "Liable", "Liability" as a FIELD name (not as an enum value inside Basis.Category)
		"guilt",   // "Guilty"
		"verdict", // "Verdict"
		"atfault", // "IsAtFault", "AtFault"
		"culpab",  // "Culpable", "Culpability"
		"confirmedresponsible",
	}

	typesToCheck := []reflect.Type{
		reflect.TypeOf(Target{}),
		reflect.TypeOf(Basis{}),
		reflect.TypeOf(Money{}),
	}

	for _, typ := range typesToCheck {
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			nameLower := strings.ToLower(f.Name)
			tagLower := strings.ToLower(f.Tag.Get("json"))
			for _, forbidden := range forbiddenFieldSubstrings {
				if strings.Contains(nameLower, forbidden) {
					t.Fatalf("type %s field %q looks like a liability-determination field (matched %q) — VICE must never produce a final legal liability determination (blueprint §0)", typ.Name(), f.Name, forbidden)
				}
				if strings.Contains(tagLower, forbidden) {
					t.Fatalf("type %s field %q has json tag %q that looks like a liability-determination field (matched %q)", typ.Name(), f.Name, f.Tag.Get("json"), forbidden)
				}
			}
			// Specifically: no bare bool field anywhere in Target. A
			// bool is exactly the shape a smuggled-in "liable: true/false"
			// verdict would take, and this package has no legitimate use
			// for one (every status this package models is a closed
			// multi-value enum precisely so nothing collapses to a
			// yes/no verdict).
			if f.Type.Kind() == reflect.Bool {
				t.Fatalf("type %s field %q is a bool — this package deliberately has none, to avoid the shape of a smuggled-in liability verdict", typ.Name(), f.Name)
			}
		}
	}

	// Every RecoveryStatus value itself must not read as a liability
	// verdict either -- it is a workflow state, not a finding.
	forbiddenStatusSubstrings := []string{"LIABLE", "GUILTY", "VERDICT", "AT_FAULT", "CULPABLE"}
	for _, s := range KnownRecoveryStatuses() {
		up := strings.ToUpper(string(s))
		for _, forbidden := range forbiddenStatusSubstrings {
			if strings.Contains(up, forbidden) {
				t.Fatalf("RecoveryStatus value %q looks like a liability verdict (matched %q)", s, forbidden)
			}
		}
	}
}

// ---- Enum coverage sanity checks ----

func TestKnownNoticeStatusesCount(t *testing.T) {
	if got := len(KnownNoticeStatuses()); got != 3 {
		t.Fatalf("expected 3 known notice statuses, got %d", got)
	}
}

func TestKnownLimitationStatusesCount(t *testing.T) {
	if got := len(KnownLimitationStatuses()); got != 3 {
		t.Fatalf("expected 3 known limitation statuses, got %d", got)
	}
}

func TestKnownRecoveryStatusesCount(t *testing.T) {
	if got := len(KnownRecoveryStatuses()); got != 5 {
		t.Fatalf("expected 5 known recovery statuses, got %d", got)
	}
}

// ---- Concurrency ----

// TestRegistryConcurrentAccess exercises Registry under concurrent
// registration and status updates. Run with -race to verify the
// sync.RWMutex actually protects every field.
func TestRegistryConcurrentAccess(t *testing.T) {
	reg, err := NewRegistry("CASE-CONCURRENT")
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("RT-%03d", i)
			tgt, err := New(id, "CASE-CONCURRENT", "PTY-1", validBasis(), validMoney())
			if err != nil {
				t.Errorf("New(%s): %v", id, err)
				return
			}
			if err := reg.Register(tgt); err != nil {
				t.Errorf("Register(%s): %v", id, err)
				return
			}
			if err := reg.SetNoticeStatus(id, NoticeStatusSent); err != nil {
				t.Errorf("SetNoticeStatus(%s): %v", id, err)
			}
			if err := reg.SetRecoveryStatus(id, RecoveryStatusPursuing); err != nil {
				t.Errorf("SetRecoveryStatus(%s): %v", id, err)
			}
			if err := reg.AddSupportingEvidence(id, "EV-CONCURRENT"); err != nil {
				t.Errorf("AddSupportingEvidence(%s): %v", id, err)
			}
			_ = reg.Count()
			_ = reg.All()
		}(i)
	}
	wg.Wait()

	if reg.Count() != n {
		t.Fatalf("expected %d targets, got %d", n, reg.Count())
	}
}
