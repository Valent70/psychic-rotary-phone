// Package deadline is VICE's deadline engine — blueprint §24. Its one
// non-negotiable behavioral constraint is stated by the blueprint as an
// explicit prohibition:
//
//	"Jangan hard-code: 'all maritime claims = 1 year'."
//
// A Rule therefore carries EXACTLY the fields the blueprint names
// (§24, verbatim): source_clause, source_document, effective_version,
// trigger_event, duration, calendar_rule, timezone. There is no
// package-level constant anywhere in this file that supplies a
// duration, a trigger event, or a calendar rule on a Rule's behalf —
// every Rule must state its own, sourced from one of the six places
// the blueprint names as legitimate origins for a deadline: policy,
// B/L, contract, carrier terms, governing law configuration, or an
// arbitration clause (§24; modelled below as SourceType).
//
// Ticks, not timestamps: like pkg/evidence/ontology.Evidence, this
// package has no wall clock. Every temporal input and output is an
// explicit VERIQO tick (uint64); nothing in this package calls
// time.Now() or otherwise reads the real clock. A deployment maps
// ticks to real time in exactly one place — its ingest adapter — never
// inside this engine.
package deadline

import (
	"errors"
	"fmt"
)

// SourceType is where a deadline Rule's duration was actually sourced
// from — the blueprint's own closed list of legitimate origins (§24):
// "policy, B/L, contract, carrier terms, governing law configuration,
// arbitration clause". A Rule's SourceType is never left to guesswork;
// Validate refuses an unrecognised value.
type SourceType string

const (
	SourceTypePolicy             SourceType = "POLICY"
	SourceTypeBillOfLading       SourceType = "BILL_OF_LADING"
	SourceTypeContract           SourceType = "CONTRACT"
	SourceTypeCarrierTerms       SourceType = "CARRIER_TERMS"
	SourceTypeGoverningLawConfig SourceType = "GOVERNING_LAW_CONFIGURATION"
	SourceTypeArbitrationClause  SourceType = "ARBITRATION_CLAUSE"
)

var knownSourceTypes = map[SourceType]bool{
	SourceTypePolicy: true, SourceTypeBillOfLading: true, SourceTypeContract: true,
	SourceTypeCarrierTerms: true, SourceTypeGoverningLawConfig: true, SourceTypeArbitrationClause: true,
}

// IsKnownSourceType reports whether t is a modelled deadline source type.
func IsKnownSourceType(t SourceType) bool { return knownSourceTypes[t] }

// CalendarRule is how a Rule's Duration is applied to a trigger tick to
// produce a deadline tick. The set is deliberately small and each
// member is FULLY implemented by ComputeDeadline — this package never
// registers a CalendarRule value it does not actually implement with
// genuinely different behavior from CalendarRuleCalendarDays:
//
//   - CalendarRuleCalendarDays: flat tick addition. deadline = trigger +
//     duration. No assumption about what a tick represents is required.
//   - CalendarRuleBusinessDays: skips weekend ticks. Requires a
//     tick-to-weekday mapping, which this package defines explicitly
//     (see weekdayOf in compute.go) rather than pretending to a real
//     IANA/Gregorian calendar library it does not have (zero external
//     dependencies).
//
// Only these two are registered. A genuinely distinct third rule (e.g.
// a real public-holiday calendar) was out of scope without either a
// hand-maintained holiday table per jurisdiction or a real calendar
// library, so the enum is kept honestly limited to these two rather
// than padded with an unimplemented option.
type CalendarRule string

const (
	CalendarRuleCalendarDays CalendarRule = "CALENDAR_DAYS"
	CalendarRuleBusinessDays CalendarRule = "BUSINESS_DAYS"
)

var knownCalendarRules = map[CalendarRule]bool{
	CalendarRuleCalendarDays: true, CalendarRuleBusinessDays: true,
}

// IsKnownCalendarRule reports whether r is a modelled, fully-implemented
// calendar rule.
func IsKnownCalendarRule(r CalendarRule) bool { return knownCalendarRules[r] }

// Rule is one deadline rule, traced back to a real source document —
// blueprint §24's own required field set, verbatim.
type Rule struct {
	// RuleID identifies this rule (e.g. for claim.TypeDefinition's
	// DeadlineRules []string, or a case's deadline registry key).
	RuleID string `json:"rule_id"`

	// SourceType names WHICH of the blueprint's six legitimate origins
	// (§24) this rule comes from.
	SourceType SourceType `json:"source_type"`

	// SourceClause is the clause/article identifier within
	// SourceDocument this duration was extracted from. Required when
	// SourceType is SourceTypePolicy — in that case it MUST equal a
	// real policy.Clause's ClauseID (see TracesToClause in trace.go);
	// this package never accepts a policy-sourced deadline that cannot
	// be matched back to an actual extracted clause. Optional (but
	// still encouraged) for non-policy sources, where SourceDocument
	// alone may be the only identifier the source instrument offers.
	SourceClause string `json:"source_clause,omitempty"`

	// SourceDocument identifies the document this rule was extracted
	// from: a policy.Clause's DocumentID for SourceTypePolicy, or a
	// contract/B-L/carrier-terms/governing-law-configuration/
	// arbitration-clause document identifier otherwise. Always
	// required — a Rule with no traceable document is exactly the
	// "hard-coded 1 year" shape the blueprint prohibits.
	SourceDocument string `json:"source_document"`

	// EffectiveVersion is which version of SourceDocument was in force
	// when this rule was extracted (mirrors policy.Version's
	// VersionID / policy.Clause's Version field for policy sources;
	// a contract/B-L revision identifier otherwise).
	EffectiveVersion string `json:"effective_version"`

	// TriggerEvent is the timeline event type (see blueprint §13, e.g.
	// "NOTICE", "DAMAGE_DISCOVERY", "DELIVERY", "INCIDENT") that starts
	// this deadline's clock. Deliberately an open string, not a closed
	// enum here — pkg/insurance/timeline owns the authoritative event
	// type set; this package only requires that SOME real event type
	// was named, never a blank trigger.
	TriggerEvent string `json:"trigger_event"`

	// Duration is how long after TriggerEvent's tick this deadline
	// falls, measured in ticks (see package doc). MUST be non-zero —
	// Validate rejects a zero/unset Duration outright (blueprint §36
	// golden rule: never assume a default duration, never silently
	// treat an unset duration as "immediately due" or "never due").
	Duration uint64 `json:"duration"`

	// CalendarRule selects how Duration is applied — see CalendarRule.
	CalendarRule CalendarRule `json:"calendar_rule"`

	// Timezone is carried through faithfully (e.g. "Asia/Jakarta", or a
	// UTC-offset string like "+07:00") for display and downstream
	// reasoning. This package does NOT perform IANA timezone-database
	// arithmetic — ticks are timezone-agnostic — but it never silently
	// discards this label either; it is required on every Rule and
	// copied onto every computed Deadline.
	Timezone string `json:"timezone"`
}

var (
	ErrEmptyRuleID             = errors.New("deadline: RuleID must be non-empty")
	ErrUnknownSourceType       = errors.New("deadline: unknown SourceType")
	ErrEmptySourceDocument     = errors.New("deadline: SourceDocument must be non-empty — a deadline rule must trace to a real source document, never a bare constant")
	ErrPolicySourceNeedsClause = errors.New("deadline: SourceType POLICY requires a non-empty SourceClause referencing a real policy.Clause ClauseID")
	ErrEmptyEffectiveVersion   = errors.New("deadline: EffectiveVersion must be non-empty")
	ErrEmptyTriggerEvent       = errors.New("deadline: TriggerEvent must be non-empty")
	ErrZeroDuration            = errors.New("deadline: Duration must be non-zero — a Rule with no stated duration must be rejected, never treated as immediately due or never due")
	ErrUnknownCalendarRule     = errors.New("deadline: unknown CalendarRule")
	ErrEmptyTimezone           = errors.New("deadline: Timezone must be non-empty")
)

// Validate checks r's own internal consistency. It is called by both
// New and ComputeDeadline — every path that turns a Rule into a
// computed deadline re-checks these invariants, so a Rule built by hand
// (via a struct literal, e.g. in a test) cannot bypass them.
func (r Rule) Validate() error {
	if r.RuleID == "" {
		return ErrEmptyRuleID
	}
	if !IsKnownSourceType(r.SourceType) {
		return fmt.Errorf("%w: %q", ErrUnknownSourceType, r.SourceType)
	}
	if r.SourceDocument == "" {
		return ErrEmptySourceDocument
	}
	if r.SourceType == SourceTypePolicy && r.SourceClause == "" {
		return ErrPolicySourceNeedsClause
	}
	if r.EffectiveVersion == "" {
		return ErrEmptyEffectiveVersion
	}
	if r.TriggerEvent == "" {
		return ErrEmptyTriggerEvent
	}
	if r.Duration == 0 {
		return ErrZeroDuration
	}
	if !IsKnownCalendarRule(r.CalendarRule) {
		return fmt.Errorf("%w: %q", ErrUnknownCalendarRule, r.CalendarRule)
	}
	if r.Timezone == "" {
		return ErrEmptyTimezone
	}
	return nil
}

// New constructs a validated Rule. It returns an error rather than
// panicking on any invalid input — matching every other constructor in
// pkg/insurance.
func New(ruleID string, sourceType SourceType, sourceClause, sourceDocument, effectiveVersion, triggerEvent string, duration uint64, calendarRule CalendarRule, timezone string) (Rule, error) {
	r := Rule{
		RuleID:           ruleID,
		SourceType:       sourceType,
		SourceClause:     sourceClause,
		SourceDocument:   sourceDocument,
		EffectiveVersion: effectiveVersion,
		TriggerEvent:     triggerEvent,
		Duration:         duration,
		CalendarRule:     calendarRule,
		Timezone:         timezone,
	}
	if err := r.Validate(); err != nil {
		return Rule{}, err
	}
	return r, nil
}
