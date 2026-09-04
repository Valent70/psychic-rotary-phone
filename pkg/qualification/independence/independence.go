// Package independence is the VERIQO source independence engine.
//
// # Law 6, stated as a mechanism
//
//	Reuters + aggregator + copied article: 3 sources tidak otomatis
//	berarti 3 independent sources.
//
// Counting sources is easy and almost always wrong. Three feeds that
// all resell one producer's data are one observation with three
// invoices. A corroboration count built by counting rows is the single
// most common way a system reports high confidence in a claim nobody
// independently observed.
//
// # Article 28: UNKNOWN is not INDEPENDENT
//
// The rule this package exists to make unbreakable is that an
// UNASSESSED pair contributes nothing to a corroboration count. Not a
// little; nothing. Two sources whose relationship nobody has examined
// might be the same source, and treating them as independent because
// no dependency was FOUND is drawing a conclusion from an absence of
// work.
//
// The mutation suite constructs the forbidden value directly and
// demands that this package refuse it, in both places it could leak:
// the pairwise verdict and the aggregate count.
//
// # Six dimensions, and why a single flag is not enough
//
//	PRODUCER        who created the observation
//	ACQUISITION     how it was obtained
//	SENSOR          the instrument or system that observed
//	TRANSFORMATION  the processing chain it passed through
//	TEMPORAL        whether the observations are of the same moment
//	OWNERSHIP       who controls the parties involved
//
// Two AIS feeds may have different producers and the same sensor
// network -- which is the case that matters most in maritime and the
// one a single "independent" flag cannot express. The verdict is
// derived from which dimensions differ, and the dimensions that
// DISQUALIFY independence when shared are named explicitly rather than
// inferred.
package independence

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	ErrNoSource       = errors.New("independence: a source must have an id")
	ErrSameSource     = errors.New("independence: a source is not independent of itself")
	ErrNotIndependent = errors.New("independence: the sources are not independent")
	ErrUnassessed     = errors.New("independence: the relationship has not been assessed")
)

// Dimension is one axis along which two sources may share an origin.
type Dimension string

const (
	Producer       Dimension = "PRODUCER"
	Acquisition    Dimension = "ACQUISITION"
	Sensor         Dimension = "SENSOR"
	Transformation Dimension = "TRANSFORMATION"
	Temporal       Dimension = "TEMPORAL"
	Ownership      Dimension = "OWNERSHIP"
)

// Dimensions returns every dimension in a fixed order.
func Dimensions() []Dimension {
	return []Dimension{Producer, Acquisition, Sensor, Transformation, Temporal, Ownership}
}

// DisqualifyingDimensions are the dimensions on which a SHARED value
// means the two sources are not independent observations, whatever the
// other dimensions say.
//
// PRODUCER, SENSOR and OWNERSHIP are here because sharing any of them
// means the second source adds no new observation: the same party, the
// same instrument, or the same controlling interest produced both.
//
// ACQUISITION, TRANSFORMATION and TEMPORAL are NOT here, because
// sharing them weakens independence without destroying it: two
// genuinely different producers using the same acquisition method are
// still two observations, and the correlation is real but partial.
func DisqualifyingDimensions() []Dimension {
	return []Dimension{Producer, Sensor, Ownership}
}

func (d Dimension) Disqualifying() bool {
	for _, k := range DisqualifyingDimensions() {
		if k == d {
			return true
		}
	}
	return false
}

func (d Dimension) Valid() bool {
	for _, k := range Dimensions() {
		if k == d {
			return true
		}
	}
	return false
}

// Source is a provider of evidence, with whatever has been assessed
// about its origin.
//
// Attributes maps a dimension to an opaque identity. Two sources share
// a dimension when their values for it are equal. A dimension ABSENT
// from the map is UNASSESSED -- not "different", which is the whole
// point of Article 28.
type Source struct {
	ID         string               `json:"id"`
	Attributes map[Dimension]string `json:"attributes,omitempty"`
}

func (s Source) Validate() error {
	if strings.TrimSpace(s.ID) == "" {
		return ErrNoSource
	}
	for d := range s.Attributes {
		if !d.Valid() {
			return fmt.Errorf("independence: source %s carries unknown dimension %q", s.ID, d)
		}
	}
	return nil
}

// Assessed reports the dimensions somebody has actually recorded.
func (s Source) Assessed() []Dimension {
	var out []Dimension
	for _, d := range Dimensions() {
		if v, ok := s.Attributes[d]; ok && strings.TrimSpace(v) != "" {
			out = append(out, d)
		}
	}
	return out
}

// Verdict is the pairwise conclusion.
type Verdict string

const (
	// Independent: every disqualifying dimension was assessed on both
	// sources and differs on all of them.
	Independent Verdict = "INDEPENDENT"
	// PartiallyIndependent: the disqualifying dimensions differ, and
	// at least one non-disqualifying dimension is shared.
	PartiallyIndependent Verdict = "PARTIALLY_INDEPENDENT"
	// Correlated: the sources share a non-disqualifying dimension and
	// at least one disqualifying dimension is unassessed.
	Correlated Verdict = "CORRELATED"
	// Dependent: they share a disqualifying dimension.
	Dependent Verdict = "DEPENDENT"
	// Unknown: not enough was assessed to say anything. This is the
	// zero value, and it satisfies no requirement.
	Unknown Verdict = "UNKNOWN"
)

// SatisfiesIndependenceRequirement reports whether a verdict may be
// used where independence is required.
//
// Only INDEPENDENT does. PARTIALLY_INDEPENDENT deliberately does not:
// a caller that needs independence needs it, and a partial answer is
// information for a reader rather than a permission for a rule.
func (v Verdict) SatisfiesIndependenceRequirement() bool { return v == Independent }

// Assessed reports whether the verdict rests on any work at all.
func (v Verdict) Assessed() bool { return v != Unknown && v != "" }

// Result is the pairwise assessment with its reasoning.
type Result struct {
	A, B    string  `json:"-"`
	Verdict Verdict `json:"verdict"`

	// Shared and Differing name the dimensions on each side.
	Shared    []Dimension `json:"shared,omitempty"`
	Differing []Dimension `json:"differing,omitempty"`
	// Unassessed names the dimensions nobody recorded on at least one
	// of the two sources. It is reported so a caller can tell a low
	// verdict caused by dependence from one caused by ignorance --
	// which are opposite problems with opposite remedies.
	Unassessed []Dimension `json:"unassessed,omitempty"`

	// Confidence is how much of the assessment was actually done: the
	// share of dimensions assessed on both sources.
	//
	// It is NOT a probability that the sources are independent. It is
	// deliberately a coverage figure, because a confidence number that
	// mixed "we looked and they differ" with "we did not look" would
	// reproduce exactly the confusion this package exists to prevent.
	Confidence float64 `json:"confidence"`

	Explanation string `json:"explanation"`
}

// Assess compares two sources.
func Assess(a, b Source) (Result, error) {
	if err := a.Validate(); err != nil {
		return Result{}, err
	}
	if err := b.Validate(); err != nil {
		return Result{}, err
	}
	if a.ID == b.ID {
		return Result{A: a.ID, B: b.ID, Verdict: Dependent,
			Explanation: "a source is not independent of itself"}, nil
	}

	r := Result{A: a.ID, B: b.ID}
	for _, d := range Dimensions() {
		av, aok := a.Attributes[d]
		bv, bok := b.Attributes[d]
		if !aok || !bok || strings.TrimSpace(av) == "" || strings.TrimSpace(bv) == "" {
			r.Unassessed = append(r.Unassessed, d)
			continue
		}
		if av == bv {
			r.Shared = append(r.Shared, d)
		} else {
			r.Differing = append(r.Differing, d)
		}
	}
	assessedCount := len(Dimensions()) - len(r.Unassessed)
	r.Confidence = float64(assessedCount) / float64(len(Dimensions()))

	// The decision, in the order that makes each case unreachable from
	// the ones below it.
	switch {
	case sharesADisqualifyingDimension(r.Shared):
		r.Verdict = Dependent
		r.Explanation = fmt.Sprintf(
			"%s and %s share %s; the same party, instrument or controlling interest produced "+
				"both, so the second adds no independent observation",
			a.ID, b.ID, joinDims(disqualifyingSubset(r.Shared)))

	case anyDisqualifyingUnassessed(r.Unassessed):
		// Article 28. This case comes BEFORE any positive verdict, so
		// there is no path on which an unassessed pair is called
		// independent.
		r.Verdict = Unknown
		r.Explanation = fmt.Sprintf(
			"%s and %s have not been assessed on %s; they might be the same source, and "+
				"finding no dependency without looking is not a finding of independence",
			a.ID, b.ID, joinDims(disqualifyingSubset(r.Unassessed)))

	case len(r.Shared) > 0:
		r.Verdict = PartiallyIndependent
		r.Explanation = fmt.Sprintf(
			"%s and %s differ on every disqualifying dimension and share %s; the observations "+
				"are distinct and correlated",
			a.ID, b.ID, joinDims(r.Shared))

	case len(r.Unassessed) > 0:
		// Every disqualifying dimension differs, but some
		// non-disqualifying dimension was never looked at. That is
		// weaker than INDEPENDENT and stronger than UNKNOWN.
		r.Verdict = Correlated
		r.Explanation = fmt.Sprintf(
			"%s and %s differ on every disqualifying dimension; %s were not assessed, so "+
				"a shared acquisition or processing chain has not been ruled out",
			a.ID, b.ID, joinDims(r.Unassessed))

	default:
		r.Verdict = Independent
		r.Explanation = fmt.Sprintf(
			"%s and %s were assessed on all six dimensions and differ on all of them",
			a.ID, b.ID)
	}
	return r, nil
}

func sharesADisqualifyingDimension(shared []Dimension) bool {
	for _, d := range shared {
		if d.Disqualifying() {
			return true
		}
	}
	return false
}

func anyDisqualifyingUnassessed(unassessed []Dimension) bool {
	for _, d := range unassessed {
		if d.Disqualifying() {
			return true
		}
	}
	return false
}

func disqualifyingSubset(ds []Dimension) []Dimension {
	var out []Dimension
	for _, d := range ds {
		if d.Disqualifying() {
			out = append(out, d)
		}
	}
	if len(out) == 0 {
		return ds
	}
	return out
}

func joinDims(ds []Dimension) string {
	if len(ds) == 0 {
		return "no dimensions"
	}
	s := make([]string, len(ds))
	for i, d := range ds {
		s[i] = string(d)
	}
	return strings.Join(s, ", ")
}

// RequireIndependent returns the result, or an error naming why the
// pair does not satisfy an independence requirement.
//
// It exists so callers do not write `res.Verdict != Independent` and
// then forget to distinguish DEPENDENT from UNKNOWN in the message a
// reviewer eventually reads.
func RequireIndependent(a, b Source) (Result, error) {
	r, err := Assess(a, b)
	if err != nil {
		return Result{}, err
	}
	if r.Verdict.SatisfiesIndependenceRequirement() {
		return r, nil
	}
	if !r.Verdict.Assessed() {
		return r, fmt.Errorf("%w: %s", ErrUnassessed, r.Explanation)
	}
	return r, fmt.Errorf("%w: %s is %s: %s", ErrNotIndependent, pairName(a, b), r.Verdict, r.Explanation)
}

func pairName(a, b Source) string { return a.ID + "/" + b.ID }

// Pair names two sources whose relationship was not assessed.
type Pair struct {
	A, B   string
	Reason string
}

func (p Pair) String() string { return fmt.Sprintf("%s+%s (%s)", p.A, p.B, p.Reason) }

// EffectiveIndependentCount is the corroboration count.
//
// # What it counts, and why not the obvious thing
//
// It is NOT len(sources). It is the size of the largest set of sources
// that are pairwise INDEPENDENT -- so three feeds reselling one
// producer count as one, and a fourth genuinely separate observation
// makes two.
//
// It returns the unassessed pairs alongside the count, because a low
// count has two completely different causes -- the sources are
// related, or nobody looked -- and only one of them is fixed by
// acquiring more evidence. A caller that sees "1" without that list
// cannot tell which situation it is in.
//
// The greedy construction is deliberate and stated rather than hidden:
// finding the true maximum independent set is NP-hard, and a greedy
// pass over a deterministic ordering UNDERSTATES the count. Understating
// corroboration is the safe direction to be wrong in.
func EffectiveIndependentCount(sources []Source) (int, []Pair, error) {
	for _, s := range sources {
		if err := s.Validate(); err != nil {
			return 0, nil, err
		}
	}
	// Deterministic ordering: the answer must not depend on the order
	// a caller happened to assemble the slice in.
	ordered := append([]Source(nil), sources...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })

	seen := map[string]bool{}
	for _, s := range ordered {
		if seen[s.ID] {
			return 0, nil, fmt.Errorf("independence: source %q appears twice; "+
				"the same source listed twice is one source", s.ID)
		}
		seen[s.ID] = true
	}

	var unknown []Pair
	for i := 0; i < len(ordered); i++ {
		for j := i + 1; j < len(ordered); j++ {
			r, err := Assess(ordered[i], ordered[j])
			if err != nil {
				return 0, nil, err
			}
			if !r.Verdict.Assessed() {
				unknown = append(unknown, Pair{A: ordered[i].ID, B: ordered[j].ID,
					Reason: r.Explanation})
			}
		}
	}

	var chosen []Source
	for _, cand := range ordered {
		ok := true
		for _, already := range chosen {
			r, err := Assess(cand, already)
			if err != nil {
				return 0, nil, err
			}
			if !r.Verdict.SatisfiesIndependenceRequirement() {
				ok = false
				break
			}
		}
		if ok {
			chosen = append(chosen, cand)
		}
	}
	return len(chosen), unknown, nil
}

// Statement renders a corroboration count in a form that cannot be
// quoted as more than it is.
func Statement(sources []Source) (string, error) {
	n, unknown, err := EffectiveIndependentCount(sources)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d source(s) supplied; %d effectively independent.", len(sources), n)
	if len(unknown) > 0 {
		names := make([]string, len(unknown))
		for i, p := range unknown {
			names[i] = p.A + "+" + p.B
		}
		sort.Strings(names)
		fmt.Fprintf(&b, " %d pair(s) UNASSESSED (%s): the count is low because nobody has "+
			"examined the relationship, not because a relationship was found.",
			len(unknown), strings.Join(names, ", "))
	}
	return b.String(), nil
}
