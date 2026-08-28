package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"veriqo/pkg/blockers/soak"
)

// captureFiles gives run() real *os.File handles (its stdout/stderr
// parameters match cmd/veriqo-verify's own testable signature) backed
// by temp files, so output can be read back afterward.
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

func TestRunSucceedsAndWritesReport(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "report.json")
	stdout, stderr, read := captureFiles(t)

	code := run([]string{"-minutes", "0.01", "-source-root", ".", "-out", outPath}, stdout, stderr)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr=%s", code, read(stderr))
	}

	raw, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("expected a report file to be written: %v", err)
	}
	var report soak.Report
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("expected valid report JSON: %v", err)
	}
	if report.Identity.RunID == "" {
		t.Fatal("expected the written report to carry a run id")
	}
	if !bytes.Contains([]byte(read(stdout)), []byte(report.Identity.RunID)) {
		t.Fatal("expected stdout to mention the run id")
	}
}

func TestRunRejectsNonPositiveMinutes(t *testing.T) {
	stdout, stderr, _ := captureFiles(t)
	code := run([]string{"-minutes", "0"}, stdout, stderr)
	if code != 2 {
		t.Fatalf("expected usage error exit code 2, got %d", code)
	}
}

func TestRunDetectsRestartAcrossTwoInvocations(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	stdout1, stderr1, read1 := captureFiles(t)
	if code := run([]string{"-minutes", "0.01", "-state", statePath}, stdout1, stderr1); code != 0 {
		t.Fatalf("first run: exit %d, stderr=%s", code, read1(stderr1))
	}
	first := read1(stdout1)
	if !bytes.Contains([]byte(first), []byte("resumed=false")) {
		t.Fatalf("expected the first invocation to report resumed=false, got: %s", first)
	}

	stdout2, stderr2, read2 := captureFiles(t)
	if code := run([]string{"-minutes", "0.01", "-state", statePath}, stdout2, stderr2); code != 0 {
		t.Fatalf("second run: exit %d, stderr=%s", code, read2(stderr2))
	}
	second := read2(stdout2)
	if !bytes.Contains([]byte(second), []byte("resumed=true")) {
		t.Fatalf("expected the second invocation to detect and report a resume, got: %s", second)
	}
	if !bytes.Contains([]byte(second), []byte("restart_count=1")) {
		t.Fatalf("expected restart_count=1 on the second invocation, got: %s", second)
	}
}

func TestRunInjectsFailureAtRequestedIteration(t *testing.T) {
	stdout, stderr, read := captureFiles(t)
	code := run([]string{"-minutes", "1", "-fail-at", "3", "-source-root", ""}, stdout, stderr)
	if code != 1 {
		t.Fatalf("expected exit code 1 for an injected failure, got %d; stdout=%s stderr=%s", code, read(stdout), read(stderr))
	}
	if !bytes.Contains([]byte(read(stderr)), []byte("injected failure at iteration 3")) {
		t.Fatalf("expected stderr to name the injected failure, got: %s", read(stderr))
	}
}

func TestRunCountsDroppedEventsInOutput(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "report.json")
	stdout, stderr, read := captureFiles(t)
	code := run([]string{"-minutes", "0.01", "-drop-every", "2", "-out", outPath}, stdout, stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%s", code, read(stderr))
	}
	raw, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	var report soak.Report
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if report.DroppedEvents == 0 {
		t.Fatal("expected -drop-every to produce at least one dropped event")
	}
	if report.Errors != 0 {
		t.Fatalf("expected dropped events to not count as real errors, got %d", report.Errors)
	}
}
