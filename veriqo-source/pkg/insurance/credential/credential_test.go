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

// ---- Round 10: enriched QualificationRecord fields ----------------------

func fullQualification() QualificationRecord {
	return QualificationRecord{
		PartyID: "PTY-SURVEYOR", Role: party.RoleSurveyor, State: network.StateExternallyVerified,
		Source: network.RegistrySourceSurveyor, CredentialID: "CRED-1", EvidenceIDs: []string{"EVID-1"},
		Jurisdiction: "England and Wales", Issuer: "Institute of Marine Engineering, Science and Technology",
		EffectiveAtTick: 100, ExpiresAtTick: 500, Scope: "cargo damage surveys only",
		DelegatedAuthorityRelationshipID: "REL-1", RecordedBy: "PTY-REVIEWER", RecordedAtTick: 100,
	}
}

func TestQualificationRecordValidateRequiresRevocationReasonWhenRevoked(t *testing.T) {
	q := fullQualification()
	q.State = network.StateRevoked
	if err := q.Validate(); err != ErrRevokedNeedsReason {
		t.Fatalf("expected ErrRevokedNeedsReason, got %v", err)
	}
	q.RevocationReason = "licence lapsed"
	if err := q.Validate(); err != nil {
		t.Fatalf("expected revoked-with-reason to pass, got %v", err)
	}
}

func TestQualificationRecordValidateRejectsExpiresBeforeEffective(t *testing.T) {
	q := fullQualification()
	q.ExpiresAtTick = 50 // before EffectiveAtTick of 100
	if err := q.Validate(); err != ErrQualificationExpiresBeforeEffective {
		t.Fatalf("expected ErrQualificationExpiresBeforeEffective, got %v", err)
	}
}

func TestQualificationRecordEffectiveAtWindow(t *testing.T) {
	q := fullQualification()
	if q.EffectiveAt(50) {
		t.Fatal("expected not yet effective before EffectiveAtTick")
	}
	if !q.EffectiveAt(200) {
		t.Fatal("expected effective within window")
	}
	if q.EffectiveAt(600) {
		t.Fatal("expected not effective after ExpiresAtTick")
	}
}

func TestQualificationRecordRevokedIsNeverEffective(t *testing.T) {
	q := fullQualification()
	q.State = network.StateRevoked
	q.RevocationReason = "withdrawn"
	if q.EffectiveAt(200) {
		t.Fatal("expected a revoked qualification to never report as effective, even within its own window")
	}
}

func TestEffectiveQualificationAtIsRequalificationAware(t *testing.T) {
	r := NewRegistry()
	first := fullQualification()
	first.State = network.StateSelfAttested
	first.Source = ""
	first.EffectiveAtTick, first.ExpiresAtTick, first.RecordedAtTick = 100, 300, 100
	if err := r.RecordQualification(first); err != nil {
		t.Fatalf("RecordQualification(first): %v", err)
	}
	second := fullQualification() // externally verified, requalifies the party
	second.EffectiveAtTick, second.ExpiresAtTick, second.RecordedAtTick = 300, 0, 300
	if err := r.RecordQualification(second); err != nil {
		t.Fatalf("RecordQualification(second): %v", err)
	}

	atFirst, ok := r.EffectiveQualificationAt("PTY-SURVEYOR", party.RoleSurveyor, 150)
	if !ok || atFirst.State != network.StateSelfAttested {
		t.Fatalf("expected the FIRST record to be effective at tick 150, got %+v ok=%v", atFirst, ok)
	}
	atSecond, ok := r.EffectiveQualificationAt("PTY-SURVEYOR", party.RoleSurveyor, 350)
	if !ok || atSecond.State != network.StateExternallyVerified {
		t.Fatalf("expected the SECOND (requalified) record to be effective at tick 350, got %+v ok=%v", atSecond, ok)
	}
}

func TestEffectiveQualificationAtHonorsRevocation(t *testing.T) {
	r := NewRegistry()
	q := fullQualification()
	if err := r.RecordQualification(q); err != nil {
		t.Fatalf("RecordQualification: %v", err)
	}
	revoked := q
	revoked.State = network.StateRevoked
	revoked.RevocationReason = "credential suspended"
	revoked.RecordedAtTick = 250
	if err := r.RecordQualification(revoked); err != nil {
		t.Fatalf("RecordQualification(revoked): %v", err)
	}
	if _, ok := r.EffectiveQualificationAt("PTY-SURVEYOR", party.RoleSurveyor, 400); ok {
		t.Fatal("expected no effective qualification after revocation")
	}
}
