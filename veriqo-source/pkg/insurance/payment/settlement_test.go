package payment

import (
	"context"
	"errors"
	"testing"

	"veriqo/pkg/insurance/party"
	"veriqo/pkg/insurance/quantum"
)

func fullyPaidPayment(t *testing.T) *PaymentRecord {
	t.Helper()
	p := mustNew(t)
	if _, err := p.Authorize("PTY-AUTHORIZER", party.RoleInsurer, "authorized", 110); err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if _, err := p.Instruct("PTY-BANK", party.RoleBankTradeFinance, "SWIFT MT103", "REF-001", 120); err != nil {
		t.Fatalf("Instruct: %v", err)
	}
	if err := p.Settle("PTY-BANK", "confirmed by SWIFT ack", 130); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	return p
}

func TestRecordSettlementEvidenceRequiresPaidStatus(t *testing.T) {
	p := mustNew(t)
	ev := SettlementEvidence{
		PaymentID: "PAY-1", Reference: "SWIFT-REF-1", SourceDescription: "SWIFT MT910 confirmation",
		SettledAmount: quantum.Amount(10_000), ConfirmedAtTick: 140,
	}
	if err := p.RecordSettlementEvidence(ev); !errors.Is(err, ErrNotYetPaid) {
		t.Fatalf("expected ErrNotYetPaid for a PENDING payment, got %v", err)
	}
}

func TestRecordSettlementEvidenceHappyPath(t *testing.T) {
	p := fullyPaidPayment(t)
	ev := SettlementEvidence{
		PaymentID: p.PaymentID, Reference: "SWIFT-REF-1", SourceDescription: "SWIFT MT910 confirmation",
		SettledAmount: quantum.Amount(10_000), ConfirmedAtTick: 140,
	}
	if err := p.RecordSettlementEvidence(ev); err != nil {
		t.Fatalf("RecordSettlementEvidence: %v", err)
	}
	got, ok := p.SettlementEvidenceRecorded()
	if !ok {
		t.Fatal("expected settlement evidence to be recorded")
	}
	if got.Reference != "SWIFT-REF-1" {
		t.Errorf("unexpected reference: %s", got.Reference)
	}
	if len(p.History()) != 5 {
		t.Fatalf("expected 5 history events (create/authorize/instruct/settle/record_settlement), got %d", len(p.History()))
	}
}

func TestRecordSettlementEvidenceRefusesDoubleRecording(t *testing.T) {
	p := fullyPaidPayment(t)
	ev := SettlementEvidence{
		PaymentID: p.PaymentID, Reference: "SWIFT-REF-1", SourceDescription: "SWIFT MT910 confirmation",
		SettledAmount: quantum.Amount(10_000), ConfirmedAtTick: 140,
	}
	if err := p.RecordSettlementEvidence(ev); err != nil {
		t.Fatalf("RecordSettlementEvidence: %v", err)
	}
	if err := p.RecordSettlementEvidence(ev); err != ErrAlreadySettled {
		t.Fatalf("expected ErrAlreadySettled, got %v", err)
	}
}

func TestRecordSettlementEvidenceRefusesMismatchedPaymentID(t *testing.T) {
	p := fullyPaidPayment(t)
	ev := SettlementEvidence{
		PaymentID: "PAY-DIFFERENT", Reference: "SWIFT-REF-1", SourceDescription: "SWIFT MT910 confirmation",
		SettledAmount: quantum.Amount(10_000), ConfirmedAtTick: 140,
	}
	if err := p.RecordSettlementEvidence(ev); !errors.Is(err, ErrSettlementPaymentIDMismatch) {
		t.Fatalf("expected ErrSettlementPaymentIDMismatch, got %v", err)
	}
}

func TestSettlementEvidenceValidateRequiresFields(t *testing.T) {
	base := SettlementEvidence{
		PaymentID: "PAY-1", Reference: "REF", SourceDescription: "desc",
		SettledAmount: quantum.Amount(100), ConfirmedAtTick: 1,
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("expected valid evidence to pass, got %v", err)
	}
	noRef := base
	noRef.Reference = ""
	if err := noRef.Validate(); err != ErrEmptySettlementReference {
		t.Fatalf("expected ErrEmptySettlementReference, got %v", err)
	}
	noSource := base
	noSource.SourceDescription = ""
	if err := noSource.Validate(); err != ErrEmptySettlementSource {
		t.Fatalf("expected ErrEmptySettlementSource, got %v", err)
	}
	zeroAmount := base
	zeroAmount.SettledAmount = 0
	if err := zeroAmount.Validate(); err != ErrNonPositiveSettledAmount {
		t.Fatalf("expected ErrNonPositiveSettledAmount, got %v", err)
	}
}

func TestReconcileSettlementRequiresEvidence(t *testing.T) {
	p := fullyPaidPayment(t)
	if _, err := p.ReconcileSettlement(); err != ErrNoSettlementEvidence {
		t.Fatalf("expected ErrNoSettlementEvidence before any evidence is recorded, got %v", err)
	}
}

func TestReconcileSettlementExactMatch(t *testing.T) {
	p := fullyPaidPayment(t)
	ev := SettlementEvidence{
		PaymentID: p.PaymentID, Reference: "SWIFT-REF-1", SourceDescription: "SWIFT MT910 confirmation",
		SettledAmount: quantum.Amount(10_000), ConfirmedAtTick: 140,
	}
	if err := p.RecordSettlementEvidence(ev); err != nil {
		t.Fatalf("RecordSettlementEvidence: %v", err)
	}
	rec, err := p.ReconcileSettlement()
	if err != nil {
		t.Fatalf("ReconcileSettlement: %v", err)
	}
	if rec.Adequacy != AdequacyExact {
		t.Errorf("expected AdequacyExact, got %s", rec.Adequacy)
	}
	if !rec.ExternallyEvidenced {
		t.Error("expected ExternallyEvidenced true")
	}
	if rec.DeltaMinor != 0 {
		t.Errorf("expected zero delta, got %s", rec.DeltaMinor)
	}
}

func TestReconcileSettlementDetectsUnderAndOverSettlement(t *testing.T) {
	under := fullyPaidPayment(t)
	if err := under.RecordSettlementEvidence(SettlementEvidence{
		PaymentID: under.PaymentID, Reference: "REF-UNDER", SourceDescription: "bank confirmation",
		SettledAmount: quantum.Amount(9_000), ConfirmedAtTick: 140, // less than the instructed 10,000
	}); err != nil {
		t.Fatalf("RecordSettlementEvidence: %v", err)
	}
	rec, err := under.ReconcileSettlement()
	if err != nil {
		t.Fatalf("ReconcileSettlement: %v", err)
	}
	if rec.Adequacy != AdequacyUnderPaid {
		t.Errorf("expected AdequacyUnderPaid (bank settled less than instructed), got %s", rec.Adequacy)
	}

	over := fullyPaidPayment(t)
	if err := over.RecordSettlementEvidence(SettlementEvidence{
		PaymentID: over.PaymentID, Reference: "REF-OVER", SourceDescription: "bank confirmation",
		SettledAmount: quantum.Amount(11_000), ConfirmedAtTick: 140, // more than the instructed 10,000
	}); err != nil {
		t.Fatalf("RecordSettlementEvidence: %v", err)
	}
	rec, err = over.ReconcileSettlement()
	if err != nil {
		t.Fatalf("ReconcileSettlement: %v", err)
	}
	if rec.Adequacy != AdequacyOverPaid {
		t.Errorf("expected AdequacyOverPaid (bank settled more than instructed), got %s", rec.Adequacy)
	}
}

// TestPaidNeverImpliesSettled is the structural proof of the
// reviewer's own core distinction: a payment reaching StatusPaid (this
// program's own internal terminal state) carries NO settlement
// evidence by default -- Settled is never silently implied by Paid.
func TestPaidNeverImpliesSettled(t *testing.T) {
	p := fullyPaidPayment(t)
	if p.Status() != StatusPaid {
		t.Fatalf("expected StatusPaid, got %s", p.Status())
	}
	if _, ok := p.SettlementEvidenceRecorded(); ok {
		t.Fatal("expected a freshly PAID payment to carry NO settlement evidence until externally recorded")
	}
}

var _ BankConfirmationAdapter = (*referenceBankAdapter)(nil)

// referenceBankAdapter is a COMPILE-TIME CONTRACT CHECK ONLY, matching
// pkg/insurance/network's own referenceAdapter discipline: it proves
// BankConfirmationAdapter is a well-formed, implementable interface,
// and never fabricates a success.
type referenceBankAdapter struct{}

func (referenceBankAdapter) ConfirmSettlement(_ context.Context, _ string, _ quantum.Amount) (SettlementEvidence, error) {
	return SettlementEvidence{}, errNotARealBankCounterparty
}

var errNotARealBankCounterparty = errNotReal{}

type errNotReal struct{}

func (errNotReal) Error() string {
	return "payment: referenceBankAdapter is a compile-time contract check only, not a real bank/payment-rail integration"
}

func TestReferenceBankAdapterNeverFabricatesSuccess(t *testing.T) {
	a := referenceBankAdapter{}
	if _, err := a.ConfirmSettlement(context.Background(), "PAY-1", quantum.Amount(100)); err == nil {
		t.Fatal("the compile-time reference adapter must never report success -- it is not a real bank counterparty")
	}
}
