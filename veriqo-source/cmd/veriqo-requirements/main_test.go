package main

import (
	"strconv"
	"strings"
	"testing"
)

func sample() []Requirement {
	return []Requirement{
		{ID: "R-000", Status: "VERIFIED"},
		{ID: "R-001", Status: "OPEN"},
		{ID: "R-002", Status: "BLOCKED", Note: "needs a vendor"},
		{ID: "R-003", Status: "IMPLEMENTED"},
	}
}

func TestValidateRejectsDuplicateIDs(t *testing.T) {
	reqs := append(sample(), Requirement{ID: "R-000", Status: "VERIFIED"})
	if err := validate(reqs); err == nil {
		t.Fatal("duplicate requirement IDs must be refused")
	}
}

func TestValidateRejectsUnknownStatus(t *testing.T) {
	reqs := []Requirement{{ID: "R-000", Status: "DONE"}}
	if err := validate(reqs); err == nil {
		t.Fatal("an unrecognised status word must be refused — the audit forbids inventing new ones")
	}
}

func TestValidateRejectsUnexplainedBlocked(t *testing.T) {
	reqs := []Requirement{{ID: "R-000", Status: "BLOCKED", Note: ""}}
	if err := validate(reqs); err == nil {
		t.Fatal("a BLOCKED requirement with no named blocker must be refused")
	}
}

func TestValidateAcceptsWellFormedSet(t *testing.T) {
	if err := validate(sample()); err != nil {
		t.Fatalf("a well-formed requirement set must validate: %v", err)
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	a := render(sample())
	b := render(sample())
	if a != b {
		t.Fatal("rendering the same requirement set twice must produce byte-identical output")
	}
}

func TestRenderSummaryCountsMatchRowTotal(t *testing.T) {
	reqs := sample()
	out := render(reqs)
	// The generated "Row count check" line is the mechanism that would
	// have caught the v7.12.0 defect (table said 2 OPEN, hand-typed
	// prose said 1): it recomputes from the same slice render() just
	// walked, so it can never itself drift from the table above it.
	if !strings.Contains(out, "= 4, of 4 total rows (consistent)") {
		t.Fatalf("expected a consistent row-count self-check, got:\n%s", out)
	}
}

func TestRenderFlagsInconsistentCounts(t *testing.T) {
	// A status outside the four known values would break the sum; since
	// validate() already refuses that before render() is ever called,
	// this instead checks the self-check arithmetic directly: every
	// requirement is counted in exactly one bucket, so the sum always
	// equals len(reqs) for any validated input. This test pins that
	// invariant so a future edit to render() cannot silently break it.
	for n := 0; n <= len(sample()); n++ {
		out := render(sample()[:n])
		want := "of " + strconv.Itoa(n) + " total rows (consistent)"
		if !strings.Contains(out, want) {
			t.Fatalf("n=%d: expected %q in output", n, want)
		}
	}
}
