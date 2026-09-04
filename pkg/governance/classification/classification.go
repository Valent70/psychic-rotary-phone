// Package classification is VERIQO's data classification lattice.
//
// # Why classification is a lattice, not a label
//
// Law 10 says security is part of semantics. The concrete consequence
// is that a derived artefact cannot be less classified than what it
// was derived from -- and that rule needs a lattice with a join, not a
// list of names, because a finding derived from four evidence items
// takes the LEAST UPPER BOUND of their classifications, not the first
// one somebody wrote down.
//
// Getting this wrong has one specific shape: an export bundle built
// from RESTRICTED evidence is labelled INTERNAL because the finding
// object was created before the evidence was attached. The label is
// then true of the object and false of its contents.
//
// # Two axes, deliberately separate
//
//	SENSITIVITY  how much damage disclosure does
//	HANDLING     what may be done with it regardless of sensitivity
//
// A commercially unremarkable AIS position under a redistribution
// prohibition is PUBLIC-sensitivity and NO_REDISTRIBUTION-handling.
// Merging the axes forces one of the two to be wrong.
package classification

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	ErrUnknownLevel  = errors.New("classification: unknown level")
	ErrDowngrade     = errors.New("classification: a derivative may not be classified below its source")
	ErrNotClassified = errors.New("classification: artefact is unclassified; UNCLASSIFIED is a determination and the zero value is not")
)

// Level is the sensitivity axis, ordered.
type Level int

const (
	// Unset is the zero value and is never a valid classification. An
	// artefact that nobody classified must not look like a public one.
	Unset Level = iota
	Public
	Internal
	Confidential
	Restricted
	Secret
)

var levelNames = map[Level]string{
	Unset: "UNSET", Public: "PUBLIC", Internal: "INTERNAL",
	Confidential: "CONFIDENTIAL", Restricted: "RESTRICTED", Secret: "SECRET",
}

func (l Level) String() string {
	if n, ok := levelNames[l]; ok {
		return n
	}
	return fmt.Sprintf("Level(%d)", int(l))
}

func (l Level) Valid() bool { return l >= Public && l <= Secret }

// ParseLevel is the only way a level enters from configuration.
func ParseLevel(s string) (Level, error) {
	for l, n := range levelNames {
		if l != Unset && strings.EqualFold(n, s) {
			return l, nil
		}
	}
	return Unset, fmt.Errorf("%w: %q", ErrUnknownLevel, s)
}

// Handling is a caveat that travels independently of sensitivity.
type Handling string

const (
	NoRedistribution  Handling = "NO_REDISTRIBUTION"
	NoExport          Handling = "NO_EXPORT"
	NoTraining        Handling = "NO_TRAINING"
	NoDerivative      Handling = "NO_DERIVATIVE"
	PersonalData      Handling = "PERSONAL_DATA"
	LegallyPrivileged Handling = "LEGALLY_PRIVILEGED"
	UnderLegalHold    Handling = "UNDER_LEGAL_HOLD"
)

func (h Handling) Valid() bool {
	switch h {
	case NoRedistribution, NoExport, NoTraining, NoDerivative,
		PersonalData, LegallyPrivileged, UnderLegalHold:
		return true
	}
	return false
}

// Marking is a complete classification: one level plus a caveat set.
type Marking struct {
	Level    Level      `json:"level"`
	Handling []Handling `json:"handling,omitempty"`
}

// New builds a marking, normalising the caveat set so two markings
// with the same caveats in different orders are equal and hash alike.
func New(l Level, h ...Handling) (Marking, error) {
	if !l.Valid() {
		return Marking{}, fmt.Errorf("%w: %s", ErrUnknownLevel, l)
	}
	seen := map[Handling]bool{}
	var out []Handling
	for _, c := range h {
		if !c.Valid() {
			return Marking{}, fmt.Errorf("classification: unknown handling caveat %q", c)
		}
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return Marking{Level: l, Handling: out}, nil
}

// MustNew is New for compile-time constants.
func MustNew(l Level, h ...Handling) Marking {
	m, err := New(l, h...)
	if err != nil {
		panic(err)
	}
	return m
}

func (m Marking) Valid() bool { return m.Level.Valid() }

func (m Marking) String() string {
	if len(m.Handling) == 0 {
		return m.Level.String()
	}
	parts := make([]string, len(m.Handling))
	for i, h := range m.Handling {
		parts[i] = string(h)
	}
	return m.Level.String() + "//" + strings.Join(parts, "/")
}

// Has reports whether a caveat applies.
func (m Marking) Has(h Handling) bool {
	for _, c := range m.Handling {
		if c == h {
			return true
		}
	}
	return false
}

// Dominates reports whether m is at least as restrictive as o on both
// axes: its level is not lower AND it carries every caveat o carries.
//
// This is the lattice order. Note that it is a PARTIAL order:
// SECRET with no caveats does not dominate PUBLIC//NO_EXPORT, because
// the caveat is not implied by the level. Code that assumes a total
// order here will conclude that a highly classified artefact may be
// exported because its level is higher.
func (m Marking) Dominates(o Marking) bool {
	if m.Level < o.Level {
		return false
	}
	for _, h := range o.Handling {
		if !m.Has(h) {
			return false
		}
	}
	return true
}

// Join is the least upper bound: the classification a derivative must
// carry to be at least as protected as everything it came from.
//
// This is the function that keeps an export bundle from being labelled
// by the object that happened to be created first.
func Join(ms ...Marking) (Marking, error) {
	if len(ms) == 0 {
		return Marking{}, ErrNotClassified
	}
	out := Marking{Level: Public}
	seen := map[Handling]bool{}
	for _, m := range ms {
		if !m.Valid() {
			return Marking{}, fmt.Errorf("%w: %s", ErrNotClassified, m.Level)
		}
		if m.Level > out.Level {
			out.Level = m.Level
		}
		for _, h := range m.Handling {
			seen[h] = true
		}
	}
	for h := range seen {
		out.Handling = append(out.Handling, h)
	}
	sort.Slice(out.Handling, func(i, j int) bool { return out.Handling[i] < out.Handling[j] })
	return out, nil
}

// Derive classifies a derivative from its sources and refuses a
// downgrade.
//
// proposed is what the caller wants to label the derivative. It may be
// MORE restrictive than the join -- a redaction manifest over public
// inputs can legitimately be RESTRICTED. It may never be less.
func Derive(proposed Marking, sources ...Marking) (Marking, error) {
	floor, err := Join(sources...)
	if err != nil {
		return Marking{}, err
	}
	if !proposed.Valid() {
		return floor, nil // an unstated proposal defaults to the floor
	}
	if !proposed.Dominates(floor) {
		return Marking{}, fmt.Errorf("%w: proposed %s does not dominate the source join %s",
			ErrDowngrade, proposed, floor)
	}
	return proposed, nil
}

// Readable reports whether a clearance may read a marking.
//
// The caveat rule is the one that surprises people: clearance must
// carry the caveat as an authorisation, so a SECRET-cleared reader
// without the PERSONAL_DATA authorisation cannot read personal data.
// Level alone is not a key to everything below it.
func Readable(clearance, marking Marking) error {
	if !clearance.Valid() {
		return fmt.Errorf("%w: reader has no clearance", ErrNotClassified)
	}
	if !marking.Valid() {
		return fmt.Errorf("%w: artefact carries no marking", ErrNotClassified)
	}
	if clearance.Level < marking.Level {
		return fmt.Errorf("classification: clearance %s is below %s", clearance.Level, marking.Level)
	}
	for _, h := range marking.Handling {
		// Prohibitions on downstream USE (export, training,
		// redistribution, derivation) do not restrict reading. Caveats
		// about the NATURE of the content do.
		switch h {
		case NoExport, NoTraining, NoRedistribution, NoDerivative:
			continue
		}
		if !clearance.Has(h) {
			return fmt.Errorf("classification: reader is not authorised for %s", h)
		}
	}
	return nil
}
