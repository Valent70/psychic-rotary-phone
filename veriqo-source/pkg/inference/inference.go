// Package inference implements VTECP-001 Capability 5's remaining gap:
// a generic InferenceTrace record usable at ANY AI/model call site.
//
// This is deliberately the only new type this capability needs.
// Everything else Capability 5 asks for already exists and is reused,
// not duplicated:
//
//   - Model identity, version, and the ACTIVE/APPROVED/CALIBRATED
//     lifecycle gate: pkg/governance/lifecycle.Registry/Model/Binding.
//   - The workflow that stops a machine's output from becoming final
//     authority on its own: pkg/governance/hitl.MachineDecision +
//     GovernedOutcome.
//   - Model-risk-management metadata and fail-closed calibration
//     gating: pkg/governance/calibration.TemporalContract.
//   - Deterministic, replayable inference math for the one domain that
//     already has it: pkg/moat/hbayes.InferenceTrace (HMM-specific --
//     this package's own InferenceTrace is intentionally a different,
//     domain-generic type, not a replacement for it).
//   - Traceable rationale: pkg/explanation.DecisionExplanation.
//
// What was genuinely missing: a small, generic record of "this model
// version, given this input, produced this output at this confidence,
// at this tick" -- usable by any AI call site in the repository, not
// just the Bayesian temporal engine. Recorder is that record's only
// writer, and it enforces VTECP-001's core Capability 5 rule
// structurally rather than by convention: Record refuses to accept a
// trace for a model that is not ACTIVE, per lifecycle.Registry, at the
// claimed tick. An inference from a DRAFT, unapproved, deprecated, or
// unknown model version is not silently recorded as equally
// trustworthy -- it is rejected. "AI output never becomes evidence
// authority directly" is enforced here by refusing to let ungoverned
// AI output enter the audit trail as a trace at all, not by adding a
// footnote after the fact.
package inference

import (
	"errors"
	"fmt"
	"sync"

	"veriqo/pkg/canonical/jcs"
	"veriqo/pkg/governance/lifecycle"
	"veriqo/pkg/platform/audit"
)

var (
	// ErrModelNotActive is Recorder.Record's core refusal: the named
	// model is not the ACTIVE version of its model ID at the claimed
	// tick, per lifecycle.Registry.BindingAt. Its output cannot be
	// recorded as a trusted inference.
	ErrModelNotActive = errors.New("inference: model is not ACTIVE at this tick; ungoverned AI output cannot be recorded as a trusted inference")
	// ErrEmptyModelKey rejects a trace with no model identity at all.
	ErrEmptyModelKey = errors.New("inference: ModelKey must be non-empty")
	// ErrEmptyInputHash rejects a trace that does not commit to what it
	// ran on -- an inference record with no input commitment cannot
	// later be checked against a replay.
	ErrEmptyInputHash = errors.New("inference: InputHash must be non-empty")
	// ErrConfidenceOutOfRange rejects a confidence value outside [0,1].
	ErrConfidenceOutOfRange = errors.New("inference: Confidence must be in [0,1]")
	// ErrHashMismatch is VerifyTraceHash's failure.
	ErrHashMismatch = errors.New("inference: trace hash mismatch")
)

// InferenceTrace is one AI/model inference call, recorded for audit.
// ModelKey is a pkg/governance/lifecycle.Model.Key() value ("id@version"),
// tying every trace back to a real, governed, versioned model record --
// never a bare free-text model name. Output is a caller-supplied
// summary or serialization of what the model produced, never a
// verdict/liability field: this package makes no claim about what the
// output MEANS, only that it was produced, by what, from what input,
// when, and at what confidence.
type InferenceTrace struct {
	TraceID    string  `json:"trace_id"`
	ModelKey   string  `json:"model_key"`
	InputHash  string  `json:"input_hash"`
	Output     string  `json:"output"`
	Confidence float64 `json:"confidence"`
	Actor      string  `json:"actor"`
	Purpose    string  `json:"purpose,omitempty"`
	Tick       uint64  `json:"tick"`
	Hash       string  `json:"hash"`
}

func computeTraceHash(t InferenceTrace) string {
	t.Hash = ""
	return jcs.MustHash(t)
}

// VerifyTraceHash re-derives t's Hash and reports whether it matches
// the recorded value.
func VerifyTraceHash(t InferenceTrace) error {
	want := computeTraceHash(t)
	if want != t.Hash {
		return fmt.Errorf("%w: recorded=%s recomputed=%s", ErrHashMismatch, t.Hash, want)
	}
	return nil
}

// Recorder is the only writer of InferenceTrace records. It is bound
// to a lifecycle.Registry at construction: Recorder has no notion of
// model governance of its own, and never invents one -- it asks the
// real registry.
type Recorder struct {
	mu         sync.Mutex
	registry   *lifecycle.Registry
	traces     []InferenceTrace
	AuditStore *audit.AuditStore
}

// NewRecorder binds a Recorder to reg. reg must not be nil: a Recorder
// with no governance registry to check against would have no way to
// enforce this package's entire reason for existing.
func NewRecorder(reg *lifecycle.Registry) *Recorder {
	if reg == nil {
		panic("inference: NewRecorder requires a non-nil lifecycle.Registry")
	}
	return &Recorder{registry: reg}
}

// AttachAuditStore opts this recorder into audit mirroring, matching
// ontology.Registry and authz.Engine's own opt-in discipline.
func (r *Recorder) AttachAuditStore(store *audit.AuditStore) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.AuditStore = store
}

// Record accepts one inference call for the named model, IF AND ONLY
// IF that model is the ACTIVE version of its model ID at tick, per the
// bound lifecycle.Registry. TraceID is derived deterministically via
// jcs.Hash over the trace's own content (never a random UUID, per
// VTECP-001 LAW-03): recording the identical inference at the
// identical tick twice produces the identical TraceID, the same
// idempotency discipline already established for authz.PolicyDecision.
func (r *Recorder) Record(modelKey, inputHash, output string, confidence float64, actor, purpose string, tick uint64) (InferenceTrace, error) {
	if modelKey == "" {
		return InferenceTrace{}, ErrEmptyModelKey
	}
	if inputHash == "" {
		return InferenceTrace{}, ErrEmptyInputHash
	}
	if confidence < 0 || confidence > 1 {
		return InferenceTrace{}, fmt.Errorf("%w: %v", ErrConfidenceOutOfRange, confidence)
	}

	binding := r.registry.BindingAt(tick)
	active := false
	for _, m := range binding.Models {
		if m == modelKey {
			active = true
			break
		}
	}
	if !active {
		return InferenceTrace{}, fmt.Errorf("%w: %s", ErrModelNotActive, modelKey)
	}

	t := InferenceTrace{
		ModelKey: modelKey, InputHash: inputHash, Output: output,
		Confidence: confidence, Actor: actor, Purpose: purpose, Tick: tick,
	}
	inputsHash := jcs.MustHash(t)
	t.TraceID = "trace:" + inputsHash
	t.Hash = computeTraceHash(t)

	r.mu.Lock()
	r.traces = append(r.traces, t)
	store := r.AuditStore
	r.mu.Unlock()

	if store != nil {
		payload, err := jcs.Canonicalize(t)
		if err != nil {
			return t, fmt.Errorf("inference: encoding trace for audit: %w", err)
		}
		if _, err := store.Append("INFERENCE:"+actor, "InferenceTrace", string(payload)); err != nil {
			return t, fmt.Errorf("inference: audit ledger append failed: %w", err)
		}
	}
	return t, nil
}

// Traces returns every trace this Recorder has accepted, oldest first.
func (r *Recorder) Traces() []InferenceTrace {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]InferenceTrace(nil), r.traces...)
}
