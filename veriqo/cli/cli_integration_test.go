package main_test

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"testing"
)

// TestCLI_BuildsAndRunsAsRealSubprocess proves the generated CLI is
// not just compilable but genuinely executable end-to-end, following
// this repo's own established pattern (test/integration/*) of
// building a real binary and invoking it via os/exec as a separate OS
// process rather than calling Go functions in-process.
func TestCLI_BuildsAndRunsAsRealSubprocess(t *testing.T) {
	bin := t.TempDir() + "/veriqo-cli"
	build := exec.Command("go", "build", "-o", bin, "./")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building CLI binary: %v\n%s", err, out)
	}

	run := exec.Command(bin, "trust", "evaluate",
		"--subject_id=cross-process-source",
		"--win_rate=0.9", "--contradiction_rate=0.05", "--tick=1")
	var stdout bytes.Buffer
	run.Stdout = &stdout
	if err := run.Run(); err != nil {
		t.Fatalf("running CLI: %v", err)
	}

	var got struct {
		SubjectID string
		Score     float64
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("parsing CLI JSON output: %v\nraw: %s", err, stdout.String())
	}
	if got.SubjectID != "cross-process-source" {
		t.Fatalf("want subject echoed back, got %q", got.SubjectID)
	}
	if got.Score <= 0.7 {
		t.Fatalf("want a high trust score for good evidence, got %v", got.Score)
	}
}

func TestCLI_UnknownCommandExitsNonZero(t *testing.T) {
	bin := t.TempDir() + "/veriqo-cli"
	build := exec.Command("go", "build", "-o", bin, "./")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building CLI binary: %v\n%s", err, out)
	}
	run := exec.Command(bin, "nonexistent", "verb")
	if err := run.Run(); err == nil {
		t.Fatal("expected a non-zero exit for an unknown engine/method")
	}
}
