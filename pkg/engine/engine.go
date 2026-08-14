// Package engine implements Veriqo's Unified Engine System — closing
// the gap named in "Hal_yang_sangat_krusial.docx" section 1: "Yang
// belum saya lihat adalah sebuah Unified Engine Orchestrator ... Semua
// engine memiliki lifecycle yang sama ... Saya belum melihat pola ini
// benar-benar dibakukan."
//
// This package defines the standardized six-stage lifecycle the audit
// doc names (Initialize -> LoadContext -> Evaluate -> GenerateEvidence
// -> Publish -> Replayable) as a single Engine interface, and a Kernel
// that registers and runs any number of engines through it uniformly.
// Two real, already-existing engines from this repository
// (contradiction.ArbitrationEngine, decision.Engine) are wrapped as
// concrete Engine implementations in adapters.go to prove this is a
// genuine orchestration layer over real logic, not an empty interface.
//
// Honest scope statement: not every engine package in this repository
// (causal, digitaltwin, hbayes, temporal) has an adapter here yet —
// three representative, real adapters (contradiction, decision, and as
// of this session fusion) are provided, each also wired into the
// Independent Verification Framework via IVFCapable (see verify.go);
// wrapping the remainder is mechanical repetition of the same pattern
// and is listed as an explicit open item in the session report rather
// than padded out here for its own sake.
package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"veriqo/pkg/platform/observability"
	"veriqo/pkg/platform/telemetry"
)

var (
	ErrUnknownEngine   = errors.New("engine: no engine registered under this name")
	ErrDuplicateEngine = errors.New("engine: an engine is already registered under this name")
	ErrNotInitialized  = errors.New("engine: Run called before Initialize")
)

// Context is Stage 2 (LoadContext)'s input/output: the subject and raw
// input data an engine evaluates, plus the logical tick — deliberately
// generic (map[string]any-free; a flat string-keyed float map plus a
// single Subject/Value pair) so this package needs no dependency on any
// specific engine's domain types.
type Context struct {
	Subject string
	Values  map[string]float64
	Tick    uint64
}

// Evidence is Stage 4 (GenerateEvidence)'s output: a generic record an
// engine produces, ready to hand to pkg/evidence/graph or
// pkg/verification without this package depending on either.
type Evidence struct {
	Kind    string
	Payload string
}

// Result is what Stage 3 (Evaluate) + Stage 4 (GenerateEvidence) produce
// together — the engine's computed outcome plus its evidentiary trail.
type Result struct {
	Summary  string
	Score    float64
	Evidence []Evidence
}

// Engine is the standardized lifecycle every registered engine
// implements — the exact six stages named in the audit doc, expressed
// as Go methods so the Kernel can run any engine identically.
type Engine interface {
	// Name identifies this engine (e.g. "contradiction", "decision").
	Name() string
	// Initialize (Stage 1) prepares the engine for use. Called once by
	// the Kernel at registration time.
	Initialize() error
	// LoadContext (Stage 2) accepts the input for one evaluation run.
	LoadContext(ctx Context) error
	// Evaluate (Stage 3) computes the engine's outcome from the
	// most-recently-loaded context.
	Evaluate() (Result, error)
	// Publish (Stage 5) is called by the Kernel after a successful
	// Evaluate, to let the engine perform any side-effecting
	// publication (e.g. appending to its own hash-chained ledger — the
	// side effect happens HERE, not in Evaluate, keeping Evaluate pure
	// and Publish the single, explicit point where state changes).
	Publish(Result) error
	// Replayable (Stage 6) reports whether this engine's last published
	// result can be independently reconstructed from stored state (the
	// engine's own VerifyChain()/Rebuild()-style guarantee, surfaced
	// through the uniform interface so the Kernel can report on it
	// without knowing each engine's concrete replay mechanism).
	Replayable() bool
}

// RunRecord is the Kernel's own audit trail of one engine run through
// the full lifecycle — the orchestrator's own evidence, distinct from
// (and referencing) each engine's individual Result.
type RunRecord struct {
	Index      uint64
	EngineName string
	Subject    string
	Tick       uint64
	Result     Result
	Replayable bool
}

// stageForEngine maps a registered engine's Name() to the ITIOS
// pipeline stage its latency should be attributed to, per the
// Intent -> Evidence -> Verification -> Policy -> Trust -> Decision ->
// Replay pipeline named throughout this project's reports. Engines this
// mapping does not recognize fall back to StageDecision (the Unified
// Engine Orchestrator's own default stage) rather than being silently
// unmeasured.
func stageForEngine(name string) observability.MOATStage {
	switch name {
	case "contradiction", "fusion":
		return observability.StageEvidence
	case "decision":
		return observability.StageDecision
	default:
		return observability.StageDecision
	}
}

// Kernel is the Unified Engine Orchestrator: registers engines and runs
// them through the standardized lifecycle, keeping its own append-only
// record of every run.
type Kernel struct {
	mu          sync.Mutex
	engines     map[string]Engine
	initialized map[string]bool
	runs        []RunRecord
	metrics     *observability.MOATMetrics // optional; nil-safe everywhere it's used
}

func NewKernel() *Kernel {
	return &Kernel{
		engines:     make(map[string]Engine),
		initialized: make(map[string]bool),
	}
}

// SetMetrics attaches a MOATMetrics surface (pkg/platform/observability)
// to this Kernel. Once attached, every Run wraps the engine's Evaluate
// call in a real latency span, and RunAndVerify additionally records
// VerificationScore and a replay-success sample — closing the "not yet
// wired into these specific call sites" gap named in this session's own
// closure report §5. Passing nil (the zero-value default) disables all
// of this with no behavior change, so existing callers are unaffected.
func (k *Kernel) SetMetrics(m *observability.MOATMetrics) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.metrics = m
}

// Register adds an engine under its own Name(). Does not call
// Initialize — that happens on first Run, or explicitly via
// InitializeAll, so registration order never implies initialization
// order dependencies.
func (k *Kernel) Register(e Engine) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	name := e.Name()
	if _, exists := k.engines[name]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateEngine, name)
	}
	k.engines[name] = e
	return nil
}

// InitializeAll runs Stage 1 for every registered engine, in
// deterministic (sorted-name) order.
func (k *Kernel) InitializeAll() error {
	k.mu.Lock()
	defer k.mu.Unlock()
	names := k.sortedNamesLocked()
	for _, name := range names {
		if k.initialized[name] {
			continue
		}
		if err := k.engines[name].Initialize(); err != nil {
			return fmt.Errorf("engine: initialize %q: %w", name, err)
		}
		k.initialized[name] = true
	}
	return nil
}

func (k *Kernel) sortedNamesLocked() []string {
	names := make([]string, 0, len(k.engines))
	for n := range k.engines {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Run executes the full Stage 2-6 lifecycle for one named engine against
// one Context: LoadContext -> Evaluate -> Publish -> Replayable, and
// appends a RunRecord to the kernel's own audit trail. Initialize is
// called automatically on first use of an engine if InitializeAll was
// not called explicitly.
func (k *Kernel) Run(engineName string, ctx Context) (RunRecord, error) {
	_, telSpan := telemetry.StartSpan(context.Background(), "engine.Run",
		telemetry.Attribute{Key: "engine_name", Value: engineName})
	defer telSpan.End()

	k.mu.Lock()
	e, ok := k.engines[engineName]
	if !ok {
		k.mu.Unlock()
		return RunRecord{}, fmt.Errorf("%w: %s", ErrUnknownEngine, engineName)
	}
	if !k.initialized[engineName] {
		if err := e.Initialize(); err != nil {
			k.mu.Unlock()
			return RunRecord{}, fmt.Errorf("engine: initialize %q: %w", engineName, err)
		}
		k.initialized[engineName] = true
	}
	k.mu.Unlock()

	k.mu.Lock()
	metrics := k.metrics
	k.mu.Unlock()
	var span *observability.Span
	if metrics != nil {
		span = metrics.StartStage(stageForEngine(engineName))
		span.SetTag("engine", engineName)
	}

	if err := e.LoadContext(ctx); err != nil {
		if span != nil {
			span.Finish()
		}
		return RunRecord{}, fmt.Errorf("engine: load context for %q: %w", engineName, err)
	}
	result, err := e.Evaluate()
	if err != nil {
		if span != nil {
			span.Finish()
		}
		return RunRecord{}, fmt.Errorf("engine: evaluate %q: %w", engineName, err)
	}
	if err := e.Publish(result); err != nil {
		if span != nil {
			span.Finish()
		}
		return RunRecord{}, fmt.Errorf("engine: publish %q: %w", engineName, err)
	}
	replayable := e.Replayable()
	if span != nil {
		span.Finish()
	}
	// Result.Score is each adapter's own already-computed confidence/
	// risk figure (see adapters.go) — surfaced here as
	// DecisionConfidence per Kritik 8, not a second, independently
	// computed number.
	if metrics != nil {
		metrics.SetDecisionConfidence(result.Score)
	}

	k.mu.Lock()
	defer k.mu.Unlock()
	rec := RunRecord{
		Index: uint64(len(k.runs)) + 1, EngineName: engineName, Subject: ctx.Subject,
		Tick: ctx.Tick, Result: result, Replayable: replayable,
	}
	k.runs = append(k.runs, rec)
	return rec, nil
}

// RunAll executes Run for every registered engine (sorted order) against
// the SAME Context — the common "one piece of evidence, every relevant
// engine reacts to it" orchestration pattern. Engines that error are
// recorded in the returned error map rather than aborting the whole
// pass, so one engine's failure never silently blocks the others.
func (k *Kernel) RunAll(ctx Context) ([]RunRecord, map[string]error) {
	k.mu.Lock()
	names := k.sortedNamesLocked()
	k.mu.Unlock()

	var records []RunRecord
	errs := make(map[string]error)
	for _, name := range names {
		rec, err := k.Run(name, ctx)
		if err != nil {
			errs[name] = err
			continue
		}
		records = append(records, rec)
	}
	return records, errs
}

// Runs returns a defensive copy of the kernel's own audit trail.
func (k *Kernel) Runs() []RunRecord {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := make([]RunRecord, len(k.runs))
	copy(out, k.runs)
	return out
}

// EngineNames returns every registered engine's name, sorted.
func (k *Kernel) EngineNames() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.sortedNamesLocked()
}
