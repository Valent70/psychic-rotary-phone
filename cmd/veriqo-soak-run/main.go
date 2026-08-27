// Command veriqo-soak-run is PART 10's real execution surface for
// pkg/blockers/soak's harness: a genuinely separate OS process that
// runs the soak workload loop for -minutes, checkpointing periodically,
// and -- when -state is given -- persisting its run identity and
// checkpoint chain to disk after every checkpoint, so a crash and
// restart of this exact binary against the exact same -state path
// resumes the SAME run (same RunID, continued hash chain,
// RestartCount incremented) rather than silently starting a new one.
//
// This binary does not, and cannot, single-handedly close the
// soak_72h blocker: this sandbox cannot stay up for 72 continuous
// hours. What it closes is the automation gap -- pointing this exact
// binary at -minutes=4320 (72*60) on a real long-lived host, with
// -state set to a durable path, is the same command this package's
// own short qualification runs already exercise, just for longer. See
// pkg/blockers/soak's own package doc for the harness's honest scope.
//
// Usage:
//
//	veriqo-soak-run -minutes 4320 -state /var/lib/veriqo/soak-state.json -source-root /path/to/repo -out /var/lib/veriqo/soak-report.json
//
// Exit codes: 0 = the run's own evidence checks passed (zero real
// errors, checkpoint chain verified, no leak/drift detected -- NOT a
// claim of 72h closure unless -minutes genuinely reached 4320 and
// IsFullSoak is true in the written report), 1 = a real failure was
// detected, 2 = usage/input error.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"veriqo/pkg/blockers/soak"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("veriqo-soak-run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	minutes := fs.Float64("minutes", 0.05, "target run duration in minutes (a real 72h continuous soak = 4320)")
	statePath := fs.String("state", "", "path to persist/resume run state; enables restart detection. Empty disables persistence")
	sourceRoot := fs.String("source-root", ".", "repository root to hash for source/binary identity; empty skips it")
	outPath := fs.String("out", "", "path to write the final evidence Report JSON; empty writes to stdout only")
	dropEvery := fs.Int("drop-every", 0, "inject a non-fatal dropped event every Nth iteration (0 disables)")
	failAt := fs.Int("fail-at", 0, "inject one real workload error at this iteration number (0 disables) -- for exercising the failure path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *minutes <= 0 {
		fmt.Fprintln(stderr, "veriqo-soak-run: -minutes must be positive")
		return 2
	}

	iteration := 0
	workload := func(int) error {
		iteration++
		if *failAt > 0 && iteration == *failAt {
			return fmt.Errorf("veriqo-soak-run: injected failure at iteration %d", iteration)
		}
		if *dropEvery > 0 && iteration%*dropEvery == 0 {
			return soak.ErrDroppedEvent
		}
		return nil
	}

	h := soak.NewHarness(workload)
	resumed := false
	if *statePath != "" {
		var err error
		resumed, err = h.StartResumable(*statePath, *sourceRoot)
		if err != nil {
			fmt.Fprintf(stderr, "veriqo-soak-run: StartResumable: %v\n", err)
		}
	} else {
		h.Start()
		if *sourceRoot != "" {
			if err := h.ComputeSourceIdentity(*sourceRoot); err != nil {
				fmt.Fprintf(stderr, "veriqo-soak-run: ComputeSourceIdentity: %v\n", err)
			}
		}
	}
	fmt.Fprintf(stdout, "veriqo-soak-run: run_id=%s host=%s resumed=%v restart_count=%d target_minutes=%.4f\n",
		h.Identity().RunID, h.Identity().Host.Hostname, resumed, h.Identity().RestartCount, *minutes)

	duration := time.Duration(*minutes * float64(time.Minute))
	deadline := time.Now().Add(duration)
	checkpointEvery := duration / 10
	if checkpointEvery < time.Millisecond {
		checkpointEvery = time.Millisecond
	}
	nextCheckpoint := time.Now()

	for time.Now().Before(deadline) {
		err := h.RunOnce()
		if err != nil && !errors.Is(err, soak.ErrDroppedEvent) {
			fmt.Fprintf(stderr, "veriqo-soak-run: workload error, stopping: %v\n", err)
			break
		}
		if time.Now().After(nextCheckpoint) {
			h.Checkpoint()
			nextCheckpoint = nextCheckpoint.Add(checkpointEvery)
			if *statePath != "" {
				if err := h.SaveState(*statePath); err != nil {
					fmt.Fprintf(stderr, "veriqo-soak-run: SaveState: %v\n", err)
				}
			}
		}
	}
	h.Checkpoint() // final checkpoint always taken, even on a short run
	if *statePath != "" {
		if err := h.SaveState(*statePath); err != nil {
			fmt.Fprintf(stderr, "veriqo-soak-run: final SaveState: %v\n", err)
		}
	}

	elapsed := h.Finalize()
	report := h.GenerateEvidence(elapsed, 50, 0.5)

	raw, err := report.JSON()
	if err != nil {
		fmt.Fprintf(stderr, "veriqo-soak-run: marshal report: %v\n", err)
		return 2
	}
	if *outPath != "" {
		if err := os.WriteFile(*outPath, raw, 0o600); err != nil { // #nosec G304 -- outPath is an operator-supplied CLI argument, not untrusted input
			fmt.Fprintf(stderr, "veriqo-soak-run: write report: %v\n", err)
			return 2
		}
		fmt.Fprintf(stdout, "veriqo-soak-run: wrote report to %s\n", *outPath)
	} else {
		_ = json.NewEncoder(stdout).Encode(json.RawMessage(raw))
	}

	fmt.Fprintf(stdout, "veriqo-soak-run: verdict: %s\n", report.Verdict)
	fmt.Fprintf(stdout, "veriqo-soak-run: checkpoints=%d dropped_events=%d errors=%d chain_verified=%v reconciliation_consistent=%v\n",
		len(report.Checkpoints), report.DroppedEvents, report.Errors, report.ChainVerified, report.Reconciliation.Consistent)

	pass := report.Errors == 0 && report.ChainVerified && !report.Leak.Leaked && !report.Drift.Drifted
	if !pass {
		return 1
	}
	return 0
}
