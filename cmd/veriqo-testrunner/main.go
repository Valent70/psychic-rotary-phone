// Command veriqo-testrunner closes V7.11.1 P0-1: bounded full-suite
// test execution.
//
// The audit's objection to `go test ./...` was precise: it is a single
// unbounded process whose failure mode is "hangs forever", and whose
// output is a wall of text rather than an artifact. This runner fixes
// the three things that made that unacceptable:
//
//	timeout            — every package gets a hard deadline
//	per-package        — one package's hang cannot hide another's result
//	  isolation
//	classification     — PASS / FAIL / TIMEOUT / BUILD_FAILURE /
//	                     NO_TESTS / SKIPPED, not "exit code 1"
//	evidence artifact  — a deterministic, hash-committed JSON report
//
// The classification matters more than it looks. "The suite failed" is
// not actionable; "pkg/x timed out after 120s while pkg/y failed a
// determinism assertion and pkg/z does not compile" is three different
// tickets with three different owners.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Outcome is the classification of one package run.
type Outcome string

const (
	OutcomePass         Outcome = "PASS"
	OutcomeFail         Outcome = "FAIL"
	OutcomeTimeout      Outcome = "TIMEOUT"
	OutcomeBuildFailure Outcome = "BUILD_FAILURE"
	OutcomeNoTests      Outcome = "NO_TESTS"
	OutcomeSkipped      Outcome = "SKIPPED"
)

// PackageResult is one package's record.
type PackageResult struct {
	Package     string   `json:"package"`
	Outcome     Outcome  `json:"outcome"`
	DurationMS  int64    `json:"duration_ms"`
	ExitCode    int      `json:"exit_code"`
	FailedTests []string `json:"failed_tests,omitempty"`
	Detail      string   `json:"detail,omitempty"`
}

// Report is the evidence artifact.
type Report struct {
	SchemaVersion string          `json:"schema_version"`
	TimeoutSec    int             `json:"timeout_seconds"`
	Race          bool            `json:"race"`
	Packages      []PackageResult `json:"packages"`
	Totals        map[string]int  `json:"totals"`
	TotalDuration int64           `json:"total_duration_ms"`
	Verdict       string          `json:"verdict"`
	ReportHash    string          `json:"report_hash"`
}

func main() {
	timeout := flag.Int("timeout", 120, "per-package timeout in seconds")
	race := flag.Bool("race", false, "run with the race detector")
	out := flag.String("out", "evidence/test-execution.json", "evidence artifact path")
	pattern := flag.String("pattern", "./...", "package pattern")
	verbose := flag.Bool("v", false, "stream per-package progress")
	flag.Parse()

	pkgs, err := listPackages(*pattern)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot enumerate packages:", err)
		os.Exit(2)
	}
	sort.Strings(pkgs)

	rep := Report{SchemaVersion: "veriqo.testrun/v1", TimeoutSec: *timeout, Race: *race,
		Totals: map[string]int{}}
	start := time.Now()

	for _, p := range pkgs {
		r := runPackage(p, *timeout, *race)
		rep.Packages = append(rep.Packages, r)
		rep.Totals[string(r.Outcome)]++
		if *verbose {
			fmt.Printf("%-10s %-55s %6dms\n", r.Outcome, r.Package, r.DurationMS)
		}
	}
	rep.TotalDuration = time.Since(start).Milliseconds()

	// Verdict semantics: NO_TESTS is not a failure (many packages are
	// pure type declarations), but TIMEOUT and BUILD_FAILURE are — a
	// suite that cannot build or cannot finish has not been run.
	bad := rep.Totals[string(OutcomeFail)] + rep.Totals[string(OutcomeTimeout)] +
		rep.Totals[string(OutcomeBuildFailure)]
	if bad == 0 {
		rep.Verdict = "PASS"
	} else {
		rep.Verdict = "FAIL"
	}
	rep.ReportHash = hashReport(rep)

	if err := writeReport(*out, rep); err != nil {
		fmt.Fprintln(os.Stderr, "cannot write evidence:", err)
		os.Exit(2)
	}
	fmt.Printf("\nveriqo-testrunner: %s — %d packages in %dms\n", rep.Verdict, len(rep.Packages),
		rep.TotalDuration)
	for _, k := range []Outcome{OutcomePass, OutcomeNoTests, OutcomeSkipped, OutcomeFail,
		OutcomeTimeout, OutcomeBuildFailure} {
		if n := rep.Totals[string(k)]; n > 0 {
			fmt.Printf("  %-14s %d\n", k, n)
		}
	}
	fmt.Println("  evidence:", *out)
	fmt.Println("  report hash:", rep.ReportHash)
	if rep.Verdict != "PASS" {
		for _, p := range rep.Packages {
			switch p.Outcome {
			case OutcomeFail, OutcomeTimeout, OutcomeBuildFailure:
				fmt.Printf("  %-14s %s %s\n", p.Outcome, p.Package, p.Detail)
			}
		}
		os.Exit(1)
	}
}

func listPackages(pattern string) ([]string, error) {
	cmd := exec.Command("go", "list", pattern)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%v: %s", err, stderr.String())
	}
	var pkgs []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			pkgs = append(pkgs, line)
		}
	}
	return pkgs, nil
}

func runPackage(pkg string, timeoutSec int, race bool) PackageResult {
	args := []string{"test", "-count=1",
		fmt.Sprintf("-timeout=%ds", timeoutSec)}
	if race {
		args = append(args, "-race")
	}
	args = append(args, pkg)

	start := time.Now()
	cmd := exec.Command("go", args...)
	combined, err := cmd.CombinedOutput()
	dur := time.Since(start).Milliseconds()

	r := PackageResult{Package: pkg, DurationMS: dur}
	text := string(combined)
	if cmd.ProcessState != nil {
		r.ExitCode = cmd.ProcessState.ExitCode()
	}
	switch {
	case err == nil && strings.Contains(text, "no test files"):
		r.Outcome = OutcomeNoTests
	case err == nil:
		r.Outcome = OutcomePass
	case strings.Contains(text, "panic: test timed out"):
		// The wall-clock guard the audit asked for. Reported as its own
		// class because a timeout is a liveness bug, not a wrong answer.
		r.Outcome = OutcomeTimeout
		r.Detail = "exceeded " + fmt.Sprint(timeoutSec) + "s"
	case strings.Contains(text, "[build failed]") || strings.Contains(text, "cannot find package") ||
		strings.Contains(text, "undefined:") || strings.Contains(text, "[setup failed]"):
		r.Outcome = OutcomeBuildFailure
		r.Detail = firstLine(text)
	default:
		r.Outcome = OutcomeFail
		r.FailedTests = failedTests(text)
		r.Detail = firstLine(text)
	}
	return r
}

func failedTests(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "--- FAIL:") {
			name := strings.TrimSpace(strings.TrimPrefix(line, "--- FAIL:"))
			if i := strings.Index(name, " "); i > 0 {
				name = name[:i]
			}
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "===") {
			if len(line) > 200 {
				line = line[:200]
			}
			return line
		}
	}
	return ""
}

// hashReport commits to the outcome set, not to timings — durations are
// machine-dependent, so including them would make the artifact
// unverifiable across runs while proving nothing.
func hashReport(r Report) string {
	var sb strings.Builder
	sb.WriteString("veriqo_testrun/v1\ntimeout=" + fmt.Sprint(r.TimeoutSec) +
		"\nrace=" + fmt.Sprint(r.Race) + "\n")
	pkgs := append([]PackageResult(nil), r.Packages...)
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Package < pkgs[j].Package })
	for _, p := range pkgs {
		sb.WriteString(p.Package + "=" + string(p.Outcome) + ":" +
			strings.Join(p.FailedTests, ",") + "\n")
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

func writeReport(path string, r Report) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
