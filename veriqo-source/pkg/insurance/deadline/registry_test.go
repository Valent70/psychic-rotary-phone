package deadline

import (
	"errors"
	"testing"
)

func mustRule(t *testing.T, id string, st SourceType, sourceDoc string, duration uint64, cr CalendarRule) Rule {
	t.Helper()
	sourceClause := ""
	if st == SourceTypePolicy {
		sourceClause = "CL-" + id
	}
	r, err := New(id, st, sourceClause, sourceDoc, "V1", "NOTICE", duration, cr, "UTC")
	if err != nil {
		t.Fatalf("unexpected error building rule %s: %v", id, err)
	}
	return r
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	reg := NewRegistry()
	r := mustRule(t, "DL-1", SourceTypeContract, "DOC-1", 30, CalendarRuleCalendarDays)
	if err := reg.Register(r); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := reg.Get("DL-1")
	if !ok {
		t.Fatalf("expected rule to be found")
	}
	if got.RuleID != "DL-1" {
		t.Fatalf("unexpected rule: %+v", got)
	}
	if _, ok := reg.Get("NOPE"); ok {
		t.Fatalf("expected unknown rule ID to be absent")
	}
}

func TestRegistry_RejectsInvalidRule(t *testing.T) {
	reg := NewRegistry()
	invalid := Rule{RuleID: "DL-BAD"} // everything else zero/empty
	if err := reg.Register(invalid); err == nil {
		t.Fatalf("expected an error registering an invalid rule")
	}
	if reg.Count() != 0 {
		t.Fatalf("expected registry to remain empty after a rejected registration")
	}
}

func TestRegistry_RejectsDuplicateRuleID(t *testing.T) {
	reg := NewRegistry()
	r := mustRule(t, "DL-DUP", SourceTypeContract, "DOC-1", 30, CalendarRuleCalendarDays)
	if err := reg.Register(r); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := reg.Register(r); !errors.Is(err, ErrDuplicateRuleID) {
		t.Fatalf("expected ErrDuplicateRuleID, got %v", err)
	}
}

func TestRegistry_AllPreservesOrder(t *testing.T) {
	reg := NewRegistry()
	ids := []string{"DL-A", "DL-B", "DL-C"}
	for _, id := range ids {
		if err := reg.Register(mustRule(t, id, SourceTypeContract, "DOC-1", 10, CalendarRuleCalendarDays)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	all := reg.All()
	if len(all) != len(ids) {
		t.Fatalf("expected %d rules, got %d", len(ids), len(all))
	}
	for i, id := range ids {
		if all[i].RuleID != id {
			t.Fatalf("expected order-preserving All(), got %+v", all)
		}
	}
	if reg.Count() != 3 {
		t.Fatalf("expected Count()==3, got %d", reg.Count())
	}
}

// TestRegistry_ComputeAll_DifferentDurationsStayDifferent is the
// registry-level companion to the adversarial "no hard-coded 1 year"
// test: two rules with different durations, resolved through the
// registry against the same trigger tick, must produce two different
// deadlines.
func TestRegistry_ComputeAll_DifferentDurationsStayDifferent(t *testing.T) {
	reg := NewRegistry()
	short := mustRule(t, "DL-SHORT", SourceTypePolicy, "POLICY-DOC", 90, CalendarRuleCalendarDays)
	long := mustRule(t, "DL-LONG", SourceTypeBillOfLading, "BL-DOC", 365, CalendarRuleCalendarDays)
	if err := reg.Register(short); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := reg.Register(long); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	deadlines, err := reg.ComputeAll(10_000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deadlines) != 2 {
		t.Fatalf("expected 2 deadlines, got %d", len(deadlines))
	}
	byRule := map[string]uint64{}
	for _, d := range deadlines {
		byRule[d.RuleID] = d.DeadlineTick
	}
	if byRule["DL-SHORT"] != 10_090 {
		t.Fatalf("expected DL-SHORT deadline 10090, got %d", byRule["DL-SHORT"])
	}
	if byRule["DL-LONG"] != 10_365 {
		t.Fatalf("expected DL-LONG deadline 10365, got %d", byRule["DL-LONG"])
	}
	if byRule["DL-SHORT"] == byRule["DL-LONG"] {
		t.Fatalf("expected different deadlines for different durations, both were %d", byRule["DL-SHORT"])
	}
}

func TestRegistry_ComputeAll_EmptyRegistry(t *testing.T) {
	reg := NewRegistry()
	deadlines, err := reg.ComputeAll(1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deadlines) != 0 {
		t.Fatalf("expected no deadlines from an empty registry, got %d", len(deadlines))
	}
}
