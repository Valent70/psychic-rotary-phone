package chaos

import "testing"

// TestPropertySafetySweep_NoSplitBrainAcrossRandomizedFaults is the
// CI-bound version of RunSafetySweep — see property_sweep.go's package
// doc comment for the honest "bounded, not full Jepsen" scope note.
// Trial count here is intentionally smaller than a report-grade run
// (cmd/veriqo-live-demo's -fault-trials flag defaults to 150) so this
// stays fast enough for `go test -race ./...` to run on every commit;
// the report-grade run is the reproducible-evidence artifact, not this
// test.
func TestPropertySafetySweep_NoSplitBrainAcrossRandomizedFaults(t *testing.T) {
	report := RunSafetySweep(40)
	if report.SafetyViolations > 0 {
		for _, r := range report.Results {
			if r.SplitBrain {
				t.Errorf("split-brain detected: seed=%d loss=%.2f dup=%.2f reorder=%.0fms skew=%.0fms",
					r.Seed, r.PacketLossProb, r.DuplicationProb, r.ReorderMaxMs, r.MaxSkewMs)
			}
		}
		t.Fatalf("%d/%d randomized trials produced split-brain (safety violation)", report.SafetyViolations, report.Trials)
	}
	t.Logf("safety sweep: %d trials, %d safe, %d liveness-failures (adversarial params legitimately preventing progress, not a bug)",
		report.Trials, report.SafeTrials, report.LivenessFailures)
}
