package deadline

import (
	"testing"

	"veriqo/pkg/insurance/policy"
)

func TestTracesToClause_RealMatch(t *testing.T) {
	clause := policy.Clause{
		ClauseID:      "CL-NOTICE-11",
		DocumentID:    "POLICY-DOC-2024-001",
		SourceHash:    "deadbeef",
		TextSpan:      "Notice of claim must be given within 90 days of discovery.",
		Version:       "V2",
		EffectiveDate: 1000,
	}
	r, err := New("DL-1", SourceTypePolicy, clause.ClauseID, clause.DocumentID, clause.Version, "DAMAGE_DISCOVERY", 90, CalendarRuleCalendarDays, "Asia/Jakarta")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.TracesToClause(clause) {
		t.Fatalf("expected rule to trace to its real source clause")
	}
}

func TestTracesToClause_MismatchedClauseIDFails(t *testing.T) {
	clause := policy.Clause{ClauseID: "CL-1", DocumentID: "DOC-1", Version: "V1"}
	r, err := New("DL-1", SourceTypePolicy, "CL-DIFFERENT", "DOC-1", "V1", "NOTICE", 30, CalendarRuleCalendarDays, "UTC")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.TracesToClause(clause) {
		t.Fatalf("expected a rule referencing a different ClauseID not to trace")
	}
}

func TestTracesToClause_MismatchedVersionFails(t *testing.T) {
	// Same document and clause ID, but a different policy version —
	// this must NOT be treated as tracing to the clause, because the
	// blueprint's own P0 (§6) is that the EFFECTIVE VERSION matters,
	// not just "some version of this clause existed".
	clause := policy.Clause{ClauseID: "CL-1", DocumentID: "DOC-1", Version: "V2"}
	r, err := New("DL-1", SourceTypePolicy, "CL-1", "DOC-1", "V1", "NOTICE", 30, CalendarRuleCalendarDays, "UTC")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.TracesToClause(clause) {
		t.Fatalf("expected a version mismatch to fail tracing")
	}
}

func TestTracesToClause_NonPolicySourceNeverTraces(t *testing.T) {
	// A B/L-sourced rule can never trace to a policy.Clause, even if
	// the identifiers happen to coincide — SourceType itself must
	// declare POLICY.
	clause := policy.Clause{ClauseID: "CL-1", DocumentID: "DOC-1", Version: "V1"}
	r, err := New("DL-1", SourceTypeBillOfLading, "CL-1", "DOC-1", "V1", "NOTICE", 30, CalendarRuleCalendarDays, "UTC")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.TracesToClause(clause) {
		t.Fatalf("expected a non-policy SourceType to never trace to a policy.Clause")
	}
}
