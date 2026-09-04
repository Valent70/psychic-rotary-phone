// Package uncertainty is the VERIQO confidence vector.
//
// # Why not confidence = 0.87
//
// The specification is blunt: that is too simple. A single number
// answers a question nobody asked. What a reader needs is:
//
//	Overall: SUPPORTED
//	but: identity = HIGH, measurement = MEDIUM,
//	     causality = LOW, completeness = LOW
//
// The two are not the same statement compressed differently. A 0.87
// hides which of the nine dimensions is the weak one, and the weak one
// is what a reviewer must act on. Worse, it lets a strong identity
// confidence carry a weak causal one, which is precisely the
// substitution that makes a plausible narrative look established.
//
// # There is deliberately no Overall() method
//
// Not "there is one and you should not use it". There is none. Any
// combining function would be used, and the first time it produced a
// pleasing number the vector would become decoration. The nearest
// thing offered is Weakest(), which names the dimension a reader
// should look at first -- a pointer, not a summary.
package uncertainty

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	ErrUnknownDimension = errors.New("uncertainty: unknown dimension")
	ErrUnknownLevel     = errors.New("uncertainty: unknown level")
	ErrIncomplete       = errors.New("uncertainty: not every dimension was materialised")
	ErrNoBasis          = errors.New("uncertainty: an assessed dimension must state its basis")
)

// Dimension is one axis of confidence.
type Dimension string

const (
	Identity     Dimension = "IDENTITY"
	Temporal     Dimension = "TEMPORAL"
	Spatial      Dimension = "SPATIAL"
	Source       Dimension = "SOURCE"
	Measurement  Dimension = "MEASUREMENT"
	Causal       Dimension = "CAUSAL"
	Completeness Dimension = "EVIDENCE_COMPLETENESS"
	Independence Dimension = "INDEPENDENCE"
	Model        Dimension = "MODEL"
)

// Dimensions returns all nine, in a fixed order.
func Dimensions() []Dimension {
	return []Dimension{Identity, Temporal, Spatial, Source, Measurement,
		Causal, Completeness, Independence, Model}
}

func (d Dimension) Valid() bool {
	for _, x := range Dimensions() {
		if x == d {
			return true
		}
	}
	return false
}

// Question states what each dimension asks, so a report can be read by
// somebody who has not seen this file.
func (d Dimension) Question() string {
	switch d {
	case Identity:
		return "are the entities the ones we think they are?"
	case Temporal:
		return "do we know when this happened, closely enough to matter?"
	case Spatial:
		return "do we know where, closely enough to matter?"
	case Source:
		return "how much weight do the sources themselves bear?"
	case Measurement:
		return "are the measurements sound and on comparable bases?"
	case Causal:
		return "do we know that this caused that, rather than that both occurred?"
	case Completeness:
		return "how much of the relevant evidence do we have?"
	case Independence:
		return "do the agreeing sources agree independently?"
	case Model:
		return "how much does the conclusion depend on a model's behaviour?"
	}
	return ""
}

// Level is a coarse confidence band.
//
// It is coarse on purpose. Finer gradations invite arithmetic, and
// arithmetic on these is what produces 0.87.
type Level string

const (
	// NotAssessed is the zero value: nobody looked at this dimension.
	NotAssessed Level = ""
	High        Level = "HIGH"
	Medium      Level = "MEDIUM"
	Low         Level = "LOW"
	// None: the dimension was assessed and there is no confidence at
	// all. Distinct from NOT_ASSESSED, which is an absence of work.
	None Level = "NONE"
)

func Levels() []Level { return []Level{High, Medium, Low, None, NotAssessed} }

func (l Level) Valid() bool {
	for _, x := range Levels() {
		if x == l {
			return true
		}
	}
	return false
}

func (l Level) String() string {
	if l == NotAssessed {
		return "NOT_ASSESSED"
	}
	return string(l)
}

func (l Level) Assessed() bool { return l != NotAssessed }

// Rank orders the bands for comparison. NOT_ASSESSED ranks BELOW
// NONE, because "we did not look" is a worse position than "we looked
// and found nothing" -- one of them can be improved by acquiring
// evidence and the other by doing the work.
func (l Level) Rank() int {
	switch l {
	case High:
		return 3
	case Medium:
		return 2
	case Low:
		return 1
	case None:
		return 0
	}
	return -1
}

// Judgement is one dimension's assessment.
type Judgement struct {
	Dimension Dimension `json:"dimension"`
	Level     Level     `json:"level"`
	// Basis is why. An assessed dimension with no basis is a number
	// somebody chose.
	Basis string `json:"basis,omitempty"`
}

func (j Judgement) Validate() error {
	if !j.Dimension.Valid() {
		return fmt.Errorf("%w: %q", ErrUnknownDimension, j.Dimension)
	}
	if !j.Level.Valid() {
		return fmt.Errorf("%w: %q", ErrUnknownLevel, j.Level)
	}
	if j.Level.Assessed() && strings.TrimSpace(j.Basis) == "" {
		return fmt.Errorf("%w: %s is %s", ErrNoBasis, j.Dimension, j.Level)
	}
	if !j.Level.Assessed() && strings.TrimSpace(j.Basis) != "" {
		// The mutation the suite attacks: attaching a reason to an
		// unasked question so the vector reads as though somebody
		// looked.
		return fmt.Errorf("uncertainty: %s is NOT_ASSESSED and carries a basis %q; "+
			"an absence of work is not a determination with a reason",
			j.Dimension, j.Basis)
	}
	return nil
}

// Vector is the nine-dimensional assessment.
//
// Every dimension is materialised, defaulting to NOT_ASSESSED. An
// omitted key and an unassessed one look identical in a map, and only
// one of them is a determination.
type Vector struct {
	SubjectID  string                  `json:"subject_id"`
	Judgements map[Dimension]Judgement `json:"judgements"`
}

// New builds a vector, materialising every dimension.
func New(subjectID string, js ...Judgement) (Vector, error) {
	if strings.TrimSpace(subjectID) == "" {
		return Vector{}, errors.New("uncertainty: a vector must name its subject")
	}
	v := Vector{SubjectID: subjectID, Judgements: map[Dimension]Judgement{}}
	for _, d := range Dimensions() {
		v.Judgements[d] = Judgement{Dimension: d, Level: NotAssessed}
	}
	for _, j := range js {
		if err := j.Validate(); err != nil {
			return Vector{}, err
		}
		v.Judgements[j.Dimension] = j
	}
	return v, nil
}

// Validate checks every dimension is present and well-formed.
func (v Vector) Validate() error {
	if len(v.Judgements) != len(Dimensions()) {
		return fmt.Errorf("%w: %d of %d present", ErrIncomplete,
			len(v.Judgements), len(Dimensions()))
	}
	for _, d := range Dimensions() {
		j, ok := v.Judgements[d]
		if !ok {
			return fmt.Errorf("%w: %s is absent", ErrIncomplete, d)
		}
		if err := j.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Level returns one dimension's band.
func (v Vector) Level(d Dimension) Level { return v.Judgements[d].Level }

// Weakest names the dimension a reader should look at first.
//
// It is a POINTER, not a summary. It deliberately does not combine
// anything: it answers "where is this argument thinnest", which is the
// question a reviewer actually has.
func (v Vector) Weakest() (Dimension, Level) {
	worst, level := Dimension(""), Level(High)
	for _, d := range Dimensions() {
		l := v.Judgements[d].Level
		if worst == "" || l.Rank() < level.Rank() {
			worst, level = d, l
		}
	}
	return worst, level
}

// Unassessed names the dimensions nobody looked at.
func (v Vector) Unassessed() []Dimension {
	var out []Dimension
	for _, d := range Dimensions() {
		if !v.Judgements[d].Level.Assessed() {
			out = append(out, d)
		}
	}
	return out
}

// Weak names the dimensions assessed LOW or NONE. These are the ones a
// conclusion has to be qualified against.
func (v Vector) Weak() []Dimension {
	var out []Dimension
	for _, d := range Dimensions() {
		if l := v.Judgements[d].Level; l == Low || l == None {
			out = append(out, d)
		}
	}
	return out
}

// Complete reports whether every dimension was assessed.
func (v Vector) Complete() bool { return len(v.Unassessed()) == 0 }

// Qualifications renders the sentences a conclusion must carry.
//
// This is what makes the vector operational rather than informational:
// a finding built on this vector cannot be stated without these
// clauses, because Limitations() returns them and the findings package
// requires them.
func (v Vector) Qualifications() []string {
	var out []string
	for _, d := range v.Weak() {
		j := v.Judgements[d]
		out = append(out, fmt.Sprintf("%s confidence is %s: %s (%s)",
			strings.ToLower(strings.ReplaceAll(string(d), "_", " ")),
			j.Level, j.Basis, d.Question()))
	}
	for _, d := range v.Unassessed() {
		out = append(out, fmt.Sprintf("%s was NOT ASSESSED: %s -- this is an absence of "+
			"work, not a finding of low confidence",
			strings.ToLower(strings.ReplaceAll(string(d), "_", " ")), d.Question()))
	}
	sort.Strings(out)
	return out
}

// Report renders the vector.
func (v Vector) Report() string {
	var b strings.Builder
	fmt.Fprintf(&b, "CONFIDENCE VECTOR for %s\n", v.SubjectID)
	for _, d := range Dimensions() {
		j := v.Judgements[d]
		fmt.Fprintf(&b, "  %-22s %-12s %s\n", d, j.Level, j.Basis)
	}
	w, l := v.Weakest()
	fmt.Fprintf(&b, "  weakest dimension: %s (%s)\n", w, l)
	if !v.Complete() {
		fmt.Fprintf(&b, "  NOT ASSESSED: %s\n", joinDims(v.Unassessed()))
	}
	b.WriteString("  There is no overall number. A single figure would let a strong " +
		"dimension carry a weak one, which is the substitution this vector exists to prevent.\n")
	return b.String()
}

func joinDims(ds []Dimension) string {
	s := make([]string, len(ds))
	for i, d := range ds {
		s[i] = string(d)
	}
	return strings.Join(s, ", ")
}

// Dominates reports whether v is at least as confident as o on EVERY
// dimension.
//
// It is a partial order, and that is the point: two vectors are often
// incomparable, and a comparison that always returned an answer would
// be the overall score by another route.
func (v Vector) Dominates(o Vector) bool {
	for _, d := range Dimensions() {
		if v.Judgements[d].Level.Rank() < o.Judgements[d].Level.Rank() {
			return false
		}
	}
	return true
}

// Comparable reports whether two vectors can be ordered at all.
func Comparable(a, b Vector) bool { return a.Dominates(b) || b.Dominates(a) }
