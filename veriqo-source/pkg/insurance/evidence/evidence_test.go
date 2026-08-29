package evidence

import (
	"errors"
	"testing"

	"veriqo/pkg/evidence/ontology"
	"veriqo/pkg/evidence/provenance"
	"veriqo/pkg/insurance/party"
)

func mustEvidence(t *testing.T, subject, source string, observedAt uint64) ontology.Evidence {
	t.Helper()
	e, err := ontology.New(ontology.Evidence{
		Type:       ontology.TypeDocument,
		Subject:    subject,
		Predicate:  "describes",
		Object:     "cargo_condition",
		Source:     source,
		ObservedAt: observedAt,
		Confidence: 0.9,
		Attributes: map[string]string{"document_hash": "deadbeef"},
	})
	if err != nil {
		t.Fatalf("ontology.New: %v", err)
	}
	return e
}

func TestNewRejectsEmptyCaseID(t *testing.T) {
	ev := mustEvidence(t, "S1", "src", 100)
	if _, err := New("", ev, "PTY-001", OriginClaimant); !errors.Is(err, ErrEmptyCaseID) {
		t.Fatalf("expected ErrEmptyCaseID, got %v", err)
	}
}

func TestNewRejectsInvalidUnderlying(t *testing.T) {
	if _, err := New("CASE-1", ontology.Evidence{}, "PTY-001", OriginClaimant); !errors.Is(err, ErrUnderlyingInvalid) {
		t.Fatalf("expected ErrUnderlyingInvalid, got %v", err)
	}
}

func TestNewRejectsEmptySourceParty(t *testing.T) {
	ev := mustEvidence(t, "S1", "src", 100)
	if _, err := New("CASE-1", ev, "", OriginClaimant); !errors.Is(err, ErrEmptySourceParty) {
		t.Fatalf("expected ErrEmptySourceParty, got %v", err)
	}
}

func TestNewRejectsUnknownOrigin(t *testing.T) {
	ev := mustEvidence(t, "S1", "src", 100)
	if _, err := New("CASE-1", ev, "PTY-001", Origin("NOT_AN_ORIGIN")); err == nil {
		t.Fatal("expected error for unknown origin")
	}
}

func TestNewSucceedsAndDefaultsToUnverified(t *testing.T) {
	ev := mustEvidence(t, "S1", "src", 100)
	rec, err := New("CASE-1", ev, "PTY-001", OriginClaimant)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if rec.Status != StatusUnverified {
		t.Fatalf("expected default status UNVERIFIED, got %s", rec.Status)
	}
	if rec.EvidenceID() == "" {
		t.Fatal("expected non-empty EvidenceID")
	}
}

func TestRegistrySubmitAndGet(t *testing.T) {
	reg := NewRegistry()
	ev := mustEvidence(t, "S1", "src", 100)
	rec, _ := New("CASE-1", ev, "PTY-001", OriginClaimant)
	if err := reg.Submit(rec); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	got, ok := reg.Get(rec.EvidenceID())
	if !ok {
		t.Fatal("expected record to be found")
	}
	if got.CaseID != "CASE-1" {
		t.Fatalf("unexpected CaseID %q", got.CaseID)
	}
}

// TestDuplicateEvidenceRejected reproduces blueprint §35 adversarial
// test #2 ("Duplicate evidence"): the literal same evidence content
// must be refused, not silently accepted twice.
func TestDuplicateEvidenceRejected(t *testing.T) {
	reg := NewRegistry()
	ev := mustEvidence(t, "S1", "src", 100)
	rec1, _ := New("CASE-1", ev, "PTY-001", OriginClaimant)
	if err := reg.Submit(rec1); err != nil {
		t.Fatalf("first Submit: %v", err)
	}
	rec2, _ := New("CASE-1", ev, "PTY-002", OriginInsurer) // same content, different submitter
	if err := reg.Submit(rec2); !errors.Is(err, ErrDuplicateEvidence) {
		t.Fatalf("expected ErrDuplicateEvidence, got %v", err)
	}
}

// TestSameEvidenceSubmittedByThreeParties reproduces blueprint §35
// adversarial test #3 directly: three parties each submit the exact
// same underlying content — only the first registers.
func TestSameEvidenceSubmittedByThreeParties(t *testing.T) {
	reg := NewRegistry()
	ev := mustEvidence(t, "S1", "src", 100)
	origins := []Origin{OriginClaimant, OriginInsurer, OriginCarrier}
	accepted := 0
	for i, o := range origins {
		rec, _ := New("CASE-1", ev, party.PartyID("PTY-00X"), o)
		_ = i
		if err := reg.Submit(rec); err == nil {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("expected exactly 1 of 3 identical submissions to be accepted, got %d", accepted)
	}
	if reg.Count() != 1 {
		t.Fatalf("expected registry count 1, got %d", reg.Count())
	}
}

func TestVerifyStatusRejectsUnknownEvidence(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.VerifyStatus("nonexistent"); !errors.Is(err, ErrEvidenceNotFound) {
		t.Fatalf("expected ErrEvidenceNotFound, got %v", err)
	}
}

// TestVerifyStatusRejectsUnassessedRecord responds to the Authority
// Boundary Audit follow-up (Perlu_ditutup_dan_ditingkatkan.docx):
// Registry.SetStatus used to let ANY caller assign ANY Status --
// including StatusCorroborated -- to a record with zero recorded
// Strength behind it. VerifyStatus has nothing to derive a Status from
// until SetStrength has actually been called, and refuses rather than
// guessing.
func TestVerifyStatusRejectsUnassessedRecord(t *testing.T) {
	reg := NewRegistry()
	ev := mustEvidence(t, "S1", "src", 100)
	rec, _ := New("CASE-1", ev, "PTY-001", OriginClaimant)
	reg.Submit(rec)
	if _, err := reg.VerifyStatus(rec.EvidenceID()); !errors.Is(err, ErrStrengthNotAssessed) {
		t.Fatalf("expected ErrStrengthNotAssessed, got %v", err)
	}
	got, _ := reg.Get(rec.EvidenceID())
	if got.Status != StatusUnverified {
		t.Fatalf("expected Status to remain StatusUnverified when VerifyStatus refuses, got %v", got.Status)
	}
}

// TestDeriveStatusRefusesUnassessedStrength mirrors SetStrength's own
// "not yet assessed" gate: the zero-value Strength must never derive to
// any Status, including StatusUnverified -- that would let a caller
// distinguish "genuinely reviewed, nothing conclusive" from "never
// reviewed at all" by accident, exactly the ambiguity Strength.Validate
// already exists to prevent for SetStrength.
func TestDeriveStatusRefusesUnassessedStrength(t *testing.T) {
	if _, err := DeriveStatus(Strength{}); !errors.Is(err, ErrStrengthNotAssessed) {
		t.Fatalf("expected ErrStrengthNotAssessed, got %v", err)
	}
}

// TestDeriveStatusCoversEveryBranch exercises every arm of DeriveStatus's
// priority chain, proving the derivation is total (every reachable
// Strength combination lands on a real Status, never a panic or an
// empty result) and that severity ordering is honored: a Strength that
// would otherwise look "supported" or "corroborated" but ALSO shows
// integrity compromise or net contradiction must still derive the more
// severe Status, never the more favorable one.
func TestDeriveStatusCoversEveryBranch(t *testing.T) {
	cases := []struct {
		name string
		s    Strength
		want Status
	}{
		{
			"integrity compromise wins over everything else",
			Strength{
				Authenticity: AuthenticitySupported, Integrity: IntegrityCompromised,
				Completeness: CompletenessComplete, IndependentCorroboration: CorroborationHigh,
				ContradictionLevel: ContradictionLevelNone,
			},
			StatusAlterationDetected,
		},
		{
			"high contradiction wins over authenticity support",
			Strength{
				Authenticity: AuthenticitySupported, Integrity: IntegrityVerified,
				Completeness: CompletenessComplete, ContradictionLevel: ContradictionLevelHigh,
			},
			StatusContradicted,
		},
		{
			"medium contradiction also contradicts",
			Strength{
				Authenticity: AuthenticitySupported, Integrity: IntegrityVerified,
				Completeness: CompletenessComplete, ContradictionLevel: ContradictionLevelMedium,
			},
			StatusContradicted,
		},
		{
			"disputed authenticity",
			Strength{
				Authenticity: AuthenticityDisputed, Integrity: IntegrityVerified,
				Completeness: CompletenessComplete, ContradictionLevel: ContradictionLevelNone,
			},
			StatusAuthenticityDisputed,
		},
		{
			"disputed temporal consistency, authenticity itself unknown",
			Strength{
				Authenticity: AuthenticityUnknown, Integrity: IntegrityVerified,
				Completeness: CompletenessComplete, TemporalConsistency: TemporalConsistencyDisputed,
				ContradictionLevel: ContradictionLevelNone,
			},
			StatusAuthenticityDisputed,
		},
		{
			"disputed entity consistency, authenticity itself unknown",
			Strength{
				Authenticity: AuthenticityUnknown, Integrity: IntegrityVerified,
				Completeness: CompletenessComplete, EntityConsistency: EntityConsistencyDisputed,
				ContradictionLevel: ContradictionLevelNone,
			},
			StatusAuthenticityDisputed,
		},
		{
			"insufficient completeness, nothing else conclusive",
			Strength{
				Authenticity: AuthenticityUnknown, Integrity: IntegrityVerified,
				Completeness: CompletenessInsufficient, ContradictionLevel: ContradictionLevelNone,
			},
			StatusIncomplete,
		},
		{
			"high corroboration with zero contradiction",
			Strength{
				Authenticity: AuthenticitySupported, Integrity: IntegrityVerified,
				Completeness: CompletenessComplete, IndependentCorroboration: CorroborationHigh,
				ContradictionLevel: ContradictionLevelNone,
			},
			StatusCorroborated,
		},
		{
			"medium corroboration with zero contradiction",
			Strength{
				Authenticity: AuthenticitySupported, Integrity: IntegrityVerified,
				Completeness: CompletenessComplete, IndependentCorroboration: CorroborationMedium,
				ContradictionLevel: ContradictionLevelNone,
			},
			StatusCorroborated,
		},
		{
			"high corroboration but nonzero (low) contradiction does not qualify as CORROBORATED",
			Strength{
				Authenticity: AuthenticitySupported, Integrity: IntegrityVerified,
				Completeness: CompletenessComplete, IndependentCorroboration: CorroborationHigh,
				ContradictionLevel: ContradictionLevelLow,
			},
			StatusAuthenticitySupported,
		},
		{
			"plain authenticity support, no corroboration",
			Strength{
				Authenticity: AuthenticitySupported, Integrity: IntegrityVerified,
				Completeness: CompletenessComplete, IndependentCorroboration: CorroborationNone,
				ContradictionLevel: ContradictionLevelNone,
			},
			StatusAuthenticitySupported,
		},
		{
			"nothing conclusive at all stays UNVERIFIED",
			Strength{
				Authenticity: AuthenticityUnknown, Integrity: IntegrityUnknown,
				Completeness: CompletenessPartial, IndependentCorroboration: CorroborationLow,
				ContradictionLevel: ContradictionLevelNone,
			},
			StatusUnverified,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DeriveStatus(tc.s)
			if err != nil {
				t.Fatalf("DeriveStatus: %v", err)
			}
			if got != tc.want {
				t.Fatalf("DeriveStatus(%+v) = %v, want %v", tc.s, got, tc.want)
			}
		})
	}
}

// TestVerifyStatusOnlyEverProducesTheDerivedStatus is the direct
// adversarial proof for the Authority Boundary Audit finding: under the
// OLD API (Registry.SetStatus), a caller with a compromised record --
// tampering detected, net contradiction -- could still call
// SetStatus(id, StatusCorroborated) and have it recorded verbatim, and
// nothing downstream could tell the difference from a genuinely
// corroborated record. VerifyStatus makes that unrepresentable: no
// matter what a caller wants the Status to say, the ONLY value that can
// ever be stored is whatever the record's own recorded Strength
// legitimately derives to.
func TestVerifyStatusOnlyEverProducesTheDerivedStatus(t *testing.T) {
	reg := NewRegistry()
	ev := mustEvidence(t, "S1", "src", 100)
	rec, _ := New("CASE-1", ev, "PTY-001", OriginClaimant)
	reg.Submit(rec)

	// A record whose OWN evidence says it was tampered with and is net
	// contradicted -- the worst case, exactly what a forger would want
	// to hide behind a favorable Status.
	compromised := Strength{
		Authenticity: AuthenticitySupported, Integrity: IntegrityCompromised,
		Completeness: CompletenessComplete, IndependentCorroboration: CorroborationHigh,
		ContradictionLevel: ContradictionLevelHigh,
	}
	if err := reg.SetStrength(rec.EvidenceID(), compromised); err != nil {
		t.Fatalf("SetStrength: %v", err)
	}
	got, err := reg.VerifyStatus(rec.EvidenceID())
	if err != nil {
		t.Fatalf("VerifyStatus: %v", err)
	}
	// There is no code path -- no parameter, no caller input -- by which
	// this could come back as StatusCorroborated or
	// StatusAuthenticitySupported: VerifyStatus takes no Status
	// argument at all. The severity-ordered priority chain in
	// DeriveStatus is what decides, and integrity compromise outranks
	// everything else.
	if got != StatusAlterationDetected {
		t.Fatalf("expected a compromised, contradicted record to derive StatusAlterationDetected regardless of how favorable its other dimensions look, got %v", got)
	}
	stored, _ := reg.Get(rec.EvidenceID())
	if stored.Status != StatusAlterationDetected {
		t.Fatalf("expected the stored record's own Status to match, got %v", stored.Status)
	}

	// Re-assessing with a genuinely clean Strength must re-derive to a
	// different Status -- VerifyStatus always reflects the CURRENT
	// Strength, never a stale first assessment.
	clean := Strength{
		Authenticity: AuthenticitySupported, Integrity: IntegrityVerified,
		Completeness: CompletenessComplete, IndependentCorroboration: CorroborationNone,
		ContradictionLevel: ContradictionLevelNone,
	}
	if err := reg.SetStrength(rec.EvidenceID(), clean); err != nil {
		t.Fatalf("SetStrength (re-assessment): %v", err)
	}
	got2, err := reg.VerifyStatus(rec.EvidenceID())
	if err != nil {
		t.Fatalf("VerifyStatus (re-assessment): %v", err)
	}
	if got2 != StatusAuthenticitySupported {
		t.Fatalf("expected re-assessment with clean Strength to derive StatusAuthenticitySupported, got %v", got2)
	}
}

func TestSetStrengthRejectsUnassessed(t *testing.T) {
	reg := NewRegistry()
	ev := mustEvidence(t, "S1", "src", 100)
	rec, _ := New("CASE-1", ev, "PTY-001", OriginClaimant)
	reg.Submit(rec)
	if err := reg.SetStrength(rec.EvidenceID(), Strength{}); !errors.Is(err, ErrStrengthNotAssessed) {
		t.Fatalf("expected ErrStrengthNotAssessed, got %v", err)
	}
}

// TestStrengthWorkedExample reproduces the blueprint's §9 worked
// example exactly.
func TestStrengthWorkedExample(t *testing.T) {
	reg := NewRegistry()
	ev := mustEvidence(t, "S1", "src", 100)
	rec, _ := New("CASE-1", ev, "PTY-001", OriginClaimant)
	reg.Submit(rec)

	s := Strength{
		Authenticity:             AuthenticitySupported,
		Integrity:                IntegrityVerified,
		Provenance:               ProvenanceVerified,
		Completeness:             CompletenessPartial,
		TemporalConsistency:      TemporalConsistencySupported,
		IndependentCorroboration: CorroborationHigh,
		ContradictionLevel:       ContradictionLevelLow,
	}
	if err := reg.SetStrength(rec.EvidenceID(), s); err != nil {
		t.Fatalf("SetStrength: %v", err)
	}
	got, _ := reg.Get(rec.EvidenceID())
	if got.Strength.Authenticity != AuthenticitySupported {
		t.Fatalf("unexpected authenticity %v", got.Strength.Authenticity)
	}
	if got.Strength.IndependentCorroboration != CorroborationHigh {
		t.Fatalf("unexpected corroboration %v", got.Strength.IndependentCorroboration)
	}
}

func TestByOrigin(t *testing.T) {
	reg := NewRegistry()
	e1 := mustEvidence(t, "S1", "src1", 100)
	e2 := mustEvidence(t, "S2", "src2", 200)
	e3 := mustEvidence(t, "S3", "src3", 300)
	r1, _ := New("CASE-1", e1, "PTY-001", OriginClaimant)
	r2, _ := New("CASE-1", e2, "PTY-002", OriginInsurer)
	r3, _ := New("CASE-1", e3, "PTY-003", OriginClaimant)
	reg.Submit(r1)
	reg.Submit(r2)
	reg.Submit(r3)

	claimantEv := reg.ByOrigin(OriginClaimant)
	if len(claimantEv) != 2 {
		t.Fatalf("expected 2 claimant records, got %d", len(claimantEv))
	}
}

func TestKnownOriginsCoversTheBlueprintList(t *testing.T) {
	// blueprint §7 enumerates exactly 8 origin classes.
	if got := len(KnownOrigins()); got != 8 {
		t.Fatalf("expected 8 known origins per VICE blueprint §7, got %d", got)
	}
}

func TestKnownStatusesCoversTheBlueprintList(t *testing.T) {
	// blueprint §8 enumerates exactly 7 statuses.
	if len(knownStatuses) != 7 {
		t.Fatalf("expected 7 known statuses per VICE blueprint §8, got %d", len(knownStatuses))
	}
}

// ---- Final Authority Hardening Round: Submit's own authority-bypass ----
//
// This is a NEW finding surfaced by this round's own audit, not one
// named in any prior review: Submit previously stored whatever Record
// value a caller handed it verbatim, with zero reset of any
// authority-bearing field. Since Record has no unexported fields and
// no accessor-only sealing (unlike cre.AuthorizedFinding), any caller
// -- or a deserializer reconstructing a Record from JSON, which is
// exactly the class of attack Final_Hardening_Round.docx's item 15
// warned about -- could hand-build Record{Status: StatusCorroborated,
// Rights: provenance.RightsCustomerFacingAllowed} directly and Submit
// it, bypassing New()'s honest defaults AND every downstream authority
// gate (VerifyStatus's derivation, SetRights's authority check)
// entirely. TestSubmitResetsAuthorityBearingFields is the direct
// adversarial proof this is now closed.

func TestSubmitResetsAuthorityBearingFields(t *testing.T) {
	reg := NewRegistry()
	forged := Record{
		CaseID: "CASE-1", Underlying: mustEvidence(t, "S1", "src", 100),
		SourcePartyID: "PTY-1", Origin: OriginClaimant,
		// Every one of these is a forged authority claim: a caller
		// constructing a Record by hand (or via json.Unmarshal, which
		// exercises exactly the same code path since Go's decoder sets
		// exported fields directly) never went through New(), VerifyStatus,
		// or a genuinely authorized SetRights call for any of them.
		Status:               StatusCorroborated,
		Strength:             Strength{Authenticity: AuthenticitySupported, Integrity: IntegrityVerified, Completeness: CompletenessComplete, ContradictionLevel: ContradictionLevelNone},
		Rights:               "CUSTOMER_FACING_ALLOWED",
		CorrectionSuperseded: true,
		SupersededBy:         "EV-SOMETHING-ELSE",
	}
	if err := reg.Submit(forged); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	got, ok := reg.Get(forged.EvidenceID())
	if !ok {
		t.Fatal("expected the record to be present after Submit")
	}
	if got.Status != StatusUnverified {
		t.Fatalf("forged Status survived Submit: got %v, want %v (New()'s own honest default)", got.Status, StatusUnverified)
	}
	if got.Strength != (Strength{}) {
		t.Fatalf("forged Strength survived Submit: got %+v, want the zero value", got.Strength)
	}
	if got.Rights != provenance.RightsUnknownPendingContract {
		t.Fatalf("forged Rights survived Submit: got %v, want %v", got.Rights, provenance.RightsUnknownPendingContract)
	}
	if got.CorrectionSuperseded {
		t.Fatal("forged CorrectionSuperseded=true survived Submit")
	}
	if got.SupersededBy != "" {
		t.Fatalf("forged SupersededBy survived Submit: got %q", got.SupersededBy)
	}
	// And the record lands on exactly New()'s own honest baseline:
	// UNKNOWN_PENDING_CONTRACT permits internal use only, nothing more.
	if !got.Permits(provenance.UseInternalOnly) {
		t.Fatal("expected the reset UNKNOWN_PENDING_CONTRACT rights state to still permit internal-only use")
	}
	if got.Permits(provenance.UseCustomerFacing) {
		t.Fatal("expected the reset rights state to NOT permit customer-facing use -- the forged CUSTOMER_FACING_ALLOWED must not survive")
	}
}

// TestSubmitDoesNotResetCallerOwnedDescriptiveFields confirms the fix
// is scoped correctly: only authority-bearing fields are reset. A
// caller's legitimate descriptive data -- including ChainOfCustody,
// which may honestly describe hand-offs that happened BEFORE the
// evidence ever reached VERIQO -- survives Submit unchanged.
func TestSubmitDoesNotResetCallerOwnedDescriptiveFields(t *testing.T) {
	reg := NewRegistry()
	rec := Record{
		CaseID: "CASE-1", Underlying: mustEvidence(t, "S1", "src", 100),
		SourcePartyID: "PTY-1", Origin: OriginSurveyor, DocumentType: "survey_report",
		ChainOfCustody: []CustodyEntry{{Holder: "field surveyor", Action: "collected", Tick: 50}},
		Metadata:       map[string]string{"note": "genuine descriptive data"},
	}
	if err := reg.Submit(rec); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	got, _ := reg.Get(rec.EvidenceID())
	if got.DocumentType != "survey_report" {
		t.Fatalf("DocumentType was incorrectly reset: got %q", got.DocumentType)
	}
	if len(got.ChainOfCustody) != 1 || got.ChainOfCustody[0].Holder != "field surveyor" {
		t.Fatalf("pre-VERIQO ChainOfCustody was incorrectly reset: got %+v", got.ChainOfCustody)
	}
	if got.Metadata["note"] != "genuine descriptive data" {
		t.Fatal("Metadata was incorrectly reset")
	}
}
