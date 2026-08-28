package credential

import (
	"testing"

	"veriqo/pkg/insurance/network"
	"veriqo/pkg/insurance/party"
)

func validCredential() Credential {
	return Credential{
		CredentialID: "CRED-1", PartyID: "PTY-PANDI", Kind: KindLicense,
		IssuingAuthority: "International Group of P&I Clubs", Jurisdiction: "England and Wales",
		IssuedAtTick: 100, EvidenceIDs: []string{"EVID-LICENSE-1"},
	}
}

func TestCredentialValidateRequiresEvidence(t *testing.T) {
	c := validCredential()
	c.EvidenceIDs = nil
	if err := c.Validate(); err != ErrNoEvidence {
		t.Fatalf("expected ErrNoEvidence, got %v", err)
	}
}

func TestCredentialValidateRequiresIssuingAuthority(t *testing.T) {
	c := validCredential()
	c.IssuingAuthority = ""
	if err := c.Validate(); err != ErrEmptyIssuingAuthority {
		t.Fatalf("expected ErrEmptyIssuingAuthority, got %v", err)
	}
}

func TestCredentialValidateRejectsExpiresBeforeIssued(t *testing.T) {
	c := validCredential()
	c.ExpiresAtTick = 50
	if err := c.Validate(); err != ErrExpiresBeforeIssued {
		t.Fatalf("expected ErrExpiresBeforeIssued, got %v", err)
	}
}

func TestCredentialStatusLifecycle(t *testing.T) {
	c := validCredential()
	c.ExpiresAtTick = 200
	if c.StatusAt(150) != StatusActive {
		t.Fatalf("expected ACTIVE at 150, got %s", c.StatusAt(150))
	}
	if c.StatusAt(250) != StatusExpired {
		t.Fatalf("expected EXPIRED at 250, got %s", c.StatusAt(250))
	}
	if c.EffectiveAt(50) {
		t.Fatal("expected credential not yet effective before IssuedAtTick")
	}
	if !c.EffectiveAt(150) {
		t.Fatal("expected credential effective within its window")
	}
}

func TestRegistryRegisterAndRevoke(t *testing.T) {
	r := NewRegistry()
	c := validCredential()
	if err := r.RegisterCredential(c); err != nil {
		t.Fatalf("RegisterCredential: %v", err)
	}
	got, ok := r.Credential("CRED-1")
	if !ok || got.CredentialID != "CRED-1" {
		t.Fatalf("expected to retrieve registered credential, got %v ok=%v", got, ok)
	}
	// Duplicate registration refused.
	if err := r.RegisterCredential(c); err == nil {
		t.Fatal("expected duplicate CredentialID to be refused")
	}
	if err := r.RevokeCredential("CRED-1", 300, "licence lapsed for cause"); err != nil {
		t.Fatalf("RevokeCredential: %v", err)
	}
	if err := r.RevokeCredential("CRED-1", 310, "again"); err != ErrAlreadyRevoked {
		t.Fatalf("expected ErrAlreadyRevoked, got %v", err)
	}
	after, _ := r.Credential("CRED-1")
	if after.StatusAt(350) != StatusRevoked {
		t.Fatalf("expected REVOKED status after revocation, got %s", after.StatusAt(350))
	}
}

func TestEffectiveCredentialFindsActiveKind(t *testing.T) {
	r := NewRegistry()
	c := validCredential()
	if err := r.RegisterCredential(c); err != nil {
		t.Fatalf("RegisterCredential: %v", err)
	}
	got, ok := r.EffectiveCredential("PTY-PANDI", KindLicense, 150)
	if !ok || got.CredentialID != "CRED-1" {
		t.Fatalf("expected to find effective credential, got %v ok=%v", got, ok)
	}
	if _, ok := r.EffectiveCredential("PTY-PANDI", KindAccreditation, 150); ok {
		t.Fatal("expected no ACCREDITATION credential to be found")
	}
}

func TestQualificationRecordValidateRequiresSourceWhenExternallyVerified(t *testing.T) {
	q := QualificationRecord{
		PartyID: "PTY-PANDI", Role: party.RolePAndIClub, State: network.StateExternallyVerified,
		RecordedBy: "PTY-REVIEWER", RecordedAtTick: 100,
	}
	if err := q.Validate(); err != ErrExternallyVerifiedNeedsSource {
		t.Fatalf("expected ErrExternallyVerifiedNeedsSource, got %v", err)
	}
	q.Source = network.RegistrySourcePAndI
	if err := q.Validate(); err != nil {
		t.Fatalf("expected valid qualification to pass, got %v", err)
	}
}

func TestQualificationRecordValidateRefusesMisroutedSource(t *testing.T) {
	q := QualificationRecord{
		PartyID: "PTY-1", Role: party.RoleRegulator, State: network.StateExternallyVerified,
		Source: network.RegistrySourcePAndI, RecordedBy: "PTY-REVIEWER", RecordedAtTick: 100,
	}
	if err := q.Validate(); err == nil {
		t.Fatal("expected a misrouted registry source to be refused")
	}
}

func TestQualificationRecordSelfAttestedNeedsNoSource(t *testing.T) {
	q := QualificationRecord{
		PartyID: "PTY-1", Role: party.RoleBroker, State: network.StateSelfAttested,
		RecordedBy: "PTY-1", RecordedAtTick: 100,
	}
	if err := q.Validate(); err != nil {
		t.Fatalf("expected self-attested qualification with no source to pass, got %v", err)
	}
}

func TestRegistryQualificationHistoryIsAppendOnly(t *testing.T) {
	r := NewRegistry()
	first := QualificationRecord{PartyID: "PTY-1", Role: party.RoleBroker, State: network.StateSelfAttested, RecordedBy: "PTY-1", RecordedAtTick: 100}
	second := QualificationRecord{PartyID: "PTY-1", Role: party.RoleBroker, State: network.StateExternallyVerified, Source: network.RegistrySourceBroker, RecordedBy: "PTY-REVIEWER", RecordedAtTick: 200}
	if err := r.RecordQualification(first); err != nil {
		t.Fatalf("RecordQualification: %v", err)
	}
	if err := r.RecordQualification(second); err != nil {
		t.Fatalf("RecordQualification: %v", err)
	}
	hist := r.QualificationHistory("PTY-1", party.RoleBroker)
	if len(hist) != 2 {
		t.Fatalf("expected 2 history entries, got %d", len(hist))
	}
	cur, ok := r.CurrentQualification("PTY-1", party.RoleBroker)
	if !ok || cur.State != network.StateExternallyVerified {
		t.Fatalf("expected current qualification to be the latest (EXTERNALLY_VERIFIED), got %v", cur)
	}
}

func TestKindAndStatusVocabulariesAreClosed(t *testing.T) {
	for _, k := range []Kind{KindLicense, KindAccreditation, KindCertification} {
		if !IsKnownKind(k) {
			t.Errorf("expected %q to be known", k)
		}
	}
	for _, s := range []Status{StatusActive, StatusExpired, StatusRevoked} {
		if !IsKnownStatus(s) {
			t.Errorf("expected %q to be known", s)
		}
	}
}
