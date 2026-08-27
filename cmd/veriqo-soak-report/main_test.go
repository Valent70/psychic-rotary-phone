package main

import (
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

func writeReport(t *testing.T, dir string) string {
	t.Helper()
	h := soak.NewHarness(func(int) error { return nil })
	h.Start()
	h.Checkpoint()
	h.Checkpoint()
	elapsed := h.Finalize()
	report := h.GenerateEvidence(elapsed, 50, 0.5)
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

func writeState(t *testing.T, dir string) string {
	t.Helper()
	h := soak.NewHarness(func(int) error { return nil })
	path := filepath.Join(dir, "state.json")
	if _, err := h.StartResumable(path, ""); err != nil {
		t.Fatal(err)
	}
	h.Checkpoint()
	if err := h.SaveState(path); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReportFromReportFile(t *testing.T) {
	dir := t.TempDir()
	path := writeReport(t, dir)
	stdout, stderr, read := captureFiles(t)
	code := run([]string{"-report", path}, stdout, stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%s", code, read(stderr))
	}
	out := read(stdout)
	if len(out) == 0 {
		t.Fatal("expected non-empty summary output")
	}
}

func TestReportFromStateFile(t *testing.T) {
	dir := t.TempDir()
	path := writeState(t, dir)
	stdout, stderr, read := captureFiles(t)
	code := run([]string{"-state", path}, stdout, stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%s", code, read(stderr))
	}
	if len(read(stdout)) == 0 {
		t.Fatal("expected non-empty summary output")
	}
}

func TestReportRequiresExactlyOneSource(t *testing.T) {
	stdout, stderr, _ := captureFiles(t)
	if code := run(nil, stdout, stderr); code != 2 {
		t.Fatalf("expected usage error with neither -report nor -state, got %d", code)
	}

	dir := t.TempDir()
	reportPath := writeReport(t, dir)
	statePath := writeState(t, dir)
	stdout2, stderr2, _ := captureFiles(t)
	if code := run([]string{"-report", reportPath, "-state", statePath}, stdout2, stderr2); code != 2 {
		t.Fatalf("expected usage error with BOTH -report and -state, got %d", code)
	}
}

func TestReportFailsOnTamperedChain(t *testing.T) {
	dir := t.TempDir()
	path := writeReport(t, dir)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt the file so it no longer parses as a valid soak.Report --
	// exercising the "parse report" error path with exit code 2.
	if err := os.WriteFile(path, append(raw, []byte("not json")...), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, read := captureFiles(t)
	code := run([]string{"-report", path}, stdout, stderr)
	if code != 2 {
		t.Fatalf("expected exit 2 for an unparseable report, got %d; stderr=%s", code, read(stderr))
	}
}
