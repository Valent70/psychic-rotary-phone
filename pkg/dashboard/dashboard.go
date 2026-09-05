// Package dashboard is what VERIQO leads with.
//
// # The metric that was the headline, and should not be
//
// "37 passed, 0 failed." It is true, it is cheap to improve, and it
// answers a question nobody outside engineering asked. A reader who
// sees it first learns that the team is diligent and learns nothing
// about whether the system has ever met a document it did not write.
//
// Worse, it moves. Adding tests raises it, so a team optimising the
// headline optimises the thing that is easiest to raise -- and the
// nine figures below, all of which are zero or unmeasured, stay where
// they are while the headline improves. A dashboard shaped like that
// does not report progress; it manufactures the appearance of it.
//
// So the test count is not on this board, and IsHeadline refuses it by
// name.
//
// # What the headline is instead
//
// Nine measures, every one of which requires something VERIQO cannot
// produce alone. Eight read zero. That is the honest state and it is
// the reason the board exists: a dashboard whose headline VERIQO can
// raise by working harder is a dashboard that will be raised by
// working harder, on the wrong thing.
//
// # Denominator and scope, always
//
// A bare number is unreadable. "12 documents processed" -- out of how
// many, of what kind, collected how?
//
// Every measure here carries a denominator and a scope, and where the
// denominator is not yet known the measure says NOT MEASURED rather
// than zero. The difference matters: zero is a measurement (we looked,
// and there were none), and NOT MEASURED is the absence of one (we
// have no basis to count). Rendering them the same way is the error
// the epistemic firewall exists to prevent, committed by the reporting
// layer.
package dashboard

import (
	"errors"
	"fmt"
	"strings"
)

// ErrNotHeadline is a measure that must not lead.
var ErrNotHeadline = errors.New("dashboard: this is not a headline measure")

// Banned are measures that must never be the headline, with why.
//
// Each is a real figure VERIQO reports elsewhere. The objection is not
// that they are false; it is that all of them rise when VERIQO works
// harder, and none of them rises when VERIQO becomes more trustworthy.
var Banned = map[string]string{
	"tests passed": "it rises when tests are added and cannot fall when the system " +
		"becomes less trustworthy",
	"test count":          "the same figure with a different name",
	"lines of code":       "it measures how much was written, which is a cost, not a result",
	"packages with tests": "a coverage proxy that reaches 100% and stops meaning anything",
	"verify.sh checks passed": "VERIQO writes the checks and VERIQO runs them; the " +
		"figure measures VERIQO's diligence about VERIQO",
	"gates satisfied": "it is zero and will stay zero until somebody outside acts, so " +
		"as a headline it reports nothing that changes",
	"assurance mutations killed": "it measures survival under an operator set VERIQO chose",
}

// IsHeadline reports whether a measure may lead, and why not.
func IsHeadline(name string) error {
	key := strings.ToLower(strings.TrimSpace(name))
	if why, banned := Banned[key]; banned {
		return fmt.Errorf("%w: %q -- %s", ErrNotHeadline, name, why)
	}
	return nil
}

// State is how a measure stands.
type State string

const (
	// Counted: we looked and this is the number.
	Counted State = "COUNTED"
	// NotMeasured: there is no basis to count yet. Distinct from zero.
	NotMeasured State = "NOT_MEASURED"
	// Internal: counted, but only over material VERIQO produced. The
	// number is real and its scope is a laboratory.
	Internal State = "INTERNAL_SCOPE_ONLY"
)

// Measure is one headline figure.
type Measure struct {
	// Name is what it measures, in a buyer's words.
	Name string `json:"name"`
	// State says whether this is a count, an absence of one, or a
	// count whose scope is internal.
	State State `json:"state"`
	// Numerator and Denominator. Denominator is zero when unknown, and
	// State must then be NotMeasured -- a fraction over an unknown
	// denominator is the classic dishonest metric.
	Numerator   int `json:"numerator"`
	Denominator int `json:"denominator"`
	// Scope states what the denominator is drawn from. Without it,
	// "12 of 23" is unreadable.
	Scope string `json:"scope"`
	// Blocks names what this measure gates, so a zero is legible as a
	// consequence rather than as a shortfall.
	Blocks string `json:"blocks"`
	// MovableBy names who can change it. For every measure here the
	// answer is somebody who is not VERIQO, which is the point.
	MovableBy string `json:"movable_by"`
}

func (m Measure) Validate() error {
	if strings.TrimSpace(m.Name) == "" {
		return errors.New("dashboard: a measure with no name")
	}
	if err := IsHeadline(m.Name); err != nil {
		return err
	}
	if strings.TrimSpace(m.Scope) == "" {
		return fmt.Errorf("dashboard: %s states no scope, so its denominator is "+
			"unreadable", m.Name)
	}
	if strings.TrimSpace(m.MovableBy) == "" {
		return fmt.Errorf("dashboard: %s does not say who can move it", m.Name)
	}
	switch m.State {
	case Counted, Internal:
		if m.Denominator <= 0 {
			return fmt.Errorf("dashboard: %s is counted with no denominator; a bare "+
				"numerator cannot be read", m.Name)
		}
		if m.Numerator > m.Denominator {
			return fmt.Errorf("dashboard: %s is %d of %d", m.Name, m.Numerator, m.Denominator)
		}
	case NotMeasured:
		if m.Denominator != 0 || m.Numerator != 0 {
			return fmt.Errorf("dashboard: %s is NOT_MEASURED and carries figures; that "+
				"reads as a measurement", m.Name)
		}
	default:
		return fmt.Errorf("dashboard: %s: unknown state %q", m.Name, m.State)
	}
	if m.Numerator < 0 || m.Denominator < 0 {
		return fmt.Errorf("dashboard: %s has a negative figure", m.Name)
	}
	return nil
}

// Figure renders the measure's number the way it should be read.
func (m Measure) Figure() string {
	switch m.State {
	case NotMeasured:
		return "NOT MEASURED"
	case Internal:
		return fmt.Sprintf("%d / %d (internal scope)", m.Numerator, m.Denominator)
	}
	return fmt.Sprintf("%d / %d", m.Numerator, m.Denominator)
}

// Board is the headline.
type Board struct {
	Measures []Measure `json:"measures"`
}

func NewBoard(ms ...Measure) (*Board, error) {
	if len(ms) == 0 {
		return nil, errors.New("dashboard: an empty board")
	}
	seen := map[string]bool{}
	for _, m := range ms {
		if err := m.Validate(); err != nil {
			return nil, err
		}
		if seen[m.Name] {
			return nil, fmt.Errorf("dashboard: %s appears twice", m.Name)
		}
		seen[m.Name] = true
	}
	return &Board{Measures: ms}, nil
}

// SelfMovable returns measures VERIQO could move alone. The board is
// designed so this is empty, and a test holds it that way: a headline
// containing anything VERIQO can raise by working harder will, in
// time, be raised by working harder.
func (b *Board) SelfMovable() []Measure {
	var out []Measure
	for _, m := range b.Measures {
		if strings.Contains(strings.ToUpper(m.MovableBy), "VERIQO ALONE") {
			out = append(out, m)
		}
	}
	return out
}

// Zeroes returns the measures that are counted and zero.
func (b *Board) Zeroes() []Measure {
	var out []Measure
	for _, m := range b.Measures {
		if m.State != NotMeasured && m.Numerator == 0 {
			out = append(out, m)
		}
	}
	return out
}

// Unmeasured returns the measures with no basis to count.
func (b *Board) Unmeasured() []Measure {
	var out []Measure
	for _, m := range b.Measures {
		if m.State == NotMeasured {
			out = append(out, m)
		}
	}
	return out
}

func (b *Board) Report() string {
	var sb strings.Builder
	sb.WriteString("HEADLINE\n")
	sb.WriteString("  Nine measures. Not one of them is the test count, and not one of\n")
	sb.WriteString("  them can be moved by VERIQO working harder.\n\n")
	for _, m := range b.Measures {
		fmt.Fprintf(&sb, "  %-42s %s\n", m.Name, m.Figure())
		sb.WriteString(wrap("      scope:    ", m.Scope))
		sb.WriteString(wrap("      blocks:   ", m.Blocks))
		sb.WriteString(wrap("      moved by: ", m.MovableBy))
		sb.WriteString("\n")
	}
	z, u := b.Zeroes(), b.Unmeasured()
	fmt.Fprintf(&sb, "  %d of %d measures read zero; %d have no basis to count yet.\n",
		len(z), len(b.Measures), len(u))
	sb.WriteString(wrap("  ", "NOT MEASURED is not zero. Zero means we looked and found "+
		"none. NOT MEASURED means there is no denominator, because the thing that "+
		"would supply one -- a corpus, a deployment, an assessor -- does not exist "+
		"yet. Rendering them alike would be this board committing the error the "+
		"rest of the system refuses."))
	sb.WriteString("\n")
	sb.WriteString(wrap("  ", "The test count is deliberately absent. It is 37 and it rises "+
		"when tests are added, which means a team managing to this board would add "+
		"tests. Every figure above requires a party that is not VERIQO, and that is "+
		"the only kind of figure worth leading with now."))
	return sb.String()
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
