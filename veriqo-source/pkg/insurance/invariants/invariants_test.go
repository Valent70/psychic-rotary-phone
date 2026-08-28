package invariants

import (
	"testing"
)

func TestCheckPaymentWithinQuantum(t *testing.T) {
	if err := CheckPaymentWithinQuantum(100, 100); err != nil {
		t.Fatalf("expected exact match to pass, got %v", err)
	}
	if err := CheckPaymentWithinQuantum(90, 100); err != nil {
		t.Fatalf("expected a partial payment (less than quantum) to pass, got %v", err)
	}
	if err := CheckPaymentWithinQuantum(101, 100); err == nil {
		t.Fatal("expected payment exceeding quantum to be refused")
	}
}

func TestCheckReserveMatchesQuantumBasisRequiresExact(t *testing.T) {
	if err := CheckReserveMatchesQuantumBasis(100, 100); err != nil {
		t.Fatalf("expected exact match to pass, got %v", err)
	}
	if err := CheckReserveMatchesQuantumBasis(99, 100); err == nil {
		t.Fatal("expected a reserve below its own quantum basis to be refused (must be EXACT, not <=)")
	}
	if err := CheckReserveMatchesQuantumBasis(101, 100); err == nil {
		t.Fatal("expected a reserve above its own quantum basis to be refused")
	}
}

func TestCheckSettlementWithinAdjustment(t *testing.T) {
	if err := CheckSettlementWithinAdjustment(1000, 1000, 0); err != nil {
		t.Fatalf("expected exact settlement with zero adjustment to pass, got %v", err)
	}
	if err := CheckSettlementWithinAdjustment(1000, 995, 0); err == nil {
		t.Fatal("expected a settlement difference with zero documented adjustment to be refused")
	}
	if err := CheckSettlementWithinAdjustment(1000, 995, 10); err != nil {
		t.Fatalf("expected a settlement difference within the documented adjustment to pass, got %v", err)
	}
	if err := CheckSettlementWithinAdjustment(1000, 985, 10); err == nil {
		t.Fatal("expected a settlement difference exceeding the documented adjustment to be refused")
	}
	// Symmetric: an OVER-settlement beyond the adjustment must also be refused.
	if err := CheckSettlementWithinAdjustment(1000, 1015, 10); err == nil {
		t.Fatal("expected an over-settlement beyond the documented adjustment to be refused")
	}
}

func TestCheckRecoveryWithinRecoverable(t *testing.T) {
	if err := CheckRecoveryWithinRecoverable(500, 1000); err != nil {
		t.Fatalf("expected recovery below the recoverable ceiling to pass, got %v", err)
	}
	if err := CheckRecoveryWithinRecoverable(1000, 1000); err != nil {
		t.Fatalf("expected recovery exactly at the ceiling to pass, got %v", err)
	}
	if err := CheckRecoveryWithinRecoverable(1001, 1000); err == nil {
		t.Fatal("expected recovery exceeding the legally/contractually recoverable amount to be refused")
	}
}

func TestAllChecksRefuseNegativeAmounts(t *testing.T) {
	cases := []func() error{
		func() error { return CheckPaymentWithinQuantum(-1, 100) },
		func() error { return CheckReserveMatchesQuantumBasis(-1, 100) },
		func() error { return CheckSettlementWithinAdjustment(-1, 100, 0) },
		func() error { return CheckRecoveryWithinRecoverable(-1, 100) },
	}
	for i, c := range cases {
		if err := c(); err != ErrNegativeAmount {
			t.Errorf("case %d: expected ErrNegativeAmount, got %v", i, err)
		}
	}
}
