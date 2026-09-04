// Package trust is the VERIQO trust calculus.
//
// # Six trusts, not one
//
// The specification is explicit about the failure it is preventing:
//
//	Jangan: source trust = conclusion trust
//
// Trust in a source, in a piece of evidence, in an entity resolution,
// in a hypothesis, in a model and in a conclusion are six different
// quantities. They are related and they are not equal, and the
// substitution -- a trusted source's output being treated as a trusted
// conclusion -- is the single most common way an intelligence system
// overstates itself.
//
// So Trust is keyed by SUBJECT KIND, and the propagation rules are
// explicit and lossy in one direction only.
//
// # Bayesian, with the prior stated
//
// Trust is a Beta distribution over a success rate: alpha successes,
// beta failures, updated by observation. That gives three things a
// point estimate does not:
//
//	the mean                what we currently believe
//	the interval            how much the belief could move
//	the observation count   how much it rests on
//
// A source with 2 successes out of 2 and one with 200 out of 200 have
// the same mean and are not the same thing, and reporting only the
// mean makes them indistinguishable. Every report here carries the
// count.
//
// # The prior is uniform and stated
//
// Beta(1,1): no prior belief. A more confident prior is defensible and
// it is a choice somebody must make deliberately, so it is a parameter
// rather than a constant buried in an update.
package trust

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

var (
	ErrUnknownKind    = errors.New("trust: unknown subject kind")
	ErrNoSubject      = errors.New("trust: no subject")
	ErrBadPrior       = errors.New("trust: a prior must be positive on both parameters")
	ErrNoObservations = errors.New("trust: no observations")
	ErrKindMismatch   = errors.New("trust: trust in one kind of thing is not trust in another")
)

// Kind is what is being trusted.
type Kind string

const (
	InSource     Kind = "SOURCE"
	InEvidence   Kind = "EVIDENCE"
	InResolution Kind = "ENTITY_RESOLUTION"
	InHypothesis Kind = "HYPOTHESIS"
	InModel      Kind = "MODEL"
	InConclusion Kind = "CONCLUSION"
)

func Kinds() []Kind {
	return []Kind{InSource, InEvidence, InResolution, InHypothesis, InModel, InConclusion}
}

func (k Kind) Valid() bool {
	for _, x := range Kinds() {
		if x == k {
			return true
		}
	}
	return false
}

// Trust is a Beta belief about one subject.
type Trust struct {
	SubjectID string `json:"subject_id"`
	Kind      Kind   `json:"kind"`

	// Alpha and Beta are the Beta parameters: prior plus observed
	// successes and failures.
	Alpha float64 `json:"alpha"`
	Beta  float64 `json:"beta"`

	// Observations is how many updates this rests on. It is carried
	// separately from Alpha+Beta because the prior contributes to
	// those and is not an observation.
	Observations int `json:"observations"`
}

// New builds a trust with a stated prior.
func New(subjectID string, kind Kind, priorAlpha, priorBeta float64) (Trust, error) {
	if strings.TrimSpace(subjectID) == "" {
		return Trust{}, ErrNoSubject
	}
	if !kind.Valid() {
		return Trust{}, fmt.Errorf("%w: %q", ErrUnknownKind, kind)
	}
	if priorAlpha <= 0 || priorBeta <= 0 {
		return Trust{}, fmt.Errorf("%w: alpha=%v beta=%v", ErrBadPrior, priorAlpha, priorBeta)
	}
	return Trust{SubjectID: subjectID, Kind: kind, Alpha: priorAlpha, Beta: priorBeta}, nil
}

// Uniform builds a trust with the uniform prior Beta(1,1): no prior
// belief.
func Uniform(subjectID string, kind Kind) (Trust, error) {
	return New(subjectID, kind, 1, 1)
}

// Observe records outcomes.
func (t Trust) Observe(successes, failures int) (Trust, error) {
	if successes < 0 || failures < 0 {
		return Trust{}, errors.New("trust: an observation count cannot be negative")
	}
	if successes+failures == 0 {
		return t, nil
	}
	out := t
	out.Alpha += float64(successes)
	out.Beta += float64(failures)
	out.Observations += successes + failures
	return out, nil
}

// Mean is the current point belief. It is deliberately not the only
// accessor: Interval and Observations qualify it, and a caller that
// reads only this has thrown away what distinguishes 2/2 from 200/200.
func (t Trust) Mean() float64 { return t.Alpha / (t.Alpha + t.Beta) }

// Variance of the Beta distribution.
func (t Trust) Variance() float64 {
	n := t.Alpha + t.Beta
	return (t.Alpha * t.Beta) / (n * n * (n + 1))
}

// Interval is a two-standard-deviation band around the mean, clamped
// to [0,1].
//
// It is a normal approximation, and for small observation counts it is
// crude. That is stated rather than hidden: Grounded() reports whether
// the belief rests on enough observations for the band to mean much,
// and every report prints the count.
func (t Trust) Interval() (low, high float64) {
	sd := math.Sqrt(t.Variance())
	low, high = t.Mean()-2*sd, t.Mean()+2*sd
	return math.Max(0, low), math.Min(1, high)
}

// MinObservations is the count below which a trust value is reported
// as ungrounded. It is a stated choice.
const MinObservations = 10

// Grounded reports whether the belief rests on enough observations to
// be worth quoting as a number rather than as a direction.
func (t Trust) Grounded() bool { return t.Observations >= MinObservations }

// Band renders the trust as a coarse level, for a reader who should
// not be handed three decimal places over four observations.
func (t Trust) Band() string {
	if !t.Grounded() {
		return "UNGROUNDED"
	}
	switch m := t.Mean(); {
	case m >= 0.9:
		return "HIGH"
	case m >= 0.7:
		return "MODERATE"
	case m >= 0.4:
		return "LOW"
	default:
		return "VERY_LOW"
	}
}

func (t Trust) String() string {
	low, high := t.Interval()
	return fmt.Sprintf("%s trust in %s: %s (mean %.2f, 95%%~[%.2f,%.2f], %d observation(s))",
		t.Kind, t.SubjectID, t.Band(), t.Mean(), low, high, t.Observations)
}

// Register holds the six kinds of trust separately.
type Register struct {
	byKind map[Kind]map[string]Trust
}

func NewRegister() *Register {
	r := &Register{byKind: map[Kind]map[string]Trust{}}
	for _, k := range Kinds() {
		r.byKind[k] = map[string]Trust{}
	}
	return r
}

// Put records a trust.
func (r *Register) Put(t Trust) error {
	if !t.Kind.Valid() {
		return fmt.Errorf("%w: %q", ErrUnknownKind, t.Kind)
	}
	if strings.TrimSpace(t.SubjectID) == "" {
		return ErrNoSubject
	}
	r.byKind[t.Kind][t.SubjectID] = t
	return nil
}

// Get retrieves a trust OF A SPECIFIC KIND.
//
// The kind is a required argument, not an optional filter. A lookup by
// id alone would let a caller ask for "trust in X" and receive
// whichever kind happened to be stored -- which is the substitution
// the package exists to prevent, delivered by a convenience method.
func (r *Register) Get(kind Kind, subjectID string) (Trust, bool) {
	m, ok := r.byKind[kind]
	if !ok {
		return Trust{}, false
	}
	t, ok := m[subjectID]
	return t, ok
}

// All returns every trust of a kind, sorted.
func (r *Register) All(kind Kind) []Trust {
	var out []Trust
	for _, t := range r.byKind[kind] {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SubjectID < out[j].SubjectID })
	return out
}

// Propagation describes how trust flows between kinds.
type Propagation struct {
	From Kind
	To   Kind
	// Attenuation is the fraction of the observation count that
	// carries. It is always < 1: trust in a source is weaker evidence
	// about its output than about the source itself.
	Attenuation float64
	Why         string
}

// Propagations are the permitted flows, written out.
//
// The list is short and the omissions are the content:
//
//   - SOURCE -> EVIDENCE is permitted, attenuated. A reliable source
//     usually produces reliable evidence, and "usually" is what the
//     attenuation encodes.
//   - EVIDENCE -> CONCLUSION is NOT here. That is the substitution the
//     specification forbids, and there is no path for it: a
//     conclusion's trust comes from its own assessment, which reads
//     the evidence trust as one input among several rather than
//     inheriting it.
//   - MODEL -> CONCLUSION is not here either, for the same reason and
//     with more force: a well-behaved model does not make its output
//     true.
func Propagations() []Propagation {
	return []Propagation{
		{From: InSource, To: InEvidence, Attenuation: 0.5,
			Why: "a reliable source usually produces reliable evidence, and usually is not always: " +
				"the same source can be authoritative on one question and out of scope on another"},
		{From: InEvidence, To: InResolution, Attenuation: 0.3,
			Why: "reliable evidence constrains an identity resolution without determining it; " +
				"the resolution also depends on the signals and the thresholds"},
		{From: InResolution, To: InHypothesis, Attenuation: 0.3,
			Why: "a sound resolution is a precondition for a hypothesis about the entities, " +
				"not evidence for the hypothesis itself"},
	}
}

// Propagate derives a trust in one kind from a trust in another.
//
// It returns an error for a flow that is not permitted, naming it,
// rather than returning a zero value a caller might use.
func Propagate(from Trust, to Kind, subjectID string) (Trust, error) {
	for _, p := range Propagations() {
		if p.From != from.Kind || p.To != to {
			continue
		}
		out, err := Uniform(subjectID, to)
		if err != nil {
			return Trust{}, err
		}
		// Carry an attenuated share of the evidence, keeping the mean
		// and shrinking the confidence. The observation count is
		// attenuated too, so a derived trust is never reported as
		// better grounded than the thing it came from.
		obs := float64(from.Observations) * p.Attenuation
		m := from.Mean()
		out.Alpha += obs * m
		out.Beta += obs * (1 - m)
		out.Observations = int(obs)
		return out, nil
	}
	return Trust{}, fmt.Errorf("%w: there is no permitted flow from %s to %s. "+
		"A conclusion's trust is assessed from its own evidence, contradictions and "+
		"independence; inheriting it from an input is how a trusted source becomes a "+
		"trusted conclusion", ErrKindMismatch, from.Kind, to)
}

// Report renders a register.
func (r *Register) Report() string {
	var b strings.Builder
	b.WriteString("TRUST REGISTER\n")
	for _, k := range Kinds() {
		ts := r.All(k)
		if len(ts) == 0 {
			continue
		}
		fmt.Fprintf(&b, "  %s\n", k)
		for _, t := range ts {
			fmt.Fprintf(&b, "    %s\n", t)
		}
	}
	b.WriteString("  Trust in a source is not trust in a conclusion. There is no flow " +
		"from evidence or model trust to conclusion trust; a conclusion is assessed on " +
		"its own evidence.\n")
	return b.String()
}
