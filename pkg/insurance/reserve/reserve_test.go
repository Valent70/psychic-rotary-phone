package reserve

import (
	"testing"

	"veriqo/pkg/insurance/party"
	"veriqo/pkg/insurance/quantum"
)

func mustNew(t *testing.T) *Reserve {
	t.Helper()
	r, err := New("RSV-1", "CLM-1", "CASE-1", quantum.MajorUnits(10_000), "PTY-HANDLER", party.RoleClaimsHandler, "initial estimate", 100)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

func TestNewRequiresReserveAuthority(t *testing.T) {
	_, err := New("RSV-1", "CLM-1", "CASE-1", quantum.MajorUnits(1_000), "PTY-X", party.RoleCarrier, "reason", 100)
	if err == nil {
		t.Fatal("expected error: RoleCarrier has no reserve authority")
	}
}

func TestNewRejectsNegativeAmount(t *testing.T) {
	_, err := New("RSV-1", "CLM-1", "CASE-1", -1, "PTY-HANDLER", party.RoleClaimsHandler, "reason", 100)
	if err == nil {
		t.Fatal("expected error for negative amount")
	}
}

func TestCurrentAmountReflectsLatestSetOrRevise(t *testing.T) {
	r := mustNew(t)
	if got := r.CurrentAmount(); got != quantum.MajorUnits(10_000) {
		t.Fatalf("CurrentAmount = %v, want 10,000", got)
	}
	if err := r.Revise(quantum.MajorUnits(15_000), "PTY-HANDLER", party.RoleClaimsHandler, "new survey report", 200); err != nil {
		t.Fatalf("Revise: %v", err)
	}
	if got := r.CurrentAmount(); got != quantum.MajorUnits(15_000) {
		t.Fatalf("CurrentAmount after revise = %v, want 15,000", got)
	}
	if r.Status() != StatusProposed {
		t.Fatalf("Status after revise = %v, want PROPOSED (revision requires fresh approval)", r.Status())
	}
}

func TestApproveEnforcesSegregationOfDuties(t *testing.T) {
	r := mustNew(t)
	if err := r.Approve("PTY-HANDLER", party.RoleClaimsHandler, 150); err != ErrSelfApproval {
		t.Fatalf("expected ErrSelfApproval, got %v", err)
	}
	if err := r.Approve("PTY-INSURER", party.RoleInsurer, 150); err != nil {
		t.Fatalf("Approve by a different party: %v", err)
	}
	if r.Status() != StatusApproved {
		t.Fatalf("Status after approve = %v, want APPROVED", r.Status())
	}
}

func TestApproveRefusesWhenNothingPending(t *testing.T) {
	r := mustNew(t)
	if err := r.Approve("PTY-INSURER", party.RoleInsurer, 150); err != nil {
		t.Fatalf("first approve: %v", err)
	}
	if err := r.Approve("PTY-ADJUSTER", party.RoleLossAdjuster, 160); err != ErrAlreadyApproved {
		t.Fatalf("expected ErrAlreadyApproved, got %v", err)
	}
}

func TestReviseAfterApprovalRequiresFreshApproval(t *testing.T) {
	r := mustNew(t)
	if err := r.Approve("PTY-INSURER", party.RoleInsurer, 150); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := r.Revise(quantum.MajorUnits(20_000), "PTY-ADJUSTER", party.RoleLossAdjuster, "revised estimate", 200); err != nil {
		t.Fatalf("revise: %v", err)
	}
	if r.Status() != StatusProposed {
		t.Fatal("a revision after approval must reset to PROPOSED, not silently stay APPROVED")
	}
}

func TestReleaseRequiresApprovedStatus(t *testing.T) {
	r := mustNew(t)
	if err := r.Release("PTY-INSURER", "claim closed", 300); err != ErrNotApproved {
		t.Fatalf("expected ErrNotApproved releasing an unapproved reserve, got %v", err)
	}
	if err := r.Approve("PTY-INSURER", party.RoleInsurer, 150); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := r.Release("PTY-INSURER", "claim closed", 300); err != nil {
		t.Fatalf("release: %v", err)
	}
	if r.Status() != StatusReleased {
		t.Fatalf("Status after release = %v, want RELEASED", r.Status())
	}
}

func TestReleasedReserveRefusesFurtherChange(t *testing.T) {
	r := mustNew(t)
	_ = r.Approve("PTY-INSURER", party.RoleInsurer, 150)
	_ = r.Release("PTY-INSURER", "claim closed", 300)
	if err := r.Revise(quantum.MajorUnits(1), "PTY-INSURER", party.RoleInsurer, "reason", 400); err != ErrReleasedNoChange {
		t.Fatalf("expected ErrReleasedNoChange, got %v", err)
	}
	if err := r.Release("PTY-INSURER", "again", 400); err != ErrAlreadyReleased {
		t.Fatalf("expected ErrAlreadyReleased, got %v", err)
	}
}

func TestHistoryRecordsEveryChangeInOrder(t *testing.T) {
	r := mustNew(t)
	_ = r.Approve("PTY-INSURER", party.RoleInsurer, 150)
	_ = r.Revise(quantum.MajorUnits(12_000), "PTY-ADJUSTER", party.RoleLossAdjuster, "updated", 200)
	_ = r.Approve("PTY-INSURER", party.RoleInsurer, 250)
	_ = r.Release("PTY-INSURER", "paid", 300)
	h := r.History()
	wantActions := []Action{ActionSet, ActionApprove, ActionRevise, ActionApprove, ActionRelease}
	if len(h) != len(wantActions) {
		t.Fatalf("history length = %d, want %d: %+v", len(h), len(wantActions), h)
	}
	for i, want := range wantActions {
		if h[i].Action != want {
			t.Fatalf("history[%d].Action = %v, want %v", i, h[i].Action, want)
		}
	}
}

func TestReconcileReportsUnderOverAndAdequate(t *testing.T) {
	r := mustNew(t) // 10,000
	if rec := r.Reconcile(quantum.MajorUnits(10_000)); rec.Adequacy != AdequacyAdequate {
		t.Fatalf("Adequacy = %v, want ADEQUATE", rec.Adequacy)
	}
	if rec := r.Reconcile(quantum.MajorUnits(12_000)); rec.Adequacy != AdequacyUnderReserved {
		t.Fatalf("Adequacy = %v, want UNDER_RESERVED", rec.Adequacy)
	}
	if rec := r.Reconcile(quantum.MajorUnits(8_000)); rec.Adequacy != AdequacyOverReserved {
		t.Fatalf("Adequacy = %v, want OVER_RESERVED", rec.Adequacy)
	}
}

func TestReconcileNeverMutatesTheReserve(t *testing.T) {
	r := mustNew(t)
	before := r.CurrentAmount()
	_ = r.Reconcile(quantum.MajorUnits(999_999))
	if r.CurrentAmount() != before {
		t.Fatal("Reconcile must never mutate the reserve's own amount")
	}
}
