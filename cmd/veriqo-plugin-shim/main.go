//go:build linux

// Command veriqo-plugin-shim is the minimal, real, zero-dependency
// privilege-escalation guard for sandboxed plugin execution (sandbox
// hardening follow-up, one bounded increment of the larger "seccomp-
// BPF, minimal rootfs, filesystem confinement" ask a later audit round
// named as still open).
//
// The gap this closes: pkg/kernel/plugin's SandboxRunner.RunOnce
// already runs a plugin binary in its own PID/mount/UTS/IPC/net
// namespaces and under a cgroup memory/CPU ceiling, but the plugin
// process itself could still, if it execs a setuid/setgid binary or one
// with file capabilities, GAIN privileges it did not start with. Go's
// standard library syscall.SysProcAttr has no field for
// PR_SET_NO_NEW_PRIVS (confirmed: `go doc syscall.SysProcAttr` lists
// Chroot/Credential/Cloneflags/AmbientCaps/etc. but nothing that maps
// to it), and applying prctl() to a child BEFORE it execs the real
// target is not something os/exec exposes a hook for at all -- Go
// deliberately does not let a caller run arbitrary code between fork
// and exec in the child (the child is single-threaded post-fork but the
// Go runtime is not fork-safe in general). The one correct, standard
// way to apply a pre-exec security attribute in Go is exactly what this
// binary is: a tiny, single-purpose, statically-built shim whose own
// main() runs the security-sensitive syscalls on a locked OS thread and
// then execs into the real target -- SandboxRunner launches THIS binary
// (with the real plugin path plus its arguments as veriqo-plugin-shim's
// own argv[1:]) instead of the plugin directly.
//
// This is deliberately NOT a seccomp-BPF syscall filter: hand-encoding
// a correct BPF allow/deny program (raw `struct sock_filter`/
// `sock_fprog` byte layout, BPF_LD/BPF_JMP/BPF_RET opcodes) without any
// existing prior art in this codebase, and with a failure mode where a
// subtly wrong filter silently breaks every syscall a legitimate plugin
// needs -- turning "no seccomp" into "seccomp that eats a plugin's exit
// syscall" -- is real, separable, higher-risk work this round does not
// attempt, exactly as pkg/kernel/plugin/sandbox_linux.go's own doc
// comment already stated. PR_SET_NO_NEW_PRIVS carries none of that risk
// by construction: it does not filter or deny any syscall a plugin
// makes; it only prevents a SUBSEQUENT exec from gaining privileges via
// setuid/setgid bits or file capabilities the plugin process did not
// already have. A plugin that never tries to exec a privileged binary
// is completely unaffected; one that does is refused the privilege
// escalation rather than silently granted it.
package main

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
)

// prSetNoNewPrivs is Linux's PR_SET_NO_NEW_PRIVS prctl() option (38 in
// every mainline kernel since 3.5 -- an ABI-stable constant, not a
// build-specific header value; golang.org/x/sys/unix defines the same
// literal). Declared locally so this binary needs nothing beyond the
// standard library's own syscall package (this repository's
// zero_dependency gate forbids adding golang.org/x/sys as a real module
// dependency even though it happens to be cached in this sandbox).
const prSetNoNewPrivs = 38

func main() {
	os.Exit(run(os.Args))
}

func run(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "veriqo-plugin-shim: usage: veriqo-plugin-shim <real-binary> [args...]")
		return 2
	}
	target := args[1]
	targetArgv := args[1:] // argv[0] for the real binary is its own path, matching normal exec conventions

	// LockOSThread BEFORE the prctl call, and never unlock: prctl's
	// NO_NEW_PRIVS bit is a per-THREAD attribute (inherited by the
	// whole process only once execve collapses every other thread away
	// at the syscall.Exec call below), so the same real OS thread must
	// make both calls -- exactly the precondition syscall.SysProcAttr's
	// own Ptrace field doc comment names for the analogous ptrace case.
	runtime.LockOSThread()

	if _, _, errno := syscall.Syscall(syscall.SYS_PRCTL, uintptr(prSetNoNewPrivs), 1, 0); errno != 0 {
		fmt.Fprintf(os.Stderr, "veriqo-plugin-shim: prctl(PR_SET_NO_NEW_PRIVS): %v\n", errno)
		return 1
	}

	// syscall.Exec replaces this process image entirely (true execve,
	// not fork+exec) -- the real target binary becomes THIS process,
	// under the same PID, inheriting the NO_NEW_PRIVS bit just set,
	// exactly as SandboxRunner's caller (which already put THIS
	// process's PID into a cgroup and namespaces before writing its
	// stdin) expects.
	env := os.Environ()
	if err := syscall.Exec(target, targetArgv, env); err != nil { // #nosec G204 G702 -- target is the resolved plugin binary path SandboxRunner (the operator's own trusted caller) supplied as this shim's own argv[1], not untrusted network input
		fmt.Fprintf(os.Stderr, "veriqo-plugin-shim: exec %s: %v\n", target, err)
		return 1
	}
	// syscall.Exec never returns on success.
	return 0
}
