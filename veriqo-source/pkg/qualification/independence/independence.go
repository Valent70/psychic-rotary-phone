// Package independence implements canonical source independence
// (MIP-001 W4.3, EQF-001 §5), enforcing Constitutional Articles 3 and
// 28.
//
// TWO RULES, AND THE SECOND IS THE EXPENSIVE ONE:
//
//  1. Article 3 -- same-root data is ONE source. Two feeds that both
//     originate from the same constellation, provider pipeline or
//     collector do not corroborate each other, however different their
//     file formats or vendor labels.
//
//  2. Article 28 -- UNKNOWN is not INDEPENDENT. An unassessed
//     dimension leaves the verdict Unknown. It never defaults to
//     independent, and it never quietly becomes independent by being
//     ignored.
//
// The second rule costs something real: a great many plausible
// corroboration claims degrade to Unknown, which is the correct and
// honest outcome. A system that resolves unknowns optimistically will
// report corroboration it does not have, and that is the precise
// failure mode this package prevents.
//
// RELATIONSHIP TO EXISTING CODE. pkg/insurance/evidence/dependency.go
// already reasons about dependency within the insurance domain. This
// package does not fork it; it provides the canonical cross-domain
// vocabulary the MIP requires, over the same underlying idea.
package independence

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Dimension is one axis along which two sources may or may not be
// independent. All fifteen are assessed separately: sharing any one of
// them can be enough to destroy independence.
type Dimension string

const (
	RootOrigin            Dimension = "RootOrigin"
	SensorMeasurement     Dimension = "SensorMeasurement"
	ProviderPipeline      Dimension = "ProviderPipeline"
	Collector             Dimension = "Collector"
	OrganizationalControl Dimension = "OrganizationalControl"
	AcquisitionPath       Dimension = "AcquisitionPath"
	TransformationLineage Dimension = "TransformationLineage"
	Temporal              Dimension = "Temporal"
	Spatial               Dimension = "Spatial"
	Methodological        Dimension = "Methodological"
	Incentive             Dimension = "Incentive"
	IdentityAssurance     Dimension = "IdentityAssurance"
	DataCustody           Dimension = "DataCustody"
	ModelDependency       Dimension = "ModelDependency"
	LegalMandate          Dimension = "LegalMandate"
)

// Dimensions returns all fifteen dimensions in canonical order.
func Dimensions() []Dimension {
	return []Dimension{
		RootOrigin, SensorMeasurement, ProviderPipeline, Collector,
		OrganizationalControl, AcquisitionPath, TransformationLineage,
		Temporal, Spatial, Methodological, Incentive, IdentityAssurance,
		DataCustody, ModelDependency, LegalMandate,
	}
}

// DisqualifyingDimensions are the dimensions on which a SHARED value
// is by itself fatal to an independence claim. Sharing a root origin
// or a sensor means the two "sources" are one observation; sharing a
// temporal window does not.
func DisqualifyingDimensions() []Dimension {
	return []Dimension{
		RootOrigin, SensorMeasurement, ProviderPipeline, Collector,
		OrganizationalControl, DataCustody, ModelDependency,
	}
}

// Relation is the assessed relation between two sources on one
// dimension.
type Relation int

const (
	// Unassessed is the zero value on purpose: a dimension nobody
	// looked at is Unknown, never Distinct.
	Unassessed Relation = iota
	// Shared means both sources have the same value on this dimension.
	Shared
	// Distinct means the values were assessed and genuinely differ.
	Distinct
)

func (r Relation) String() string {
	switch r {
	case Shared:
		return "SHARED"
	case Distinct:
		return "DISTINCT"
	default:
		return "UNASSESSED"
	}
}

// Verdict is the overall independence conclusion for a source pair.
type Verdict string

const (
	// Independent: every disqualifying dimension assessed and distinct.
	Independent Verdict = "INDEPENDENT"
	// Dependent: at least one disqualifying dimension is shared.
	Dependent Verdict = "DEPENDENT"
	// Unknown: no disqualifying dimension is shared, but at least one
	// was never assessed. Article 28 forbids reading this as
	// Independent.
	Unknown Verdict = "UNKNOWN"
)

// SatisfiesIndependenceRequirement reports whether a verdict may be
// used to satisfy a two-source independence requirement. Only
// Independent qualifies -- this is Article 28 in one method.
func (v Verdict) SatisfiesIndependenceRequirement() bool { return v == Independent }

var (
	ErrSameSource     = errors.New("independence: a source cannot corroborate itself")
	ErrEmptySourceID  = errors.New("independence: both source IDs must be non-empty")
	ErrNotIndependent = errors.New("independence: the pair does not satisfy an independent-source requirement")
)

// Source describes one source's attributes along the dimensions.
// A dimension absent from Attributes is UNASSESSED for that source,
// and any pair involving it is Unknown on that dimension.
type Source struct {
	ID         string
	Attributes map[Dimension]string
}

// Assessment is the result of comparing two sources.
type Assessment struct {
	SourceA   string               `json:"source_a"`
	SourceB   string               `json:"source_b"`
	Verdict   Verdict              `json:"verdict"`
	Relations map[Dimension]string `json:"relations"`
	// SharedDisqualifying lists the disqualifying dimensions the pair
	// shares -- the reason for a Dependent verdict.
	SharedDisqualifying []Dimension `json:"shared_disqualifying,omitempty"`
	// UnassessedDisqualifying lists disqualifying dimensions nobody
	// assessed -- the reason for an Unknown verdict.
	UnassessedDisqualifying []Dimension `json:"unassessed_disqualifying,omitempty"`
	Reason                  string      `json:"reason"`
}

// Assess compares two sources across all fifteen dimensions.
func Assess(a, b Source) (Assessment, error) {
	if strings.TrimSpace(a.ID) == "" || strings.TrimSpace(b.ID) == "" {
		return Assessment{}, ErrEmptySourceID
	}
	if a.ID == b.ID {
		return Assessment{}, fmt.Errorf("%w: %q", ErrSameSource, a.ID)
	}

	rels := make(map[Dimension]string, len(Dimensions()))
	relOf := make(map[Dimension]Relation, len(Dimensions()))
	for _, d := range Dimensions() {
		av, aok := a.Attributes[d]
		bv, bok := b.Attributes[d]
		switch {
		case !aok || !bok || av == "" || bv == "":
			relOf[d] = Unassessed
		case av == bv:
			relOf[d] = Shared
		default:
			relOf[d] = Distinct
		}
		rels[d] = relOf[d].String()
	}

	var shared, unassessed []Dimension
	for _, d := range DisqualifyingDimensions() {
		switch relOf[d] {
		case Shared:
			shared = append(shared, d)
		case Unassessed:
			unassessed = append(unassessed, d)
		}
	}
	sortDims(shared)
	sortDims(unassessed)

	res := Assessment{
		SourceA: a.ID, SourceB: b.ID, Relations: rels,
		SharedDisqualifying: shared, UnassessedDisqualifying: unassessed,
	}

	switch {
	case len(shared) > 0:
		res.Verdict = Dependent
		res.Reason = fmt.Sprintf("sources share disqualifying dimension(s) %s -- same-root data is one source (Article 3)",
			joinDims(shared))
	case len(unassessed) > 0:
		res.Verdict = Unknown
		res.Reason = fmt.Sprintf("disqualifying dimension(s) %s were never assessed -- UNKNOWN is not INDEPENDENT (Article 28)",
			joinDims(unassessed))
	default:
		res.Verdict = Independent
		res.Reason = "every disqualifying dimension was assessed and found distinct"
	}
	return res, nil
}

// RequireIndependent is the strict form for callers that genuinely
// need an independent pair. It fails on Unknown as loudly as on
// Dependent, which is the whole point of Article 28.
func RequireIndependent(a, b Source) (Assessment, error) {
	res, err := Assess(a, b)
	if err != nil {
		return Assessment{}, err
	}
	if !res.Verdict.SatisfiesIndependenceRequirement() {
		return res, fmt.Errorf("%w: %s/%s is %s -- %s", ErrNotIndependent, a.ID, b.ID, res.Verdict, res.Reason)
	}
	return res, nil
}

// Cluster groups sources that are not mutually independent. Each
// returned cluster counts as ONE source for corroboration purposes,
// which is the operational form of Article 3.
//
// Sources are clustered transitively: if A shares a root with B, and B
// shares a pipeline with C, then A, B and C are one cluster even
// though A and C may share nothing directly. Corroboration between
// them would still be circular.
func Cluster(sources []Source) ([][]string, error) {
	n := len(sources)
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	union := func(i, j int) {
		ri, rj := find(i), find(j)
		if ri != rj {
			parent[rj] = ri
		}
	}

	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			res, err := Assess(sources[i], sources[j])
			if err != nil {
				return nil, err
			}
			// Only a positively-established dependency merges a cluster.
			// Unknown does NOT merge -- it is reported per-pair by Assess
			// and must not be silently resolved either way here.
			if res.Verdict == Dependent {
				union(i, j)
			}
		}
	}

	groups := map[int][]string{}
	for i, s := range sources {
		r := find(i)
		groups[r] = append(groups[r], s.ID)
	}
	out := make([][]string, 0, len(groups))
	for _, ids := range groups {
		sort.Strings(ids)
		out = append(out, ids)
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out, nil
}

// EffectiveSourceCount reports how many genuinely distinct CLUSTERS a
// set of sources forms.
//
// It answers "how many of these are the same source", and it answers it
// conservatively: only a positively-established dependency merges a
// cluster, so an unassessed pair stays apart.
//
// # What this number is not
//
// It is NOT a corroboration count. Two sources whose independence was
// never assessed form two clusters and therefore count as two here,
// and a caller that reads that as "two independent sources agree" has
// treated UNKNOWN as INDEPENDENT -- which Article 28 forbids.
//
// The distinction is easy to lose because the two numbers are equal
// whenever everything has been assessed, which is the case in every
// fixture. Use EffectiveIndependentCount when the question is
// corroboration.
func EffectiveSourceCount(sources []Source) (int, error) {
	c, err := Cluster(sources)
	if err != nil {
		return 0, err
	}
	return len(c), nil
}

// EffectiveIndependentCount reports how many sources may be counted
// towards corroboration, and names every pair that could not be
// assessed.
//
// A source counts only if EVERY pairing it takes part in was assessed
// and found Independent. One unassessed pairing disqualifies both
// sources from the count, because corroboration between a source and
// something that might be itself is not corroboration.
//
// This is the Article 28 rule -- UNKNOWN is never INDEPENDENT -- applied
// to the number callers actually use. EffectiveSourceCount answers a
// different question and answering it conservatively is not the same as
// answering this one.
//
// The returned pairs are the honest finding: they name what has not
// been looked at, so a caller can go and assess them rather than
// silently losing sources.
func EffectiveIndependentCount(sources []Source) (int, []string, error) {
	n := len(sources)
	if n == 0 {
		return 0, nil, nil
	}
	if n == 1 {
		// A single source corroborates nothing, but it is not
		// disqualified by an assessment that never had to happen.
		return 1, nil, nil
	}
	disqualified := make([]bool, n)
	var unknown []string
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			res, err := Assess(sources[i], sources[j])
			if err != nil {
				return 0, nil, err
			}
			switch res.Verdict {
			case Independent:
				// Nothing to do: this pairing supports both.
			case Dependent:
				// Handled by clustering; for corroboration the pair
				// is one source, so the later one does not add.
				disqualified[j] = true
			default:
				// Unknown. Neither source may be counted, and the
				// pair is reported.
				disqualified[i], disqualified[j] = true, true
				unknown = append(unknown, fmt.Sprintf("%s/%s: %s",
					sources[i].ID, sources[j].ID, res.Reason))
			}
		}
	}
	count := 0
	for i := range disqualified {
		if !disqualified[i] {
			count++
		}
	}
	sort.Strings(unknown)
	return count, unknown, nil
}

func sortDims(d []Dimension) {
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
}

func joinDims(d []Dimension) string {
	s := make([]string, len(d))
	for i, x := range d {
		s[i] = string(x)
	}
	return strings.Join(s, ", ")
}
