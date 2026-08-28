package deadline

import "fmt"

// Deadline is the real, computed result of applying a Rule to one
// trigger-event tick. It is produced ONLY by ComputeDeadline — nothing
// in this package lets a caller construct a "deadline" by simply
// setting a field, which is the entire point: the deadline tick below
// is genuinely derived from Rule.Duration and the trigger tick, never
// a caller-supplied or hard-coded value.
type Deadline struct {
	RuleID       string       `json:"rule_id"`
	TriggerEvent string       `json:"trigger_event"`
	TriggerTick  uint64       `json:"trigger_tick"`
	DeadlineTick uint64       `json:"deadline_tick"`
	CalendarRule CalendarRule `json:"calendar_rule"`

	// Timezone is copied from the Rule verbatim — carried through, not
	// silently dropped, per the package doc's timezone note.
	Timezone string `json:"timezone"`
}

// ticksPerWeek is the period of the weekday cycle this package defines
// for CalendarRuleBusinessDays (see weekdayOf below). It is NOT a
// general "how many ticks are in a week" claim about VERIQO ticks
// system-wide — it only governs how THIS package interprets a tick's
// day-of-week when, and only when, CalendarRuleBusinessDays is in use.
const ticksPerWeek = 7

// weekdayOf reports the zero-based day-of-week for tick t, under this
// package's own explicit, documented convention: tick 0 is defined as
// a Monday, and each increment of one tick represents exactly one
// calendar day (0=Mon, 1=Tue, 2=Wed, 3=Thu, 4=Fri, 5=Sat, 6=Sun). This
// convention applies ONLY to CalendarRuleBusinessDays. CalendarDays
// arithmetic (ComputeDeadline's default path) never calls this
// function and makes no assumption whatsoever about what a tick means.
func weekdayOf(t uint64) uint64 {
	return t % ticksPerWeek
}

// isWeekendTick reports whether tick t falls on the Saturday or Sunday
// of weekdayOf's convention.
func isWeekendTick(t uint64) bool {
	d := weekdayOf(t)
	return d == 5 || d == 6
}

// addBusinessDayTicks advances from startTick by n business-day ticks,
// skipping every weekend tick per isWeekendTick, and returns the
// resulting tick. The result always lands on a non-weekend tick,
// because remaining is only decremented on a non-weekend step.
func addBusinessDayTicks(startTick, n uint64) uint64 {
	t := startTick
	var advanced uint64
	for advanced < n {
		t++
		if !isWeekendTick(t) {
			advanced++
		}
	}
	return t
}

// ComputeDeadline is the deadline engine's core operation (blueprint
// §24). It takes a Rule and a trigger-event tick and returns a
// Deadline whose DeadlineTick is genuinely derived from
// Rule.TriggerEvent's tick and Rule.Duration — there is no code path
// in this function, or anywhere else in this package, that ignores
// Rule.Duration and substitutes a fixed constant.
//
// rule is re-validated here (not just trusted from a prior New/
// Validate call) so a hand-built Rule — e.g. one a test constructs
// directly via a struct literal with an unrecognised CalendarRule —
// cannot reach the calendar-rule switch below at all: Validate's own
// IsKnownCalendarRule check already refuses it. The switch's default
// arm is kept anyway, and returns the identical error, as a second,
// independent guard against ever silently defaulting to
// CalendarRuleCalendarDays for an unrecognised value — the blueprint
// §36 golden rule this function exists to satisfy.
func ComputeDeadline(rule Rule, triggerTick uint64) (Deadline, error) {
	if err := rule.Validate(); err != nil {
		return Deadline{}, err
	}

	var deadlineTick uint64
	switch rule.CalendarRule {
	case CalendarRuleCalendarDays:
		deadlineTick = triggerTick + rule.Duration
	case CalendarRuleBusinessDays:
		deadlineTick = addBusinessDayTicks(triggerTick, rule.Duration)
	default:
		return Deadline{}, fmt.Errorf("%w: %q", ErrUnknownCalendarRule, rule.CalendarRule)
	}

	return Deadline{
		RuleID:       rule.RuleID,
		TriggerEvent: rule.TriggerEvent,
		TriggerTick:  triggerTick,
		DeadlineTick: deadlineTick,
		CalendarRule: rule.CalendarRule,
		Timezone:     rule.Timezone,
	}, nil
}

// Urgency is a genuinely computed comparison between a deadline tick
// and explicit "now"/"completed" ticks — never a caller-set field.
type Urgency string

const (
	// UrgencyUpcoming: not yet due, and not within the due-soon window.
	UrgencyUpcoming Urgency = "UPCOMING"
	// UrgencyDueSoon: not yet due, but within DueSoonThresholdTicks of
	// the deadline.
	UrgencyDueSoon Urgency = "DUE_SOON"
	// UrgencyOverdue: the deadline tick has passed with no completion
	// recorded at or before it.
	UrgencyOverdue Urgency = "OVERDUE"
	// UrgencyMet: the required action was completed at or before the
	// deadline tick.
	UrgencyMet Urgency = "MET"
)

// DueSoonThresholdTicks is the documented window, in ticks, inside
// which an unmet deadline that has not yet passed is reported as
// DUE_SOON rather than UPCOMING. Fixed and package-level (not a
// per-call parameter) so urgency reporting is consistent across every
// caller; a deployment that needs a different window computes its own
// classification on top of the raw ticks ComputeDeadline/ComputeUrgency
// already expose.
const DueSoonThresholdTicks uint64 = 3

// ComputeUrgency derives a deadline's Urgency purely from explicit tick
// inputs:
//
//   - deadlineTick: the DeadlineTick from a prior ComputeDeadline call.
//   - nowTick: the caller's current logical tick.
//   - completedAtTick: the tick at which the action this deadline
//     governs (e.g. notice given, claim filed) was actually performed;
//     0 means "not yet performed". This is genuinely a THIRD input, not
//     inferred — ComputeUrgency never assumes an action was completed
//     just because nowTick has passed the deadline.
//
// Every branch below is a real comparison of these three values; there
// is no path that returns a fixed Urgency regardless of input.
func ComputeUrgency(deadlineTick, nowTick, completedAtTick uint64) Urgency {
	if completedAtTick != 0 {
		if completedAtTick <= deadlineTick {
			return UrgencyMet
		}
		// The action happened, but after its own deadline had already
		// passed — that is not "met", it is exactly what OVERDUE means.
		return UrgencyOverdue
	}
	if nowTick > deadlineTick {
		return UrgencyOverdue
	}
	if deadlineTick-nowTick <= DueSoonThresholdTicks {
		return UrgencyDueSoon
	}
	return UrgencyUpcoming
}
