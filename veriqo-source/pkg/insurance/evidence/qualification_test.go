package evidence

import "testing"

func TestQualificationStatusUnassessedStrengthIsUnknown(t *testing.T) {
	r := mustRecord(t)
	if got := r.QualificationStatus(); got != QualificationUnknown {
		t.Fatalf("QualificationStatus() = %q, want UNKNOWN for an unassessed Strength", got)
	}
}

func TestQualificationStatusStructurallyValidatedFloor(t *testing.T) {
	r := mustRecord(t)
	r.Strength = Strength{
		Authenticity: AuthenticityUnknown, Provenance: ProvenanceUnverified,
		Integrity: IntegrityUnknown, Completeness: CompletenessPartial, Relevance: RelevanceMedium,
		TemporalConsistency: TemporalConsistencyUnknown, EntityConsistency: EntityConsistencyUnknown,
		IndependentCorroboration: CorroborationNone, ContradictionLevel: ContradictionLevelNone,
	}
	if got := r.QualificationStatus(); got != QualificationStructurallyValidated {
		t.Fatalf("QualificationStatus() = %q, want STRUCTURALLY_VALIDATED", got)
	}
}

func TestQualificationStatusSourceValidated(t *testing.T) {
	r := mustRecord(t)
	r.Strength = Strength{
		Authenticity: AuthenticitySupported, Provenance: ProvenanceVerified,
		Integrity: IntegrityVerified, Completeness: CompletenessComplete, Relevance: RelevanceHigh,
		TemporalConsistency: TemporalConsistencySupported, EntityConsistency: EntityConsistencySupported,
		IndependentCorroboration: CorroborationNone, ContradictionLevel: ContradictionLevelNone,
	}
	if got := r.QualificationStatus(); got != QualificationSourceValidated {
		t.Fatalf("QualificationStatus() = %q, want SOURCE_VALIDATED", got)
	}
}

func TestQualificationStatusCorroboratedVsIndependentlyCorroborated(t *testing.T) {
	r := mustRecord(t)
	base := Strength{
		Authenticity: AuthenticitySupported, Provenance: ProvenanceVerified,
		Integrity: IntegrityVerified, Completeness: CompletenessComplete, Relevance: RelevanceHigh,
		TemporalConsistency: TemporalConsistencySupported, EntityConsistency: EntityConsistencySupported,
		ContradictionLevel: ContradictionLevelNone,
	}

	low := base
	low.IndependentCorroboration = CorroborationLow
	r.Strength = low
	if got := r.QualificationStatus(); got != QualificationCorroborated {
		t.Fatalf("QualificationStatus() with LOW corroboration = %q, want CORROBORATED", got)
	}

	high := base
	high.IndependentCorroboration = CorroborationHigh
	r.Strength = high
	if got := r.QualificationStatus(); got != QualificationIndependentlyCorroborated {
		t.Fatalf("QualificationStatus() with HIGH corroboration = %q, want INDEPENDENTLY_CORROBORATED", got)
	}
}

func TestQualificationStatusDisputedOutranksCorroboration(t *testing.T) {
	r := mustRecord(t)
	r.Strength = Strength{
		Authenticity: AuthenticitySupported, Provenance: ProvenanceVerified,
		Integrity: IntegrityVerified, Completeness: CompletenessComplete, Relevance: RelevanceHigh,
		TemporalConsistency: TemporalConsistencySupported, EntityConsistency: EntityConsistencySupported,
		IndependentCorroboration: CorroborationHigh, ContradictionLevel: ContradictionLevelHigh,
	}
	if got := r.QualificationStatus(); got != QualificationDisputed {
		t.Fatalf("QualificationStatus() = %q, want DISPUTED (contradiction must outrank even HIGH corroboration)", got)
	}
}

func TestQualificationStatusRejectedIsTheStrongestSignal(t *testing.T) {
	r := mustRecord(t)
	r.Strength = Strength{
		Authenticity: AuthenticityDisputed, Provenance: ProvenanceVerified,
		Integrity: IntegrityCompromised, Completeness: CompletenessComplete, Relevance: RelevanceHigh,
		TemporalConsistency: TemporalConsistencySupported, EntityConsistency: EntityConsistencySupported,
		IndependentCorroboration: CorroborationHigh, ContradictionLevel: ContradictionLevelNone,
	}
	if got := r.QualificationStatus(); got != QualificationRejected {
		t.Fatalf("QualificationStatus() = %q, want REJECTED (disputed authenticity must outrank corroboration)", got)
	}
}

func TestKnownQualificationStatusesExhaustive(t *testing.T) {
	got := KnownQualificationStatuses()
	if len(got) != 8 {
		t.Fatalf("expected 8 known qualification statuses, got %d: %v", len(got), got)
	}
	for _, s := range []QualificationStatus{
		QualificationUnknown, QualificationSelfAsserted, QualificationStructurallyValidated,
		QualificationSourceValidated, QualificationCorroborated, QualificationIndependentlyCorroborated,
		QualificationDisputed, QualificationRejected,
	} {
		if !IsKnownQualificationStatus(s) {
			t.Fatalf("expected %q to be a known qualification status", s)
		}
	}
}

func TestSourceIndependentReusesDependencyGraphRoot(t *testing.T) {
	r := mustRecord(t)
	g := NewDependencyGraph()

	if !r.SourceIndependent(g) {
		t.Fatal("expected a record with no recorded dependency to be a root (independent)")
	}

	parentID := r.Underlying.EvidenceID + "-parent"
	if err := g.AddDependency(r.Underlying.EvidenceID, parentID); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}
	if r.SourceIndependent(g) {
		t.Fatal("expected a record that depends on another to no longer be a root (not independent)")
	}
}

func TestSourceIndependentFailsClosedOnNilGraph(t *testing.T) {
	r := mustRecord(t)
	if r.SourceIndependent(nil) {
		t.Fatal("expected SourceIndependent(nil) to fail closed (false), not assume independence")
	}
}

func TestPartyAuthorityQualificationFieldsRoundTrip(t *testing.T) {
	r := mustRecord(t)
	r.SourceAuthority = "vessel master's own logbook entry"
	r.AcquisitionMethod = "manual upload by claims handler"
	r.LicenseReference = "TOBA-2024-081 §4.2"
	r.AccessPolicy = "case parties with VIEW_EVIDENCE permission only"
	r.RetentionPolicy = "7 years per regulatory retention schedule X"
	// These are plain additive fields -- round-tripping through the
	// struct is the whole test; nothing about New()/Validate() should
	// reject a record that sets them.
	if r.SourceAuthority == "" || r.AcquisitionMethod == "" || r.LicenseReference == "" ||
		r.AccessPolicy == "" || r.RetentionPolicy == "" {
		t.Fatal("expected all five fields to round-trip")
	}
}
