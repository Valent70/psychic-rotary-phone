// Package metrics keeps three kinds of number apart.
//
// # Test inflation
//
// "814 tests" is impressive and it is a trap. The moment a test count
// becomes a quality signal, the cheapest way to improve quality is to
// write more tests -- and the cheapest tests to write are the ones
// that assert what the code already does. A codebase can double its
// test count without becoming one line safer, and the number will not
// say so.
//
// The deeper problem is that the number is on the wrong axis
// entirely. One independent penetration test is worth more than two
// hundred additional unit tests. One real-world document corpus is
// worth more than a hundred additional synthetic fixtures. Those are
// not comparisons between bigger and smaller numbers; they are
// comparisons between DIFFERENT KINDS OF EVIDENCE, and a single
// dashboard that shows both invites the arithmetic that destroys the
// distinction.
//
// So there are three registers here and they cannot be combined. The
// type system enforces it: there is no function that takes two
// registers, and no aggregate of any kind.
//
//	SOFTWARE VERIFICATION   tests, coverage, race, vet, fuzz, mutation
//	                        -> what the builder can establish alone
//
//	ASSURANCE QUALIFICATION external evidence, independent validation,
//	                        real corpus, operational history, legal
//	                        review
//	                        -> what only somebody else can establish
//
//	PRODUCTION EVIDENCE     uptime, incidents, recovery, rotation,
//	                        failover, access review
//	                        -> what only running the thing establishes
//
// The registers do not compensate for one another in either direction.
// A perfect software-verification register says nothing about the
// other two, and an empty assurance register is not offset by any
// amount of testing.
package metrics

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

var (
	ErrWrongRegister = errors.New("metrics: this measure does not belong to this register")
	ErrNoMeasure     = errors.New("metrics: a measure states nothing")
	ErrCombine       = errors.New("metrics: registers may not be combined")
)

// Register is one of the three kinds.
type Register string

const (
	SoftwareVerification   Register = "SOFTWARE_VERIFICATION"
	AssuranceQualification Register = "ASSURANCE_QUALIFICATION"
	ProductionEvidence     Register = "PRODUCTION_EVIDENCE"
)

func Registers() []Register {
	return []Register{SoftwareVerification, AssuranceQualification, ProductionEvidence}
}

func (r Register) Valid() bool {
	for _, k := range Registers() {
		if k == r {
			return true
		}
	}
	return false
}

// SelfProducible reports whether the builder can move this register
// alone.
//
// Only the first. That asymmetry is why the three must not be summed:
// two of them cannot be improved by working harder, and an aggregate
// would let the one that can be, hide the two that cannot.
func (r Register) SelfProducible() bool { return r == SoftwareVerification }

// WhatItEstablishes states, in a sentence, the limit of what this
// register can show. It is printed with every report, because the
// limit is the part a reader forgets.
func (r Register) WhatItEstablishes() string {
	switch r {
	case SoftwareVerification:
		return "that the code does what its authors intended. It says nothing about " +
			"whether the intention was right, and nothing about behaviour outside the " +
			"cases its authors imagined"
	case AssuranceQualification:
		return "that somebody other than the builder examined a control and said what " +
			"they found. It is the only register whose entries cannot be produced by " +
			"working harder"
	case ProductionEvidence:
		return "that the system behaved a certain way while running, for a stated " +
			"period, under stated load. It is the only register that can distinguish a " +
			"design that works from one that has not yet been tried"
	}
	return ""
}

// Measure is one number, in one register.
type Measure struct {
	Register Register `json:"register"`
	Name     string   `json:"name"`
	// Value is the figure, as text. It is a string rather than a
	// number deliberately: "0", "none", "never attempted" and "90
	// days" are all honest values, and forcing them into a float would
	// turn three of them into zero.
	Value string `json:"value"`
	// Basis says how it was arrived at.
	Basis string `json:"basis"`
	// Caveat is what the measure does NOT show. Required: a measure
	// with no stated limit is read as covering everything.
	Caveat string    `json:"caveat"`
	At     time.Time `json:"at,omitempty"`
}

func (m Measure) Validate() error {
	if !m.Register.Valid() {
		return fmt.Errorf("%w: %q", ErrWrongRegister, m.Register)
	}
	if strings.TrimSpace(m.Name) == "" || strings.TrimSpace(m.Value) == "" {
		return fmt.Errorf("%w: %q", ErrNoMeasure, m.Name)
	}
	if strings.TrimSpace(m.Basis) == "" {
		return fmt.Errorf("%w: %s states no basis", ErrNoMeasure, m.Name)
	}
	if strings.TrimSpace(m.Caveat) == "" {
		return fmt.Errorf("%w: %s states no caveat; a measure with no stated limit is "+
			"read as covering everything", ErrNoMeasure, m.Name)
	}
	return nil
}

// Set is the measures of ONE register.
//
// There is deliberately no type holding all three. A struct with three
// fields would be summed by somebody within a month.
type Set struct {
	register Register
	measures []Measure
}

// New builds a set for one register and refuses a measure from
// another.
func New(r Register, ms ...Measure) (*Set, error) {
	if !r.Valid() {
		return nil, fmt.Errorf("%w: %q", ErrWrongRegister, r)
	}
	s := &Set{register: r}
	seen := map[string]bool{}
	for _, m := range ms {
		if err := m.Validate(); err != nil {
			return nil, err
		}
		if m.Register != r {
			return nil, fmt.Errorf("%w: %s belongs to %s and was offered to %s",
				ErrWrongRegister, m.Name, m.Register, r)
		}
		if seen[m.Name] {
			return nil, fmt.Errorf("metrics: %s appears twice in %s", m.Name, r)
		}
		seen[m.Name] = true
		s.measures = append(s.measures, m)
	}
	sort.Slice(s.measures, func(i, j int) bool { return s.measures[i].Name < s.measures[j].Name })
	return s, nil
}

func (s *Set) Register() Register { return s.register }

func (s *Set) Measures() []Measure { return append([]Measure(nil), s.measures...) }

// Empty reports whether the register has nothing in it.
//
// An empty assurance register is the single most important fact a
// reader can be told, and it is easy to miss when a report shows three
// sections and one of them is short.
func (s *Set) Empty() bool { return len(s.measures) == 0 }

// Report renders one register, with its limit stated.
func (s *Set) Report() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", s.register)
	fmt.Fprintf(&b, "  establishes: %s\n", s.register.WhatItEstablishes())
	if !s.register.SelfProducible() {
		b.WriteString("  this register cannot be improved by working harder\n")
	}
	b.WriteString("\n")
	if s.Empty() {
		b.WriteString("  EMPTY. Nothing in this register has been established at all.\n")
		return b.String()
	}
	for _, m := range s.measures {
		fmt.Fprintf(&b, "  %-34s %s\n", m.Name, m.Value)
		fmt.Fprintf(&b, "  %-34s   basis:  %s\n", "", m.Basis)
		fmt.Fprintf(&b, "  %-34s   caveat: %s\n", "", m.Caveat)
	}
	return b.String()
}

// Panel renders all three registers, separated, with the rule against
// combining them stated between each pair.
//
// It takes three Sets rather than a container, and checks that each is
// the register it claims, so a caller cannot pass the same one twice
// and produce a report that looks complete.
func Panel(sv, aq, pe *Set) (string, error) {
	if sv == nil || aq == nil || pe == nil {
		return "", fmt.Errorf("%w: a panel showing fewer than three registers reads as a "+
			"complete picture", ErrCombine)
	}
	for want, got := range map[Register]*Set{
		SoftwareVerification: sv, AssuranceQualification: aq, ProductionEvidence: pe,
	} {
		if got.register != want {
			return "", fmt.Errorf("%w: the %s slot holds a %s register",
				ErrWrongRegister, want, got.register)
		}
	}

	var b strings.Builder
	b.WriteString("THREE REGISTERS, DELIBERATELY NOT COMBINED\n\n")
	b.WriteString("  One independent penetration test is worth more than two hundred\n")
	b.WriteString("  additional unit tests. One real-world corpus is worth more than a\n")
	b.WriteString("  hundred additional synthetic fixtures. Those are not comparisons\n")
	b.WriteString("  between bigger and smaller numbers -- they are comparisons between\n")
	b.WriteString("  different KINDS of evidence, and no arithmetic relates them.\n\n")
	b.WriteString("  There is no total below, and there is no function in this package\n")
	b.WriteString("  that would produce one.\n\n")

	for i, s := range []*Set{sv, aq, pe} {
		if i > 0 {
			b.WriteString("\n" + strings.Repeat("-", 66) + "\n\n")
		}
		b.WriteString(s.Report())
	}

	if aq.Empty() {
		b.WriteString("\n" + strings.Repeat("=", 66) + "\n")
		b.WriteString("The assurance register is EMPTY. No quantity of software\n")
		b.WriteString("verification compensates for that, because the two registers do\n")
		b.WriteString("not measure the same thing. A reader who takes the first section\n")
		b.WriteString("as reassurance has made the exact error this separation prevents.\n")
	}
	return b.String(), nil
}
