//go:build linux

// Session update (Resource Manager — real cgroups): the original
// resource.Manager was honestly scoped as accounting-only ("does NOT
// enforce real OS-level limits"). This file adds that missing layer for
// Linux: real cgroup v1 (memory + cpu) control groups, created,
// configured, and torn down via direct filesystem operations under
// /sys/fs/cgroup — the same mechanism Docker and Kubernetes use, without
// any external dependency. It is deliberately a *separate*, optional
// layer: Manager's in-process accounting still runs identically whether
// or not OSEnforcer is used, so callers who don't have (or don't want)
// cgroup access are unaffected.
//
// Honest scope: this targets cgroup v1 (memory + cpuacct/cpu
// controllers under /sys/fs/cgroup/{memory,cpu}), which is what this
// sandbox and most current container hosts expose; a cgroup v2
// (unified hierarchy) implementation would use a different, single-path
// file set (memory.max, cpu.max) and is a mechanical follow-up, not
// included here. Requires root or CAP_SYS_ADMIN-equivalent access to
// /sys/fs/cgroup, same as any container runtime.
package resource

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

const (
	memoryRoot = "/sys/fs/cgroup/memory"
	cpuRoot    = "/sys/fs/cgroup/cpu"
	pidsRoot   = "/sys/fs/cgroup/pids"
)

// OSEnforcer creates and manages real Linux cgroup v1 control groups,
// one per holder, translating resource.Kind grants into actual kernel
// enforcement (OOM-kill on memory overrun, CFS throttling on CPU
// overrun) instead of only accounting.
type OSEnforcer struct {
	prefix string // cgroup name prefix, e.g. "veriqo"
}

// NewOSEnforcer returns an enforcer that creates cgroups named
// "<prefix>/<holder>" under the memory and cpu v1 hierarchies. It does
// not touch the filesystem until CreateGroup is called.
func NewOSEnforcer(prefix string) *OSEnforcer {
	if prefix == "" {
		prefix = "veriqo"
	}
	return &OSEnforcer{prefix: prefix}
}

// Available reports whether this process can actually create and write
// cgroups — false in most unprivileged sandboxes/CI runners, so callers
// can fall back to accounting-only enforcement without erroring.
//
// Gap-closure note: this previously probed a fixed path
// (memoryRoot/"<prefix>-probe") shared by every caller using the same
// prefix. SandboxRunner.RunOnce calls Available() once per invocation,
// so concurrent invocations under the same holder-prefix raced on
// mkdir/rmdir of that identical directory — under load this could
// spuriously return false (ErrSandboxNoLimits) even though enforcement
// was genuinely available, silently downgrading a legitimate plugin
// call. Fixed by probing a uniquely-named directory per call
// (os.MkdirTemp) so concurrent callers never touch the same path.
func (e *OSEnforcer) Available() bool {
	testDir, err := os.MkdirTemp(memoryRoot, e.prefix+"-probe-*")
	if err != nil {
		return false
	}
	_ = os.Remove(testDir)
	return true
}

// CreateGroup creates the cgroup directories for holder under both the
// memory and cpu v1 hierarchies. Safe to call more than once (idempotent).
func (e *OSEnforcer) CreateGroup(holder string) error {
	for _, root := range []string{memoryRoot, cpuRoot, pidsRoot} {
		dir := filepath.Join(root, e.prefix, holder)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("resource: create cgroup dir %s: %w", dir, err)
		}
	}
	return nil
}

// SetMemoryLimitBytes writes memory.limit_in_bytes for holder's cgroup —
// the kernel will OOM-kill any process added to this group (via AddPID)
// that tries to exceed this many bytes of resident memory.
func (e *OSEnforcer) SetMemoryLimitBytes(holder string, limitBytes uint64) error {
	path := filepath.Join(memoryRoot, e.prefix, holder, "memory.limit_in_bytes")
	return os.WriteFile(path, []byte(strconv.FormatUint(limitBytes, 10)), 0o600)
}

// SetCPUQuota sets a hard CPU limit for holder's cgroup as a fraction of
// one core (e.g. 0.5 = half a core), using the CFS bandwidth controller
// (cpu.cfs_quota_us / cpu.cfs_period_us) — real kernel-level throttling,
// not a cooperative/best-effort limit.
func (e *OSEnforcer) SetCPUQuota(holder string, cores float64) error {
	const periodUs = 100000 // 100ms, the common default period
	quotaUs := int64(cores * periodUs)
	dir := filepath.Join(cpuRoot, e.prefix, holder)
	if err := os.WriteFile(filepath.Join(dir, "cpu.cfs_period_us"), []byte(strconv.Itoa(periodUs)), 0o600); err != nil {
		return fmt.Errorf("resource: set cpu.cfs_period_us: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cpu.cfs_quota_us"), []byte(strconv.FormatInt(quotaUs, 10)), 0o600); err != nil {
		return fmt.Errorf("resource: set cpu.cfs_quota_us: %w", err)
	}
	return nil
}

// AddPID moves an already-running process into holder's cgroups (both
// memory and cpu), subjecting it to the configured limits from this
// point forward. This is the step that turns configuration into actual
// enforcement — a process not added to the group is not limited.
func (e *OSEnforcer) AddPID(holder string, pid int) error {
	for _, root := range []string{memoryRoot, cpuRoot, pidsRoot} {
		path := filepath.Join(root, e.prefix, holder, "cgroup.procs")
		if err := os.WriteFile(path, []byte(strconv.Itoa(pid)), 0o600); err != nil {
			return fmt.Errorf("resource: add pid %d to %s: %w", pid, path, err)
		}
	}
	return nil
}

// SetPIDsLimit caps the number of processes/threads holder's cgroup may
// fork — real kernel-enforced fork-bomb protection (cgroup v1 `pids`
// controller), not merely a memory ceiling.
func (e *OSEnforcer) SetPIDsLimit(holder string, max int) error {
	dir := filepath.Join(pidsRoot, e.prefix, holder)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("resource: create pids cgroup dir: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, "pids.max"), []byte(strconv.Itoa(max)), 0o600)
}

// MemoryUsageBytes reads the live (kernel-reported, not self-declared)
// current memory usage of holder's cgroup.
func (e *OSEnforcer) MemoryUsageBytes(holder string) (uint64, error) {
	path := filepath.Join(memoryRoot, e.prefix, holder, "memory.usage_in_bytes")
	b, err := os.ReadFile(path) // #nosec G304 -- path is built from this process's own cgroup prefix/holder state, not external input
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(string(trimNewline(b)), 10, 64)
}

// RemoveGroup deletes holder's cgroup directories. The kernel refuses to
// rmdir a cgroup that still has member processes, so callers must first
// ensure the process has exited or been moved out.
func (e *OSEnforcer) RemoveGroup(holder string) error {
	var firstErr error
	for _, root := range []string{memoryRoot, cpuRoot, pidsRoot} {
		dir := filepath.Join(root, e.prefix, holder)
		if err := os.Remove(dir); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func trimNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}
