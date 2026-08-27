package coverage

import (
	"strings"
	"testing"
)

// TestLateNoticeNeverSetsACoverageOutcome is the coverage-engine half of
// the "LATE NOTICE ≠ COVERAGE DENIED" rule that pkg/insurance/obligation
// enforces on the notice side.
//
// Before this round the separation held only BY ABSENCE: this package
// has no denial field, so late notice could not set one. That was true
// but unasserted, and an unasserted invariant is one a future field can
// break silently. This test asserts it directly — a late notice must
// produce a DISPUTED fact and a review requirement, and must leave
// every other coverage fact exactly as it was.
func TestLateNoticeNeverSetsACoverageOutcome(t *testing.T) {
	pv := basePolicyVersion("V1")
	base := Input{
		Claim:           baseClaim("V1"),
		PolicyVersion:   pv,
		IncidentTick:    2000,
		PerilDocTypes:   []string{"survey_report"},
		NoticeDocTypes:  []string{"notice_letter"},
		QuantumDocTypes: []string{"invoice"},
		PeriodClauseID:  "§2",
		PerilClauseID:   "§4",
		NoticeClauseID:  "§11",
		QuantumClauseID: "§14",
	}

	timely := base
	timely.NoticeTick = 2010
	timely.NoticeDeadlineTick = 2100

	late := base
	late.NoticeTick = 2500 // well past the deadline
	late.NoticeDeadlineTick = 2100

	timelyAnalysis, err := Analyze(timely)
	if err != nil {
		t.Fatalf("Analyze(timely): %v", err)
	}
	lateAnalysis, err := Analyze(late)
	if err != nil {
		t.Fatalf("Analyze(late): %v", err)
	}

	// 1. The notice fact itself moves to DISPUTED — a flag for review,
	//    not a conclusion.
	lateNotice := factByID(t, lateAnalysis, "notice_timely")
	if lateNotice.Status != StatusDisputed {
		t.Fatalf("late notice produced status %q, want DISPUTED", lateNotice.Status)
	}
	if !lateAnalysis.ReviewRequired {
		t.Fatal("a late notice must set ReviewRequired")
	}

	// 2. NOTHING else changes. Late notice does not degrade the peril
	//    fact, the policy-period fact, the quantum fact, or any required
	//    evidence fact — because those are different questions.
	timelyByID := map[string]FactStatus{}
	for _, f := range timelyAnalysis.Facts {
		timelyByID[f.FactID] = f.Status
	}
	for _, f := range lateAnalysis.Facts {
		if f.FactID == "notice_timely" {
			continue
		}
		if was, ok := timelyByID[f.FactID]; ok && was != f.Status {
			t.Fatalf("late notice changed unrelated fact %q from %q to %q — notice timeliness is a "+
				"separate question from every other coverage question", f.FactID, was, f.Status)
		}
	}

	// 3. No output anywhere reads as a coverage outcome.
	assertNoCoverageOutcomeLanguage(t, lateAnalysis)
}

// TestNoticeBeforeIncidentIsAlsoOnlyDisputed: the other notice-shaped
// temporal anomaly is likewise a flag, never a finding.
func TestNoticeBeforeIncidentIsAlsoOnlyDisputed(t *testing.T) {
	in := Input{
		Claim:          baseClaim("V1"),
		PolicyVersion:  basePolicyVersion("V1"),
		IncidentTick:   2000,
		NoticeTick:     1900, // before the incident it purports to notify
		NoticeClauseID: "§11",
	}
	a, err := Analyze(in)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	f := factByID(t, a, "notice_timely")
	if f.Status != StatusDisputed {
		t.Fatalf("status = %q, want DISPUTED", f.Status)
	}
	assertNoCoverageOutcomeLanguage(t, a)
}

// TestUnknownNoticeTimeIsInsufficientNotLate: an unknown notice time is
// an evidence gap, never a late notice.
func TestUnknownNoticeTimeIsInsufficientNotLate(t *testing.T) {
	in := Input{
		Claim:              baseClaim("V1"),
		PolicyVersion:      basePolicyVersion("V1"),
		IncidentTick:       2000,
		NoticeTick:         0,
		NoticeDeadlineTick: 2100,
		NoticeClauseID:     "§11",
	}
	a, err := Analyze(in)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	f := factByID(t, a, "notice_timely")
	if f.Status != StatusInsufficientEvidence {
		t.Fatalf("an unknown notice time produced %q, want INSUFFICIENT_EVIDENCE", f.Status)
	}
}

// TestNoDeadlineRuleIsPartialNotSupported: notice given with no
// deadline to measure against cannot be called timely.
func TestNoDeadlineRuleIsPartialNotSupported(t *testing.T) {
	in := Input{
		Claim:              baseClaim("V1"),
		PolicyVersion:      basePolicyVersion("V1"),
		IncidentTick:       2000,
		NoticeTick:         2010,
		NoticeDeadlineTick: 0,
		NoticeClauseID:     "§11",
	}
	a, err := Analyze(in)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	f := factByID(t, a, "notice_timely")
	if f.Status != StatusPartial {
		t.Fatalf("notice with no deadline rule produced %q, want PARTIAL", f.Status)
	}
}

func assertNoCoverageOutcomeLanguage(t *testing.T, a CoverageAnalysis) {
	t.Helper()
	var b strings.Builder
	for _, f := range a.Facts {
		b.WriteString(" " + f.Description + " " + f.Notes)
	}
	for _, q := range a.Questions {
		b.WriteString(" " + q.Question + " " + q.Reason)
	}
	for _, c := range a.Conflicts {
		b.WriteString(" " + c.Description)
	}
	for _, r := range a.ReviewReasons {
		b.WriteString(" " + r)
	}
	lower := strings.ToLower(b.String())
	for _, bad := range []string{
		"coverage is denied", "coverage denied", "claim is denied", "claim denied",
		"not covered", "cover is lost", "coverage forfeited", "claim fails",
	} {
		if strings.Contains(lower, bad) {
			t.Fatalf("coverage analysis output contains outcome language %q", bad)
		}
	}
}
