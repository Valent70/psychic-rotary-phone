// Package resolution is the VERIQO entity resolution engine.
//
// # Law 3: no silent merge
//
//	Tidak boleh langsung: A == B tanpa qualification.
//
// The five outcomes are the law:
//
//	SAME_ENTITY           the same real-world thing
//	POSSIBLE_SAME_ENTITY  might be; a reviewer must decide
//	RELATED_ENTITY        distinct, and connected
//	DISTINCT_ENTITY       actively shown to be different
//	UNRESOLVED            not enough was assessed to say
//
// UNRESOLVED is the zero value, and it is the answer far more often
// than a demo suggests. A resolver that returns SAME or DISTINCT for
// every pair is not more capable; it is one that has stopped
// distinguishing "different" from "no evidence of sameness".
//
// # Why ten signals and not one score
//
// The specification lists them, and the reason they are separate is
// that they FAIL differently. Name similarity is defeated by
// transliteration; identifier match is defeated by reassignment;
// behavioural similarity is defeated by two ships on the same route.
// A single blended score lets a strong signal cover for a
// contradicted one, and the contradiction is the thing that should
// have stopped the merge.
//
// So contradiction is not a negative weight. It is a VETO: a
// contradicted pair cannot be SAME_ENTITY however much else agrees.
//
// # A merge is proposed here and approved elsewhere
//
// This package never merges. It produces a ResolutionResult that says
// what it concluded, on what evidence, with what contradictions, and
// whether a reviewer is required. Merging is an authority act
// (pkg/authority), and separating the two is what keeps an automated
// pipeline from quietly rewriting a case's identity model.
package resolution

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"veriqo/pkg/contract"
	"veriqo/pkg/entity"
	"veriqo/pkg/qualification/independence"
)

var (
	ErrNoSignals     = errors.New("resolution: no signal was assessed")
	ErrSameEntity    = errors.New("resolution: an entity resolved against itself")
	ErrDifferentKind = errors.New("resolution: entities of different kinds")
	ErrNotResolved   = errors.New("resolution: the pair is not resolved to the same entity")
)

// Resolution is the five-valued outcome.
type Resolution string

const (
	Unresolved         Resolution = "UNRESOLVED"
	SameEntity         Resolution = "SAME_ENTITY"
	PossibleSameEntity Resolution = "POSSIBLE_SAME_ENTITY"
	RelatedEntity      Resolution = "RELATED_ENTITY"
	DistinctEntity     Resolution = "DISTINCT_ENTITY"
)

// PermitsMerge reports whether this outcome may lead to a merge.
// Only SAME_ENTITY does, and even then only with an approval.
func (r Resolution) PermitsMerge() bool { return r == SameEntity }

// Determined reports whether any conclusion was reached.
func (r Resolution) Determined() bool { return r != Unresolved && r != "" }

// Signal is one axis of comparison.
type Signal string

const (
	ExactIdentifier      Signal = "EXACT_IDENTIFIER"
	NameSimilarity       Signal = "NAME_SIMILARITY"
	TemporalConsistency  Signal = "TEMPORAL_CONSISTENCY"
	SpatialConsistency   Signal = "SPATIAL_CONSISTENCY"
	RegistryConsistency  Signal = "REGISTRY_CONSISTENCY"
	Ownership            Signal = "OWNERSHIP"
	OperationalBehaviour Signal = "OPERATIONAL_BEHAVIOUR"
	DocumentRelationship Signal = "DOCUMENT_RELATIONSHIP"
	SourceIndependence   Signal = "SOURCE_INDEPENDENCE"
	Contradiction        Signal = "CONTRADICTION"
)

// Signals returns all ten, in the specification's order.
func Signals() []Signal {
	return []Signal{ExactIdentifier, NameSimilarity, TemporalConsistency,
		SpatialConsistency, RegistryConsistency, Ownership, OperationalBehaviour,
		DocumentRelationship, SourceIndependence, Contradiction}
}

func (s Signal) Valid() bool {
	for _, x := range Signals() {
		if x == s {
			return true
		}
	}
	return false
}

// Verdict is what one signal concluded.
type Verdict string

const (
	// NotAssessed is the zero value. It is not "neutral": it means
	// nobody ran this signal, and it is reported separately from
	// NEUTRAL for the same reason UNKNOWN is not INDEPENDENT.
	NotAssessed Verdict = ""
	Supports    Verdict = "SUPPORTS"
	Neutral     Verdict = "NEUTRAL"
	Opposes     Verdict = "OPPOSES"
	// Contradicts is stronger than OPPOSES: the two entities cannot be
	// the same, on this evidence. It vetoes SAME_ENTITY outright.
	Contradicts Verdict = "CONTRADICTS"
)

func (v Verdict) Assessed() bool { return v != NotAssessed }

// Finding is one signal's result with its reasoning.
type Finding struct {
	Signal  Signal  `json:"signal"`
	Verdict Verdict `json:"verdict"`
	// Weight is how much this signal may contribute, 0..1. It is
	// carried per finding rather than per signal because the same
	// signal is worth different amounts depending on what it matched:
	// an IMO match and an MMSI match are both EXACT_IDENTIFIER.
	Weight       float64  `json:"weight"`
	Detail       string   `json:"detail"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
}

// Result is the engine's output. It is a proposal, never a merge.
type Result struct {
	A, B contract.ID `json:"-"`

	Resolution Resolution `json:"resolution"`

	// IdentityConfidence is the weighted support, 0..1. It is
	// deliberately NOT the whole answer: a caller that reads only this
	// number has re-created the blended score this package exists to
	// avoid, so Resolution is what decides and this is what explains.
	IdentityConfidence float64 `json:"identity_confidence"`

	Findings       []Finding `json:"findings"`
	EvidenceRefs   []string  `json:"evidence_refs"`
	SourceClusters []string  `json:"source_clusters"`
	Contradictions []string  `json:"contradictions"`

	ValidFrom time.Time  `json:"valid_from"`
	ValidTo   *time.Time `json:"valid_to,omitempty"`

	PolicyVersion   contract.Version `json:"policy_version"`
	OntologyVersion contract.Version `json:"ontology_version"`

	// ReviewerRequired is true whenever a human must decide. It is set
	// for POSSIBLE_SAME_ENTITY always, and for SAME_ENTITY whenever the
	// support rests on a reassignable identifier or on correlated
	// sources.
	ReviewerRequired bool   `json:"reviewer_required"`
	ReviewReason     string `json:"review_reason,omitempty"`

	ReplayReference string `json:"replay_reference"`

	// Unassessed names the signals nobody ran. A caller seeing
	// UNRESOLVED needs to know whether that is because the evidence
	// says nothing or because the work was not done.
	Unassessed []Signal `json:"unassessed,omitempty"`
}

// Config is what the engine needs that is not in the entities.
type Config struct {
	PolicyVersion   contract.Version
	OntologyVersion contract.Version
	// At is the instant the resolution is made for. Identifier
	// overlaps and attribute scopes are evaluated against it.
	At time.Time
	// Sources maps an evidence ref to the source that supplied it, so
	// the SOURCE_INDEPENDENCE signal can be computed rather than
	// assumed.
	Sources map[string]independence.Source
	// SameThreshold is the weighted support required for SAME_ENTITY.
	// PossibleThreshold is the floor for POSSIBLE_SAME_ENTITY.
	SameThreshold     float64
	PossibleThreshold float64
}

// DefaultConfig returns thresholds that are deliberately conservative.
//
// The cost asymmetry is the reason: a false merge corrupts a case and
// is discovered late, if ever; a missed merge produces a review task.
// 0.85 and 0.45 are VERIQO's stated choice, not a measured optimum,
// and they are published so a customer can disagree with the number
// rather than with a hidden default.
func DefaultConfig() Config {
	return Config{SameThreshold: 0.85, PossibleThreshold: 0.45}
}

// Resolve compares two entities.
func Resolve(a, b entity.Entity, findings []Finding, cfg Config) (Result, error) {
	if err := a.Validate(); err != nil {
		return Result{}, fmt.Errorf("resolution: a: %w", err)
	}
	if err := b.Validate(); err != nil {
		return Result{}, fmt.Errorf("resolution: b: %w", err)
	}
	if a.ID == b.ID {
		return Result{}, fmt.Errorf("%w: %s", ErrSameEntity, a.ID)
	}
	if a.TenantID != b.TenantID {
		return Result{}, fmt.Errorf("%w: %s and %s", contract.ErrCrossTenant, a.ID, b.ID)
	}
	if a.Kind != b.Kind {
		return Result{}, fmt.Errorf("%w: %s is %s, %s is %s", ErrDifferentKind, a.ID, a.Kind, b.ID, b.Kind)
	}
	if cfg.At.IsZero() {
		return Result{}, errors.New("resolution: no instant; identifier overlaps cannot be evaluated")
	}
	if cfg.PolicyVersion.Zero() || cfg.OntologyVersion.Zero() {
		return Result{}, fmt.Errorf("%w: a resolution that does not record its versions "+
			"cannot be replayed", contract.ErrUnversioned)
	}

	// The engine computes the signals it can compute itself and takes
	// the rest from the caller, who has the domain evidence.
	all := append([]Finding(nil), findings...)
	all = append(all, identifierFindings(a, b)...)
	if f, ok := independenceFinding(a, b, cfg); ok {
		all = append(all, f)
	}
	all = dedupe(all)

	res := Result{
		A: a.ID, B: b.ID,
		Findings:        all,
		ValidFrom:       cfg.At,
		PolicyVersion:   cfg.PolicyVersion,
		OntologyVersion: cfg.OntologyVersion,
		EvidenceRefs:    mergeRefs(a, b, all),
	}

	assessed := map[Signal]bool{}
	var support, opposition, totalWeight float64
	vetoed := false
	reassignableOnly := true

	for _, f := range all {
		if !f.Signal.Valid() {
			return Result{}, fmt.Errorf("resolution: unknown signal %q", f.Signal)
		}
		if !f.Verdict.Assessed() {
			continue
		}
		assessed[f.Signal] = true
		totalWeight += f.Weight
		switch f.Verdict {
		case Supports:
			support += f.Weight
			if f.Signal == ExactIdentifier && strings.Contains(f.Detail, "DEFINITIVE") {
				reassignableOnly = false
			}
		case Opposes:
			opposition += f.Weight
		case Contradicts:
			vetoed = true
			res.Contradictions = append(res.Contradictions, string(f.Signal)+": "+f.Detail)
		}
	}
	for _, s := range Signals() {
		if !assessed[s] {
			res.Unassessed = append(res.Unassessed, s)
		}
	}
	if len(assessed) == 0 {
		return Result{}, ErrNoSignals
	}
	if totalWeight > 0 {
		res.IdentityConfidence = support / totalWeight
	}
	res.SourceClusters = sourceClusters(res.EvidenceRefs, cfg)

	// Caveats are computed before the decision, not inside one branch
	// of it. A merge supported entirely by one source cluster is one
	// observation repeated whether the confidence landed at 0.83 or
	// 0.93, and a reviewer needs to be told which it is either way.
	var caveats []string
	if reassignableOnly && hasVerdict(all, ExactIdentifier, Supports) {
		caveats = append(caveats, "the identifier support rests on a reassignable scheme "+
			"(MMSI, call sign or internal id); the same value identifies different things "+
			"at different times")
	}
	if len(res.SourceClusters) == 1 && len(res.EvidenceRefs) > 1 {
		caveats = append(caveats, "every supporting evidence item came from one source "+
			"cluster, so the agreement is one observation repeated rather than corroboration")
	}

	// The decision. Order matters: the veto is checked first, so no
	// amount of supporting weight can reach SAME_ENTITY past a
	// contradiction.
	switch {
	case vetoed:
		// A contradiction says these are not the same thing. Whether
		// they are RELATED is a separate question the caller may have
		// answered; without that, DISTINCT is what was shown.
		res.Resolution = DistinctEntity
		if hasVerdict(all, DocumentRelationship, Supports) || hasVerdict(all, Ownership, Supports) {
			res.Resolution = RelatedEntity
		}

	case res.IdentityConfidence >= cfg.SameThreshold && opposition == 0:
		res.Resolution = SameEntity
		// A clean threshold crossing is still not an approval when a
		// caveat applies: these are the two ways a high number can be
		// produced by weak evidence.
		if len(caveats) > 0 {
			res.ReviewerRequired = true
			res.ReviewReason = strings.Join(caveats, "; ")
		}

	case res.IdentityConfidence >= cfg.PossibleThreshold:
		res.Resolution = PossibleSameEntity
		res.ReviewerRequired = true
		res.ReviewReason = fmt.Sprintf(
			"identity confidence %.2f is between the possible floor %.2f and the same-entity "+
				"threshold %.2f; a reviewer decides",
			res.IdentityConfidence, cfg.PossibleThreshold, cfg.SameThreshold)
		if len(caveats) > 0 {
			res.ReviewReason += ". Also: " + strings.Join(caveats, "; ")
		}

	case hasVerdict(all, DocumentRelationship, Supports) || hasVerdict(all, Ownership, Supports):
		res.Resolution = RelatedEntity

	default:
		res.Resolution = Unresolved
	}

	res.ReplayReference = fmt.Sprintf("resolve/%s/%s@%s/%s",
		a.ID, b.ID, cfg.At.UTC().Format(time.RFC3339), cfg.PolicyVersion)
	return res, nil
}

// identifierFindings computes the EXACT_IDENTIFIER signal.
//
// The strength of the matched scheme becomes the weight, and the
// temporal-overlap requirement for a reassignable scheme is enforced
// by Identifier.Matches rather than re-implemented here.
func identifierFindings(a, b entity.Entity) []Finding {
	var out []Finding
	best := Finding{Signal: ExactIdentifier, Verdict: Neutral, Weight: 0.1,
		Detail: "no identifier is shared"}
	matched := false

	for _, ia := range a.Identifiers {
		for _, ib := range b.Identifiers {
			if ia.Scheme != ib.Scheme {
				continue
			}
			same := strings.EqualFold(ia.Value, ib.Value)
			overlaps := ia.Scope.Overlaps(ib.Scope)

			switch {
			case same && ia.Matches(ib):
				matched = true
				w, label := 0.35, "WEAK"
				switch ia.Scheme.Strength() {
				case entity.Definitive:
					w, label = 0.95, "DEFINITIVE"
				case entity.Strong:
					w, label = 0.7, "STRONG"
				}
				if w > best.Weight || best.Verdict != Supports {
					best = Finding{Signal: ExactIdentifier, Verdict: Supports, Weight: w,
						Detail:       fmt.Sprintf("%s matches on %s (%s)", ia, ia.Scheme, label),
						EvidenceRefs: append(append([]string{}, ia.EvidenceRefs...), ib.EvidenceRefs...)}
				}

			case same && !overlaps && ia.Scheme.Reassignable():
				// The reflagging case: equal values, disjoint periods.
				// This is not a match and it is not neutral -- it is
				// the specific trap, and it must be reported.
				out = append(out, Finding{Signal: ExactIdentifier, Verdict: Neutral, Weight: 0.15,
					Detail: fmt.Sprintf("%s carries the same %s value in a NON-OVERLAPPING period; "+
						"%s is reassigned, so this is not evidence of sameness",
						ia, ia.Scheme, ia.Scheme)})

			case !same && ia.Scheme.Strength() == entity.Definitive && overlaps:
				// Two different permanent identifiers, both valid at
				// once. They cannot be the same thing.
				out = append(out, Finding{Signal: Contradiction, Verdict: Contradicts, Weight: 1.0,
					Detail: fmt.Sprintf("both carry a %s over an overlapping period and the values "+
						"differ (%s vs %s); a permanent identifier is not reassigned",
						ia.Scheme, ia.Value, ib.Value),
					EvidenceRefs: append(append([]string{}, ia.EvidenceRefs...), ib.EvidenceRefs...)})
			}
		}
	}
	if matched || len(out) == 0 {
		out = append(out, best)
	}
	return out
}

// independenceFinding computes SOURCE_INDEPENDENCE from the evidence
// each entity rests on.
//
// A pair whose agreement comes entirely from one source cluster has
// one observation supporting it, not two, and that is a fact about the
// resolution rather than about the sources.
func independenceFinding(a, b entity.Entity, cfg Config) (Finding, bool) {
	if len(cfg.Sources) == 0 {
		return Finding{}, false
	}
	var srcs []independence.Source
	seen := map[string]bool{}
	for _, ref := range append(a.EvidenceRefs(), b.EvidenceRefs()...) {
		s, ok := cfg.Sources[ref]
		if !ok || seen[s.ID] {
			continue
		}
		seen[s.ID] = true
		srcs = append(srcs, s)
	}
	if len(srcs) == 0 {
		return Finding{}, false
	}
	n, unknown, err := independence.EffectiveIndependentCount(srcs)
	if err != nil {
		return Finding{}, false
	}
	switch {
	case n >= 2:
		return Finding{Signal: SourceIndependence, Verdict: Supports, Weight: 0.4,
			Detail: fmt.Sprintf("%d effectively independent source(s) support the pairing", n)}, true
	case len(unknown) > 0:
		return Finding{Signal: SourceIndependence, Verdict: Neutral, Weight: 0.2,
			Detail: fmt.Sprintf("%d source pair(s) are UNASSESSED for independence; the "+
				"agreement may be one observation repeated", len(unknown))}, true
	default:
		return Finding{Signal: SourceIndependence, Verdict: Neutral, Weight: 0.2,
			Detail: "all supporting evidence traces to one source cluster"}, true
	}
}

func sourceClusters(refs []string, cfg Config) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range refs {
		s, ok := cfg.Sources[r]
		if !ok || seen[s.ID] {
			continue
		}
		seen[s.ID] = true
		out = append(out, s.ID)
	}
	sort.Strings(out)
	return out
}

func hasVerdict(fs []Finding, s Signal, v Verdict) bool {
	for _, f := range fs {
		if f.Signal == s && f.Verdict == v {
			return true
		}
	}
	return false
}

// dedupe keeps the strongest finding per signal, preferring an
// assessed verdict over NOT_ASSESSED and a contradiction over
// everything.
func dedupe(fs []Finding) []Finding {
	best := map[Signal]Finding{}
	for _, f := range fs {
		cur, ok := best[f.Signal]
		if !ok {
			best[f.Signal] = f
			continue
		}
		if f.Verdict == Contradicts && cur.Verdict != Contradicts {
			best[f.Signal] = f
			continue
		}
		if cur.Verdict == Contradicts {
			continue
		}
		if f.Verdict.Assessed() && (!cur.Verdict.Assessed() || f.Weight > cur.Weight) {
			best[f.Signal] = f
		}
	}
	out := make([]Finding, 0, len(best))
	for _, s := range Signals() {
		if f, ok := best[s]; ok {
			out = append(out, f)
		}
	}
	return out
}

func mergeRefs(a, b entity.Entity, fs []Finding) []string {
	seen := map[string]bool{}
	var out []string
	add := func(refs []string) {
		for _, r := range refs {
			if r != "" && !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	add(a.EvidenceRefs())
	add(b.EvidenceRefs())
	for _, f := range fs {
		add(f.EvidenceRefs)
	}
	sort.Strings(out)
	return out
}

// Explain renders the result for a "why did VERIQO say this" view,
// including the signals nobody ran.
func (r Result) Explain() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s <-> %s: %s (identity confidence %.2f)\n",
		r.A, r.B, r.Resolution, r.IdentityConfidence)
	for _, f := range r.Findings {
		v := f.Verdict
		if !v.Assessed() {
			v = "NOT_ASSESSED"
		}
		fmt.Fprintf(&b, "  %-22s %-12s w=%.2f  %s\n", f.Signal, v, f.Weight, f.Detail)
	}
	for _, s := range r.Unassessed {
		fmt.Fprintf(&b, "  %-22s %-12s        nobody ran this signal\n", s, "NOT_ASSESSED")
	}
	if len(r.Contradictions) > 0 {
		fmt.Fprintf(&b, "  CONTRADICTED: %s\n", strings.Join(r.Contradictions, "; "))
	}
	if r.ReviewerRequired {
		fmt.Fprintf(&b, "  REVIEWER REQUIRED: %s\n", r.ReviewReason)
	}
	return b.String()
}

// RequireSameEntity is the guard a merge path calls. It refuses
// anything short of an unreviewed-free SAME_ENTITY and says why.
func RequireSameEntity(r Result) error {
	if !r.Resolution.PermitsMerge() {
		return fmt.Errorf("%w: %s is %s", ErrNotResolved, r.A, r.Resolution)
	}
	if r.ReviewerRequired {
		return fmt.Errorf("%w: %s and %s resolved SAME_ENTITY and a reviewer is required: %s",
			ErrNotResolved, r.A, r.B, r.ReviewReason)
	}
	return nil
}
