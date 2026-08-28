package deadline

import (
	"errors"
	"testing"
)

// TestComputeDeadline_CalendarDays proves the basic, real CALENDAR_DAYS
// arithmetic: deadline = trigger + duration, nothing more.
func TestComputeDeadline_CalendarDays(t *testing.T) {
	r, err := New("DL-CD", SourceTypeContract, "", "CONTRACT-1", "V1", "NOTICE", 30, CalendarRuleCalendarDays, "UTC")
	if err != nil {
		t.Fatalf("unexpected error building rule: %v", err)
	}
	d, err := ComputeDeadline(r, 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.DeadlineTick != 1030 {
		t.Fatalf("expected deadline tick 1030, got %d", d.DeadlineTick)
	}
	if d.TriggerTick != 1000 {
		t.Fatalf("expected trigger tick preserved as 1000, got %d", d.TriggerTick)
	}
	if d.Timezone != "UTC" {
		t.Fatalf("expected timezone carried through, got %q", d.Timezone)
	}
	if d.RuleID != "DL-CD" {
		t.Fatalf("expected RuleID carried through, got %q", d.RuleID)
	}
}

// TestComputeDeadline_AdversarialNoHardcodedOneYear is the adversarial
// test the task explicitly demands: construct two Rules with genuinely
// different durations, sourced from two DIFFERENT documents/clauses
// (one 90-day policy clause, one 365-day B/L clause), apply both to the
// SAME trigger-event tick, and confirm the two computed deadlines are
// DIFFERENT and each is EXACTLY trigger+duration for that rule. If any
// code path in this package silently substituted a fixed "1 year"
// constant for Rule.Duration, this test would either see both
// deadlines collapse to the same tick, or see the 90-day rule produce
// a 365-day-out deadline — either failure proves a hard-coded default
// snuck in.
func TestComputeDeadline_AdversarialNoHardcodedOneYear(t *testing.T) {
	const oneYearInTicks = 365
	const ninetyDaysInTicks = 90

	policyRule, err := New(
		"DL-POLICY-90D", SourceTypePolicy, "CL-NOTICE-4", "POLICY-DOC-2024-001", "V2",
		"DAMAGE_DISCOVERY", ninetyDaysInTicks, CalendarRuleCalendarDays, "Asia/Jakarta",
	)
	if err != nil {
		t.Fatalf("unexpected error building policy-sourced rule: %v", err)
	}

	blRule, err := New(
		"DL-BL-365D", SourceTypeBillOfLading, "CL-HAGUE-3", "BL-2024-XYZ-777", "ORIGINAL",
		"DAMAGE_DISCOVERY", oneYearInTicks, CalendarRuleCalendarDays, "UTC",
	)
	if err != nil {
		t.Fatalf("unexpected error building B/L-sourced rule: %v", err)
	}

	// Both rules trigger off the exact same tick — a shared
	// DAMAGE_DISCOVERY event.
	const triggerTick = 500_000

	policyDeadline, err := ComputeDeadline(policyRule, triggerTick)
	if err != nil {
		t.Fatalf("unexpected error computing policy deadline: %v", err)
	}
	blDeadline, err := ComputeDeadline(blRule, triggerTick)
	if err != nil {
		t.Fatalf("unexpected error computing B/L deadline: %v", err)
	}

	wantPolicyDeadline := uint64(triggerTick + ninetyDaysInTicks)
	wantBLDeadline := uint64(triggerTick + oneYearInTicks)

	if policyDeadline.DeadlineTick != wantPolicyDeadline {
		t.Fatalf("policy (90-day) deadline: got %d, want %d — duration was not honored", policyDeadline.DeadlineTick, wantPolicyDeadline)
	}
	if blDeadline.DeadlineTick != wantBLDeadline {
		t.Fatalf("B/L (365-day) deadline: got %d, want %d — duration was not honored", blDeadline.DeadlineTick, wantBLDeadline)
	}
	if policyDeadline.DeadlineTick == blDeadline.DeadlineTick {
		t.Fatalf("two rules with genuinely different durations (90 vs 365) produced the SAME deadline tick (%d) for the same trigger — this is exactly the hard-coded-duration bug the blueprint prohibits", policyDeadline.DeadlineTick)
	}
	// The 365-day rule must land further out than the 90-day rule —
	// not just "different", but different in the right direction.
	if blDeadline.DeadlineTick <= policyDeadline.DeadlineTick {
		t.Fatalf("expected the 365-day B/L deadline (%d) to fall after the 90-day policy deadline (%d)", blDeadline.DeadlineTick, policyDeadline.DeadlineTick)
	}
	gap := blDeadline.DeadlineTick - policyDeadline.DeadlineTick
	if gap != oneYearInTicks-ninetyDaysInTicks {
		t.Fatalf("expected the gap between the two deadlines to equal the difference in stated durations (%d), got %d", oneYearInTicks-ninetyDaysInTicks, gap)
	}
}

// TestComputeDeadline_BusinessDaysSkipsWeekends proves
// CalendarRuleBusinessDays is a real, independently-implemented rule —
// not CalendarDays under a different name. Tick 0 is Monday under this
// package's documented weekday convention (weekdayOf), so ticks 5 and
// 6 are the first Saturday/Sunday.
func TestComputeDeadline_BusinessDaysSkipsWeekends(t *testing.T) {
	r, err := New("DL-BD", SourceTypeCarrierTerms, "", "CARRIER-TERMS-1", "V1", "NOTICE", 5, CalendarRuleBusinessDays, "UTC")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Starting Monday (tick 0), +5 business days must skip the
	// Saturday/Sunday in between: Tue,Wed,Thu,Fri,(skip Sat,Sun),Mon =
	// tick 7, NOT tick 5 (which CalendarDays would give).
	d, err := ComputeDeadline(r, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.DeadlineTick != 7 {
		t.Fatalf("expected business-day deadline tick 7 (skipping the weekend), got %d", d.DeadlineTick)
	}
	if isWeekendTick(d.DeadlineTick) {
		t.Fatalf("business-day deadline must never land on a weekend tick, got tick %d", d.DeadlineTick)
	}

	// The same Duration under CalendarRuleCalendarDays must land
	// EARLIER (or at least differently) than under BusinessDays,
	// proving the two rules are genuinely distinct implementations.
	flat, err := New("DL-CD-CMP", SourceTypeCarrierTerms, "", "CARRIER-TERMS-1", "V1", "NOTICE", 5, CalendarRuleCalendarDays, "UTC")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	flatDeadline, err := ComputeDeadline(flat, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if flatDeadline.DeadlineTick != 5 {
		t.Fatalf("expected flat calendar-days deadline tick 5, got %d", flatDeadline.DeadlineTick)
	}
	if flatDeadline.DeadlineTick == d.DeadlineTick {
		t.Fatalf("CalendarDays and BusinessDays produced the identical deadline (%d) for the same duration crossing a weekend — BusinessDays is not actually implemented differently", flatDeadline.DeadlineTick)
	}
}

// TestComputeDeadline_BusinessDaysStartingOnWeekend proves the
// business-day walk correctly skips a weekend that starts immediately
// after the trigger tick, not just one later in the run.
func TestComputeDeadline_BusinessDaysStartingOnWeekend(t *testing.T) {
	r, err := New("DL-BD-2", SourceTypeCarrierTerms, "", "CARRIER-TERMS-1", "V1", "NOTICE", 1, CalendarRuleBusinessDays, "UTC")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Tick 4 is Friday (weekdayOf(4)==4). +1 business day must skip
	// Saturday(5) and Sunday(6), landing on Monday, tick 7.
	d, err := ComputeDeadline(r, 4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.DeadlineTick != 7 {
		t.Fatalf("expected tick 7 (next Monday), got %d", d.DeadlineTick)
	}
}

// TestComputeDeadline_UnrecognizedCalendarRuleErrors proves the
// blueprint §36 golden rule: ComputeDeadline must return an explicit
// error for an unrecognized calendar_rule value rather than falling
// back to a default behavior (e.g. silently treating it as
// CalendarDays).
func TestComputeDeadline_UnrecognizedCalendarRuleErrors(t *testing.T) {
	// Bypass New/Validate deliberately: build the Rule as a bare
	// struct literal so an otherwise-valid Rule carries a CalendarRule
	// value this package has never registered.
	r := Rule{
		RuleID: "DL-BOGUS", SourceType: SourceTypeContract, SourceDocument: "DOC-1",
		EffectiveVersion: "V1", TriggerEvent: "NOTICE", Duration: 30,
		CalendarRule: CalendarRule("LUNAR_MONTHS"), Timezone: "UTC",
	}
	d, err := ComputeDeadline(r, 1000)
	if err == nil {
		t.Fatalf("expected an error for unrecognized calendar_rule, got a computed deadline: %+v", d)
	}
	if !errors.Is(err, ErrUnknownCalendarRule) {
		t.Fatalf("expected ErrUnknownCalendarRule, got %v", err)
	}
	if d != (Deadline{}) {
		t.Fatalf("expected zero-value Deadline on error, got %+v", d)
	}
}

// TestComputeDeadline_InvalidRulePropagatesValidationError proves
// ComputeDeadline never computes a deadline from an invalid rule (e.g.
// zero Duration) — it must return the validation error, not silently
// treat the rule as "immediately due" (deadline == trigger) or "never
// due".
func TestComputeDeadline_InvalidRulePropagatesValidationError(t *testing.T) {
	r := Rule{
		RuleID: "DL-ZERO", SourceType: SourceTypeContract, SourceDocument: "DOC-1",
		EffectiveVersion: "V1", TriggerEvent: "NOTICE", Duration: 0,
		CalendarRule: CalendarRuleCalendarDays, Timezone: "UTC",
	}
	_, err := ComputeDeadline(r, 1000)
	if !errors.Is(err, ErrZeroDuration) {
		t.Fatalf("expected ErrZeroDuration to propagate from ComputeDeadline, got %v", err)
	}
}

func TestWeekdayOf_Convention(t *testing.T) {
	cases := map[uint64]bool{
		0:  false, // Monday
		1:  false, // Tuesday
		2:  false, // Wednesday
		3:  false, // Thursday
		4:  false, // Friday
		5:  true,  // Saturday
		6:  true,  // Sunday
		7:  false, // Monday again
		13: true,  // Saturday (week 2)
	}
	for tick, wantWeekend := range cases {
		if got := isWeekendTick(tick); got != wantWeekend {
			t.Errorf("isWeekendTick(%d) = %v, want %v", tick, got, wantWeekend)
		}
	}
}

// ---- Urgency ----

func TestComputeUrgency_Upcoming(t *testing.T) {
	// deadline far in the future, nothing completed.
	got := ComputeUrgency(1000, 900, 0)
	if got != UrgencyUpcoming {
		t.Fatalf("expected UPCOMING, got %s", got)
	}
}

func TestComputeUrgency_DueSoon(t *testing.T) {
	// within DueSoonThresholdTicks of the deadline.
	got := ComputeUrgency(1000, 1000-DueSoonThresholdTicks, 0)
	if got != UrgencyDueSoon {
		t.Fatalf("expected DUE_SOON at exactly the threshold, got %s", got)
	}
	got = ComputeUrgency(1000, 999, 0)
	if got != UrgencyDueSoon {
		t.Fatalf("expected DUE_SOON one tick out, got %s", got)
	}
}

func TestComputeUrgency_JustOutsideDueSoonIsUpcoming(t *testing.T) {
	got := ComputeUrgency(1000, 1000-DueSoonThresholdTicks-1, 0)
	if got != UrgencyUpcoming {
		t.Fatalf("expected UPCOMING just outside the due-soon window, got %s", got)
	}
}

func TestComputeUrgency_Overdue(t *testing.T) {
	got := ComputeUrgency(1000, 1001, 0)
	if got != UrgencyOverdue {
		t.Fatalf("expected OVERDUE, got %s", got)
	}
}

func TestComputeUrgency_MetOnTime(t *testing.T) {
	got := ComputeUrgency(1000, 2000, 999)
	if got != UrgencyMet {
		t.Fatalf("expected MET when completed before the deadline, got %s", got)
	}
	got = ComputeUrgency(1000, 2000, 1000)
	if got != UrgencyMet {
		t.Fatalf("expected MET when completed exactly at the deadline, got %s", got)
	}
}

func TestComputeUrgency_CompletedLateIsStillOverdue(t *testing.T) {
	got := ComputeUrgency(1000, 2000, 1500)
	if got != UrgencyOverdue {
		t.Fatalf("expected OVERDUE when completed after its own deadline, got %s", got)
	}
}

// TestComputeUrgency_NotCallerSet proves urgency is a genuine
// computation, not a field the caller can just set: sweeping nowTick
// across the whole range around a fixed deadline must produce all
// three unmet states, and each must match the documented comparison.
func TestComputeUrgency_NotCallerSet(t *testing.T) {
	const deadline = 100
	seen := map[Urgency]bool{}
	for now := uint64(0); now <= 110; now++ {
		u := ComputeUrgency(deadline, now, 0)
		seen[u] = true
		switch {
		case now > deadline:
			if u != UrgencyOverdue {
				t.Fatalf("now=%d > deadline=%d: expected OVERDUE, got %s", now, deadline, u)
			}
		case deadline-now <= DueSoonThresholdTicks:
			if u != UrgencyDueSoon {
				t.Fatalf("now=%d within threshold of deadline=%d: expected DUE_SOON, got %s", now, deadline, u)
			}
		default:
			if u != UrgencyUpcoming {
				t.Fatalf("now=%d: expected UPCOMING, got %s", now, u)
			}
		}
	}
	if !seen[UrgencyUpcoming] || !seen[UrgencyDueSoon] || !seen[UrgencyOverdue] {
		t.Fatalf("expected all three unmet urgency states to occur across the sweep, saw: %+v", seen)
	}
}
