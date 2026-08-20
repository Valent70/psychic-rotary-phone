package execution

import (
	"testing"
	"time"

	"veriqo/veriqo/core/runtime"
)

// TestCanonicalHashStableAcrossWallClockJitter is the exact regression
// this method exists to prevent: two executions of the identical
// pipeline over identical inputs, where the second run is artificially
// slowed down (so its recorded Duration values genuinely differ from
// the first run's), must still produce an identical CanonicalHash —
// proving the hash is a function of logical execution outcome only,
// never of wall-clock timing.
func TestCanonicalHashStableAcrossWallClockJitter(t *testing.T) {
	build := func(slow bool) *ExecutionTrace {
		e := &fakeEngine{
			meta: runtime.Metadata{Name: "e", Capabilities: []runtime.Capability{"cap.x"}},
			invoke: func(method string, args map[string]string) (map[string]string, error) {
				if slow {
					time.Sleep(15 * time.Millisecond)
				}
				return map[string]string{"result": "ok"}, nil
			},
		}
		m := buildManager(t, e)
		o := New(m)
		p := Pipeline{Name: "hash-stability", Steps: []Step{
			{Name: "step-a", Capability: "cap.x", Method: "run"},
		}}
		trace, err := o.Execute(p, nil)
		if err != nil {
			t.Fatal(err)
		}
		return trace
	}

	fast := build(false)
	slow := build(true)

	if fast.Attempts[0].Duration == slow.Attempts[0].Duration {
		t.Fatalf("test setup invalid: expected genuinely different wall-clock durations, got equal (%v)", fast.Attempts[0].Duration)
	}
	if fast.CanonicalHash() != slow.CanonicalHash() {
		t.Fatalf("CanonicalHash must be identical regardless of wall-clock jitter: fast=%s slow=%s",
			fast.CanonicalHash(), slow.CanonicalHash())
	}
}

// TestCanonicalHashChangesOnLogicalDifference is the complementary
// proof: the hash must NOT be a constant — a genuinely different
// outcome (different step result) must change it.
func TestCanonicalHashChangesOnLogicalDifference(t *testing.T) {
	build := func(result string) *ExecutionTrace {
		e := &fakeEngine{
			meta: runtime.Metadata{Name: "e", Capabilities: []runtime.Capability{"cap.x"}},
			invoke: func(method string, args map[string]string) (map[string]string, error) {
				return map[string]string{"result": result}, nil
			},
		}
		m := buildManager(t, e)
		o := New(m)
		p := Pipeline{Name: "hash-sensitivity", Steps: []Step{
			{Name: "step-a", Capability: "cap.x", Method: "run"},
		}}
		trace, err := o.Execute(p, nil)
		if err != nil {
			t.Fatal(err)
		}
		return trace
	}

	a := build("outcome-A")
	b := build("outcome-B")
	if a.CanonicalHash() == b.CanonicalHash() {
		t.Fatalf("expected different step outcomes to produce different CanonicalHash values")
	}
}
