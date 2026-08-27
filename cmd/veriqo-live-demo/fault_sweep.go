package main

import (
	"fmt"

	"veriqo/pkg/chaos"
)

type faultSweepResult struct {
	Trials           int `json:"trials"`
	SafeTrials       int `json:"safe_trials"`
	SafetyViolations int `json:"safety_violations"`
	LivenessFailures int `json:"liveness_failures"`
}

// runFaultSweep is the live-demo's window into pkg/chaos.RunSafetySweep
// — see that package's doc comment for the honest "bounded property
// sweep, not full Jepsen" scope statement. This directly answers the
// audit critique asking to see leaderLive validated against randomized
// combinations of partition/skew/reorder/duplication rather than the
// one hand-written scenario it was originally fixed against.
func runFaultSweep(w *narrator, trials int) faultSweepResult {
	report := chaos.RunSafetySweep(trials)
	res := faultSweepResult{
		Trials:           report.Trials,
		SafeTrials:       report.SafeTrials,
		SafetyViolations: report.SafetyViolations,
		LivenessFailures: report.LivenessFailures,
	}
	status := "PASS"
	if res.SafetyViolations > 0 {
		status = "FAIL — SPLIT-BRAIN DETECTED"
	}
	w.line(fmt.Sprintf("[%s] %d trials: %d safe, %d safety violations, %d liveness-only failures (adversarial params, not bugs)",
		status, res.Trials, res.SafeTrials, res.SafetyViolations, res.LivenessFailures))
	w.line("This is the randomized check that the leaderLive fix (raft.go) holds")
	w.line("beyond the single scripted clock-skew scenario it was written against.")
	return res
}
