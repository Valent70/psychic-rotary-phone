package main

import (
	"testing"

	"veriqo/pkg/insurance/casepack"
)

func TestReplayPassesForEverySyntheticCase(t *testing.T) {
	for _, id := range []string{
		"CASE-INS-001", "CASE-INS-002", "CASE-INS-003", "CASE-INS-004",
		"CASE-INS-005", "CASE-INS-006", "CASE-INS-007",
	} {
		id := id
		t.Run(id, func(t *testing.T) {
			r, err := Replay(id)
			if err != nil {
				t.Fatalf("Replay(%s): %v", id, err)
			}
			if !r.Pass() {
				t.Fatalf("Replay(%s) did not pass: axes=%+v evidence=%v policy=%v rule=%v auth=%v",
					id, r.Axes, r.EvidenceDifferences, r.PolicyDifferences, r.RuleDifferences, r.AuthorizationDifferences)
			}
			if r.SnapshotID == "" {
				t.Fatal("expected a non-empty snapshot ID")
			}
		})
	}
}

func TestReplayPassesForGoldenCase(t *testing.T) {
	r, err := Replay("golden")
	if err != nil {
		t.Fatalf("Replay(golden): %v", err)
	}
	if !r.Pass() {
		t.Fatalf("Replay(golden) did not pass: %+v", r)
	}
}

func TestReplayRejectsAnUnknownCase(t *testing.T) {
	if _, err := Replay("NOT-A-CASE"); err == nil {
		t.Fatal("expected an error for an unknown case ID")
	}
}

// TestDiffEvidenceDetectsAMissingKey proves the evidence diff actually
// walks both sides rather than trivially returning empty.
func TestDiffEvidenceDetectsAMissingKey(t *testing.T) {
	c, err := casepack.Get(casepack.CasePortCallDemurrage)
	if err != nil {
		t.Fatalf("casepack.Get: %v", err)
	}
	built, err := c.BuildAllEvidence()
	if err != nil {
		t.Fatalf("BuildAllEvidence: %v", err)
	}
	if len(built.ByKey) == 0 {
		t.Fatal("expected at least one evidence record")
	}

	diffs := diffEvidence(built, casepack.BuiltEvidence{})
	if len(diffs) == 0 {
		t.Fatal("expected diffEvidence to report every key as missing from the empty side")
	}
	if len(diffs) != len(built.ByKey) {
		t.Fatalf("expected %d diffs (one per key), got %d: %v", len(built.ByKey), len(diffs), diffs)
	}
}

// TestDiffAuthorizationByReviewGateDetectsADivergedFlag proves the
// base-case authorization diff is a real field comparison.
func TestDiffAuthorizationByReviewGateDetectsADivergedFlag(t *testing.T) {
	c, err := casepack.Get(casepack.CasePortCallDemurrage)
	if err != nil {
		t.Fatalf("casepack.Get: %v", err)
	}
	run, err := casepack.Drive(c, nil)
	if err != nil {
		t.Fatalf("casepack.Drive: %v", err)
	}
	if run.Dossier == nil {
		t.Fatal("expected a dossier")
	}
	flipped := *run.Dossier
	flipped.HumanReviewRequired = !run.Dossier.HumanReviewRequired
	diffs := diffAuthorizationByReviewGate(run.Dossier, &flipped)
	if len(diffs) == 0 {
		t.Fatal("expected a diff when HumanReviewRequired is flipped")
	}
}
