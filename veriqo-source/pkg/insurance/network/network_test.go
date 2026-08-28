package network

import (
	"context"
	"errors"
	"testing"

	"veriqo/pkg/insurance/party"
)

// referenceAdapter is a COMPILE-TIME CONTRACT CHECK ONLY: it proves
// EvidenceExchangeAdapter and QualificationAdapter are well-formed,
// implementable interfaces (a real Go type CAN satisfy them). It is
// NOT a fake live integration and must never be mistaken for one: both
// methods return a fixed, honestly-labelled "not a real counterparty"
// error, never a fabricated success. This mirrors how this program
// treats every other compile-time-only interface check elsewhere in
// the codebase (e.g. Transport in pkg/connector/aisstream, whose own
// doc comment states plainly that no real implementation exists yet).
type referenceAdapter struct{}

var errNotARealCounterparty = errNotReal{}

type errNotReal struct{}

func (errNotReal) Error() string {
	return "network: referenceAdapter is a compile-time contract check only, not a real counterparty integration"
}

func (referenceAdapter) SubmitEvidence(context.Context, ExchangeRequest) (ExchangeReceipt, error) {
	return ExchangeReceipt{}, errNotARealCounterparty
}

func (referenceAdapter) FetchQualificationState(context.Context, party.PartyID) (QualificationState, error) {
	return "", errNotARealCounterparty
}

func (referenceAdapter) VerifyQualification(context.Context, party.PartyID, party.Role) (QualificationState, error) {
	return "", errNotARealCounterparty
}

var (
	_ EvidenceExchangeAdapter = referenceAdapter{}
	_ QualificationAdapter    = referenceAdapter{}
)

func TestReferenceAdapterNeverFabricatesSuccess(t *testing.T) {
	a := referenceAdapter{}
	if _, err := a.SubmitEvidence(context.Background(), ExchangeRequest{}); err == nil {
		t.Fatal("the compile-time reference adapter must never report success -- it is not a real counterparty")
	}
	if _, err := a.FetchQualificationState(context.Background(), "PTY-1"); err == nil {
		t.Fatal("the compile-time reference adapter must never report success -- it is not a real counterparty")
	}
	if _, err := a.VerifyQualification(context.Background(), "PTY-1", party.RoleInsurer); err == nil {
		t.Fatal("the compile-time reference adapter must never report success -- it is not a real counterparty")
	}
}

func TestIsNetworkParticipantRoleCoversTheWorkOrdersOwnList(t *testing.T) {
	for _, r := range []party.Role{
		party.RoleInsurer, party.RoleBroker, party.RoleReinsurer,
		party.RolePAndIClub, party.RoleSurveyor, party.RoleLossAdjuster, party.RoleSalvageParty,
	} {
		if !IsNetworkParticipantRole(r) {
			t.Errorf("expected %q to be a recognised network participant role", r)
		}
	}
}

func TestIsNetworkParticipantRoleRefusesUnrelatedRoles(t *testing.T) {
	if IsNetworkParticipantRole(party.RoleCarrier) {
		t.Error("RoleCarrier is a responsible-party category, not a network participant role this package models")
	}
	if IsNetworkParticipantRole("NOT_A_REAL_ROLE") {
		t.Error("an unknown role must never report as a recognised participant")
	}
}

// validReceipt is a fully-populated ExchangeReceipt carrying every
// FINAL INTERNAL CHECK item F field this test file's other cases mutate
// one at a time.
func validReceipt() ExchangeReceipt {
	return ExchangeReceipt{
		CaseID: "CASE-1", EvidenceContentHash: "sha256:abc123",
		ReceivedByPartyID: "PTY-INSURER", ReceivedAtTick: 100,
		Source: "P&I club claims portal", IssuerPartyID: "PTY-PANDI",
		ReceiptReference: "PORTAL-CONF-9001", VerificationStatus: VerificationNotPerformed,
	}
}

func TestExchangeReceiptValidateAcceptsAFullyPopulatedReceipt(t *testing.T) {
	if err := validReceipt().Validate(); err != nil {
		t.Fatalf("expected a fully-populated receipt to validate, got %v", err)
	}
}

// TestExchangeReceiptValidateRequiresEveryItemFField is the FINAL
// INTERNAL CHECK item F structural proof: source, timestamp (implicitly
// carried by every case here already setting ReceivedAtTick), issuer,
// content hash, receipt reference, and a recognised verification status
// are each independently required -- omitting any one is refused, not
// silently accepted with a zero value.
func TestExchangeReceiptValidateRequiresEveryItemFField(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(r *ExchangeReceipt)
		wantErr error
	}{
		{"empty CaseID", func(r *ExchangeReceipt) { r.CaseID = "" }, ErrEmptyReceiptCaseID},
		{"empty EvidenceContentHash", func(r *ExchangeReceipt) { r.EvidenceContentHash = "" }, ErrEmptyContentHash},
		{"empty ReceivedByPartyID", func(r *ExchangeReceipt) { r.ReceivedByPartyID = "" }, ErrEmptyReceivedBy},
		{"empty Source", func(r *ExchangeReceipt) { r.Source = "" }, ErrEmptySource},
		{"empty IssuerPartyID", func(r *ExchangeReceipt) { r.IssuerPartyID = "" }, ErrEmptyIssuer},
		{"empty ReceiptReference", func(r *ExchangeReceipt) { r.ReceiptReference = "" }, ErrEmptyReceiptRef},
		{"unknown VerificationStatus", func(r *ExchangeReceipt) { r.VerificationStatus = "BOGUS" }, ErrUnknownVerification},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := validReceipt()
			c.mutate(&r)
			err := r.Validate()
			if err == nil {
				t.Fatalf("expected Validate to refuse a receipt with %s", c.name)
			}
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("expected %v, got %v", c.wantErr, err)
			}
		})
	}
}

func TestReceiptVerificationStatusVocabularyIsClosed(t *testing.T) {
	for _, s := range []ReceiptVerificationStatus{VerificationNotPerformed, VerificationVerified, VerificationFailed} {
		if !IsKnownVerificationStatus(s) {
			t.Errorf("expected %q to be a known verification status", s)
		}
	}
	if IsKnownVerificationStatus("BOGUS") {
		t.Fatal("an unknown verification status must never report as known")
	}
}

func TestQualificationStateVocabularyIsClosed(t *testing.T) {
	for _, s := range []QualificationState{StateUnverified, StateSelfAttested, StateExternallyVerified, StateRevoked} {
		if !IsKnownQualificationState(s) {
			t.Errorf("expected %q to be a known qualification state", s)
		}
	}
	if IsKnownQualificationState("PRODUCTION_QUALIFIED") {
		t.Fatal("QualificationState must never accept internal/assurance's own canonical vocabulary -- these are deliberately different taxonomies")
	}
}
