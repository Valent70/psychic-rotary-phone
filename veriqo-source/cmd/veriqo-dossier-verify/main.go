// Command veriqo-dossier-verify is the CLI surface for
// pkg/insurance/dossierverify — the Independent Dossier Verifier the
// Round 4 work order requires under the tagline "Don't trust VERIQO.
// Verify VERIQO." See that package's own doc comment for the full
// honesty note on what "independent" means here and the exact checks
// performed. This command is deliberately thin: every check it prints
// is the SAME logic cmd/veriqo-readiness registers as its own
// dossier_verification gate, via the shared library.
//
// Usage:
//
//	veriqo-dossier-verify -case CASE-INS-002
//	veriqo-dossier-verify -case golden -out /tmp/report.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"veriqo/pkg/insurance/dossierverify"
)

func main() {
	caseFlag := flag.String("case", "", `case ID to verify: CASE-INS-001 .. CASE-INS-007, or "golden"`)
	outFlag := flag.String("out", "", "optional path to write the report as JSON")
	flag.Parse()

	if *caseFlag == "" {
		fmt.Fprintln(os.Stderr, `veriqo-dossier-verify: -case is required (CASE-INS-001..007, or "golden")`)
		os.Exit(2)
	}

	report, err := dossierverify.Verify(*caseFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "veriqo-dossier-verify: %v\n", err)
		os.Exit(2)
	}

	printReport(report)

	if *outFlag != "" {
		raw, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "veriqo-dossier-verify: marshaling report: %v\n", err)
			os.Exit(2)
		}
		if err := os.WriteFile(*outFlag, raw, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "veriqo-dossier-verify: writing report: %v\n", err)
			os.Exit(2)
		}
	}

	if !report.Pass() {
		os.Exit(1)
	}
}

func printReport(r dossierverify.Report) {
	fmt.Println("===== VERIQO INDEPENDENT DOSSIER VERIFIER =====")
	fmt.Println(r.Tagline)
	fmt.Printf("case: %s\n\n", r.CaseID)
	for _, c := range r.Checks {
		status := "PASS"
		if !c.Pass {
			status = "FAIL"
		}
		fmt.Printf("[%s] %-28s %s\n", status, c.Name, c.Detail)
	}
	fmt.Println()
	if r.Pass() {
		fmt.Println("verdict: INDEPENDENTLY VERIFIED — every claim above was recomputed, not read.")
	} else {
		fmt.Println("verdict: VERIFICATION FAILED — see the FAIL rows above.")
	}
}
