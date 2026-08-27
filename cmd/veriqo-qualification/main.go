// Command veriqo-qualification runs all eight production blockers'
// fixture qualification pipelines (pkg/blockers/orchestrator) against
// docs/governance/production-blockers.json, prints a per-blocker
// verdict, and writes evidence/blockers-qualification-report.json.
//
// A PASS here means every blocker reached READY_FOR_REAL_QUALIFICATION:
// its internal machinery -- provider abstraction, fixture, integrity
// checks, evidence shape -- is proven correct against a fixture. It
// does NOT mean any blocker is VERIFIED; that status can only ever come
// from pkg/governance/qualification validating real external evidence
// (a real pentest report, a real 100-node run, a real HSM/KMS tenancy,
// and so on). This command exists so that when that real evidence
// arrives, closing each gate is a provider swap against already-proven
// machinery, not a from-scratch build.
//
// Exit codes: 0 all eight blockers qualified, 1 at least one did not,
// 2 could not even load the registry.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"veriqo/pkg/blockers"
	"veriqo/pkg/blockers/orchestrator"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}

	registryPath := filepath.Join(root, "docs", "governance", "production-blockers.json")
	data, err := os.ReadFile(registryPath) // #nosec G304 G703 -- root is an operator-supplied CLI argument, not untrusted input
	if err != nil {
		fmt.Fprintf(os.Stderr, "veriqo-qualification: read %s: %v\n", registryPath, err)
		os.Exit(2)
	}
	registry, err := blockers.LoadRegistry(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "veriqo-qualification: %v\n", err)
		os.Exit(2)
	}

	report := orchestrator.RunAll(context.Background(), root, registry, orchestrator.Overrides{})

	fmt.Println("===== VERIQO 8-BLOCKER FIXTURE QUALIFICATION =====")
	for _, o := range report.Outcomes {
		verdict := "PASS"
		if !o.Pass {
			verdict = "FAIL"
		}
		fmt.Printf("%-22s %-30s %s\n", o.ID, o.Status, verdict)
		if o.Error != "" {
			fmt.Printf("  error: %s\n", o.Error)
		}
		if !o.Pass && o.FailureReason != "" {
			fmt.Printf("  reason: %s\n", o.FailureReason)
		}
	}
	fmt.Println()
	fmt.Printf("overall: %v\n", report.AllPass)
	fmt.Println()
	fmt.Println("Note: PASS/READY_FOR_REAL_QUALIFICATION means this blocker's internal")
	fmt.Println("machinery is proven against a fixture. None of these blockers is VERIFIED --")
	fmt.Println("that requires real external evidence through pkg/governance/qualification.")

	raw, err := json.MarshalIndent(report, "", "  ")
	if err == nil {
		evidenceDir := filepath.Join(root, "evidence")
		reportPath := filepath.Join(evidenceDir, "blockers-qualification-report.json")
		_ = os.MkdirAll(evidenceDir, 0o750)      // #nosec G703 -- root is an operator-supplied CLI argument, not untrusted input
		_ = os.WriteFile(reportPath, raw, 0o600) // #nosec G304 G703 -- root is an operator-supplied CLI argument, not untrusted input
	}

	if !report.AllPass {
		os.Exit(1)
	}
}
