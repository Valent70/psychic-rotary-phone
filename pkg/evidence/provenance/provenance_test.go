package provenance

import (
	"errors"
	"testing"
)

func validExternalEvidence() ExternalEvidence {
	return ExternalEvidence{
		SourceID: "aisstream-ws", ProviderID: "aisstream.io", DatasetID: "ais-positions",
		DeliveryID: "delivery-1", OriginClass: OriginRealExternalUnverified,
		RightsState: RightsUnknownPendingContract, ReceivedAt: 10, ObservedAt: 9,
		PayloadHash: "deadbeef", CanonicalHash: "cafebabe", SchemaVersion: SchemaVersionV1,
		CorrectionState: CorrectionNone, AttestationState: AttestationUnattested,
	}
}

func TestValidateAcceptsAFullyPopulatedRecord(t *testing.T) {
	if err := validExternalEvidence().Validate(); err != nil {
		t.Fatalf("expected a fully populated record to validate, got %v", err)
	}
}

func TestValidateFailsClosedOnEachMissingRequiredField(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(e ExternalEvidence) ExternalEvidence
	}{
		{"source_id", func(e ExternalEvidence) ExternalEvidence { e.SourceID = ""; return e }},
		{"provider_id", func(e ExternalEvidence) ExternalEvidence { e.ProviderID = ""; return e }},
		{"dataset_id", func(e ExternalEvidence) ExternalEvidence { e.DatasetID = ""; return e }},
		{"delivery_id", func(e ExternalEvidence) ExternalEvidence { e.DeliveryID = ""; return e }},
		{"payload_hash", func(e ExternalEvidence) ExternalEvidence { e.PayloadHash = ""; return e }},
		{"canonical_hash", func(e ExternalEvidence) ExternalEvidence { e.CanonicalHash = ""; return e }},
		{"schema_version", func(e ExternalEvidence) ExternalEvidence { e.SchemaVersion = ""; return e }},
		{"origin_class", func(e ExternalEvidence) ExternalEvidence { e.OriginClass = ""; return e }},
		{"rights_state", func(e ExternalEvidence) ExternalEvidence { e.RightsState = ""; return e }},
		{"correction_state", func(e ExternalEvidence) ExternalEvidence { e.CorrectionState = ""; return e }},
		{"attestation_state", func(e ExternalEvidence) ExternalEvidence { e.AttestationState = ""; return e }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := c.mutate(validExternalEvidence())
			if err := e.Validate(); err == nil {
				t.Fatalf("expected Validate to fail closed with %s missing, got nil error", c.name)
			}
		})
	}
}

func TestValidateRejectsUnknownEnumValues(t *testing.T) {
	e := validExternalEvidence()
	e.OriginClass = OriginClass("MADE_UP")
	if err := e.Validate(); !errors.Is(err, ErrUnknownOriginClass) {
		t.Fatalf("expected ErrUnknownOriginClass, got %v", err)
	}
	e = validExternalEvidence()
	e.RightsState = RightsState("MADE_UP")
	if err := e.Validate(); !errors.Is(err, ErrUnknownRightsState) {
		t.Fatalf("expected ErrUnknownRightsState, got %v", err)
	}
}

// --- PART 15-style anti-false-green tests ----------------------------

func TestUnknownPendingContractPermitsNothingButInternalUse(t *testing.T) {
	e := validExternalEvidence() // RightsUnknownPendingContract
	if e.Permits(UseCustomerFacing) {
		t.Fatal("real but unauthorized data must not become customer-facing evidence")
	}
	if e.Permits(UseCalibration) {
		t.Fatal("real but unauthorized data must not become calibration evidence")
	}
	if e.Permits(UseDispute) {
		t.Fatal("real but unauthorized data must not become dispute evidence")
	}
	if e.Permits(UseTraining) {
		t.Fatal("real but unauthorized data must not become training evidence")
	}
	if !e.Permits(UseInternalOnly) {
		t.Fatal("internal-only use should still be permitted for triage/debugging")
	}
}

func TestRevokedAndExpiredPermitNothingAtAll(t *testing.T) {
	for _, rs := range []RightsState{RightsRevoked, RightsExpired} {
		e := validExternalEvidence()
		e.RightsState = rs
		for _, use := range []Use{UseInternalOnly, UseCustomerFacing, UseDispute, UseCalibration, UseTraining} {
			if e.Permits(use) {
				t.Fatalf("RightsState=%s must permit nothing, but Permits(%s)=true", rs, use)
			}
		}
	}
}

func TestSupersededOrRetractedDenyEveryUseEvenWithGenerousRights(t *testing.T) {
	e := validExternalEvidence()
	e.RightsState = RightsCustomerFacingAllowed // otherwise generous
	e.CorrectionState = CorrectionSuperseded
	if e.Permits(UseCustomerFacing) {
		t.Fatal("a superseded record must never be usable, regardless of RightsState")
	}
	e.CorrectionState = CorrectionRetracted
	if e.Permits(UseInternalOnly) {
		t.Fatal("a retracted record must never be usable, not even internally")
	}
}

func TestIDIsDeterministicAndChangesWithAnySemanticField(t *testing.T) {
	a := validExternalEvidence()
	b := validExternalEvidence()
	if a.ID() != b.ID() {
		t.Fatal("two byte-identical records must have the same ID")
	}
	b.DeliveryID = "delivery-2"
	if a.ID() == b.ID() {
		t.Fatal("changing delivery_id must change the ID")
	}
}

func TestOntologyProvenanceProjectsIdentityFieldsOnly(t *testing.T) {
	e := validExternalEvidence()
	e.TransformationChain = []string{"raw-decode", "canonicalize"}
	producerID, upstreamID, transformationID := e.OntologyProvenance()
	if producerID != e.ProviderID || upstreamID != e.SourceID || transformationID != "canonicalize" {
		t.Fatalf("unexpected projection: producer=%s upstream=%s transform=%s", producerID, upstreamID, transformationID)
	}
}

// --- PART 2 registry ---------------------------------------------------

func TestRegisterNeverAutoGrantsTrustRegardlessOfCallerInput(t *testing.T) {
	r := NewRegistry()
	// A caller (or a compromised upstream config) tries to sneak
	// TrustGranted=true straight through Register -- must be ignored.
	if err := r.Register(Entry{ID: "aisstream", Kind: KindEvidenceProvider, Name: "AISstream.io", TrustGranted: true}); err != nil {
		t.Fatal(err)
	}
	if r.IsTrustedEvidenceProvider("aisstream") {
		t.Fatal("Register must never auto-grant trust, even if the caller set TrustGranted=true")
	}
}

func TestNamedNeverAutoTrustSourcesStayUntrustedUntilExplicitGrant(t *testing.T) {
	r := NewRegistry()
	neverAutoTrust := []string{"aisstream", "world_monitor", "gdelt", "public_dashboard", "oss_project", "gov_website"}
	for _, id := range neverAutoTrust {
		if err := r.Register(Entry{ID: id, Kind: KindEvidenceProvider, Name: id}); err != nil {
			t.Fatal(err)
		}
		if r.IsTrustedEvidenceProvider(id) {
			t.Fatalf("%s must not be trusted immediately after registration", id)
		}
	}
}

func TestGrantTrustToEvidenceProviderRequiresBothPolicyAndAttestation(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Entry{ID: "aisstream", Kind: KindEvidenceProvider, Name: "AISstream.io"}); err != nil {
		t.Fatal(err)
	}
	if err := r.GrantTrust("aisstream", "policy-2026-08", "", "ops-lead", 5); !errors.Is(err, ErrTrustGrantRequiresPolicyAndAttestation) {
		t.Fatalf("expected a policy-only grant to an EVIDENCE_PROVIDER to be refused, got %v", err)
	}
	if r.IsTrustedEvidenceProvider("aisstream") {
		t.Fatal("a refused grant must not have taken effect")
	}
	if err := r.GrantTrust("aisstream", "policy-2026-08", "attestation-xyz", "ops-lead", 5); err != nil {
		t.Fatalf("expected a policy+attestation grant to succeed: %v", err)
	}
	if !r.IsTrustedEvidenceProvider("aisstream") {
		t.Fatal("expected trust to be granted once both policy and attestation refs are present")
	}
}

func TestGrantTrustToNonEvidenceProviderDoesNotRequireAttestation(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Entry{ID: "gdelt-source", Kind: KindSource, Name: "GDELT raw feed"}); err != nil {
		t.Fatal(err)
	}
	if err := r.GrantTrust("gdelt-source", "policy-2026-08", "", "ops-lead", 5); err != nil {
		t.Fatalf("a SOURCE-kind grant should only require a policy ref: %v", err)
	}
}

func TestRevokeTrustReturnsProviderToUntrusted(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Entry{ID: "aisstream", Kind: KindEvidenceProvider, Name: "AISstream.io"}); err != nil {
		t.Fatal(err)
	}
	if err := r.GrantTrust("aisstream", "policy-1", "attestation-1", "ops-lead", 1); err != nil {
		t.Fatal(err)
	}
	if !r.IsTrustedEvidenceProvider("aisstream") {
		t.Fatal("expected trust to be granted")
	}
	if err := r.RevokeTrust("aisstream"); err != nil {
		t.Fatal(err)
	}
	if r.IsTrustedEvidenceProvider("aisstream") {
		t.Fatal("expected trust to be revoked")
	}
}

func TestDuplicateRegistrationRejected(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Entry{ID: "aisstream", Kind: KindEvidenceProvider, Name: "AISstream.io"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(Entry{ID: "aisstream", Kind: KindEvidenceProvider, Name: "AISstream.io"}); !errors.Is(err, ErrEntityExists) {
		t.Fatalf("expected ErrEntityExists, got %v", err)
	}
}

func TestGrantTrustToUnregisteredEntityFails(t *testing.T) {
	r := NewRegistry()
	if err := r.GrantTrust("ghost", "policy-1", "attestation-1", "ops-lead", 1); !errors.Is(err, ErrEntityNotFound) {
		t.Fatalf("expected ErrEntityNotFound, got %v", err)
	}
}

func TestUnknownEntityKindRejected(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Entry{ID: "x", Kind: EntityKind("BOGUS"), Name: "x"}); !errors.Is(err, ErrUnknownEntityKind) {
		t.Fatalf("expected ErrUnknownEntityKind, got %v", err)
	}
}
