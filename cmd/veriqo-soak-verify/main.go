// Command veriqo-soak-verify is an independent verifier over soak
// evidence on disk: given ONLY a Report or PersistedState JSON file --
// no shared memory or process state with whatever produced it -- it
// recomputes the checkpoint hash chain (soak.VerifyChain) and the
// checkpoint-span-vs-elapsed reconciliation (soak.Reconcile)
// independently, rather than trusting the embedded ChainVerified /
// Reconciliation.Consistent booleans a tampered file could simply
// assert. This mirrors cmd/veriqo-verify's own "recompute, don't
// trust" independence discipline, scoped to soak evidence.
//
// Usage:
//
//	veriqo-soak-verify -report /path/to/soak-report.json
//	veriqo-soak-verify -state /path/to/soak-state.json
//
// Exit codes: 0 = PASSED (chain and reconciliation both independently
// verify), 1 = FAILED, 2 = usage/input error.
package main

import (
	"encoding/json"
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
	fs := flag.NewFlagSet("veriqo-soak-verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	reportPath := fs.String("report", "", "path to a Report JSON file written by veriqo-soak-run -out")
	statePath := fs.String("state", "", "path to a PersistedState JSON file written by veriqo-soak-run -state")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if (*reportPath == "") == (*statePath == "") {
		fmt.Fprintln(stderr, "usage: veriqo-soak-verify (-report <file> | -state <file>)")
		return 2
	}

	var checkpoints []soak.Checkpoint
	var runID string
	var elapsed time.Duration
	haveElapsed := false

	if *reportPath != "" {
		raw, err := os.ReadFile(*reportPath) // #nosec G304 -- reportPath is an operator-supplied CLI argument, not untrusted input
		if err != nil {
			fmt.Fprintf(stderr, "veriqo-soak-verify: read report: %v\n", err)
			return 2
		}
		var report soak.Report
		if err := json.Unmarshal(raw, &report); err != nil {
			fmt.Fprintf(stderr, "veriqo-soak-verify: parse report: %v\n", err)
			return 2
		}
		checkpoints = report.Checkpoints
		runID = report.Identity.RunID
		if d, err := time.ParseDuration(report.ActualDuration); err == nil {
			elapsed = d
			haveElapsed = true
		}
	} else {
		state, err := soak.LoadState(*statePath)
		if err != nil {
			fmt.Fprintf(stderr, "veriqo-soak-verify: load state: %v\n", err)
			return 2
		}
		checkpoints = state.Checkpoints
		runID = state.Identity.RunID
		if n := len(checkpoints); n > 0 {
			elapsed = time.Duration(checkpoints[n-1].AtSeconds * float64(time.Second))
			haveElapsed = true
		}
	}

	fmt.Fprintf(stdout, "veriqo-soak-verify: independently re-verifying run %s (%d checkpoints)\n", runID, len(checkpoints))

	chainErr := soak.VerifyChain(checkpoints)
	fmt.Fprintf(stdout, "  chain_verified: %v", chainErr == nil)
	if chainErr != nil {
		fmt.Fprintf(stdout, " (%v)", chainErr)
	}
	fmt.Fprintln(stdout)

	reconciled := true
	if haveElapsed {
		rec := soak.Reconcile(checkpoints, elapsed)
		reconciled = rec.Consistent
		fmt.Fprintf(stdout, "  reconciliation_consistent: %v (%s)\n", rec.Consistent, rec.Detail)
	} else {
		fmt.Fprintln(stdout, "  reconciliation_consistent: skipped (no elapsed duration available in this evidence file)")
	}

	if chainErr != nil || !reconciled {
		fmt.Fprintln(stdout, "veriqo-soak-verify: VERDICT: FAILED")
		return 1
	}
	fmt.Fprintln(stdout, "veriqo-soak-verify: VERDICT: PASSED")
	return 0
}
