package temporal

import (
	"errors"
	"fmt"
	"strings"
)

// Validity: the second dimension.
//
// # Why standing alone is not enough
//
// The six temporal states answer "which state of the world does this
// reference come from". They do not answer "does it still hold". For an
// evidence platform those come apart constantly, and the review named
// exactly where:
//
//	insurance policy, maritime contracts, sanctions, ownership,
//	vessel certificates, inspection reports, bills of lading,
//	letters of credit, regulatory authority.
//
// A class certificate issued last year is CURRENT as a record and
// EXPIRED as an authority. A survey report from 2019 is HISTORICAL and
// was VALID AT THE TIME, which is exactly what makes it usable as
// evidence of the condition then. A superseded policy wording is
// INVALID FOR CURRENT questions and remains valid for a claim arising
// while it was in force.
//
// Collapsing these into one axis forces a choice between two wrong
// answers: treat the old survey as current, or discard it. Both destroy
// the case.

// Validity is whether a reference's content still holds.
type Validity string

const (
	// ValidityUnassessed is the zero value: nobody has asked whether
	// this still holds. As everywhere in VERIQO, the honest default is
	// the absence of a determination.
	ValidityUnassessed Validity = ""

	// Valid: it holds now.
	Valid Validity = "VALID"

	// Expired: it held and no longer does, because time passed. The
	// content is unchanged; its authority lapsed.
	Expired Validity = "EXPIRED"

	// ValidAtTime: it held over a stated interval and makes no claim
	// about now. This is the state most historical evidence should
	// carry, and the one most often mislabelled EXPIRED -- which reads
	// as "no longer useful" when the truth is "useful for precisely
	// the period it covers".
	ValidAtTime Validity = "VALID_AT_TIME"

	// InvalidForCurrent: it has been replaced for present purposes but
	// governs the period before its replacement. A superseded policy
	// wording is the canonical case.
	InvalidForCurrent Validity = "INVALID_FOR_CURRENT"

	// Revoked: the issuing authority withdrew it. Unlike Expired this
	// is an act, and it can reach backwards -- a revoked certificate
	// may never have been valid.
	Revoked Validity = "REVOKED"
)

// Validities returns the six, in the order above.
func Validities() []Validity {
	return []Validity{Valid, Expired, ValidAtTime, InvalidForCurrent, Revoked, ValidityUnassessed}
}

func (v Validity) String() string {
	if v == ValidityUnassessed {
		return "UNASSESSED"
	}
	return string(v)
}

// Known reports whether the validity is one of the six.
func (v Validity) Known() bool {
	switch v {
	case ValidityUnassessed, Valid, Expired, ValidAtTime, InvalidForCurrent, Revoked:
		return true
	}
	return false
}

// SupportsACurrentConclusion reports whether a reference in this state
// may be relied on for a question about now.
//
// Only Valid may. ValidAtTime is deliberately excluded and it is the
// interesting exclusion: a survey that was valid in 2019 supports a
// conclusion about 2019, and using it for a conclusion about today is
// the most common way old evidence is misused.
func (v Validity) SupportsACurrentConclusion() bool { return v == Valid }

// SupportsAHistoricalConclusion reports whether a reference may be
// relied on for a question about the period it covers.
//
// Revoked is excluded: a withdrawn certificate may never have been
// good, so it supports nothing without the withdrawal's own reasoning.
func (v Validity) SupportsAHistoricalConclusion() bool {
	switch v {
	case Valid, ValidAtTime, Expired, InvalidForCurrent:
		return true
	}
	return false
}

// RequiresInterval reports whether the state obliges a stated period.
// A "valid at time" with no time is not a determination.
func (v Validity) RequiresInterval() bool {
	return v == ValidAtTime || v == InvalidForCurrent || v == Expired
}

// RequiresAuthority reports whether the state obliges a named actor.
// Revocation is an act and an act has an actor.
func (v Validity) RequiresAuthority() bool { return v == Revoked }

// Meaning states the semantics, for reports.
func (v Validity) Meaning() string {
	switch v {
	case Valid:
		return "it holds now"
	case Expired:
		return "it held and no longer does; the content is unchanged and its authority lapsed"
	case ValidAtTime:
		return "it held over a stated interval and makes no claim about now"
	case InvalidForCurrent:
		return "replaced for present purposes; it still governs the period before its replacement"
	case Revoked:
		return "the issuing authority withdrew it; it may never have held"
	default:
		return "nobody has asked whether this still holds"
	}
}

var (
	ErrUnknownValidity  = errors.New("temporal: not one of the six validity states")
	ErrNoInterval       = errors.New("temporal: this validity state requires the interval it covers")
	ErrNoAuthority      = errors.New("temporal: REVOKED requires the authority that withdrew it")
	ErrValidityMismatch = errors.New("temporal: the standing and the validity cannot both hold")
)

// Standing is a reference's full position: where in time it comes from,
// and whether it still holds.
//
// The pairs the review named map onto this directly:
//
//	CURRENT + VALID                 usable now
//	CURRENT + EXPIRED               on file, no longer authoritative
//	HISTORICAL + VALID_AT_TIME      evidence of the period it covers
//	SUPERSEDED + INVALID_FOR_CURRENT  governs the period before replacement
//	EXTERNAL + VALID_AT_TIME        an outside attestation of a past state
type Standing struct {
	Reference
	Validity Validity
	// FromTick and ToTick bound the interval the validity covers.
	// Required for the states that carry one.
	FromTick, ToTick uint64
	// RevokedBy names the authority that withdrew it.
	RevokedBy string
}

// Validate refuses a standing whose two dimensions disagree.
func (s Standing) Validate() error {
	if err := s.Reference.Validate(); err != nil {
		return err
	}
	if !s.Validity.Known() {
		return fmt.Errorf("%w: %q", ErrUnknownValidity, string(s.Validity))
	}
	if s.Validity.RequiresInterval() && s.ToTick == 0 && s.FromTick == 0 {
		return fmt.Errorf("%w: %s is %s", ErrNoInterval, s.Subject, s.Validity)
	}
	if s.FromTick > s.ToTick && s.ToTick != 0 {
		return fmt.Errorf("temporal: %s covers [%d,%d], which runs backwards",
			s.Subject, s.FromTick, s.ToTick)
	}
	if s.Validity.RequiresAuthority() && strings.TrimSpace(s.RevokedBy) == "" {
		return fmt.Errorf("%w: %s", ErrNoAuthority, s.Subject)
	}
	if !s.Validity.RequiresAuthority() && strings.TrimSpace(s.RevokedBy) != "" {
		return fmt.Errorf("temporal: %s is %s but names a revoking authority %q: only REVOKED may",
			s.Subject, s.Validity, s.RevokedBy)
	}
	// The combination that cannot mean anything: a reference explicitly
	// quoting a superseded state while claiming it holds now.
	if s.State == Superseded && s.Validity == Valid {
		return fmt.Errorf("%w: %s is SUPERSEDED and VALID, which asserts that a replaced "+
			"reference still holds; the state it governs is INVALID_FOR_CURRENT",
			ErrValidityMismatch, s.Subject)
	}
	if s.State == Historical && s.Validity == Valid {
		return fmt.Errorf("%w: %s is HISTORICAL and VALID; a reference quoted as history "+
			"that also holds now is CURRENT, or its validity is VALID_AT_TIME",
			ErrValidityMismatch, s.Subject)
	}
	return nil
}

// UsableNow reports whether this reference may found a conclusion about
// the present. Both dimensions must agree.
func (s Standing) UsableNow() bool {
	return s.Validate() == nil &&
		s.State.PresentableAsCurrent() &&
		s.Validity.SupportsACurrentConclusion()
}

// UsableForItsPeriod reports whether it may found a conclusion about
// the interval it covers -- which is what most evidence in a dispute is
// actually for.
func (s Standing) UsableForItsPeriod() bool {
	return s.Validate() == nil && s.Validity.SupportsAHistoricalConclusion()
}

// Describe renders both dimensions.
func (s Standing) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s [%s + %s]", s.Subject, s.State, s.Validity)
	if s.Validity.RequiresInterval() {
		fmt.Fprintf(&b, " over ticks [%d,%d]", s.FromTick, s.ToTick)
	}
	if s.Validity == Revoked {
		fmt.Fprintf(&b, " revoked by %s", s.RevokedBy)
	}
	switch {
	case s.UsableNow():
		b.WriteString(" -- usable now")
	case s.UsableForItsPeriod():
		b.WriteString(" -- usable for its period only")
	default:
		b.WriteString(" -- supports no conclusion without more")
	}
	return b.String()
}
