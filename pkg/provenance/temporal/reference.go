package temporal

import (
	"errors"
	"fmt"
	"strings"
)

// Temporal STATE: where a reference sits in time.
//
// # Why state and validity are two axes and not one
//
// State answers "what kind of record is this?". Validity (validity.go)
// answers "does it still hold?". Collapsing them produces the specific
// error the review named: a certificate that expired last month is
// CURRENT as a record and useless as authority. A single enum forces
// one of those two true statements to be discarded.
//
// The six states are the ones a case actually contains:
//
//	CURRENT      the record in force now, as far as anyone recorded
//	HISTORICAL   a record OF a past period, quoted as such
//	SUPERSEDED   replaced by a later record for present purposes
//	DERIVED      produced from other records rather than observed
//	EXTERNAL     attested by a party outside VERIQO
//	UNVERIFIED   the zero value: nobody has classified it
//
// # UNVERIFIED is the zero value on purpose
//
// A reference nobody has classified must not read as CURRENT. That is
// the same discipline as classification.Unset and quality.NotAssessed:
// an absence of work is not a determination, and the type system is
// where that gets enforced rather than remembered.
type State string

const (
	// Unverified is the zero value. It supports nothing.
	Unverified State = ""

	Current    State = "CURRENT"
	Historical State = "HISTORICAL"
	Superseded State = "SUPERSEDED"
	Derived    State = "DERIVED"
	External   State = "EXTERNAL"
)

// States returns the classified states, excluding the zero value.
func States() []State {
	return []State{Current, Historical, Superseded, Derived, External}
}

// Known reports whether the state is one somebody assigned.
func (s State) Known() bool {
	for _, k := range States() {
		if k == s {
			return true
		}
	}
	return false
}

func (s State) String() string {
	if s == Unverified {
		return "UNVERIFIED"
	}
	return string(s)
}

// PresentableAsCurrent reports whether a reference in this state may,
// on the state axis alone, found a conclusion about the present.
//
// EXTERNAL is included: an outside attestation of the present state is
// exactly the kind of reference that should be able to. DERIVED is
// included too, and that is a deliberate decision rather than an
// oversight -- a derived record can describe the present, and whether
// its derivation is sound is a question for the evidence quality
// vector, not for its temporal state.
//
// Note what this method does NOT do: it does not decide usability.
// Standing.UsableNow requires this AND the validity axis to agree,
// which is the whole reason there are two axes.
func (s State) PresentableAsCurrent() bool {
	switch s {
	case Current, External, Derived:
		return true
	}
	return false
}

// DescribesAPastPeriod reports whether the state is one whose subject
// matter is a period rather than the present.
func (s State) DescribesAPastPeriod() bool {
	switch s {
	case Historical, Superseded:
		return true
	}
	return false
}

var (
	ErrUnknownState     = errors.New("temporal: not one of the five classified states")
	ErrNoSubject        = errors.New("temporal: a reference must name what it refers to")
	ErrNoSuccessor      = errors.New("temporal: SUPERSEDED requires the record that replaced it")
	ErrNoAttestor       = errors.New("temporal: EXTERNAL requires the party that attested it")
	ErrSelfSupersession = errors.New("temporal: a reference cannot supersede itself")
	ErrNoDerivation     = errors.New("temporal: DERIVED requires what it was derived from")
)

// Reference is a pointer to a record, with its temporal state.
type Reference struct {
	// Subject names the record. It is required: a reference to
	// nothing is a state assertion with no subject.
	Subject string `json:"subject"`

	State State `json:"state"`

	// SupersededBy names the replacement. Required for SUPERSEDED,
	// because "this was replaced" without naming the replacement
	// leaves a reader unable to find what now governs.
	SupersededBy string `json:"superseded_by,omitempty"`

	// Attestor names the outside party. Required for EXTERNAL: an
	// external attestation with no attestor is VERIQO's own word
	// wearing somebody else's label, which is FC-006 in miniature.
	Attestor string `json:"attestor,omitempty"`

	// DerivedFrom names the inputs. Required for DERIVED, so a derived
	// record's lineage is followable rather than asserted.
	DerivedFrom []string `json:"derived_from,omitempty"`
}

// Validate refuses references whose state promises information the
// reference does not carry.
func (r Reference) Validate() error {
	if strings.TrimSpace(r.Subject) == "" {
		return ErrNoSubject
	}
	if !r.State.Known() {
		return fmt.Errorf("%w: %q (a reference nobody classified is UNVERIFIED, "+
			"which supports nothing)", ErrUnknownState, r.State.String())
	}
	switch r.State {
	case Superseded:
		if strings.TrimSpace(r.SupersededBy) == "" {
			return fmt.Errorf("%w: %s", ErrNoSuccessor, r.Subject)
		}
		if r.SupersededBy == r.Subject {
			return fmt.Errorf("%w: %s", ErrSelfSupersession, r.Subject)
		}
	case External:
		if strings.TrimSpace(r.Attestor) == "" {
			return fmt.Errorf("%w: %s", ErrNoAttestor, r.Subject)
		}
	case Derived:
		if len(r.DerivedFrom) == 0 {
			return fmt.Errorf("%w: %s", ErrNoDerivation, r.Subject)
		}
	}
	// The inverse: a field that only one state may carry must not be
	// set by another, or a reader cannot tell an assertion from a
	// leftover.
	if r.State != Superseded && strings.TrimSpace(r.SupersededBy) != "" {
		return fmt.Errorf("temporal: %s is %s and names a successor %q: only SUPERSEDED may",
			r.Subject, r.State, r.SupersededBy)
	}
	if r.State != External && strings.TrimSpace(r.Attestor) != "" {
		return fmt.Errorf("temporal: %s is %s and names an attestor %q: only EXTERNAL may",
			r.Subject, r.State, r.Attestor)
	}
	return nil
}

// ErrPromotion is returned when a reference is moved to a state that
// claims MORE than it held, with no reason recorded.
var ErrPromotion = errors.New("temporal: a promotion requires a stated reason")

// PresentableAsCurrent reports whether this reference may, on the
// state axis alone, found a conclusion about the present.
//
// It delegates to the state, and it exists on Reference as well
// because that is where callers hold the value -- and a caller
// reaching for r.State.PresentableAsCurrent() on a reference that
// failed Validate would get an answer about a value nobody classified.
func (r Reference) PresentableAsCurrent() bool {
	return r.Validate() == nil && r.State.PresentableAsCurrent()
}

// Transition moves a reference to another state.
//
// # The asymmetry, which is the whole rule
//
//	DEMOTION   needs no reason. Concluding less from the same
//	           material is always safe, and requiring a
//	           justification for it would discourage the safe move.
//	PROMOTION  needs one. Claiming more requires somebody to say what
//	           changed, and an unreasoned promotion is how a
//	           superseded record becomes fact again -- which is the
//	           defect this rule was written after.
//
// The reason is not validated for content; nothing can validate that.
// What is enforced is that it EXISTS and travels with the reference,
// so a reviewer reading the case sees the assertion rather than
// inferring it from a state that quietly changed.
func (r Reference) Transition(to State, reason string) (Reference, error) {
	if err := r.Validate(); err != nil {
		return Reference{}, err
	}
	if !to.Known() {
		return Reference{}, fmt.Errorf("%w: %q", ErrUnknownState, to.String())
	}
	promotion := to.PresentableAsCurrent() && !r.State.PresentableAsCurrent()
	if promotion && strings.TrimSpace(reason) == "" {
		return Reference{}, fmt.Errorf(
			"%w: %s -> %s for %s claims more than the reference held; a reference is "+
				"re-established from evidence, and the reason must be recorded",
			ErrPromotion, r.State, to, r.Subject)
	}

	out := r
	out.State = to
	// Fields that only one state may carry do not survive a move away
	// from it. A HISTORICAL reference still naming a replacement is a
	// dangling claim: a reader follows the link and finds a successor
	// that no longer supersedes anything.
	if to != Superseded {
		out.SupersededBy = ""
	}
	if to != External {
		out.Attestor = ""
	}
	if to != Derived {
		out.DerivedFrom = nil
	}
	// A move INTO a state that requires a field the reference does not
	// carry is refused rather than producing an invalid reference.
	if err := out.Validate(); err != nil {
		return Reference{}, fmt.Errorf("temporal: %s -> %s: %w", r.State, to, err)
	}
	return out, nil
}

// Demote is Transition to a state that claims less. It exists as a
// separate name because that is the move callers should reach for by
// default, and because it cannot be misused: a demotion that is
// actually a promotion is refused rather than silently allowed.
func (r Reference) Demote(to State) (Reference, error) {
	if to.PresentableAsCurrent() && !r.State.PresentableAsCurrent() {
		return Reference{}, fmt.Errorf("%w: %s -> %s is a promotion; use Transition and state why",
			ErrPromotion, r.State, to)
	}
	return r.Transition(to, "")
}
