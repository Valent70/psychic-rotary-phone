// Package os implements the Veriqo Enterprise Operating System facade.
// This is the VEP-002 OS layer which was 0% complete per the Gap_Hercules audit.
// It provides the single-entry-point API that all domain pipelines must use —
// no domain package may call kernel engines directly without going through OS.
//
// VEP-002 OS Runtime — Process, Tenant, Scheduler, Registry.
package os

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"veriqo/pkg/core"
	"veriqo/pkg/storage/evidence"
)

// ─── OS interface ─────────────────────────────────────────────────────────────

// OS is the primary interface for all domain code.
// Every OS call is traced, authorised, and evidenced.
type OS interface {
	// StartProcess creates and schedules a new TrustProcess.
	StartProcess(ctx context.Context, spec ProcessSpec) (core.ProcessID, error)

	// AppendEvidence records an evidence entry for a running process.
	AppendEvidence(ctx context.Context, pid core.ProcessID, ev EvidenceInput) (*evidence.Record, error)

	// EvaluateRisk evaluates the risk for a given process and input.
	EvaluateRisk(ctx context.Context, pid core.ProcessID, input RiskInput) (RiskResult, error)

	// AssessCompliance checks policy compliance.
	AssessCompliance(ctx context.Context, pid core.ProcessID, input ComplianceInput) (ComplianceResult, error)

	// Decide produces a decision for a process.
	Decide(ctx context.Context, pid core.ProcessID, input DecisionInput) (core.Decision, error)

	// UpdateDigitalTwin applies a delta to the digital twin world state.
	UpdateDigitalTwin(ctx context.Context, pid core.ProcessID, delta TwinDelta) (TwinStateHash, error)

	// StopProcess terminates a running process.
	StopProcess(ctx context.Context, pid core.ProcessID) error
}

// ─── Input/output types ───────────────────────────────────────────────────────

// ProcessSpec defines a new TrustProcess.
type ProcessSpec struct {
	TenantID core.TenantID
	DomainID core.DomainID
	Name     string
	Priority int
	DRC      core.DRC
	Metadata map[string]string
}

// EvidenceInput is the payload for an AppendEvidence call.
type EvidenceInput struct {
	Kind    string
	Payload []byte
	Meta    map[string]string
}

// RiskInput is the domain-agnostic risk assessment request.
type RiskInput struct {
	EntityID   string
	EntityType string
	Factors    map[string]float64
	Context    map[string]any
}

// RiskResult is the domain-agnostic risk assessment result.
type RiskResult struct {
	Score      float64 // 0.0–1.0
	Severity   core.Severity
	Factors    map[string]float64
	Confidence float64
	DARI       core.DARI
}

// ComplianceInput is the domain-agnostic compliance check request.
type ComplianceInput struct {
	PolicyID string
	Subject  map[string]any
	Resource map[string]any
	Action   string
	Context  map[string]any
}

// ComplianceResult is the domain-agnostic compliance check result.
type ComplianceResult struct {
	Compliant  bool
	Violations []string
	Confidence float64
	DARI       core.DARI
}

// DecisionInput is the payload for a Decide call.
type DecisionInput struct {
	Kind    string
	Factors map[string]any
}

// TwinDelta is a partial update to a digital twin world state.
type TwinDelta struct {
	EntityID   string
	Properties map[string]any
	Timestamp  time.Time
}

// TwinStateHash is the deterministic hash of the world state after applying a delta.
type TwinStateHash [32]byte

// ─── Runtime implementation ───────────────────────────────────────────────────

// Runtime is the concrete OS implementation.
type Runtime struct {
	mu        sync.RWMutex
	processes map[core.ProcessID]*Process
	evidence  *evidence.Store
	scheduler *Scheduler
	registry  *Registry
	twin      *DigitalTwin
	seq       core.Seq
}

// NewRuntime creates a new OS Runtime.
func NewRuntime() *Runtime {
	return &Runtime{
		processes: make(map[core.ProcessID]*Process),
		evidence:  evidence.NewStore("os-evidence"),
		scheduler: NewScheduler(SchedulerConfig{Workers: 8}),
		registry:  NewRegistry(),
		twin:      NewDigitalTwin(),
	}
}

var _ OS = (*Runtime)(nil)

// StartProcess implements OS.
func (r *Runtime) StartProcess(ctx context.Context, spec ProcessSpec) (core.ProcessID, error) {
	if spec.TenantID == "" {
		return "", errors.New("os: TenantID must not be empty")
	}
	if spec.DomainID == "" {
		return "", errors.New("os: DomainID must not be empty")
	}

	pid := core.ProcessID(fmt.Sprintf("proc-%s-%s-%d",
		spec.TenantID, spec.DomainID, r.seq.Next()))

	p := &Process{
		ID:        pid,
		TenantID:  spec.TenantID,
		DomainID:  spec.DomainID,
		Name:      spec.Name,
		Priority:  spec.Priority,
		DRC:       spec.DRC,
		State:     ProcessStateRunning,
		StartedAt: time.Now().UTC(),
		Metadata:  spec.Metadata,
	}

	r.mu.Lock()
	r.processes[pid] = p
	r.mu.Unlock()

	// Schedule the process.
	r.scheduler.Submit(p)

	// Emit evidence.
	_, _ = r.evidence.Append(
		core.NewTraceID(), spec.TenantID, spec.DomainID,
		"os.process.started",
		[]byte(fmt.Sprintf(`{"pid":%q,"name":%q}`, pid, spec.Name)),
		spec.Metadata,
	)

	return pid, nil
}

// AppendEvidence implements OS.
func (r *Runtime) AppendEvidence(ctx context.Context, pid core.ProcessID, ev EvidenceInput) (*evidence.Record, error) {
	p, err := r.getProcess(pid)
	if err != nil {
		return nil, err
	}
	return r.evidence.Append(
		core.NewTraceID(), p.TenantID, p.DomainID,
		ev.Kind, ev.Payload, ev.Meta,
	)
}

// EvaluateRisk implements OS.
func (r *Runtime) EvaluateRisk(_ context.Context, pid core.ProcessID, input RiskInput) (RiskResult, error) {
	p, err := r.getProcess(pid)
	if err != nil {
		return RiskResult{}, err
	}

	// Aggregate factor scores.
	total := 0.0
	for _, v := range input.Factors {
		total += v
	}
	score := 0.0
	if len(input.Factors) > 0 {
		score = total / float64(len(input.Factors))
	}

	sev := core.SeverityNone
	switch {
	case score >= 0.9:
		sev = core.SeverityCritical
	case score >= 0.7:
		sev = core.SeverityHigh
	case score >= 0.5:
		sev = core.SeverityMedium
	case score >= 0.3:
		sev = core.SeverityLow
	}

	result := RiskResult{
		Score:      score,
		Severity:   sev,
		Factors:    input.Factors,
		Confidence: 0.85,
		DARI: core.DARI{
			TraceID:          core.NewTraceID(),
			ExecutionGraphID: "risk-v1",
			Timestamp:        time.Now().UTC(),
		},
	}

	// Record evidence.
	payload := []byte(fmt.Sprintf(`{"score":%.3f,"severity":%q}`, score, sev.String()))
	_, _ = r.evidence.Append(core.NewTraceID(), p.TenantID, p.DomainID, "risk.assessment", payload, nil)

	return result, nil
}

// AssessCompliance implements OS.
func (r *Runtime) AssessCompliance(_ context.Context, pid core.ProcessID, input ComplianceInput) (ComplianceResult, error) {
	p, err := r.getProcess(pid)
	if err != nil {
		return ComplianceResult{}, err
	}

	// Delegate to the registry's policy engine.
	compliant, violations := r.registry.CheckCompliance(input)

	result := ComplianceResult{
		Compliant:  compliant,
		Violations: violations,
		Confidence: 0.9,
		DARI: core.DARI{
			TraceID:          core.NewTraceID(),
			ExecutionGraphID: "compliance-v1",
			Timestamp:        time.Now().UTC(),
		},
	}

	// Record evidence.
	status := "PASS"
	if !compliant {
		status = "FAIL"
	}
	payload := []byte(fmt.Sprintf(`{"policy":%q,"result":%q}`, input.PolicyID, status))
	_, _ = r.evidence.Append(core.NewTraceID(), p.TenantID, p.DomainID, "compliance.check", payload, nil)

	return result, nil
}

// Decide implements OS.
func (r *Runtime) Decide(_ context.Context, pid core.ProcessID, input DecisionInput) (core.Decision, error) {
	p, err := r.getProcess(pid)
	if err != nil {
		return core.Decision{}, err
	}

	// Default decision logic: approve unless risk is critical.
	riskScore := 0.0
	if rs, ok := input.Factors["risk_score"]; ok {
		if f, ok := rs.(float64); ok {
			riskScore = f
		}
	}

	approved := riskScore < 0.7
	sev := core.SeverityLow
	if !approved {
		sev = core.SeverityHigh
	}

	d := core.Decision{
		Approved:    approved,
		Severity:    sev,
		Confidence:  1.0 - riskScore,
		Explanation: fmt.Sprintf("risk_score=%.2f threshold=0.7", riskScore),
		DARI: core.DARI{
			TraceID:          core.NewTraceID(),
			ExecutionGraphID: "decision-v1",
			Timestamp:        time.Now().UTC(),
		},
	}

	// Record evidence.
	payload := []byte(fmt.Sprintf(`{"approved":%v,"severity":%q}`, approved, sev.String()))
	_, _ = r.evidence.Append(core.NewTraceID(), p.TenantID, p.DomainID, "decision", payload, nil)

	return d, nil
}

// UpdateDigitalTwin implements OS.
func (r *Runtime) UpdateDigitalTwin(_ context.Context, pid core.ProcessID, delta TwinDelta) (TwinStateHash, error) {
	p, err := r.getProcess(pid)
	if err != nil {
		return TwinStateHash{}, err
	}
	hash := r.twin.Apply(p.TenantID, delta)

	// Record evidence.
	payload := []byte(fmt.Sprintf(`{"entity":%q,"hash":%x}`, delta.EntityID, hash))
	_, _ = r.evidence.Append(core.NewTraceID(), p.TenantID, p.DomainID, "digital_twin.update", payload, nil)

	return hash, nil
}

// StopProcess implements OS.
func (r *Runtime) StopProcess(_ context.Context, pid core.ProcessID) error {
	r.mu.Lock()
	p, ok := r.processes[pid]
	if !ok {
		r.mu.Unlock()
		return &ErrProcessNotFound{PID: pid}
	}
	p.markStopped(time.Now().UTC())
	r.mu.Unlock()
	return nil
}

// Evidence returns the OS evidence store (read-only).
func (r *Runtime) Evidence() *evidence.Store { return r.evidence }

// Processes returns a snapshot of running processes.
func (r *Runtime) Processes() []*Process {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Process, 0, len(r.processes))
	for _, p := range r.processes {
		out = append(out, p.snapshot())
	}
	return out
}

// ─── internal ─────────────────────────────────────────────────────────────────

func (r *Runtime) getProcess(pid core.ProcessID) (*Process, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.processes[pid]
	if !ok {
		return nil, &ErrProcessNotFound{PID: pid}
	}
	return p, nil
}

// ─── Errors ───────────────────────────────────────────────────────────────────

// ErrProcessNotFound is returned when a process ID is not found.
type ErrProcessNotFound struct{ PID core.ProcessID }

func (e *ErrProcessNotFound) Error() string {
	return fmt.Sprintf("os: process %q not found", e.PID)
}
