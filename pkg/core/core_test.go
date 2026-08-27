package core_test

import (
	"testing"
	"time"

	"veriqo/pkg/core"
)

func TestTraceIDUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for range 10_000 {
		id := core.NewTraceID()
		s := id.String()
		if seen[s] {
			t.Fatalf("duplicate TraceID: %s", s)
		}
		seen[s] = true
	}
}

func TestTraceIDZero(t *testing.T) {
	var z core.TraceID
	if !z.Zero() {
		t.Fatal("zero TraceID must be zero")
	}
	if core.NewTraceID().Zero() {
		t.Fatal("new TraceID must not be zero")
	}
}

func TestVectorClockHappensBefore(t *testing.T) {
	a := core.VectorClock{"A": 1, "B": 0}
	b := core.VectorClock{"A": 1, "B": 1}
	if !a.HappensBefore(b) {
		t.Fatal("a should happen before b")
	}
	if b.HappensBefore(a) {
		t.Fatal("b should not happen before a")
	}
}

func TestVectorClockConcurrent(t *testing.T) {
	a := core.VectorClock{"A": 2, "B": 1}
	b := core.VectorClock{"A": 1, "B": 2}
	if !a.Concurrent(b) {
		t.Fatal("a and b should be concurrent")
	}
}

func TestVectorClockMerge(t *testing.T) {
	a := core.VectorClock{"A": 3, "B": 1, "C": 0}
	b := core.VectorClock{"A": 1, "B": 4, "D": 2}
	m := a.Merge(b)
	if m["A"] != 3 || m["B"] != 4 || m["D"] != 2 {
		t.Fatalf("unexpected merged clock: %v", m)
	}
}

func TestVectorClockHappensBeforeAntiSymmetry(t *testing.T) {
	// Property: if a→b then ¬(b→a)
	a := core.VectorClock{"X": 1}
	b := core.VectorClock{"X": 2}
	if a.HappensBefore(b) == b.HappensBefore(a) {
		t.Fatal("violated: if a→b then ¬(b→a)")
	}
}

func TestSeverityString(t *testing.T) {
	cases := map[core.Severity]string{
		core.SeverityNone:     "NONE",
		core.SeverityCritical: "CRITICAL",
		core.SeverityHigh:     "HIGH",
	}
	for sev, want := range cases {
		if got := sev.String(); got != want {
			t.Errorf("Severity(%d).String() = %q, want %q", sev, got, want)
		}
	}
}

func TestDARIConstruction(t *testing.T) {
	d := core.DARI{
		TraceID:          core.NewTraceID(),
		ExecutionGraphID: "pipeline-v1",
		Clock:            core.VectorClock{"node1": 1},
		Timestamp:        time.Now(),
		DRC: core.DRC{
			SLOTarget:  0.999,
			MaxLatency: 100 * time.Millisecond,
		},
	}
	if d.TraceID.Zero() {
		t.Fatal("DARI TraceID must not be zero")
	}
}

func TestSeqMonotonic(t *testing.T) {
	var s core.Seq
	prev := s.Next()
	for range 1_000 {
		n := s.Next()
		if n <= prev {
			t.Fatalf("sequence not monotonic: %d after %d", n, prev)
		}
		prev = n
	}
}
