// Command veriqo-soak-report renders a human-readable summary from
// soak evidence already on disk -- either a full Report JSON written
// by veriqo-soak-run's -out, or a PersistedState written by its -state
// (useful specifically when a run crashed before it could produce a
// final Report: PersistedState is what survives, and this command
// derives everything a Report would have said from it -- checkpoint
// chain verification, reconciliation, and best-effort leak/drift
// signals computed from the checkpoint series itself, all via
// pkg/blockers/soak's own *FromCheckpoints functions rather than a
// second, hand-rolled copy of that logic).
//
// Usage:
//
//	veriqo-soak-report -report /path/to/soak-report.json
//	veriqo-soak-report -state /path/to/soak-state.json
//
// Exit codes: 0 = report rendered, 1 = the underlying run's own
// evidence shows a failure (chain broken, leak, or drift), 2 =
// usage/input error.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"veriqo/pkg/blockers/soak"
)

func timeDurationFromSeconds(s float64) time.Duration {
	return time.Duration(s * float64(time.Second))
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("veriqo-soak-report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	reportPath := fs.String("report", "", "path to a Report JSON file written by veriqo-soak-run -out")
	statePath := fs.String("state", "", "path to a PersistedState JSON file written by veriqo-soak-run -state")
	leakThreshold := fs.Int("leak-threshold", 50, "goroutine growth allowed above baseline before flagging a leak")
	driftThreshold := fs.Float64("drift-threshold", 0.5, "fractional heap growth (first half vs second half) before flagging drift")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if (*reportPath == "") == (*statePath == "") {
		fmt.Fprintln(stderr, "usage: veriqo-soak-report (-report <file> | -state <file>)")
		return 2
	}

	if *reportPath != "" {
		raw, err := os.ReadFile(*reportPath) // #nosec G304 -- reportPath is an operator-supplied CLI argument, not untrusted input
		if err != nil {
			fmt.Fprintf(stderr, "veriqo-soak-report: read report: %v\n", err)
			return 2
		}
		var report soak.Report
		if err := json.Unmarshal(raw, &report); err != nil {
			fmt.Fprintf(stderr, "veriqo-soak-report: parse report: %v\n", err)
			return 2
		}
		printReport(stdout, report.Identity, report.Checkpoints, report.ChainVerified, report.Reconciliation.Consistent,
			report.Leak, report.Drift, report.Errors, report.DroppedEvents, report.Verdict)
		if report.Errors > 0 || !report.ChainVerified || report.Leak.Leaked || report.Drift.Drifted {
			return 1
		}
		return 0
	}

	state, err := soak.LoadState(*statePath)
	if err != nil {
		fmt.Fprintf(stderr, "veriqo-soak-report: load state: %v\n", err)
		return 2
	}
	chainErr := soak.VerifyChain(state.Checkpoints)
	leak := soak.DetectLeakFromCheckpoints(state.Checkpoints, *leakThreshold)
	drift := soak.DetectDriftFromCheckpoints(state.Checkpoints, *driftThreshold)
	dropped := 0
	reconciled := false
	if n := len(state.Checkpoints); n > 0 {
		dropped = state.Checkpoints[n-1].DroppedEvents
		// PersistedState carries no independently-measured elapsed
		// duration (that only exists once Finalize runs, at the end of a
		// completed process instance) -- the last checkpoint's own
		// AtSeconds is the best real proxy available: the checkpoint
		// series' own account of how far into the run it got.
		elapsed := timeDurationFromSeconds(state.Checkpoints[n-1].AtSeconds)
		reconciled = soak.Reconcile(state.Checkpoints, elapsed).Consistent
	}
	verdict := "reconstructed from PersistedState (no final Report was ever written -- likely an interrupted run)"
	printReport(stdout, state.Identity, state.Checkpoints, chainErr == nil, reconciled, leak, drift, 0, dropped, verdict)
	fmt.Fprintf(stdout, "  note: leak/drift here are computed from the checkpoint SERIES only (DetectLeakFromCheckpoints/"+
		"DetectDriftFromCheckpoints), not a live baseline -- see pkg/blockers/soak's own doc comments\n")
	if chainErr != nil || leak.Leaked || drift.Drifted {
		return 1
	}
	return 0
}

func printReport(stdout *os.File, id soak.RunIdentity, checkpoints []soak.Checkpoint, chainVerified, reconciled bool,
	leak soak.LeakVerdict, drift soak.DriftVerdict, errs, dropped int, verdict string) {
	fmt.Fprintf(stdout, "veriqo-soak-report: run_id=%s host=%s restart_count=%d\n", id.RunID, id.Host.Hostname, id.RestartCount)
	fmt.Fprintf(stdout, "  source_hash=%s binary_hash=%s\n", id.SourceHash, id.BinaryHash)
	fmt.Fprintf(stdout, "  checkpoints=%d chain_verified=%v reconciliation_consistent=%v\n", len(checkpoints), chainVerified, reconciled)
	fmt.Fprintf(stdout, "  errors=%d dropped_events=%d leak_detected=%v drift_detected=%v\n", errs, dropped, leak.Leaked, drift.Drifted)
	fmt.Fprintf(stdout, "  verdict: %s\n", verdict)
}
