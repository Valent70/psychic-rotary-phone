package deadline

import (
	"errors"
	"testing"
)

func validRuleArgs() (ruleID string, st SourceType, sourceClause, sourceDocument, effectiveVersion, triggerEvent string, duration uint64, cr CalendarRule, tz string) {
	return "DL-001", SourceTypePolicy, "CL-1", "DOC-1", "V1", "NOTICE", 90, CalendarRuleCalendarDays, "Asia/Jakarta"
}

func TestNew_Valid(t *testing.T) {
	r, err := New(validRuleArgs())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.RuleID != "DL-001" || r.Duration != 90 || r.CalendarRule != CalendarRuleCalendarDays {
		t.Fatalf("unexpected rule: %+v", r)
	}
}

func TestNew_EmptyRuleID(t *testing.T) {
	_, err := New("", SourceTypePolicy, "CL-1", "DOC-1", "V1", "NOTICE", 90, CalendarRuleCalendarDays, "UTC")
	if !errors.Is(err, ErrEmptyRuleID) {
		t.Fatalf("expected ErrEmptyRuleID, got %v", err)
	}
}

func TestNew_UnknownSourceType(t *testing.T) {
	_, err := New("DL-001", SourceType("BOGUS"), "CL-1", "DOC-1", "V1", "NOTICE", 90, CalendarRuleCalendarDays, "UTC")
	if !errors.Is(err, ErrUnknownSourceType) {
		t.Fatalf("expected ErrUnknownSourceType, got %v", err)
	}
}

func TestNew_EmptySourceDocument(t *testing.T) {
	_, err := New("DL-001", SourceTypeContract, "", "", "V1", "NOTICE", 90, CalendarRuleCalendarDays, "UTC")
	if !errors.Is(err, ErrEmptySourceDocument) {
		t.Fatalf("expected ErrEmptySourceDocument, got %v", err)
	}
}

func TestNew_PolicySourceRequiresClause(t *testing.T) {
	_, err := New("DL-001", SourceTypePolicy, "", "DOC-1", "V1", "NOTICE", 90, CalendarRuleCalendarDays, "UTC")
	if !errors.Is(err, ErrPolicySourceNeedsClause) {
		t.Fatalf("expected ErrPolicySourceNeedsClause, got %v", err)
	}
}

func TestNew_NonPolicySourceClauseOptional(t *testing.T) {
	// A B/L-sourced rule need not name a SourceClause — SourceDocument
	// alone identifies it.
	r, err := New("DL-002", SourceTypeBillOfLading, "", "BL-2024-001", "ORIGINAL", "DELIVERY", 365, CalendarRuleCalendarDays, "UTC")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.SourceClause != "" {
		t.Fatalf("expected empty SourceClause, got %q", r.SourceClause)
	}
}

func TestNew_EmptyEffectiveVersion(t *testing.T) {
	_, err := New("DL-001", SourceTypeContract, "", "DOC-1", "", "NOTICE", 90, CalendarRuleCalendarDays, "UTC")
	if !errors.Is(err, ErrEmptyEffectiveVersion) {
		t.Fatalf("expected ErrEmptyEffectiveVersion, got %v", err)
	}
}

func TestNew_EmptyTriggerEvent(t *testing.T) {
	_, err := New("DL-001", SourceTypeContract, "", "DOC-1", "V1", "", 90, CalendarRuleCalendarDays, "UTC")
	if !errors.Is(err, ErrEmptyTriggerEvent) {
		t.Fatalf("expected ErrEmptyTriggerEvent, got %v", err)
	}
}

// TestNew_ZeroDurationRejected proves the blueprint §36 golden rule:
// never assume a default duration when a Rule doesn't specify one — a
// Rule with a zero/unset Duration must be REJECTED by validation with
// a clear error, never silently treated as "immediately due" or "never
// due".
func TestNew_ZeroDurationRejected(t *testing.T) {
	_, err := New("DL-001", SourceTypeContract, "", "DOC-1", "V1", "NOTICE", 0, CalendarRuleCalendarDays, "UTC")
	if !errors.Is(err, ErrZeroDuration) {
		t.Fatalf("expected ErrZeroDuration for zero Duration, got %v", err)
	}

	// Also prove it directly through Validate on a hand-built Rule
	// (the zero-value Rule{} in particular has Duration == 0), so the
	// rejection is not just a New()-specific check.
	var zero Rule
	if err := zero.Validate(); !errors.Is(err, ErrEmptyRuleID) {
		// zero value fails on RuleID first; fill in everything except
		// Duration to isolate the zero-duration check specifically.
		t.Fatalf("expected zero Rule to fail validation on RuleID first, got %v", err)
	}
	r := Rule{
		RuleID: "DL-ZERO", SourceType: SourceTypeContract, SourceDocument: "DOC-1",
		EffectiveVersion: "V1", TriggerEvent: "NOTICE", Duration: 0,
		CalendarRule: CalendarRuleCalendarDays, Timezone: "UTC",
	}
	if err := r.Validate(); !errors.Is(err, ErrZeroDuration) {
		t.Fatalf("expected ErrZeroDuration, got %v", err)
	}
}

func TestNew_UnknownCalendarRule(t *testing.T) {
	_, err := New("DL-001", SourceTypeContract, "", "DOC-1", "V1", "NOTICE", 90, CalendarRule("BOGUS"), "UTC")
	if !errors.Is(err, ErrUnknownCalendarRule) {
		t.Fatalf("expected ErrUnknownCalendarRule, got %v", err)
	}
}

func TestNew_EmptyTimezone(t *testing.T) {
	_, err := New("DL-001", SourceTypeContract, "", "DOC-1", "V1", "NOTICE", 90, CalendarRuleCalendarDays, "")
	if !errors.Is(err, ErrEmptyTimezone) {
		t.Fatalf("expected ErrEmptyTimezone, got %v", err)
	}
}

func TestIsKnownSourceType(t *testing.T) {
	for _, st := range []SourceType{
		SourceTypePolicy, SourceTypeBillOfLading, SourceTypeContract,
		SourceTypeCarrierTerms, SourceTypeGoverningLawConfig, SourceTypeArbitrationClause,
	} {
		if !IsKnownSourceType(st) {
			t.Errorf("expected %q to be known", st)
		}
	}
	if IsKnownSourceType(SourceType("NOT_A_SOURCE")) {
		t.Errorf("expected unknown source type to be reported unknown")
	}
}

func TestIsKnownCalendarRule(t *testing.T) {
	if !IsKnownCalendarRule(CalendarRuleCalendarDays) || !IsKnownCalendarRule(CalendarRuleBusinessDays) {
		t.Fatalf("expected both registered calendar rules to be known")
	}
	if IsKnownCalendarRule(CalendarRule("MONTHLY")) {
		t.Fatalf("expected unregistered calendar rule to be unknown")
	}
}
