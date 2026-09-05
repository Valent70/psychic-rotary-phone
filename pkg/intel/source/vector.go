package source

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// The Source Trust Vector.
//
// # Why not a number
//
//	source confidence = 0.8
//
// That figure is the single most damaging simplification available in
// this domain, because it is arrived at by averaging answers to
// questions that are not commensurable. A source whose ATTRIBUTION is
// unknown and whose AUTHENTICITY is verified is not "somewhat
// trustworthy": it is a document we can prove is genuine and cannot
// say who wrote. That is a specific situation with specific
// consequences, and 0.8 erases it.
//
// Worse, the number is directional in a way its users forget. A
// weakness in LEGALITY cannot be compensated by strength in
// TIMELINESS -- one is a question about whether the material may be
// held at all, and the other is about whether it is current. Averaging
// them produces a figure in which a lawyer's objection is offset by a
// fresh timestamp.
//
// So the vector has nine dimensions, each with its own scale, and
// there is deliberately no function that combines them. Callers that
// need a decision get Weakest() -- which returns a DIMENSION, not a
// number, because the answer to "how much can I trust this" is a name.
type Dimension string

const (
	// Attribution: do we know who produced it?
	Attribution Dimension = "ATTRIBUTION"
	// Provenance: do we know the path it took to reach us?
	Provenance Dimension = "PROVENANCE"
	// Legality: may we hold and use it?
	Legality Dimension = "LEGALITY"
	// Independence: is it a separate observation from our others?
	Independence Dimension = "INDEPENDENCE"
	// Authenticity: is it what it purports to be?
	Authenticity Dimension = "AUTHENTICITY"
	// Timeliness: does it describe the period we care about?
	Timeliness Dimension = "TIMELINESS"
	// Corroboration: does anything else support it?
	Corroboration Dimension = "CORROBORATION"
	// Rights: what may we do with it downstream?
	Rights Dimension = "RIGHTS"
	// ReliabilityHistory: has this producer been right before?
	ReliabilityHistory Dimension = "RELIABILITY_HISTORY"
)

// Dimensions returns all nine in a fixed order.
func Dimensions() []Dimension {
	return []Dimension{Attribution, Provenance, Legality, Independence, Authenticity,
		Timeliness, Corroboration, Rights, ReliabilityHistory}
}

func (d Dimension) Valid() bool {
	for _, k := range Dimensions() {
		if k == d {
			return true
		}
	}
	return false
}

// Question is what the dimension asks, so a reader can tell whether it
// is the one they care about.
func (d Dimension) Question() string {
	switch d {
	case Attribution:
		return "do we know who produced it?"
	case Provenance:
		return "do we know the path it took to reach us?"
	case Legality:
		return "may we hold and use it?"
	case Independence:
		return "is it a separate observation from our others?"
	case Authenticity:
		return "is it what it purports to be?"
	case Timeliness:
		return "does it describe the period we care about?"
	case Corroboration:
		return "does anything else support it?"
	case Rights:
		return "what may we do with it downstream?"
	case ReliabilityHistory:
		return "has this producer been right before?"
	}
	return ""
}

// Disqualifying reports whether a non-supporting answer on this
// dimension stops the source being used at all, rather than merely
// weakening it.
//
// Only LEGALITY. The others make a source weaker; legality makes it
// unusable, and no amount of strength elsewhere changes that. Marking
// more dimensions disqualifying would make the vector unusable;
// marking fewer would let a lawyer's objection be outvoted.
func (d Dimension) Disqualifying() bool { return d == Legality }

// Grade is one dimension's answer.
//
// The scale is deliberately coarse and deliberately not numeric. Three
// named answers cannot be averaged, and the middle one is not "half".
type Grade int

const (
	// NotAssessed: nobody has looked at this dimension. The zero
	// value, so an unpopulated vector claims nothing.
	NotAssessed Grade = iota
	// Unknown: it was looked at and cannot be determined. Distinct
	// from NotAssessed: somebody tried.
	Unknown
	// Partial: partly established, with a stated remainder.
	Partial
	// Confirmed: answered, affirmatively.
	Confirmed
	// Adverse: answered, and the answer is bad. Not the bottom of the
	// scale -- it is more informative than Unknown, and it is the
	// answer a reader most needs.
	Adverse
)

var gradeNames = map[Grade]string{
	NotAssessed: "NOT_ASSESSED", Unknown: "UNKNOWN", Partial: "PARTIAL",
	Confirmed: "CONFIRMED", Adverse: "ADVERSE",
}

func (g Grade) String() string {
	if n, ok := gradeNames[g]; ok {
		return n
	}
	return fmt.Sprintf("Grade(%d)", int(g))
}

func (g Grade) MarshalJSON() ([]byte, error) { return []byte(`"` + g.String() + `"`), nil }

func (g Grade) Valid() bool { _, ok := gradeNames[g]; return ok }

// Grades returns every grade in a fixed order.
func Grades() []Grade {
	out := make([]Grade, 0, len(gradeNames))
	for g := range gradeNames {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Supports reports whether this grade lets the dimension support a
// claim. ADVERSE does not, and neither does anything below PARTIAL.
func (g Grade) Supports() bool { return g == Partial || g == Confirmed }

// Answered reports whether anybody looked.
func (g Grade) Answered() bool { return g != NotAssessed }

// Reading is one dimension's grade with its reason.
type Reading struct {
	Dimension Dimension `json:"dimension"`
	Grade     Grade     `json:"grade"`
	// Basis says how the grade was reached. Required, because a grade
	// nobody can argue with is a grade nobody checked.
	Basis string `json:"basis"`
	// Remainder is what is still missing, for PARTIAL. Without it,
	// PARTIAL is indistinguishable from a shrug.
	Remainder string `json:"remainder,omitempty"`
}

func (r Reading) Validate() error {
	if !r.Dimension.Valid() {
		return fmt.Errorf("intel/source: unknown trust dimension %q", r.Dimension)
	}
	if !r.Grade.Valid() {
		return fmt.Errorf("intel/source: %s has unknown grade %v", r.Dimension, r.Grade)
	}
	if r.Grade.Answered() && strings.TrimSpace(r.Basis) == "" {
		return fmt.Errorf("intel/source: %s is %s and states no basis; a grade nobody can "+
			"argue with is a grade nobody checked", r.Dimension, r.Grade)
	}
	if r.Grade == Partial && strings.TrimSpace(r.Remainder) == "" {
		return fmt.Errorf("intel/source: %s is PARTIAL and does not say what is missing. "+
			"Without that, PARTIAL is indistinguishable from a shrug", r.Dimension)
	}
	return nil
}

var ErrIncomplete = errors.New("intel/source: the trust vector omits a dimension")

// Vector is a source's profile across all nine dimensions.
//
// There is no Score, no Overall and no Combine. The type exists to
// make the absence structural rather than a convention.
type Vector struct {
	SourceID string                `json:"source_id"`
	Readings map[Dimension]Reading `json:"readings"`
}

// NewVector requires every dimension.
//
// An omitted dimension is not "not applicable": it is one nobody
// looked at, and its absence from a profile is exactly how a weakness
// disappears. NOT_ASSESSED must be written deliberately.
func NewVector(sourceID string, rs ...Reading) (*Vector, error) {
	if strings.TrimSpace(sourceID) == "" {
		return nil, errors.New("intel/source: a trust vector names no source")
	}
	v := &Vector{SourceID: sourceID, Readings: map[Dimension]Reading{}}
	for _, r := range rs {
		if err := r.Validate(); err != nil {
			return nil, err
		}
		if _, dup := v.Readings[r.Dimension]; dup {
			return nil, fmt.Errorf("intel/source: %s graded twice for %s",
				r.Dimension, sourceID)
		}
		v.Readings[r.Dimension] = r
	}
	var missing []string
	for _, d := range Dimensions() {
		if _, ok := v.Readings[d]; !ok {
			missing = append(missing, string(d))
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w: %s for %s. NOT_ASSESSED must be written deliberately, "+
			"because an omitted dimension is how a weakness disappears",
			ErrIncomplete, strings.Join(missing, ", "), sourceID)
	}
	return v, nil
}

// Weakest returns the dimension in the worst state, and its grade.
//
// It returns a DIMENSION rather than a number for the same reason
// uncertainty.Weakest does: the answer to "how much can I trust this"
// is a name, and the name tells you what to do next.
//
// ADVERSE outranks NOT_ASSESSED as the weakest, because a bad answer
// is worse than no answer for the purpose of relying on the source --
// even though it is more informative.
func (v *Vector) Weakest() (Dimension, Grade) {
	rank := func(g Grade) int {
		switch g {
		case Adverse:
			return 0
		case NotAssessed:
			return 1
		case Unknown:
			return 2
		case Partial:
			return 3
		}
		return 4
	}
	worst := 5
	var which Dimension
	for _, d := range Dimensions() {
		if r := rank(v.Readings[d].Grade); r < worst {
			worst, which = r, d
		}
	}
	return which, v.Readings[which].Grade
}

// Usable reports whether the source may be used at all, and says why
// not.
//
// Only LEGALITY can make a source unusable. Everything else weakens
// it, and weakness is for a human to weigh.
func (v *Vector) Usable() error {
	for _, d := range Dimensions() {
		if !d.Disqualifying() {
			continue
		}
		g := v.Readings[d].Grade
		if g.Supports() {
			continue
		}
		return fmt.Errorf("intel/source: %s is %s on %s. No strength on another dimension "+
			"compensates: this is a question about whether the material may be held at "+
			"all", v.SourceID, g, d)
	}
	return nil
}

// Unanswered returns the dimensions nobody has looked at.
func (v *Vector) Unanswered() []Dimension {
	var out []Dimension
	for _, d := range Dimensions() {
		if !v.Readings[d].Grade.Answered() {
			out = append(out, d)
		}
	}
	return out
}

// Adverse returns the dimensions whose answer is bad. They are the
// ones a reader most needs, and they are easy to lose in a table.
func (v *Vector) Adverse() []Dimension {
	var out []Dimension
	for _, d := range Dimensions() {
		if v.Readings[d].Grade == Adverse {
			out = append(out, d)
		}
	}
	return out
}

// Render writes the vector.
func (v *Vector) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "SOURCE PROFILE  %s\n", v.SourceID)
	b.WriteString("  no combined score: these questions are not commensurable, and a\n")
	b.WriteString("  weakness in LEGALITY is not offset by strength in TIMELINESS\n\n")
	for _, d := range Dimensions() {
		r := v.Readings[d]
		fmt.Fprintf(&b, "  %-20s %-12s %s\n", d, r.Grade, r.Basis)
		if r.Remainder != "" {
			fmt.Fprintf(&b, "  %-20s %-12s still missing: %s\n", "", "", r.Remainder)
		}
	}
	w, g := v.Weakest()
	fmt.Fprintf(&b, "\n  weakest dimension: %s (%s) -- %s\n", w, g, w.Question())
	if adv := v.Adverse(); len(adv) > 0 {
		var ns []string
		for _, d := range adv {
			ns = append(ns, string(d))
		}
		fmt.Fprintf(&b, "  ADVERSE findings on: %s\n", strings.Join(ns, ", "))
	}
	if un := v.Unanswered(); len(un) > 0 {
		var ns []string
		for _, d := range un {
			ns = append(ns, string(d))
		}
		fmt.Fprintf(&b, "  nobody has looked at: %s\n", strings.Join(ns, ", "))
	}
	if err := v.Usable(); err != nil {
		fmt.Fprintf(&b, "  NOT USABLE: %v\n", err)
	}
	return b.String()
}
