package policy

import (
	"errors"
	"testing"
)

func baseVersion(t *testing.T) Version {
	t.Helper()
	return Version{
		PolicyID: "POL-1", VersionID: "POL-1-V1", PolicyNumber: "PN-1",
		Insurer: "Primary Insurer", Insured: "Insured Co",
		EffectiveFrom: 1, EffectiveTo: 5000, Kind: KindOriginal,
	}
}

func TestVersionValidatesWithNoParticipants(t *testing.T) {
	v := baseVersion(t)
	if err := v.Validate(); err != nil {
		t.Fatalf("a version with no participants (single insurer bears the whole risk) must validate: %v", err)
	}
	if got := v.RetainedBasisPoints(); got != ParticipationScale {
		t.Fatalf("with no reinsurers, retention must be the whole risk: got %d", got)
	}
}

func TestCoInsurerParticipant(t *testing.T) {
	v := baseVersion(t)
	v.Participants = []Participant{
		{PartyID: "PTY-COINS-1", Role: ParticipantCoInsurer, BasisPoints: 40_000},
	}
	if err := v.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	co := v.CoInsurers()
	if len(co) != 1 || co[0].BasisPoints != 40_000 {
		t.Fatalf("CoInsurers: unexpected %+v", co)
	}
}

func TestCoInsurerMustNotDeclareBasis(t *testing.T) {
	v := baseVersion(t)
	v.Participants = []Participant{
		{PartyID: "PTY-1", Role: ParticipantCoInsurer, BasisPoints: 10_000, Basis: BasisTreaty},
	}
	if err := v.Validate(); !errors.Is(err, ErrCoInsurerMustNotSetBasis) {
		t.Fatalf("expected ErrCoInsurerMustNotSetBasis, got %v", err)
	}
}

func TestReinsurerRequiresBasis(t *testing.T) {
	v := baseVersion(t)
	v.Participants = []Participant{
		{PartyID: "PTY-RE-1", Role: ParticipantReinsurer, BasisPoints: 30_000},
	}
	if err := v.Validate(); !errors.Is(err, ErrReinsurerNeedsBasis) {
		t.Fatalf("expected ErrReinsurerNeedsBasis, got %v", err)
	}
}

func TestReinsurerWithBasisComputesRetention(t *testing.T) {
	v := baseVersion(t)
	v.Participants = []Participant{
		{PartyID: "PTY-RE-1", Role: ParticipantReinsurer, BasisPoints: 30_000, Basis: BasisTreaty},
		{PartyID: "PTY-RE-2", Role: ParticipantReinsurer, BasisPoints: 20_000, Basis: BasisFacultative},
	}
	if err := v.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	want := int64(ParticipationScale - 50_000)
	if got := v.RetainedBasisPoints(); got != want {
		t.Fatalf("expected retained %d, got %d", want, got)
	}
	if len(v.Reinsurers()) != 2 {
		t.Fatalf("expected 2 reinsurers, got %d", len(v.Reinsurers()))
	}
}

func TestCededExceedingWholeIsRejected(t *testing.T) {
	v := baseVersion(t)
	v.Participants = []Participant{
		{PartyID: "PTY-RE-1", Role: ParticipantReinsurer, BasisPoints: 60_000, Basis: BasisTreaty},
		{PartyID: "PTY-RE-2", Role: ParticipantReinsurer, BasisPoints: 60_000, Basis: BasisTreaty},
	}
	if err := v.Validate(); !errors.Is(err, ErrCededExceedsWhole) {
		t.Fatalf("expected ErrCededExceedsWhole, got %v", err)
	}
}

func TestCoInsuredExceedingWholeIsRejected(t *testing.T) {
	v := baseVersion(t)
	v.Participants = []Participant{
		{PartyID: "PTY-1", Role: ParticipantCoInsurer, BasisPoints: 70_000},
		{PartyID: "PTY-2", Role: ParticipantCoInsurer, BasisPoints: 70_000},
	}
	if err := v.Validate(); !errors.Is(err, ErrCoInsuredExceedsWhole) {
		t.Fatalf("expected ErrCoInsuredExceedsWhole, got %v", err)
	}
}

func TestParticipantValidateRejectsMalformed(t *testing.T) {
	cases := []struct {
		name string
		p    Participant
		want error
	}{
		{"empty party", Participant{Role: ParticipantCoInsurer, BasisPoints: 1}, ErrEmptyParticipantID},
		{"unknown role", Participant{PartyID: "P", Role: "BOGUS", BasisPoints: 1}, ErrUnknownParticipantRole},
		{"zero basis points", Participant{PartyID: "P", Role: ParticipantCoInsurer, BasisPoints: 0}, ErrNonPositiveBasisPoints},
		{"negative basis points", Participant{PartyID: "P", Role: ParticipantCoInsurer, BasisPoints: -1}, ErrNonPositiveBasisPoints},
		{"exceeds whole", Participant{PartyID: "P", Role: ParticipantCoInsurer, BasisPoints: ParticipationScale + 1}, ErrBasisPointsExceedWhole},
		{"unknown basis", Participant{PartyID: "P", Role: ParticipantReinsurer, BasisPoints: 1, Basis: "BOGUS"}, ErrUnknownReinsuranceBasis},
	}
	for _, c := range cases {
		if err := c.p.Validate(); !errors.Is(err, c.want) {
			t.Errorf("%s: expected %v, got %v", c.name, c.want, err)
		}
	}
}

// TestRetainedBasisPointsNeverGoesNegative proves the honest floor
// documented on RetainedBasisPoints: even if a caller bypasses Validate
// and constructs an over-ceded Version directly, RetainedBasisPoints
// never fabricates a negative retention.
func TestRetainedBasisPointsNeverGoesNegative(t *testing.T) {
	v := baseVersion(t)
	v.Participants = []Participant{
		{PartyID: "PTY-RE-1", Role: ParticipantReinsurer, BasisPoints: 90_000, Basis: BasisTreaty},
		{PartyID: "PTY-RE-2", Role: ParticipantReinsurer, BasisPoints: 90_000, Basis: BasisTreaty},
	}
	if got := v.RetainedBasisPoints(); got != 0 {
		t.Fatalf("expected floor of 0, got %d", got)
	}
}
