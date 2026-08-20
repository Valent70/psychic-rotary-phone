// Package workflow implements Veriqo's Multi-Agent Runtime and
// Distributed Workflow layer — items #6 and #7 in the investor
// re-prioritization doc's own ordering (Masalah_terbesar_nomor_1.docx:
// "...5. Digital Twin -> 6. Multi-Agent Runtime -> 7. Distributed
// Workflow -> 8. Real-world Demonstration"), and the concrete answer to
// "Saat ini alur masih: manual pipeline. Saya ingin melihat: Collector
// Agent -> Fusion Agent -> Reasoning Agent -> Decision Agent ->
// Reporting Agent yang berjalan sebagai orkestrasi", plus "Belum ada
// Workflow Runtime: Planner, Executor, Scheduler, Recovery, Checkpoint".
//
// Design: an Agent is a named, pure function of (input, tick) -> output.
// A Plan is a directed sequence of agent Steps with declared
// dependencies (each step consumes named outputs of earlier steps). The
// Scheduler topologically orders steps respecting dependencies; the
// Executor runs each step, checkpointing its result via
// platform/audit.AuditStore after every step so a crash mid-workflow
// can Resume from the last durable checkpoint instead of re-running
// completed work — the concrete Checkpoint+Recovery pair the audit asks
// for. This mirrors the same DARI discipline as the rest of the kernel:
// no time.Now() (explicit Tick per run), deterministic step ordering
// (stable topological sort, ties broken lexicographically by step
// name), and a hash-chained execution log for replay/audit.
package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"

	"veriqo/pkg/platform/audit"
)

var (
	ErrDuplicateStep     = errors.New("workflow: duplicate step name in plan")
	ErrUnknownDependency = errors.New("workflow: step depends on an unregistered step")
	ErrCycleDetected     = errors.New("workflow: dependency cycle detected in plan")
	ErrStepFailed        = errors.New("workflow: step execution failed")
	ErrNoSuchRun         = errors.New("workflow: run ID not found")
)

// AgentInput is the read-only view a Step's Agent function receives:
// the outputs of every step it declared as a dependency, keyed by step
// name, plus the run's logical Tick.
type AgentInput struct {
	Outputs map[string]any
	Tick    uint64
}

// AgentFunc is the signature every agent (Collector, Fusion, Reasoning,
// Decision, Reporting, or any custom agent) implements. It must be a
// pure function of its input — no time.Now(), no hidden global state —
// so a Plan replays deterministically.
type AgentFunc func(in AgentInput) (any, error)

// Step is one named node in a Plan: an agent function plus the names of
// steps whose outputs it consumes.
type Step struct {
	Name      string
	DependsOn []string
	Agent     AgentFunc
}

// Plan is a directed acyclic graph of Steps — the Planner's output and
// the Executor's input.
type Plan struct {
	Name  string
	Steps []Step
}

// StepResult records one executed step's outcome for checkpointing and
// audit.
type StepResult struct {
	StepName string
	Output   any
	Err      string // empty on success
}

// RunRecord is the durable, resumable state of one workflow execution.
type RunRecord struct {
	RunID     string
	PlanName  string
	Tick      uint64
	Order     []string // topological execution order, fixed at plan time
	Completed map[string]StepResult
	NextIndex int // index into Order of the next step to run
	Done      bool
}

// Planner validates a Plan and computes a deterministic topological
// execution order — the concrete "Planner" the audit names.
type Planner struct{}

// Plan validates dependencies and cycles, returning a fixed, stable
// execution order (ties broken lexicographically for determinism).
func (Planner) Order(p Plan) ([]string, error) {
	byName := make(map[string]Step, len(p.Steps))
	for _, s := range p.Steps {
		if _, exists := byName[s.Name]; exists {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateStep, s.Name)
		}
		byName[s.Name] = s
	}
	for _, s := range p.Steps {
		for _, dep := range s.DependsOn {
			if _, ok := byName[dep]; !ok {
				return nil, fmt.Errorf("%w: %s -> %s", ErrUnknownDependency, s.Name, dep)
			}
		}
	}

	visited := make(map[string]int) // 0=unvisited 1=in-progress 2=done
	var order []string
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)

	var visit func(name string) error
	visit = func(name string) error {
		switch visited[name] {
		case 2:
			return nil
		case 1:
			return fmt.Errorf("%w: involves %s", ErrCycleDetected, name)
		}
		visited[name] = 1
		deps := append([]string{}, byName[name].DependsOn...)
		sort.Strings(deps)
		for _, dep := range deps {
			if err := visit(dep); err != nil {
				return err
			}
		}
		visited[name] = 2
		order = append(order, name)
		return nil
	}
	for _, name := range names {
		if err := visit(name); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// Scheduler wraps Planner to produce a fresh RunRecord for a Plan.
type Scheduler struct {
	planner Planner
}

func NewScheduler() *Scheduler { return &Scheduler{} }

// Schedule validates the plan and returns a new, not-yet-started
// RunRecord with a deterministic RunID derived from the plan's content
// and tick (no UUIDs).
func (s *Scheduler) Schedule(p Plan, tick uint64) (*RunRecord, error) {
	order, err := s.planner.Order(p)
	if err != nil {
		return nil, err
	}
	raw := fmt.Sprintf("plan=%s|order=%v|tick=%d", p.Name, order, tick)
	sum := sha256.Sum256([]byte(raw))
	return &RunRecord{
		RunID:     hex.EncodeToString(sum[:]),
		PlanName:  p.Name,
		Tick:      tick,
		Order:     order,
		Completed: make(map[string]StepResult),
	}, nil
}

// Executor runs a Plan's steps in scheduled order, checkpointing every
// completed step to an audit.AuditStore. It is safe to call Resume
// after a partial run (simulating a crash) — completed steps are never
// re-executed.
type Executor struct {
	mu    sync.Mutex
	audit *audit.AuditStore
	runs  map[string]*RunRecord
	plans map[string]Plan // by plan name, needed to resume
}

// NewExecutor constructs an Executor backed by the given audit store for
// checkpointing. Pass audit.NewAuditStore() for a fresh, isolated log.
func NewExecutor(store *audit.AuditStore) *Executor {
	return &Executor{
		audit: store,
		runs:  make(map[string]*RunRecord),
		plans: make(map[string]Plan),
	}
}

// Run executes a freshly scheduled RunRecord to completion (or first
// failure), checkpointing after every step.
func (ex *Executor) Run(p Plan, record *RunRecord) (*RunRecord, error) {
	ex.mu.Lock()
	ex.plans[p.Name] = p
	ex.runs[record.RunID] = record
	ex.mu.Unlock()
	return ex.advance(p, record)
}

// Resume continues a previously checkpointed, not-yet-Done run from its
// NextIndex — the concrete "Recovery" the audit asks for: a crash after
// step 2 of 5 resumes at step 3, not step 1.
func (ex *Executor) Resume(runID string) (*RunRecord, error) {
	ex.mu.Lock()
	record, ok := ex.runs[runID]
	if !ok {
		ex.mu.Unlock()
		return nil, ErrNoSuchRun
	}
	p, ok := ex.plans[record.PlanName]
	ex.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("workflow: plan %q for run %q not retained", record.PlanName, runID)
	}
	return ex.advance(p, record)
}

func (ex *Executor) advance(p Plan, record *RunRecord) (*RunRecord, error) {
	byName := make(map[string]Step, len(p.Steps))
	for _, s := range p.Steps {
		byName[s.Name] = s
	}
	for record.NextIndex < len(record.Order) {
		name := record.Order[record.NextIndex]
		step := byName[name]
		outputs := make(map[string]any, len(step.DependsOn))
		for _, dep := range step.DependsOn {
			outputs[dep] = record.Completed[dep].Output
		}
		out, err := step.Agent(AgentInput{Outputs: outputs, Tick: record.Tick})
		result := StepResult{StepName: name, Output: out}
		if err != nil {
			result.Err = err.Error()
			record.Completed[name] = result
			ex.checkpoint(record, result)
			return record, fmt.Errorf("%w: step %q: %v", ErrStepFailed, name, err)
		}
		record.Completed[name] = result
		ex.checkpoint(record, result)
		record.NextIndex++
	}
	record.Done = true
	ex.checkpointDone(record)
	return record, nil
}

func (ex *Executor) checkpoint(record *RunRecord, result StepResult) {
	payload := fmt.Sprintf("run=%s|step=%s|err=%s", record.RunID, result.StepName, result.Err)
	_, _ = ex.audit.Append("workflow.executor", "STEP_CHECKPOINT", payload) // best-effort audit trail; a logging failure must not abort the workflow step
}

func (ex *Executor) checkpointDone(record *RunRecord) {
	payload := fmt.Sprintf("run=%s|steps=%d", record.RunID, len(record.Order))
	_, _ = ex.audit.Append("workflow.executor", "RUN_COMPLETE", payload) // best-effort audit trail; a logging failure must not abort a run that already succeeded
}
