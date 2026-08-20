package trustcalc

import "testing"

func TestObserveUpdatesPosteriorMean(t *testing.T) {
	c := New(1, 1)
	if got := c.Score("s1"); got != 0.5 {
		t.Fatalf("expected prior mean 0.5, got %v", got)
	}
	for i := 0; i < 9; i++ {
		if _, err := c.Observe("s1", true, 1.0); err != nil {
			t.Fatal(err)
		}
	}
	got := c.Score("s1")
	if got < 0.85 || got > 0.95 {
		t.Fatalf("expected posterior mean near 0.909 after 9 matches, got %v", got)
	}
}

func TestObserveDivergesLowersMean(t *testing.T) {
	c := New(1, 1)
	for i := 0; i < 5; i++ {
		_, _ = c.Observe("bad", false, 1.0)
	}
	if got := c.Score("bad"); got >= 0.5 {
		t.Fatalf("expected mean below prior after divergent observations, got %v", got)
	}
}

func TestVarianceShrinksWithMoreObservations(t *testing.T) {
	c := New(1, 1)
	before := c.Belief("s1").Variance()
	for i := 0; i < 20; i++ {
		_, _ = c.Observe("s1", true, 1.0)
	}
	after := c.Belief("s1").Variance()
	if after >= before {
		t.Fatalf("expected variance to shrink with more observations: before=%v after=%v", before, after)
	}
}

func TestCredibleIntervalContainsMean(t *testing.T) {
	c := New(1, 1)
	for i := 0; i < 10; i++ {
		_, _ = c.Observe("s1", true, 1.0)
	}
	belief := c.Belief("s1")
	lo, hi := belief.CredibleInterval(1.96)
	m := belief.Mean()
	if !(lo <= m && m <= hi) {
		t.Fatalf("expected mean within credible interval [%v,%v], got mean=%v", lo, hi, m)
	}
	if lo < 0 || hi > 1 {
		t.Fatalf("expected interval clamped to [0,1], got [%v,%v]", lo, hi)
	}
}

func TestObserveRejectsInvalidWeight(t *testing.T) {
	c := New(1, 1)
	if _, err := c.Observe("s1", true, 0); err == nil {
		t.Fatal("expected error for weight=0")
	}
	if _, err := c.Observe("s1", true, 1.5); err == nil {
		t.Fatal("expected error for weight>1")
	}
}

func TestCombineLogOddsStrengthensBelief(t *testing.T) {
	// Prior 0.5, strong confirming evidence (likelihood ratio 9:1) should
	// push posterior well above 0.5.
	post := CombineLogOdds(0.5, 9.0)
	if post <= 0.85 || post >= 0.95 {
		t.Fatalf("expected posterior near 0.9, got %v", post)
	}
	// Disconfirming evidence (ratio < 1) should lower it.
	post2 := CombineLogOdds(0.5, 1.0/9.0)
	if post2 >= 0.15 {
		t.Fatalf("expected posterior near 0.1, got %v", post2)
	}
}

func TestSmoothIsBoundedAverage(t *testing.T) {
	got := Smooth(0.2, 0.8, 0.5)
	if got != 0.5 {
		t.Fatalf("expected midpoint 0.5, got %v", got)
	}
}

func TestSubjectsListsObserved(t *testing.T) {
	c := New(1, 1)
	_, _ = c.Observe("a", true, 1.0)
	_, _ = c.Observe("b", false, 1.0)
	subs := c.Subjects()
	if len(subs) != 2 {
		t.Fatalf("expected 2 subjects, got %v", subs)
	}
}
