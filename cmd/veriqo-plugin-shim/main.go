//go:build linux

// Command veriqo-plugin-shim is the minimal, real, zero-dependency
// privilege-escalation and syscall-filtering guard for sandboxed plugin
// execution (real sandbox hardening: PR_SET_NO_NEW_PRIVS always, plus a
// real, hand-encoded seccomp-BPF denylist filter when -seccomp is
// passed).
//
// PR_SET_NO_NEW_PRIVS (always applied): pkg/kernel/plugin's
// SandboxRunner.RunOnce already runs a plugin binary in its own
// PID/mount/UTS/IPC/net namespaces and under a cgroup memory/CPU
// ceiling, but the plugin process itself could still, if it execs a
// setuid/setgid binary or one with file capabilities, GAIN privileges
// it did not start with. Go's standard library syscall.SysProcAttr has
// no field for this (confirmed: `go doc syscall.SysProcAttr` lists
// Chroot/Credential/Cloneflags/AmbientCaps/etc. but nothing that maps
// to it), and applying prctl() to a child BEFORE it execs the real
// target is not something os/exec exposes a hook for at all -- Go
// deliberately does not let a caller run arbitrary code between fork
// and exec in the child. The one correct, standard way to apply a
// pre-exec security attribute in Go is exactly what this binary is: a
// tiny, single-purpose, statically-built shim whose own main() runs
// the security-sensitive syscalls on a locked OS thread and then execs
// into the real target.
//
// seccomp-BPF (-seccomp flag, opt-in): a real, hand-encoded classic-BPF
// DENYLIST program -- default SECCOMP_RET_ALLOW, with a small, fixed
// set of syscalls (see deniedSyscalls below) returning EPERM instead.
// This is deliberately NOT an allowlist: an allowlist wrong in either
// direction either leaves a real hole (missing a dangerous syscall) or
// silently breaks a legitimate plugin (omitting a syscall it actually
// needs) -- and with zero prior art for BPF encoding in this codebase,
// getting an allowlist wrong in the second direction was judged too
// risky in an earlier round. A denylist inverts that risk profile: the
// failure mode of an entry this list is missing is "not yet more
// restrictive than today", never "breaks a legitimate plugin", because
// every syscall not explicitly named still defaults to ALLOW. The
// syscalls named below are real container/sandbox-escape and host-
// tampering primitives (kernel module loading, mount/pivot_root,
// ptrace, raw I/O port access, host clock/hostname tampering, kernel
// module and BPF loading, namespace escape) that a legitimate
// analytics/forecast/risk plugin -- the stated use case in
// sandbox_linux.go's own doc comment -- has no reason to call. Full
// filesystem confinement (chroot/pivot_root into a minimal rootfs) and
// a complete allowlist-style seccomp profile remain genuinely open, for
// the same reasons already stated in sandbox_linux.go: hand-authoring
// either correctly for an arbitrary Go/native plugin binary without
// over- or under-restricting it is real, separable, higher-risk work.
package main

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

// prSetNoNewPrivs is Linux's PR_SET_NO_NEW_PRIVS prctl() option (38 in
// every mainline kernel since 3.5 -- an ABI-stable constant, not a
// build-specific header value; golang.org/x/sys/unix defines the same
// literal). prSetSeccomp is PR_SET_SECCOMP (22). seccompModeFilter is
// SECCOMP_MODE_FILTER (2). Declared locally so this binary needs
// nothing beyond the standard library's own syscall package (this
// repository's zero_dependency gate forbids adding golang.org/x/sys as
// a real module dependency even though it happens to be cached in this
// sandbox).
const (
	prSetNoNewPrivs   = 38
	prSetSeccomp      = 22
	seccompModeFilter = 2
)

// Classic BPF opcodes and seccomp verdicts, all stable Linux UAPI
// constants (linux/filter.h, linux/seccomp.h, linux/audit.h) -- values
// confirmed against this sandbox's own /usr/include/x86_64-linux-gnu
// headers, not guessed.
const (
	// bpfLdWAbs = BPF_LD(0x00) | BPF_W(0x00) | BPF_ABS(0x20): load a word
	// from the fixed offset in the (implicit) seccomp_data buffer.
	bpfLdWAbs = 0x20
	// bpfJmpJeqK = BPF_JMP(0x05) | BPF_JEQ(0x10) | BPF_K(0x00): compare
	// the accumulator against an immediate constant.
	bpfJmpJeqK = 0x15
	// bpfRetK = BPF_RET(0x06) | BPF_K(0x00): return an immediate verdict.
	bpfRetK = 0x06

	seccompRetKillProcess = 0x80000000
	seccompRetErrno       = 0x00050000
	seccompRetAllow       = 0x7fff0000

	// auditArchX86_64 = AUDIT_ARCH_X86_64 = EM_X86_64(62) |
	// __AUDIT_ARCH_64BIT(0x80000000) | __AUDIT_ARCH_LE(0x40000000).
	// Rejecting any other reported arch (e.g. a 32-bit syscall-ABI
	// entry point) closes the classic seccomp bypass where a filter
	// written only for one ABI is circumvented via another.
	auditArchX86_64 = 0xC000003E
)

// deniedSyscalls are real container/sandbox-escape and host-tampering
// primitives, by x86_64 syscall number (from this sandbox's own
// /usr/include/x86_64-linux-gnu/asm/unistd_64.h, not a remembered
// list): ptrace(101) debugging/injection; pivot_root(155) mount(165)
// umount2(166) filesystem-namespace escape; acct(163)
// settimeofday(164) clock_settime(227) clock_adjtime(305)
// adjtimex(159) host-clock/accounting tampering; swapon(167)
// swapoff(168) host resource tampering; reboot(169) host disruption;
// iopl(172) ioperm(173) raw I/O port access; init_module(175)
// finit_module(313) delete_module(176) kexec_load(246)
// kexec_file_load(320) kernel code loading -- immediate host
// compromise; quotactl(179) filesystem quota tampering; unshare(272)
// setns(308) namespace manipulation/escape; bpf(321) further sandbox
// bypass; perf_event_open(298) kernel-info/side-channel exposure.
var deniedSyscalls = []int64{
	101, 155, 165, 166, 163, 164, 227, 305, 159, 167, 168, 169, 172, 173,
	175, 313, 176, 246, 320, 179, 272, 308, 321, 298,
}

// sockFilter mirrors struct sock_filter (linux/filter.h) byte-for-byte:
// u16 code; u8 jt; u8 jf; u32 k -- 8 bytes, naturally aligned, no
// padding.
type sockFilter struct {
	code uint16
	jt   uint8
	jf   uint8
	k    uint32
}

// sockFprog mirrors struct sock_fprog (linux/filter.h): unsigned short
// len; struct sock_filter *filter.
type sockFprog struct {
	len    uint16
	_      [6]byte // padding to align the pointer field on amd64
	filter *sockFilter
}

// buildSeccompFilter constructs the denylist program described above.
// Layout (verified by hand, instruction by instruction, against
// classic-BPF jump semantics -- jt/jf are relative to the PC
// immediately after the jump instruction):
//
//	[0] LD  W ABS k=4                 ; A = seccomp_data.arch
//	[1] JEQ auditArchX86_64 jt=1 jf=0 ; match -> skip KILL; else fall through
//	[2] RET KILL_PROCESS              ; wrong syscall ABI -> kill
//	[3] LD  W ABS k=0                 ; A = seccomp_data.nr
//	[4..4+N-1] one JEQ per denied syscall:
//	    i < N-1: jt = N-1-i (skip remaining checks, land on DENY), jf=0 (check next)
//	    i = N-1: jt = 0 (fall straight into DENY), jf = 1 (skip DENY, land on ALLOW)
//	[4+N]   RET ERRNO(EPERM)          ; DENY
//	[5+N]   RET ALLOW                 ; default: every other syscall
func buildSeccompFilter() []sockFilter {
	n := len(deniedSyscalls)
	prog := make([]sockFilter, 0, 6+n)
	prog = append(prog,
		sockFilter{code: bpfLdWAbs, k: 4},
		sockFilter{code: bpfJmpJeqK, k: auditArchX86_64, jt: 1, jf: 0},
		sockFilter{code: bpfRetK, k: seccompRetKillProcess},
		sockFilter{code: bpfLdWAbs, k: 0},
	)
	for i, nr := range deniedSyscalls {
		var jt, jf uint8
		if i < n-1 {
			jt, jf = uint8(n-1-i), 0 // #nosec G115 -- n-1-i is bounded by len(deniedSyscalls) (~24), far below uint8's 255 max
		} else {
			jt, jf = 0, 1
		}
		prog = append(prog, sockFilter{code: bpfJmpJeqK, k: uint32(nr), jt: jt, jf: jf}) // #nosec G115 -- nr is one of this file's own deniedSyscalls constants (small, fixed x86_64 syscall numbers, all well under uint32's range), never externally supplied
	}
	prog = append(prog,
		sockFilter{code: bpfRetK, k: seccompRetErrno | 1}, // 1 = EPERM
		sockFilter{code: bpfRetK, k: seccompRetAllow},
	)
	return prog
}

func main() {
	os.Exit(run(os.Args))
}

func run(args []string) int {
	rest := args[1:]
	applySeccomp := false
	if len(rest) > 0 && rest[0] == "-seccomp" {
		applySeccomp = true
		rest = rest[1:]
	}
	if len(rest) < 1 {
		fmt.Fprintln(os.Stderr, "veriqo-plugin-shim: usage: veriqo-plugin-shim [-seccomp] <real-binary> [args...]")
		return 2
	}
	target := rest[0]
	targetArgv := rest // argv[0] for the real binary is its own path, matching normal exec conventions

	// LockOSThread BEFORE any prctl call, and never unlock: both
	// NO_NEW_PRIVS and a seccomp filter are per-THREAD attributes
	// (inherited by the whole process only once execve collapses
	// every other thread away at the syscall.Exec call below), so the
	// same real OS thread must make every call here -- exactly the
	// precondition syscall.SysProcAttr's own Ptrace field doc comment
	// names for the analogous ptrace case.
	runtime.LockOSThread()

	if _, _, errno := syscall.Syscall(syscall.SYS_PRCTL, uintptr(prSetNoNewPrivs), 1, 0); errno != 0 {
		fmt.Fprintf(os.Stderr, "veriqo-plugin-shim: prctl(PR_SET_NO_NEW_PRIVS): %v\n", errno)
		return 1
	}

	if applySeccomp {
		prog := buildSeccompFilter()
		fprog := sockFprog{len: uint16(len(prog)), filter: &prog[0]} // #nosec G115 -- len(prog) is 6+len(deniedSyscalls) (~30 instructions), far below uint16's 65535 max
		// PR_SET_SECCOMP with SECCOMP_MODE_FILTER, passing a pointer to
		// fprog as the 3rd prctl argument -- valid here (not after
		// NO_NEW_PRIVS is lost) because NO_NEW_PRIVS was just set above,
		// which is exactly what lets an unprivileged process install a
		// seccomp filter without CAP_SYS_ADMIN. unsafe.Pointer is the only
		// way to pass a raw sock_fprog struct pointer to prctl(2); fprog
		// and its backing prog slice are both stack/heap values owned by
		// this function and kept alive by the closure over fprog for the
		// duration of this single syscall, so there is no lifetime risk.
		if _, _, errno := syscall.Syscall(syscall.SYS_PRCTL, uintptr(prSetSeccomp),
			uintptr(seccompModeFilter), uintptr(unsafe.Pointer(&fprog))); errno != 0 { // #nosec G103 -- see comment above
			fmt.Fprintf(os.Stderr, "veriqo-plugin-shim: prctl(PR_SET_SECCOMP): %v\n", errno)
			return 1
		}
	}

	// syscall.Exec replaces this process image entirely (true execve,
	// not fork+exec) -- the real target binary becomes THIS process,
	// under the same PID, inheriting the NO_NEW_PRIVS bit and (if set)
	// the seccomp filter just installed, exactly as SandboxRunner's
	// caller (which already put THIS process's PID into a cgroup and
	// namespaces before writing its stdin) expects.
	env := os.Environ()
	if err := syscall.Exec(target, targetArgv, env); err != nil { // #nosec G204 G702 -- target is the resolved plugin binary path SandboxRunner (the operator's own trusted caller) supplied as this shim's own argv, not untrusted network input
		fmt.Fprintf(os.Stderr, "veriqo-plugin-shim: exec %s: %v\n", target, err)
		return 1
	}
	// syscall.Exec never returns on success.
	return 0
}
