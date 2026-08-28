package cre

import (
	"testing"

	"veriqo/pkg/insurance/causation"
	"veriqo/pkg/insurance/evidence"
	"veriqo/pkg/insurance/finding"
)

func buildHypothesisSet(t *testing.T) *causation.HypothesisSet {
	t.Helper()
	hs, err := causation.NewHypothesisSet("case-1", "claim-1", "What caused the loss?")
	if err != nil {
		t.Fatal(err)
	}
	if err := hs.Add(causation.Hypothesis{ID: "H1", Description: "Water ingress during heavy weather."}); err != nil {
		t.Fatal(err)
	}
	if err := hs.Add(causation.Hypothesis{ID: "H2", Description: "Pre-existing damage before loading."}); err != nil {
		t.Fatal(err)
	}
	if err := hs.Add(causation.Hypothesis{ID: "H3", Description: "Cargo mishandling during discharge."}); err != nil {
		t.Fatal(err)
	}
	if err := hs.AddSupportingEvidence("H1", "ev-survey-1"); err != nil {
		t.Fatal(err)
	}
	if err := hs.AddSupportingEvidence("H1", "ev-weather-log-1"); err != nil {
		t.Fatal(err)
	}
	if err := hs.AddContradictingEvidence("H2", "ev-survey-1"); err != nil {
		t.Fatal(err)
	}
	// H3 gets no evidence at all -- stays UNPROVEN, deliberately.
	return hs
}

func TestCandidateHypothesesExcludesUnsupportedAndContradicted(t *testing.T) {
	hs := buildHypothesisSet(t)
	candidates := CandidateHypotheses(hs)
	if len(candidates) != 1 {
		t.Fatalf("expected exactly 1 candidate (H1), got %d: %v", len(candidates), candidates)
	}
	if candidates[0].ID != "H1" {
		t.Fatalf("expected H1, got %s", candidates[0].ID)
	}
}

func TestCandidateHypothesesOnEmptySetIsEmptyNotError(t *testing.T) {
	hs, err := causation.NewHypothesisSet("case-1", "claim-1", "Unresolved question")
	if err != nil {
		t.Fatal(err)
	}
	if got := CandidateHypotheses(hs); len(got) != 0 {
		t.Fatalf("expected zero candidates for an empty hypothesis set, got %d", len(got))
	}
}

func TestBuildFindingProducesAFullFindingFromRealHypothesisData(t *testing.T) {
	hs := buildHypothesisSet(t)
	candidates := CandidateHypotheses(hs)
	dg := evidence.NewDependencyGraph()

	f, err := BuildFinding(hs, candidates[0], dg, FindingInput{
		CaseID: "case-1", ContractBasis: "clause-9.3", ObligationRef: "obl-1",
		EventRef: "event-1", QuantumRef: "calc-1", HumanReviewRequired: true,
	}, "finding-h1", 100)
	if err != nil {
		t.Fatalf("BuildFinding: %v", err)
	}
	if f.Status != finding.StatusFinding {
		t.Fatalf("expected StatusFinding, got %s (missing=%v)", f.Status, finding.MissingFields(f))
	}
	if len(f.SupportedBy) != 2 {
		t.Fatalf("expected SupportedBy to carry H1's own 2 supporting evidence IDs, got %v", f.SupportedBy)
	}
	if f.ConfidenceBasis != causation.StatusSupported && f.ConfidenceBasis != causation.StatusPartiallySupported {
		t.Fatalf("expected ConfidenceBasis to be a supported-family status, got %s", f.ConfidenceBasis)
	}
	// Alternatives must be every OTHER hypothesis, mechanically -- never
	// hand-picked.
	wantAlts := map[string]bool{"H2": true, "H3": true}
	if len(f.Alternatives) != 2 {
		t.Fatalf("expected 2 alternatives (H2, H3), got %v", f.Alternatives)
	}
	for _, a := range f.Alternatives {
		if !wantAlts[a] {
			t.Fatalf("unexpected alternative %q", a)
		}
	}
	if f.Causation == "" {
		t.Fatal("expected a non-empty Causation narrative from causation.Explain")
	}
	if err := finding.VerifyFindingHash(f); err != nil {
		t.Fatalf("VerifyFindingHash: %v", err)
	}
}

func TestBuildFindingRejectsHypothesisWithUnknownStatus(t *testing.T) {
	hs := buildHypothesisSet(t)
	dg := evidence.NewDependencyGraph()
	bogus := causation.Hypothesis{ID: "H99", Description: "not really in the set", Status: "NOT_A_REAL_STATUS"}
	if _, err := BuildFinding(hs, bogus, dg, FindingInput{CaseID: "case-1"}, "f-bogus", 1); err == nil {
		t.Fatal("expected an error for a hypothesis with an unknown status")
	}
}

func TestGenerateFindingsProducesOneFindingPerCandidate(t *testing.T) {
	hs := buildHypothesisSet(t)
	dg := evidence.NewDependencyGraph()
	findings, err := GenerateFindings(hs, dg, FindingInput{
		CaseID: "case-1", ContractBasis: "clause-9.3", ObligationRef: "obl-1",
		EventRef: "event-1", QuantumRef: "calc-1", HumanReviewRequired: true,
	}, "case-1-finding", 100)
	if err != nil {
		t.Fatalf("GenerateFindings: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected exactly 1 finding (only H1 qualifies), got %d", len(findings))
	}
	if findings[0].FindingID != "case-1-finding-H1" {
		t.Fatalf("expected a deterministic FindingID derived from the hypothesis ID, got %s", findings[0].FindingID)
	}
}

func TestGenerateFindingsOnNoQualifyingHypothesisReturnsEmptyNotError(t *testing.T) {
	hs, err := causation.NewHypothesisSet("case-2", "claim-2", "What caused the loss?")
	if err != nil {
		t.Fatal(err)
	}
	if err := hs.Add(causation.Hypothesis{ID: "H1", Description: "unproven theory"}); err != nil {
		t.Fatal(err)
	}
	dg := evidence.NewDependencyGraph()
	findings, err := GenerateFindings(hs, dg, FindingInput{CaseID: "case-2"}, "case-2-finding", 1)
	if err != nil {
		t.Fatalf("expected no error for a genuinely unresolved case, got %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected zero findings when nothing is supported, got %d", len(findings))
	}
}

func TestGenerateFindingsIsDeterministic(t *testing.T) {
	hs := buildHypothesisSet(t)
	dg := evidence.NewDependencyGraph()
	in := FindingInput{CaseID: "case-1", ContractBasis: "clause-9.3", ObligationRef: "obl-1",
		EventRef: "event-1", QuantumRef: "calc-1", HumanReviewRequired: true}
	f1, err := GenerateFindings(hs, dg, in, "prefix", 100)
	if err != nil {
		t.Fatal(err)
	}
	f2, err := GenerateFindings(hs, dg, in, "prefix", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(f1) != len(f2) || f1[0].Hash != f2[0].Hash {
		t.Fatal("expected identical inputs to produce identical Finding hashes")
	}
}
