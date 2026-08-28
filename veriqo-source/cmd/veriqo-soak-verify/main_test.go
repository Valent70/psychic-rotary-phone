package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"veriqo/pkg/blockers/soak"
)

func captureFiles(t *testing.T) (stdout, stderr *os.File, read func(f *os.File) string) {
	t.Helper()
	dir := t.TempDir()
	outF, err := os.Create(filepath.Join(dir, "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	errF, err := os.Create(filepath.Join(dir, "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { outF.Close(); errF.Close() })
	return outF, errF, func(f *os.File) string {
		raw, err := os.ReadFile(f.Name()) // #nosec G304 -- a temp file this test itself created
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
}

func genuineReport(t *testing.T) soak.Report {
	t.Helper()
	h := soak.NewHarness(func(int) error { return nil })
	h.Start()
	h.Checkpoint()
	h.Checkpoint()
	h.Checkpoint()
	elapsed := h.Finalize()
	return h.GenerateEvidence(elapsed, 50, 0.5)
}

func writeReportFile(t *testing.T, dir string, report soak.Report) string {
	t.Helper()
	raw, err := report.JSON()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "report.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestVerifyPassesForGenuineReport(t *testing.T) {
	dir := t.TempDir()
	path := writeReportFile(t, dir, genuineReport(t))
	stdout, stderr, read := captureFiles(t)
	code := run([]string{"-report", path}, stdout, stderr)
	if code != 0 {
		t.Fatalf("expected PASSED (exit 0) for genuine evidence, got %d; stdout=%s stderr=%s", code, read(stdout), read(stderr))
	}
}

func TestVerifyDetectsTamperedCheckpoint(t *testing.T) {
	report := genuineReport(t)
	if len(report.Checkpoints) < 2 {
		t.Fatal("expected at least 2 checkpoints from the fixture harness")
	}
	report.Checkpoints[0].Iterations = 999999 // mutate a field without recomputing its hash

	dir := t.TempDir()
	path := writeReportFile(t, dir, report)
	stdout, stderr, read := captureFiles(t)
	code := run([]string{"-report", path}, stdout, stderr)
	if code != 1 {
		t.Fatalf("expected FAILED (exit 1) for a tampered checkpoint, got %d; stdout=%s stderr=%s", code, read(stdout), read(stderr))
	}
}

func TestVerifyRequiresExactlyOneSource(t *testing.T) {
	stdout, stderr, _ := captureFiles(t)
	if code := run(nil, stdout, stderr); code != 2 {
		t.Fatalf("expected usage error, got %d", code)
	}
}

func TestVerifyFromStateFile(t *testing.T) {
	dir := t.TempDir()
	h := soak.NewHarness(func(int) error { return nil })
	path := filepath.Join(dir, "state.json")
	if _, err := h.StartResumable(path, ""); err != nil {
		t.Fatal(err)
	}
	h.Checkpoint()
	h.Checkpoint()
	if err := h.SaveState(path); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, read := captureFiles(t)
	code := run([]string{"-state", path}, stdout, stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stdout=%s stderr=%s", code, read(stdout), read(stderr))
	}
}

func TestVerifyRejectsUnreadableReport(t *testing.T) {
	stdout, stderr, _ := captureFiles(t)
	code := run([]string{"-report", filepath.Join(t.TempDir(), "does-not-exist.json")}, stdout, stderr)
	if code != 2 {
		t.Fatalf("expected usage/input error 2, got %d", code)
	}
}

func TestVerifyReportJSONRoundTripsThroughStdlib(t *testing.T) {
	// A sanity check that Report's JSON encoding really is what
	// veriqo-soak-run would hand veriqo-soak-verify -- not a
	// hand-simplified stand-in shape.
	report := genuineReport(t)
	raw, err := report.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var round soak.Report
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatal(err)
	}
	if round.Identity.RunID != report.Identity.RunID {
		t.Fatal("expected run id to round-trip through JSON")
	}
}
