// Package epistemic is the Epistemic Firewall.
//
// # The failure it exists to stop
//
// Two slides, and they are the same slide:
//
//	unknown -> assumed -> scored -> trusted -> decision
//
//	internal test -> internal assurance -> external qualification
//	              -> production proven
//
// In both, each arrow is individually reasonable and the chain as a
// whole is a laundering operation. Nothing is falsified at any step;
// something merely stops being marked as not-known, and by the end it
// is carrying weight it never earned.
//
// The firewall is four refusals. They are stated as inequalities
// because each names a pair of things that a system, left alone, will
// eventually treat as identical:
//
//	UNREADABLE   != VERIFIED    a document nobody could parse has not
//	                            been checked and found clean; it has
//	                            not been checked
//
//	UNPARSEABLE  != ABSENT      a field that failed to decode is not a
//	                            field that was not there. The first is
//	                            a fault; the second is a fact
//
//	MISSING      != VALID       an absent constraint is not a
//	                            satisfied one. Skipping a check is not
//	                            passing it
//
//	UNKNOWN      != NEGATIVE    not having found something is not
//	                            having found its absence. This is
//	                            Law 5, and it is the one people
//	                            re-derive wrongly most often
//
// And the invariant over all four:
//
//	Unreadable evidence can never increase assurance.
//
// # Why a type rather than a rule
//
// Every one of these failures happens through a ZERO VALUE. A struct
// field that failed to populate is empty; empty compares equal to
// "nothing was there"; and "nothing was there" is routinely treated as
// "nothing is wrong". The firewall works by making the unreadable case
// a VALUE rather than an absence, so that the two cannot compare
// equal, and by making the assurance-relevant operations refuse it.
package epistemic

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	ErrUnreadable   = errors.New("epistemic: unreadable evidence cannot increase assurance")
	ErrUnknownState = errors.New("epistemic: unknown epistemic state")
	ErrCoerced      = errors.New("epistemic: an epistemic state was coerced into a different one")
)

// State is what is known about a piece of information.
//
// The zero value is Unexamined, deliberately. A field nobody filled in
// must not read as "fine", and it must not read as "absent" either --
// nobody looked, and that is a third thing.
type State int

const (
	// Unexamined: nobody has looked. The zero value.
	Unexamined State = iota
	// Unreadable: it was looked at and could not be decoded --
	// encrypted, corrupt, in a format nothing here parses. A FAULT in
	// the observation, not a fact about the world.
	Unreadable
	// Absent: it was looked for and is genuinely not there. A FACT
	// about the world, and a far stronger statement than Unreadable.
	Absent
	// Present: it is there and was read.
	Present
	// Verified: it is there, was read, and something independent
	// confirmed it.
	Verified
	// Contradicted: it is there, was read, and something contradicts
	// it. Deliberately ranked ABOVE Present in information terms and
	// treated as a finding rather than as a failure -- a contradiction
	// is knowledge.
	Contradicted
)

var names = map[State]string{
	Unexamined: "UNEXAMINED", Unreadable: "UNREADABLE", Absent: "ABSENT",
	Present: "PRESENT", Verified: "VERIFIED", Contradicted: "CONTRADICTED",
}

func (s State) String() string {
	if n, ok := names[s]; ok {
		return n
	}
	return fmt.Sprintf("State(%d)", int(s))
}

func (s State) MarshalJSON() ([]byte, error) { return []byte(`"` + s.String() + `"`), nil }

func (s State) Valid() bool { _, ok := names[s]; return ok }

// States returns every state in a fixed order.
func States() []State {
	out := make([]State, 0, len(names))
	for s := range names {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func Parse(s string) (State, error) {
	for k, v := range names {
		if strings.EqualFold(v, strings.TrimSpace(s)) {
			return k, nil
		}
	}
	return Unexamined, fmt.Errorf("%w: %q", ErrUnknownState, s)
}

// Readable reports whether the information could be decoded at all.
//
// It is the gate on everything downstream: an unreadable input may be
// recorded, reported and investigated, and may not contribute to any
// assurance judgement.
func (s State) Readable() bool { return s >= Absent }

// MayIncreaseAssurance is the firewall's central rule.
//
//	Unreadable evidence can never increase assurance.
//
// UNEXAMINED and UNREADABLE both return false, for different reasons:
// nobody looked, and looking failed. ABSENT returns false too, which
// is the subtle one -- confirmed absence is real information and it is
// information about what is NOT there, so it cannot raise confidence
// in a positive claim. It can lower it, which is a different operation.
func (s State) MayIncreaseAssurance() bool { return s == Present || s == Verified }

// IsFault reports whether the state describes a problem with the
// OBSERVATION rather than a fact about the world.
//
// The distinction decides who fixes it. A fault is fixed by obtaining
// a better copy, a decoder, or a key. A fact is not fixable at all --
// it is the answer.
func (s State) IsFault() bool { return s == Unexamined || s == Unreadable }

// Meaning states, in a sentence, what the state does and does not say.
func (s State) Meaning() string {
	switch s {
	case Unexamined:
		return "nobody has looked. This is not a statement about the information; it is " +
			"the absence of one"
	case Unreadable:
		return "it was looked at and could not be decoded. Nothing follows about its " +
			"content -- a document nobody could parse has not been checked and found " +
			"clean, it has not been checked"
	case Absent:
		return "it was looked for and is genuinely not there. This is a fact about the " +
			"world and is much stronger than UNREADABLE"
	case Present:
		return "it is there and was read. Nothing independent has confirmed it"
	case Verified:
		return "it is there, was read, and something independent confirmed it"
	case Contradicted:
		return "it is there, was read, and something contradicts it. A contradiction is " +
			"knowledge, not a failure"
	}
	return ""
}

// --- the four inequalities, as executable checks -------------------
//
// Each is a function rather than a comment because a comment cannot
// fail a build. They take the two things that must not be conflated
// and return an error when a caller has conflated them.

// UnreadableIsNotVerified refuses a claim that unreadable material was
// checked.
//
// The real-world shape: a redaction worker that cannot decode a
// document, reports no forbidden terms found, and is read as having
// found none.
func UnreadableIsNotVerified(s State, claimedVerified bool) error {
	if s == Unreadable && claimedVerified {
		return fmt.Errorf("%w: material in state %s was reported as verified. Nothing was "+
			"read, so nothing was checked", ErrCoerced, s)
	}
	return nil
}

// UnparseableIsNotAbsent refuses treating a decode failure as a
// finding of absence.
//
// The real-world shape: a field that fails to parse is skipped, the
// record is processed without it, and downstream code sees a record
// that simply did not carry that field.
func UnparseableIsNotAbsent(s State, treatedAsAbsent bool) error {
	if s == Unreadable && treatedAsAbsent {
		return fmt.Errorf("%w: material in state %s was treated as ABSENT. A field that "+
			"failed to decode is a fault in the observation; a field that is not there "+
			"is a fact about the world", ErrCoerced, s)
	}
	return nil
}

// MissingIsNotValid refuses treating a check that did not run as one
// that passed.
//
// The real-world shape: a validation step is skipped because its input
// was unavailable, and the pipeline continues as though it had passed.
func MissingIsNotValid(checkRan, treatedAsValid bool) error {
	if !checkRan && treatedAsValid {
		return fmt.Errorf("%w: a check that did not run was treated as satisfied. "+
			"Skipping a check is not passing it", ErrCoerced)
	}
	return nil
}

// UnknownIsNotNegative refuses treating the absence of a finding as a
// finding of absence.
//
// This is Law 5, and it is the one people re-derive wrongly most
// often, because in ordinary speech "we found nothing" and "there is
// nothing" are the same sentence.
func UnknownIsNotNegative(s State, treatedAsNegative bool) error {
	if (s == Unexamined || s == Unreadable) && treatedAsNegative {
		return fmt.Errorf("%w: material in state %s was treated as a negative finding. "+
			"Not having found something is not having found its absence", ErrCoerced, s)
	}
	return nil
}

// Observation is one piece of information with its epistemic state.
type Observation struct {
	// Subject is what the observation is about.
	Subject string `json:"subject"`
	// State is what is known.
	State State `json:"state"`
	// Why explains a fault. Required for UNEXAMINED and UNREADABLE:
	// "could not be read" without a reason is not actionable, and the
	// action -- get a key, get a decoder, get a better copy -- depends
	// entirely on the reason.
	Why string `json:"why,omitempty"`
	// Value is the content, when there is any.
	Value string `json:"value,omitempty"`
}

func (o Observation) Validate() error {
	if strings.TrimSpace(o.Subject) == "" {
		return errors.New("epistemic: an observation has no subject")
	}
	if !o.State.Valid() {
		return fmt.Errorf("%w: %v", ErrUnknownState, o.State)
	}
	if o.State.IsFault() && strings.TrimSpace(o.Why) == "" {
		return fmt.Errorf("epistemic: %s is %s and says why not. 'Could not be read' "+
			"without a reason is not actionable: the fix depends entirely on whether it "+
			"was encrypted, corrupt, or in a format nothing here decodes", o.Subject, o.State)
	}
	if o.State == Absent && strings.TrimSpace(o.Value) != "" {
		return fmt.Errorf("epistemic: %s is ABSENT and carries a value", o.Subject)
	}
	return nil
}

// Set is a group of observations assessed together.
type Set struct {
	Observations []Observation
}

// Supporting returns the observations that may contribute to a
// positive assurance judgement.
//
// It is the firewall in its most-used form: a caller that ranges over
// this instead of over Observations cannot accidentally count an
// unreadable input as support.
func (s Set) Supporting() []Observation {
	var out []Observation
	for _, o := range s.Observations {
		if o.State.MayIncreaseAssurance() {
			out = append(out, o)
		}
	}
	return out
}

// Faults returns the observations that failed rather than answered.
func (s Set) Faults() []Observation {
	var out []Observation
	for _, o := range s.Observations {
		if o.State.IsFault() {
			out = append(out, o)
		}
	}
	return out
}

// Sound reports whether every observation is either informative or
// explicitly recorded as a fault.
func (s Set) Sound() error {
	for _, o := range s.Observations {
		if err := o.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Coverage describes what a set actually establishes.
//
// It deliberately reports four counts rather than a ratio. A ratio --
// "4 of 6 verified" -- reads as two-thirds of the way to something,
// and the missing two are not two-thirds of anything: they are a
// different kind of gap, and one of them may be the one that matters.
type Coverage struct {
	Total int `json:"total"`
	// Informative is the count that says something about the world:
	// ABSENT, PRESENT, VERIFIED, CONTRADICTED.
	Informative int `json:"informative"`
	// Supporting is the subset that may raise confidence in a positive
	// claim.
	Supporting int `json:"supporting"`
	// Faults is the count that failed rather than answered.
	Faults int `json:"faults"`
}

// Assess computes coverage, refusing an unsound set.
func (s Set) Assess() (Coverage, error) {
	if err := s.Sound(); err != nil {
		return Coverage{}, err
	}
	c := Coverage{Total: len(s.Observations)}
	for _, o := range s.Observations {
		switch {
		case o.State.IsFault():
			c.Faults++
		default:
			c.Informative++
		}
		if o.State.MayIncreaseAssurance() {
			c.Supporting++
		}
	}
	return c, nil
}

// Statement renders coverage the way it must reach a reader.
func (c Coverage) Statement() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d observation(s): %d informative, %d supporting a positive claim, "+
		"%d fault(s)", c.Total, c.Informative, c.Supporting, c.Faults)
	if c.Faults > 0 {
		fmt.Fprintf(&b, ". The %d fault(s) are not evidence of anything. They are "+
			"observations that failed, and they must not be read as a partial result: "+
			"the thing that could not be read may be the thing that mattered", c.Faults)
	}
	return b.String()
}

// Report renders a set with every fault's reason.
func (s Set) Report() string {
	var b strings.Builder
	b.WriteString("EPISTEMIC STATE\n")
	b.WriteString("  unreadable != verified   unparseable != absent\n")
	b.WriteString("  missing    != valid      unknown     != negative\n\n")
	for _, o := range s.Observations {
		fmt.Fprintf(&b, "  %-34s %s\n", o.Subject, o.State)
		if o.Why != "" {
			fmt.Fprintf(&b, "  %-34s   why: %s\n", "", o.Why)
		}
	}
	if c, err := s.Assess(); err == nil {
		b.WriteString("\n  " + c.Statement() + "\n")
	}
	return b.String()
}
