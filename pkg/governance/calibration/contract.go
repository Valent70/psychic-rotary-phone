package calibration

// PHASE F (P1-9) — the temporal Bayesian production contract.
//
// The Bayesian mechanism itself is real and finished, and is NOT
// rebuilt here: pkg/moat/hbayes does the inference, this package's
// CalibrationRecord already refuses an ungoverned LikelihoodTable
// (calibration_source, model_version, prior, effective_tick,
// dataset_provenance are all mandatory), corpus.go's Fit already
// performs real frequentist estimation from labeled events, and
// pkg/lifecycle.Orchestrator already drives a real hbayes.Model.Infer
// when a registry is bound.
//
// What was missing is the GOVERNANCE wrapper around all of that: the
// metadata a real model-risk process asks for, and — more importantly —
// an explicit record of whether calibration actually RAN. Before this
// file, "the temporal stage was skipped because no model exists" and
// "the temporal stage was skipped because the policy did not need one"
// were the same observable outcome. For a regulated decision those are
// completely different facts, and collapsing them is precisely the
// class of silence this project's discipline exists to prevent.
//
// ExecutionState below makes them distinct, with five named values, and
// RequiredButMissing is deliberately a state a caller can be handed
// rather than an error that might be swallowed: pkg/execution already
// fails closed (ErrTemporalCalibrationRequired) when a policy declares
// RequiresTemporalCalibration and nothing was supplied, and this state
// is what that refusal looks like when it is being REPORTED rather than
// returned.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ExecutionState records what actually happened to a temporal
// calibration step. The five values are the program's own vocabulary.
type ExecutionState string

const (
	// StateRequiredAndExecuted: policy required calibration, and it ran.
	StateRequiredAndExecuted ExecutionState = "REQUIRED_AND_EXECUTED"
	// StateRequiredButMissing: policy required calibration and no model
	// was available. This must NEVER be treated as a skip — it is a
	// fail-closed condition, and pkg/execution returns
	// ErrTemporalCalibrationRequired for exactly this case.
	StateRequiredButMissing ExecutionState = "REQUIRED_BUT_MISSING"
	// StateOptionalAndSkipped: policy did not require calibration and
	// none ran. The honest, unremarkable default.
	StateOptionalAndSkipped ExecutionState = "OPTIONAL_AND_SKIPPED"
	// StateExecuted: calibration ran although policy did not require it.
	StateExecuted ExecutionState = "EXECUTED"
	// StateFailed: calibration was attempted and errored. Distinct from
	// RequiredButMissing: something was there and it broke, rather than
	// nothing being there at all.
	StateFailed ExecutionState = "FAILED"
)

var knownStates = map[ExecutionState]bool{
	StateRequiredAndExecuted: true, StateRequiredButMissing: true,
	StateOptionalAndSkipped: true, StateExecuted: true, StateFailed: true,
}

// FailsClosed reports whether this state must stop a governed decision.
// Only one state does, and naming it as a method stops each caller
// re-deriving the rule slightly differently.
func (s ExecutionState) FailsClosed() bool {
	return s == StateRequiredButMissing || s == StateFailed
}

// Errors.
var (
	ErrUnknownExecutionState = errors.New("calibration: unknown temporal execution state")
	ErrMissingContractField  = errors.New("calibration: required temporal contract field missing")
	ErrSilentSkip            = errors.New("calibration: policy requires temporal calibration but the contract records a skip")
)

// ContractMetadata is the model-risk metadata a production temporal
// calibration must carry. Every field is one the program names
// explicitly; none of them is derived or defaulted, because a defaulted
// provenance field is indistinguishable from a real one once written
// down.
//
// It deliberately does NOT restate CalibrationRecord's five fields.
// A ContractMetadata is ABOUT a CalibrationRecord — Bind below carries
// both — so the two cannot drift into two versions of the same claim.
type ContractMetadata struct {
	DatasetSchemaVersion string `json:"dataset_schema_version"`
	LabelProvenance      string `json:"label_provenance"`
	GroundTruthOwner     string `json:"ground_truth_owner"`
	Reviewer             string `json:"reviewer"`
	SamplingMethod       string `json:"sampling_method"`
	// ClassBalance is the observed label distribution the model was fit
	// against, state -> fraction. A model fit on a 99/1 split behaves
	// very differently from one fit on 50/50, and not recording which
	// makes the model's numbers uninterpretable later.
	ClassBalance   map[string]float64 `json:"class_balance"`
	TrainSplitHash string             `json:"train_split_hash"`
	TestSplitHash  string             `json:"test_split_hash"`

	CalibrationVersion string `json:"calibration_version"`
	ModelHash          string `json:"model_hash"`
	EffectiveTick      uint64 `json:"effective_tick"`
	PriorHash          string `json:"prior_hash"`
	LikelihoodHash     string `json:"likelihood_hash"`

	// DriftPolicy states what happens when the live distribution moves
	// away from the one the model was fit on; RecalibrationTrigger
	// states what causes a refit. Both are free text on purpose: they
	// describe an operational commitment, and forcing them into an enum
	// would push real commitments into an "other" bucket.
	DriftPolicy          string `json:"drift_policy"`
	RecalibrationTrigger string `json:"recalibration_trigger"`
}

// Validate refuses metadata missing any field. There is no partial
// mode: a temporal contract that cannot say who owns its ground truth,
// or against which split it was fit, is not a governed model.
func (m ContractMetadata) Validate() error {
	required := []struct{ name, value string }{
		{"dataset_schema_version", m.DatasetSchemaVersion},
		{"label_provenance", m.LabelProvenance},
		{"ground_truth_owner", m.GroundTruthOwner},
		{"reviewer", m.Reviewer},
		{"sampling_method", m.SamplingMethod},
		{"train_split_hash", m.TrainSplitHash},
		{"test_split_hash", m.TestSplitHash},
		{"calibration_version", m.CalibrationVersion},
		{"model_hash", m.ModelHash},
		{"prior_hash", m.PriorHash},
		{"likelihood_hash", m.LikelihoodHash},
		{"drift_policy", m.DriftPolicy},
		{"recalibration_trigger", m.RecalibrationTrigger},
	}
	var missing []string
	for _, r := range required {
		if strings.TrimSpace(r.value) == "" {
			missing = append(missing, r.name)
		}
	}
	if len(m.ClassBalance) == 0 {
		missing = append(missing, "class_balance")
	}
	if m.EffectiveTick == 0 {
		missing = append(missing, "effective_tick")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s", ErrMissingContractField, strings.Join(missing, ", "))
	}
	sum := 0.0
	for state, fraction := range m.ClassBalance {
		if fraction < 0 || fraction > 1 {
			return fmt.Errorf("%w: class_balance[%s]=%.6f is not a fraction", ErrMissingContractField, state, fraction)
		}
		sum += fraction
	}
	if sum < 0.999 || sum > 1.001 {
		return fmt.Errorf("%w: class_balance sums to %.6f, not 1", ErrMissingContractField, sum)
	}
	return nil
}

// Hash content-addresses the metadata so a decision can commit to the
// exact governance posture its temporal reasoning ran under.
func (m ContractMetadata) Hash() string {
	var b strings.Builder
	fmt.Fprintf(&b, "veriqo.temporal.contract/v1\ndataset_schema=%s\nlabel_provenance=%s\nowner=%s\nreviewer=%s\n",
		m.DatasetSchemaVersion, m.LabelProvenance, m.GroundTruthOwner, m.Reviewer)
	fmt.Fprintf(&b, "sampling=%s\ntrain=%s\ntest=%s\n", m.SamplingMethod, m.TrainSplitHash, m.TestSplitHash)
	fmt.Fprintf(&b, "calibration_version=%s\nmodel=%s\neffective_tick=%d\nprior=%s\nlikelihood=%s\n",
		m.CalibrationVersion, m.ModelHash, m.EffectiveTick, m.PriorHash, m.LikelihoodHash)
	fmt.Fprintf(&b, "drift=%s\nretrigger=%s\n", m.DriftPolicy, m.RecalibrationTrigger)
	states := make([]string, 0, len(m.ClassBalance))
	for s := range m.ClassBalance {
		states = append(states, s)
	}
	sort.Strings(states)
	for _, s := range states {
		fmt.Fprintf(&b, "balance.%s=%.9f\n", s, m.ClassBalance[s])
	}
	sum := sha256.Sum256([]byte(b.String()))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// TemporalContract binds one predicate's governance metadata to the
// CalibrationRecord it describes and to what actually happened.
type TemporalContract struct {
	Predicate string            `json:"predicate"`
	Record    CalibrationRecord `json:"record"`
	Metadata  ContractMetadata  `json:"metadata"`
	// PolicyRequires mirrors decision.Policy.RequiresTemporalCalibration
	// for this run. It is supplied by the caller that holds the policy,
	// never inferred.
	PolicyRequires bool           `json:"policy_requires"`
	State          ExecutionState `json:"state"`
	// Detail explains a FAILED or REQUIRED_BUT_MISSING state. Mandatory
	// for those two; a failure with no reason is not a report.
	Detail string `json:"detail,omitempty"`
	Hash   string `json:"hash"`
}

// NewContract builds and validates a temporal contract, computing its
// hash. It refuses every combination that would let a required
// calibration be recorded as a skip.
func NewContract(predicate string, rec CalibrationRecord, meta ContractMetadata, policyRequires bool, state ExecutionState, detail string) (TemporalContract, error) {
	if strings.TrimSpace(predicate) == "" {
		return TemporalContract{}, fmt.Errorf("%w: predicate", ErrMissingContractField)
	}
	if !knownStates[state] {
		return TemporalContract{}, fmt.Errorf("%w: %q", ErrUnknownExecutionState, state)
	}
	if err := rec.Validate(); err != nil {
		return TemporalContract{}, err
	}
	if err := meta.Validate(); err != nil {
		return TemporalContract{}, err
	}
	// The rule this whole file exists for: a policy that requires
	// temporal calibration can never be recorded as having optionally
	// skipped it. FAIL CLOSED, never silently skip.
	if policyRequires && state == StateOptionalAndSkipped {
		return TemporalContract{}, ErrSilentSkip
	}
	if policyRequires && state == StateExecuted {
		// EXECUTED means "ran although not required". Under a policy that
		// DOES require it, the correct state is REQUIRED_AND_EXECUTED, and
		// accepting the weaker label would understate the obligation.
		return TemporalContract{}, fmt.Errorf("%w: policy requires calibration, so a run must be recorded as %s, not %s",
			ErrUnknownExecutionState, StateRequiredAndExecuted, StateExecuted)
	}
	if !policyRequires && state == StateRequiredAndExecuted {
		return TemporalContract{}, fmt.Errorf("%w: policy does not require calibration, so %s misstates the obligation",
			ErrUnknownExecutionState, StateRequiredAndExecuted)
	}
	if state.FailsClosed() && strings.TrimSpace(detail) == "" {
		return TemporalContract{}, fmt.Errorf("%w: detail (mandatory for %s)", ErrMissingContractField, state)
	}

	c := TemporalContract{
		Predicate: predicate, Record: rec, Metadata: meta,
		PolicyRequires: policyRequires, State: state, Detail: detail,
	}
	c.Hash = contractHash(c)
	return c, nil
}

func contractHash(c TemporalContract) string {
	var b strings.Builder
	fmt.Fprintf(&b, "veriqo.temporal.contract.binding/v1\npredicate=%s\n", c.Predicate)
	fmt.Fprintf(&b, "calibration_source=%s\nmodel_version=%s\neffective_tick=%d\ndataset=%s\n",
		c.Record.CalibrationSource, c.Record.ModelVersion, c.Record.EffectiveTick, c.Record.DatasetProvenance)
	// Prior is keyed by hbayes.State. Collect the (state, probability)
	// pairs and sort by the state's string form, so the hash is stable
	// regardless of Go's map iteration order -- the same technique every
	// other content hash in this repository uses.
	type priorEntry struct {
		state string
		p     float64
	}
	priors := make([]priorEntry, 0, len(c.Record.Prior))
	for s, p := range c.Record.Prior {
		priors = append(priors, priorEntry{state: string(s), p: p})
	}
	sort.Slice(priors, func(i, j int) bool { return priors[i].state < priors[j].state })
	for _, e := range priors {
		fmt.Fprintf(&b, "prior.%s=%.9f\n", e.state, e.p)
	}
	fmt.Fprintf(&b, "metadata=%s\nrequires=%v\nstate=%s\ndetail=%s\n",
		c.Metadata.Hash(), c.PolicyRequires, c.State, c.Detail)
	sum := sha256.Sum256([]byte(b.String()))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Assert converts a fail-closed contract into an error, so a caller
// cannot read State and proceed anyway.
func (c TemporalContract) Assert() error {
	if !c.State.FailsClosed() {
		return nil
	}
	return fmt.Errorf("calibration: temporal contract for %q is %s: %s", c.Predicate, c.State, c.Detail)
}

// ContractFor derives the execution state a registry's CURRENT
// registration status implies for one predicate under one policy, and
// builds the contract for it. It is the honest bridge between "what is
// registered" and "what may be claimed": a predicate with no model
// under a policy that requires one yields REQUIRED_BUT_MISSING, which
// Assert then refuses — the same fail-closed outcome pkg/execution's
// ErrTemporalCalibrationRequired already produces, made reportable.
func (r *Registry) ContractFor(predicate string, meta ContractMetadata, policyRequires bool) (TemporalContract, error) {
	r.mu.RLock()
	table, hasTable := r.tables[predicate]
	_, hasModel := r.models[predicate]
	r.mu.RUnlock()

	if !hasTable {
		if !policyRequires {
			// Nothing registered and nothing required: there is no
			// CalibrationRecord to bind, so there is no contract to make.
			// Returning an error here rather than an empty contract keeps
			// the invariant that every TemporalContract that exists is a
			// real, validated one.
			return TemporalContract{}, fmt.Errorf("%w: predicate %q (nothing registered and nothing required)",
				ErrNoCalibration, predicate)
		}
		return TemporalContract{}, fmt.Errorf("%w: predicate %q has no registered likelihood table", ErrNoCalibration, predicate)
	}

	state := StateOptionalAndSkipped
	detail := ""
	switch {
	case policyRequires && hasModel:
		state = StateRequiredAndExecuted
	case policyRequires && !hasModel:
		state = StateRequiredButMissing
		detail = "policy requires temporal calibration; predicate has a likelihood table but no registered temporal model"
	case !policyRequires && hasModel:
		state = StateExecuted
	}
	return NewContract(predicate, table.Record, meta, policyRequires, state, detail)
}
