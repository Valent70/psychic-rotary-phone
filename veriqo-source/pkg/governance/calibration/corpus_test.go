package calibration

import (
	"errors"
	"strconv"
	"testing"

	"veriqo/pkg/moat/hbayes"
)

// fixtureCorpus is a clearly-labelled, synthetic dataset of labeled
// historical events -- mirroring fixtureTable's own "clearly labeled
// synthetic" convention in calibration_test.go, and
// test/integration/dark_vessel_test.go's before it. It exists only to
// prove Fit's frequentist-counting machinery is real and correct; it is
// never asserted as a real production calibration corpus. Hand-designed
// so the resulting counts are exactly checkable by hand:
//
//	TrueState=DARK:   6 events, AIS_STATUS=OFF x5, AIS_STATUS=ON x1
//	TrueState=NORMAL: 4 events, AIS_STATUS=OFF x1, AIS_STATUS=ON x3
//
// so P(OFF|DARK)=5/6, P(ON|DARK)=1/6, P(OFF|NORMAL)=1/4, P(ON|NORMAL)=3/4,
// and Prior(DARK)=6/10, Prior(NORMAL)=4/10.
func fixtureCorpus() Dataset {
	events := make([]LabeledEvent, 0, 10)
	darkOff := 5
	darkOn := 1
	normalOff := 1
	normalOn := 3
	id := 0
	add := func(state hbayes.State, value string, n int) {
		for i := 0; i < n; i++ {
			id++
			events = append(events, LabeledEvent{
				EventID:   "case-" + strconv.Itoa(id),
				Predicate: "AIS_STATUS",
				Value:     value,
				TrueState: state,
			})
		}
	}
	add("DARK", "OFF", darkOff)
	add("DARK", "ON", darkOn)
	add("NORMAL", "OFF", normalOff)
	add("NORMAL", "ON", normalOn)
	return Dataset{Name: "fixture:synthetic-dark-vessel-corpus-v1 (NOT a real production calibration dataset)", Events: events}
}

var fixtureStates = []hbayes.State{"DARK", "NORMAL"}

func TestFitComputesExactFrequentistLikelihoodsFromLabeledEvents(t *testing.T) {
	ds := fixtureCorpus()
	table, err := Fit("AIS_STATUS", ds, fixtureStates, "fixture:synthetic-dark-vessel-corpus-v1 (NOT real production calibration)", "temporal-v1", 1, 3)
	if err != nil {
		t.Fatalf("Fit on a well-formed fixture corpus must succeed: %v", err)
	}
	const eps = 1e-9
	checks := []struct {
		value string
		state hbayes.State
		want  float64
	}{
		{"OFF", "DARK", 5.0 / 6.0},
		{"ON", "DARK", 1.0 / 6.0},
		{"OFF", "NORMAL", 1.0 / 4.0},
		{"ON", "NORMAL", 3.0 / 4.0},
	}
	for _, c := range checks {
		got := table.Likelihood[c.value][c.state]
		if diff := got - c.want; diff > eps || diff < -eps {
			t.Fatalf("P(%s|%s): want %.9f, got %.9f", c.value, c.state, c.want, got)
		}
	}
	if diff := table.Record.Prior["DARK"] - 0.6; diff > eps || diff < -eps {
		t.Fatalf("Prior(DARK): want 0.6, got %.9f", table.Record.Prior["DARK"])
	}
	if diff := table.Record.Prior["NORMAL"] - 0.4; diff > eps || diff < -eps {
		t.Fatalf("Prior(NORMAL): want 0.4, got %.9f", table.Record.Prior["NORMAL"])
	}
	if table.Record.DatasetProvenance != ds.Hash() {
		t.Fatalf("fitted table's DatasetProvenance must cite the exact dataset it was fit against")
	}
}

// TestFitFailsClosedOnInsufficientSamples is the adversarial case this
// package exists to catch: a state with too few labeled events must
// refuse to produce a likelihood, not silently emit a number computed
// from noise.
func TestFitFailsClosedOnInsufficientSamples(t *testing.T) {
	ds := fixtureCorpus() // NORMAL has only 4 events
	_, err := Fit("AIS_STATUS", ds, fixtureStates, "fixture", "temporal-v1", 1, 5)
	if !errors.Is(err, ErrInsufficientSamples) {
		t.Fatalf("expected ErrInsufficientSamples when a declared state has fewer than minSamples events, got %v", err)
	}
}

func TestFitFailsClosedOnEmptyDatasetForPredicate(t *testing.T) {
	ds := Dataset{Name: "empty", Events: nil}
	_, err := Fit("AIS_STATUS", ds, fixtureStates, "fixture", "temporal-v1", 1, 1)
	if !errors.Is(err, ErrEmptyDataset) {
		t.Fatalf("expected ErrEmptyDataset for a dataset with no matching events, got %v", err)
	}
}

func TestFitRejectsAnEventLabeledWithAnUndeclaredState(t *testing.T) {
	ds := Dataset{Events: []LabeledEvent{
		{EventID: "x1", Predicate: "AIS_STATUS", Value: "OFF", TrueState: "UNKNOWN_STATE"},
	}}
	_, err := Fit("AIS_STATUS", ds, fixtureStates, "fixture", "temporal-v1", 1, 1)
	if err == nil {
		t.Fatal("expected an error when a dataset event's TrueState is outside the declared state space")
	}
}

// TestDatasetHashIsContentAddressedAndDetectsTampering proves the
// integrity property real dataset provenance requires: identical
// content hashes identically regardless of event order, and changing
// even one event's label changes the hash.
func TestDatasetHashIsContentAddressedAndDetectsTampering(t *testing.T) {
	ds := fixtureCorpus()
	h1 := ds.Hash()

	reversed := Dataset{Name: ds.Name, Events: make([]LabeledEvent, len(ds.Events))}
	for i, e := range ds.Events {
		reversed.Events[len(ds.Events)-1-i] = e
	}
	if reversed.Hash() != h1 {
		t.Fatal("dataset hash must not depend on event order")
	}

	tampered := Dataset{Name: ds.Name, Events: append([]LabeledEvent{}, ds.Events...)}
	tampered.Events[0].TrueState = "NORMAL" // flip one label
	if tampered.Hash() == h1 {
		t.Fatal("flipping a single event's label must change the dataset hash")
	}
}

// TestFittedTableRoundTripsThroughTheRealRegistryAndProducesAnObservation
// is the end-to-end proof this file exists for: a table produced by
// Fit from labeled historical events is not just numerically correct in
// isolation, it is a genuine, registrable LikelihoodTable that flows
// through the exact same Register/BuildObservation contract every
// hand-written fixture table in calibration_test.go already does.
func TestFittedTableRoundTripsThroughTheRealRegistryAndProducesAnObservation(t *testing.T) {
	ds := fixtureCorpus()
	table, err := Fit("AIS_STATUS", ds, fixtureStates, "fixture:synthetic-dark-vessel-corpus-v1 (NOT real production calibration)", "temporal-v1", 1, 3)
	if err != nil {
		t.Fatalf("Fit: %v", err)
	}
	reg := NewRegistry()
	if err := reg.Register(table); err != nil {
		t.Fatalf("a table produced by Fit must satisfy the registry's own Validate contract: %v", err)
	}
	obs, err := reg.BuildObservation("vessel-9001", "AIS_STATUS", "OFF", 5, 0)
	if err != nil {
		t.Fatalf("BuildObservation against a Fit-produced, registered table must succeed: %v", err)
	}
	const eps = 1e-9
	if diff := obs.Likelihood["DARK"] - 5.0/6.0; diff > eps || diff < -eps {
		t.Fatalf("observation likelihood must match the fitted table exactly, got %.9f", obs.Likelihood["DARK"])
	}
}
