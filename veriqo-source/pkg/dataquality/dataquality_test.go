package dataquality

import (
	"errors"
	"math"
	"sync"
	"testing"
)

func good() Vector {
	return Vector{Completeness: 0.95, Freshness: 0.9, Validity: 0.98,
		Consistency: 0.9, Provenance: 0.8, Reliability: 0.85}
}

func TestCompositeIsAProjectionOfTheVectorNotAReplacement(t *testing.T) {
	a, err := Assess("src", "b1", 1, good(), DefaultWeights(), DefaultFloors())
	if err != nil {
		t.Fatal(err)
	}
	if !a.Acceptable {
		t.Fatalf("a healthy batch should pass: %v", a.Gates)
	}
	if a.Vector != good() {
		t.Fatal("the decomposed vector must be preserved on the assessment")
	}
}

func TestAZeroDimensionCannotBeAveragedAway(t *testing.T) {
	v := Vector{Completeness: 1, Freshness: 1, Validity: 0, Consistency: 1, Provenance: 1, Reliability: 1}
	a, err := Assess("src", "b", 1, v, DefaultWeights(), DefaultFloors())
	if err != nil {
		t.Fatal(err)
	}
	if a.Composite < 0.7 {
		t.Fatalf("precondition: the weighted mean should still look survivable, got %v", a.Composite)
	}
	if a.Acceptable {
		t.Fatal("a validity of 0 must never be acceptable regardless of the composite")
	}
	if a.Bottleneck != "validity" {
		t.Fatalf("bottleneck should be validity, got %s", a.Bottleneck)
	}
}

func TestBottleneckIsDeterministicOnTies(t *testing.T) {
	v := Vector{Completeness: 0.5, Freshness: 0.5, Validity: 0.5,
		Consistency: 0.5, Provenance: 0.5, Reliability: 0.5}
	first, _ := Assess("s", "b", 1, v, DefaultWeights(), DefaultFloors())
	for i := 0; i < 20; i++ {
		a, _ := Assess("s", "b", 1, v, DefaultWeights(), DefaultFloors())
		if a.Bottleneck != first.Bottleneck || a.Hash != first.Hash {
			t.Fatal("tie-breaking must be deterministic across runs")
		}
	}
}

func TestOutOfRangeDimensionRejected(t *testing.T) {
	v := good()
	v.Freshness = 1.5
	if _, err := Assess("s", "b", 1, v, DefaultWeights(), DefaultFloors()); !errors.Is(err, ErrDimensionRange) {
		t.Fatalf("expected range error, got %v", err)
	}
}

func TestMalformedWeightsRejected(t *testing.T) {
	w := DefaultWeights()
	w.Validity = 0.9
	if _, err := Assess("s", "b", 1, good(), w, DefaultFloors()); !errors.Is(err, ErrWeightSum) {
		t.Fatalf("expected weight-sum error, got %v", err)
	}
}

func TestWeightVersionIsPartOfTheHash(t *testing.T) {
	w1 := DefaultWeights()
	w2 := DefaultWeights()
	w2.Version = 2
	a, _ := Assess("s", "b", 1, good(), w1, DefaultFloors())
	b, _ := Assess("s", "b", 1, good(), w2, DefaultFloors())
	if a.Hash == b.Hash {
		t.Fatal("changing the weighting version must change the assessment hash")
	}
}

func TestFreshnessDecaysDeterministicallyInTicks(t *testing.T) {
	if f := FreshnessAt(100, 100, 50); f != 1 {
		t.Fatalf("no age means full freshness, got %v", f)
	}
	half := FreshnessAt(100, 150, 50)
	if math.Abs(half-0.5) > 1e-12 {
		t.Fatalf("one half-life should halve freshness, got %v", half)
	}
	if FreshnessAt(100, 250, 50) >= half {
		t.Fatal("freshness must be monotonically decreasing in age")
	}
}

func learner(t *testing.T) *Learner {
	t.Helper()
	l := NewLearner(DefaultConfig())
	if err := l.Register("ais-vendor-a", 0.8); err != nil {
		t.Fatal(err)
	}
	return l
}

func TestReliabilityMovesWithEvidenceAndIsBounded(t *testing.T) {
	l := learner(t)
	start, _ := l.Reliability("ais-vendor-a")
	u, err := l.Observe("o1", "ais-vendor-a", 1, false, 1, "contradicted by SAR", []string{"e1"})
	if err != nil {
		t.Fatal(err)
	}
	if u.After >= start {
		t.Fatal("an incorrect observation must lower reliability")
	}
	if math.Abs(u.After-u.Before) > DefaultConfig().MaxStepPerUpdate+1e-12 {
		t.Fatalf("a single observation moved reliability by more than the cap: %v -> %v", u.Before, u.After)
	}
}

func TestSingleAdversarialObservationCannotCollapseASource(t *testing.T) {
	l := learner(t)
	_, err := l.Observe("o1", "ais-vendor-a", 1, false, 1e9, "adversarial ground truth", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := l.Reliability("ais-vendor-a")
	if got < 0.8-DefaultConfig().MaxStepPerUpdate-1e-12 {
		t.Fatalf("an enormous weight must still be capped, got %v", got)
	}
	if got < DefaultConfig().Floor {
		t.Fatalf("reliability must never drop below the floor, got %v", got)
	}
}

func TestReliabilityNeverReachesZeroOrOne(t *testing.T) {
	cfg := DefaultConfig()
	l := NewLearner(cfg)
	_ = l.Register("s", 0.5)
	for i := 0; i < 500; i++ {
		if _, err := l.Observe("bad"+itoa(i), "s", uint64(i), false, 5, "", nil); err != nil {
			t.Fatal(err)
		}
	}
	lo, _ := l.Reliability("s")
	if lo <= 0 || lo < cfg.Floor-1e-12 {
		t.Fatalf("floor violated: %v", lo)
	}
	l2 := NewLearner(cfg)
	_ = l2.Register("s", 0.5)
	for i := 0; i < 500; i++ {
		if _, err := l2.Observe("ok"+itoa(i), "s", uint64(i), true, 5, "", nil); err != nil {
			t.Fatal(err)
		}
	}
	hi, _ := l2.Reliability("s")
	if hi >= 1 || hi > cfg.Ceiling+1e-12 {
		t.Fatalf("ceiling violated: %v", hi)
	}
}

func TestUnregisteredSourceCannotTeachTheSystem(t *testing.T) {
	l := learner(t)
	if _, err := l.Observe("o", "ghost-feed", 1, true, 1, "", nil); !errors.Is(err, ErrUnknownSource) {
		t.Fatalf("expected unknown-source rejection, got %v", err)
	}
}

func TestDuplicateObservationRejected(t *testing.T) {
	l := learner(t)
	if _, err := l.Observe("o1", "ais-vendor-a", 1, true, 1, "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Observe("o1", "ais-vendor-a", 2, true, 1, "", nil); !errors.Is(err, ErrDuplicateObservation) {
		t.Fatalf("expected duplicate rejection, got %v", err)
	}
}

func TestBackdatedObservationRejected(t *testing.T) {
	l := learner(t)
	if _, err := l.Observe("o1", "ais-vendor-a", 10, true, 1, "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Observe("o2", "ais-vendor-a", 5, true, 1, "", nil); !errors.Is(err, ErrNonMonotonicTick) {
		t.Fatalf("expected monotonic-tick rejection, got %v", err)
	}
}

func TestHistoricalReliabilityIsAnsweredFromTheLedger(t *testing.T) {
	l := learner(t)
	_, _ = l.Observe("o1", "ais-vendor-a", 10, false, 1, "", nil)
	atTen, _ := l.ReliabilityAt("ais-vendor-a", 10)
	_, _ = l.Observe("o2", "ais-vendor-a", 20, false, 1, "", nil)
	stillAtTen, _ := l.ReliabilityAt("ais-vendor-a", 10)
	if atTen != stillAtTen {
		t.Fatalf("a later update changed the historical answer: %v -> %v", atTen, stillAtTen)
	}
	now, _ := l.Reliability("ais-vendor-a")
	if now >= stillAtTen {
		t.Fatal("the current value should reflect the newer negative evidence")
	}
	before, _ := l.ReliabilityAt("ais-vendor-a", 0)
	if before != 0.8 {
		t.Fatalf("before any update the answer must be the declared value, got %v", before)
	}
}

func TestLedgerIsHashChainedAndTamperEvident(t *testing.T) {
	l := learner(t)
	for i := 0; i < 5; i++ {
		if _, err := l.Observe("o"+itoa(i), "ais-vendor-a", uint64(i), i%2 == 0, 1, "r", []string{"e"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.VerifyChain(); err != nil {
		t.Fatal(err)
	}
	if l.Head() == "" {
		t.Fatal("head must be non-empty after updates")
	}
	l.mu.Lock()
	l.ledger[2].After = 0.99
	l.mu.Unlock()
	if err := l.VerifyChain(); !errors.Is(err, ErrLedgerTampered) {
		t.Fatalf("tampering must be detected, got %v", err)
	}
}

func TestReplayFromLedgerReproducesEveryHash(t *testing.T) {
	l := learner(t)
	for i := 0; i < 8; i++ {
		if _, err := l.Observe("o"+itoa(i), "ais-vendor-a", uint64(i), i%3 != 0, float64(i%3)+1, "r", []string{"e"}); err != nil {
			t.Fatal(err)
		}
	}
	replayed, err := Replay(DefaultConfig(), map[string]float64{"ais-vendor-a": 0.8}, l.Ledger())
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Head() != l.Head() {
		t.Fatal("replayed head must match the original head")
	}
	a, _ := l.Reliability("ais-vendor-a")
	b, _ := replayed.Reliability("ais-vendor-a")
	if a != b {
		t.Fatalf("replay diverged: %v vs %v", a, b)
	}
}

func TestReplayUnderADifferentCapIsDetected(t *testing.T) {
	l := learner(t)
	for i := 0; i < 5; i++ {
		_, _ = l.Observe("o"+itoa(i), "ais-vendor-a", uint64(i), false, 3, "", nil)
	}
	cfg := DefaultConfig()
	cfg.MaxStepPerUpdate = 0.2
	if _, err := Replay(cfg, map[string]float64{"ais-vendor-a": 0.8}, l.Ledger()); err == nil {
		t.Fatal("replaying under a different learner configuration must be detected, not accepted")
	}
}

func TestExplainNamesTheClamp(t *testing.T) {
	l := learner(t)
	_, _ = l.Observe("o1", "ais-vendor-a", 1, false, 500, "big hit", []string{"e1"})
	lines, err := l.Explain("ais-vendor-a")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range lines {
		if contains(s, "CLAMPED") {
			found = true
		}
	}
	if !found {
		t.Fatalf("a clamped update must be visible in the explanation: %v", lines)
	}
}

func TestConcurrentObservationsKeepTheChainIntact(t *testing.T) {
	l := NewLearner(DefaultConfig())
	for i := 0; i < 4; i++ {
		if err := l.Register("s"+itoa(i), 0.7); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				_, _ = l.Observe("s"+itoa(i)+"-"+itoa(j), "s"+itoa(i), uint64(j), j%2 == 0, 1, "", nil)
			}
		}(i)
	}
	wg.Wait()
	if err := l.VerifyChain(); err != nil {
		t.Fatal(err)
	}
	if got := len(l.Ledger()); got != 100 {
		t.Fatalf("expected 100 ledger entries, got %d", got)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
