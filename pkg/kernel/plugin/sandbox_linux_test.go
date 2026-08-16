//go:build linux

package plugin

import (
	"context"
	"errors"
	"net"
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

// nsReportPluginSource reports what the sandboxed process itself
// observes about its own process and network namespace -- the ground
// truth for proving CLONE_NEWPID/CLONE_NEWNET isolation actually took
// effect, not merely that the flags were set.
const nsReportPluginSource = `package main

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
)

type req struct{}
type resp struct {
	PID        int
	Interfaces []string
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	var names []string
	if ifaces, err := net.Interfaces(); err == nil {
		for _, i := range ifaces {
			names = append(names, i.Name)
		}
	}
	out, _ := json.Marshal(resp{PID: os.Getpid(), Interfaces: names})
	os.Stdout.Write(append(out, '\n'))
}
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

// TestSandboxRunner_NamespaceIsolation_ChildIsPID1WithNoHostInterfaces
// is the ground-truth proof for P1-05: the sandboxed plugin, from ITS
// OWN perspective (not this test's assumption), must observe itself as
// PID 1 of a fresh process namespace and must NOT see this host's real
// network interfaces -- both properties a naive "just exec it" runner
// (the pre-P1-05 behaviour) cannot provide, since a plain child process
// shares the host's PID and network namespace.
func TestSandboxRunner_NamespaceIsolation_ChildIsPID1WithNoHostInterfaces(t *testing.T) {
	runner := NewSandboxRunner("veriqo-plugin-test")
	if !runner.Available() {
		t.Skip("cgroup v1 not writable in this environment")
	}
	bin := buildHelperBinary(t, "nsreportplugin", nsReportPluginSource)

	hostIfaces, err := net.Interfaces()
	if err != nil {
		t.Fatalf("host net.Interfaces: %v", err)
	}

	var resp struct {
		PID        int
		Interfaces []string
	}
	if err := runner.RunOnce(context.Background(), "nsreport-holder", bin,
		SandboxLimits{MemoryBytes: 64 * 1024 * 1024, CPUCores: 1, Timeout: 5 * time.Second},
		struct{}{}, &resp); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if resp.PID != 1 {
		t.Fatalf("sandboxed plugin must be PID 1 of its own process namespace, observed PID %d -- "+
			"CLONE_NEWPID isolation did not take effect", resp.PID)
	}
	for _, name := range resp.Interfaces {
		if name != "lo" {
			t.Fatalf("sandboxed plugin must not see any host network interface, observed %q -- "+
				"CLONE_NEWNET isolation did not take effect (host had %d interfaces: %v)",
				name, len(hostIfaces), hostIfaces)
		}
	}
}

// nnpReportPluginSource reports what the sandboxed process itself
// observes about its own PR_SET_NO_NEW_PRIVS status (read from
// /proc/self/status, the kernel's own ground truth for this bit) --
// proof ShimPath's prctl call actually took effect inside the plugin
// process, not merely that the shim binary ran without erroring.
const nnpReportPluginSource = `package main

import (
	"bufio"
	"encoding/json"
	"os"
	"strconv"
	"strings"
)

type req struct{}
type resp struct{ NoNewPrivs int }

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	f, _ := os.Open("/proc/self/status")
	defer f.Close()
	s := bufio.NewScanner(f)
	val := -1
	for s.Scan() {
		line := s.Text()
		if strings.HasPrefix(line, "NoNewPrivs:") {
			fields := strings.Fields(line)
			if len(fields) == 2 {
				val, _ = strconv.Atoi(fields[1])
			}
		}
	}
	out, _ := json.Marshal(resp{NoNewPrivs: val})
	os.Stdout.Write(append(out, '\n'))
}
`

func buildShimBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "veriqo-plugin-shim")
	cmd := exec.Command("go", "build", "-o", bin, "veriqo/cmd/veriqo-plugin-shim")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("building veriqo-plugin-shim: %v\n%s", err, out)
	}
	return bin
}

// TestSandboxRunner_ShimAppliesNoNewPrivs is the sandbox-hardening
// follow-up's real proof: with ShimPath set, a sandboxed plugin
// observes PR_SET_NO_NEW_PRIVS=1 on itself -- the kernel's own ground
// truth that the shim's prctl call took effect on the process the
// plugin's real code actually runs as, not merely on some intermediate
// process that then got replaced without the bit surviving.
func TestSandboxRunner_ShimAppliesNoNewPrivs(t *testing.T) {
	runner := NewSandboxRunner("veriqo-plugin-test")
	if !runner.Available() {
		t.Skip("cgroup v1 not writable in this environment")
	}
	runner.ShimPath = buildShimBinary(t)
	bin := buildHelperBinary(t, "nnpreportplugin", nnpReportPluginSource)

	var resp struct{ NoNewPrivs int }
	if err := runner.RunOnce(context.Background(), "nnpreport-holder", bin,
		SandboxLimits{MemoryBytes: 64 * 1024 * 1024, CPUCores: 1, Timeout: 5 * time.Second},
		struct{}{}, &resp); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if resp.NoNewPrivs != 1 {
		t.Fatalf("expected the sandboxed plugin to observe NoNewPrivs=1 via the shim, got %d", resp.NoNewPrivs)
	}
}

// TestSandboxRunner_WithoutShimPathPreservesPriorBehavior is the nil-
// safety property: RunOnce with ShimPath left empty (every caller
// before this field existed) still execs the plugin directly, with no
// NO_NEW_PRIVS guarantee -- exactly the behavior this repo's own prior,
// already-passing sandbox tests assume.
func TestSandboxRunner_WithoutShimPathPreservesPriorBehavior(t *testing.T) {
	runner := NewSandboxRunner("veriqo-plugin-test")
	if !runner.Available() {
		t.Skip("cgroup v1 not writable in this environment")
	}
	bin := buildHelperBinary(t, "nnpreportplugin2", nnpReportPluginSource)

	var resp struct{ NoNewPrivs int }
	if err := runner.RunOnce(context.Background(), "nnpreport-holder2", bin,
		SandboxLimits{MemoryBytes: 64 * 1024 * 1024, CPUCores: 1, Timeout: 5 * time.Second},
		struct{}{}, &resp); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if resp.NoNewPrivs != 0 {
		t.Fatalf("expected NoNewPrivs=0 without ShimPath set (unaffected default behavior), got %d", resp.NoNewPrivs)
	}
}
