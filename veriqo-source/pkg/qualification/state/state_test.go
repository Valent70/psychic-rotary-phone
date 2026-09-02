package state

import (
	"errors"
	"testing"
)

// TestVocabularyHasNoVerdictState is Article 16 enforced by the type
// system: a vocabulary that cannot express a verdict cannot
// accidentally deliver one.
func TestVocabularyHasNoVerdictState(t *testing.T) {
	for _, s := range States() {
		if s.AssertsLegalConclusion() {
			t.Fatalf("canonical state %q asserts a legal conclusion", s)
		}
	}
}

// TestForbiddenTermsAreRefused walks every forbidden term. PROVEN and
// CORROBORATED are the two that matter most commercially -- they are
// the words a reader most wants to see and the ones VERIQO must never
// produce.
func TestForbiddenTermsAreRefused(t *testing.T) {
	for _, f := range ForbiddenStates() {
		if _, err := Parse(f); !errors.Is(err, ErrForbiddenState) {
			t.Fatalf("term %q must be refused with ErrForbiddenState, got %v", f, err)
		}
	}
}

func TestForbiddenTermsAreRefusedCaseInsensitively(t *testing.T) {
	for _, s := range []string{"proven", "Corroborated", "  LIABLE  "} {
		if _, err := Parse(s); !errors.Is(err, ErrForbiddenState) {
			t.Fatalf("term %q must be refused, got %v", s, err)
		}
	}
}

func TestParseAcceptsEveryCanonicalState(t *testing.T) {
	for _, s := range States() {
		got, err := Parse(string(s))
		if err != nil || got != s {
			t.Fatalf("Parse(%q) = %v, %v", s, got, err)
		}
	}
}

func TestParseDistinguishesUnknownFromForbidden(t *testing.T) {
	if _, err := Parse("MAYBE"); !errors.Is(err, ErrUnknownState) {
		t.Fatalf("an unrecognised term should be ErrUnknownState, got %v", err)
	}
	if _, err := Parse("PROVEN"); !errors.Is(err, ErrForbiddenState) {
		t.Fatalf("a prohibited term should be ErrForbiddenState, got %v", err)
	}
}

// --- Single-source exception ---

func validException() SingleSourceException {
	return SingleSourceException{
		ClaimID: "C-1", SourceID: "S-1",
		WhyNecessary:               "only operator with coverage of that terminal",
		WhyAlternativesUnavailable: "two other providers queried; neither covers the window",
		SourceAssurance:            "ISO-certified, audited annually",
		Coverage:                   "full temporal window, 90% spatial",
		KnownLimitations:           "cannot distinguish berth from anchorage",
		Reviewer:                   "analyst-lead-1", PolicyVersion: "policy-v1",
		ReviewTick: 100,
	}
}

// TestSingleSourceExceptionYieldsOnlyItsOwnState proves there is no
// argument that turns one source into corroboration.
func TestSingleSourceExceptionYieldsOnlyItsOwnState(t *testing.T) {
	got, err := Apply(validException(), 1, 50)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got != SupportedBySingleHighAssuranceSource {
		t.Fatalf("expected SUPPORTED_BY_SINGLE_HIGH_ASSURANCE_SOURCE, got %s", got)
	}
	if got == Supported {
		t.Fatal("a single-source exception must never yield plain SUPPORTED")
	}
}

// TestEveryExceptionFieldIsMandatory proves an exception that does not
// say why it was necessary is an omission, not an exception.
func TestEveryExceptionFieldIsMandatory(t *testing.T) {
	mutators := map[string]func(*SingleSourceException){
		"ClaimID":                    func(e *SingleSourceException) { e.ClaimID = "" },
		"SourceID":                   func(e *SingleSourceException) { e.SourceID = "" },
		"WhyNecessary":               func(e *SingleSourceException) { e.WhyNecessary = "" },
		"WhyAlternativesUnavailable": func(e *SingleSourceException) { e.WhyAlternativesUnavailable = "" },
		"SourceAssurance":            func(e *SingleSourceException) { e.SourceAssurance = "" },
		"Coverage":                   func(e *SingleSourceException) { e.Coverage = "" },
		"KnownLimitations":           func(e *SingleSourceException) { e.KnownLimitations = "" },
		"Reviewer":                   func(e *SingleSourceException) { e.Reviewer = "" },
		"PolicyVersion":              func(e *SingleSourceException) { e.PolicyVersion = "" },
		"ReviewTick":                 func(e *SingleSourceException) { e.ReviewTick = 0 },
	}
	for name, mut := range mutators {
		e := validException()
		mut(&e)
		if err := e.Validate(); !errors.Is(err, ErrExceptionIncomplete) {
			t.Fatalf("clearing %s must make the exception incomplete, got %v", name, err)
		}
	}
}

// TestExceptionRefusedWhenMoreThanOneEffectiveSource guards against
// the Article 3 confusion: passing 2 when the effective count is 1, or
// invoking the exception when corroboration was actually available.
func TestExceptionRefusedWhenMoreThanOneEffectiveSource(t *testing.T) {
	if _, err := Apply(validException(), 2, 50); !errors.Is(err, ErrNotSingleSource) {
		t.Fatalf("expected ErrNotSingleSource for 2 effective sources, got %v", err)
	}
	if _, err := Apply(validException(), 0, 50); !errors.Is(err, ErrNotSingleSource) {
		t.Fatalf("expected ErrNotSingleSource for 0 sources, got %v", err)
	}
}

// TestExceptionExpires proves an exception without a live review
// window cannot quietly become permanent.
func TestExceptionExpires(t *testing.T) {
	if _, err := Apply(validException(), 1, 101); !errors.Is(err, ErrExceptionExpired) {
		t.Fatalf("expected ErrExceptionExpired past the review tick, got %v", err)
	}
}

func TestExceptionValidAtExactlyTheReviewTick(t *testing.T) {
	if _, err := Apply(validException(), 1, 100); err != nil {
		t.Fatalf("the exception should still be valid at its review tick: %v", err)
	}
}

// --- Qualification construction ---

// TestMaterialDissentForcesDissentBearingState is Article 11 at the
// construction boundary: a caller asking for SUPPORTED while carrying
// material dissent is corrected, not silently granted.
func TestMaterialDissentForcesDissentBearingState(t *testing.T) {
	q, err := New("C-1", Supported, "policy-v1", "rationale", []string{"reviewer-2 disputes provenance"}, 10)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if q.State != QualifiedWithDissent {
		t.Fatalf("material dissent must force QUALIFIED_WITH_DISSENT, got %s", q.State)
	}
	if !q.State.CarriesDissent() {
		t.Fatal("the resulting state must advertise dissent")
	}
	if len(q.MaterialDissent) != 1 {
		t.Fatalf("the dissent must be carried into the qualification, got %v", q.MaterialDissent)
	}
}

// TestContradictedIsNotUpgradedByDissent proves the correction is
// targeted: CONTRADICTED already conveys an adverse outcome and is not
// weakened into QUALIFIED_WITH_DISSENT.
func TestContradictedIsNotUpgradedByDissent(t *testing.T) {
	q, err := New("C-1", Contradicted, "policy-v1", "r", []string{"d"}, 10)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if q.State != Contradicted {
		t.Fatalf("CONTRADICTED must be preserved, got %s", q.State)
	}
}

func TestNewWithoutDissentKeepsRequestedState(t *testing.T) {
	q, err := New("C-1", Supported, "policy-v1", "r", nil, 10)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if q.State != Supported {
		t.Fatalf("expected SUPPORTED, got %s", q.State)
	}
}

// TestNewRequiresPolicyVersion enforces Article 7's precondition.
func TestNewRequiresPolicyVersion(t *testing.T) {
	if _, err := New("C-1", Supported, "", "r", nil, 10); err == nil {
		t.Fatal("a qualification with no policy version must be refused")
	}
}

func TestNewRejectsForbiddenState(t *testing.T) {
	if _, err := New("C-1", State("PROVEN"), "policy-v1", "r", nil, 10); !errors.Is(err, ErrForbiddenState) {
		t.Fatalf("expected ErrForbiddenState, got %v", err)
	}
}

func TestNewRequiresClaimID(t *testing.T) {
	if _, err := New("", Supported, "policy-v1", "r", nil, 10); err == nil {
		t.Fatal("a qualification with no claim ID must be refused")
	}
}

func TestOnlyQualifiedWithDissentCarriesDissent(t *testing.T) {
	for _, s := range States() {
		if s.CarriesDissent() && s != QualifiedWithDissent {
			t.Fatalf("state %q should not report carrying dissent", s)
		}
	}
}
