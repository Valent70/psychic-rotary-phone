package payment

import (
	"testing"

	"veriqo/pkg/insurance/party"
	"veriqo/pkg/insurance/quantum"
)

func mustNew(t *testing.T) *PaymentRecord {
	t.Helper()
	p, err := New("PAY-1", "CLM-1", "CASE-1", "PTY-PAYEE", quantum.Amount(10_000), "IDEM-1", "PTY-CREATOR", "initial payment", 100)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestNewValidatesRequiredFields(t *testing.T) {
	_, err := New("", "CLM-1", "CASE-1", "PTY-PAYEE", 100, "IDEM-1", "PTY-1", "reason", 0)
	if err != ErrEmptyPaymentID {
		t.Errorf("expected ErrEmptyPaymentID, got %v", err)
	}
	_, err = New("PAY-1", "CLM-1", "CASE-1", "PTY-PAYEE", 0, "IDEM-1", "PTY-1", "reason", 0)
	if err != ErrNonPositiveAmount {
		t.Errorf("expected ErrNonPositiveAmount, got %v", err)
	}
	_, err = New("PAY-1", "CLM-1", "CASE-1", "PTY-PAYEE", 100, "", "PTY-1", "reason", 0)
	if err != ErrEmptyIdempotencyKey {
		t.Errorf("expected ErrEmptyIdempotencyKey, got %v", err)
	}
}

func TestFullLifecycleHappyPath(t *testing.T) {
	p := mustNew(t)
	if p.Status() != StatusPending {
		t.Fatalf("expected StatusPending, got %s", p.Status())
	}

	auth, err := p.Authorize("PTY-AUTHORIZER", party.RoleInsurer, "reviewed and authorized", 110)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if auth.AuthorizedBy != "PTY-AUTHORIZER" {
		t.Errorf("unexpected AuthorizedBy: %s", auth.AuthorizedBy)
	}
	if p.Status() != StatusAuthorized {
		t.Fatalf("expected StatusAuthorized, got %s", p.Status())
	}

	instr, err := p.Instruct("PTY-BANK", party.RoleBankTradeFinance, "SWIFT MT103", "REF-001", 120)
	if err != nil {
		t.Fatalf("Instruct: %v", err)
	}
	if instr.Amount != quantum.Amount(10_000) {
		t.Errorf("unexpected instructed amount: %s", instr.Amount)
	}
	if p.Status() != StatusInstructed {
		t.Fatalf("expected StatusInstructed, got %s", p.Status())
	}

	if err := p.Settle("PTY-BANK", "confirmed by SWIFT ack", 130); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if p.Status() != StatusPaid {
		t.Fatalf("expected StatusPaid, got %s", p.Status())
	}

	rev, err := p.Reverse("PTY-AUTHORIZER", "duplicate payment detected", "PAY-1-REVERSAL", 140)
	if err != nil {
		t.Fatalf("Reverse: %v", err)
	}
	if rev.ReversalPaymentID != "PAY-1-REVERSAL" {
		t.Errorf("unexpected ReversalPaymentID: %s", rev.ReversalPaymentID)
	}
	if p.Status() != StatusReversed {
		t.Fatalf("expected StatusReversed, got %s", p.Status())
	}

	if len(p.History()) != 5 {
		t.Fatalf("expected 5 history events (create/authorize/instruct/settle/reverse), got %d", len(p.History()))
	}
}

func TestSegregationOfDutiesRefusesSelfAuthorization(t *testing.T) {
	p := mustNew(t)
	_, err := p.Authorize("PTY-CREATOR", party.RoleInsurer, "self authorize", 110)
	if err != ErrSelfAuthorization {
		t.Fatalf("expected ErrSelfAuthorization, got %v", err)
	}
}

func TestAuthorizeRefusesWrongRole(t *testing.T) {
	p := mustNew(t)
	_, err := p.Authorize("PTY-AUTHORIZER", party.RoleBankTradeFinance, "wrong role", 110)
	if err == nil {
		t.Fatal("expected a bank/trade-finance role to lack payment authority")
	}
}

func TestInstructRefusesWrongRole(t *testing.T) {
	p := mustNew(t)
	if _, err := p.Authorize("PTY-AUTHORIZER", party.RoleInsurer, "authorized", 110); err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	_, err := p.Instruct("PTY-AUTHORIZER", party.RoleInsurer, "wire", "REF", 120)
	if err == nil {
		t.Fatal("expected an insurer role to lack execution authority — that is the second segregation of duties")
	}
}

func TestInstructRefusesBeforeAuthorization(t *testing.T) {
	p := mustNew(t)
	_, err := p.Instruct("PTY-BANK", party.RoleBankTradeFinance, "wire", "REF", 120)
	if err != ErrNotAuthorized {
		t.Fatalf("expected ErrNotAuthorized, got %v", err)
	}
}

func TestSettleRefusesBeforeInstruction(t *testing.T) {
	p := mustNew(t)
	if _, err := p.Authorize("PTY-AUTHORIZER", party.RoleInsurer, "authorized", 110); err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if err := p.Settle("PTY-BANK", "premature", 120); err != ErrNotInstructed {
		t.Fatalf("expected ErrNotInstructed, got %v", err)
	}
}

func TestReverseRefusesUnpaid(t *testing.T) {
	p := mustNew(t)
	_, err := p.Reverse("PTY-1", "reason", "", 100)
	if err != ErrNotPaid {
		t.Fatalf("expected ErrNotPaid, got %v", err)
	}
}

func TestDisputeLifecycle(t *testing.T) {
	p := mustNew(t)
	if _, err := p.Authorize("PTY-AUTHORIZER", party.RoleInsurer, "authorized", 110); err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	d, err := p.RaiseDispute("PTY-PAYEE", "amount disputed", "DIS-1", 115)
	if err != nil {
		t.Fatalf("RaiseDispute: %v", err)
	}
	if !d.Open {
		t.Fatal("expected dispute to be open")
	}
	if p.Status() != StatusDisputed {
		t.Fatalf("expected StatusDisputed, got %s", p.Status())
	}
	// Cannot raise a second dispute while one is open.
	if _, err := p.RaiseDispute("PTY-PAYEE", "second dispute", "", 116); err != ErrAlreadyDisputed {
		t.Fatalf("expected ErrAlreadyDisputed, got %v", err)
	}
	if err := p.ResolveDispute("PTY-AUTHORIZER", "resolved: amount confirmed correct", StatusAuthorized, 120); err != nil {
		t.Fatalf("ResolveDispute: %v", err)
	}
	if p.Status() != StatusAuthorized {
		t.Fatalf("expected status restored to StatusAuthorized, got %s", p.Status())
	}
	gotDispute, ok := p.Dispute()
	if !ok || gotDispute.Open {
		t.Fatal("expected dispute to be closed")
	}
}

func TestReconcileReportsAdequacy(t *testing.T) {
	p := mustNew(t)
	exact := p.Reconcile(quantum.Amount(10_000))
	if exact.Adequacy != AdequacyExact {
		t.Errorf("expected AdequacyExact, got %s", exact.Adequacy)
	}
	under := p.Reconcile(quantum.Amount(12_000))
	if under.Adequacy != AdequacyUnderPaid {
		t.Errorf("expected AdequacyUnderPaid, got %s", under.Adequacy)
	}
	over := p.Reconcile(quantum.Amount(8_000))
	if over.Adequacy != AdequacyOverPaid {
		t.Errorf("expected AdequacyOverPaid, got %s", over.Adequacy)
	}
}

func TestLinkAllocationAndQuantum(t *testing.T) {
	p := mustNew(t)
	p.LinkAllocation("INSURER_PRIMARY", "INSURER:PTY-002-INSURER")
	p.LinkQuantum("QC-GOLDEN-WITH-SALVAGE")
	if p.AllocationPartyID != "INSURER:PTY-002-INSURER" {
		t.Errorf("unexpected AllocationPartyID: %s", p.AllocationPartyID)
	}
	if p.QuantumCalculationID != "QC-GOLDEN-WITH-SALVAGE" {
		t.Errorf("unexpected QuantumCalculationID: %s", p.QuantumCalculationID)
	}
}

func TestRegistryIdempotentCreate(t *testing.T) {
	reg, err := NewPaymentRegistry("CASE-1")
	if err != nil {
		t.Fatalf("NewPaymentRegistry: %v", err)
	}
	p1, err := reg.Create("PAY-1", "CLM-1", "PTY-PAYEE", quantum.Amount(5_000), "IDEM-A", "PTY-CREATOR", "first attempt", 100)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// A retry with the SAME idempotency key, different PaymentID and
	// amount, must return the ORIGINAL record unchanged.
	p2, err := reg.Create("PAY-2-SHOULD-BE-IGNORED", "CLM-1", "PTY-PAYEE", quantum.Amount(9_999), "IDEM-A", "PTY-CREATOR", "retry", 101)
	if err != nil {
		t.Fatalf("Create (retry): %v", err)
	}
	if p1 != p2 {
		t.Fatal("expected the retried Create to return the SAME PaymentRecord pointer")
	}
	if reg.Count() != 1 {
		t.Fatalf("expected exactly 1 payment after an idempotent retry, got %d", reg.Count())
	}
	if p2.CurrentAmount() != quantum.Amount(5_000) {
		t.Fatalf("expected the ORIGINAL amount to survive, got %s", p2.CurrentAmount())
	}
}

func TestRegistryRefusesDuplicatePaymentIDWithDifferentIdempotencyKey(t *testing.T) {
	reg, err := NewPaymentRegistry("CASE-1")
	if err != nil {
		t.Fatalf("NewPaymentRegistry: %v", err)
	}
	if _, err := reg.Create("PAY-1", "CLM-1", "PTY-PAYEE", 1000, "IDEM-A", "PTY-1", "r", 1); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := reg.Create("PAY-1", "CLM-1", "PTY-PAYEE", 1000, "IDEM-B", "PTY-1", "r", 2); err == nil {
		t.Fatal("expected a duplicate PaymentID under a different idempotency key to be refused")
	}
}

func TestPaymentAuthorityAndExecutionAuthorityAreDisjoint(t *testing.T) {
	for r := range paymentAuthority {
		if HasExecutionAuthority(r) {
			t.Errorf("role %q has both payment authority and execution authority — segregation of duties is broken", r)
		}
	}
}

func TestStatusVocabularyIsClosed(t *testing.T) {
	for _, s := range []Status{StatusPending, StatusAuthorized, StatusInstructed, StatusPaid, StatusReversed, StatusDisputed} {
		if !IsKnownStatus(s) {
			t.Errorf("expected %q to be known", s)
		}
	}
	if IsKnownStatus("NOT_A_STATUS") {
		t.Fatal("an unknown status must never report as known")
	}
}
