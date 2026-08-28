//go:build linux

package resource

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// memhogSource is a standalone Go program (built to a temp binary at test
// time) that allocates and *touches* anonymous memory in a growing,
// never-freed slice. Unlike writing to a disk-backed file (whose pages
// the kernel can reclaim as clean page cache under pressure, never
// triggering the cgroup OOM killer), touched anonymous pages cannot be
// reclaimed without either swapping (typically disabled in containers)
// or killing the process — which is exactly the enforcement this test
// needs to observe.
const memhogSource = `package main

import "time"

func main() {
	var hog [][]byte
	for {
		b := make([]byte, 1<<20) // 1MB
		for i := range b {
			b[i] = 1
		}
		hog = append(hog, b)
		time.Sleep(5 * time.Millisecond)
	}
}
`

func buildMemhog(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "memhog.go")
	if err := os.WriteFile(src, []byte(memhogSource), 0o644); err != nil {
		t.Fatalf("write memhog source: %v", err)
	}
	bin := filepath.Join(dir, "memhog")
	cmd := exec.Command("go", "build", "-o", bin, src)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("building memhog helper: %v\n%s", err, out)
	}
	return bin
}

// TestOSEnforcer_MemoryLimit_RealOOMKill is the concrete proof this is
// real OS-level enforcement, not accounting: it spawns an actual `sh`
// subprocess that intentionally grows its memory unboundedly, places
// THAT REAL PROCESS into a cgroup limited to 20MB, and asserts the
// kernel kills it — the same signal (SIGKILL via the OOM killer) a
// production container runtime relies on.
func TestOSEnforcer_MemoryLimit_RealOOMKill(t *testing.T) {
	e := NewOSEnforcer("veriqo-test")
	if !e.Available() {
		t.Skip("cgroup v1 memory hierarchy not writable in this environment (need root/CAP_SYS_ADMIN) — skipping real-enforcement test")
	}
	holder := "mem-hog"
	if err := e.CreateGroup(holder); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	defer e.RemoveGroup(holder)

	if err := e.SetMemoryLimitBytes(holder, 20*1024*1024); err != nil {
		t.Fatalf("SetMemoryLimitBytes: %v", err)
	}

	bin := buildMemhog(t)
	cmd := exec.Command(bin)
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting mem-hog process: %v", err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}()

	if err := e.AddPID(holder, cmd.Process.Pid); err != nil {
		t.Fatalf("AddPID: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected the memory-hog process to be killed by the kernel OOM killer, but it exited cleanly")
		}
		t.Logf("cgroup memory limit enforced by the kernel: process was killed as expected (%v)", err)
	case <-time.After(20 * time.Second):
		t.Fatal("memory-hog process was not killed within timeout — cgroup limit was not enforced")
	}
}

func TestOSEnforcer_CreateSetRemove(t *testing.T) {
	e := NewOSEnforcer("veriqo-test")
	if !e.Available() {
		t.Skip("cgroup v1 not writable in this environment")
	}
	holder := "quota-check"
	if err := e.CreateGroup(holder); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	defer e.RemoveGroup(holder)

	if err := e.SetMemoryLimitBytes(holder, 64*1024*1024); err != nil {
		t.Fatalf("SetMemoryLimitBytes: %v", err)
	}
	if err := e.SetCPUQuota(holder, 0.5); err != nil {
		t.Fatalf("SetCPUQuota: %v", err)
	}
	usage, err := e.MemoryUsageBytes(holder)
	if err != nil {
		t.Fatalf("MemoryUsageBytes: %v", err)
	}
	t.Logf("empty cgroup baseline memory usage: %d bytes", usage)
}
