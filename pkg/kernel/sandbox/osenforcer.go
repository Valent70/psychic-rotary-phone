package sandbox

// PHASE J (P1-15) — Sandbox Hardening, the consolidation half.
//
// This is explicitly NOT about suppressing gosec findings with #nosec.
// It closes the specific structural gap a prior round's own audit
// recorded and deliberately left open
// (docs/governance/AUDITOR_PRIORITY_RECONCILIATION.md, R19 addendum):
//
//	"...both of those live in cmd/veriqo-plugin-shim, a separate
//	 command with its own hand-rolled enforcement for its own specific
//	 plugin-execution use case. pkg/kernel/sandbox -- R-028's actual
//	 owner package -- still has exactly two Enforcer implementations,
//	 InProcessEnforcer (which explicitly REFUSES policies requiring
//	 process spawning or syscall filtering) and UnenforceableEnforcer
//	 (no enforcement at all). Neither is a real OS-level
//	 seccomp/namespace enforcer, and neither wires in
//	 cmd/veriqo-plugin-shim's BPF work as a pluggable Enforcer."
//
// So the real confinement work is real, and it was simply not
// reachable through the interface this package defines. OSEnforcer
// below is that missing implementation. It does not reimplement any
// confinement primitive: the namespaces come from
// pkg/kernel/plugin.SandboxRunner's CLONE_NEWPID/NEWNS/NEWUTS/NEWIPC/
// NEWNET, the seccomp-BPF denylist and PR_SET_NO_NEW_PRIVS come from
// cmd/veriqo-plugin-shim, and the cgroup v2 limits come from
// pkg/kernel/resource.OSEnforcer. What OSEnforcer adds is the honest
// mapping from a declared Policy clause to the primitive that would
// have to be present to enforce it, plus a runtime probe of which
// primitives this kernel actually offers.
//
// The architecture chosen, per the program's instruction to pick one:
// an OCI-shaped process boundary — read-only view of the host
// filesystem outside declared paths, dropped capabilities via
// no-new-privs, a seccomp filter, a network namespace, a PID
// namespace, and cgroup v2 limits. AppArmor/SELinux are probed and
// reported but never required, because they are distribution-dependent
// and demanding them would make the enforcer unusable on kernels that
// are otherwise perfectly capable.
//
// Anti-false-green discipline: Probe reports what the RUNNING kernel
// offers, read from /proc and /sys rather than assumed. A primitive
// that cannot be confirmed is reported absent, and a policy needing it
// is refused — CanEnforce never returns true on an assumption. The
// ceiling this package can reach on its own is stated by
// ConfinementQualification and is INTERNAL_QUALIFIED, never VERIFIED:
// proving real confinement requires executing a hostile binary under
// the enforcer on a production kernel, which is an operational act.

import (
	"os"
	"runtime"
	"sort"
	"strings"
)

// Primitive names one kernel confinement mechanism.
type Primitive string

const (
	// PrimitiveSeccomp is seccomp-BPF syscall filtering.
	PrimitiveSeccomp Primitive = "seccomp"
	// PrimitiveNoNewPrivs is PR_SET_NO_NEW_PRIVS, which is what stops a
	// confined process regaining privilege by exec-ing a setuid binary.
	PrimitiveNoNewPrivs Primitive = "no_new_privs"
	// PrimitivePIDNamespace isolates process visibility.
	PrimitivePIDNamespace Primitive = "pid_namespace"
	// PrimitiveMountNamespace gives a private mount table, so a mount
	// inside the sandbox never propagates to the host.
	PrimitiveMountNamespace Primitive = "mount_namespace"
	// PrimitiveNetworkNamespace starts the process in a fresh network
	// namespace with no configured interfaces.
	PrimitiveNetworkNamespace Primitive = "network_namespace"
	// PrimitiveUserNamespace remaps UIDs/GIDs. Probed, and honestly NOT
	// currently used by pkg/kernel/plugin -- see ConfinementQualification.
	PrimitiveUserNamespace Primitive = "user_namespace"
	// PrimitiveCgroupV2 is the resource-limit mechanism.
	PrimitiveCgroupV2 Primitive = "cgroup_v2"
	// PrimitiveReadOnlyRootfs is a read-only root filesystem view.
	// Probed and reported; pkg/kernel/plugin does NOT currently
	// pivot_root, which its own doc comment states plainly.
	PrimitiveReadOnlyRootfs Primitive = "read_only_rootfs"
	// PrimitiveMAC is a mandatory access control layer (AppArmor or
	// SELinux). Reported, never required.
	PrimitiveMAC Primitive = "mandatory_access_control"
)

// AllPrimitives is the declared inventory, so a test can enumerate what
// must be probed rather than hardcoding a list that could drift.
func AllPrimitives() []Primitive {
	return []Primitive{
		PrimitiveSeccomp, PrimitiveNoNewPrivs, PrimitivePIDNamespace,
		PrimitiveMountNamespace, PrimitiveNetworkNamespace, PrimitiveUserNamespace,
		PrimitiveCgroupV2, PrimitiveReadOnlyRootfs, PrimitiveMAC,
	}
}

// EscapeVector names one thing a confined process must not be able to
// do. These are exactly the seven the program enumerates, and the
// negative test suite has one test per vector.
type EscapeVector string

const (
	EscapeHostFilesystem      EscapeVector = "host_filesystem_access"
	EscapeHostNetwork         EscapeVector = "host_network_access"
	EscapePrivilegeEscalation EscapeVector = "privilege_escalation"
	EscapeSecretMount         EscapeVector = "secret_mount_access"
	EscapeForbiddenSyscall    EscapeVector = "forbidden_syscall"
	EscapeCgroupEscape        EscapeVector = "cgroup_escape"
	EscapeNamespaceEscape     EscapeVector = "namespace_escape"
)

// AllEscapeVectors is the declared inventory the negative suite covers.
func AllEscapeVectors() []EscapeVector {
	return []EscapeVector{
		EscapeHostFilesystem, EscapeHostNetwork, EscapePrivilegeEscalation,
		EscapeSecretMount, EscapeForbiddenSyscall, EscapeCgroupEscape,
		EscapeNamespaceEscape,
	}
}

// requiredPrimitives maps each escape vector to the primitives that
// must ALL be present for the vector to be genuinely closed at the OS
// level. This table is the honest core of the enforcer: it is what
// makes "we block X" a checkable claim rather than an assertion.
var requiredPrimitives = map[EscapeVector][]Primitive{
	EscapeHostFilesystem:      {PrimitiveMountNamespace},
	EscapeHostNetwork:         {PrimitiveNetworkNamespace},
	EscapePrivilegeEscalation: {PrimitiveNoNewPrivs},
	EscapeSecretMount:         {PrimitiveMountNamespace},
	EscapeForbiddenSyscall:    {PrimitiveSeccomp},
	EscapeCgroupEscape:        {PrimitiveCgroupV2, PrimitiveMountNamespace},
	EscapeNamespaceEscape:     {PrimitivePIDNamespace, PrimitiveMountNamespace, PrimitiveSeccomp},
}

// Confinement is what a specific kernel actually offers, probed rather
// than assumed.
type Confinement struct {
	// Available maps each primitive to whether this kernel provides it.
	Available map[Primitive]bool `json:"available"`
	// Detail explains each answer, so an absent primitive is never an
	// unexplained "no".
	Detail map[Primitive]string `json:"detail"`
	// Platform is the GOOS this was probed on.
	Platform string `json:"platform"`
}

// Has reports whether a primitive is available.
func (c Confinement) Has(p Primitive) bool { return c.Available[p] }

// Missing returns every primitive this kernel does not offer, sorted.
func (c Confinement) Missing() []Primitive {
	var out []Primitive
	for _, p := range AllPrimitives() {
		if !c.Available[p] {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Closes reports whether every primitive a vector requires is present,
// and names the ones that are not.
func (c Confinement) Closes(v EscapeVector) (bool, []Primitive) {
	var missing []Primitive
	for _, p := range requiredPrimitives[v] {
		if !c.Available[p] {
			missing = append(missing, p)
		}
	}
	sort.Slice(missing, func(i, j int) bool { return missing[i] < missing[j] })
	return len(missing) == 0, missing
}

// Probe reads the running kernel to determine which primitives exist.
// Every answer comes from a real file read under /proc or /sys — never
// from a build tag, a GOOS check alone, or an assumption.
//
// On a non-Linux platform every primitive is reported absent with a
// stated reason, which is the truthful answer rather than a failure.
func Probe() Confinement {
	c := Confinement{
		Available: map[Primitive]bool{},
		Detail:    map[Primitive]string{},
		Platform:  runtime.GOOS,
	}
	for _, p := range AllPrimitives() {
		c.Available[p] = false
	}
	if runtime.GOOS != "linux" {
		for _, p := range AllPrimitives() {
			c.Detail[p] = "not probed: " + runtime.GOOS + " provides no Linux confinement primitive"
		}
		return c
	}

	// seccomp: the kernel exposes a Seccomp: line in /proc/self/status
	// on any kernel built with CONFIG_SECCOMP.
	if status, err := os.ReadFile("/proc/self/status"); err == nil { // #nosec G304 -- fixed, well-known kernel-exposed path
		if strings.Contains(string(status), "Seccomp:") {
			c.Available[PrimitiveSeccomp] = true
			c.Detail[PrimitiveSeccomp] = "/proc/self/status exposes a Seccomp field (CONFIG_SECCOMP is enabled)"
		} else {
			c.Detail[PrimitiveSeccomp] = "/proc/self/status has no Seccomp field; this kernel was built without CONFIG_SECCOMP"
		}
		if strings.Contains(string(status), "NoNewPrivs:") {
			c.Available[PrimitiveNoNewPrivs] = true
			c.Detail[PrimitiveNoNewPrivs] = "/proc/self/status exposes NoNewPrivs (PR_SET_NO_NEW_PRIVS is supported)"
		} else {
			c.Detail[PrimitiveNoNewPrivs] = "/proc/self/status has no NoNewPrivs field"
		}
	} else {
		c.Detail[PrimitiveSeccomp] = "cannot read /proc/self/status: " + err.Error()
		c.Detail[PrimitiveNoNewPrivs] = c.Detail[PrimitiveSeccomp]
	}

	// namespaces: /proc/self/ns/<kind> exists exactly when the kernel
	// supports that namespace type.
	for _, ns := range []struct {
		prim Primitive
		path string
	}{
		{PrimitivePIDNamespace, "/proc/self/ns/pid"},
		{PrimitiveMountNamespace, "/proc/self/ns/mnt"},
		{PrimitiveNetworkNamespace, "/proc/self/ns/net"},
		{PrimitiveUserNamespace, "/proc/self/ns/user"},
	} {
		if _, err := os.Stat(ns.path); err == nil {
			c.Available[ns.prim] = true
			c.Detail[ns.prim] = ns.path + " exists; this namespace type is supported"
		} else {
			c.Detail[ns.prim] = ns.path + " is absent: " + err.Error()
		}
	}

	// cgroup v2: the unified hierarchy exposes cgroup.controllers at its
	// root. cgroup v1 does not.
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err == nil {
		c.Available[PrimitiveCgroupV2] = true
		c.Detail[PrimitiveCgroupV2] = "/sys/fs/cgroup/cgroup.controllers exists; the unified (v2) hierarchy is mounted"
	} else {
		c.Detail[PrimitiveCgroupV2] = "/sys/fs/cgroup/cgroup.controllers is absent: " + err.Error() +
			" (cgroup v1, or no cgroup filesystem mounted)"
	}

	// read-only rootfs: reported from the mount table rather than
	// assumed. This is deliberately conservative -- a "ro" root mount
	// is what an OCI runtime's readonlyRootfs produces.
	if mounts, err := os.ReadFile("/proc/self/mounts"); err == nil { // #nosec G304 -- fixed, well-known kernel-exposed path
		c.Detail[PrimitiveReadOnlyRootfs] = "root mount is read-write in this process; pkg/kernel/plugin does not pivot_root today"
		for _, line := range strings.Split(string(mounts), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 4 && fields[1] == "/" {
				for _, opt := range strings.Split(fields[3], ",") {
					if opt == "ro" {
						c.Available[PrimitiveReadOnlyRootfs] = true
						c.Detail[PrimitiveReadOnlyRootfs] = "root mount is read-only"
					}
				}
			}
		}
	} else {
		c.Detail[PrimitiveReadOnlyRootfs] = "cannot read /proc/self/mounts: " + err.Error()
	}

	// MAC: AppArmor exposes /sys/kernel/security/apparmor; SELinux
	// exposes /sys/fs/selinux. Either satisfies the primitive.
	switch {
	case statOK("/sys/kernel/security/apparmor"):
		c.Available[PrimitiveMAC] = true
		c.Detail[PrimitiveMAC] = "AppArmor is present (/sys/kernel/security/apparmor)"
	case statOK("/sys/fs/selinux"):
		c.Available[PrimitiveMAC] = true
		c.Detail[PrimitiveMAC] = "SELinux is present (/sys/fs/selinux)"
	default:
		c.Detail[PrimitiveMAC] = "neither AppArmor nor SELinux is present; MAC is reported, never required"
	}
	return c
}

func statOK(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// OSEnforcer is the Enforcer implementation that reaches the real
// OS-level confinement this repository already builds. It is the
// missing wiring the R19 audit note named.
//
// It holds a Confinement rather than probing on every call, so a
// caller can construct one from a probe of the machine a plugin will
// ACTUALLY run on, which is not necessarily the machine deciding
// whether to run it.
type OSEnforcer struct {
	Confinement Confinement
	// ShimAvailable reports whether cmd/veriqo-plugin-shim is deployed
	// alongside. Without it, no-new-privs and seccomp are unreachable
	// even on a kernel that supports them: the primitives exist, but
	// nothing applies them. Defaulting this to false is deliberate --
	// assuming a binary is deployed is exactly the kind of unchecked
	// assumption this package refuses.
	ShimAvailable bool
}

// NewOSEnforcer probes the running kernel and returns an enforcer bound
// to what it found. shimPath is checked on disk; passing "" means no
// shim, which correctly makes syscall filtering unenforceable.
func NewOSEnforcer(shimPath string) OSEnforcer {
	e := OSEnforcer{Confinement: Probe()}
	if strings.TrimSpace(shimPath) != "" {
		if info, err := os.Stat(shimPath); err == nil && !info.IsDir() {
			e.ShimAvailable = true
		}
	}
	return e
}

// Name implements Enforcer.
func (e OSEnforcer) Name() string {
	var have []string
	for _, p := range AllPrimitives() {
		if e.Confinement.Has(p) {
			have = append(have, string(p))
		}
	}
	if len(have) == 0 {
		return "OS enforcer with no available confinement primitive"
	}
	return "OS enforcer (" + strings.Join(have, ", ") + ")"
}

// CanEnforce implements Enforcer. It refuses on an absent primitive
// rather than assuming one, and it names every gap so a denial is
// always explainable.
func (e OSEnforcer) CanEnforce(p Policy) (bool, []string) {
	var gaps []string

	if len(p.Syscalls) > 0 {
		if !e.Confinement.Has(PrimitiveSeccomp) {
			gaps = append(gaps, "syscall filtering: "+e.Confinement.Detail[PrimitiveSeccomp])
		} else if !e.ShimAvailable {
			gaps = append(gaps, "syscall filtering: this kernel supports seccomp but cmd/veriqo-plugin-shim "+
				"is not deployed, so nothing installs the filter")
		}
	}
	if p.AllowProcessSpawn {
		if !e.Confinement.Has(PrimitivePIDNamespace) {
			gaps = append(gaps, "process spawning: "+e.Confinement.Detail[PrimitivePIDNamespace])
		}
		if !e.Confinement.Has(PrimitiveNoNewPrivs) {
			gaps = append(gaps, "process spawning: no PR_SET_NO_NEW_PRIVS, so a spawned process could regain "+
				"privilege by exec-ing a setuid binary")
		} else if !e.ShimAvailable {
			gaps = append(gaps, "process spawning: this kernel supports PR_SET_NO_NEW_PRIVS but "+
				"cmd/veriqo-plugin-shim is not deployed, so nothing applies it")
		}
	}
	if len(p.NetworkDestinations) == 0 && !e.Confinement.Has(PrimitiveNetworkNamespace) {
		// A policy granting NO network is only genuinely enforced if the
		// process really has no network. Without a network namespace,
		// "no destinations declared" is a convention, not a boundary.
		gaps = append(gaps, "network isolation: "+e.Confinement.Detail[PrimitiveNetworkNamespace])
	}
	if (len(p.ReadPaths) > 0 || len(p.WritePaths) > 0) && !e.Confinement.Has(PrimitiveMountNamespace) {
		gaps = append(gaps, "filesystem confinement: "+e.Confinement.Detail[PrimitiveMountNamespace])
	}
	if p.Limits.MaxMemoryBytes > 0 && !e.Confinement.Has(PrimitiveCgroupV2) {
		gaps = append(gaps, "resource limits: "+e.Confinement.Detail[PrimitiveCgroupV2])
	}

	sort.Strings(gaps)
	return len(gaps) == 0, gaps
}

// VectorReport is one escape vector's status under this enforcer.
type VectorReport struct {
	Vector EscapeVector `json:"vector"`
	// Closed is true only when every required primitive is present AND,
	// where the primitive must be actively applied, the shim is
	// deployed to apply it.
	Closed  bool        `json:"closed"`
	Missing []Primitive `json:"missing_primitives,omitempty"`
	Reason  string      `json:"reason"`
}

// Vectors reports every escape vector's status. This is the artifact a
// readiness gate attaches: it says, vector by vector, what is actually
// closed on this machine and why anything else is not.
func (e OSEnforcer) Vectors() []VectorReport {
	out := make([]VectorReport, 0, len(AllEscapeVectors()))
	for _, v := range AllEscapeVectors() {
		closed, missing := e.Confinement.Closes(v)
		r := VectorReport{Vector: v, Closed: closed, Missing: missing}
		switch {
		case !closed:
			names := make([]string, len(missing))
			for i, m := range missing {
				names[i] = string(m)
			}
			r.Reason = "this kernel lacks: " + strings.Join(names, ", ")
		case needsShim(v) && !e.ShimAvailable:
			r.Closed = false
			r.Reason = "every required primitive is present, but cmd/veriqo-plugin-shim is not deployed, " +
				"so nothing applies it -- a supported primitive that nothing applies confines nothing"
		default:
			r.Reason = "every required primitive is present and applied"
		}
		out = append(out, r)
	}
	return out
}

// needsShim reports whether closing a vector requires the shim to
// actively apply a primitive (as opposed to the runner setting a clone
// flag, which pkg/kernel/plugin does directly).
func needsShim(v EscapeVector) bool {
	switch v {
	case EscapePrivilegeEscalation, EscapeForbiddenSyscall, EscapeNamespaceEscape:
		return true
	default:
		return false
	}
}

// ConfinementQualification states the honest ceiling for this package's
// own evidence, in the same shape pkg/platform/telemetry's
// PipelineQualification uses.
type ConfinementQualification struct {
	Status    string   `json:"status"`
	Proven    []string `json:"proven"`
	NotProven []string `json:"not_proven"`
	RaisedBy  string   `json:"raised_by"`
}

// Qualification reports what this package's tests establish and what
// they do not. INTERNAL_QUALIFIED is the ceiling: proving real
// confinement means running a genuinely hostile binary under the
// enforcer on a production kernel, which is an operational act no unit
// test performs.
func Qualification() ConfinementQualification {
	return ConfinementQualification{
		Status: "INTERNAL_QUALIFIED",
		Proven: []string{
			"the policy engine denies every escape vector's corresponding capability request by default",
			"the enforcer refuses a policy whose required primitive is absent, rather than assuming it",
			"a supported-but-unapplied primitive (kernel has seccomp, shim not deployed) is reported as NOT closed",
			"kernel primitive availability is read from /proc and /sys, never inferred from GOOS or a build tag",
		},
		NotProven: []string{
			"that a genuinely hostile binary is contained: that requires executing one under the enforcer",
			"full filesystem confinement (pivot_root into a minimal rootfs) -- pkg/kernel/plugin does not do this, " +
				"and its own doc comment says so",
			"user-namespace UID/GID remapping -- probed and reported, not currently applied",
			"an allowlist-style seccomp profile: the shipped filter is a denylist, by a documented and deliberate " +
				"risk trade-off recorded in cmd/veriqo-plugin-shim",
		},
		RaisedBy: "an adversarial execution drill on a production kernel: a hostile binary run under the enforcer, " +
			"attempting each vector, with the kernel's refusals captured as evidence",
	}
}
