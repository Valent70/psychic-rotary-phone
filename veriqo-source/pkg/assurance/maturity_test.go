package assurance

import (
	"strings"
	"testing"
)

// TestTheLadderIsContiguousAndOrdered: a model with a gap or an
// unreachable level is not a ladder.
func TestTheLadderIsContiguousAndOrdered(t *testing.T) {
	if err := ValidateMaturityLadder(); err != nil {
		t.Fatalf("ValidateMaturityLadder: %v", err)
	}
	levels := MaturityLevels()
	if len(levels) != 8 {
		t.Fatalf("expected L0-L7, got %d levels", len(levels))
	}
	for i := 1; i < len(levels); i++ {
		prev, has := levels[i].Requires()
		if !has || prev != levels[i-1] {
			t.Fatalf("%s does not require %s", levels[i], levels[i-1])
		}
	}
}

// TestZeroMaturityIsDesigned: a capability nobody classified is
// designed at best.
func TestZeroMaturityIsDesigned(t *testing.T) {
	var m Maturity
	if m != L0Designed || m.String() != "L0_DESIGNED" {
		t.Fatalf("the zero Maturity must be L0_DESIGNED, got %s", m)
	}
	if m.RequiresOutsideParty() {
		t.Fatal("L0 needs nobody")
	}
}

// TestEverythingFromL4RequiresAnOutsideParty is the honest boundary, and
// the reason no amount of further engineering moves the ladder.
func TestEverythingFromL4RequiresAnOutsideParty(t *testing.T) {
	for _, m := range []Maturity{L0Designed, L1Implemented, L2UnitVerified, L3IntegrationVerified} {
		if m.RequiresOutsideParty() {
			t.Fatalf("%s is reachable by VERIQO alone", m)
		}
	}
	for _, m := range []Maturity{L4RealDataValidated, L5IndependentlyAssured,
		L6ExternallyQualified, L7ProductionQualified} {
		if !m.RequiresOutsideParty() {
			t.Fatalf("%s requires somebody who is not VERIQO", m)
		}
	}
	if InternalCeiling() != L3IntegrationVerified {
		t.Fatalf("the internal ceiling must be L3, got %s", InternalCeiling())
	}
}

// TestRealDataIsNotAnEngineeringLevel is the specific judgement worth
// asserting: L4 needs a data agreement, not more code.
func TestRealDataIsNotAnEngineeringLevel(t *testing.T) {
	if !L4RealDataValidated.RequiresOutsideParty() {
		t.Fatal("real data arrives under an agreement with somebody who owns it, not from engineering effort")
	}
	if !strings.Contains(EvidenceFor(L4RealDataValidated), "data agreement") {
		t.Fatalf("L4's evidence must name the agreement, got %q", EvidenceFor(L4RealDataValidated))
	}
}

// TestNothingClaimsAboveTheInternalCeiling records the round's honest
// position so it cannot drift.
//
// The day an assessor or a data agreement arrives, this test fails and
// somebody has to update the claim deliberately.
func TestNothingClaimsAboveTheInternalCeiling(t *testing.T) {
	if HighestClaimed() > InternalCeiling() {
		t.Fatalf("a capability claims %s, above the internal ceiling %s, with no outside party engaged",
			HighestClaimed(), InternalCeiling())
	}
	for _, c := range MaturityClaims() {
		if c.Level.RequiresOutsideParty() {
			t.Fatalf("%q claims %s", c.Capability, c.Level)
		}
	}
}

// TestEveryClaimCitesEvidenceAndNamesItsBlocker: a level whose evidence
// a reader cannot check is a level anybody can claim.
func TestEveryClaimCitesEvidenceAndNamesItsBlocker(t *testing.T) {
	for _, c := range MaturityClaims() {
		if err := c.Validate(); err != nil {
			t.Fatalf("claim %q: %v", c.Capability, err)
		}
		if len(strings.Fields(c.Blocker)) < 4 {
			t.Fatalf("%q states its blocker too thinly: %q", c.Capability, c.Blocker)
		}
	}
}

// TestEveryLevelStatesWhatItTakes keeps the model checkable rather than
// aspirational.
func TestEveryLevelStatesWhatItTakes(t *testing.T) {
	for _, m := range MaturityLevels() {
		e := EvidenceFor(m)
		if len(strings.Fields(e)) < 5 {
			t.Fatalf("level %s states its requirement too thinly: %q", m, e)
		}
	}
}

// TestTheChainTheModelBreaks is the model's purpose, asserted: the
// report must state the internal ceiling rather than leaving a reader
// to infer it.
func TestTheReportStatesTheCeiling(t *testing.T) {
	r := MaturityReport()
	for _, m := range MaturityLevels() {
		if !strings.Contains(r, m.String()) {
			t.Fatalf("level %s missing from the report", m)
		}
	}
	if !strings.Contains(r, "requires an outside party") {
		t.Fatal("the report must mark the levels VERIQO cannot reach")
	}
	if !strings.Contains(r, "Nothing claims L4 or above") {
		t.Fatal("the report must state the position, not just the ladder")
	}
	if strings.Contains(r, "%") {
		t.Fatal("maturity is not a percentage")
	}
}

// TestTheTwoAxesAndTheLadderAgree: a capability cannot be
// INTERNALLY_PROVED on the assurance axis and claim a maturity level
// that requires an outside party.
func TestTheTwoAxesAndTheLadderAgree(t *testing.T) {
	for _, s := range Capabilities() {
		if s.Assurance >= ExternallyValidated {
			t.Fatalf("capability %q claims %s on the assurance axis", s.Capability, s.Assurance)
		}
	}
	if HighestClaimed().RequiresOutsideParty() {
		t.Fatal("the ladder and the assurance axis disagree about whether anybody outside has looked")
	}
}
