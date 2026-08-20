package sandbox

import (
	"errors"
	"testing"
)

func policy() Policy {
	p := DefaultPolicy("ais-normaliser")
	p.ReadPaths = []string{"/var/veriqo/evidence"}
	p.WritePaths = []string{"/var/veriqo/out"}
	p.NetworkDestinations = []string{"feed.example:443"}
	p.EnvVars = []string{"VERIQO_TENANT"}
	return p
}

func box(t *testing.T, p Policy) *Sandbox {
	t.Helper()
	s, err := New(p, InProcessEnforcer{})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	return s
}

func TestZeroVerdictIsADenial(t *testing.T) {
	var v Verdict
	if v.Allowed() {
		t.Fatal("the zero verdict must not be permissive")
	}
	if v.Err() == nil || !errors.Is(v.Err(), ErrDenied) {
		t.Fatal("a denial must convert to PLUGIN_EXECUTION_DENIED")
	}
}

func TestUnenforceablePlatformDeniesExecutionEntirely(t *testing.T) {
	_, err := New(policy(), UnenforceableEnforcer{Platform: "test-os"})
	if !errors.Is(err, ErrUnenforceable) {
		t.Fatalf("an unenforceable platform must refuse to construct a sandbox, got %v", err)
	}
}

func TestNilEnforcerIsRefused(t *testing.T) {
	if _, err := New(policy(), nil); !errors.Is(err, ErrUnenforceable) {
		t.Fatalf("no enforcer means no execution, got %v", err)
	}
}

func TestPolicyRequiringSeccompIsRefusedByTheInProcessEnforcer(t *testing.T) {
	p := policy()
	p.Syscalls = []string{"read", "write"}
	if _, err := New(p, InProcessEnforcer{}); !errors.Is(err, ErrUnenforceable) {
		t.Fatalf("syscall filtering must not be silently ignored, got %v", err)
	}
	p2 := policy()
	p2.AllowProcessSpawn = true
	p2.Limits.MaxProcesses = 2
	if _, err := New(p2, InProcessEnforcer{}); !errors.Is(err, ErrUnenforceable) {
		t.Fatalf("process spawn must not be silently ignored, got %v", err)
	}
}

func TestInvalidPoliciesAreRefused(t *testing.T) {
	cases := map[string]func(*Policy){
		"wildcard network":  func(p *Policy) { p.NetworkDestinations = []string{"*"} },
		"portless network":  func(p *Policy) { p.NetworkDestinations = []string{"feed.example"} },
		"root path":         func(p *Policy) { p.ReadPaths = []string{"/"} },
		"no timeout":        func(p *Policy) { p.Limits.TimeoutTicks = 0 },
		"no memory ceiling": func(p *Policy) { p.Limits.MaxMemoryBytes = 0 },
		"no plugin id":      func(p *Policy) { p.PluginID = "" },
		"spawn without ceiling": func(p *Policy) {
			p.AllowProcessSpawn = true
			p.Limits.MaxProcesses = 0
		},
	}
	for name, mutate := range cases {
		p := policy()
		mutate(&p)
		if _, err := New(p, InProcessEnforcer{}); err == nil {
			t.Fatalf("%s must be refused", name)
		}
	}
}

func TestDefaultPolicyDeniesEverything(t *testing.T) {
	s := box(t, DefaultPolicy("bare"))
	for _, r := range []Request{
		{Kind: KindFileRead, Target: "/etc/passwd"},
		{Kind: KindFileWrite, Target: "/tmp/x"},
		{Kind: KindNetwork, Target: "example.com:443"},
		{Kind: KindEnvironment, Target: "PATH"},
		{Kind: KindProcess, Target: "sh"},
	} {
		if s.Evaluate(r).Allowed() {
			t.Fatalf("the default policy must deny %s %s", r.Kind, r.Target)
		}
	}
}

func TestDeclaredPathsAllowedAndOthersDenied(t *testing.T) {
	s := box(t, policy())
	if !s.Evaluate(Request{Kind: KindFileRead, Target: "/var/veriqo/evidence/ais/1.json"}).Allowed() {
		t.Fatal("a path under a declared read prefix must be allowed")
	}
	if s.Evaluate(Request{Kind: KindFileRead, Target: "/var/veriqo/out/secret"}).Allowed() {
		t.Fatal("a write prefix must not grant read access")
	}
	if s.Evaluate(Request{Kind: KindFileWrite, Target: "/var/veriqo/evidence/x"}).Allowed() {
		t.Fatal("a read prefix must not grant write access")
	}
}

func TestPathTraversalCannotEscapeThePrefix(t *testing.T) {
	s := box(t, policy())
	for _, target := range []string{
		"/var/veriqo/evidence/../../../etc/shadow",
		"/var/veriqo/evidence/../out/../../root/.ssh/id_rsa",
		"/var/veriqo/evidence-sibling/secret",
	} {
		if s.Evaluate(Request{Kind: KindFileRead, Target: target}).Allowed() {
			t.Fatalf("traversal target %q must be denied", target)
		}
	}
}

func TestRelativePathIsDeniedRatherThanGuessed(t *testing.T) {
	s := box(t, policy())
	v := s.Evaluate(Request{Kind: KindFileRead, Target: "evidence/1.json"})
	if v.Allowed() {
		t.Fatal("a relative path cannot be checked and must be denied")
	}
}

func TestNetworkDestinationMustMatchHostAndPortExactly(t *testing.T) {
	s := box(t, policy())
	if !s.Evaluate(Request{Kind: KindNetwork, Target: "feed.example:443"}).Allowed() {
		t.Fatal("the declared destination must be allowed")
	}
	for _, target := range []string{"feed.example:80", "evil.example:443", "feed.example.evil:443"} {
		if s.Evaluate(Request{Kind: KindNetwork, Target: target}).Allowed() {
			t.Fatalf("%q must be denied", target)
		}
	}
}

func TestEnvironmentReadsAreAllowListed(t *testing.T) {
	s := box(t, policy())
	if !s.Evaluate(Request{Kind: KindEnvironment, Target: "VERIQO_TENANT"}).Allowed() {
		t.Fatal("a declared env var must be readable")
	}
	if s.Evaluate(Request{Kind: KindEnvironment, Target: "AWS_SECRET_ACCESS_KEY"}).Allowed() {
		t.Fatal("an undeclared env var must be denied")
	}
}

func TestUnknownRequestKindFailsClosed(t *testing.T) {
	s := box(t, policy())
	v := s.Evaluate(Request{Kind: Kind("QUANTUM_TUNNEL"), Target: "anything"})
	if v.Allowed() {
		t.Fatal("an unrecognised capability must fail closed")
	}
	if v.Rule != "unknown_kind" {
		t.Fatalf("the denial must say why: %+v", v)
	}
}

func TestTimeoutDeniesLaterRequests(t *testing.T) {
	p := policy()
	p.Limits.TimeoutTicks = 10
	s := box(t, p)
	s.Start(100)
	if !s.Evaluate(Request{Kind: KindFileRead, Target: "/var/veriqo/evidence/a", Tick: 105}).Allowed() {
		t.Fatal("a request inside the timeout must be allowed")
	}
	v := s.Evaluate(Request{Kind: KindFileRead, Target: "/var/veriqo/evidence/a", Tick: 200})
	if v.Allowed() || v.Rule != "timeout" {
		t.Fatalf("a request past the timeout must be denied: %+v", v)
	}
}

func TestResourceCeilingsAreEnforced(t *testing.T) {
	p := policy()
	p.Limits.MaxCPUTicks = 100
	p.Limits.MaxMemoryBytes = 1024
	p.Limits.MaxOpenFiles = 2
	p.Limits.MaxNetworkBytes = 500
	s := box(t, p)

	if err := s.Consume(Usage{CPUTicks: 50, MemoryBytes: 512, OpenFiles: 1}); err != nil {
		t.Fatalf("usage inside the ceilings must be accepted: %v", err)
	}
	if err := s.Consume(Usage{CPUTicks: 60}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("the cpu ceiling must bite, got %v", err)
	}

	s2 := box(t, p)
	if err := s2.Consume(Usage{MemoryBytes: 4096}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("the memory ceiling must bite, got %v", err)
	}
	s3 := box(t, p)
	if err := s3.Consume(Usage{NetworkBytes: 600}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("the network ceiling must bite, got %v", err)
	}
	s4 := box(t, p)
	if err := s4.Consume(Usage{OpenFiles: 5}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("the file-descriptor ceiling must bite, got %v", err)
	}
}

func TestMemoryIsAPeakNotASum(t *testing.T) {
	p := policy()
	p.Limits.MaxMemoryBytes = 1000
	s := box(t, p)
	for i := 0; i < 5; i++ {
		if err := s.Consume(Usage{MemoryBytes: 400}); err != nil {
			t.Fatalf("a steady 400-byte footprint must not accumulate into a breach: %v", err)
		}
	}
	if s.Usage().MemoryBytes != 400 {
		t.Fatalf("memory must be tracked as a peak, got %d", s.Usage().MemoryBytes)
	}
}

func TestEveryDecisionIsAudited(t *testing.T) {
	s := box(t, policy())
	s.Evaluate(Request{Kind: KindFileRead, Target: "/var/veriqo/evidence/a"})
	s.Evaluate(Request{Kind: KindFileRead, Target: "/etc/shadow"})
	s.Evaluate(Request{Kind: KindNetwork, Target: "evil:1"})

	if len(s.Audit()) != 3 {
		t.Fatalf("every decision must be audited, got %d", len(s.Audit()))
	}
	if len(s.Denials()) != 2 {
		t.Fatalf("expected two denials, got %d", len(s.Denials()))
	}
	for _, r := range s.Audit() {
		if r.Verdict.PolicyID == "" || r.Verdict.Reason == "" {
			t.Fatalf("each record must name the policy and the reason: %+v", r)
		}
	}
}

func TestPolicyHashIsStableAndSensitive(t *testing.T) {
	a := policy()
	b := policy()
	b.ReadPaths = []string{"/var/veriqo/evidence"} // same content
	if a.Hash() != b.Hash() {
		t.Fatal("identical policies must hash identically")
	}
	c := policy()
	c.NetworkDestinations = append(c.NetworkDestinations, "other.example:443")
	if c.Hash() == a.Hash() {
		t.Fatal("widening the network allow-list must change the policy hash")
	}
	d := policy()
	d.Limits.MaxMemoryBytes *= 2
	if d.Hash() == a.Hash() {
		t.Fatal("changing a limit must change the policy hash")
	}
}

func TestSetOrderDoesNotChangeThePolicyHash(t *testing.T) {
	a := policy()
	a.EnvVars = []string{"A", "B"}
	b := policy()
	b.EnvVars = []string{"B", "A"}
	if a.Hash() != b.Hash() {
		t.Fatal("allow-list ordering must not change the policy identity")
	}
}
