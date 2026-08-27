package calibration

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"veriqo/pkg/moat/hbayes"
)

// pipelineFixtureCorpus is a clearly-labelled FIXTURE corpus, following
// this package's own standing convention. It is large enough to split
// and evaluate, which is the only reason it exists: it proves the
// machinery runs. It is NOT, and is never claimed to be, a real
// production calibration dataset.
func pipelineFixtureCorpus() Dataset {
	ds := Dataset{Name: "fixture:synthetic-holdout-corpus-v1 (NOT a real production calibration dataset)"}
	// A deliberately learnable relationship: OFF mostly co-occurs with
	// DARK, ON mostly with NORMAL. Real corpora are not this clean; that
	// is exactly why this one can never be declared REAL_INVESTIGATED.
	for i := 0; i < 120; i++ {
		state := hbayes.State("NORMAL")
		value := "ON"
		switch {
		case i%10 < 3:
			state, value = "DARK", "OFF"
		case i%10 == 3:
			state, value = "DARK", "ON" // deliberate noise
		case i%10 == 4:
			state, value = "NORMAL", "OFF" // deliberate noise
		}
		ds.Events = append(ds.Events, LabeledEvent{
			EventID:   fmt.Sprintf("fixture-event-%03d", i),
			Predicate: "AIS_STATUS", Value: value, TrueState: state,
		})
	}
	return ds
}

func fixtureDeclaration() CorpusDeclaration {
	return CorpusDeclaration{
		Provenance:  ProvenanceFixture,
		Owner:       "veriqo-engineering (unit test)",
		Description: "synthetic hand-generated events proving the holdout/fit/evaluate machinery runs; NOT real history",
	}
}

func pipelineStates() []hbayes.State { return []hbayes.State{"DARK", "NORMAL"} }

func buildFixtureModel(t *testing.T) CalibratedModel {
	t.Helper()
	m, err := BuildModel("AIS_STATUS", pipelineFixtureCorpus(), fixtureDeclaration(),
		pipelineStates(), "temporal-v1", 1, 5, 0.3, 10)
	if err != nil {
		t.Fatalf("BuildModel: %v", err)
	}
	return m
}

// TestFixtureCorpusAlwaysReportsExternalDataRequired is PHASE F2's
// headline requirement: the machinery runs end to end, and the status
// still says the real data is missing, because it is.
func TestFixtureCorpusAlwaysReportsExternalDataRequired(t *testing.T) {
	m := buildFixtureModel(t)

	if m.Status != StatusExternalDataRequired {
		t.Fatalf("Status = %q, want EXTERNAL_DATA_REQUIRED", m.Status)
	}
	if m.Qualified() {
		t.Fatal("a fixture-backed model reported itself qualified")
	}
	if err := m.Assert(); err == nil {
		t.Fatal("Assert accepted a fixture-backed model -- a caller could read Status and use the table anyway")
	}
	if len(m.Limitations) == 0 {
		t.Fatal("an unqualified model states no limitations")
	}
	// The machinery genuinely ran: a real split, a real fit, real
	// held-out evaluations.
	if len(m.Holdout.Train.Events) == 0 || len(m.Holdout.Test.Events) == 0 {
		t.Fatal("the holdout is empty on one side; the machinery did not really run")
	}
	if len(m.Table.Likelihood) == 0 {
		t.Fatal("no likelihood table was fitted")
	}
	if len(m.Evaluations) != len(pipelineStates()) {
		t.Fatalf("evaluated %d states, want %d", len(m.Evaluations), len(pipelineStates()))
	}
	for _, e := range m.Evaluations {
		if e.Samples == 0 {
			t.Errorf("evaluation for %s scored zero samples", e.TargetState)
		}
	}
}

// TestNoAmountOfRunningTheMachineryRaisesTheStatus is the anti-false-
// green core of this phase: the status is derived from the DECLARATION,
// so re-running, enlarging or improving the fixture cannot promote it.
func TestNoAmountOfRunningTheMachineryRaisesTheStatus(t *testing.T) {
	big := pipelineFixtureCorpus()
	// Ten times the data, perfectly separable, best-case everything.
	for i := 0; i < 1200; i++ {
		state, value := hbayes.State("NORMAL"), "ON"
		if i%2 == 0 {
			state, value = "DARK", "OFF"
		}
		big.Events = append(big.Events, LabeledEvent{
			EventID:   fmt.Sprintf("fixture-extra-%04d", i),
			Predicate: "AIS_STATUS", Value: value, TrueState: state,
		})
	}
	m, err := BuildModel("AIS_STATUS", big, fixtureDeclaration(), pipelineStates(), "temporal-v1", 1, 5, 0.3, 10)
	if err != nil {
		t.Fatalf("BuildModel: %v", err)
	}
	if m.Status != StatusExternalDataRequired {
		t.Fatalf("Status = %q after 10x more, cleaner fixture data -- the status must be derived from provenance, not performance", m.Status)
	}
}

// TestAnUndeclaredCorpusIsRefused: there is no default provenance. Code
// cannot look at a slice of events and tell whether they are real, so
// this pipeline asks rather than guessing.
func TestAnUndeclaredCorpusIsRefused(t *testing.T) {
	cases := map[string]CorpusDeclaration{
		"zero value":         {},
		"no owner":           {Provenance: ProvenanceFixture, Description: "x"},
		"no description":     {Provenance: ProvenanceFixture, Owner: "x"},
		"unknown provenance": {Provenance: Provenance("PROBABLY_REAL"), Owner: "x", Description: "x"},
	}
	for name, decl := range cases {
		if _, err := BuildModel("AIS_STATUS", pipelineFixtureCorpus(), decl,
			pipelineStates(), "temporal-v1", 1, 5, 0.3, 10); !errors.Is(err, ErrCorpusUndeclared) {
			t.Errorf("%s: err = %v, want ErrCorpusUndeclared", name, err)
		}
	}
}

// TestHoldoutIsDeterministicAndDisjoint proves the split is real: the
// same corpus always splits identically, and no event appears on both
// sides. Without disjointness the "held-out" evaluation would be
// scoring the model on its own training data.
func TestHoldoutIsDeterministicAndDisjoint(t *testing.T) {
	ds := pipelineFixtureCorpus()
	a, err := Split(ds, 0.3, 10)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	b, err := Split(ds, 0.3, 10)
	if err != nil {
		t.Fatalf("Split (2nd): %v", err)
	}
	if a.Train.Hash() != b.Train.Hash() || a.Test.Hash() != b.Test.Hash() {
		t.Fatal("the same corpus split differently on two runs -- a model's evaluation would be unreproducible")
	}

	inTrain := map[string]bool{}
	for _, e := range a.Train.Events {
		inTrain[e.EventID] = true
	}
	for _, e := range a.Test.Events {
		if inTrain[e.EventID] {
			t.Fatalf("event %s is in BOTH the training and the held-out set", e.EventID)
		}
	}
	if len(a.Train.Events)+len(a.Test.Events) != len(ds.Events) {
		t.Fatal("the split lost or duplicated events")
	}
	if a.Actual <= 0 || a.Actual >= 1 {
		t.Fatalf("actual test fraction %.4f is not a real fraction", a.Actual)
	}
}

// TestSplitIsStableWhenAnEventIsInsertedInTheMiddle records the reason
// the split hashes each event's identity rather than using its
// position: a positional split would reshuffle the whole corpus across
// the boundary whenever one event was added.
func TestSplitIsStableWhenAnEventIsInsertedInTheMiddle(t *testing.T) {
	ds := pipelineFixtureCorpus()
	before, err := Split(ds, 0.3, 10)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	beforeSide := map[string]bool{}
	for _, e := range before.Test.Events {
		beforeSide[e.EventID] = true
	}

	inserted := Dataset{Name: ds.Name}
	inserted.Events = append(inserted.Events, ds.Events[:50]...)
	inserted.Events = append(inserted.Events, LabeledEvent{
		EventID: "fixture-inserted-mid", Predicate: "AIS_STATUS", Value: "ON", TrueState: "NORMAL"})
	inserted.Events = append(inserted.Events, ds.Events[50:]...)

	after, err := Split(inserted, 0.3, 10)
	if err != nil {
		t.Fatalf("Split (inserted): %v", err)
	}
	afterSide := map[string]bool{}
	for _, e := range after.Test.Events {
		afterSide[e.EventID] = true
	}
	for _, e := range ds.Events {
		if beforeSide[e.EventID] != afterSide[e.EventID] {
			t.Fatalf("inserting one event moved %s across the train/test boundary", e.EventID)
		}
	}
}

func TestSplitRefusesADegenerateHoldout(t *testing.T) {
	ds := pipelineFixtureCorpus()
	for _, f := range []float64{0, 1, -0.1, 1.5} {
		if _, err := Split(ds, f, 10); !errors.Is(err, ErrHoldoutFraction) {
			t.Errorf("fraction %.2f: err = %v, want ErrHoldoutFraction", f, err)
		}
	}
	tiny := Dataset{Name: "tiny", Events: ds.Events[:4]}
	if _, err := Split(tiny, 0.3, 10); !errors.Is(err, ErrHoldoutTooSmall) {
		t.Fatalf("err = %v, want ErrHoldoutTooSmall -- a 'holdout' of two events is not a holdout", err)
	}
}

// TestEvaluationUsesTheRepositorysOwnMetrics confirms the evaluation is
// computed by pkg/moat/reliability rather than by a second, unvalidated
// metric implementation, by checking the values are in the ranges those
// metrics guarantee.
func TestEvaluationUsesTheRepositorysOwnMetrics(t *testing.T) {
	m := buildFixtureModel(t)
	for _, e := range m.Evaluations {
		if e.Brier < 0 || e.Brier > 1 {
			t.Errorf("%s: Brier %.6f is outside [0,1]", e.TargetState, e.Brier)
		}
		if e.ECE < 0 || e.ECE > 1 {
			t.Errorf("%s: ECE %.6f is outside [0,1]", e.TargetState, e.ECE)
		}
		if e.LogLoss < 0 {
			t.Errorf("%s: LogLoss %.6f is negative", e.TargetState, e.LogLoss)
		}
		if e.Bins <= 0 {
			t.Errorf("%s: evaluation records no bin count", e.TargetState)
		}
	}
}

// TestEvaluateSkipsUnseenValuesRatherThanInventingAProbability records
// a deliberate decision: a held-out value the training set never
// contained is EXCLUDED from the sample count, not assigned a
// made-up probability.
func TestEvaluateSkipsUnseenValuesRatherThanInventingAProbability(t *testing.T) {
	m := buildFixtureModel(t)

	withUnseen := m.Holdout.Test
	withUnseen.Events = append(append([]LabeledEvent(nil), withUnseen.Events...),
		LabeledEvent{EventID: "unseen-1", Predicate: "AIS_STATUS", Value: "TRANSPONDER_SPOOFED", TrueState: "DARK"})

	base, err := Evaluate(m.Table, m.Holdout.Test, "DARK", 10)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	withExtra, err := Evaluate(m.Table, withUnseen, "DARK", 10)
	if err != nil {
		t.Fatalf("Evaluate (with unseen value): %v", err)
	}
	if withExtra.Samples != base.Samples {
		t.Fatalf("an unseen value changed the sample count %d -> %d; it must be excluded, not scored with an invented probability",
			base.Samples, withExtra.Samples)
	}
}

func TestEvaluateFailsClosedWithNoEvaluableSample(t *testing.T) {
	m := buildFixtureModel(t)
	empty := Dataset{Name: "empty"}
	if _, err := Evaluate(m.Table, empty, "DARK", 10); !errors.Is(err, ErrNoEvaluationSample) {
		t.Fatalf("err = %v, want ErrNoEvaluationSample", err)
	}
}

// TestModelHashBindsCorpusFitAndEvaluationTogether makes the "Model"
// stage's binding real: a likelihood table with no stated evaluation,
// or an evaluation with no stated corpus, is the free-floating number
// this pipeline exists to prevent.
func TestModelHashBindsCorpusFitAndEvaluationTogether(t *testing.T) {
	m := buildFixtureModel(t)
	if m.Hash == "" {
		t.Fatal("model is not content-addressed")
	}
	if buildFixtureModel(t).Hash != m.Hash {
		t.Fatal("model hash is not deterministic")
	}

	mutations := map[string]func(*CalibratedModel){
		"status":              func(x *CalibratedModel) { x.Status = StatusRealCorpusFitted },
		"owner":               func(x *CalibratedModel) { x.Declaration.Owner = "someone-else" },
		"provenance":          func(x *CalibratedModel) { x.Declaration.Provenance = ProvenanceRealInvestigated },
		"train set":           func(x *CalibratedModel) { x.Holdout.Train.Events = x.Holdout.Train.Events[:5] },
		"test set":            func(x *CalibratedModel) { x.Holdout.Test.Events = x.Holdout.Test.Events[:5] },
		"evaluation log loss": func(x *CalibratedModel) { x.Evaluations[0].LogLoss = 0 },
		"dataset provenance":  func(x *CalibratedModel) { x.Table.Record.DatasetProvenance = "sha256:different" },
	}
	for name, mutate := range mutations {
		mutated := m
		mutated.Holdout.Train.Events = append([]LabeledEvent(nil), m.Holdout.Train.Events...)
		mutated.Holdout.Test.Events = append([]LabeledEvent(nil), m.Holdout.Test.Events...)
		mutated.Evaluations = append([]Evaluation(nil), m.Evaluations...)
		mutate(&mutated)
		if modelHash(mutated) == m.Hash {
			t.Errorf("changing %s did not change the model hash", name)
		}
	}
}

// TestFitHappensOnTheTrainingHalfOnly proves the model was not fit on
// the data it was then scored against, which would make every metric
// meaningless.
func TestFitHappensOnTheTrainingHalfOnly(t *testing.T) {
	m := buildFixtureModel(t)
	if m.Table.Record.DatasetProvenance != m.Holdout.Train.Hash() {
		t.Fatalf("the fitted table cites %q as its dataset, but the training set hashes to %q -- the fit did not happen on the training half",
			m.Table.Record.DatasetProvenance, m.Holdout.Train.Hash())
	}
	if m.Table.Record.DatasetProvenance == m.Holdout.Test.Hash() {
		t.Fatal("the table was fit on the held-out set")
	}
}

// TestDeclarationDescriptionIsCarriedIntoTheCalibrationSource keeps the
// fixture label attached to the artifact: a table whose provenance
// string does not say FIXTURE is one someone could later mistake for
// production calibration.
func TestDeclarationDescriptionIsCarriedIntoTheCalibrationSource(t *testing.T) {
	m := buildFixtureModel(t)
	if !strings.Contains(m.Table.Record.CalibrationSource, string(ProvenanceFixture)) {
		t.Fatalf("CalibrationSource %q does not carry the FIXTURE provenance forward", m.Table.Record.CalibrationSource)
	}
}

// TestRealCorpusPathIsReachableButNotReachedHere documents the honest
// boundary: the REAL_CORPUS_FITTED branch is real, tested code -- it is
// simply not reachable from anything in this repository, because
// nothing here can truthfully declare a corpus REAL_INVESTIGATED.
//
// The declaration below is made INSIDE this test only, to exercise the
// branch. It is not, and must never become, a production declaration.
func TestRealCorpusPathIsReachableButNotReachedHere(t *testing.T) {
	declaredReal := CorpusDeclaration{
		Provenance:  ProvenanceRealInvestigated,
		Owner:       "unit-test-only",
		Description: "TEST-ONLY declaration exercising the REAL_INVESTIGATED branch; the underlying events are still synthetic",
	}
	m, err := BuildModel("AIS_STATUS", pipelineFixtureCorpus(), declaredReal,
		pipelineStates(), "temporal-v1", 1, 5, 0.3, 10)
	if err != nil {
		t.Fatalf("BuildModel: %v", err)
	}
	if m.Status != StatusRealCorpusFitted {
		t.Fatalf("Status = %q; the REAL_INVESTIGATED branch is unreachable, which would make it dead code", m.Status)
	}
	if !m.Qualified() {
		t.Fatal("a fully-evaluated real-declared corpus did not qualify")
	}
	if err := m.Assert(); err != nil {
		t.Fatalf("Assert: %v", err)
	}
}

// TestIncompleteEvaluationYieldsInsufficientCorpus proves the middle
// status is real: a model that cannot be scored on every state it
// claims to model is not qualified, even with a real corpus.
func TestIncompleteEvaluationYieldsInsufficientCorpus(t *testing.T) {
	status, limits := deriveStatus(
		CorpusDeclaration{Provenance: ProvenanceRealInvestigated, Owner: "o", Description: "d"},
		[]hbayes.State{"DARK", "NORMAL", "AMBIGUOUS"},
		[]Evaluation{{TargetState: "DARK", Samples: 10}, {TargetState: "NORMAL", Samples: 10}},
	)
	if status != StatusInsufficientCorpus {
		t.Fatalf("Status = %q, want INSUFFICIENT_CORPUS", status)
	}
	if len(limits) == 0 {
		t.Fatal("INSUFFICIENT_CORPUS states no limitation")
	}
}

// TestPosteriorFallsBackToThePriorRatherThanInventing covers the
// zero-likelihood edge: when every state assigns an observed value zero
// likelihood, the observation carries no information, and returning the
// prior is the only non-inventing answer.
func TestPosteriorFallsBackToThePriorRatherThanInventing(t *testing.T) {
	prior := map[hbayes.State]float64{"DARK": 0.25, "NORMAL": 0.75}
	zero := map[hbayes.State]float64{"DARK": 0, "NORMAL": 0}
	if got := posteriorFor(zero, prior, "DARK"); got != 0.25 {
		t.Fatalf("posterior = %.6f, want the prior 0.25", got)
	}
}
