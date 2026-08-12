// Package sandbox closes PHASE 16: the plugin execution sandbox policy
// engine.
//
// pkg/kernel/plugin already has a Linux-specific enforcement file. What
// it did not have was a POLICY: a declarative statement of what a plugin
// may touch, evaluated before execution, with a hard failure when the
// answer is "not allowed" or "cannot tell".
//
// The audit's single non-negotiable rule for this phase:
//
//	"Jangan pernah fallback ke unrestricted execution.
//	 Kalau sandbox tidak bisa ditegakkan -> PLUGIN_EXECUTION_DENIED."
//
// That rule is why Evaluate returns a Verdict whose zero value is DENY,
// why an unknown request kind is denied rather than passed through, and
// why Enforce refuses to run when the platform cannot enforce the policy
// it was handed. A sandbox that degrades to "run it anyway" on an
// unsupported platform is not a sandbox; it is a comment.
package sandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var (
	// ErrDenied is the audit's PLUGIN_EXECUTION_DENIED.
	ErrDenied = errors.New("sandbox: PLUGIN_EXECUTION_DENIED")
	// ErrUnenforceable is raised when the platform cannot enforce the
	// declared policy. It is a denial, never a warning.
	ErrUnenforceable = errors.New("sandbox: policy cannot be enforced on this platform")
	// ErrPolicyInvalid refuses a policy that would be meaningless.
	ErrPolicyInvalid = errors.New("sandbox: policy is invalid")
	// ErrLimitExceeded is a runtime resource breach.
	ErrLimitExceeded = errors.New("sandbox: resource limit exceeded")
)

// Kind is the class of thing a plugin is asking to do.
type Kind string

const (
	KindFileRead    Kind = "FILE_READ"
	KindFileWrite   Kind = "FILE_WRITE"
	KindNetwork     Kind = "NETWORK"
	KindSyscall     Kind = "SYSCALL"
	KindProcess     Kind = "PROCESS_SPAWN"
	KindEnvironment Kind = "ENV_READ"
)

// Effect is the verdict of a rule.
type Effect string

const (
	EffectAllow Effect = "ALLOW"
	EffectDeny  Effect = "DENY"
)

// Policy is the declarative sandbox contract for one plugin.
//
// Everything is an allow-list. There is deliberately no "deny-list"
// mode: a deny-list is only as good as the imagination of whoever wrote
// it, and the failure mode of forgetting an entry is silent escalation.
type Policy struct {
	PluginID string `json:"plugin_id"`
	Version  string `json:"version"`

	// ReadPaths and WritePaths are path prefixes. A prefix is matched
	// after cleaning, so "../" cannot escape it.
	ReadPaths  []string `json:"read_paths"`
	WritePaths []string `json:"write_paths"`

	// NetworkDestinations are "host:port" entries. "*" is not accepted
	// anywhere; see validate().
	NetworkDestinations []string `json:"network_destinations"`

	// Syscalls is the allowed syscall name set.
	Syscalls []string `json:"syscalls"`

	// EnvVars the plugin may read. Secrets leak through the environment
	// more often than through the filesystem, so this is explicit.
	EnvVars []string `json:"env_vars"`

	// AllowProcessSpawn gates fork/exec entirely.
	AllowProcessSpawn bool `json:"allow_process_spawn"`

	Limits Limits `json:"limits"`
}

// Limits are the resource ceilings.
type Limits struct {
	MaxCPUTicks     uint64 `json:"max_cpu_ticks"`
	MaxMemoryBytes  uint64 `json:"max_memory_bytes"`
	MaxProcesses    int    `json:"max_processes"`
	TimeoutTicks    uint64 `json:"timeout_ticks"`
	MaxOpenFiles    int    `json:"max_open_files"`
	MaxNetworkBytes uint64 `json:"max_network_bytes"`
}

// DefaultPolicy is the most restrictive useful policy: read nothing,
// write nothing, no network, no spawn. A caller must widen it
// explicitly, which is the right direction for a default to fail in.
func DefaultPolicy(pluginID string) Policy {
	return Policy{PluginID: pluginID, Version: "1",
		Limits: Limits{MaxCPUTicks: 1000, MaxMemoryBytes: 64 << 20, MaxProcesses: 0,
			TimeoutTicks: 500, MaxOpenFiles: 16}}
}

func (p Policy) validate() error {
	if p.PluginID == "" {
		return fmt.Errorf("%w: plugin id is required", ErrPolicyInvalid)
	}
	for _, d := range p.NetworkDestinations {
		if d == "*" || strings.HasPrefix(d, "*:") || strings.HasSuffix(d, ":*") {
			return fmt.Errorf("%w: wildcard network destination %q is not an allow-list", ErrPolicyInvalid, d)
		}
		if !strings.Contains(d, ":") {
			return fmt.Errorf("%w: network destination %q must be host:port", ErrPolicyInvalid, d)
		}
	}
	for _, s := range append(append([]string{}, p.ReadPaths...), p.WritePaths...) {
		if s == "/" || s == "" {
			return fmt.Errorf("%w: path prefix %q grants the whole filesystem", ErrPolicyInvalid, s)
		}
	}
	if p.Limits.TimeoutTicks == 0 {
		return fmt.Errorf("%w: a plugin without a timeout can hang the kernel", ErrPolicyInvalid)
	}
	if p.Limits.MaxMemoryBytes == 0 {
		return fmt.Errorf("%w: a memory ceiling is mandatory", ErrPolicyInvalid)
	}
	if p.AllowProcessSpawn && p.Limits.MaxProcesses <= 0 {
		return fmt.Errorf("%w: process spawn allowed but MaxProcesses is %d",
			ErrPolicyInvalid, p.Limits.MaxProcesses)
	}
	return nil
}

// Hash content-addresses the policy so an execution can commit to the
// exact sandbox contract it ran under.
func (p Policy) Hash() string {
	var sb strings.Builder
	sb.WriteString("sandbox_policy/v1\n" + p.PluginID + "\n" + p.Version + "\n")
	writeSet := func(k string, v []string) {
		c := append([]string(nil), v...)
		sort.Strings(c)
		sb.WriteString(k + "=" + strings.Join(c, ";") + "\n")
	}
	writeSet("read", p.ReadPaths)
	writeSet("write", p.WritePaths)
	writeSet("net", p.NetworkDestinations)
	writeSet("syscall", p.Syscalls)
	writeSet("env", p.EnvVars)
	sb.WriteString("spawn=" + strconv.FormatBool(p.AllowProcessSpawn) + "\n")
	sb.WriteString("limits=" + strconv.FormatUint(p.Limits.MaxCPUTicks, 10) + "," +
		strconv.FormatUint(p.Limits.MaxMemoryBytes, 10) + "," +
		strconv.Itoa(p.Limits.MaxProcesses) + "," +
		strconv.FormatUint(p.Limits.TimeoutTicks, 10) + "," +
		strconv.Itoa(p.Limits.MaxOpenFiles) + "," +
		strconv.FormatUint(p.Limits.MaxNetworkBytes, 10) + "\n")
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// Request is one capability request from a plugin.
type Request struct {
	Kind   Kind   `json:"kind"`
	Target string `json:"target"` // path, host:port, syscall name, env var
	Tick   uint64 `json:"tick"`
}

// Verdict is the decision. Its zero value is a denial with an empty
// reason, so a caller that forgets to check gets DENY rather than a
// permissive zero value.
type Verdict struct {
	Effect   Effect `json:"effect"`
	Reason   string `json:"reason"`
	Rule     string `json:"rule"`
	PolicyID string `json:"policy_hash"`
}

// Allowed is the only correct way to read a Verdict.
func (v Verdict) Allowed() bool { return v.Effect == EffectAllow }

// Err converts a denial into the audit's named error.
func (v Verdict) Err() error {
	if v.Allowed() {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrDenied, v.Reason)
}

// Enforcer is what a platform must implement to actually confine a
// process. It exists so "can this platform enforce the policy" is a
// question with a real answer instead of an assumption.
type Enforcer interface {
	// Name identifies the mechanism (seccomp, namespaces, in-process).
	Name() string
	// CanEnforce reports whether every clause of the policy is
	// enforceable here. A false answer must produce a denial.
	CanEnforce(p Policy) (bool, []string)
}

// Sandbox evaluates requests against a policy and tracks usage.
type Sandbox struct {
	mu       sync.Mutex
	policy   Policy
	enforcer Enforcer
	usage    Usage
	audit    []Record
	started  uint64
	running  bool
}

// Usage is the observed resource consumption.
type Usage struct {
	CPUTicks     uint64 `json:"cpu_ticks"`
	MemoryBytes  uint64 `json:"memory_bytes"`
	Processes    int    `json:"processes"`
	OpenFiles    int    `json:"open_files"`
	NetworkBytes uint64 `json:"network_bytes"`
}

// Record is one audited decision.
type Record struct {
	Index   int     `json:"index"`
	Request Request `json:"request"`
	Verdict Verdict `json:"verdict"`
}

// New constructs a Sandbox, refusing outright if the policy is invalid
// or the platform cannot enforce it.
func New(p Policy, e Enforcer) (*Sandbox, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	if e == nil {
		return nil, fmt.Errorf("%w: no enforcer supplied", ErrUnenforceable)
	}
	ok, gaps := e.CanEnforce(p)
	if !ok {
		return nil, fmt.Errorf("%w: %s cannot enforce %s", ErrUnenforceable, e.Name(),
			strings.Join(gaps, ", "))
	}
	return &Sandbox{policy: p, enforcer: e}, nil
}

// Start marks execution as begun at a tick, which arms the timeout.
func (s *Sandbox) Start(tick uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.started, s.running = tick, true
}

// Evaluate decides one request.
func (s *Sandbox) Evaluate(r Request) Verdict {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.decideLocked(r)
	v.PolicyID = s.policy.Hash()
	s.audit = append(s.audit, Record{Index: len(s.audit), Request: r, Verdict: v})
	return v
}

func (s *Sandbox) decideLocked(r Request) Verdict {
	if s.running && s.policy.Limits.TimeoutTicks > 0 &&
		r.Tick > s.started+s.policy.Limits.TimeoutTicks {
		return Verdict{Effect: EffectDeny, Rule: "timeout",
			Reason: "execution exceeded " + strconv.FormatUint(s.policy.Limits.TimeoutTicks, 10) +
				" ticks"}
	}
	switch r.Kind {
	case KindFileRead:
		return matchPath(r.Target, s.policy.ReadPaths, "read_paths")
	case KindFileWrite:
		return matchPath(r.Target, s.policy.WritePaths, "write_paths")
	case KindNetwork:
		if containsExact(s.policy.NetworkDestinations, r.Target) {
			return Verdict{Effect: EffectAllow, Rule: "network_destinations",
				Reason: r.Target + " is declared"}
		}
		return Verdict{Effect: EffectDeny, Rule: "network_destinations",
			Reason: r.Target + " is not in the declared destination allow-list"}
	case KindSyscall:
		if containsExact(s.policy.Syscalls, r.Target) {
			return Verdict{Effect: EffectAllow, Rule: "syscalls", Reason: r.Target + " is declared"}
		}
		return Verdict{Effect: EffectDeny, Rule: "syscalls",
			Reason: "syscall " + r.Target + " is not in the allow-list"}
	case KindEnvironment:
		if containsExact(s.policy.EnvVars, r.Target) {
			return Verdict{Effect: EffectAllow, Rule: "env_vars", Reason: r.Target + " is declared"}
		}
		return Verdict{Effect: EffectDeny, Rule: "env_vars",
			Reason: "environment variable " + r.Target + " is not readable by this plugin"}
	case KindProcess:
		if !s.policy.AllowProcessSpawn {
			return Verdict{Effect: EffectDeny, Rule: "allow_process_spawn",
				Reason: "this plugin may not spawn processes"}
		}
		if s.usage.Processes >= s.policy.Limits.MaxProcesses {
			return Verdict{Effect: EffectDeny, Rule: "max_processes",
				Reason: "process ceiling " + strconv.Itoa(s.policy.Limits.MaxProcesses) + " reached"}
		}
		return Verdict{Effect: EffectAllow, Rule: "allow_process_spawn", Reason: "within the ceiling"}
	default:
		// An unrecognised request kind is denied. A new capability that
		// nobody taught the policy engine about must not execute merely
		// because no rule mentions it.
		return Verdict{Effect: EffectDeny, Rule: "unknown_kind",
			Reason: "request kind " + string(r.Kind) + " has no policy clause; failing closed"}
	}
}

func matchPath(target string, allowed []string, rule string) Verdict {
	clean := path.Clean(target)
	if !strings.HasPrefix(clean, "/") {
		return Verdict{Effect: EffectDeny, Rule: rule,
			Reason: "relative path " + target + " cannot be checked against a prefix allow-list"}
	}
	for _, prefix := range allowed {
		p := path.Clean(prefix)
		if clean == p || strings.HasPrefix(clean, strings.TrimSuffix(p, "/")+"/") {
			return Verdict{Effect: EffectAllow, Rule: rule, Reason: clean + " is under " + p}
		}
	}
	return Verdict{Effect: EffectDeny, Rule: rule,
		Reason: clean + " is outside every declared prefix"}
}

func containsExact(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Consume records resource usage and denies once a ceiling is passed.
func (s *Sandbox) Consume(u Usage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.usage.CPUTicks += u.CPUTicks
	s.usage.NetworkBytes += u.NetworkBytes
	s.usage.Processes += u.Processes
	s.usage.OpenFiles += u.OpenFiles
	if u.MemoryBytes > s.usage.MemoryBytes {
		s.usage.MemoryBytes = u.MemoryBytes
	}
	l := s.policy.Limits
	switch {
	case l.MaxCPUTicks > 0 && s.usage.CPUTicks > l.MaxCPUTicks:
		return fmt.Errorf("%w: cpu %d > %d", ErrLimitExceeded, s.usage.CPUTicks, l.MaxCPUTicks)
	case l.MaxMemoryBytes > 0 && s.usage.MemoryBytes > l.MaxMemoryBytes:
		return fmt.Errorf("%w: memory %d > %d", ErrLimitExceeded, s.usage.MemoryBytes, l.MaxMemoryBytes)
	case l.MaxOpenFiles > 0 && s.usage.OpenFiles > l.MaxOpenFiles:
		return fmt.Errorf("%w: open files %d > %d", ErrLimitExceeded, s.usage.OpenFiles, l.MaxOpenFiles)
	case l.MaxNetworkBytes > 0 && s.usage.NetworkBytes > l.MaxNetworkBytes:
		return fmt.Errorf("%w: network %d > %d", ErrLimitExceeded, s.usage.NetworkBytes, l.MaxNetworkBytes)
	case s.usage.Processes > l.MaxProcesses:
		return fmt.Errorf("%w: processes %d > %d", ErrLimitExceeded, s.usage.Processes, l.MaxProcesses)
	}
	return nil
}

// Usage returns the observed consumption.
func (s *Sandbox) Usage() Usage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.usage
}

// Audit returns every decision taken, in order.
func (s *Sandbox) Audit() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Record(nil), s.audit...)
}

// Denials returns only the refusals — what an operator actually reads
// after a plugin misbehaves.
func (s *Sandbox) Denials() []Record {
	var out []Record
	for _, r := range s.Audit() {
		if !r.Verdict.Allowed() {
			out = append(out, r)
		}
	}
	return out
}

// PolicyHash exposes the committed contract.
func (s *Sandbox) PolicyHash() string { return s.policy.Hash() }

// ---------------------------------------------------------------------
// Enforcers
// ---------------------------------------------------------------------

// InProcessEnforcer is the enforcer available everywhere: it can gate
// capability requests that flow through this package, and nothing else.
//
// It therefore REFUSES policies that would require kernel-level
// confinement — process spawning and raw syscall filtering — instead of
// pretending to enforce them. That refusal is the whole point: a plugin
// that needs to fork must run under a platform enforcer that can
// actually contain a fork, or it must not run.
type InProcessEnforcer struct{}

// Name implements Enforcer.
func (InProcessEnforcer) Name() string { return "in-process capability gate" }

// CanEnforce implements Enforcer.
func (InProcessEnforcer) CanEnforce(p Policy) (bool, []string) {
	var gaps []string
	if p.AllowProcessSpawn {
		gaps = append(gaps, "process spawning (needs OS-level namespaces)")
	}
	if len(p.Syscalls) > 0 {
		gaps = append(gaps, "syscall filtering (needs seccomp)")
	}
	return len(gaps) == 0, gaps
}

// UnenforceableEnforcer represents a platform with no confinement at
// all. It is exported so the "unsupported platform" path is testable
// and so nobody is tempted to write a permissive stub for it.
type UnenforceableEnforcer struct{ Platform string }

// Name implements Enforcer.
func (u UnenforceableEnforcer) Name() string { return "no enforcement on " + u.Platform }

// CanEnforce implements Enforcer: always false.
func (u UnenforceableEnforcer) CanEnforce(Policy) (bool, []string) {
	return false, []string{"this platform provides no sandboxing primitive"}
}
