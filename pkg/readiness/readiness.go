// Package readiness answers "how ready is it" without a percentage.
//
// # Why not a number
//
// "VERIQO is 85% production ready" is a sentence with no truth conditions.
// It cannot be checked, it cannot be disagreed with in detail, and it
// invites the reader to interpolate: 85% sounds like six weeks. The
// reality it usually describes is a system whose architecture is
// finished and whose external validation has not started -- two facts
// that no single number can carry, because they are not on the same
// scale and one does not compensate for the other.
//
// The failure is the same one the uncertainty package refuses for
// intelligence claims, applied to the system itself: an aggregate lets
// a strong dimension carry a weak one, and the weak one is what the
// reader needs.
//
// So readiness is five dimensions, each with its own state, and there
// is deliberately no Overall(). The honest summary sentence is
// generated FROM the dimensions and names the weakest one.
//
// # The five
//
//	ARCHITECTURE            is the design right
//	SEMANTICS               are the rules enforced rather than documented
//	IMPLEMENTATION          is it built and internally assured
//	PRODUCTION_INFRA        has it run anywhere real
//	EXTERNAL_VALIDATION     has anybody outside looked
//
// The first three are within the builder's power. The last two are not,
// and that asymmetry is the whole point: effort moves the first three
// and cannot move the last two at all.
package readiness

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"veriqo/pkg/assurance/state"
)

var (
	ErrUnknownDimension = errors.New("readiness: unknown dimension")
	ErrNoBasis          = errors.New("readiness: an assessment states no basis")
	ErrSelfAssessed     = errors.New("readiness: a dimension requiring an outside party was assessed from inside")
)

// Dimension is one axis.
type Dimension string

const (
	Architecture       Dimension = "ARCHITECTURE"
	Semantics          Dimension = "SEMANTICS"
	Implementation     Dimension = "IMPLEMENTATION"
	ProductionInfra    Dimension = "PRODUCTION_INFRA"
	ExternalValidation Dimension = "EXTERNAL_VALIDATION"
)

// Dimensions returns all five, in the order they are assessed.
func Dimensions() []Dimension {
	return []Dimension{Architecture, Semantics, Implementation, ProductionInfra,
		ExternalValidation}
}

func (d Dimension) Valid() bool {
	for _, k := range Dimensions() {
		if k == d {
			return true
		}
	}
	return false
}

// SelfAssessable reports whether the builder can honestly assess this
// dimension alone.
//
// The first three: yes -- a team can judge its own architecture and
// know whether its rules are enforced. The last two: no. "We think our
// infrastructure would hold" and "we believe we would pass a pentest"
// are not assessments, they are hopes, and a model that let them be
// recorded as assessments would defeat the purpose.
func (d Dimension) SelfAssessable() bool {
	return d == Architecture || d == Semantics || d == Implementation
}

// Level is where a dimension stands.
//
// The scale is deliberately coarse. A five-point scale invites
// arithmetic; four named states that mean different KINDS of thing do
// not.
type Level int

const (
	// NotStarted is the zero value.
	NotStarted Level = iota
	// Partial: begun, and materially incomplete.
	Partial
	// Substantial: the work is largely done and something identifiable
	// remains.
	Substantial
	// High: complete as far as the party assessing it can tell. It is
	// NOT "proven" -- for the two dimensions that need an outside
	// party, High is unreachable from inside and Ready is the top.
	High
	// Ready: complete and confirmed by whoever is entitled to confirm
	// it.
	Ready
)

var levelNames = map[Level]string{
	NotStarted: "NOT_STARTED", Partial: "PARTIAL", Substantial: "SUBSTANTIAL",
	High: "HIGH", Ready: "READY",
}

func (l Level) String() string {
	if n, ok := levelNames[l]; ok {
		return n
	}
	return fmt.Sprintf("Level(%d)", int(l))
}

func (l Level) MarshalJSON() ([]byte, error) { return []byte(`"` + l.String() + `"`), nil }

func (l Level) Valid() bool { _, ok := levelNames[l]; return ok }

// Levels returns every level, weakest first.
func Levels() []Level {
	out := make([]Level, 0, len(levelNames))
	for l := range levelNames {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Assessment is one dimension's position.
type Assessment struct {
	Dimension Dimension `json:"dimension"`
	Level     Level     `json:"level"`
	// Basis is what the level rests on, in a sentence somebody can
	// disagree with.
	Basis string `json:"basis"`
	// Blockers are what stands between this level and the next.
	Blockers []string `json:"blockers,omitempty"`
	// AssessedBy names who judged it. For the two dimensions that need
	// an outside party, an internal assessor cannot record anything
	// above NOT_STARTED.
	AssessedBy string `json:"assessed_by"`
	// External marks an assessment by a party that is not the builder.
	External bool `json:"external"`
	// MaxAssuranceState is the highest assurance rung any control in
	// this dimension has reached. It ties readiness to the register
	// rather than letting the two drift apart.
	MaxAssuranceState state.State `json:"max_assurance_state"`
}

func (a Assessment) Validate() error {
	if !a.Dimension.Valid() {
		return fmt.Errorf("%w: %q", ErrUnknownDimension, a.Dimension)
	}
	if !a.Level.Valid() {
		return fmt.Errorf("readiness: %s has an unknown level", a.Dimension)
	}
	if strings.TrimSpace(a.Basis) == "" {
		return fmt.Errorf("%w: %s", ErrNoBasis, a.Dimension)
	}
	if strings.TrimSpace(a.AssessedBy) == "" {
		return fmt.Errorf("readiness: %s does not say who assessed it", a.Dimension)
	}
	// The rule that makes this model honest rather than decorative.
	if !a.Dimension.SelfAssessable() && !a.External && a.Level > NotStarted {
		return fmt.Errorf("%w: %s is at %s and was assessed by %s, who is not an outside "+
			"party. 'We believe we would pass' is a hope, not an assessment",
			ErrSelfAssessed, a.Dimension, a.Level, a.AssessedBy)
	}
	// HIGH on a dimension nobody outside has touched would say the
	// work is complete as far as anyone can tell -- which is exactly
	// what an outside party is for.
	if !a.Dimension.SelfAssessable() && a.Level >= High && !a.External {
		return fmt.Errorf("%w: %s cannot reach %s from inside", ErrSelfAssessed,
			a.Dimension, a.Level)
	}
	if a.Level > NotStarted && len(a.Blockers) == 0 && a.Level < Ready {
		return fmt.Errorf("readiness: %s is at %s and names nothing standing between it "+
			"and the next level", a.Dimension, a.Level)
	}
	return nil
}

// Profile is the whole picture.
//
// There is deliberately no Overall(), no score, and no percentage.
type Profile struct {
	assessments map[Dimension]Assessment
}

// New builds a profile and requires every dimension.
//
// A missing dimension is not "not applicable": it is a dimension
// nobody assessed, and omitting it from a report is how the weakest
// axis disappears.
func New(as ...Assessment) (*Profile, error) {
	p := &Profile{assessments: map[Dimension]Assessment{}}
	for _, a := range as {
		if err := a.Validate(); err != nil {
			return nil, err
		}
		if _, dup := p.assessments[a.Dimension]; dup {
			return nil, fmt.Errorf("readiness: %s assessed twice", a.Dimension)
		}
		p.assessments[a.Dimension] = a
	}
	var missing []string
	for _, d := range Dimensions() {
		if _, ok := p.assessments[d]; !ok {
			missing = append(missing, string(d))
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("readiness: no assessment for %s; an omitted dimension is "+
			"one nobody looked at, not one that does not apply", strings.Join(missing, ", "))
	}
	return p, nil
}

// All returns every assessment in dimension order.
func (p *Profile) All() []Assessment {
	out := make([]Assessment, 0, len(p.assessments))
	for _, d := range Dimensions() {
		out = append(out, p.assessments[d])
	}
	return out
}

// Weakest returns the dimension in the worst state, and its level.
//
// It returns a DIMENSION, not a number, for the same reason
// uncertainty.Weakest does: the answer to "how ready" is a name, and
// the name is what tells you what to do next.
func (p *Profile) Weakest() (Dimension, Level) {
	worst := Ready
	var which Dimension
	for _, d := range Dimensions() {
		a := p.assessments[d]
		if a.Level < worst || which == "" {
			worst, which = a.Level, d
		}
	}
	return which, worst
}

// Blocked returns the dimensions that are not READY, with their
// blockers.
func (p *Profile) Blocked() map[Dimension][]string {
	out := map[Dimension][]string{}
	for _, d := range Dimensions() {
		a := p.assessments[d]
		if a.Level < Ready {
			out[d] = a.Blockers
		}
	}
	return out
}

// ExternallyTouched reports whether any dimension has been assessed by
// somebody who is not the builder.
func (p *Profile) ExternallyTouched() bool {
	for _, a := range p.assessments {
		if a.External {
			return true
		}
	}
	return false
}

// Sentence is the honest one-line summary.
//
// It exists so that nobody has to invent one, and it is constructed
// from the dimensions rather than written by hand -- which means it
// cannot drift away from what the assessments say. This is the
// replacement for "VERIQO is 85% production ready".
func (p *Profile) Sentence() string {
	self, ext := []string{}, []string{}
	for _, d := range Dimensions() {
		a := p.assessments[d]
		s := fmt.Sprintf("%s %s", strings.ToLower(strings.ReplaceAll(string(d), "_", " ")),
			strings.ToLower(strings.ReplaceAll(a.Level.String(), "_", " ")))
		if d.SelfAssessable() {
			self = append(self, s)
		} else {
			ext = append(ext, s)
		}
	}
	w, l := p.Weakest()
	return fmt.Sprintf(
		"%s. %s. The weakest dimension is %s at %s, and no single figure is offered "+
			"because a strong dimension must not be allowed to carry a weak one.",
		capitalise(strings.Join(self, ", ")),
		capitalise(strings.Join(ext, ", ")),
		strings.ToLower(strings.ReplaceAll(string(w), "_", " ")), l)
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// Report renders the profile.
func (p *Profile) Report() string {
	var b strings.Builder
	b.WriteString("READINESS PROFILE\n")
	b.WriteString("  five dimensions, no aggregate. The first three are within the\n")
	b.WriteString("  builder's power; the last two are not, and effort cannot move them.\n\n")
	for _, a := range p.All() {
		who := a.AssessedBy
		if !a.External {
			who += " (the builder -- not an independent assessment)"
		}
		fmt.Fprintf(&b, "  %-20s %-12s %s\n", a.Dimension, a.Level, a.Basis)
		fmt.Fprintf(&b, "  %-20s %-12s assessed by %s; highest assurance rung reached: %s\n",
			"", "", who, a.MaxAssuranceState)
		for _, bl := range a.Blockers {
			fmt.Fprintf(&b, "  %-20s %-12s blocker: %s\n", "", "", bl)
		}
		b.WriteString("\n")
	}
	if !p.ExternallyTouched() {
		b.WriteString("  No dimension has been assessed by anybody outside the builder.\n\n")
	}
	b.WriteString("  " + p.Sentence() + "\n")
	return b.String()
}

// Veriqo is VERIQO's own readiness, as of the assurance register's
// assessment date.
func Veriqo() (*Profile, error) {
	const by = "VERIQO engineering"
	return New(
		Assessment{Dimension: Architecture, Level: High, AssessedBy: by,
			MaxAssuranceState: state.InternallyAssured,
			Basis: "the qualification kernel is separated from the intelligence fabric, " +
				"the ten design laws each have an enforcement site, and the canonical " +
				"pipeline composes end to end on a worked case",
			Blockers: []string{
				"no architecture has survived contact with a deployment; the topology is " +
					"a diagram",
				"no outside architect has reviewed it",
			}},

		Assessment{Dimension: Semantics, Level: High, AssessedBy: by,
			MaxAssuranceState: state.InternallyAssured,
			Basis: "the rules are enforced by types and constructors rather than by " +
				"convention: a forbidden AI act cannot be recorded, an assurance promotion " +
				"needing independence cannot be represented without it, a gate cannot be " +
				"closed by editing a field, and a figure cannot be rendered without its " +
				"epistemic status",
			Blockers: []string{
				"an operator with commit access can still change the code; the semantics " +
					"raise the cost and make the change visible, they do not make it impossible",
				"the source-class lawfulness model is engineering's reading of the law and " +
					"not counsel's (ED-010)",
			}},

		Assessment{Dimension: Implementation, Level: Substantial, AssessedBy: by,
			MaxAssuranceState: state.InternallyAssured,
			Basis: "the kernel, the assurance layer, the verification kit and two domain " +
				"surfaces are built and tested, and the adversarial suite has found and " +
				"closed three real defects in the system's own core controls",
			Blockers: []string{
				"the key root is a software test double and the code refuses production " +
					"mode (ED-002)",
				"no external anchor is implemented, deliberately (ED-003)",
				"no SBOM, artefact signing or vulnerability scanning (ED-008)",
				"the canonicaliser has never been compared against an independent " +
					"implementation (ED-011)",
			}},

		Assessment{Dimension: ProductionInfra, Level: NotStarted, AssessedBy: by,
			MaxAssuranceState: state.Implemented,
			Basis: "nothing has ever run outside a single process. There is no deployment, " +
				"no multi-host run, no soak beyond seconds, no measured recovery, and no " +
				"operational telemetry of any kind",
			Blockers: []string{
				"multi-host and multi-region deployment (ED-006)",
				"a timed disaster recovery and a restore-and-replay verification (ED-006)",
				"a 72-hour soak under real load (ED-006)",
			}},

		Assessment{Dimension: ExternalValidation, Level: NotStarted, AssessedBy: by,
			MaxAssuranceState: state.InternallyAssured,
			Basis: "no party outside VERIQO has examined, attacked, validated or " +
				"corroborated any part of this system. Thirteen of the twenty production " +
				"gates require one and none is satisfied",
			Blockers: []string{
				"an independent security assessment and red team (ED-001, ED-005)",
				"a real-world document corpus and a recovery attempt (ED-004)",
				"a commercial data contract and a case on data VERIQO did not build (ED-007)",
				"an evaluation set VERIQO did not construct (ED-009)",
				"a third party willing to confirm a content hash or countersign a " +
					"checkpoint (ED-003)",
			}},
	)
}
