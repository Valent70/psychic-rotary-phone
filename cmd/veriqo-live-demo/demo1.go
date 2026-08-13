package main

import (
	"fmt"
	"os/exec"
	"regexp"
)

type demo1Result struct {
	DemoStdoutSHA256    string `json:"demo_stdout_sha256"`
	BundlePath          string `json:"bundle_path"`
	BundleSHA256        string `json:"bundle_sha256"`
	VerifyExitCode      int    `json:"verify_exit_code"`
	VerifyStdout        string `json:"verify_stdout"`
	IndependentlyPassed bool   `json:"independently_passed"`
}

var bundlePathRe = regexp.MustCompile(`-bundle\s+(\S+\.json)\s+-domain\s+fusion`)

// runDemo1 executes the already-tested cmd/veriqo-demo binary as a real
// child process (not an in-process function call — this run's stdout is
// exactly what an investor watching a terminal would see), extracts the
// evidence bundle path it reports, then executes cmd/veriqo-verify
// against that same bundle as a SECOND, independent OS process — the
// same pattern test/integration/ivf_cross_process_test.go already
// proves works, now driven live instead of from a test harness.
func runDemo1(w *narrator, repoRoot, outDir string, bins map[string]string) (demo1Result, error) {
	res := demo1Result{}

	w.line("launching cmd/veriqo-demo as a child process (dark-vessel scenario)…")
	demoOut, err := exec.Command(bins["veriqo-demo"], "-scenario", "dark-vessel", "-tick", "100").CombinedOutput() // #nosec G204 -- fixed, hardcoded args against this repo's own built binary, not external input
	if err != nil {
		return res, fmt.Errorf("veriqo-demo run: %w\n%s", err, demoOut)
	}
	res.DemoStdoutSHA256 = sha256Bytes(demoOut)
	w.line(fmt.Sprintf("veriqo-demo exited 0, stdout sha256=%s (%d bytes)", res.DemoStdoutSHA256[:16]+"…", len(demoOut)))

	m := bundlePathRe.FindSubmatch(demoOut)
	if m == nil {
		return res, fmt.Errorf("could not locate evidence bundle path in veriqo-demo output")
	}
	res.BundlePath = string(m[1])
	bh, err := sha256File(res.BundlePath)
	if err != nil {
		return res, fmt.Errorf("hashing bundle file: %w", err)
	}
	res.BundleSHA256 = bh
	w.line(fmt.Sprintf("evidence bundle: %s (sha256=%s…)", res.BundlePath, bh[:16]))

	w.line("launching cmd/veriqo-verify as a SEPARATE, independent process against that bundle…")
	verifyCmd := exec.Command(bins["veriqo-verify"], "-bundle", res.BundlePath, "-domain", "fusion") // #nosec G204 -- fixed args plus an internally-produced bundle path, not external input
	verifyOut, verifyErr := verifyCmd.CombinedOutput()
	res.VerifyStdout = string(verifyOut)
	if verifyCmd.ProcessState != nil {
		res.VerifyExitCode = verifyCmd.ProcessState.ExitCode()
	} else if verifyErr != nil {
		res.VerifyExitCode = -1
	}
	res.IndependentlyPassed = res.VerifyExitCode == 0
	status := "PASSED"
	if !res.IndependentlyPassed {
		status = "FAILED"
	}
	w.line(fmt.Sprintf("independent verification: exit=%d -> %s", res.VerifyExitCode, status))
	if !res.IndependentlyPassed {
		return res, fmt.Errorf("independent cross-process verification did not pass (exit %d): %s", res.VerifyExitCode, verifyOut)
	}
	w.line("Demo 1 proof: a certificate produced by one process was independently")
	w.line("re-derived and accepted by a SEPARATE process with no shared memory or state.")
	return res, nil
}
