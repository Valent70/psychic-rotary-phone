package rwc

import (
	"context"
	"testing"

	"veriqo/veriqo/kernel"
)

// TestRWC001DeterministicReplay is the brief's §12 P0 requirement: run
// RWC-001 candidate A once, record it, replay it independently, and
// assert byte-identical certificate/result hashes. This calls no
// RWC-specific replay logic of its own — VerifyReplay (replay.go) is a
// thin wrapper around the exact pkg/replay.Engine every other VERIQO
// caller uses.
func TestRWC001DeterministicReplay(t *testing.T) {
	k, err := kernel.New()
	if err != nil {
		t.Fatalf("kernel.New: %v", err)
	}
	defer k.Shutdown()

	req, _, err := BuildRWC001Case(RWC001Candidates()[0], 1) // candidate A
	if err != nil {
		t.Fatalf("BuildRWC001Case: %v", err)
	}
	res, err := Run(context.Background(), k, req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	cert, err := VerifyReplay(k, "rwc-v2-test-auditor", res)
	if err != nil {
		t.Fatalf("VerifyReplay: %v", err)
	}
	if err := cert.Assert(); err != nil {
		t.Fatalf("REPLAY FAIL for %s: %v (original=%s replay=%s diverged_stage=%s)",
			res.CaseID, err, cert.OriginalResultHash, cert.ReplayResultHash, cert.DivergedStage)
	}
	t.Logf("REPLAY PASS for %s: original=%s replay=%s stages_compared=%d",
		res.CaseID, cert.OriginalResultHash, cert.ReplayResultHash, cert.StagesCompared)
}

// TestRWC001MutationChangesDecisionOnlyWhenExpected is the brief's §13
// adversarial-replay requirement: mutate ONE input field at a time
// (Akonikien max LOA 150->149, vessel LOA 140->151, draft 7.2->8.4,
// geared true->false) and assert the verdict changes ONLY for the
// mutated dimension, using the exact fixed baseline vessel/port pair
// (candidate A vs Akonikien) as the control.
func TestRWC001MutationChangesDecisionOnlyWhenExpected(t *testing.T) {
	baseline := RWC001BaselineVessel // LOA 140, draft 7.2, geared=true (candidate A's spec)
	akonikien := Ports["AKONIKIEN"]

	run := func(t *testing.T, v Vessel, p Port) Verdict {
		t.Helper()
		k, err := kernel.New()
		if err != nil {
			t.Fatalf("kernel.New: %v", err)
		}
		defer k.Shutdown()

		req, cr, err := BuildVesselPortCase("MUTATION-TEST", v, p, "MUTATED_PORT", 1)
		if err != nil {
			t.Fatalf("BuildVesselPortCase: %v", err)
		}
		res, err := Run(context.Background(), k, req)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		verdict, warn := InterpretVerdict(cr, res.Lifecycle.Canonical.Decision)
		if warn != "" {
			t.Errorf("consistency warning: %s", warn)
		}
		return verdict
	}

	t.Run("baseline_is_control_PASS", func(t *testing.T) {
		if v := run(t, baseline, akonikien); v != VerdictPass {
			t.Fatalf("control case: got %s, want PASS", v)
		}
	})

	// NAME CORRECTED IN ROUND R23. This subtest was called
	// "port_max_LOA_150_to_149_flips_baseline_to_FAIL", which was false in
	// its own terms: the vessel it mutates the port against is NOT the
	// baseline. The baseline's LOA is 140 m and fits under both 150 m and
	// 149 m, so mutating the port around it would be unobservable. The
	// subtest therefore uses a boundary vessel at exactly 150 m — which is
	// the right way to test a constraint mutation, and was always what the
	// body did. Only the name claimed otherwise.
	//
	// Within the subtest the mutation is still strictly single-field: the
	// before and after runs share the same boundary vessel and differ only
	// in Port.MaxLOA.
	t.Run("port_max_LOA_150_to_149_flips_a_boundary_vessel_to_FAIL", func(t *testing.T) {
		mutatedPort := akonikien
		mutatedPort.MaxLOA = 149
		boundaryVessel := baseline
		boundaryVessel.LOAMeters = 150
		before := run(t, boundaryVessel, akonikien)
		after := run(t, boundaryVessel, mutatedPort)
		if before != VerdictPass {
			t.Fatalf("pre-mutation control: got %s, want PASS (LOA=150 == original max)", before)
		}
		if after != VerdictFail {
			t.Fatalf("post-mutation (port max LOA 150->149): got %s, want FAIL", after)
		}
	})

	t.Run("vessel_LOA_140_to_151_flips_to_FAIL", func(t *testing.T) {
		mutated := baseline
		mutated.LOAMeters = 151
		before := run(t, baseline, akonikien)
		after := run(t, mutated, akonikien)
		if before != VerdictPass || after != VerdictFail {
			t.Fatalf("LOA mutation: before=%s after=%s, want PASS->FAIL", before, after)
		}
	})

	t.Run("draft_7.2_to_8.4_flips_to_FAIL", func(t *testing.T) {
		mutated := baseline
		mutated.DraftMeters = 8.4
		before := run(t, baseline, akonikien)
		after := run(t, mutated, akonikien)
		if before != VerdictPass || after != VerdictFail {
			t.Fatalf("draft mutation: before=%s after=%s, want PASS->FAIL", before, after)
		}
	})

	t.Run("geared_true_to_false_flips_to_FAIL", func(t *testing.T) {
		mutated := baseline
		mutated.Geared = false
		before := run(t, baseline, akonikien)
		after := run(t, mutated, akonikien)
		if before != VerdictPass || after != VerdictFail {
			t.Fatalf("geared mutation: before=%s after=%s, want PASS->FAIL", before, after)
		}
	})

	t.Run("irrelevant_field_bowthruster_does_not_change_verdict", func(t *testing.T) {
		mutated := baseline
		mutated.Bowthruster = false // not a hard constraint at Akonikien per corpus
		before := run(t, baseline, akonikien)
		after := run(t, mutated, akonikien)
		if before != after {
			t.Fatalf("bowthruster is not an Akonikien constraint: verdict changed from %s to %s, expected no change", before, after)
		}
	})
}
