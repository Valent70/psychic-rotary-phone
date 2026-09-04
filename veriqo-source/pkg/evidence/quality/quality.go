// Package quality is the VERIQO evidence quality model.
//
// # The gap this closes
//
// The chain Control -> Evidence -> Validator says who checked what. It
// says nothing about whether the evidence was any good. The review put
// it precisely:
//
//	evidence sendiri harus punya quality attributes.
//
// Without them "evidence exists" and "evidence supports the
// conclusion" are the same sentence, and every qualification decision
// rests on an unstated judgement about which one was meant.
//
// # The nine attributes
//
// Each is a separate question with a separate failure mode, and each
// can be strong while another is absent. That independence is the whole
// point: a document with impeccable INTEGRITY and no INDEPENDENCE is a
// well-preserved copy of one party's assertion.
//
//	AUTHENTICITY     is it what it claims to be?
//	INTEGRITY        is it unaltered since acquisition?
//	PROVENANCE       do we know where it came from and how?
//	COMPLETENESS     is any of it missing, and do we know what?
//	INDEPENDENCE     did it reach us without passing through an
//	                 interested party?
//	FRESHNESS        does it still describe the state it is offered for?
//	REPRODUCIBILITY  can somebody else obtain the same result?
//	SCOPE            does it cover the question being asked?
//	AUTHORITY        was the source entitled to say it?
//
// # What this package refuses to do
//
// It does not produce a score. A single number would let a strong
// INTEGRITY offset an absent INDEPENDENCE, and those do not trade
// against each other: no amount of tamper-evidence makes a
// party-mediated survey independent. The assessment is a vector, and
// the qualification decision reads the vector.
package quality

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Attribute is one dimension of evidence quality.
type Attribute string

const (
	Authenticity    Attribute = "AUTHENTICITY"
	Integrity       Attribute = "INTEGRITY"
	Provenance      Attribute = "PROVENANCE"
	Completeness    Attribute = "COMPLETENESS"
	Independence    Attribute = "INDEPENDENCE"
	Freshness       Attribute = "FRESHNESS"
	Reproducibility Attribute = "REPRODUCIBILITY"
	Scope           Attribute = "SCOPE"
	Authority       Attribute = "AUTHORITY"
)

// Attributes returns the nine, in the order above.
func Attributes() []Attribute {
	return []Attribute{
		Authenticity, Integrity, Provenance, Completeness, Independence,
		Freshness, Reproducibility, Scope, Authority,
	}
}

// Question states what the attribute asks.
func (a Attribute) Question() string {
	switch a {
	case Authenticity:
		return "is it what it claims to be?"
	case Integrity:
		return "is it unaltered since acquisition?"
	case Provenance:
		return "do we know where it came from and how?"
	case Completeness:
		return "is any of it missing, and do we know what?"
	case Independence:
		return "did it reach us without passing through an interested party?"
	case Freshness:
		return "does it still describe the state it is offered for?"
	case Reproducibility:
		return "can somebody else obtain the same result?"
	case Scope:
		return "does it cover the question being asked?"
	case Authority:
		return "was the source entitled to say it?"
	}
	return ""
}

// Known reports whether the attribute is one of the nine.
func (a Attribute) Known() bool {
	for _, k := range Attributes() {
		if k == a {
			return true
		}
	}
	return false
}

// Grade is one attribute's assessed level.
//
// There are four, and the fourth is the one that matters: NOT_ASSESSED
// is distinct from WEAK. "We looked and it is poor" and "we never
// looked" lead to different next actions, and collapsing them is how a
// gap becomes a finding.
type Grade string

const (
	// NotAssessed is the zero value.
	NotAssessed Grade = ""
	// Strong: the attribute holds and the basis is recorded.
	Strong Grade = "STRONG"
	// Adequate: it holds well enough for the question, with stated
	// limits.
	Adequate Grade = "ADEQUATE"
	// Weak: it was assessed and found wanting.
	Weak Grade = "WEAK"
	// Absent: it was assessed and does not hold at all.
	Absent Grade = "ABSENT"
)

// Grades returns the five, strongest first.
func Grades() []Grade { return []Grade{Strong, Adequate, Weak, Absent, NotAssessed} }

func (g Grade) String() string {
	if g == NotAssessed {
		return "NOT_ASSESSED"
	}
	return string(g)
}

// Known reports whether the grade is one of the five.
func (g Grade) Known() bool {
	switch g {
	case NotAssessed, Strong, Adequate, Weak, Absent:
		return true
	}
	return false
}

// Sufficient reports whether the grade supports relying on the
// attribute. NotAssessed is deliberately not sufficient: an unasked
// question has no answer.
func (g Grade) Sufficient() bool { return g == Strong || g == Adequate }

var (
	ErrUnknownAttribute = errors.New("quality: not one of the nine evidence quality attributes")
	ErrUnknownGrade     = errors.New("quality: not one of the five grades")
	ErrNoBasis          = errors.New("quality: an assessed attribute must state its basis")
	ErrBasisWithoutWork = errors.New("quality: a NOT_ASSESSED attribute may not state a basis")
	ErrIncomplete       = errors.New("quality: every one of the nine attributes must be present")
	ErrNoSubject        = errors.New("quality: an assessment must name the evidence it assesses")
)

// Judgement is one attribute's assessment.
type Judgement struct {
	Attribute Attribute
	Grade     Grade
	// Basis is why. Required for every grade except NotAssessed, and
	// forbidden for it -- a reason attached to an unasked question is
	// how "we never checked" comes to read as "we checked".
	Basis string
	// Limits states what the grade does NOT cover. Required for
	// Adequate, which is the grade most often read as Strong.
	Limits string
}

// Validate refuses a judgement that could not be acted on.
func (j Judgement) Validate() error {
	if !j.Attribute.Known() {
		return fmt.Errorf("%w: %q", ErrUnknownAttribute, string(j.Attribute))
	}
	if !j.Grade.Known() {
		return fmt.Errorf("%w: %q", ErrUnknownGrade, string(j.Grade))
	}
	basis := strings.TrimSpace(j.Basis)
	if j.Grade == NotAssessed {
		if basis != "" {
			return fmt.Errorf("%w: %s", ErrBasisWithoutWork, j.Attribute)
		}
		return nil
	}
	if basis == "" {
		return fmt.Errorf("%w: %s is %s", ErrNoBasis, j.Attribute, j.Grade)
	}
	if j.Grade == Adequate && strings.TrimSpace(j.Limits) == "" {
		return fmt.Errorf("quality: %s is ADEQUATE and states no limits; ADEQUATE without limits "+
			"is read as STRONG by every reader who is in a hurry", j.Attribute)
	}
	return nil
}

// Assessment is the full nine-attribute vector for one piece of
// evidence.
type Assessment struct {
	// EvidenceVersionID names what is assessed.
	EvidenceVersionID string
	Judgements        map[Attribute]Judgement
}

// New builds an assessment, defaulting every unstated attribute to
// NotAssessed rather than omitting it.
//
// Omission and non-assessment look identical in a map, and only one of
// them is a determination. Materialising all nine means a reader always
// sees the shape of what was not done.
func New(evidenceVersionID string, judgements ...Judgement) (Assessment, error) {
	a := Assessment{
		EvidenceVersionID: strings.TrimSpace(evidenceVersionID),
		Judgements:        map[Attribute]Judgement{},
	}
	if a.EvidenceVersionID == "" {
		return Assessment{}, ErrNoSubject
	}
	for _, attr := range Attributes() {
		a.Judgements[attr] = Judgement{Attribute: attr, Grade: NotAssessed}
	}
	for _, j := range judgements {
		if err := j.Validate(); err != nil {
			return Assessment{}, err
		}
		if !j.Attribute.Known() {
			return Assessment{}, fmt.Errorf("%w: %q", ErrUnknownAttribute, string(j.Attribute))
		}
		a.Judgements[j.Attribute] = j
	}
	return a, nil
}

// Validate checks the assessment is complete and internally sound.
func (a Assessment) Validate() error {
	if strings.TrimSpace(a.EvidenceVersionID) == "" {
		return ErrNoSubject
	}
	for _, attr := range Attributes() {
		j, ok := a.Judgements[attr]
		if !ok {
			return fmt.Errorf("%w: %s is missing", ErrIncomplete, attr)
		}
		if err := j.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// NotAssessedAttributes returns what nobody looked at. A non-empty
// result is the finding.
func (a Assessment) NotAssessedAttributes() []Attribute {
	var out []Attribute
	for _, attr := range Attributes() {
		if a.Judgements[attr].Grade == NotAssessed {
			out = append(out, attr)
		}
	}
	return out
}

// Deficient returns the attributes assessed and found wanting.
func (a Assessment) Deficient() []Attribute {
	var out []Attribute
	for _, attr := range Attributes() {
		if g := a.Judgements[attr].Grade; g == Weak || g == Absent {
			out = append(out, attr)
		}
	}
	return out
}

// Decision is what the quality vector permits.
type Decision string

const (
	// Supports: every attribute is sufficient. The evidence may found
	// a conclusion.
	Supports Decision = "SUPPORTS"
	// SupportsWithLimits: sufficient, with at least one ADEQUATE
	// attribute whose limits travel with the conclusion.
	SupportsWithLimits Decision = "SUPPORTS_WITH_LIMITS"
	// Insufficient: one or more attributes were assessed and found
	// wanting.
	Insufficient Decision = "INSUFFICIENT"
	// Unassessable: one or more attributes were never assessed. This
	// is NOT the same as insufficient, and separating them is the
	// point: one needs better evidence, the other needs somebody to
	// look.
	Unassessable Decision = "UNASSESSABLE"
)

// Decide reads the vector.
//
// The order is the argument. Unassessable is checked FIRST, because a
// vector with unasked questions cannot be called insufficient -- that
// would be a conclusion drawn from an absence of work, which is the
// error this whole package exists to prevent.
func (a Assessment) Decide() (Decision, string, error) {
	if err := a.Validate(); err != nil {
		return Unassessable, "", err
	}
	if missing := a.NotAssessedAttributes(); len(missing) > 0 {
		return Unassessable, fmt.Sprintf(
			"%d attribute(s) were never assessed: %s. This is not a judgement that the evidence "+
				"is poor; it is a statement that nobody looked.", len(missing), names(missing)), nil
	}
	if bad := a.Deficient(); len(bad) > 0 {
		return Insufficient, fmt.Sprintf(
			"assessed and found wanting on: %s", names(bad)), nil
	}
	var limited []Attribute
	for _, attr := range Attributes() {
		if a.Judgements[attr].Grade == Adequate {
			limited = append(limited, attr)
		}
	}
	if len(limited) > 0 {
		return SupportsWithLimits, fmt.Sprintf(
			"every attribute is sufficient; %s are ADEQUATE and their limits travel with any "+
				"conclusion drawn from this evidence", names(limited)), nil
	}
	return Supports, "every attribute is STRONG", nil
}

// Report renders the vector.
func (a Assessment) Report() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Evidence quality: %s\n", a.EvidenceVersionID)
	b.WriteString("Nine independent attributes. There is no score: a strong INTEGRITY does not\n")
	b.WriteString("offset an absent INDEPENDENCE, because those do not trade against each other.\n\n")
	for _, attr := range Attributes() {
		j := a.Judgements[attr]
		fmt.Fprintf(&b, "  %-16s %-12s %s\n", attr, j.Grade, attr.Question())
		if j.Basis != "" {
			fmt.Fprintf(&b, "                   basis:  %s\n", j.Basis)
		}
		if j.Limits != "" {
			fmt.Fprintf(&b, "                   limits: %s\n", j.Limits)
		}
	}
	d, why, err := a.Decide()
	if err != nil {
		fmt.Fprintf(&b, "\nthe assessment does not validate: %v\n", err)
		return b.String()
	}
	fmt.Fprintf(&b, "\n%s -- %s\n", d, why)
	return b.String()
}

func names(attrs []Attribute) string {
	out := make([]string, len(attrs))
	for i, a := range attrs {
		out[i] = string(a)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}
