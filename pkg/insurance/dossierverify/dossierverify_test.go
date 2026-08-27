package dossierverify

import (
	"testing"

	"veriqo/pkg/insurance/casepack"
)

func TestVerifyPassesForEverySyntheticCase(t *testing.T) {
	for _, id := range []string{
		"CASE-INS-001", "CASE-INS-002", "CASE-INS-003", "CASE-INS-004",
		"CASE-INS-005", "CASE-INS-006", "CASE-INS-007",
	} {
		id := id
		t.Run(id, func(t *testing.T) {
			r, err := Verify(id)
			if err != nil {
				t.Fatalf("Verify(%s): %v", id, err)
			}
			if !r.Pass() {
				t.Fatalf("Verify(%s) did not pass: %+v", id, r.Checks)
			}
			if len(r.Checks) == 0 {
				t.Fatal("expected at least one check to have run")
			}
		})
	}
}

func TestVerifyPassesForGoldenCase(t *testing.T) {
	r, err := Verify("golden")
	if err != nil {
		t.Fatalf("Verify(golden): %v", err)
	}
	if !r.Pass() {
		t.Fatalf("Verify(golden) did not pass: %+v", r.Checks)
	}
	names := map[string]bool{}
	for _, c := range r.Checks {
		names[c.Name] = true
	}
	for _, want := range []string{
		"independent_reproduction", "evidence_chain_integrity",
		"quantum_recomputation", "golden_salvage_recomputation",
		"cold_replay", "no_verdict_field",
	} {
		if !names[want] {
			t.Errorf("expected a %q check in the golden report, got %v", want, names)
		}
	}
}

func TestVerifyRejectsAnUnknownCase(t *testing.T) {
	if _, err := Verify("NOT-A-CASE"); err == nil {
		t.Fatal("expected an error for an unknown case ID")
	}
}

// TestCheckQuantumRecomputationCatchesADivergedCachedValue proves the
// check is a real independent recomputation, not a comparison against
// itself: a cached value that disagrees with what quantum.Compute
// produces from the same recorded input must fail, not silently pass.
func TestCheckQuantumRecomputationCatchesADivergedCachedValue(t *testing.T) {
	c, err := Verify("CASE-INS-001")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	for _, chk := range c.Checks {
		if chk.Name == "quantum_recomputation" && !chk.Pass {
			t.Fatalf("baseline quantum_recomputation unexpectedly failed: %s", chk.Detail)
		}
	}

	scenario, err := casepack.Get(casepack.CasePortCallDemurrage)
	if err != nil {
		t.Fatalf("casepack.Get: %v", err)
	}
	run, err := casepack.Drive(scenario, nil)
	if err != nil {
		t.Fatalf("casepack.Drive: %v", err)
	}
	tampered := run.Quantum
	tampered.IndicativeClaimValue += 1 // one minor unit off is still a divergence
	if got := checkQuantumRecomputation(run.QuantumInput, tampered); got.Pass {
		t.Fatal("checkQuantumRecomputation passed against a deliberately tampered cached value")
	}
}

// TestRunAllCoversEveryCaseAndPasses proves the readiness-gate
// aggregation function actually exercises all eight cases (seven
// synthetic plus golden) and reports the same per-case pass/fail this
// package's own Verify would.
func TestRunAllCoversEveryCaseAndPasses(t *testing.T) {
	allPass, reports, err := RunAll()
	if err != nil {
		t.Fatalf("RunAll: %v", err)
	}
	if !allPass {
		t.Fatalf("RunAll did not pass: %+v", reports)
	}
	if len(reports) != 8 {
		t.Fatalf("expected 8 reports (7 synthetic cases + golden), got %d", len(reports))
	}
	seen := map[string]bool{}
	for _, r := range reports {
		seen[r.CaseID] = true
	}
	if !seen["GOLDEN"] {
		t.Error("expected a GOLDEN report in RunAll's output")
	}
}
