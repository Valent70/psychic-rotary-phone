// Package temporal is VERIQO's temporal provenance vocabulary: the
// standing in time of any statement that cites evidence.
//
// # Why this is a type and not a convention
//
// The previous round found a real defect -- documents quoting a
// superseded runtime ledger -- and fixed it with a markdown marker plus
// a test. The review's response was that this treats a provenance
// semantics problem as a documentation problem:
//
//	Jangan hanya mengandalkan marker markdown.
//
// That is right, and the reason is that a marker can only ever answer
// one question (is this quotation deliberate?) while a citation raises
// four:
//
//	EXISTENCE    does the thing cited exist at all?
//	CORRECTNESS  does the citation's content match it?
//	TEMPORAL     which state of the world does it come from?
//	SEMANTIC     is it labelled as what it is?
//
// A marker answers only the fourth, and only by convention. Encoding
// the answer as a value means a citation with no temporal standing is
// not "unmarked" -- it is UNVERIFIED, which is a claim that can be
// checked and refused.
//
// # The zero value is UNVERIFIED, deliberately
//
// Throughout VERIQO the zero value is the honest default: Unknown,
// NotDetermined, NotSought, Open, L0Designed. A reference nobody has
// classified is not current; it is unverified. Any other zero value
// would let an unreviewed citation acquire standing by omission, which
// is precisely how the defect that prompted this package arose.
package temporal

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// State is the temporal standing of a cited statement.
type State string

const (
	// Unverified is the zero value: nobody has classified this
	// reference. It is not an error and not a current claim; it is the
	// absence of a determination, and it may not be presented as fact.
	Unverified State = ""

	// Current means the reference agrees with the artefact as
	// committed. It is the only state that may be read as a claim about
	// the system now.
	Current State = "CURRENT"

	// Historical means the reference describes a past state that was
	// true when written and is quoted deliberately. A historical
	// reference makes no claim about now.
	Historical State = "HISTORICAL"

	// Superseded means the reference describes a past state that has
	// been REPLACED by a specific successor. It is stronger than
	// Historical: it names what replaced it, so a reader can follow the
	// chain forward rather than being left to search.
	Superseded State = "SUPERSEDED"

	// Derived means the reference was computed from another reference
	// rather than observed. A derived value inherits the standing of
	// its source and can never exceed it.
	Derived State = "DERIVED"

	// External means the reference comes from outside VERIQO and its
	// standing is the attestor's, not ours. VERIQO records it and does
	// not vouch for it.
	External State = "EXTERNAL"
)

// States returns the six states, most-authoritative first.
func States() []State {
	return []State{Current, Historical, Superseded, Derived, External, Unverified}
}

// String renders the state, naming the zero value rather than printing
// an empty string, because an empty string in a report reads as an
// oversight rather than as a determination.
func (s State) String() string {
	if s == Unverified {
		return "UNVERIFIED"
	}
	return string(s)
}

// Known reports whether the state is one of the six.
func (s State) Known() bool {
	switch s {
	case Unverified, Current, Historical, Superseded, Derived, External:
		return true
	}
	return false
}

// PresentableAsCurrent reports whether a reference in this state may be
// read as a statement about the system as it stands.
//
// Only Current may. Derived is excluded on purpose: a derived value is
// only as current as what it was derived from, and that is a property
// of the source, not of the derivation.
func (s State) PresentableAsCurrent() bool { return s == Current }

// RequiresSuccessor reports whether the state obliges the reference to
// name what replaced it.
func (s State) RequiresSuccessor() bool { return s == Superseded }

// RequiresSource reports whether the state obliges the reference to
// name what it came from.
func (s State) RequiresSource() bool { return s == Derived }

// RequiresAttestor reports whether the state obliges the reference to
// name who outside VERIQO stands behind it.
func (s State) RequiresAttestor() bool { return s == External }

// Meaning returns the one-line semantics, for reports.
func (s State) Meaning() string {
	switch s {
	case Current:
		return "agrees with the artefact as committed; may be read as a claim about the system now"
	case Historical:
		return "describes a past state, quoted deliberately; makes no claim about now"
	case Superseded:
		return "describes a past state that a named successor replaced"
	case Derived:
		return "computed from another reference; inherits that reference's standing and cannot exceed it"
	case External:
		return "comes from outside VERIQO; the standing is the attestor's, not ours"
	default:
		return "nobody has classified this reference; it may not be presented as fact"
	}
}

var (
	// ErrUnknownState is returned for a state outside the six.
	ErrUnknownState = errors.New("temporal: not one of the six provenance states")
	// ErrNoSubject is returned for a reference that cites nothing.
	ErrNoSubject = errors.New("temporal: a reference must name what it cites")
	// ErrSupersededWithoutSuccessor is the check that makes SUPERSEDED
	// stronger than HISTORICAL. Without a successor the two are the
	// same statement, and the distinction collapses.
	ErrSupersededWithoutSuccessor = errors.New("temporal: a SUPERSEDED reference must name its successor")
	// ErrDerivedWithoutSource is returned for a derivation with no
	// origin. A derived value with no source cannot inherit a standing,
	// so it has none.
	ErrDerivedWithoutSource = errors.New("temporal: a DERIVED reference must name the reference it was derived from")
	// ErrExternalWithoutAttestor is returned for an external claim with
	// nobody behind it, which is an anonymous assertion presented as an
	// outside party's.
	ErrExternalWithoutAttestor = errors.New("temporal: an EXTERNAL reference must name its attestor")
	// ErrPromotion is returned when a transition would raise a
	// reference's standing without new evidence.
	ErrPromotion = errors.New("temporal: standing cannot be promoted")
)

// Reference is one citation with its temporal standing.
type Reference struct {
	// Subject is what is cited: an audit event id, an artefact path, a
	// document section, a control id.
	Subject string
	// State is its standing.
	State State
	// SupersededBy names the successor. Required for Superseded.
	SupersededBy string
	// DerivedFrom names the source. Required for Derived.
	DerivedFrom string
	// Attestor names who outside VERIQO stands behind it. Required for
	// External.
	Attestor string
	// Note is free text for a reader. It is never load-bearing: no
	// check in this package reads it, because a rule that depended on
	// prose would be the marker problem again.
	Note string
}

// Validate refuses a reference whose state and fields disagree.
func (r Reference) Validate() error {
	if strings.TrimSpace(r.Subject) == "" {
		return ErrNoSubject
	}
	if !r.State.Known() {
		return fmt.Errorf("%w: %q", ErrUnknownState, string(r.State))
	}
	if r.State.RequiresSuccessor() && strings.TrimSpace(r.SupersededBy) == "" {
		return fmt.Errorf("%w: %s", ErrSupersededWithoutSuccessor, r.Subject)
	}
	if r.State.RequiresSource() && strings.TrimSpace(r.DerivedFrom) == "" {
		return fmt.Errorf("%w: %s", ErrDerivedWithoutSource, r.Subject)
	}
	if r.State.RequiresAttestor() && strings.TrimSpace(r.Attestor) == "" {
		return fmt.Errorf("%w: %s", ErrExternalWithoutAttestor, r.Subject)
	}
	// A field that contradicts the state is refused rather than
	// ignored. A reference marked CURRENT that also names a successor
	// is two claims, and silently preferring one of them is how a
	// stale citation survives review.
	if r.State != Superseded && strings.TrimSpace(r.SupersededBy) != "" {
		return fmt.Errorf("temporal: %s is %s but names a successor %q: only SUPERSEDED may",
			r.Subject, r.State, r.SupersededBy)
	}
	if r.State != Derived && strings.TrimSpace(r.DerivedFrom) != "" {
		return fmt.Errorf("temporal: %s is %s but names a source %q: only DERIVED may",
			r.Subject, r.State, r.DerivedFrom)
	}
	if r.State != External && strings.TrimSpace(r.Attestor) != "" {
		return fmt.Errorf("temporal: %s is %s but names an attestor %q: only EXTERNAL may",
			r.Subject, r.State, r.Attestor)
	}
	return nil
}

// PresentableAsCurrent reports whether this reference may be read as a
// statement about the system now.
func (r Reference) PresentableAsCurrent() bool {
	return r.Validate() == nil && r.State.PresentableAsCurrent()
}

// rank orders the states by how much standing they carry. It exists
// only to make promotion detectable; it is not a quality ordering, and
// EXTERNAL is deliberately not ranked above CURRENT because an outside
// attestation of a stale fact is still stale.
func (s State) rank() int {
	switch s {
	case Current:
		return 4
	case External:
		return 3
	case Derived:
		return 2
	case Superseded, Historical:
		return 1
	default:
		return 0
	}
}

// Transition moves a reference to a new state, refusing a promotion
// that is not accounted for.
//
// Demotion is always allowed: discovering that something you thought
// was current has been superseded is ordinary, and requiring ceremony
// to record it would discourage recording it. Promotion is the
// direction that needs evidence, because promotion is how a stale claim
// becomes a current one.
func (r Reference) Transition(to State, because string) (Reference, error) {
	if !to.Known() {
		return Reference{}, fmt.Errorf("%w: %q", ErrUnknownState, string(to))
	}
	if to.rank() > r.State.rank() && strings.TrimSpace(because) == "" {
		return Reference{}, fmt.Errorf("%w: %s -> %s without a stated reason",
			ErrPromotion, r.State, to)
	}
	out := r
	out.State = to
	// Clear the fields the new state does not license, so a transition
	// cannot leave a contradiction behind.
	if to != Superseded {
		out.SupersededBy = ""
	}
	if to != Derived {
		out.DerivedFrom = ""
	}
	if to != External {
		out.Attestor = ""
	}
	if strings.TrimSpace(because) != "" {
		out.Note = because
	}
	return out, out.Validate()
}

// Supersede marks a current reference as replaced by a named successor.
func (r Reference) Supersede(successor, because string) (Reference, error) {
	if strings.TrimSpace(successor) == "" {
		return Reference{}, ErrSupersededWithoutSuccessor
	}
	out := r
	out.State = Superseded
	out.SupersededBy = successor
	out.DerivedFrom, out.Attestor = "", ""
	if strings.TrimSpace(because) != "" {
		out.Note = because
	}
	return out, out.Validate()
}

// Describe renders a reference for a report.
func (r Reference) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s [%s]", r.Subject, r.State)
	switch {
	case r.State == Superseded:
		fmt.Fprintf(&b, " superseded by %s", r.SupersededBy)
	case r.State == Derived:
		fmt.Fprintf(&b, " derived from %s", r.DerivedFrom)
	case r.State == External:
		fmt.Fprintf(&b, " attested by %s", r.Attestor)
	}
	if strings.TrimSpace(r.Note) != "" {
		fmt.Fprintf(&b, " -- %s", r.Note)
	}
	return b.String()
}

// Set is a collection of references that can be audited as a whole.
type Set struct {
	refs []Reference
}

// NewSet builds a validated set.
func NewSet(refs ...Reference) (*Set, error) {
	s := &Set{}
	for _, r := range refs {
		if err := s.Add(r); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Add validates and appends.
func (s *Set) Add(r Reference) error {
	if err := r.Validate(); err != nil {
		return err
	}
	s.refs = append(s.refs, r)
	return nil
}

// All returns a copy.
func (s *Set) All() []Reference { return append([]Reference(nil), s.refs...) }

// Unverified returns the subjects nobody has classified. A non-empty
// result is the honest finding, not an error: it names what has not
// been looked at.
func (s *Set) Unverified() []string {
	var out []string
	for _, r := range s.refs {
		if r.State == Unverified {
			out = append(out, r.Subject)
		}
	}
	sort.Strings(out)
	return out
}

// Report renders the set grouped by state.
func (s *Set) Report() string {
	var b strings.Builder
	b.WriteString("VERIQO temporal provenance\n")
	b.WriteString("The standing in time of every citation. UNVERIFIED is the zero value:\n")
	b.WriteString("a reference nobody has classified is not current, it is unclassified.\n\n")
	byState := map[State][]Reference{}
	for _, r := range s.refs {
		byState[r.State] = append(byState[r.State], r)
	}
	for _, st := range States() {
		rs := byState[st]
		if len(rs) == 0 {
			continue
		}
		sort.Slice(rs, func(i, j int) bool { return rs[i].Subject < rs[j].Subject })
		fmt.Fprintf(&b, "%s (%d)\n    %s\n", st, len(rs), st.Meaning())
		for _, r := range rs {
			fmt.Fprintf(&b, "      %s\n", r.Describe())
		}
		b.WriteString("\n")
	}
	return b.String()
}
