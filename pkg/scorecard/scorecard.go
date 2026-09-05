// Package scorecard is the enterprise qualification scorecard.
//
// # Why not READY / NOT READY
//
// The specification refuses a single verdict, and the reason is that
// one bit cannot carry a decision somebody has to make. A system with
// excellent evidence integrity and no external validation is in a
// completely different position from one with weak evidence integrity
// and a clean pentest, and both are "NOT READY".
//
// So there are nine dimensions, each GREEN, YELLOW or RED, and the
// release rule is:
//
//	NO RED, AND every mandatory gate GREEN
//
// # Why there is no aggregate score
//
// Same reason as the uncertainty vector: a number would let a strong
// dimension carry a weak one, and the weak one is what a customer
// needs to see. The scorecard's only aggregation is the release rule,
// which is a conjunction rather than an average.
//
// # YELLOW is the honest majority state
//
// GREEN means the dimension is qualified. YELLOW means it is
// implemented and not externally qualified -- which is where almost
// everything in an honest system sits before it has met the world.
// A scorecard that showed mostly GREEN before external validation
// would be a scorecard whose GREEN meant "we tested it ourselves".
package scorecard

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"veriqo/pkg/gates"
)

var (
	ErrUnknownDimension     = errors.New("scorecard: unknown dimension")
	ErrNoBasis              = errors.New("scorecard: a rating must state its basis")
	ErrGreenWithoutExternal = errors.New("scorecard: this dimension may not be GREEN without " +
		"external qualification")
	ErrIncomplete = errors.New("scorecard: not every dimension was rated")
)

// Dimension is one axis of enterprise readiness.
type Dimension string

const (
	EvidenceIntegrity      Dimension = "EVIDENCE_INTEGRITY"
	EntityIntegrity        Dimension = "ENTITY_INTEGRITY"
	ReasoningIntegrity     Dimension = "REASONING_INTEGRITY"
	Security               Dimension = "SECURITY"
	OperationalReliability Dimension = "OPERATIONAL_RELIABILITY"
	DataRights             Dimension = "DATA_RIGHTS"
	AIGovernance           Dimension = "AI_GOVERNANCE"
	Replayability          Dimension = "REPLAYABILITY"
	ExternalValidation     Dimension = "EXTERNAL_VALIDATION"
)

// Dimensions returns all nine in a fixed order.
func Dimensions() []Dimension {
	return []Dimension{EvidenceIntegrity, EntityIntegrity, ReasoningIntegrity,
		Security, OperationalReliability, DataRights, AIGovernance,
		Replayability, ExternalValidation}
}

func (d Dimension) Valid() bool {
	for _, x := range Dimensions() {
		if x == d {
			return true
		}
	}
	return false
}

// Question states what each dimension asks.
func (d Dimension) Question() string {
	switch d {
	case EvidenceIntegrity:
		return "can we show the evidence is what it claims to be, unaltered since acquisition?"
	case EntityIntegrity:
		return "can we show that entities were not silently merged?"
	case ReasoningIntegrity:
		return "can we show that conclusions carry their contradictions and limits?"
	case Security:
		return "can we show the controls hold against somebody trying?"
	case OperationalReliability:
		return "can we show the system stays up and recovers?"
	case DataRights:
		return "can we show every use of every input is within its licence?"
	case AIGovernance:
		return "can we show no model concluded anything on its own?"
	case Replayability:
		return "can we show how any conclusion was produced?"
	case ExternalValidation:
		return "has anybody outside VERIQO examined any of it?"
	}
	return ""
}

// RequiresExternalToBeGreen reports whether a dimension is
// self-certifiable.
//
// SECURITY and EXTERNAL_VALIDATION are not: a security posture rated
// GREEN by the party it protects is a self-assessment, and
// EXTERNAL_VALIDATION rated GREEN with no external party is a
// contradiction in terms.
func (d Dimension) RequiresExternalToBeGreen() bool {
	return d == Security || d == ExternalValidation
}

// Rating is a dimension's colour.
type Rating string

const (
	// Unrated is the zero value.
	Unrated Rating = ""
	Green   Rating = "GREEN"
	Yellow  Rating = "YELLOW"
	Red     Rating = "RED"
)

func Ratings() []Rating { return []Rating{Green, Yellow, Red, Unrated} }

func (r Rating) Valid() bool {
	for _, x := range Ratings() {
		if x == r {
			return true
		}
	}
	return false
}

func (r Rating) String() string {
	if r == Unrated {
		return "UNRATED"
	}
	return string(r)
}

// Blocking reports whether this rating prevents a production release.
// RED does. UNRATED does too -- a dimension nobody assessed is not a
// dimension that passed.
func (r Rating) Blocking() bool { return r == Red || r == Unrated }

// Assessment is one dimension's rating with its reasoning.
type Assessment struct {
	Dimension Dimension `json:"dimension"`
	Rating    Rating    `json:"rating"`
	// Basis is why. A colour with no basis is a colour somebody chose.
	Basis string `json:"basis"`
	// ExternallyQualified marks a rating an outside party supports.
	ExternallyQualified bool   `json:"externally_qualified"`
	QualifiedBy         string `json:"qualified_by,omitempty"`
	// Gaps name what would move the rating. A YELLOW with no stated
	// gap is a rating nobody can act on.
	Gaps []string `json:"gaps,omitempty"`
}

func (a Assessment) Validate() error {
	if !a.Dimension.Valid() {
		return fmt.Errorf("%w: %q", ErrUnknownDimension, a.Dimension)
	}
	if !a.Rating.Valid() {
		return fmt.Errorf("scorecard: unknown rating %q", a.Rating)
	}
	if a.Rating != Unrated && strings.TrimSpace(a.Basis) == "" {
		return fmt.Errorf("%w: %s is %s", ErrNoBasis, a.Dimension, a.Rating)
	}
	if a.Rating == Green && a.Dimension.RequiresExternalToBeGreen() && !a.ExternallyQualified {
		return fmt.Errorf("%w: %s", ErrGreenWithoutExternal, a.Dimension)
	}
	if a.ExternallyQualified && strings.TrimSpace(a.QualifiedBy) == "" {
		return errors.New("scorecard: a claim of external qualification must name the party")
	}
	if a.ExternallyQualified && strings.Contains(strings.ToLower(a.QualifiedBy), "veriqo") {
		return fmt.Errorf("%w: %q qualified it", ErrGreenWithoutExternal, a.QualifiedBy)
	}
	if a.Rating == Yellow && len(a.Gaps) == 0 {
		return fmt.Errorf("scorecard: %s is YELLOW and states no gap; a rating nobody can "+
			"act on is a colour", a.Dimension)
	}
	return nil
}

// Scorecard is the nine-dimension assessment plus the gate register.
type Scorecard struct {
	assessments map[Dimension]Assessment
	gates       *gates.Register
	// MandatoryGates are the gates that must be SATISFIED for a
	// release, over and above having no RED. Naming them explicitly
	// means a deployment can require more, never fewer.
	MandatoryGates []string
}

// New builds a scorecard. Every dimension is materialised as UNRATED,
// so an omitted dimension and an unassessed one are the same visible
// thing rather than an absent key.
func New(g *gates.Register, mandatory []string, as ...Assessment) (*Scorecard, error) {
	if g == nil {
		return nil, errors.New("scorecard: no gate register; a scorecard with no gates " +
			"assesses opinions")
	}
	s := &Scorecard{assessments: map[Dimension]Assessment{}, gates: g,
		MandatoryGates: append([]string(nil), mandatory...)}
	for _, d := range Dimensions() {
		s.assessments[d] = Assessment{Dimension: d, Rating: Unrated}
	}
	for _, a := range as {
		if err := a.Validate(); err != nil {
			return nil, err
		}
		s.assessments[a.Dimension] = a
	}
	for _, id := range mandatory {
		if _, err := g.Gate(id); err != nil {
			return nil, fmt.Errorf("scorecard: mandatory gate %s: %w", id, err)
		}
	}
	return s, nil
}

// Assessment returns one dimension's rating.
func (s *Scorecard) Assessment(d Dimension) Assessment { return s.assessments[d] }

// Assessments returns all nine in a fixed order.
func (s *Scorecard) Assessments() []Assessment {
	out := make([]Assessment, 0, len(Dimensions()))
	for _, d := range Dimensions() {
		out = append(out, s.assessments[d])
	}
	return out
}

// Red returns the dimensions that block on their own.
func (s *Scorecard) Red() []Dimension {
	var out []Dimension
	for _, d := range Dimensions() {
		if s.assessments[d].Rating == Red {
			out = append(out, d)
		}
	}
	return out
}

// Unrated returns the dimensions nobody assessed.
func (s *Scorecard) Unrated() []Dimension {
	var out []Dimension
	for _, d := range Dimensions() {
		if s.assessments[d].Rating == Unrated {
			out = append(out, d)
		}
	}
	return out
}

// UnsatisfiedMandatoryGates returns the mandatory gates not satisfied.
func (s *Scorecard) UnsatisfiedMandatoryGates() []string {
	var out []string
	for _, id := range s.MandatoryGates {
		st, err := s.gates.Status(id)
		if err != nil || st.State != gates.Satisfied {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// ReleasePermitted applies the rule: no RED, no UNRATED, and every
// mandatory gate satisfied.
//
// It returns the reasons, because a boolean that says no without
// saying why gets overridden by somebody in a hurry.
func (s *Scorecard) ReleasePermitted() (bool, []string) {
	var reasons []string
	for _, d := range s.Red() {
		reasons = append(reasons, fmt.Sprintf("%s is RED: %s", d, s.assessments[d].Basis))
	}
	for _, d := range s.Unrated() {
		reasons = append(reasons, fmt.Sprintf("%s was not assessed; an unassessed dimension "+
			"is not a passing one", d))
	}
	for _, id := range s.UnsatisfiedMandatoryGates() {
		g, err := s.gates.Gate(id)
		name := id
		if err == nil {
			name = fmt.Sprintf("%s (%s)", id, g.Name)
		}
		st, _ := s.gates.Status(id)
		reasons = append(reasons, fmt.Sprintf("mandatory gate %s is %s", name, st.State))
	}
	sort.Strings(reasons)
	return len(reasons) == 0, reasons
}

// Report renders the scorecard.
func (s *Scorecard) Report() string {
	var b strings.Builder
	b.WriteString("ENTERPRISE QUALIFICATION SCORECARD\n")
	for _, a := range s.Assessments() {
		mark := " "
		if a.ExternallyQualified {
			mark = "+"
		}
		fmt.Fprintf(&b, "  %s %-24s %-8s %s\n", mark, a.Dimension, a.Rating, a.Basis)
		for _, g := range a.Gaps {
			fmt.Fprintf(&b, "      gap: %s\n", g)
		}
		if a.ExternallyQualified {
			fmt.Fprintf(&b, "      qualified by %s\n", a.QualifiedBy)
		}
	}
	ok, reasons := s.ReleasePermitted()
	if ok {
		b.WriteString("\n  RELEASE PERMITTED: no dimension is RED and every mandatory gate " +
			"is satisfied.\n")
	} else {
		fmt.Fprintf(&b, "\n  RELEASE NOT PERMITTED (%d reason(s)):\n", len(reasons))
		for _, r := range reasons {
			fmt.Fprintf(&b, "    - %s\n", r)
		}
	}
	b.WriteString("  There is no aggregate score. A single figure would let a strong " +
		"dimension carry a weak one, and the weak one is what a customer needs to see.\n")
	return b.String()
}
