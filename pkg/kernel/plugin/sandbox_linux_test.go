//go:build linux

package plugin

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"veriqo/pkg/platform/audit"
)

const echoPluginSource = `package main

import (
	"bufio"
	"encoding/json"
	"os"
)

type req struct{ Value int } 
type resp struct{ Doubled int }

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	var r req
	_ = json.Unmarshal(scanner.Bytes(), &r)
	out, _ := json.Marshal(resp{Doubled: r.Value * 2})
	os.Stdout.Write(append(out, '\n'))
}
`

const hangPluginSource = `package main
import "time"
func main() { time.Sleep(30 * time.Second) }
`

const memhogPluginSource = `package main
import "time"
func main() {
	var hog [][]byte
	for {
		b := make([]byte, 1<<20)
		for i := range b { b[i] = 1 }
		hog = append(hog, b)
		time.Sleep(2 * time.Millisecond)
	}
}
`

func buildHelperBinary(t *testing.T, name, source string) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, name+".go")
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatalf("write helper source: %v", err)
	}
	bin := filepath.Join(dir, name)
	cmd := exec.Command("go", "build", "-o", bin, src)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("building helper %s: %v\n%s", name, err, out)
	}
	return bin
}

func TestSandboxRunner_NormalRoundTrip(t *testing.T) {
	runner := NewSandboxRunner("veriqo-plugin-test")
	if !runner.Available() {
		t.Skip("cgroup v1 not writable in this environment")
	}
	runner.Audit = audit.NewAuditStore()
	bin := buildHelperBinary(t, "echoplugin", echoPluginSource)

	var resp struct{ Doubled int }
	err := runner.RunOnce(context.Background(), "echo-holder", bin,
		SandboxLimits{MemoryBytes: 64 * 1024 * 1024, CPUCores: 1, Timeout: 5 * time.Second},
		struct{ Value int }{Value: 21}, &resp)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if resp.Doubled != 42 {
		t.Fatalf("want 42, got %d", resp.Doubled)
	}

	records := runner.Audit.Snapshot()
	if len(records) != 2 {
		t.Fatalf("want 2 audit records (spawn+exit), got %d", len(records))
	}
	if records[0].Action != "spawn" || records[1].Action != "exit" {
		t.Fatalf("unexpected audit actions: %s, %s", records[0].Action, records[1].Action)
	}
	if err := (audit.Auditor{}).VerifyChain(records); err != nil {
		t.Fatalf("sandbox audit trail failed hash-chain verification: %v", err)
	}
}

func TestSandboxRunner_TimeoutKillsHungPlugin(t *testing.T) {
	runner := NewSandboxRunner("veriqo-plugin-test")
	if !runner.Available() {
		t.Skip("cgroup v1 not writable in this environment")
	}
	bin := buildHelperBinary(t, "hangplugin", hangPluginSource)

	var resp struct{}
	start := time.Now()
	err := runner.RunOnce(context.Background(), "hang-holder", bin,
		SandboxLimits{MemoryBytes: 64 * 1024 * 1024, Timeout: 1 * time.Second},
		struct{}{}, &resp)
	elapsed := time.Since(start)
	if err != ErrSandboxTimeout {
		t.Fatalf("want ErrSandboxTimeout, got %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("timeout enforcement took too long: %s", elapsed)
	}
}

func TestSandboxRunner_MemoryLimitKillsRunawayPlugin(t *testing.T) {
	runner := NewSandboxRunner("veriqo-plugin-test")
	if !runner.Available() {
		t.Skip("cgroup v1 not writable in this environment")
	}
	bin := buildHelperBinary(t, "memhogplugin", memhogPluginSource)

	var resp struct{}
	err := runner.RunOnce(context.Background(), "memhog-holder", bin,
		SandboxLimits{MemoryBytes: 20 * 1024 * 1024, Timeout: 15 * time.Second},
		struct{}{}, &resp)
	if !errors.Is(err, ErrSandboxCrashed) {
		t.Fatalf("want ErrSandboxCrashed (OOM-killed by cgroup), got %v", err)
	}
}
