package network

import (
	"context"
	"testing"

	"veriqo/pkg/insurance/party"
)

// referenceLifecycleAdapter is a COMPILE-TIME CONTRACT CHECK ONLY,
// exactly matching referenceAdapter's own discipline in network_test.go
// applied to LifecycleAdapter: it proves the full lifecycle contract is
// a well-formed, implementable interface. It is NOT a fake live
// integration; every method returns the same honestly-labelled
// errNotARealCounterparty network_test.go already declares.
type referenceLifecycleAdapter struct{ referenceAdapter }

func (referenceLifecycleAdapter) ResolveIdentity(context.Context, party.PartyID) (Identity, error) {
	return Identity{}, errNotARealCounterparty
}

func (referenceLifecycleAdapter) VerifyAuthority(context.Context, party.PartyID, party.Role) (AuthorityGrant, error) {
	return AuthorityGrant{}, errNotARealCounterparty
}

func (referenceLifecycleAdapter) InviteToCase(context.Context, CaseInvitation) error {
	return errNotARealCounterparty
}

func (referenceLifecycleAdapter) RespondToInvitation(context.Context, CaseAcceptance) (CaseAcceptance, error) {
	return CaseAcceptance{}, errNotARealCounterparty
}

func (referenceLifecycleAdapter) RequestClarification(context.Context, ClarificationRequest) error {
	return errNotARealCounterparty
}

func (referenceLifecycleAdapter) SubmitClarificationResponse(context.Context, ClarificationResponse) error {
	return errNotARealCounterparty
}

func (referenceLifecycleAdapter) SubmitReview(context.Context, CaseReview) error {
	return errNotARealCounterparty
}

func (referenceLifecycleAdapter) RecordOutcome(context.Context, CaseOutcome) error {
	return errNotARealCounterparty
}

func (referenceLifecycleAdapter) Revoke(context.Context, Revocation) error {
	return errNotARealCounterparty
}

var _ LifecycleAdapter = referenceLifecycleAdapter{}

func TestReferenceLifecycleAdapterNeverFabricatesSuccess(t *testing.T) {
	a := referenceLifecycleAdapter{}
	ctx := context.Background()
	if _, err := a.ResolveIdentity(ctx, "PTY-1"); err == nil {
		t.Fatal("must never report success")
	}
	if _, err := a.VerifyAuthority(ctx, "PTY-1", party.RoleInsurer); err == nil {
		t.Fatal("must never report success")
	}
	if err := a.InviteToCase(ctx, CaseInvitation{}); err == nil {
		t.Fatal("must never report success")
	}
	if _, err := a.RespondToInvitation(ctx, CaseAcceptance{}); err == nil {
		t.Fatal("must never report success")
	}
	if err := a.RequestClarification(ctx, ClarificationRequest{}); err == nil {
		t.Fatal("must never report success")
	}
	if err := a.SubmitClarificationResponse(ctx, ClarificationResponse{}); err == nil {
		t.Fatal("must never report success")
	}
	if err := a.SubmitReview(ctx, CaseReview{}); err == nil {
		t.Fatal("must never report success")
	}
	if err := a.RecordOutcome(ctx, CaseOutcome{}); err == nil {
		t.Fatal("must never report success")
	}
	if err := a.Revoke(ctx, Revocation{}); err == nil {
		t.Fatal("must never report success")
	}
}

func TestOutcomeKindVocabularyIsClosed(t *testing.T) {
	for _, k := range []OutcomeKind{OutcomeExchangeConcluded, OutcomeWithdrawn, OutcomeHandedOff} {
		if !IsKnownOutcomeKind(k) {
			t.Errorf("expected %q to be a known outcome kind", k)
		}
	}
	if IsKnownOutcomeKind("CLAIM_APPROVED") {
		t.Fatal("OutcomeKind must never accept a claim-level determination — it describes the exchange only")
	}
}

func TestRevocationValidate(t *testing.T) {
	valid := Revocation{PartyID: "PTY-1", Subject: RevocationSubjectAuthority, Reason: "authority withdrawn", RevokedByPartyID: "PTY-2"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("expected valid revocation to pass, got %v", err)
	}
	noReason := valid
	noReason.Reason = ""
	if err := noReason.Validate(); err == nil {
		t.Fatal("expected a Revocation with no Reason to be refused")
	}
	unknownSubject := valid
	unknownSubject.Subject = "NOT_A_SUBJECT"
	if err := unknownSubject.Validate(); err == nil {
		t.Fatal("expected an unknown Revocation.Subject to be refused")
	}
}

func TestRevocationSubjectVocabularyIsClosed(t *testing.T) {
	for _, s := range []RevocationSubject{RevocationSubjectAuthority, RevocationSubjectQualification, RevocationSubjectInvitation} {
		if !IsKnownRevocationSubject(s) {
			t.Errorf("expected %q to be a known revocation subject", s)
		}
	}
}
