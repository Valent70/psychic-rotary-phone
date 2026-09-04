package temporal

import (
	"errors"
	"strings"
	"testing"
)

// TestTheFivePairsTheReviewNamed. Each is a real combination an
// evidence platform meets, and each must validate.
func TestTheFivePairsTheReviewNamed(t *testing.T) {
	for _, tc := range []struct {
		name      string
		s         Standing
		usableNow bool
	}{
		{"CURRENT + VALID -- a policy in force",
			Standing{Reference: Reference{Subject: "policy-2026", State: Current}, Validity: Valid}, true},
		{"CURRENT + EXPIRED -- a class certificate on file whose term lapsed",
			Standing{Reference: Reference{Subject: "class-cert", State: Current},
				Validity: Expired, FromTick: 10, ToTick: 400}, false},
		{"HISTORICAL + VALID_AT_TIME -- a 2019 survey",
			Standing{Reference: Reference{Subject: "survey-2019", State: Historical},
				Validity: ValidAtTime, FromTick: 100, ToTick: 200}, false},
		{"SUPERSEDED + INVALID_FOR_CURRENT -- a replaced policy wording",
			Standing{Reference: Reference{Subject: "wording-v1", State: Superseded,
				SupersededBy: "wording-v2"}, Validity: InvalidForCurrent, FromTick: 1, ToTick: 300}, false},
		{"EXTERNAL + VALID_AT_TIME -- an outside attestation of a past state",
			Standing{Reference: Reference{Subject: "tsa-token", State: External, Attestor: "a named TSA"},
				Validity: ValidAtTime, FromTick: 50, ToTick: 51}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.s.Validate(); err != nil {
				t.Fatalf("a real combination must validate: %v", err)
			}
			if tc.s.UsableNow() != tc.usableNow {
				t.Fatalf("UsableNow = %v, want %v", tc.s.UsableNow(), tc.usableNow)
			}
		})
	}
}

// TestOldEvidenceRemainsUsableForItsPeriod is the property that makes
// this dimension worth having.
//
// Collapsing standing and validity into one axis forces a choice
// between two wrong answers: treat the 2019 survey as current, or
// discard it. Both destroy the case. It is neither current nor useless.
func TestOldEvidenceRemainsUsableForItsPeriod(t *testing.T) {
	survey := Standing{
		Reference: Reference{Subject: "loading-survey-2019", State: Historical},
		Validity:  ValidAtTime, FromTick: 100, ToTick: 200,
	}
	if survey.UsableNow() {
		t.Fatal("a 2019 survey supports a conclusion about today")
	}
	if !survey.UsableForItsPeriod() {
		t.Fatal("a 2019 survey does not support a conclusion about 2019: the evidence has been discarded")
	}
}

// TestValidAtTimeIsNotUsableNow is the misuse this exists to stop.
func TestValidAtTimeIsNotUsableNow(t *testing.T) {
	if ValidAtTime.SupportsACurrentConclusion() {
		t.Fatal("VALID_AT_TIME supports a conclusion about now: old evidence can be presented as current")
	}
	if !ValidAtTime.SupportsAHistoricalConclusion() {
		t.Fatal("VALID_AT_TIME supports nothing at all, which is the opposite error")
	}
}

// TestRevokedSupportsNothing. A withdrawn certificate may never have
// been good, so it cannot even support a conclusion about its own
// period without the withdrawal's reasoning.
func TestRevokedSupportsNothing(t *testing.T) {
	if Revoked.SupportsACurrentConclusion() || Revoked.SupportsAHistoricalConclusion() {
		t.Fatal("a revoked reference supports a conclusion")
	}
	s := Standing{Reference: Reference{Subject: "withdrawn-cert", State: Current}, Validity: Revoked}
	if err := s.Validate(); !errors.Is(err, ErrNoAuthority) {
		t.Fatalf("want ErrNoAuthority, got %v", err)
	}
	s.RevokedBy = "the flag state"
	if err := s.Validate(); err != nil {
		t.Fatalf("a revocation naming its authority must validate: %v", err)
	}
	if s.UsableForItsPeriod() {
		t.Fatal("a revoked certificate supports a conclusion about its own period")
	}
}

// TestTheImpossibleCombinationsAreRefused. SUPERSEDED+VALID asserts
// that a replaced reference still holds; HISTORICAL+VALID says a
// reference quoted as history also holds now. Both are contradictions
// a reader would have to resolve by guessing.
func TestTheImpossibleCombinationsAreRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		s    Standing
	}{
		{"superseded and valid", Standing{
			Reference: Reference{Subject: "x", State: Superseded, SupersededBy: "y"}, Validity: Valid}},
		{"historical and valid", Standing{
			Reference: Reference{Subject: "x", State: Historical}, Validity: Valid}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.s.Validate(); !errors.Is(err, ErrValidityMismatch) {
				t.Fatalf("want ErrValidityMismatch, got %v", err)
			}
		})
	}
}

// TestAnIntervalIsRequiredWhereItIsMeaningful. A "valid at time" with
// no time is not a determination.
func TestAnIntervalIsRequiredWhereItIsMeaningful(t *testing.T) {
	s := Standing{Reference: Reference{Subject: "x", State: Historical}, Validity: ValidAtTime}
	if err := s.Validate(); !errors.Is(err, ErrNoInterval) {
		t.Fatalf("want ErrNoInterval, got %v", err)
	}
	s.FromTick, s.ToTick = 10, 20
	if err := s.Validate(); err != nil {
		t.Fatalf("an interval-bearing standing must validate: %v", err)
	}
	s.FromTick, s.ToTick = 30, 20
	if err := s.Validate(); err == nil {
		t.Fatal("an interval running backwards was accepted")
	}
}

// TestTheZeroValidityIsUnassessed.
func TestTheZeroValidityIsUnassessed(t *testing.T) {
	var s Standing
	if s.Validity != ValidityUnassessed {
		t.Fatalf("the zero validity is %q", s.Validity)
	}
	if s.Validity.String() != "UNASSESSED" {
		t.Fatalf("the zero validity renders as %q", s.Validity.String())
	}
	if s.Validity.SupportsACurrentConclusion() || s.Validity.SupportsAHistoricalConclusion() {
		t.Fatal("an unassessed validity supports a conclusion")
	}
}

// TestEveryValidityHasMeaning.
func TestEveryValidityHasMeaning(t *testing.T) {
	for _, v := range Validities() {
		if strings.TrimSpace(v.Meaning()) == "" {
			t.Errorf("%s states no meaning", v)
		}
	}
}

// TestDescribeSaysWhatTheReferenceIsFor. A reader must not have to
// work out from two enum values whether they may rely on something.
func TestDescribeSaysWhatTheReferenceIsFor(t *testing.T) {
	now := Standing{Reference: Reference{Subject: "policy", State: Current}, Validity: Valid}
	if !strings.Contains(now.Describe(), "usable now") {
		t.Fatalf("Describe does not say a current valid reference is usable: %s", now.Describe())
	}
	old := Standing{Reference: Reference{Subject: "survey", State: Historical},
		Validity: ValidAtTime, FromTick: 1, ToTick: 2}
	if !strings.Contains(old.Describe(), "usable for its period only") {
		t.Fatalf("Describe does not bound an at-time reference: %s", old.Describe())
	}
	var none Standing
	none.Subject, none.State = "unclassified", Current
	if !strings.Contains(none.Describe(), "supports no conclusion") {
		t.Fatalf("Describe does not refuse an unassessed reference: %s", none.Describe())
	}
}
