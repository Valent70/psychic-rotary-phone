// Command veriqo-demo-cases builds the three canonical Commercialization
// Sprint demo cases (pkg/commercial/democases) and writes each one's real
// Evidence Dossier v1 Machine Package (.zip) plus human Markdown to the
// given output directory -- so "3 canonical demo cases" names actual,
// inspectable, independently verifiable files on disk, not just Go test
// assertions. Each package this command writes passes the same
// standalone verifier (cmd/veriqo-commercial-verify) a customer would
// run against it.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"veriqo/pkg/commercial/democases"
	"veriqo/pkg/commercial/dossier"
)

func main() {
	outDir := "."
	if len(os.Args) > 1 {
		outDir = os.Args[1]
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "veriqo-demo-cases: creating output dir: %v\n", err)
		os.Exit(1)
	}

	cases := []struct {
		name  string
		build func() (democases.Case, error)
	}{
		{"demo1-ebl-transfer-dispute", democases.BuildEBLTransferDisputeCase},
		{"demo2-maritime-incident", democases.BuildMaritimeIncidentCase},
		{"demo3-insurance-claim", democases.BuildInsuranceClaimCase},
	}

	exitCode := 0
	for _, c := range cases {
		built, err := c.build()
		if err != nil {
			fmt.Fprintf(os.Stderr, "[FAIL] %s: building case: %v\n", c.name, err)
			exitCode = 1
			continue
		}

		zipPath := filepath.Join(outDir, c.name+".zip")
		d, err := built.WriteMachinePackage(zipPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[FAIL] %s: writing machine package: %v\n", c.name, err)
			exitCode = 1
			continue
		}

		mdPath := filepath.Join(outDir, c.name+".md")
		if err := os.WriteFile(mdPath, []byte(dossier.RenderMarkdown(d)), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "[FAIL] %s: writing markdown: %v\n", c.name, err)
			exitCode = 1
			continue
		}

		fmt.Printf("[OK] %s: case=%s outcome=%s package_hash=%s -> %s, %s\n",
			c.name, built.CaseID, built.Decision.Outcome(), d.PackageHash, zipPath, mdPath)
	}
	os.Exit(exitCode)
}
