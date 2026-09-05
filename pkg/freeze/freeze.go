// Package freeze is the Qualification-Driven Engineering Freeze.
//
// # What is frozen
//
// Not development. A freeze on all work would be a different mistake:
// the system would stop responding to what the external parties find.
//
// What is frozen is DISCRETIONARY SCOPE. From Round 6, a piece of work
// enters the repository only if it is a direct dependency of one of
// two things:
//
//	an external gate    somebody outside VERIQO is waiting on it
//	a customer pilot    somebody outside VERIQO would use it
//
// Everything else waits, however good it is. That is the whole rule,
// and the discipline is in applying it to work one wants to do.
//
// # Why a rule rather than judgement
//
// Rounds 1 to 5 added assurance because each addition was justified by
// the last. The arguments were sound individually and the aggregate
// was a system that had become very good at examining itself and had
// still never met a document it did not write.
//
// Judgement did not stop that, because judgement was what produced it.
// Each decision looked like the responsible one. So the constraint has
// to be external to the judgement: a proposal names the gate or the
// pilot it serves, or it does not enter.
//
// # The honest part
//
// A freeze is easy to declare and easy to route around, and the usual
// route is a definition: work one wants to do gets described as a
// dependency of a gate it is only distantly related to. Justify()
// therefore requires the gate to be NAMED, and a test in this package
// checks every entry in the Round 6 register against the gate register
// rather than against a string somebody typed.
//
// The register below includes the items this round REFUSED under its
// own rule. A freeze register containing only approvals is a record of
// a freeze that was not applied.
package freeze

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	// ErrNoWarrant is work that serves no external gate and no pilot.
	ErrNoWarrant = errors.New("freeze: this serves nothing outside VERIQO")
	// ErrVagueWarrant is a warrant that names no specific gate.
	ErrVagueWarrant = errors.New("freeze: the warrant names no specific gate or pilot")
)

// Warrant is why a piece of work is permitted under the freeze.
type Warrant string

const (
	// ExternalGate: a named gate cannot be attempted until this exists.
	ExternalGate Warrant = "EXTERNAL_GATE"
	// CustomerPilot: a pilot cannot run until this exists.
	CustomerPilot Warrant = "CUSTOMER_PILOT"
	// Correctness: a defect in something already shipped. Always
	// permitted -- a freeze that forbids fixing a bug is a freeze that
	// will be ignored, and a rule that is ignored constrains nothing.
	Correctness Warrant = "CORRECTNESS"
	// Discretionary: good work that serves neither. REFUSED, and
	// recorded so the decision can be revisited when the freeze lifts.
	Discretionary Warrant = "DISCRETIONARY"
)

func Warrants() []Warrant {
	return []Warrant{ExternalGate, CustomerPilot, Correctness, Discretionary}
}

// Permits reports whether a warrant lets work through.
func (w Warrant) Permits() bool { return w != Discretionary }

// Item is a piece of work weighed against the freeze.
type Item struct {
	// Name is what was built, or proposed.
	Name string `json:"name"`
	// Warrant is the class of permission claimed.
	Warrant Warrant `json:"warrant"`
	// Serves names the specific gate, debt or pilot. A warrant of
	// EXTERNAL_GATE with an empty Serves is the exact evasion this
	// register exists to catch.
	Serves string `json:"serves,omitempty"`
	// Why states, in one sentence, how the work unblocks what it
	// serves. "Related to" is not an answer; the sentence has to
	// describe a dependency.
	Why string `json:"why"`
	// Built records whether it was actually done this round.
	Built bool `json:"built"`
}

func (i Item) Validate() error {
	if strings.TrimSpace(i.Name) == "" {
		return errors.New("freeze: an item with no name")
	}
	valid := false
	for _, w := range Warrants() {
		if w == i.Warrant {
			valid = true
		}
	}
	if !valid {
		return fmt.Errorf("freeze: %s: unknown warrant %q", i.Name, i.Warrant)
	}
	if strings.TrimSpace(i.Why) == "" {
		return fmt.Errorf("freeze: %s states no reason", i.Name)
	}
	switch i.Warrant {
	case ExternalGate, CustomerPilot:
		if strings.TrimSpace(i.Serves) == "" {
			return fmt.Errorf("%w: %s", ErrVagueWarrant, i.Name)
		}
	case Discretionary:
		if i.Built {
			return fmt.Errorf("freeze: %s is discretionary and was built anyway; the "+
				"freeze was declared and not applied", i.Name)
		}
	}
	return nil
}

// Register is a round's work, weighed.
type Register struct {
	Round int    `json:"round"`
	Items []Item `json:"items"`
}

func NewRegister(round int, items ...Item) (*Register, error) {
	if round < 6 {
		return nil, errors.New("freeze: the freeze begins at Round 6")
	}
	seen := map[string]bool{}
	for _, i := range items {
		if err := i.Validate(); err != nil {
			return nil, err
		}
		if seen[i.Name] {
			return nil, fmt.Errorf("freeze: %s appears twice", i.Name)
		}
		seen[i.Name] = true
	}
	return &Register{Round: round, Items: items}, nil
}

// Permitted returns the work the freeze let through.
func (r *Register) Permitted() []Item {
	var out []Item
	for _, i := range r.Items {
		if i.Warrant.Permits() {
			out = append(out, i)
		}
	}
	return out
}

// Refused returns the work the freeze stopped. A register with none is
// a register of a freeze nobody applied, and Report says so.
func (r *Register) Refused() []Item {
	var out []Item
	for _, i := range r.Items {
		if !i.Warrant.Permits() {
			out = append(out, i)
		}
	}
	return out
}

// Gates returns every gate or pilot named, deduplicated and sorted.
func (r *Register) Gates() []string {
	seen := map[string]bool{}
	var out []string
	for _, i := range r.Items {
		if i.Serves == "" || seen[i.Serves] {
			continue
		}
		seen[i.Serves] = true
		out = append(out, i.Serves)
	}
	sort.Strings(out)
	return out
}

func (r *Register) Report() string {
	var b strings.Builder
	fmt.Fprintf(&b, "QUALIFICATION-DRIVEN ENGINEERING FREEZE -- ROUND %d\n", r.Round)
	b.WriteString("  Work enters only as a direct dependency of a named external gate\n")
	b.WriteString("  or a customer pilot. Everything else waits, however good it is.\n\n")

	perm, ref := r.Permitted(), r.Refused()
	b.WriteString("  PERMITTED\n")
	for _, i := range perm {
		mark := " "
		if !i.Built {
			mark = "-"
		}
		fmt.Fprintf(&b, "   %s %s\n", mark, i.Name)
		fmt.Fprintf(&b, "       warrant: %s", i.Warrant)
		if i.Serves != "" {
			fmt.Fprintf(&b, " -> %s", i.Serves)
		}
		b.WriteString("\n")
		b.WriteString(wrap("       ", i.Why))
	}
	b.WriteString("\n  REFUSED UNDER THE FREEZE\n")
	if len(ref) == 0 {
		b.WriteString("   (none)\n")
		b.WriteString("   A freeze register with no refusals is a record of a freeze that\n")
		b.WriteString("   was declared and not applied. Treat this as a finding.\n")
	}
	for _, i := range ref {
		fmt.Fprintf(&b, "   x %s\n", i.Name)
		b.WriteString(wrap("       ", i.Why))
	}
	fmt.Fprintf(&b, "\n  %d permitted, %d refused.\n", len(perm), len(ref))
	b.WriteString(wrap("  gates and pilots served: ", strings.Join(r.Gates(), ", ")))
	return b.String()
}

func wrap(label, text string) string {
	const width = 78
	indent := strings.Repeat(" ", len(label))
	var b strings.Builder
	line := label
	for i, word := range strings.Fields(text) {
		if i > 0 && len(line)+1+len(word) > width {
			b.WriteString(strings.TrimRight(line, " ") + "\n")
			line = indent
		} else if i > 0 {
			line += " "
		}
		line += word
	}
	b.WriteString(strings.TrimRight(line, " ") + "\n")
	return b.String()
}
