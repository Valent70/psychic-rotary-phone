package policy

import (
	"errors"
	"fmt"
)

// This file closes the VERIQO Master Closure Mandate §20 ("Reinsurance
// / Co-insurance") item: "Model: primary insurer; co-insurer;
// participation percentage; reinsurer; treaty/facultative relationship
// where authorized; ceded exposure; retention; allocation. Ensure
// primary claim decisions remain linked to original evidence."
//
// Deliberately NOT a second policy/coverage engine (Final Design §39):
// a Participant is a fact about WHO SHARES this policy Version's risk
// and BY HOW MUCH, recorded on the version itself. It changes nothing
// about how pkg/insurance/coverage reasons over a claim — coverage
// analysis is unaffected by whether the capacity behind a policy is one
// insurer or twelve. The "primary claim decisions remain linked to
// original evidence" half of §20 is already true by construction:
// Participants lives on policy.Version, and every coverage.Fact already
// pins PolicyVersionID (see pkg/insurance/verification's §54 gate) — a
// participation split changes who ultimately pays, never what the
// evidence says.

// ParticipantRole distinguishes a co-insurer (shares PRIMARY risk
// alongside the named Insurer on this Version) from a reinsurer (has NO
// direct relationship with the insured; assumes risk CEDED to it by the
// insurer/co-insurers). This distinction matters: a co-insurer's share
// is visible to and enforceable by the insured, a reinsurer's is not.
type ParticipantRole string

const (
	ParticipantCoInsurer ParticipantRole = "CO_INSURER"
	ParticipantReinsurer ParticipantRole = "REINSURER"
)

var knownParticipantRoles = map[ParticipantRole]bool{
	ParticipantCoInsurer: true, ParticipantReinsurer: true,
}

// IsKnownParticipantRole reports whether r is a modelled participant role.
func IsKnownParticipantRole(r ParticipantRole) bool { return knownParticipantRoles[r] }

// ReinsuranceBasis is the treaty/facultative distinction the mandate
// names explicitly. Empty is valid ONLY for a co-insurer (the
// distinction is meaningless for primary risk-sharing); a reinsurer
// MUST declare one — see validateParticipants.
type ReinsuranceBasis string

const (
	// BasisTreaty: ceded automatically under a standing treaty covering
	// a whole book of business, not negotiated per policy.
	BasisTreaty ReinsuranceBasis = "TREATY"
	// BasisFacultative: ceded individually, negotiated for this specific
	// policy/risk.
	BasisFacultative ReinsuranceBasis = "FACULTATIVE"
)

var knownReinsuranceBases = map[ReinsuranceBasis]bool{
	BasisTreaty: true, BasisFacultative: true,
}

// IsKnownReinsuranceBasis reports whether b is a modelled basis.
func IsKnownReinsuranceBasis(b ReinsuranceBasis) bool { return knownReinsuranceBases[b] }

// ParticipationScale is the fixed-point scale applied to every
// Participant's share, matching quantum.RateScale's own reasoning
// (pkg/insurance/quantum/money.go): a Go binary float64 percentage is
// not guaranteed bit-identical across compilers/architectures, and a
// participation split feeds directly into how a claim payment is
// allocated (§20 "allocation"), so it must be exactly reproducible.
// BasisPoints of ParticipationScale means 100.000%.
const ParticipationScale = 100_000

// Participant is one co-insurer's or reinsurer's share of a policy
// Version's risk — the mandate's own worked fields: party identity
// (PartyID), participation percentage (BasisPoints), and — for a
// reinsurer — the treaty/facultative relationship (Basis).
type Participant struct {
	// PartyID is the party.PartyID (kept as a plain string here, not a
	// party.Role import, so this package does not gain a new dependency
	// on pkg/insurance/party for one field — callers cross-reference the
	// case's own party.Registry the same way claim.Claim.ClaimantPartyID
	// already does across packages).
	PartyID string          `json:"party_id"`
	Role    ParticipantRole `json:"role"`
	// BasisPoints is this participant's share, in units of
	// 1/ParticipationScale (i.e. hundred-thousandths of one), a fixed-
	// point integer for the reproducibility reason ParticipationScale's
	// doc comment gives. 50_000 == 50.000%.
	BasisPoints int64 `json:"basis_points"`
	// Basis is the treaty/facultative relationship. Required for
	// ParticipantReinsurer; must be empty for ParticipantCoInsurer (see
	// validateParticipants) — co-insurance has no such concept.
	Basis ReinsuranceBasis `json:"basis,omitempty"`
}

var (
	ErrEmptyParticipantID       = errors.New("policy: Participant.PartyID must be non-empty")
	ErrUnknownParticipantRole   = errors.New("policy: unknown Participant.Role")
	ErrNonPositiveBasisPoints   = errors.New("policy: Participant.BasisPoints must be > 0")
	ErrBasisPointsExceedWhole   = errors.New("policy: Participant.BasisPoints must be <= ParticipationScale (100%)")
	ErrReinsurerNeedsBasis      = errors.New("policy: a REINSURER participant must declare a ReinsuranceBasis")
	ErrCoInsurerMustNotSetBasis = errors.New("policy: a CO_INSURER participant must not declare a ReinsuranceBasis -- that concept applies only to reinsurance")
	ErrUnknownReinsuranceBasis  = errors.New("policy: unknown Participant.Basis")
	ErrCededExceedsWhole        = errors.New("policy: total REINSURER BasisPoints on one version exceeds ParticipationScale (100%) -- more risk ceded than exists")
	ErrCoInsuredExceedsWhole    = errors.New("policy: total CO_INSURER BasisPoints on one version exceeds ParticipationScale (100%) -- more primary risk shared than exists")
)

// Validate checks one Participant's own internal consistency.
func (p Participant) Validate() error {
	if p.PartyID == "" {
		return ErrEmptyParticipantID
	}
	if !IsKnownParticipantRole(p.Role) {
		return fmt.Errorf("%w: %q", ErrUnknownParticipantRole, p.Role)
	}
	if p.BasisPoints <= 0 {
		return ErrNonPositiveBasisPoints
	}
	if p.BasisPoints > ParticipationScale {
		return ErrBasisPointsExceedWhole
	}
	switch p.Role {
	case ParticipantReinsurer:
		if p.Basis == "" {
			return ErrReinsurerNeedsBasis
		}
		if !IsKnownReinsuranceBasis(p.Basis) {
			return fmt.Errorf("%w: %q", ErrUnknownReinsuranceBasis, p.Basis)
		}
	case ParticipantCoInsurer:
		if p.Basis != "" {
			return ErrCoInsurerMustNotSetBasis
		}
	}
	return nil
}

// validateParticipants validates every Participant individually and
// then the AGGREGATE invariant §20 implies but never states in so many
// words: you cannot cede or co-insure more than 100% of one risk.
// Co-insurance and reinsurance are checked as SEPARATE totals — a
// reinsurer's cession is against the CEDING insurer's own retained
// share, not against the co-insurance split, so summing them together
// would conflate two different 100%s.
func validateParticipants(ps []Participant) error {
	var coInsuredTotal, cededTotal int64
	for _, p := range ps {
		if err := p.Validate(); err != nil {
			return err
		}
		switch p.Role {
		case ParticipantCoInsurer:
			coInsuredTotal += p.BasisPoints
		case ParticipantReinsurer:
			cededTotal += p.BasisPoints
		}
	}
	if coInsuredTotal > ParticipationScale {
		return ErrCoInsuredExceedsWhole
	}
	if cededTotal > ParticipationScale {
		return ErrCededExceedsWhole
	}
	return nil
}

// Reinsurers returns every REINSURER participant on v, in declared order.
func (v Version) Reinsurers() []Participant {
	var out []Participant
	for _, p := range v.Participants {
		if p.Role == ParticipantReinsurer {
			out = append(out, p)
		}
	}
	return out
}

// CoInsurers returns every CO_INSURER participant on v, in declared order.
func (v Version) CoInsurers() []Participant {
	var out []Participant
	for _, p := range v.Participants {
		if p.Role == ParticipantCoInsurer {
			out = append(out, p)
		}
	}
	return out
}

// RetainedBasisPoints is the named Insurer's own retention (§20
// "retention"): ParticipationScale minus everything ceded to
// reinsurers. This is the REAL computed logic behind "retention" — it
// is never a separately settable field that could drift from the
// participants actually on record.
func (v Version) RetainedBasisPoints() int64 {
	var ceded int64
	for _, p := range v.Reinsurers() {
		ceded += p.BasisPoints
	}
	retained := ParticipationScale - ceded
	if retained < 0 {
		// validateParticipants (run by Version.Validate) refuses this
		// state from ever being reachable through this package's own
		// constructors; a caller who bypassed Validate and asks anyway
		// gets an honest floor rather than a fabricated negative
		// retention.
		return 0
	}
	return retained
}
