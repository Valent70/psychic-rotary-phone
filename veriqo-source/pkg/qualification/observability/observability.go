// Package observability implements the observability gate and the
// observed-absence state model (MIP-001 W4.2, EQF-001 §4), enforcing
// Constitutional Article 29 -- No Unqualified Absence.
//
// THE DISTINCTION THIS PACKAGE EXISTS TO PROTECT:
//
//	"The vessel did not transmit AIS"        <- a finding about the world
//	"We were not receiving AIS in that area" <- a fact about our coverage
//
// Both look like "no AIS data" in a database. Only the first carries
// evidential weight, and it may only be asserted when we were actually
// in a position to observe. Everything else is one of six weaker
// states that carry no weight and therefore need no gate.
//
// The gate is deliberately conjunctive over all nine conditions: eight
// of nine is not "nearly observed absent", it is not observed absent.
package observability

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// State is an absence state. Only ObservedAbsent carries evidential
// weight.
type State string

const (
	// ObservedAbsent: we looked, with adequate coverage, and it was not
	// there. The only state that supports an inference.
	ObservedAbsent State = "OBSERVED_ABSENT"
	// ExpectedButNotTested: we expected it and did not look.
	ExpectedButNotTested State = "EXPECTED_BUT_NOT_TESTED"
	// NotObservable: no source could have shown it.
	NotObservable State = "NOT_OBSERVABLE"
	// NotCollectable: observable in principle, not acquirable by us.
	NotCollectable State = "NOT_COLLECTABLE"
	// PartialCoverage: we looked, but coverage was incomplete.
	PartialCoverage State = "PARTIAL_COVERAGE"
	// SourceUnavailable: the source existed but was unreachable.
	SourceUnavailable State = "SOURCE_UNAVAILABLE"
	// Inconclusive: we looked and cannot say.
	Inconclusive State = "INCONCLUSIVE"
)

// States returns every absence state.
func States() []State {
	return []State{
		ObservedAbsent, ExpectedButNotTested, NotObservable, NotCollectable,
		PartialCoverage, SourceUnavailable, Inconclusive,
	}
}

// CarriesEvidentialWeight reports whether a state supports an
// inference about the world. Exactly one state does.
func (s State) CarriesEvidentialWeight() bool { return s == ObservedAbsent }

var (
	ErrGateNotMet   = errors.New("observability: OBSERVED_ABSENT requires every gate condition")
	ErrUnknownState = errors.New("observability: unknown absence state")
	ErrNoAssessment = errors.New("observability: no coverage assessment supplied")
	ErrEmptySubject = errors.New("observability: subject must be non-empty")
)

// Condition names the nine prerequisites for ObservedAbsent.
type Condition string

const (
	AdequateSource          Condition = "adequate_source"
	OperationalAvailability Condition = "operational_availability"
	KnownCoverage           Condition = "known_coverage"
	ValidQuery              Condition = "valid_query"
	ValidExpectation        Condition = "valid_expectation"
	CorrectTemporalScope    Condition = "correct_temporal_scope"
	CorrectSpatialScope     Condition = "correct_spatial_scope"
	Integrity               Condition = "integrity"
	ReviewWhereMaterial     Condition = "review_where_material"
)

// GateConditions returns the nine conditions, in a fixed order.
func GateConditions() []Condition {
	return []Condition{
		AdequateSource, OperationalAvailability, KnownCoverage,
		ValidQuery, ValidExpectation, CorrectTemporalScope,
		CorrectSpatialScope, Integrity, ReviewWhereMaterial,
	}
}

// Assessment is a caller's account of what was actually observed.
type Assessment struct {
	// Subject is what was looked for -- an evidence requirement, a
	// signal, an expected record.
	Subject string
	// SourceID is the source that was (or would have been) queried.
	SourceID string
	// Conditions records which gate conditions hold. A condition absent
	// from the map is treated as NOT met: the gate is conjunctive and
	// silence is never assent.
	Conditions map[Condition]bool
	// Material marks the assertion as material to a finding, which
	// makes ReviewWhereMaterial mandatory rather than advisory.
	Material bool
	// Tick is the logical time of the assessment.
	Tick uint64
}

// Result is the gate's verdict.
type Result struct {
	Subject  string `json:"subject"`
	SourceID string `json:"source_id"`
	State    State  `json:"state"`
	// Met and Unmet partition the nine conditions, both sorted, so a
	// report can show precisely why an assertion was refused.
	Met    []Condition `json:"met"`
	Unmet  []Condition `json:"unmet"`
	Reason string      `json:"reason"`
	Tick   uint64      `json:"tick"`
}

// Evaluate runs the gate. It returns the state the evidence actually
// supports -- which may be weaker than the one the caller hoped for.
//
// It never returns an error for a failed gate: falling back to a
// weaker state is a normal, correct outcome, not an exception. Errors
// are reserved for malformed input.
func Evaluate(a Assessment) (Result, error) {
	if strings.TrimSpace(a.Subject) == "" {
		return Result{}, ErrEmptySubject
	}
	if a.Conditions == nil {
		return Result{}, fmt.Errorf("%w for subject %q", ErrNoAssessment, a.Subject)
	}

	var met, unmet []Condition
	for _, c := range GateConditions() {
		if a.Conditions[c] {
			met = append(met, c)
		} else {
			unmet = append(unmet, c)
		}
	}
	sort.Slice(met, func(i, j int) bool { return met[i] < met[j] })
	sort.Slice(unmet, func(i, j int) bool { return unmet[i] < unmet[j] })

	r := Result{
		Subject: a.Subject, SourceID: a.SourceID,
		Met: met, Unmet: unmet, Tick: a.Tick,
	}

	if len(unmet) == 0 {
		r.State = ObservedAbsent
		r.Reason = "all nine observability conditions met; absence is evidentially meaningful"
		return r, nil
	}

	// Fall back to the most specific weaker state the unmet conditions
	// justify. Order matters: the first matching cause wins, most
	// specific first, so a caller learns the actual reason rather than
	// a generic "inconclusive".
	switch {
	case !a.Conditions[AdequateSource]:
		r.State = NotObservable
		r.Reason = "no adequate source could have shown this"
	case !a.Conditions[OperationalAvailability]:
		r.State = SourceUnavailable
		r.Reason = "the source was not operationally available during the window"
	case !a.Conditions[KnownCoverage], !a.Conditions[CorrectTemporalScope], !a.Conditions[CorrectSpatialScope]:
		r.State = PartialCoverage
		r.Reason = "coverage was incomplete or scoped incorrectly; absence here is not informative"
	case !a.Conditions[ValidQuery], !a.Conditions[ValidExpectation]:
		r.State = ExpectedButNotTested
		r.Reason = "the expectation was not validly tested"
	case !a.Conditions[Integrity]:
		r.State = Inconclusive
		r.Reason = "observation integrity could not be established"
	case a.Material && !a.Conditions[ReviewWhereMaterial]:
		r.State = Inconclusive
		r.Reason = "a material absence assertion requires human review, which has not occurred"
	default:
		r.State = Inconclusive
		r.Reason = "one or more observability conditions unmet"
	}
	return r, nil
}

// AssertObservedAbsent is the strict form: it succeeds only when the
// gate fully passes, and returns ErrGateNotMet otherwise. Use it where
// the caller genuinely requires OBSERVED_ABSENT and should fail rather
// than silently proceed with a weaker state.
func AssertObservedAbsent(a Assessment) (Result, error) {
	r, err := Evaluate(a)
	if err != nil {
		return Result{}, err
	}
	if r.State != ObservedAbsent {
		names := make([]string, 0, len(r.Unmet))
		for _, c := range r.Unmet {
			names = append(names, string(c))
		}
		return r, fmt.Errorf("%w: %d unmet (%s); best supportable state is %s",
			ErrGateNotMet, len(r.Unmet), strings.Join(names, ", "), r.State)
	}
	return r, nil
}

// ParseState validates a state string.
func ParseState(s string) (State, error) {
	for _, st := range States() {
		if State(s) == st {
			return st, nil
		}
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownState, s)
}

// AllConditionsMet is a convenience for constructing a fully-gated
// assessment in tests and in callers that have genuinely satisfied
// every condition.
func AllConditionsMet() map[Condition]bool {
	m := make(map[Condition]bool, len(GateConditions()))
	for _, c := range GateConditions() {
		m[c] = true
	}
	return m
}
