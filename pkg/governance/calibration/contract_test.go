package calibration

import (
	"errors"
	"strings"
	"testing"

	"veriqo/pkg/moat/hbayes"
)

// fixtureMetadata is a complete, valid temporal contract metadata
// block. Every field is populated so a test that blanks ONE field is
// proving that field is genuinely required.
//
// It is clearly labelled as a fixture, following this package's own
// standing convention (corpus_test.go's
// "fixture:synthetic-dark-vessel-corpus-v1 (NOT a real production
// calibration dataset)"): nothing here is or claims to be real
// production calibration governance.
func fixtureMetadata() ContractMetadata {
	return ContractMetadata{
		DatasetSchemaVersion: "fixture:labeled-event/v1 (NOT production)",
		LabelProvenance:      "fixture:hand-constructed labels for a unit test",
		GroundTruthOwner:     "fixture:unit-test",
		Reviewer:             "fixture:unit-test",
		SamplingMethod:       "fixture:exhaustive over a hand-written corpus",
		ClassBalance:         map[string]float64{"DARK": 0.4, "NORMAL": 0.6},
		TrainSplitHash:       "sha256:1111",
		TestSplitHash:        "sha256:2222",
		CalibrationVersion:   "fixture-cal-v1",
		ModelHash:            "sha256:3333",
		EffectiveTick:        1,
		PriorHash:            "sha256:4444",
		LikelihoodHash:       "sha256:5555",
		DriftPolicy:          "PSI > 0.2 on any evidence predicate blocks new decisions under this model",
		RecalibrationTrigger: "quarterly, or immediately on a drift-policy breach",
	}
}

func fixtureRecord() CalibrationRecord {
	return CalibrationRecord{
		CalibrationSource: "fixture:synthetic-corpus-v1 (NOT a real production calibration dataset)",
		ModelVersion:      "temporal-v1",
		Prior:             map[hbayes.State]float64{"DARK": 0.2, "NORMAL": 0.8},
		EffectiveTick:     1,
		DatasetProvenance: "fixture:sha256:0000",
	}
}

// registryWithTableAndModel builds a registry with a real, registered
// likelihood table and (optionally) a real temporal model, so
// ContractFor is deriving state from genuine registration status rather
// than from a flag a test set.
func registryWithTableAndModel(t *testing.T, withModel bool) *Registry {
	t.Helper()
	r := NewRegistry()
	if err := r.Register(LikelihoodTable{
		Predicate: "AIS_STATUS", Record: fixtureRecord(),
		Likelihood: map[string]map[hbayes.State]float64{
			"OFF": {"DARK": 0.9, "NORMAL": 0.1},
			"ON":  {"DARK": 0.05, "NORMAL": 0.95},
		},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if withModel {
		tr := hbayes.Transition{States: []hbayes.State{"DARK", "NORMAL"}, P: map[hbayes.State]map[hbayes.State]float64{
			"DARK":   {"DARK": 0.7, "NORMAL": 0.3},
			"NORMAL": {"DARK": 0.1, "NORMAL": 0.9},
		}}
		if err := r.RegisterTemporalModel("AIS_STATUS", tr, nil, 0); err != nil {
			t.Fatalf("RegisterTemporalModel: %v", err)
		}
	}
	return r
}

// TestRequiredCalibrationCanNeverBeRecordedAsASkip is PHASE F's
// headline rule: "If policy requires calibration but no model is
// available, it must FAIL CLOSED, never silently skip."
func TestRequiredCalibrationCanNeverBeRecordedAsASkip(t *testing.T) {
	_, err := NewContract("AIS_STATUS", fixtureRecord(), fixtureMetadata(),
		true, StateOptionalAndSkipped, "")
	if !errors.Is(err, ErrSilentSkip) {
		t.Fatalf("err = %v, want ErrSilentSkip -- a required calibration was recorded as an optional skip", err)
	}
}

// TestMissingModelUnderARequiringPolicyFailsClosed proves the same rule
// end to end through the registry, deriving state from what is actually
// registered rather than from a caller's claim.
func TestMissingModelUnderARequiringPolicyFailsClosed(t *testing.T) {
	r := registryWithTableAndModel(t, false) // table registered, NO temporal model

	c, err := r.ContractFor("AIS_STATUS", fixtureMetadata(), true)
	if err != nil {
		t.Fatalf("ContractFor: %v", err)
	}
	if c.State != StateRequiredButMissing {
		t.Fatalf("State = %q, want REQUIRED_BUT_MISSING", c.State)
	}
	if !c.State.FailsClosed() {
		t.Fatal("REQUIRED_BUT_MISSING does not fail closed")
	}
	if c.Detail == "" {
		t.Fatal("a fail-closed contract with no detail is not a report")
	}
	assertErr := c.Assert()
	if assertErr == nil {
		t.Fatal("Assert accepted a REQUIRED_BUT_MISSING contract -- a caller could read State and proceed anyway")
	}
	if !strings.Contains(assertErr.Error(), "AIS_STATUS") {
		t.Errorf("Assert's error does not name the predicate: %v", assertErr)
	}
}

// TestRequiredAndAvailableCalibrationIsRecordedAsExecuted is the
// positive control: without it, a suite that failed everything would
// look like it was working.
func TestRequiredAndAvailableCalibrationIsRecordedAsExecuted(t *testing.T) {
	r := registryWithTableAndModel(t, true)

	c, err := r.ContractFor("AIS_STATUS", fixtureMetadata(), true)
	if err != nil {
		t.Fatalf("ContractFor: %v", err)
	}
	if c.State != StateRequiredAndExecuted {
		t.Fatalf("State = %q, want REQUIRED_AND_EXECUTED", c.State)
	}
	if c.State.FailsClosed() {
		t.Fatal("REQUIRED_AND_EXECUTED must not fail closed")
	}
	if err := c.Assert(); err != nil {
		t.Fatalf("Assert: %v", err)
	}
	if c.Hash == "" {
		t.Fatal("contract is not content-addressed")
	}
}

// TestOptionalPolicyWithAModelRecordsExecutedNotRequired keeps the two
// "it ran" states apart: EXECUTED and REQUIRED_AND_EXECUTED describe
// different obligations, and conflating them would either overstate or
// understate what the policy actually demanded.
func TestOptionalPolicyWithAModelRecordsExecutedNotRequired(t *testing.T) {
	r := registryWithTableAndModel(t, true)
	c, err := r.ContractFor("AIS_STATUS", fixtureMetadata(), false)
	if err != nil {
		t.Fatalf("ContractFor: %v", err)
	}
	if c.State != StateExecuted {
		t.Fatalf("State = %q, want EXECUTED", c.State)
	}
}

func TestOptionalPolicyWithoutAModelRecordsAnHonestSkip(t *testing.T) {
	r := registryWithTableAndModel(t, false)
	c, err := r.ContractFor("AIS_STATUS", fixtureMetadata(), false)
	if err != nil {
		t.Fatalf("ContractFor: %v", err)
	}
	if c.State != StateOptionalAndSkipped {
		t.Fatalf("State = %q, want OPTIONAL_AND_SKIPPED", c.State)
	}
	if c.State.FailsClosed() {
		t.Fatal("an optional skip must not fail closed")
	}
}

// TestMislabelledObligationsAreRefused covers the two remaining ways a
// state could misstate what the policy demanded.
func TestMislabelledObligationsAreRefused(t *testing.T) {
	cases := []struct {
		name           string
		policyRequires bool
		state          ExecutionState
	}{
		{"required policy labelled as a bare EXECUTED", true, StateExecuted},
		{"optional policy labelled REQUIRED_AND_EXECUTED", false, StateRequiredAndExecuted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewContract("AIS_STATUS", fixtureRecord(), fixtureMetadata(),
				tc.policyRequires, tc.state, ""); err == nil {
				t.Fatal("a state that misstates the policy's obligation was accepted")
			}
		})
	}
}

func TestUnknownExecutionStateIsRefused(t *testing.T) {
	_, err := NewContract("AIS_STATUS", fixtureRecord(), fixtureMetadata(),
		false, ExecutionState("PROBABLY_FINE"), "")
	if !errors.Is(err, ErrUnknownExecutionState) {
		t.Fatalf("err = %v, want ErrUnknownExecutionState", err)
	}
}

func TestFailedStateRequiresADetail(t *testing.T) {
	if _, err := NewContract("AIS_STATUS", fixtureRecord(), fixtureMetadata(),
		false, StateFailed, ""); !errors.Is(err, ErrMissingContractField) {
		t.Fatal("a FAILED contract with no stated reason was accepted")
	}
	c, err := NewContract("AIS_STATUS", fixtureRecord(), fixtureMetadata(),
		false, StateFailed, "hbayes.Infer returned a non-finite posterior")
	if err != nil {
		t.Fatalf("NewContract: %v", err)
	}
	if !c.State.FailsClosed() {
		t.Fatal("FAILED does not fail closed")
	}
}

// TestEveryContractMetadataFieldIsIndividuallyRequired is the same
// field-by-field discipline calibration_test.go already applies to
// CalibrationRecord's five provenance fields, extended to the fourteen
// PHASE F adds. A defaulted governance field is indistinguishable from
// a real one once written down, so none may be defaulted.
func TestEveryContractMetadataFieldIsIndividuallyRequired(t *testing.T) {
	blanks := map[string]func(*ContractMetadata){
		"dataset_schema_version": func(m *ContractMetadata) { m.DatasetSchemaVersion = "" },
		"label_provenance":       func(m *ContractMetadata) { m.LabelProvenance = "" },
		"ground_truth_owner":     func(m *ContractMetadata) { m.GroundTruthOwner = "" },
		"reviewer":               func(m *ContractMetadata) { m.Reviewer = "" },
		"sampling_method":        func(m *ContractMetadata) { m.SamplingMethod = "" },
		"class_balance":          func(m *ContractMetadata) { m.ClassBalance = nil },
		"train_split_hash":       func(m *ContractMetadata) { m.TrainSplitHash = "" },
		"test_split_hash":        func(m *ContractMetadata) { m.TestSplitHash = "" },
		"calibration_version":    func(m *ContractMetadata) { m.CalibrationVersion = "" },
		"model_hash":             func(m *ContractMetadata) { m.ModelHash = "" },
		"effective_tick":         func(m *ContractMetadata) { m.EffectiveTick = 0 },
		"prior_hash":             func(m *ContractMetadata) { m.PriorHash = "" },
		"likelihood_hash":        func(m *ContractMetadata) { m.LikelihoodHash = "" },
		"drift_policy":           func(m *ContractMetadata) { m.DriftPolicy = "" },
		"recalibration_trigger":  func(m *ContractMetadata) { m.RecalibrationTrigger = "" },
	}
	if len(blanks) != 15 {
		t.Fatalf("the blanking table covers %d fields; keep it exhaustive", len(blanks))
	}
	for name, blank := range blanks {
		m := fixtureMetadata()
		blank(&m)
		if err := m.Validate(); !errors.Is(err, ErrMissingContractField) {
			t.Errorf("blanking %s was accepted: err = %v", name, err)
		}
	}
}

func TestClassBalanceMustBeARealDistribution(t *testing.T) {
	for name, balance := range map[string]map[string]float64{
		"does not sum to one": {"DARK": 0.4, "NORMAL": 0.4},
		"negative fraction":   {"DARK": -0.1, "NORMAL": 1.1},
		"above one":           {"DARK": 1.5, "NORMAL": -0.5},
	} {
		m := fixtureMetadata()
		m.ClassBalance = balance
		if err := m.Validate(); err == nil {
			t.Errorf("class balance %q (%v) was accepted", name, balance)
		}
	}
}

// TestContractHashDetectsEveryMeaningfulChange makes the contract's
// content-addressing real rather than decorative: a decision that
// commits to a contract hash must be committing to the whole posture.
func TestContractHashDetectsEveryMeaningfulChange(t *testing.T) {
	base, err := NewContract("AIS_STATUS", fixtureRecord(), fixtureMetadata(), false, StateOptionalAndSkipped, "")
	if err != nil {
		t.Fatalf("NewContract: %v", err)
	}
	if again, _ := NewContract("AIS_STATUS", fixtureRecord(), fixtureMetadata(), false, StateOptionalAndSkipped, ""); again.Hash != base.Hash {
		t.Fatal("contract hash is not deterministic")
	}

	mutations := map[string]func(*CalibrationRecord, *ContractMetadata, *ExecutionState){
		"predicate-independent record source": func(r *CalibrationRecord, _ *ContractMetadata, _ *ExecutionState) {
			r.CalibrationSource = "fixture:a-different-corpus"
		},
		"model version": func(r *CalibrationRecord, _ *ContractMetadata, _ *ExecutionState) {
			r.ModelVersion = "temporal-v2"
		},
		"prior": func(r *CalibrationRecord, _ *ContractMetadata, _ *ExecutionState) {
			r.Prior = map[hbayes.State]float64{"DARK": 0.3, "NORMAL": 0.7}
		},
		"dataset provenance": func(r *CalibrationRecord, _ *ContractMetadata, _ *ExecutionState) {
			r.DatasetProvenance = "fixture:sha256:9999"
		},
		"reviewer": func(_ *CalibrationRecord, m *ContractMetadata, _ *ExecutionState) {
			m.Reviewer = "someone-else"
		},
		"drift policy": func(_ *CalibrationRecord, m *ContractMetadata, _ *ExecutionState) {
			m.DriftPolicy = "no drift policy"
		},
		"class balance": func(_ *CalibrationRecord, m *ContractMetadata, _ *ExecutionState) {
			m.ClassBalance = map[string]float64{"DARK": 0.5, "NORMAL": 0.5}
		},
		"model hash": func(_ *CalibrationRecord, m *ContractMetadata, _ *ExecutionState) {
			m.ModelHash = "sha256:different"
		},
		"execution state": func(_ *CalibrationRecord, _ *ContractMetadata, s *ExecutionState) {
			*s = StateFailed
		},
	}
	for name, mutate := range mutations {
		rec, meta, state := fixtureRecord(), fixtureMetadata(), StateOptionalAndSkipped
		mutate(&rec, &meta, &state)
		detail := ""
		if state.FailsClosed() {
			detail = "mutation test"
		}
		c, err := NewContract("AIS_STATUS", rec, meta, false, state, detail)
		if err != nil {
			t.Fatalf("NewContract after mutating %s: %v", name, err)
		}
		if c.Hash == base.Hash {
			t.Errorf("changing %s did not change the contract hash", name)
		}
	}
}

// TestContractForRefusesAnUnregisteredPredicate keeps the invariant
// that every TemporalContract which exists is a real, validated one:
// there is no empty-contract path.
func TestContractForRefusesAnUnregisteredPredicate(t *testing.T) {
	r := NewRegistry()
	for _, requires := range []bool{true, false} {
		if _, err := r.ContractFor("NEVER_REGISTERED", fixtureMetadata(), requires); !errors.Is(err, ErrNoCalibration) {
			t.Errorf("policyRequires=%v: err = %v, want ErrNoCalibration", requires, err)
		}
	}
}

// TestContractForRefusesIncompleteMetadata proves the governance
// wrapper cannot be bypassed by going through the registry instead of
// NewContract directly.
func TestContractForRefusesIncompleteMetadata(t *testing.T) {
	r := registryWithTableAndModel(t, true)
	m := fixtureMetadata()
	m.GroundTruthOwner = ""
	if _, err := r.ContractFor("AIS_STATUS", m, true); !errors.Is(err, ErrMissingContractField) {
		t.Fatalf("err = %v, want ErrMissingContractField", err)
	}
}

// TestFailsClosedIsExhaustiveOverTheStateVocabulary stops a future
// sixth state being added without anyone deciding whether it stops a
// governed decision.
func TestFailsClosedIsExhaustiveOverTheStateVocabulary(t *testing.T) {
	failing := 0
	for s := range knownStates {
		if s.FailsClosed() {
			failing++
		}
	}
	if len(knownStates) != 5 {
		t.Fatalf("the state vocabulary has %d values; FailsClosed's classification below assumes 5", len(knownStates))
	}
	if failing != 2 {
		t.Fatalf("%d states fail closed, want exactly 2 (REQUIRED_BUT_MISSING and FAILED)", failing)
	}
}
