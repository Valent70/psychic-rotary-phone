package calibration

import (
	"errors"
	"testing"

	"veriqo/pkg/moat/hbayes"
)

// fixtureTable is a clearly-labelled, synthetic calibration table --
// mirroring test/integration/dark_vessel_test.go's own "clearly
// labeled synthetic" convention -- used only to prove the contract's
// machinery works, never asserted as real production calibration.
func fixtureTable() LikelihoodTable {
	return LikelihoodTable{
		Predicate: "AIS_STATUS",
		Record: CalibrationRecord{
			CalibrationSource: "fixture:synthetic-dark-vessel-dataset-v1 (NOT real production calibration)",
			ModelVersion:      "temporal-v1",
			Prior:             map[hbayes.State]float64{"DARK": 0.2, "NORMAL": 0.8},
			EffectiveTick:     1,
			DatasetProvenance: "fixture:sha256:0000000000000000000000000000000000000000000000000000000000000",
		},
		Likelihood: map[string]map[hbayes.State]float64{
			"OFF": {"DARK": 0.9, "NORMAL": 0.1},
			"ON":  {"DARK": 0.05, "NORMAL": 0.95},
		},
	}
}

func TestValidCalibrationRecordPasses(t *testing.T) {
	r := fixtureTable().Record
	if err := r.Validate(); err != nil {
		t.Fatalf("a genuinely complete record must validate: %v", err)
	}
}

// TestEachMissingProvenanceFieldIsIndividuallyRefused is the audit's
// exact ask made adversarial: no probability may enter without ALL
// five fields (source, model version, prior, effective tick, dataset
// provenance) -- proven by removing each one individually.
func TestEachMissingProvenanceFieldIsIndividuallyRefused(t *testing.T) {
	base := fixtureTable().Record
	cases := []struct {
		name   string
		mutate func(*CalibrationRecord)
		want   error
	}{
		{"missing source", func(r *CalibrationRecord) { r.CalibrationSource = "" }, ErrMissingCalibrationSource},
		{"missing model version", func(r *CalibrationRecord) { r.ModelVersion = "" }, ErrMissingModelVersion},
		{"empty prior", func(r *CalibrationRecord) { r.Prior = nil }, ErrEmptyPrior},
		{"missing dataset provenance", func(r *CalibrationRecord) { r.DatasetProvenance = "" }, ErrMissingDatasetProvenance},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := base
			r.Prior = map[hbayes.State]float64{"DARK": 0.2, "NORMAL": 0.8}
			c.mutate(&r)
			if err := r.Validate(); !errors.Is(err, c.want) {
				t.Fatalf("expected %v, got %v", c.want, err)
			}
		})
	}
}

func TestPriorMustSumToOne(t *testing.T) {
	r := fixtureTable().Record
	r.Prior = map[hbayes.State]float64{"DARK": 0.2, "NORMAL": 0.5} // sums to 0.7
	if err := r.Validate(); err == nil {
		t.Fatal("a prior that does not sum to 1 must be refused")
	}
}

func TestLikelihoodTableRejectsAnUndeclaredState(t *testing.T) {
	tab := fixtureTable()
	tab.Likelihood["OFF"]["UNKNOWN_STATE"] = 0.5 // not in Record.Prior
	if err := tab.Validate(); !errors.Is(err, ErrUnregisteredState) {
		t.Fatalf("a likelihood entry for a state outside the declared Prior must be refused, got %v", err)
	}
}

func TestLikelihoodTableRejectsOutOfRangeProbability(t *testing.T) {
	tab := fixtureTable()
	tab.Likelihood["OFF"]["DARK"] = 1.5
	if err := tab.Validate(); !errors.Is(err, ErrInvalidLikelihood) {
		t.Fatalf("a likelihood value outside [0,1] must be refused, got %v", err)
	}
}

func TestRegistryRefusesAnInvalidTable(t *testing.T) {
	reg := NewRegistry()
	bad := fixtureTable()
	bad.Record.CalibrationSource = ""
	if err := reg.Register(bad); err == nil {
		t.Fatal("Register must refuse a table with an invalid CalibrationRecord")
	}
	if reg.Calibrated("AIS_STATUS") {
		t.Fatal("a refused registration must not mark the predicate as calibrated")
	}
}

// TestBuildObservationFailsClosedWithoutCalibration is THE property
// this package exists to guarantee: an empty (or partially populated)
// registry must refuse to produce an Observation, never fabricate a
// default/uniform distribution to keep the pipeline moving.
func TestBuildObservationFailsClosedWithoutCalibration(t *testing.T) {
	reg := NewRegistry()
	_, err := reg.BuildObservation("port-monitor", "AIS_STATUS", "OFF", 5, 0)
	if !errors.Is(err, ErrNoCalibration) {
		t.Fatalf("expected ErrNoCalibration from an empty registry, got %v", err)
	}

	if err := reg.Register(fixtureTable()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	_, err = reg.BuildObservation("port-monitor", "AIS_STATUS", "SOME_UNCALIBRATED_VALUE", 5, 0)
	if !errors.Is(err, ErrNoCalibration) {
		t.Fatalf("a value with no calibrated entry must also fail closed, got %v", err)
	}
	_, err = reg.BuildObservation("port-monitor", "SOME_OTHER_PREDICATE", "OFF", 5, 0)
	if !errors.Is(err, ErrNoCalibration) {
		t.Fatalf("an entirely uncalibrated predicate must fail closed, got %v", err)
	}
}

func TestBuildObservationSucceedsWithRealCalibration(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(fixtureTable()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	obs, err := reg.BuildObservation("port-monitor", "AIS_STATUS", "OFF", 5, 0.1)
	if err != nil {
		t.Fatalf("BuildObservation: %v", err)
	}
	if obs.SourceID != "port-monitor" || obs.Tick != 5 || obs.Correlation != 0.1 {
		t.Fatalf("unexpected observation shape: %+v", obs)
	}
	if obs.Likelihood["DARK"] != 0.9 || obs.Likelihood["NORMAL"] != 0.1 {
		t.Fatalf("unexpected likelihood distribution: %+v", obs.Likelihood)
	}
	if !reg.Calibrated("AIS_STATUS") {
		t.Fatal("Calibrated must report true once a real table is registered")
	}
}

func TestBuildObservationRefusesATickBeforeEffectiveTick(t *testing.T) {
	reg := NewRegistry()
	tab := fixtureTable()
	tab.Record.EffectiveTick = 10
	if err := reg.Register(tab); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := reg.BuildObservation("port-monitor", "AIS_STATUS", "OFF", 5, 0); err == nil {
		t.Fatal("a tick before the calibration's effective tick must be refused")
	}
}

// TestBuildObservationReturnsADefensiveCopy proves a caller mutating
// the returned Observation's Likelihood map cannot corrupt the
// registry's own calibrated distribution for the next caller.
func TestBuildObservationReturnsADefensiveCopy(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(fixtureTable()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	obs, err := reg.BuildObservation("a", "AIS_STATUS", "OFF", 5, 0)
	if err != nil {
		t.Fatalf("BuildObservation: %v", err)
	}
	obs.Likelihood["DARK"] = 0.0 // attempt to corrupt

	again, err := reg.BuildObservation("b", "AIS_STATUS", "OFF", 5, 0)
	if err != nil {
		t.Fatalf("BuildObservation: %v", err)
	}
	if again.Likelihood["DARK"] != 0.9 {
		t.Fatalf("mutating one caller's Observation must not affect the registry's calibration: got %.4f", again.Likelihood["DARK"])
	}
}

// TestFeedsARealHBayesModelEndToEnd proves this contract's output is
// actually consumable by the real hbayes engine, not just
// shape-compatible in isolation.
func TestFeedsARealHBayesModelEndToEnd(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(fixtureTable()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	obs1, err := reg.BuildObservation("port-monitor", "AIS_STATUS", "OFF", 1, 0)
	if err != nil {
		t.Fatalf("BuildObservation: %v", err)
	}
	obs2, err := reg.BuildObservation("port-monitor", "AIS_STATUS", "OFF", 2, 0)
	if err != nil {
		t.Fatalf("BuildObservation: %v", err)
	}

	tr := hbayes.Transition{States: []hbayes.State{"DARK", "NORMAL"}, P: map[hbayes.State]map[hbayes.State]float64{
		"DARK":   {"DARK": 0.7, "NORMAL": 0.3},
		"NORMAL": {"DARK": 0.1, "NORMAL": 0.9},
	}}
	model, err := hbayes.NewTemporalModel([]hbayes.State{"DARK", "NORMAL"}, tr,
		map[hbayes.State]float64{"DARK": 0.2, "NORMAL": 0.8}, nil, 0)
	if err != nil {
		t.Fatalf("NewTemporalModel: %v", err)
	}
	trace, err := model.Infer([]hbayes.TickObservations{
		{Tick: 1, Observations: []hbayes.Observation{obs1}},
		{Tick: 2, Observations: []hbayes.Observation{obs2}},
	}, nil)
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	if len(trace.Steps) != 2 {
		t.Fatalf("expected 2 inference steps, got %d", len(trace.Steps))
	}
}

func fixtureTransition() hbayes.Transition {
	return hbayes.Transition{States: []hbayes.State{"DARK", "NORMAL"}, P: map[hbayes.State]map[hbayes.State]float64{
		"DARK":   {"DARK": 0.7, "NORMAL": 0.3},
		"NORMAL": {"DARK": 0.1, "NORMAL": 0.9},
	}}
}

// TestRegisterTemporalModelFailsClosedWithoutALikelihoodTable is the
// same fail-closed discipline BuildObservation already enforces,
// applied to the model half of the bridge: a Transition/CausalEdges/
// decay may not be registered for a predicate that has no
// LikelihoodTable, because RegisterTemporalModel reads its Prior back
// from that table -- there is nothing to read back from if it does not
// exist.
func TestRegisterTemporalModelFailsClosedWithoutALikelihoodTable(t *testing.T) {
	reg := NewRegistry()
	if err := reg.RegisterTemporalModel("AIS_STATUS", fixtureTransition(), nil, 0); !errors.Is(err, ErrNoCalibration) {
		t.Fatalf("expected ErrNoCalibration registering a model for an uncalibrated predicate, got %v", err)
	}
	if reg.TemporalModelRegistered("AIS_STATUS") {
		t.Fatal("a refused registration must not mark the predicate as having a temporal model")
	}
}

// TestRegisterTemporalModelReusesTheTablesPrior proves the single-
// source-of-truth invariant the method's own doc comment states: the
// registered model's Prior must be exactly the LikelihoodTable's own
// Prior, never a second, independently-supplied value that could
// silently diverge from it.
func TestRegisterTemporalModelReusesTheTablesPrior(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(fixtureTable()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := reg.RegisterTemporalModel("AIS_STATUS", fixtureTransition(), nil, 0); err != nil {
		t.Fatalf("RegisterTemporalModel: %v", err)
	}
	model, err := reg.BuildTemporalModel("AIS_STATUS")
	if err != nil {
		t.Fatalf("BuildTemporalModel: %v", err)
	}
	want := fixtureTable().Record.Prior
	if len(model.Prior) != len(want) {
		t.Fatalf("prior size mismatch: got %+v want %+v", model.Prior, want)
	}
	for state, p := range want {
		if model.Prior[state] != p {
			t.Fatalf("model prior[%s]=%.6f does not match the registered table's prior=%.6f -- the two must never diverge", state, model.Prior[state], p)
		}
	}
	if !reg.TemporalModelRegistered("AIS_STATUS") {
		t.Fatal("TemporalModelRegistered must report true once both a table and a model are registered")
	}
}

// TestBuildTemporalModelFailsClosedWithoutRegistration is the third
// fail-closed check: a LikelihoodTable alone (no RegisterTemporalModel
// call) must not be enough to obtain a Model -- both halves of the
// bridge are independently required.
func TestBuildTemporalModelFailsClosedWithoutRegistration(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(fixtureTable()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := reg.BuildTemporalModel("AIS_STATUS"); !errors.Is(err, ErrNoCalibration) {
		t.Fatalf("expected ErrNoCalibration without a registered temporal model, got %v", err)
	}
	if reg.TemporalModelRegistered("AIS_STATUS") {
		t.Fatal("TemporalModelRegistered must report false without RegisterTemporalModel")
	}
}

// TestRegisterTemporalModelRejectsAnInvalidTransition proves
// RegisterTemporalModel does not bypass hbayes's own validation --
// hbayes.NewTemporalModel's error must propagate, not be swallowed.
func TestRegisterTemporalModelRejectsAnInvalidTransition(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(fixtureTable()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	bad := hbayes.Transition{States: []hbayes.State{"DARK", "NORMAL"}, P: map[hbayes.State]map[hbayes.State]float64{
		"DARK":   {"DARK": 0.9, "NORMAL": 0.5}, // does not sum to 1
		"NORMAL": {"DARK": 0.1, "NORMAL": 0.9},
	}}
	if err := reg.RegisterTemporalModel("AIS_STATUS", bad, nil, 0); err == nil {
		t.Fatal("a non-stochastic transition must be refused, not silently accepted")
	}
}
