package coverage

import (
	"errors"
	"strings"
	"testing"

	"veriqo/pkg/insurance/claim"
	"veriqo/pkg/insurance/evidence"
	"veriqo/pkg/insurance/policy"
)

func factByID(t *testing.T, a CoverageAnalysis, id string) CoverageFact {
	t.Helper()
	for _, f := range a.Facts {
		if f.FactID == id {
			return f
		}
	}
	t.Fatalf("no fact with FactID %q in %v", id, a.Facts)
	return CoverageFact{}
}

// ---- input validation ----

func TestAnalyzeRejectsEmptyClaimID(t *testing.T) {
	_, err := Analyze(Input{PolicyVersion: basePolicyVersion("V1")})
	if !errors.Is(err, ErrEmptyClaimID) {
		t.Fatalf("expected ErrEmptyClaimID, got %v", err)
	}
}

func TestAnalyzeRejectsEmptyPolicyVersionID(t *testing.T) {
	_, err := Analyze(Input{Claim: baseClaim("")})
	if !errors.Is(err, ErrEmptyPolicyVersionID) {
		t.Fatalf("expected ErrEmptyPolicyVersionID, got %v", err)
	}
}

// TestAnalyzeRejectsPolicyVersionMismatch proves Analyze will not
// silently accept a policy.Version other than the one already pinned
// to the claim (e.g. via a prior policy.History.EffectiveAt
// resolution) — it must be handed the EFFECTIVE version, not just any
// version of the same policy.
func TestAnalyzeRejectsPolicyVersionMismatch(t *testing.T) {
	cl := baseClaim("POL-1-V-EFFECTIVE")
	pv := basePolicyVersion("POL-1-V-OTHER")
	_, err := Analyze(Input{Claim: cl, PolicyVersion: pv, IncidentTick: 1500})
	if !errors.Is(err, ErrPolicyVersionMismatch) {
		t.Fatalf("expected ErrPolicyVersionMismatch, got %v", err)
	}
}

// TestAnalyzeAcceptsUnpinnedClaim covers the case where the claim
// hasn't yet had PolicyVersionID resolved — Analyze must still work
// (the field is optional), it just cannot cross-check it.
func TestAnalyzeAcceptsUnpinnedClaim(t *testing.T) {
	cl := baseClaim("") // not yet pinned
	pv := basePolicyVersion("V1")
	a, err := Analyze(Input{Claim: cl, PolicyVersion: pv, IncidentTick: 1500})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if a.PolicyVersionID != "V1" {
		t.Fatalf("expected PolicyVersionID V1, got %s", a.PolicyVersionID)
	}
}

// ---- within_policy_period ----

func TestWithinPolicyPeriod_SupportedWhenTickInsideWindow(t *testing.T) {
	cl := baseClaim("V1")
	pv := basePolicyVersion("V1")
	a, err := Analyze(Input{Claim: cl, PolicyVersion: pv, IncidentTick: 1500, PeriodClauseID: "§2"})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	f := factByID(t, a, "within_policy_period")
	if f.Status != StatusSupported {
		t.Fatalf("expected StatusSupported, got %s: %s", f.Status, f.Description)
	}
	if len(f.ClauseIDs) != 1 || f.ClauseIDs[0] != "§2" {
		t.Fatalf("expected ClauseIDs [§2], got %v", f.ClauseIDs)
	}
	if f.PolicyVersionID != "V1" {
		t.Fatalf("expected fact tied to PolicyVersionID V1, got %s", f.PolicyVersionID)
	}
}

func TestWithinPolicyPeriod_SupportedButOutsideWindow(t *testing.T) {
	cl := baseClaim("V1")
	pv := basePolicyVersion("V1") // [1000, 3000)
	a, err := Analyze(Input{Claim: cl, PolicyVersion: pv, IncidentTick: 5000})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	f := factByID(t, a, "within_policy_period")
	// The finding itself ("outside the period") is well-supported by
	// the tick comparison, even though it is an unfavourable finding
	// for the claim — FactStatus measures evidentiary support, not
	// which way a finding cuts.
	if f.Status != StatusSupported {
		t.Fatalf("expected StatusSupported (well-evidenced negative finding), got %s", f.Status)
	}
	if !strings.Contains(f.Description, "outside") {
		t.Fatalf("expected description to say the incident falls outside the period, got %q", f.Description)
	}
}

// TestWithinPolicyPeriod_InsufficientEvidenceWhenIncidentUnknown is a
// blueprint golden-rule #3 case: missing data must be an explicit
// unresolved marker, never silently omitted or defaulted to Supported.
func TestWithinPolicyPeriod_InsufficientEvidenceWhenIncidentUnknown(t *testing.T) {
	cl := baseClaim("V1")
	pv := basePolicyVersion("V1")
	a, err := Analyze(Input{Claim: cl, PolicyVersion: pv}) // IncidentTick left 0/unknown
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	f := factByID(t, a, "within_policy_period")
	if f.Status != StatusInsufficientEvidence {
		t.Fatalf("expected StatusInsufficientEvidence, got %s", f.Status)
	}
	if !a.ReviewRequired {
		t.Fatal("expected ReviewRequired when a fact is insufficient")
	}
}

func TestOpenEndedPolicyVersionCoversAnyLaterTick(t *testing.T) {
	cl := baseClaim("V1")
	pv := basePolicyVersion("V1")
	pv.EffectiveTo = 0 // open-ended
	a, err := Analyze(Input{Claim: cl, PolicyVersion: pv, IncidentTick: 999999})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	f := factByID(t, a, "within_policy_period")
	if !strings.Contains(f.Description, "falls within") {
		t.Fatalf("expected an open-ended version to cover a far-future tick, got %q", f.Description)
	}
}

// ---- peril_occurred ----

func TestPerilOccurred_SupportedWithCleanEvidence(t *testing.T) {
	cl := baseClaim("V1")
	pv := basePolicyVersion("V1")
	survey := mustRecord(t, "survey-1", "survey_report", evidence.StatusAuthenticitySupported)
	incident := mustRecord(t, "incident-1", "incident_report", evidence.StatusCorroborated)
	a, err := Analyze(Input{
		Claim: cl, PolicyVersion: pv, IncidentTick: 1500,
		Evidence:      []evidence.Record{survey, incident},
		PerilDocTypes: []string{"survey_report", "incident_report"},
		PerilClauseID: "§4",
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	f := factByID(t, a, "peril_occurred")
	if f.Status != StatusSupported {
		t.Fatalf("expected StatusSupported, got %s", f.Status)
	}
	if len(f.EvidenceIDs) != 2 {
		t.Fatalf("expected 2 evidence IDs, got %v", f.EvidenceIDs)
	}
	if len(f.ClauseIDs) != 1 || f.ClauseIDs[0] != "§4" {
		t.Fatalf("expected ClauseIDs [§4], got %v", f.ClauseIDs)
	}
}

// TestPerilOccurred_InsufficientEvidenceWhenNoMatchingEvidence is
// golden-rule #3: absent evidence must be an explicit
// StatusInsufficientEvidence marker in the output, not an omitted fact
// and not a silent StatusSupported default.
func TestPerilOccurred_InsufficientEvidenceWhenNoMatchingEvidence(t *testing.T) {
	cl := baseClaim("V1")
	pv := basePolicyVersion("V1")
	a, err := Analyze(Input{
		Claim: cl, PolicyVersion: pv, IncidentTick: 1500,
		PerilDocTypes: []string{"survey_report", "incident_report"},
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	f := factByID(t, a, "peril_occurred")
	if f.Status != StatusInsufficientEvidence {
		t.Fatalf("expected StatusInsufficientEvidence, got %s", f.Status)
	}
	if len(f.EvidenceIDs) != 0 {
		t.Fatalf("expected no evidence IDs, got %v", f.EvidenceIDs)
	}
	// The fact must be present in the output, not omitted.
	found := false
	for _, ff := range a.Facts {
		if ff.FactID == "peril_occurred" {
			found = true
		}
	}
	if !found {
		t.Fatal("peril_occurred fact was omitted from output instead of being marked insufficient")
	}
}

func TestPerilOccurred_DisputedWhenEvidenceContradicted(t *testing.T) {
	cl := baseClaim("V1")
	pv := basePolicyVersion("V1")
	survey := mustRecord(t, "survey-2", "survey_report", evidence.StatusContradicted)
	a, err := Analyze(Input{
		Claim: cl, PolicyVersion: pv, IncidentTick: 1500,
		Evidence:      []evidence.Record{survey},
		PerilDocTypes: []string{"survey_report"},
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	f := factByID(t, a, "peril_occurred")
	if f.Status != StatusDisputed {
		t.Fatalf("expected StatusDisputed, got %s", f.Status)
	}
}

// ---- exclusion_applies (golden rule #2) ----

// TestExclusionApplies_AlwaysDisputedAndTriggersConflictAndReview is
// the blueprint golden-rule #2 adversarial test: an exclusion clause
// that could apply but is contested must produce status
// Disputed/ReviewRequired — never silently resolved either way, in
// either direction (never silently "excluded" and never silently
// "not excluded").
func TestExclusionApplies_AlwaysDisputedAndTriggersConflictAndReview(t *testing.T) {
	cl := baseClaim("V1")
	pv := basePolicyVersion("V1")
	survey := mustRecord(t, "survey-3", "survey_report", evidence.StatusAuthenticitySupported)
	a, err := Analyze(Input{
		Claim: cl, PolicyVersion: pv, IncidentTick: 1500,
		Evidence:           []evidence.Record{survey},
		PerilDocTypes:      []string{"survey_report"},
		PerilClauseID:      "§4",
		ExclusionClauseIDs: []string{"§8"},
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	f := factByID(t, a, "exclusion_applies:§8")
	if f.Status != StatusDisputed {
		t.Fatalf("expected StatusDisputed for a potentially-applicable exclusion, got %s", f.Status)
	}
	if len(a.Conflicts) != 1 {
		t.Fatalf("expected exactly 1 conflict (peril supported vs. exclusion contested), got %d: %v", len(a.Conflicts), a.Conflicts)
	}
	c := a.Conflicts[0]
	if c.Status != StatusDisputed {
		t.Fatalf("expected conflict StatusDisputed, got %s", c.Status)
	}
	if !a.ReviewRequired {
		t.Fatal("expected ReviewRequired to be true when an exclusion is contested")
	}
}

func TestExclusionApplies_NoConflictWhenPerilNotSupported(t *testing.T) {
	cl := baseClaim("V1")
	pv := basePolicyVersion("V1")
	a, err := Analyze(Input{
		Claim: cl, PolicyVersion: pv, IncidentTick: 1500,
		// No peril evidence at all -> peril_occurred is INSUFFICIENT_EVIDENCE, not SUPPORTED.
		ExclusionClauseIDs: []string{"§8"},
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(a.Conflicts) != 0 {
		t.Fatalf("expected no exclusion-vs-peril conflict when peril itself is not established, got %v", a.Conflicts)
	}
	// The exclusion fact itself must still be present and DISPUTED —
	// it is never dropped just because there is no conflict to pair it with.
	f := factByID(t, a, "exclusion_applies:§8")
	if f.Status != StatusDisputed {
		t.Fatalf("expected StatusDisputed, got %s", f.Status)
	}
}

// ---- notice_timely ----

func TestNoticeTimely_DisputedWhenNoticeBeforeIncident(t *testing.T) {
	// Blueprint §14: "notice submitted before incident" is a named
	// temporal-conflict case this package must detect for real.
	cl := baseClaim("V1")
	pv := basePolicyVersion("V1")
	a, err := Analyze(Input{
		Claim: cl, PolicyVersion: pv,
		IncidentTick: 2000, NoticeTick: 1500,
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	f := factByID(t, a, "notice_timely")
	if f.Status != StatusDisputed {
		t.Fatalf("expected StatusDisputed for notice-before-incident, got %s", f.Status)
	}
}

func TestNoticeTimely_SupportedWithinDeadline(t *testing.T) {
	cl := baseClaim("V1")
	pv := basePolicyVersion("V1")
	a, err := Analyze(Input{
		Claim: cl, PolicyVersion: pv,
		IncidentTick: 1000, NoticeTick: 1100, NoticeDeadlineTick: 1200,
		NoticeClauseID: "§11",
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	f := factByID(t, a, "notice_timely")
	if f.Status != StatusSupported {
		t.Fatalf("expected StatusSupported, got %s", f.Status)
	}
}

func TestNoticeTimely_DisputedAfterDeadline(t *testing.T) {
	cl := baseClaim("V1")
	pv := basePolicyVersion("V1")
	a, err := Analyze(Input{
		Claim: cl, PolicyVersion: pv,
		IncidentTick: 1000, NoticeTick: 1300, NoticeDeadlineTick: 1200,
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	f := factByID(t, a, "notice_timely")
	if f.Status != StatusDisputed {
		t.Fatalf("expected StatusDisputed for notice after deadline, got %s", f.Status)
	}
}

func TestNoticeTimely_PartialWhenNoDeadlineRuleSupplied(t *testing.T) {
	cl := baseClaim("V1")
	pv := basePolicyVersion("V1")
	a, err := Analyze(Input{
		Claim: cl, PolicyVersion: pv,
		IncidentTick: 1000, NoticeTick: 1100, // no NoticeDeadlineTick
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	f := factByID(t, a, "notice_timely")
	if f.Status != StatusPartial {
		t.Fatalf("expected StatusPartial, got %s", f.Status)
	}
}

func TestNoticeTimely_InsufficientEvidenceWhenNoticeTickUnknown(t *testing.T) {
	cl := baseClaim("V1")
	pv := basePolicyVersion("V1")
	a, err := Analyze(Input{Claim: cl, PolicyVersion: pv, IncidentTick: 1000})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	f := factByID(t, a, "notice_timely")
	if f.Status != StatusInsufficientEvidence {
		t.Fatalf("expected StatusInsufficientEvidence, got %s", f.Status)
	}
}

// ---- quantum_supported ----

func TestQuantumSupported_PartialWhenOnlySomeDocTypesPresent(t *testing.T) {
	// Reproduces the blueprint's own §19 worked example: invoice +
	// survey required, only partial evidence available -> Partial.
	cl := baseClaim("V1")
	pv := basePolicyVersion("V1")
	invoice := mustRecord(t, "invoice-1", "invoice", evidence.StatusAuthenticitySupported)
	a, err := Analyze(Input{
		Claim: cl, PolicyVersion: pv, IncidentTick: 1500,
		Evidence:        []evidence.Record{invoice},
		QuantumDocTypes: []string{"invoice", "survey_report"},
		QuantumClauseID: "§14",
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	f := factByID(t, a, "quantum_supported")
	if f.Status != StatusPartial {
		t.Fatalf("expected StatusPartial, got %s", f.Status)
	}
}

func TestQuantumSupported_SupportedWhenAllDocTypesPresentAndClean(t *testing.T) {
	cl := baseClaim("V1")
	pv := basePolicyVersion("V1")
	invoice := mustRecord(t, "invoice-2", "invoice", evidence.StatusAuthenticitySupported)
	survey := mustRecord(t, "survey-4", "survey_report", evidence.StatusCorroborated)
	a, err := Analyze(Input{
		Claim: cl, PolicyVersion: pv, IncidentTick: 1500,
		Evidence:        []evidence.Record{invoice, survey},
		QuantumDocTypes: []string{"invoice", "survey_report"},
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	f := factByID(t, a, "quantum_supported")
	if f.Status != StatusSupported {
		t.Fatalf("expected StatusSupported, got %s", f.Status)
	}
}

func TestQuantumSupported_DisputedWhenContradicted(t *testing.T) {
	cl := baseClaim("V1")
	pv := basePolicyVersion("V1")
	invoice := mustRecord(t, "invoice-3", "invoice", evidence.StatusContradicted)
	survey := mustRecord(t, "survey-5", "survey_report", evidence.StatusAuthenticitySupported)
	a, err := Analyze(Input{
		Claim: cl, PolicyVersion: pv, IncidentTick: 1500,
		Evidence:        []evidence.Record{invoice, survey},
		QuantumDocTypes: []string{"invoice", "survey_report"},
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	f := factByID(t, a, "quantum_supported")
	if f.Status != StatusDisputed {
		t.Fatalf("expected StatusDisputed, got %s", f.Status)
	}
}

func TestQuantumSupported_InsufficientWhenNoDocTypesConfigured(t *testing.T) {
	cl := baseClaim("V1")
	pv := basePolicyVersion("V1")
	a, err := Analyze(Input{Claim: cl, PolicyVersion: pv, IncidentTick: 1500})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	f := factByID(t, a, "quantum_supported")
	if f.Status != StatusInsufficientEvidence {
		t.Fatalf("expected StatusInsufficientEvidence, got %s", f.Status)
	}
}

// ---- claim.TypeDefinition integration (RequiredEvidence, PolicyQuestions) ----

func TestRequiredEvidenceFacts_DerivedFromTypeDefinition(t *testing.T) {
	cl := baseClaim("V1")
	pv := basePolicyVersion("V1")
	td := claim.TypeDefinition{
		Type:             claim.TypeCargoDamage,
		RequiredEvidence: []string{"survey_report", "bill_of_lading", "photos"},
	}
	bol := mustRecord(t, "bol-1", "bill_of_lading", evidence.StatusAuthenticitySupported)
	a, err := Analyze(Input{
		Claim: cl, PolicyVersion: pv, IncidentTick: 1500,
		Evidence: []evidence.Record{bol},
		TypeDef:  &td,
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	got := factByID(t, a, "required_evidence:bill_of_lading")
	if got.Status != StatusSupported {
		t.Fatalf("expected StatusSupported for bill_of_lading, got %s", got.Status)
	}
	missing := factByID(t, a, "required_evidence:photos")
	if missing.Status != StatusInsufficientEvidence {
		t.Fatalf("expected StatusInsufficientEvidence for missing photos, got %s", missing.Status)
	}
}

func TestPolicyQuestions_SurfacedFromTypeDefinitionNeutrally(t *testing.T) {
	cl := baseClaim("V1")
	pv := basePolicyVersion("V1")
	td := claim.TypeDefinition{
		Type:             claim.TypeCargoDamage,
		RequiredEvidence: []string{"survey_report"},
		PolicyQuestions:  []string{"Was the cargo declared at its full insurable value at the time of loading?"},
	}
	a, err := Analyze(Input{Claim: cl, PolicyVersion: pv, IncidentTick: 1500, TypeDef: &td})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(a.Questions) == 0 {
		t.Fatal("expected at least one CoverageQuestion")
	}
	found := false
	for _, q := range a.Questions {
		if q.Question == td.PolicyQuestions[0] {
			found = true
			// Neutral phrasing: must not contain leading/loaded words.
			for _, loaded := range []string{"should be covered", "should be denied", "clearly covered", "clearly excluded"} {
				if strings.Contains(q.Question, loaded) {
					t.Fatalf("question is not neutrally phrased: %q", q.Question)
				}
			}
		}
	}
	if !found {
		t.Fatal("policy question from TypeDefinition was not surfaced as a CoverageQuestion")
	}
}

func TestAnalyzeOmitsTypeDefFactsWhenTypeDefNil(t *testing.T) {
	cl := baseClaim("V1")
	pv := basePolicyVersion("V1")
	a, err := Analyze(Input{Claim: cl, PolicyVersion: pv, IncidentTick: 1500})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	for _, f := range a.Facts {
		if len(f.FactID) >= len("required_evidence:") && f.FactID[:len("required_evidence:")] == "required_evidence:" {
			t.Fatalf("did not expect required_evidence facts with a nil TypeDef, got %s", f.FactID)
		}
	}
}

// ---- ReviewRequired ----

func TestReviewRequired_FalseWhenEverythingCleanlyResolved(t *testing.T) {
	cl := baseClaim("V1")
	pv := basePolicyVersion("V1")
	survey := mustRecord(t, "survey-6", "survey_report", evidence.StatusAuthenticitySupported)
	incident := mustRecord(t, "incident-2", "incident_report", evidence.StatusCorroborated)
	notice := mustRecord(t, "notice-1", "notice", evidence.StatusAuthenticitySupported)
	invoice := mustRecord(t, "invoice-4", "invoice", evidence.StatusAuthenticitySupported)
	a, err := Analyze(Input{
		Claim: cl, PolicyVersion: pv,
		IncidentTick: 1500, NoticeTick: 1550, NoticeDeadlineTick: 1600,
		Evidence:        []evidence.Record{survey, incident, notice, invoice},
		PerilDocTypes:   []string{"survey_report", "incident_report"},
		NoticeDocTypes:  []string{"notice"},
		QuantumDocTypes: []string{"invoice"},
		// no ExclusionClauseIDs, no TypeDef -> no contested/insufficient facts
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	for _, f := range a.Facts {
		if f.Status != StatusSupported {
			t.Fatalf("expected every fact SUPPORTED in this clean scenario, got %s = %s", f.FactID, f.Status)
		}
	}
	if len(a.Conflicts) != 0 {
		t.Fatalf("expected no conflicts, got %v", a.Conflicts)
	}
	if len(a.Questions) != 0 {
		t.Fatalf("expected no questions, got %v", a.Questions)
	}
	if a.ReviewRequired {
		t.Fatalf("expected ReviewRequired=false, got true with reasons %v", a.ReviewReasons)
	}
}

func TestReviewRequired_TrueWhenAnyFactUnresolved(t *testing.T) {
	cl := baseClaim("V1")
	pv := basePolicyVersion("V1")
	a, err := Analyze(Input{Claim: cl, PolicyVersion: pv}) // IncidentTick unknown -> insufficient
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !a.ReviewRequired {
		t.Fatal("expected ReviewRequired=true")
	}
	if len(a.ReviewReasons) == 0 {
		t.Fatal("expected at least one review reason")
	}
}

// ---- identity / traceability ----

func TestAnalyzeResultCarriesCaseClaimPolicyIdentity(t *testing.T) {
	cl := baseClaim("V1")
	pv := basePolicyVersion("V1")
	a, err := Analyze(Input{Claim: cl, PolicyVersion: pv, IncidentTick: 1500})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if a.CaseID != cl.CaseID || a.ClaimID != cl.ClaimID {
		t.Fatalf("expected CaseID/ClaimID to match the input claim, got %+v", a)
	}
	if a.PolicyID != pv.PolicyID || a.PolicyVersionID != pv.VersionID {
		t.Fatalf("expected PolicyID/PolicyVersionID to match the input version, got %+v", a)
	}
	for _, f := range a.Facts {
		if f.PolicyVersionID != pv.VersionID {
			t.Fatalf("fact %s not tied to PolicyVersionID %s: got %s", f.FactID, pv.VersionID, f.PolicyVersionID)
		}
	}
}

// ---- policy.History.EffectiveAt tie-in ----
//
// Blueprint §6 is P0: which policy version was effective at the time
// of the incident must never default to "the latest version". These
// tests prove Analyze is wired to consume an EffectiveAt-resolved
// Version, not to re-derive or ignore that resolution.

func buildTwoVersionHistory(t *testing.T) *policy.History {
	t.Helper()
	h, err := policy.NewHistory("POL-1")
	if err != nil {
		t.Fatalf("NewHistory: %v", err)
	}
	original := policy.Version{
		PolicyID: "POL-1", VersionID: "POL-1-ORIGINAL", PolicyNumber: "PN-1",
		Insurer: "Ins Co", Insured: "Insured Co",
		EffectiveFrom: 1000, EffectiveTo: 5000, Kind: policy.KindOriginal,
		Clauses: []policy.Clause{{ClauseID: "§8", DocumentID: "DOC-1", SourceHash: "h", TextSpan: "original exclusions", Version: "V1"}},
	}
	if err := h.Add(original); err != nil {
		t.Fatalf("Add original: %v", err)
	}
	endorsement := policy.Version{
		PolicyID: "POL-1", VersionID: "POL-1-ENDORSEMENT", PolicyNumber: "PN-1",
		Insurer: "Ins Co", Insured: "Insured Co",
		EffectiveFrom: 2000, EffectiveTo: 0, Kind: policy.KindEndorsement, Supersedes: "POL-1-ORIGINAL",
		Clauses: []policy.Clause{{ClauseID: "§8", DocumentID: "DOC-2", SourceHash: "h2", TextSpan: "endorsed exclusions", Version: "V2"}},
	}
	if err := h.Add(endorsement); err != nil {
		t.Fatalf("Add endorsement: %v", err)
	}
	return h
}

func TestAnalyzeUsesEffectiveAtResolvedVersion_NotLatest(t *testing.T) {
	h := buildTwoVersionHistory(t)
	// Incident occurs at tick 1500: only the ORIGINAL version's window
	// covers it (endorsement starts at 2000). EffectiveAt must resolve
	// to the original, even though the endorsement is "newer".
	resolved, err := h.EffectiveAt(1500)
	if err != nil {
		t.Fatalf("EffectiveAt: %v", err)
	}
	if resolved.VersionID != "POL-1-ORIGINAL" {
		t.Fatalf("expected EffectiveAt(1500) to resolve to the original version, got %s", resolved.VersionID)
	}

	cl := claim.Claim{
		ClaimID: "CLM-1", CaseID: "CASE-1", Type: claim.TypeCargoDamage,
		ClaimantPartyID: "PTY-1", Status: claim.StatusRegistered,
		PolicyVersionID: resolved.VersionID,
	}
	a, err := Analyze(Input{Claim: cl, PolicyVersion: resolved, IncidentTick: 1500})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if a.PolicyVersionID != "POL-1-ORIGINAL" {
		t.Fatalf("expected CoverageAnalysis tied to the original version, got %s", a.PolicyVersionID)
	}
}

// TestAnalyzeRefusesLatestVersionWhenClaimPinsAnEarlierOne proves the
// mismatch guard actually bites: even though "the latest version"
// (the endorsement) is a real, valid policy.Version, Analyze refuses
// to use it in place of the version the claim was actually resolved
// against — this is what makes "never default to the latest policy"
// structural rather than a matter of caller discipline.
func TestAnalyzeRefusesLatestVersionWhenClaimPinsAnEarlierOne(t *testing.T) {
	h := buildTwoVersionHistory(t)
	resolved, err := h.EffectiveAt(1500) // -> POL-1-ORIGINAL
	if err != nil {
		t.Fatalf("EffectiveAt: %v", err)
	}
	latest, ok := h.Latest() // -> POL-1-ENDORSEMENT
	if !ok {
		t.Fatal("expected a latest version")
	}
	if latest.VersionID == resolved.VersionID {
		t.Fatal("test setup invalid: latest and resolved must differ")
	}

	cl := claim.Claim{
		ClaimID: "CLM-1", CaseID: "CASE-1", Type: claim.TypeCargoDamage,
		ClaimantPartyID: "PTY-1", Status: claim.StatusRegistered,
		PolicyVersionID: resolved.VersionID, // pinned to the ORIGINAL
	}
	_, err = Analyze(Input{Claim: cl, PolicyVersion: latest, IncidentTick: 1500})
	if !errors.Is(err, ErrPolicyVersionMismatch) {
		t.Fatalf("expected ErrPolicyVersionMismatch when passing the latest version instead of the pinned/effective one, got %v", err)
	}
}
