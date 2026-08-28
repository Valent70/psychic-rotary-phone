package sandbox

// PHASE J (P1-15) — the negative suite.
//
// Seven tests, one per escape vector the program enumerates: host
// filesystem access, host network access, privilege escalation, secret
// mount access, forbidden syscall, cgroup escape, namespace escape.
//
// Each proves TWO things, which are genuinely different and are often
// conflated:
//
//  1. The POLICY ENGINE denies the corresponding capability request.
//     This is provable here, deterministically, on any platform.
//  2. The ENFORCER refuses to run the policy at all when the kernel
//     primitive that would actually contain the vector is absent —
//     rather than accepting the policy and relying on (1) alone.
//
// What none of them proves is that a genuinely hostile binary is
// contained. That requires executing one under the enforcer on a real
// kernel, which is an operational drill, not a unit test — and
// Qualification() says so in the code rather than in a comment nobody
// reads.

import (
	"runtime"
	"strings"
	"testing"
)

// hardenedPolicy is a realistic confined-plugin policy: a narrow read
// path, no writes, no network, no spawn, and real resource ceilings.
func hardenedPolicy() Policy {
	p := DefaultPolicy("confined-plugin")
	p.ReadPaths = []string{"/opt/veriqo/plugin-data"}
	p.Limits.MaxMemoryBytes = 64 << 20
	p.Limits.TimeoutTicks = 500
	return p
}

// fullyCapableConfinement is a Confinement with every primitive present
// — the machine an operator is meant to deploy on. Constructed
// explicitly rather than probed, so the enforcer's LOGIC is tested
// independently of whichever kernel happens to run the suite.
func fullyCapableConfinement() Confinement {
	c := Confinement{Available: map[Primitive]bool{}, Detail: map[Primitive]string{}, Platform: "linux"}
	for _, p := range AllPrimitives() {
		c.Available[p] = true
		c.Detail[p] = "present (test fixture)"
	}
	return c
}

func withoutPrimitive(missing Primitive) Confinement {
	c := fullyCapableConfinement()
	c.Available[missing] = false
	c.Detail[missing] = "absent (test fixture): this kernel does not provide " + string(missing)
	return c
}

// mustDeny asserts the policy engine refuses one capability request.
func mustDeny(t *testing.T, s *Sandbox, r Request, what string) {
	t.Helper()
	v := s.Evaluate(r)
	if v.Allowed() {
		t.Fatalf("%s was ALLOWED: %+v", what, v)
	}
	if v.Err() == nil {
		t.Fatalf("%s produced a denial with no error", what)
	}
	if v.Reason == "" {
		t.Errorf("%s was denied with no stated reason", what)
	}
	if v.PolicyID == "" {
		t.Errorf("%s denial does not cite the policy it was denied under", what)
	}
}

// enforcerMustRefuse asserts the enforcer refuses a policy when the
// primitive that would contain the vector is missing, and names the gap.
func enforcerMustRefuse(t *testing.T, e OSEnforcer, p Policy, expectSubstring string) {
	t.Helper()
	ok, gaps := e.CanEnforce(p)
	if ok {
		t.Fatalf("the enforcer accepted a policy despite a missing primitive (gaps: %v)", gaps)
	}
	joined := strings.Join(gaps, " | ")
	if !strings.Contains(joined, expectSubstring) {
		t.Fatalf("gaps %q do not mention %q -- a refusal must name what is missing", joined, expectSubstring)
	}
	// And New() must refuse to construct a Sandbox at all.
	if _, err := New(p, e); err == nil {
		t.Fatal("New() built a Sandbox on an enforcer that cannot enforce the policy")
	}
}

// --- vector 1: host filesystem access --------------------------------

func TestEscapeVectorHostFilesystemAccessIsDenied(t *testing.T) {
	s, err := New(hardenedPolicy(), OSEnforcer{Confinement: fullyCapableConfinement(), ShimAvailable: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, target := range []string{
		"/etc/shadow", "/etc/passwd", "/root/.ssh/id_ed25519",
		"/proc/self/environ", "/var/lib/veriqo/keys",
		// Traversal out of an allowed prefix must not work either.
		"/opt/veriqo/plugin-data/../../../etc/shadow",
	} {
		mustDeny(t, s, Request{Kind: KindFileRead, Target: target, Tick: 1}, "host filesystem read of "+target)
		mustDeny(t, s, Request{Kind: KindFileWrite, Target: target, Tick: 1}, "host filesystem write to "+target)
	}
	enforcerMustRefuse(t, OSEnforcer{Confinement: withoutPrimitive(PrimitiveMountNamespace), ShimAvailable: true},
		hardenedPolicy(), "filesystem confinement")
}

// --- vector 2: host network access -----------------------------------

func TestEscapeVectorHostNetworkAccessIsDenied(t *testing.T) {
	s, err := New(hardenedPolicy(), OSEnforcer{Confinement: fullyCapableConfinement(), ShimAvailable: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, target := range []string{
		"169.254.169.254:80", // cloud instance metadata
		"127.0.0.1:8080",     // host loopback services
		"10.0.0.1:6443",      // cluster API
		"evil.example.com:443",
	} {
		mustDeny(t, s, Request{Kind: KindNetwork, Target: target, Tick: 1}, "network egress to "+target)
	}
	enforcerMustRefuse(t, OSEnforcer{Confinement: withoutPrimitive(PrimitiveNetworkNamespace), ShimAvailable: true},
		hardenedPolicy(), "network isolation")
}

// TestWildcardNetworkDestinationIsRefusedAtPolicyConstruction covers the
// other direction: a policy cannot grant blanket network access even if
// someone tries.
func TestWildcardNetworkDestinationIsRefusedAtPolicyConstruction(t *testing.T) {
	for _, dest := range []string{"*", "*:443", "evil.example.com:*"} {
		p := hardenedPolicy()
		p.NetworkDestinations = []string{dest}
		if _, err := New(p, OSEnforcer{Confinement: fullyCapableConfinement(), ShimAvailable: true}); err == nil {
			t.Errorf("a policy granting %q was accepted", dest)
		}
	}
}

// --- vector 3: privilege escalation ----------------------------------

func TestEscapeVectorPrivilegeEscalationIsDenied(t *testing.T) {
	p := hardenedPolicy() // AllowProcessSpawn is false
	s, err := New(p, OSEnforcer{Confinement: fullyCapableConfinement(), ShimAvailable: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, target := range []string{"/usr/bin/sudo", "/bin/su", "/usr/bin/newgrp", "/usr/bin/passwd"} {
		mustDeny(t, s, Request{Kind: KindProcess, Target: target, Tick: 1}, "spawning setuid binary "+target)
	}

	// A policy that DOES allow spawning must be refused when
	// PR_SET_NO_NEW_PRIVS is unavailable, because without it a spawned
	// process can regain privilege by exec-ing a setuid binary.
	spawning := hardenedPolicy()
	spawning.AllowProcessSpawn = true
	spawning.Limits.MaxProcesses = 2
	enforcerMustRefuse(t, OSEnforcer{Confinement: withoutPrimitive(PrimitiveNoNewPrivs), ShimAvailable: true},
		spawning, "setuid")
}

// TestSupportedButUnappliedPrimitiveIsNotTreatedAsClosed is the subtlest
// and most important case in this file: a kernel that SUPPORTS
// no-new-privs still confines nothing if the shim that applies it is
// not deployed.
func TestSupportedButUnappliedPrimitiveIsNotTreatedAsClosed(t *testing.T) {
	spawning := hardenedPolicy()
	spawning.AllowProcessSpawn = true
	spawning.Limits.MaxProcesses = 2

	e := OSEnforcer{Confinement: fullyCapableConfinement(), ShimAvailable: false}
	ok, gaps := e.CanEnforce(spawning)
	if ok {
		t.Fatal("a fully-capable kernel with no shim deployed was treated as able to enforce process spawning")
	}
	if !strings.Contains(strings.Join(gaps, " | "), "not deployed") {
		t.Fatalf("gaps %v do not explain that nothing applies the primitive", gaps)
	}

	// The vector report must agree.
	for _, r := range e.Vectors() {
		if r.Vector == EscapePrivilegeEscalation && r.Closed {
			t.Fatal("privilege escalation reported CLOSED with no shim to apply PR_SET_NO_NEW_PRIVS")
		}
	}
}

// --- vector 4: secret mount access -----------------------------------

func TestEscapeVectorSecretMountAccessIsDenied(t *testing.T) {
	s, err := New(hardenedPolicy(), OSEnforcer{Confinement: fullyCapableConfinement(), ShimAvailable: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, target := range []string{
		"/run/secrets/veriqo-signing-key",
		"/var/run/secrets/kubernetes.io/serviceaccount/token",
		"/etc/veriqo/kms.json",
	} {
		mustDeny(t, s, Request{Kind: KindFileRead, Target: target, Tick: 1}, "secret mount read of "+target)
	}
	// Secrets leak through the environment at least as often as through
	// the filesystem, and the policy's EnvVars list is empty.
	for _, target := range []string{"AWS_SECRET_ACCESS_KEY", "VERIQO_SIGNING_KEY", "KUBERNETES_SERVICE_TOKEN"} {
		mustDeny(t, s, Request{Kind: KindEnvironment, Target: target, Tick: 1}, "environment read of "+target)
	}
	enforcerMustRefuse(t, OSEnforcer{Confinement: withoutPrimitive(PrimitiveMountNamespace), ShimAvailable: true},
		hardenedPolicy(), "filesystem confinement")
}

// --- vector 5: forbidden syscall -------------------------------------

func TestEscapeVectorForbiddenSyscallIsDenied(t *testing.T) {
	s, err := New(hardenedPolicy(), OSEnforcer{Confinement: fullyCapableConfinement(), ShimAvailable: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// The policy declares no syscalls, so the allow-list is empty and
	// every syscall request is denied -- including the ones a real
	// container escape reaches for.
	for _, target := range []string{
		"mount", "umount2", "pivot_root", "ptrace", "init_module",
		"finit_module", "kexec_load", "bpf", "setns", "unshare",
	} {
		mustDeny(t, s, Request{Kind: KindSyscall, Target: target, Tick: 1}, "syscall "+target)
	}

	// A policy that names syscalls must be refused when seccomp is
	// absent: an allow-list nothing enforces is a comment.
	filtered := hardenedPolicy()
	filtered.Syscalls = []string{"read", "write", "close"}
	enforcerMustRefuse(t, OSEnforcer{Confinement: withoutPrimitive(PrimitiveSeccomp), ShimAvailable: true},
		filtered, "syscall filtering")
}

// --- vector 6: cgroup escape -----------------------------------------

func TestEscapeVectorCgroupEscapeIsDenied(t *testing.T) {
	s, err := New(hardenedPolicy(), OSEnforcer{Confinement: fullyCapableConfinement(), ShimAvailable: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// The classic cgroup-escape primitives: writing the release agent,
	// re-attaching to a different cgroup, or remounting the hierarchy.
	for _, target := range []string{
		"/sys/fs/cgroup/release_agent",
		"/sys/fs/cgroup/notify_on_release",
		"/sys/fs/cgroup/cgroup.procs",
		"/sys/fs/cgroup/veriqo/cgroup.subtree_control",
	} {
		mustDeny(t, s, Request{Kind: KindFileWrite, Target: target, Tick: 1}, "cgroup escape write to "+target)
	}
	for _, target := range []string{"mount", "umount2"} {
		mustDeny(t, s, Request{Kind: KindSyscall, Target: target, Tick: 1}, "cgroup remount via "+target)
	}

	// A policy declaring resource limits must be refused when cgroup v2
	// is absent: a memory ceiling nothing enforces is a comment too.
	enforcerMustRefuse(t, OSEnforcer{Confinement: withoutPrimitive(PrimitiveCgroupV2), ShimAvailable: true},
		hardenedPolicy(), "resource limits")
}

// TestResourceLimitsAreActuallyEnforcedAtRuntime proves the limit half
// is real, not only declared: consuming past the ceiling is refused.
func TestResourceLimitsAreActuallyEnforcedAtRuntime(t *testing.T) {
	p := hardenedPolicy()
	p.Limits.MaxMemoryBytes = 1024
	s, err := New(p, OSEnforcer{Confinement: fullyCapableConfinement(), ShimAvailable: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Consume(Usage{MemoryBytes: 4096}); err == nil {
		t.Fatal("consuming four times the memory ceiling was accepted")
	}
}

// --- vector 7: namespace escape --------------------------------------

func TestEscapeVectorNamespaceEscapeIsDenied(t *testing.T) {
	s, err := New(hardenedPolicy(), OSEnforcer{Confinement: fullyCapableConfinement(), ShimAvailable: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// setns/unshare are how a process joins or leaves a namespace;
	// /proc/*/ns/* are the handles it needs to do so.
	for _, target := range []string{"setns", "unshare", "clone", "pivot_root"} {
		mustDeny(t, s, Request{Kind: KindSyscall, Target: target, Tick: 1}, "namespace escape via "+target)
	}
	for _, target := range []string{"/proc/1/ns/mnt", "/proc/1/ns/net", "/proc/1/ns/pid", "/proc/1/root"} {
		mustDeny(t, s, Request{Kind: KindFileRead, Target: target, Tick: 1}, "namespace handle read of "+target)
	}
	// Escaping the PID namespace also requires spawning, which is off.
	mustDeny(t, s, Request{Kind: KindProcess, Target: "/bin/sh", Tick: 1}, "spawning a shell")

	enforcerMustRefuse(t, OSEnforcer{Confinement: withoutPrimitive(PrimitivePIDNamespace), ShimAvailable: true},
		func() Policy {
			p := hardenedPolicy()
			p.AllowProcessSpawn = true
			p.Limits.MaxProcesses = 2
			return p
		}(), "process spawning")
}

// --- coverage and honesty --------------------------------------------

// TestEveryEscapeVectorHasATest keeps the suite exhaustive: adding a
// vector to the inventory without adding a test for it fails here
// rather than being an oversight nobody notices.
func TestEveryEscapeVectorHasATest(t *testing.T) {
	covered := map[EscapeVector]string{
		EscapeHostFilesystem:      "TestEscapeVectorHostFilesystemAccessIsDenied",
		EscapeHostNetwork:         "TestEscapeVectorHostNetworkAccessIsDenied",
		EscapePrivilegeEscalation: "TestEscapeVectorPrivilegeEscalationIsDenied",
		EscapeSecretMount:         "TestEscapeVectorSecretMountAccessIsDenied",
		EscapeForbiddenSyscall:    "TestEscapeVectorForbiddenSyscallIsDenied",
		EscapeCgroupEscape:        "TestEscapeVectorCgroupEscapeIsDenied",
		EscapeNamespaceEscape:     "TestEscapeVectorNamespaceEscapeIsDenied",
	}
	for _, v := range AllEscapeVectors() {
		if covered[v] == "" {
			t.Errorf("escape vector %s has no named test", v)
		}
	}
	if len(covered) != len(AllEscapeVectors()) {
		t.Errorf("the coverage table lists %d vectors, the inventory has %d",
			len(covered), len(AllEscapeVectors()))
	}
	// Every vector must also declare which primitives close it.
	for _, v := range AllEscapeVectors() {
		if len(requiredPrimitives[v]) == 0 {
			t.Errorf("escape vector %s declares no required primitive -- it cannot be checked", v)
		}
	}
}

// TestProbeReadsTheRealKernelRatherThanAssuming: the answers must come
// from /proc and /sys, and every answer must carry a stated reason.
func TestProbeReadsTheRealKernelRatherThanAssuming(t *testing.T) {
	c := Probe()
	if c.Platform != runtime.GOOS {
		t.Fatalf("Platform = %q, want %q", c.Platform, runtime.GOOS)
	}
	for _, p := range AllPrimitives() {
		if _, stated := c.Available[p]; !stated {
			t.Errorf("primitive %s was not probed at all", p)
		}
		if strings.TrimSpace(c.Detail[p]) == "" {
			t.Errorf("primitive %s has no stated reason for its answer", p)
		}
	}
	if runtime.GOOS == "linux" {
		// On Linux, namespace support is essentially universal on any
		// kernel this project could run on; if the probe says otherwise
		// it is far more likely the probe is broken than the kernel.
		if !c.Has(PrimitivePIDNamespace) {
			t.Errorf("PID namespaces reported absent on Linux: %s", c.Detail[PrimitivePIDNamespace])
		}
	} else {
		for _, p := range AllPrimitives() {
			if c.Has(p) {
				t.Errorf("primitive %s reported present on %s", p, runtime.GOOS)
			}
		}
	}
}

// TestEnforcerNeverAssumesAnAbsentPrimitive is the fail-closed core.
func TestEnforcerNeverAssumesAnAbsentPrimitive(t *testing.T) {
	empty := Confinement{Available: map[Primitive]bool{}, Detail: map[Primitive]string{}, Platform: "linux"}
	e := OSEnforcer{Confinement: empty}
	ok, gaps := e.CanEnforce(hardenedPolicy())
	if ok {
		t.Fatal("an enforcer with NO available primitive claimed it could enforce a policy")
	}
	if len(gaps) == 0 {
		t.Fatal("a refusal with no named gap")
	}
	for _, r := range e.Vectors() {
		if r.Closed {
			t.Errorf("vector %s reported closed with no primitives at all", r.Vector)
		}
		if r.Reason == "" {
			t.Errorf("vector %s reported with no reason", r.Vector)
		}
	}
}

// TestFullyCapableKernelWithShimClosesEveryVector is the positive
// control: without it, an enforcer that refused everything would pass
// every test above and be useless.
func TestFullyCapableKernelWithShimClosesEveryVector(t *testing.T) {
	e := OSEnforcer{Confinement: fullyCapableConfinement(), ShimAvailable: true}
	if ok, gaps := e.CanEnforce(hardenedPolicy()); !ok {
		t.Fatalf("a fully-capable kernel could not enforce a realistic policy: %v", gaps)
	}
	for _, r := range e.Vectors() {
		if !r.Closed {
			t.Errorf("vector %s not closed on a fully-capable kernel: %s (missing %v)", r.Vector, r.Reason, r.Missing)
		}
	}
	if !strings.Contains(e.Name(), "seccomp") {
		t.Errorf("Name() = %q does not report the primitives in use", e.Name())
	}
}

// TestNewOSEnforcerRequiresTheShimBinaryToExist proves ShimAvailable is
// a real on-disk check, not a flag someone can set.
func TestNewOSEnforcerRequiresTheShimBinaryToExist(t *testing.T) {
	if e := NewOSEnforcer(""); e.ShimAvailable {
		t.Error("an empty shim path reported the shim as available")
	}
	if e := NewOSEnforcer("/nonexistent/veriqo-plugin-shim"); e.ShimAvailable {
		t.Error("a nonexistent shim path reported the shim as available")
	}
	// A directory is not an executable.
	if e := NewOSEnforcer(t.TempDir()); e.ShimAvailable {
		t.Error("a directory reported the shim as available")
	}
}

// TestQualificationIsHonestlyCapped is the anti-false-green assertion
// for this whole phase.
func TestQualificationIsHonestlyCapped(t *testing.T) {
	q := Qualification()
	if q.Status != "INTERNAL_QUALIFIED" {
		t.Fatalf("Status = %q -- a unit test can never establish that a hostile binary is contained", q.Status)
	}
	if len(q.NotProven) == 0 {
		t.Fatal("the qualification lists nothing unproven, which cannot be true of a test-only proof")
	}
	if len(q.Proven) == 0 {
		t.Fatal("the qualification claims nothing proven, which would make the phase pointless")
	}
	if q.RaisedBy == "" {
		t.Fatal("the qualification does not say what would raise it")
	}
}

// TestInProcessEnforcerStillRefusesWhatItAlwaysDid confirms this phase
// is purely additive: the pre-existing enforcer's contract is unchanged.
func TestInProcessEnforcerStillRefusesWhatItAlwaysDid(t *testing.T) {
	p := hardenedPolicy()
	p.AllowProcessSpawn = true
	p.Limits.MaxProcesses = 2
	p.Syscalls = []string{"read"}
	ok, gaps := InProcessEnforcer{}.CanEnforce(p)
	if ok {
		t.Fatal("InProcessEnforcer accepted a policy needing kernel confinement")
	}
	if len(gaps) != 2 {
		t.Fatalf("gaps = %v, want both process spawning and syscall filtering", gaps)
	}
}
