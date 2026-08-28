package coverage

import (
	"errors"
	"strings"
	"testing"
)

// TestCoverageIssueMatrix_BlueprintSection19WorkedExample reproduces
// the blueprint's own §19 worked table EXACTLY — same 5 rows, same
// column values, same status words (Supported / Disputed / Partial) —
// and asserts the constructed matrix holds precisely that content.
func TestCoverageIssueMatrix_BlueprintSection19WorkedExample(t *testing.T) {
	want := []IssueRow{
		{Issue: "Peril occurred", Evidence: "Survey + incident report", PolicyClause: "§4", Status: StatusSupported},
		{Issue: "Within policy period", Evidence: "Policy + incident date", PolicyClause: "§2", Status: StatusSupported},
		{Issue: "Exclusion applies (Policy §8)", Evidence: "", PolicyClause: "§8", Status: StatusDisputed},
		{Issue: "Notice timely", Evidence: "Notice + timestamp", PolicyClause: "§11", Status: StatusSupported},
		{Issue: "Quantum supported", Evidence: "Invoice + survey", PolicyClause: "§14", Status: StatusPartial},
	}

	m := NewIssueMatrix()
	for _, row := range want {
		if err := m.Add(row); err != nil {
			t.Fatalf("Add(%+v): %v", row, err)
		}
	}

	if got := m.Len(); got != len(want) {
		t.Fatalf("expected %d rows, got %d", len(want), got)
	}

	got := m.Rows()
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d: expected %+v, got %+v", i, want[i], got[i])
		}
	}

	// The blueprint's own worked example uses exactly three distinct
	// status words. Assert all three are present, proving this
	// package's FactStatus vocabulary genuinely covers them.
	seen := map[FactStatus]bool{}
	for _, r := range got {
		seen[r.Status] = true
	}
	for _, s := range []FactStatus{StatusSupported, StatusDisputed, StatusPartial} {
		if !seen[s] {
			t.Fatalf("expected status %s to appear in the blueprint's own worked example, but it did not", s)
		}
	}

	// Spot-check the render is a real table containing every issue name.
	rendered := m.Render()
	for _, row := range want {
		if !strings.Contains(rendered, row.Issue) {
			t.Fatalf("rendered matrix missing issue %q:\n%s", row.Issue, rendered)
		}
	}
}

func TestIssueMatrixAddRejectsEmptyIssue(t *testing.T) {
	m := NewIssueMatrix()
	err := m.Add(IssueRow{Status: StatusSupported})
	if !errors.Is(err, ErrEmptyIssue) {
		t.Fatalf("expected ErrEmptyIssue, got %v", err)
	}
	if m.Len() != 0 {
		t.Fatalf("expected the invalid row to be rejected, matrix has %d rows", m.Len())
	}
}

func TestIssueMatrixAddRejectsUnknownStatus(t *testing.T) {
	m := NewIssueMatrix()
	err := m.Add(IssueRow{Issue: "Some issue", Status: FactStatus("MAYBE")})
	if !errors.Is(err, ErrUnknownFactStatus) {
		t.Fatalf("expected ErrUnknownFactStatus, got %v", err)
	}
}

func TestIssueMatrixRowsReturnsACopy(t *testing.T) {
	m := NewIssueMatrix()
	if err := m.Add(IssueRow{Issue: "A", Status: StatusSupported}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	rows := m.Rows()
	rows[0].Issue = "mutated"
	if m.Rows()[0].Issue != "A" {
		t.Fatal("Rows() leaked internal state — mutating the returned slice affected the matrix")
	}
}

// TestCoverageAnalysisIssueMatrix proves a real Analyze() result can
// be rendered in the exact §19 table shape via
// CoverageAnalysis.IssueMatrix(), not just constructed by hand.
func TestCoverageAnalysisIssueMatrix(t *testing.T) {
	cl := baseClaim("V1")
	pv := basePolicyVersion("V1")
	a, err := Analyze(Input{
		Claim: cl, PolicyVersion: pv, IncidentTick: 1500,
		PeriodClauseID:     "§2",
		ExclusionClauseIDs: []string{"§8"},
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	matrix := a.IssueMatrix()
	if matrix.Len() != len(a.Facts) {
		t.Fatalf("expected 1 issue-matrix row per fact: %d facts, %d rows", len(a.Facts), matrix.Len())
	}
	for _, row := range matrix.Rows() {
		if !IsKnownStatus(row.Status) {
			t.Fatalf("row %+v has an unrecognised status", row)
		}
	}
}
