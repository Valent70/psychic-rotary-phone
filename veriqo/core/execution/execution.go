// Package execution is Veriqo's Execution layer (renamed from the
// prior session's "orchestrator" package — no behavior change, only
// the naming consolidation "Yang_masih_saya_kritisi.docx" §1 asks for:
// "Core Runtime, Knowledge, Evidence, Intelligence, Execution. Sudah
// cukup."). It drives a Pipeline of Steps over veriqo/core/runtime's
// Engine contract: dependency-resolved waves run concurrently, a
// failing step retries up to MaxRetries times, and a step that still
// fails triggers saga-style compensation of every already-succeeded
// step in reverse order. Every attempt of every step is recorded in an
// ExecutionTrace.
package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"veriqo/pkg/platform/telemetry"
	"veriqo/veriqo/core/runtime"
)

type StepStatus string

const (
	StepSucceeded   StepStatus = "succeeded"
	StepFailed      StepStatus = "failed"
	StepRetried     StepStatus = "retried"
	StepCompensated StepStatus = "compensated"
)

// ExecutionContext carries accumulated step results plus the pipeline's
// initial input.
type ExecutionContext struct {
	mu      sync.RWMutex
	Input   map[string]string
	Results map[string]map[string]string
}

func newExecutionContext(input map[string]string) *ExecutionContext {
	return &ExecutionContext{Input: input, Results: make(map[string]map[string]string)}
}

func (c *ExecutionContext) Get(step, key string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	r, ok := c.Results[step]
	if !ok {
		return "", false
	}
	v, ok := r[key]
	return v, ok
}

func (c *ExecutionContext) set(step string, result map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Results[step] = result
}

type ArgsFunc func(ctx *ExecutionContext) map[string]string
type CompensateFunc func(ctx *ExecutionContext) error

// Step is one node in the pipeline DAG.
type Step struct {
	Name       string
	Capability runtime.Capability
	Method     string
	Args       ArgsFunc
	DependsOn  []string
	MaxRetries int
	Compensate CompensateFunc
}

type Pipeline struct {
	Name  string
	Steps []Step
}

type Attempt struct {
	StepName string
	Attempt  int
	Status   StepStatus
	Err      string
	Duration time.Duration
	Result   map[string]string
}

type ExecutionTrace struct {
	PipelineName string
	Attempts     []Attempt
	Succeeded    bool
	mu           sync.Mutex
}

func (t *ExecutionTrace) record(a Attempt) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Attempts = append(t.Attempts, a)
}

// CanonicalHash is the v7.10.2 gap-closure fix for a determinism risk
// the V7.10.1 technical audit flagged preventively: Duration is
// wall-clock (time.Now()/time.Since()), so if ExecutionTrace were ever
// folded into replayed/hashed canonical evidence (as pkg/canonical
// does for the OS's single execution path), a naive hash over the
// whole struct would make "the same logical execution" hash
// differently on every run purely from scheduling jitter — the same
// class of bug the belief ledger (pkg/core/trustcalc) and provenance
// graph were already hardened against. CanonicalHash hashes only the
// logically-deterministic fields (pipeline name, per-attempt step
// name/attempt number/status/error/result — never Duration or any
// wall-clock value) so two runs of the identical pipeline over
// identical inputs always produce an identical hash regardless of how
// long each attempt actually took. Duration itself is untouched and
// remains available on Attempt for operational telemetry/SLO
// reporting — this method simply gives callers a way to prove
// execution-outcome equality that wall-clock jitter cannot break.
func (t *ExecutionTrace) CanonicalHash() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	h := sha256.New()
	h.Write([]byte(t.PipelineName))
	h.Write([]byte{0})
	h.Write([]byte(strconv.FormatBool(t.Succeeded)))
	for _, a := range t.Attempts {
		h.Write([]byte{0})
		h.Write([]byte(a.StepName))
		h.Write([]byte{0})
		h.Write([]byte(strconv.Itoa(a.Attempt)))
		h.Write([]byte{0})
		h.Write([]byte(a.Status))
		h.Write([]byte{0})
		h.Write([]byte(a.Err))
		if len(a.Result) > 0 {
			keys := make([]string, 0, len(a.Result))
			for k := range a.Result {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				h.Write([]byte{0})
				h.Write([]byte(k))
				h.Write([]byte{'='})
				h.Write([]byte(a.Result[k]))
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

var (
	ErrCycle          = fmt.Errorf("execution: dependency cycle in pipeline")
	ErrUnknownDep     = fmt.Errorf("execution: step depends on unknown step")
	ErrNoEngineForCap = fmt.Errorf("execution: no healthy engine found for capability")
)

// Orchestrator executes Pipelines against a shared runtime.Manager.
type Orchestrator struct {
	Manager *runtime.Manager
}

func New(m *runtime.Manager) *Orchestrator { return &Orchestrator{Manager: m} }

func levels(steps []Step) ([][]Step, error) {
	byName := make(map[string]Step, len(steps))
	for _, s := range steps {
		byName[s.Name] = s
	}
	for _, s := range steps {
		for _, d := range s.DependsOn {
			if _, ok := byName[d]; !ok {
				return nil, fmt.Errorf("%w: %q -> %q", ErrUnknownDep, s.Name, d)
			}
		}
	}

	resolved := make(map[string]int)
	var order []string
	for _, s := range steps {
		order = append(order, s.Name)
	}
	sort.Strings(order)

	changed := true
	for iterations := 0; changed && iterations <= len(steps)+1; iterations++ {
		changed = false
		for _, name := range order {
			if _, done := resolved[name]; done {
				continue
			}
			s := byName[name]
			maxDepLevel := -1
			ready := true
			for _, d := range s.DependsOn {
				lvl, ok := resolved[d]
				if !ok {
					ready = false
					break
				}
				if lvl > maxDepLevel {
					maxDepLevel = lvl
				}
			}
			if !ready {
				continue
			}
			resolved[name] = maxDepLevel + 1
			changed = true
		}
	}
	if len(resolved) != len(steps) {
		return nil, ErrCycle
	}

	maxLevel := 0
	for _, lvl := range resolved {
		if lvl > maxLevel {
			maxLevel = lvl
		}
	}
	waves := make([][]Step, maxLevel+1)
	for _, name := range order {
		lvl := resolved[name]
		waves[lvl] = append(waves[lvl], byName[name])
	}
	return waves, nil
}

func (o *Orchestrator) Execute(p Pipeline, input map[string]string) (*ExecutionTrace, error) {
	_, span := telemetry.StartSpan(context.Background(), "execution.Execute", telemetry.Attribute{Key: "pipeline", Value: p.Name})
	defer span.End()

	waves, err := levels(p.Steps)
	if err != nil {
		return nil, err
	}
	ctx := newExecutionContext(input)
	trace := &ExecutionTrace{PipelineName: p.Name, Succeeded: true}
	var succeededOrder []Step

	for _, wave := range waves {
		type outcome struct {
			step Step
			err  error
		}
		results := make(chan outcome, len(wave))
		var wg sync.WaitGroup
		for _, s := range wave {
			wg.Add(1)
			go func(s Step) {
				defer wg.Done()
				err := o.runStepWithRetry(s, ctx, trace)
				results <- outcome{step: s, err: err}
			}(s)
		}
		wg.Wait()
		close(results)

		var waveErr error
		for r := range results {
			if r.err != nil {
				waveErr = r.err
			} else {
				succeededOrder = append(succeededOrder, r.step)
			}
		}
		if waveErr != nil {
			trace.Succeeded = false
			o.compensate(succeededOrder, ctx, trace)
			return trace, waveErr
		}
	}
	return trace, nil
}

func (o *Orchestrator) runStepWithRetry(s Step, ctx *ExecutionContext, trace *ExecutionTrace) error {
	names := o.Manager.Discover(s.Capability)
	if len(names) == 0 {
		trace.record(Attempt{StepName: s.Name, Attempt: 1, Status: StepFailed, Err: ErrNoEngineForCap.Error()})
		return fmt.Errorf("%w: %s", ErrNoEngineForCap, s.Capability)
	}
	engineName := names[0]

	maxAttempts := s.MaxRetries + 1
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var args map[string]string
		if s.Args != nil {
			args = s.Args(ctx)
		}
		start := time.Now()
		result, err := o.Manager.Invoke(engineName, s.Method, args)
		dur := time.Since(start)
		if err == nil {
			status := StepSucceeded
			if attempt > 1 {
				status = StepRetried
			}
			trace.record(Attempt{StepName: s.Name, Attempt: attempt, Status: status, Duration: dur, Result: result})
			ctx.set(s.Name, result)
			return nil
		}
		lastErr = err
		trace.record(Attempt{StepName: s.Name, Attempt: attempt, Status: StepFailed, Err: err.Error(), Duration: dur})
	}
	return fmt.Errorf("step %q: exhausted %d attempts: %w", s.Name, maxAttempts, lastErr)
}

func (o *Orchestrator) compensate(succeeded []Step, ctx *ExecutionContext, trace *ExecutionTrace) {
	for i := len(succeeded) - 1; i >= 0; i-- {
		s := succeeded[i]
		if s.Compensate == nil {
			continue
		}
		if err := s.Compensate(ctx); err != nil {
			trace.record(Attempt{StepName: s.Name, Status: StepFailed, Err: "compensate: " + err.Error()})
			continue
		}
		trace.record(Attempt{StepName: s.Name, Status: StepCompensated})
	}
}
