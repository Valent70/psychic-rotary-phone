package main

import (
	"os"
	"strings"
	"testing"

	"veriqo/pkg/platform/config"
)

// osPipe is a small test helper wrapping os.Pipe with cleanup-friendly
// error handling, since run() writes to *os.File (matching os.Stdout /
// os.Stderr's real type) rather than the io.Writer interface.
func osPipe(t *testing.T) (*os.File, *os.File, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	// Drain the read end in the background so a full pipe buffer never
	// blocks the CLI's writes during the test.
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := r.Read(buf); err != nil {
				return
			}
		}
	}()
	return r, w, nil
}

// TestRunDarkVesselDemoDeterministic proves the same tick produces a
// byte-identical report across independent runs — the CLI's own
// end-to-end determinism guarantee, not just each underlying package's.
func TestRunDarkVesselDemoDeterministic(t *testing.T) {
	sources, policies, err := config.LoadDocument(defaultPolicyYAML)
	if err != nil {
		t.Fatalf("load embedded config: %v", err)
	}
	if len(policies) != 1 {
		t.Fatalf("expected 1 policy in embedded config, got %d", len(policies))
	}

	r1, err := runDarkVesselDemo(sources, policies[0], 500)
	if err != nil {
		t.Fatalf("run 1: %v", err)
	}
	r2, err := runDarkVesselDemo(sources, policies[0], 500)
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if r1 != r2 {
		t.Fatalf("expected byte-identical reports across independent runs at the same tick")
	}

	if !strings.Contains(r1, "ESCALATE") {
		t.Fatalf("expected the synthetic dark-vessel scenario to escalate, got:\n%s", r1)
	}
	if !strings.Contains(r1, "run_id=") {
		t.Fatalf("expected workflow run_id to be reported")
	}
}

func TestRunDarkVesselDemoDifferentTickDiffersButBothSucceed(t *testing.T) {
	sources, policies, err := config.LoadDocument(defaultPolicyYAML)
	if err != nil {
		t.Fatal(err)
	}
	r1, err := runDarkVesselDemo(sources, policies[0], 1)
	if err != nil {
		t.Fatalf("run at tick 1: %v", err)
	}
	r2, err := runDarkVesselDemo(sources, policies[0], 2)
	if err != nil {
		t.Fatalf("run at tick 2: %v", err)
	}
	if r1 == r2 {
		t.Fatal("expected different ticks to (at minimum) produce different run_id")
	}
}

func TestRunCLIRejectsUnknownScenario(t *testing.T) {
	rOut, wOut, err := osPipe(t)
	if err != nil {
		t.Fatal(err)
	}
	defer rOut.Close()
	rErr, wErr, err := osPipe(t)
	if err != nil {
		t.Fatal(err)
	}
	defer rErr.Close()
	code := run([]string{"-scenario", "not-a-real-scenario"}, wOut, wErr)
	wOut.Close()
	wErr.Close()
	if code != 2 {
		t.Fatalf("expected exit code 2 for unknown scenario, got %d", code)
	}
}

func TestRunCLIRejectsMissingConfigFile(t *testing.T) {
	rOut, wOut, err := osPipe(t)
	if err != nil {
		t.Fatal(err)
	}
	defer rOut.Close()
	rErr, wErr, err := osPipe(t)
	if err != nil {
		t.Fatal(err)
	}
	defer rErr.Close()
	code := run([]string{"-config", "/nonexistent/path/policy.yaml"}, wOut, wErr)
	wOut.Close()
	wErr.Close()
	if code != 1 {
		t.Fatalf("expected exit code 1 for missing config file, got %d", code)
	}
}

func TestRunCLIHappyPathExitsZero(t *testing.T) {
	rOut, wOut, err := osPipe(t)
	if err != nil {
		t.Fatal(err)
	}
	defer rOut.Close()
	rErr, wErr, err := osPipe(t)
	if err != nil {
		t.Fatal(err)
	}
	defer rErr.Close()
	code := run([]string{"-tick", "999"}, wOut, wErr)
	wOut.Close()
	wErr.Close()
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
}
