package gap

import (
	"errors"
	"testing"

	"veriqo/pkg/evidence/ontology"
	"veriqo/pkg/insurance/claim"
	"veriqo/pkg/insurance/evidence"
	"veriqo/pkg/insurance/party"
)

// ---- test helpers ----

func mustEvidence(t *testing.T, subject, documentType string, observedAt uint64) ontology.Evidence {
	t.Helper()
	e, err := ontology.New(ontology.Evidence{
		Type:       ontology.TypeDocument,
		Subject:    subject,
		Predicate:  "describes",
		Object:     documentType,
		Source:     "src",
		ObservedAt: observedAt,
		Confidence: 0.9,
		Attributes: map[string]string{"document_hash": "deadbeef", "document_type": documentType},
	})
	if err != nil {
		t.Fatalf("ontology.New: %v", err)
	}
	return e
}

func mustRecord(t *testing.T, caseID, documentType string, tick uint64) evidence.Record {
	t.Helper()
	ev := mustEvidence(t, "S-"+documentType, documentType, tick)
	rec, err := evidence.New(caseID, ev, party.PartyID("PTY-CLAIMANT"), evidence.OriginClaimant)
	if err != nil {
		t.Fatalf("evidence.New: %v", err)
	}
	rec.DocumentType = documentType
	return rec
}

// ---- §26 worked example, reproduced as a real test ----
//
// Blueprint §26, verbatim:
//
//	Claim: reefer failure
//	Available: Temperature report
//	Missing: Raw logger data, Calibration certificate,
//	         Plug-in/out record, Pre-trip inspection
//
// claim.DefaultTypes()'s TypeReeferFailure only seeds three of the
// blueprint's four missing items (it does not include
// "pre_trip_inspection"); per the task instructions this test
// constructs a local TypeDefinition inline with the blueprint's exact
// four required items so the comparison below is a real, testable
// set-difference rather than a coincidence of the seed data.
func TestReeferFailureWorkedExample(t *testing.T) {
	def := claim.TypeDefinition{
		Type: claim.TypeReeferFailure,
		RequiredEvidence: []string{
			"temperature_report",
			"raw_logger_data",
			"calibration_certificate",
			"plug_in_out_record",
			"pre_trip_inspection",
		},
	}
	available := []string{"temperature_report"}

	report, err := DetectGaps(def, available)
	if err != nil {
		t.Fatalf("DetectGaps: %v", err)
	}

	wantPresent := []string{"temperature_report"}
	wantMissing := []string{
		"raw_logger_data",
		"calibration_certificate",
		"plug_in_out_record",
		"pre_trip_inspection",
	}

	if !stringSlicesEqual(report.RequiredPresent, wantPresent) {
		t.Fatalf("RequiredPresent = %v, want %v", report.RequiredPresent, wantPresent)
	}
	if !stringSlicesEqual(report.RequiredMissing, wantMissing) {
		t.Fatalf("RequiredMissing = %v, want %v", report.RequiredMissing, wantMissing)
	}
	if report.IsComplete() {
		t.Fatal("IsComplete() = true, want false: 4 required items are missing")
	}
	if report.ClaimType != claim.TypeReeferFailure {
		t.Fatalf("ClaimType = %v, want %v", report.ClaimType, claim.TypeReeferFailure)
	}
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---- DetectGaps: validation ----

func TestDetectGapsRejectsEmptyType(t *testing.T) {
	_, err := DetectGaps(claim.TypeDefinition{RequiredEvidence: []string{"x"}}, nil)
	if !errors.Is(err, ErrNilTypeDefinition) {
		t.Fatalf("expected ErrNilTypeDefinition, got %v", err)
	}
}

func TestDetectGapsRejectsEmptyRequiredEvidence(t *testing.T) {
	_, err := DetectGaps(claim.TypeDefinition{Type: claim.TypeCargoDamage}, nil)
	if !errors.Is(err, ErrEmptyRequiredInDef) {
		t.Fatalf("expected ErrEmptyRequiredInDef, got %v", err)
	}
}

// ---- DetectGaps: optional evidence tracked separately from required ----

func TestDetectGapsTracksOptionalSeparately(t *testing.T) {
	def := claim.TypeDefinition{
		Type:             claim.TypeCargoDamage,
		RequiredEvidence: []string{"survey_report", "bill_of_lading"},
		OptionalEvidence: []string{"weather_report", "photos"},
	}
	report, err := DetectGaps(def, []string{"survey_report", "bill_of_lading", "photos"})
	if err != nil {
		t.Fatalf("DetectGaps: %v", err)
	}
	if !stringSlicesEqual(report.RequiredPresent, []string{"survey_report", "bill_of_lading"}) {
		t.Fatalf("RequiredPresent = %v", report.RequiredPresent)
	}
	if len(report.RequiredMissing) != 0 {
		t.Fatalf("RequiredMissing = %v, want empty", report.RequiredMissing)
	}
	if !stringSlicesEqual(report.OptionalPresent, []string{"photos"}) {
		t.Fatalf("OptionalPresent = %v", report.OptionalPresent)
	}
	if !stringSlicesEqual(report.OptionalMissing, []string{"weather_report"}) {
		t.Fatalf("OptionalMissing = %v", report.OptionalMissing)
	}
	// A missing OPTIONAL item must never affect IsComplete.
	if !report.IsComplete() {
		t.Fatalf("IsComplete() = false, want true: all required evidence present regardless of optional gaps")
	}
}

// ---- DetectGaps: never fabricates, never omits ----

func TestDetectGapsExactSetDifferenceNoFabrication(t *testing.T) {
	def := claim.TypeDefinition{
		Type:             claim.TypeCargoTheft,
		RequiredEvidence: []string{"police_report", "bill_of_lading", "cctv_or_seal_record"},
	}
	// Submit an item that isn't even a required/optional item for this
	// claim type (e.g. an unrelated document type) plus one real match.
	report, err := DetectGaps(def, []string{"bill_of_lading", "unrelated_document_type"})
	if err != nil {
		t.Fatalf("DetectGaps: %v", err)
	}
	if !stringSlicesEqual(report.RequiredPresent, []string{"bill_of_lading"}) {
		t.Fatalf("RequiredPresent = %v, want only bill_of_lading (no fabricated matches)", report.RequiredPresent)
	}
	if !stringSlicesEqual(report.RequiredMissing, []string{"police_report", "cctv_or_seal_record"}) {
		t.Fatalf("RequiredMissing = %v, want exactly the two genuinely-missing items", report.RequiredMissing)
	}
}

func TestDetectGapsAllPresentMeansNoMissing(t *testing.T) {
	def := claim.TypeDefinition{
		Type:             claim.TypeCargoShortage,
		RequiredEvidence: []string{"packing_list", "bill_of_lading", "delivery_receipt"},
	}
	report, err := DetectGaps(def, []string{"packing_list", "bill_of_lading", "delivery_receipt"})
	if err != nil {
		t.Fatalf("DetectGaps: %v", err)
	}
	if len(report.RequiredMissing) != 0 {
		t.Fatalf("RequiredMissing = %v, want empty", report.RequiredMissing)
	}
	if !report.IsComplete() {
		t.Fatal("IsComplete() = false, want true")
	}
}

func TestDetectGapsIgnoresEmptyPresentEntries(t *testing.T) {
	def := claim.TypeDefinition{Type: claim.TypeDelay, RequiredEvidence: []string{"voyage_records", "notice_of_delay"}}
	report, err := DetectGaps(def, []string{"", "voyage_records", ""})
	if err != nil {
		t.Fatalf("DetectGaps: %v", err)
	}
	if !stringSlicesEqual(report.RequiredPresent, []string{"voyage_records"}) {
		t.Fatalf("RequiredPresent = %v", report.RequiredPresent)
	}
	if !stringSlicesEqual(report.RequiredMissing, []string{"notice_of_delay"}) {
		t.Fatalf("RequiredMissing = %v", report.RequiredMissing)
	}
}

// ---- DetectGapsFromRegistry ----

func TestDetectGapsFromRegistryReadsDocumentTypesForCaseOnly(t *testing.T) {
	reg := evidence.NewRegistry()
	if err := reg.Submit(mustRecord(t, "CASE-1", "temperature_report", 100)); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	// A record for a DIFFERENT case must not count toward CASE-1's gaps.
	if err := reg.Submit(mustRecord(t, "CASE-2", "raw_logger_data", 101)); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	def := claim.TypeDefinition{
		Type: claim.TypeReeferFailure,
		RequiredEvidence: []string{
			"temperature_report", "raw_logger_data", "calibration_certificate", "plug_in_out_record",
		},
	}
	report, err := DetectGapsFromRegistry(def, reg, "CASE-1")
	if err != nil {
		t.Fatalf("DetectGapsFromRegistry: %v", err)
	}
	if !stringSlicesEqual(report.RequiredPresent, []string{"temperature_report"}) {
		t.Fatalf("RequiredPresent = %v, want only temperature_report (CASE-2's evidence must not leak in)", report.RequiredPresent)
	}
	want := []string{"raw_logger_data", "calibration_certificate", "plug_in_out_record"}
	if !stringSlicesEqual(report.RequiredMissing, want) {
		t.Fatalf("RequiredMissing = %v, want %v", report.RequiredMissing, want)
	}
}

func TestDetectGapsFromRegistryNilRegistryMeansNothingPresent(t *testing.T) {
	def := claim.TypeDefinition{Type: claim.TypeFire, RequiredEvidence: []string{"incident_report"}}
	report, err := DetectGapsFromRegistry(def, nil, "CASE-1")
	if err != nil {
		t.Fatalf("DetectGapsFromRegistry: %v", err)
	}
	if !stringSlicesEqual(report.RequiredMissing, []string{"incident_report"}) {
		t.Fatalf("RequiredMissing = %v, want [incident_report]", report.RequiredMissing)
	}
}

func TestDetectGapsFromRegistrySkipsRecordsWithNoDocumentType(t *testing.T) {
	reg := evidence.NewRegistry()
	rec := mustRecord(t, "CASE-1", "survey_report", 100)
	rec.DocumentType = "" // simulate an ingested record whose type was never classified
	if err := reg.Submit(rec); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	def := claim.TypeDefinition{Type: claim.TypeCargoDamage, RequiredEvidence: []string{"survey_report"}}
	report, err := DetectGapsFromRegistry(def, reg, "CASE-1")
	if err != nil {
		t.Fatalf("DetectGapsFromRegistry: %v", err)
	}
	if !stringSlicesEqual(report.RequiredMissing, []string{"survey_report"}) {
		t.Fatalf("RequiredMissing = %v, want [survey_report]: an untyped record must not silently satisfy a requirement", report.RequiredMissing)
	}
}

// ---- Status / Sufficiency validation ----

func TestIsKnownStatus(t *testing.T) {
	for _, s := range []Status{StatusComplete, StatusPartial, StatusInsufficient, StatusContradicted} {
		if !IsKnownStatus(s) {
			t.Errorf("IsKnownStatus(%q) = false, want true", s)
		}
	}
	if IsKnownStatus(Status("HUMAN_REVIEW_REQUIRED")) {
		t.Error("IsKnownStatus(HUMAN_REVIEW_REQUIRED) = true, want false: not one of the 4 core values")
	}
	if IsKnownStatus(Status("")) {
		t.Error("IsKnownStatus(\"\") = true, want false")
	}
}

func TestSufficiencyValidate(t *testing.T) {
	valid := Sufficiency{Issue: "Causation", Status: StatusPartial}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() on well-formed Sufficiency: %v", err)
	}
	if err := (Sufficiency{Status: StatusComplete}).Validate(); !errors.Is(err, ErrEmptyIssue) {
		t.Fatalf("expected ErrEmptyIssue, got %v", err)
	}
	if err := (Sufficiency{Issue: "Notice", Status: "BOGUS"}).Validate(); !errors.Is(err, ErrUnknownStatus) {
		t.Fatalf("expected ErrUnknownStatus, got %v", err)
	}
}

// ---- ComputeSufficiency: exact boundary semantics ----
//
// 0 of N required present  -> INSUFFICIENT
// some but not all present -> PARTIAL
// N of N required present  -> COMPLETE
// contradicted overrides everything -> CONTRADICTED

func TestComputeSufficiencyBoundaries(t *testing.T) {
	tests := []struct {
		name         string
		required     int
		present      int
		contradicted bool
		wantStatus   Status
	}{
		{"zero of three present", 3, 0, false, StatusInsufficient},
		{"two of three present", 3, 2, false, StatusPartial},
		{"three of three present", 3, 3, false, StatusComplete},
		{"one of one present", 1, 1, false, StatusComplete},
		{"zero of one present", 1, 0, false, StatusInsufficient},
		{"contradicted overrides complete", 3, 3, true, StatusContradicted},
		{"contradicted overrides partial", 3, 2, true, StatusContradicted},
		{"contradicted overrides insufficient", 3, 0, true, StatusContradicted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ComputeSufficiency("TestIssue", tt.required, tt.present, tt.contradicted)
			if err != nil {
				t.Fatalf("ComputeSufficiency: %v", err)
			}
			if got.Status != tt.wantStatus {
				t.Fatalf("Status = %v, want %v", got.Status, tt.wantStatus)
			}
			if got.Reason == "" {
				t.Fatal("Reason must not be empty")
			}
		})
	}
}

func TestComputeSufficiencyRejectsEmptyIssue(t *testing.T) {
	_, err := ComputeSufficiency("", 3, 1, false)
	if !errors.Is(err, ErrEmptyIssue) {
		t.Fatalf("expected ErrEmptyIssue, got %v", err)
	}
}

func TestComputeSufficiencyRejectsNegativeCounts(t *testing.T) {
	if _, err := ComputeSufficiency("X", -1, 0, false); !errors.Is(err, ErrNegativeCounts) {
		t.Fatalf("expected ErrNegativeCounts for negative required, got %v", err)
	}
	if _, err := ComputeSufficiency("X", 3, -1, false); !errors.Is(err, ErrNegativeCounts) {
		t.Fatalf("expected ErrNegativeCounts for negative present, got %v", err)
	}
}

func TestComputeSufficiencyRejectsPresentExceedingRequired(t *testing.T) {
	_, err := ComputeSufficiency("X", 2, 3, false)
	if !errors.Is(err, ErrPresentExceedsReq) {
		t.Fatalf("expected ErrPresentExceedsReq, got %v", err)
	}
}

// ---- THE golden-rule adversarial test (§36): insufficient evidence
// must never be silently upgraded to COMPLETE, and missing items must
// never be dropped from the gap report. This is also blueprint §35's
// adversarial test #20 ("Insufficient evidence"). ----

func TestGoldenRule_InsufficientEvidenceNeverBecomesComplete(t *testing.T) {
	def := claim.TypeDefinition{
		Type: claim.TypeCollision,
		RequiredEvidence: []string{
			"incident_report", "ais_data", "collision_report",
		},
	}
	// Adversarial: claimant submits NOTHING that matches what is
	// actually required — only an unrelated document.
	report, err := DetectGaps(def, []string{"unrelated_marketing_brochure"})
	if err != nil {
		t.Fatalf("DetectGaps: %v", err)
	}
	if report.IsComplete() {
		t.Fatal("adversarial: IsComplete() = true with zero matching required evidence — golden rule violated")
	}
	wantMissing := []string{"incident_report", "ais_data", "collision_report"}
	if !stringSlicesEqual(report.RequiredMissing, wantMissing) {
		t.Fatalf("RequiredMissing = %v, want %v: missing items must never be omitted from the gap report", report.RequiredMissing, wantMissing)
	}
	if len(report.RequiredPresent) != 0 {
		t.Fatalf("RequiredPresent = %v, want empty: an unrelated document must never be fabricated into a match", report.RequiredPresent)
	}

	suff, err := SufficiencyFromReport("Causation", report, false)
	if err != nil {
		t.Fatalf("SufficiencyFromReport: %v", err)
	}
	if suff.Status != StatusInsufficient {
		t.Fatalf("Status = %v, want INSUFFICIENT — never a guessed conclusion when evidence is this thin", suff.Status)
	}
	if suff.Status == StatusComplete {
		t.Fatal("golden rule violated: insufficient evidence must never silently default to COMPLETE")
	}
}

func TestSufficiencyFromReportPartial(t *testing.T) {
	def := claim.TypeDefinition{
		Type:             claim.TypeCargoDamage,
		RequiredEvidence: []string{"survey_report", "bill_of_lading", "photos"},
	}
	report, err := DetectGaps(def, []string{"survey_report", "bill_of_lading"})
	if err != nil {
		t.Fatalf("DetectGaps: %v", err)
	}
	suff, err := SufficiencyFromReport("Quantum", report, false)
	if err != nil {
		t.Fatalf("SufficiencyFromReport: %v", err)
	}
	if suff.Status != StatusPartial {
		t.Fatalf("Status = %v, want PARTIAL (2 of 3 required present)", suff.Status)
	}
}

func TestSufficiencyFromReportComplete(t *testing.T) {
	def := claim.TypeDefinition{
		Type:             claim.TypeCargoDamage,
		RequiredEvidence: []string{"survey_report", "bill_of_lading", "photos"},
	}
	report, err := DetectGaps(def, []string{"survey_report", "bill_of_lading", "photos"})
	if err != nil {
		t.Fatalf("DetectGaps: %v", err)
	}
	suff, err := SufficiencyFromReport("Notice", report, false)
	if err != nil {
		t.Fatalf("SufficiencyFromReport: %v", err)
	}
	if suff.Status != StatusComplete {
		t.Fatalf("Status = %v, want COMPLETE (3 of 3 required present)", suff.Status)
	}
}

// ---- §27 worked example reproduced on one Assessment ----
//
// Causation: PARTIAL, Quantum: COMPLETE, Notice: COMPLETE,
// Coverage: sufficiency COMPLETE but HumanReviewRequired (see the
// design-decision comment on Sufficiency for why HUMAN_REVIEW_REQUIRED
// is modelled as an orthogonal flag rather than a 5th status value).
func TestSection27WorkedExampleAssessment(t *testing.T) {
	a := NewAssessment("CLAIM-1")

	mustSet := func(s Sufficiency) {
		t.Helper()
		if err := a.Set(s); err != nil {
			t.Fatalf("Set(%+v): %v", s, err)
		}
	}

	mustSet(Sufficiency{Issue: "Causation", Status: StatusPartial, Reason: "custody interval incomplete"})
	mustSet(Sufficiency{Issue: "Quantum", Status: StatusComplete})
	mustSet(Sufficiency{Issue: "Notice", Status: StatusComplete})
	mustSet(Sufficiency{Issue: "Coverage", Status: StatusComplete, HumanReviewRequired: true, Reason: "evidentially settled but legally novel exclusion question"})

	if a.Count() != 4 {
		t.Fatalf("Count() = %d, want 4", a.Count())
	}

	causation, ok := a.Get("Causation")
	if !ok || causation.Status != StatusPartial {
		t.Fatalf("Causation = %+v, ok=%v, want PARTIAL", causation, ok)
	}
	coverage, ok := a.Get("Coverage")
	if !ok {
		t.Fatal("Coverage not found")
	}
	if coverage.Status != StatusComplete {
		t.Fatalf("Coverage.Status = %v, want COMPLETE (evidence-wise)", coverage.Status)
	}
	if !coverage.HumanReviewRequired {
		t.Fatal("Coverage.HumanReviewRequired = false, want true — §27's HUMAN_REVIEW_REQUIRED example must still surface")
	}

	// The overall Assessment is NOT fully complete because Causation is
	// PARTIAL and Coverage needs human review, even though 2 of 4 issues
	// are individually COMPLETE.
	if a.IsFullyComplete() {
		t.Fatal("IsFullyComplete() = true, want false: Causation is PARTIAL and Coverage needs human review")
	}
	if a.HasInsufficientOrContradicted() {
		t.Fatal("HasInsufficientOrContradicted() = true, want false: no issue here is INSUFFICIENT or CONTRADICTED")
	}

	sorted := a.SortedByIssue()
	wantOrder := []string{"Causation", "Coverage", "Notice", "Quantum"}
	for i, s := range sorted {
		if s.Issue != wantOrder[i] {
			t.Fatalf("SortedByIssue()[%d].Issue = %q, want %q", i, s.Issue, wantOrder[i])
		}
	}
}

// ---- Assessment: validation, duplicate protection, ordering ----

func TestAssessmentSetRejectsInvalidSufficiency(t *testing.T) {
	a := NewAssessment("CLAIM-1")
	if err := a.Set(Sufficiency{Status: StatusComplete}); !errors.Is(err, ErrEmptyIssue) {
		t.Fatalf("expected ErrEmptyIssue, got %v", err)
	}
}

func TestAssessmentSetRefusesSilentClobber(t *testing.T) {
	a := NewAssessment("CLAIM-1")
	if err := a.Set(Sufficiency{Issue: "Causation", Status: StatusPartial}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// Setting a DIFFERENT rating for the same issue via Set (not
	// Overwrite) must be refused.
	err := a.Set(Sufficiency{Issue: "Causation", Status: StatusComplete})
	if !errors.Is(err, ErrDuplicateIssue) {
		t.Fatalf("expected ErrDuplicateIssue, got %v", err)
	}
	got, _ := a.Get("Causation")
	if got.Status != StatusPartial {
		t.Fatalf("Causation.Status = %v, want unchanged PARTIAL after refused Set", got.Status)
	}
}

func TestAssessmentSetIsIdempotentForIdenticalRating(t *testing.T) {
	a := NewAssessment("CLAIM-1")
	s := Sufficiency{Issue: "Notice", Status: StatusComplete}
	if err := a.Set(s); err != nil {
		t.Fatalf("first Set: %v", err)
	}
	if err := a.Set(s); err != nil {
		t.Fatalf("second identical Set should be idempotent, got: %v", err)
	}
	if a.Count() != 1 {
		t.Fatalf("Count() = %d, want 1", a.Count())
	}
}

func TestAssessmentOverwriteAllowsRevision(t *testing.T) {
	a := NewAssessment("CLAIM-1")
	if err := a.Set(Sufficiency{Issue: "Quantum", Status: StatusPartial}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := a.Overwrite(Sufficiency{Issue: "Quantum", Status: StatusComplete, Reason: "new invoice evidence arrived"}); err != nil {
		t.Fatalf("Overwrite: %v", err)
	}
	got, ok := a.Get("Quantum")
	if !ok || got.Status != StatusComplete {
		t.Fatalf("Quantum = %+v, ok=%v, want COMPLETE after Overwrite", got, ok)
	}
	if a.Count() != 1 {
		t.Fatalf("Count() = %d, want 1 (Overwrite must not duplicate the issue entry)", a.Count())
	}
}

func TestAssessmentAllPreservesFirstSetOrder(t *testing.T) {
	a := NewAssessment("CLAIM-1")
	order := []string{"Coverage", "Causation", "Notice", "Quantum"}
	for _, issue := range order {
		if err := a.Set(Sufficiency{Issue: issue, Status: StatusComplete}); err != nil {
			t.Fatalf("Set(%s): %v", issue, err)
		}
	}
	got := a.All()
	if len(got) != len(order) {
		t.Fatalf("All() length = %d, want %d", len(got), len(order))
	}
	for i, s := range got {
		if s.Issue != order[i] {
			t.Fatalf("All()[%d].Issue = %q, want %q (must preserve first-Set order)", i, s.Issue, order[i])
		}
	}
}

// ---- IsFullyComplete / HasInsufficientOrContradicted edge cases ----

func TestAssessmentIsFullyCompleteEmptyIsFalse(t *testing.T) {
	a := NewAssessment("CLAIM-1")
	if a.IsFullyComplete() {
		t.Fatal("IsFullyComplete() on an empty (never-assessed) Assessment must be false, not true")
	}
}

func TestAssessmentIsFullyCompleteAllComplete(t *testing.T) {
	a := NewAssessment("CLAIM-1")
	for _, issue := range []string{"Causation", "Quantum", "Notice", "Coverage"} {
		if err := a.Set(Sufficiency{Issue: issue, Status: StatusComplete}); err != nil {
			t.Fatalf("Set(%s): %v", issue, err)
		}
	}
	if !a.IsFullyComplete() {
		t.Fatal("IsFullyComplete() = false, want true: every issue is COMPLETE with no human-review flag")
	}
}

func TestAssessmentHasInsufficientOrContradictedDetectsEither(t *testing.T) {
	a1 := NewAssessment("C1")
	_ = a1.Set(Sufficiency{Issue: "Causation", Status: StatusInsufficient})
	if !a1.HasInsufficientOrContradicted() {
		t.Fatal("expected true for an INSUFFICIENT issue")
	}

	a2 := NewAssessment("C2")
	_ = a2.Set(Sufficiency{Issue: "Causation", Status: StatusContradicted})
	if !a2.HasInsufficientOrContradicted() {
		t.Fatal("expected true for a CONTRADICTED issue")
	}

	a3 := NewAssessment("C3")
	_ = a3.Set(Sufficiency{Issue: "Causation", Status: StatusComplete})
	if a3.HasInsufficientOrContradicted() {
		t.Fatal("expected false when no issue is INSUFFICIENT or CONTRADICTED")
	}
}
