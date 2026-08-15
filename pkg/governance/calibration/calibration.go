// Package calibration is the "Evidence -> Probabilistic Observation
// Contract" a fresh audit (V7.12.7, item P0-C) asked VERIQO to build:
// pkg/moat/hbayes has a real, tested temporal Bayesian inference
// engine (forward filtering, backward smoothing, decay, causal edges),
// but nothing in the pipeline could turn a real evidence assertion
// (a claim like AIS_STATUS="OFF") into an hbayes.Observation's
// required Likelihood distribution over hidden states, because no
// deterministic mapping from evidence value to hidden-state likelihood
// existed anywhere -- and inventing one on the spot would be exactly
// the fabrication earlier rounds correctly refused to do.
//
// This package is the fix for the right half of that sentence: it is
// the CONTRACT that makes fabrication structurally impossible, not a
// source of real probabilities. A LikelihoodTable may only be
// registered with a full CalibrationRecord naming where its numbers
// came from (calibration_source, model_version, effective_tick,
// dataset_provenance) -- the exact five fields the audit named -- and
// BuildObservation refuses, fails closed, to produce an
// hbayes.Observation for any (predicate, value) that has no registered
// table. Nothing here can silently default to a plausible-looking
// distribution.
//
// What this does NOT do: it does not supply real calibration numbers.
// Populating a real LikelihoodTable requires a real historical dataset
// and a real calibration process (fitting P(evidence|state) from
// labelled history) -- external data acquisition work of the same
// category as the eight blockers in pkg/blockers, not a code gap. The
// tests in this package register a clearly-labelled FIXTURE table
// (mirroring test/integration/dark_vessel_test.go's own "synthetic,
// clearly labeled" convention) to prove the contract's machinery is
// real; they do not claim that fixture is production-calibrated data.
//
// A second, DISTINCT gap an external audit's own words named precisely
// ("Bayesian mechanism sudah ada. Tetapi production calibration corpus
// dan temporal observation stream belum tersedia."): even with a real
// LikelihoodTable registered, no production caller of
// pkg/lifecycle.Orchestrator.RunUnified currently wires evidence
// submissions THROUGH BuildObservation into pkg/execution's
// TemporalModel/TemporalObservations inputs -- confirmed by
// pkg/lifecycle's own TestRunUnifiedTemporalBayesianStageIsHonestly-
// SkippedInProduction, which asserts the real production path's
// TEMPORAL_BAYESIAN stage records SKIPPED today, not a fabricated
// result. That wiring is real, closable code work (map
// canonical.CaseInput.Submissions to BuildObservation calls, assemble
// a temporal Model, thread both into execution.Input), deliberately
// NOT attempted in this package: doing it without a real caller's
// actual observation cadence and state-space requirements to design
// against would risk inventing exactly the kind of plausible-looking
// placeholder this package's whole contract exists to make impossible.
// Keep the two gaps distinct when reporting status: the CORPUS is
// externally blocked (data acquisition); the WIRING is not blocked on
// anything but engineering time once a real caller's shape is known.
package calibration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"veriqo/pkg/moat/hbayes"
	"veriqo/pkg/platform/telemetry"
)

// Errors.
var (
	ErrMissingCalibrationSource = errors.New("calibration: CalibrationSource is required")
	ErrMissingModelVersion      = errors.New("calibration: ModelVersion is required")
	ErrMissingDatasetProvenance = errors.New("calibration: DatasetProvenance is required")
	ErrEmptyPrior               = errors.New("calibration: Prior must declare at least one state")
	ErrEmptyLikelihoodTable     = errors.New("calibration: LikelihoodTable must map at least one value")
	ErrInvalidLikelihood        = errors.New("calibration: likelihood values must be real probabilities in [0,1]")
	ErrNoCalibration            = errors.New("calibration: no registered, provenanced likelihood table for this predicate/value")
	ErrUnregisteredState        = errors.New("calibration: likelihood entry names a state outside this table's declared Prior")
)

// CalibrationRecord is the audit's own required provenance for any
// probability entering the temporal Bayesian pipeline. All five fields
// are mandatory; Validate refuses a record missing any of them.
type CalibrationRecord struct {
	// CalibrationSource names WHERE these numbers came from -- a real
	// dataset identifier, a named calibration run, a vendor study.
	// Never blank, never "TODO", never invented.
	CalibrationSource string `json:"calibration_source"`
	// ModelVersion binds this table to one exact version of the hidden-
	// state model it was fit against, so a model change can't silently
	// reuse stale likelihoods.
	ModelVersion string `json:"model_version"`
	// Prior is the model's declared prior over hidden states -- part of
	// the contract, not an implementation detail, because a likelihood
	// table is only meaningful relative to the prior it was calibrated
	// against.
	Prior map[hbayes.State]float64 `json:"prior"`
	// EffectiveTick is the logical tick from which this calibration is
	// valid; a table calibrated for one operating regime is not silently
	// applied to evidence from before it existed.
	EffectiveTick uint64 `json:"effective_tick"`
	// DatasetProvenance identifies the underlying calibration dataset --
	// a content hash, a URI, a document ID. Real and checkable, not a
	// free-text assertion of trustworthiness.
	DatasetProvenance string `json:"dataset_provenance"`
}

// Validate enforces every one of the audit's five required fields.
func (r CalibrationRecord) Validate() error {
	if strings.TrimSpace(r.CalibrationSource) == "" {
		return ErrMissingCalibrationSource
	}
	if strings.TrimSpace(r.ModelVersion) == "" {
		return ErrMissingModelVersion
	}
	if len(r.Prior) == 0 {
		return ErrEmptyPrior
	}
	if strings.TrimSpace(r.DatasetProvenance) == "" {
		return ErrMissingDatasetProvenance
	}
	sum := 0.0
	for s, v := range r.Prior {
		if v < 0 || v > 1 {
			return fmt.Errorf("%w: prior[%s]=%.6f", ErrInvalidLikelihood, s, v)
		}
		sum += v
	}
	if sum < 0.999 || sum > 1.001 {
		return fmt.Errorf("calibration: prior must sum to 1, got %.6f", sum)
	}
	return nil
}

// LikelihoodTable is a real, deterministic mapping from one evidence
// predicate's asserted values to a likelihood distribution over hidden
// states -- the "Evidence assertion -> Hidden state -> Likelihood
// distribution" chain the audit named as missing -- bound to the
// CalibrationRecord that justifies its numbers.
type LikelihoodTable struct {
	Predicate  string                              `json:"predicate"`
	Record     CalibrationRecord                   `json:"record"`
	Likelihood map[string]map[hbayes.State]float64 `json:"likelihood"` // value -> state -> P(value | state)
}

// Validate checks the record and every entry's internal consistency:
// every likelihood value is a real probability, and every named state
// is one this table's own Prior actually declares (a likelihood entry
// for an undeclared state is exactly the kind of silent drift a
// governance contract exists to catch).
func (t LikelihoodTable) Validate() error {
	if err := t.Record.Validate(); err != nil {
		return err
	}
	if len(t.Likelihood) == 0 {
		return ErrEmptyLikelihoodTable
	}
	for value, dist := range t.Likelihood {
		for state, p := range dist {
			if p < 0 || p > 1 {
				return fmt.Errorf("%w: %s[%s]=%.6f", ErrInvalidLikelihood, value, state, p)
			}
			if _, declared := t.Record.Prior[state]; !declared {
				return fmt.Errorf("%w: %s references state %s", ErrUnregisteredState, value, state)
			}
		}
	}
	return nil
}

// Registry holds every registered, validated LikelihoodTable, keyed by
// predicate. Thread-safe: calibration can be updated as real datasets
// arrive without stopping the pipeline.
type Registry struct {
	mu     sync.RWMutex
	tables map[string]LikelihoodTable // predicate -> table
}

// NewRegistry constructs an empty registry. An empty registry is the
// correct, honest default: it can build ZERO observations until a real,
// validated calibration is registered.
func NewRegistry() *Registry { return &Registry{tables: map[string]LikelihoodTable{}} }

// Register adds or replaces a predicate's likelihood table, refusing
// anything that fails Validate -- the one and only door through which
// a probability can enter this registry, and it is locked unless every
// provenance field is present.
func (r *Registry) Register(t LikelihoodTable) error {
	if err := t.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tables[t.Predicate] = t
	return nil
}

// BuildObservation turns one real evidence assertion (predicate, value,
// asserting source, tick) into an hbayes.Observation, using ONLY a
// registered, provenanced LikelihoodTable. It fails closed --
// ErrNoCalibration, never a fabricated uniform or default distribution
// -- when no table is registered for predicate, or when value has no
// entry in that table.
func (r *Registry) BuildObservation(sourceID, predicate, value string, tick uint64, correlation float64) (hbayes.Observation, error) {
	_, span := telemetry.StartSpan(context.Background(), "calibration.BuildObservation",
		telemetry.Attribute{Key: "predicate", Value: predicate}, telemetry.Attribute{Key: "source_id", Value: sourceID})
	defer span.End()

	r.mu.RLock()
	table, ok := r.tables[predicate]
	r.mu.RUnlock()
	if !ok {
		return hbayes.Observation{}, fmt.Errorf("%w: predicate %q", ErrNoCalibration, predicate)
	}
	dist, ok := table.Likelihood[value]
	if !ok {
		return hbayes.Observation{}, fmt.Errorf("%w: predicate %q value %q", ErrNoCalibration, predicate, value)
	}
	if tick < table.Record.EffectiveTick {
		return hbayes.Observation{}, fmt.Errorf("calibration: tick %d precedes this table's effective tick %d", tick, table.Record.EffectiveTick)
	}
	// Defensive copy: callers must not be able to mutate the registry's
	// calibrated distribution through the returned Observation.
	out := make(map[hbayes.State]float64, len(dist))
	for s, p := range dist {
		out[s] = p
	}
	return hbayes.Observation{SourceID: sourceID, Tick: tick, Likelihood: out, Correlation: correlation}, nil
}

// Calibrated reports whether predicate currently has ANY registered
// table -- the honest, machine-checkable answer to "is this pipeline's
// TEMPORAL_BAYESIAN stage actually fed by real calibration, or would it
// be forced to skip / fail closed", usable directly by
// cmd/veriqo-readiness or engine_registry.json without guessing.
func (r *Registry) Calibrated(predicate string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.tables[predicate]
	return ok
}
