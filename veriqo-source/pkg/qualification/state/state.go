// Package state defines the canonical qualification vocabulary
// (MIP-001 W4.5) and the single-source exception (MIP §29),
// enforcing Constitutional Articles 16 and 30.
//
// WHAT IS DELIBERATELY ABSENT. There is no PROVEN state, no
// ESTABLISHED state, and no LIABLE state. That absence is the
// enforcement mechanism for Article 16 -- no adjudication by platform.
// A vocabulary that cannot express a verdict cannot accidentally
// deliver one, and no amount of downstream editorial care is needed to
// keep the boundary.
//
// The single-source exception exists because real cases sometimes have
// exactly one source and no honest alternative. It is a way to proceed
// transparently under a genuine constraint -- never a way to launder
// one source into corroboration. Its output state says so in its own
// name.
package state

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// State is a qualification outcome.
type State string

const (
	// Supported: the claim is supported by qualified evidence.
	Supported State = "SUPPORTED"
	// SupportedWithExceptions: supported, with recorded caveats.
	SupportedWithExceptions State = "SUPPORTED_WITH_EXCEPTIONS"
	// Qualified: qualified subject to stated limits.
	Qualified State = "QUALIFIED"
	// QualifiedWithDissent: qualified, with material dissent carried.
	QualifiedWithDissent State = "QUALIFIED_WITH_DISSENT"
	// Inconclusive: the evidence does not settle the claim.
	Inconclusive State = "INCONCLUSIVE"
	// InsufficientEvidence: not enough evidence was obtained.
	InsufficientEvidence State = "INSUFFICIENT_EVIDENCE"
	// Contradicted: the evidence contradicts the claim.
	Contradicted State = "CONTRADICTED"
	// NotObservable: no source could have shown it.
	NotObservable State = "NOT_OBSERVABLE"
	// NotCollectable: observable in principle, not acquirable.
	NotCollectable State = "NOT_COLLECTABLE"
	// SupportedBySingleHighAssuranceSource is the ONLY state a
	// single-source exception may produce. It is deliberately verbose:
	// nobody can quote it while implying corroboration.
	SupportedBySingleHighAssuranceSource State = "SUPPORTED_BY_SINGLE_HIGH_ASSURANCE_SOURCE"
)

// States returns the canonical qualification vocabulary.
func States() []State {
	return []State{
		Supported, SupportedWithExceptions, Qualified, QualifiedWithDissent,
		Inconclusive, InsufficientEvidence, Contradicted, NotObservable,
		NotCollectable, SupportedBySingleHighAssuranceSource,
	}
}

// forbiddenStates are terms that must never appear as a qualification
// outcome. Kept as data so the prohibition is testable rather than
// merely conventional.
var forbiddenStates = []string{
	"PROVEN", "ESTABLISHED", "LEGALLY_ESTABLISHED", "CORROBORATED",
	"LIABLE", "NOT_LIABLE", "GUILTY", "FRAUD", "TRUE", "VERDICT",
}

// ForbiddenStates returns the terms the vocabulary refuses.
func ForbiddenStates() []string {
	out := make([]string, len(forbiddenStates))
	copy(out, forbiddenStates)
	return out
}

var (
	ErrUnknownState        = errors.New("state: not a canonical qualification state")
	ErrForbiddenState      = errors.New("state: this term asserts a legal or absolute conclusion and is not a qualification state (Articles 16, 30)")
	ErrExceptionIncomplete = errors.New("state: SingleSourceException is missing required fields")
	ErrExceptionExpired    = errors.New("state: SingleSourceException has passed its review tick")
	ErrNotSingleSource     = errors.New("state: a single-source exception requires exactly one effective source")
)

// Parse validates a qualification state, refusing forbidden terms with
// a distinct error so a caller can tell "unknown" from "not allowed".
func Parse(s string) (State, error) {
	up := strings.ToUpper(strings.TrimSpace(s))
	for _, f := range forbiddenStates {
		if up == f {
			return "", fmt.Errorf("%w: %q", ErrForbiddenState, s)
		}
	}
	for _, st := range States() {
		if State(up) == st {
			return st, nil
		}
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownState, s)
}

// AssertsLegalConclusion reports whether a state would assert a legal
// conclusion. Always false for canonical states -- the vocabulary has
// no such member. Present so callers can assert the property.
func (s State) AssertsLegalConclusion() bool {
	up := strings.ToUpper(string(s))
	for _, f := range forbiddenStates {
		if up == f {
			return true
		}
	}
	return false
}

// CarriesDissent reports whether the state advertises unresolved
// dissent to a reader.
func (s State) CarriesDissent() bool { return s == QualifiedWithDissent }

// SingleSourceException records why a claim rests on one source.
// Every field is mandatory: an exception that does not say why it was
// necessary is not an exception, it is an omission.
type SingleSourceException struct {
	ClaimID string
	// SourceID is the one source relied upon.
	SourceID string
	// WhyNecessary explains why single-source reliance is unavoidable.
	WhyNecessary string
	// WhyAlternativesUnavailable explains what else was sought.
	WhyAlternativesUnavailable string
	// SourceAssurance describes the assurance level of that source.
	SourceAssurance string
	// Coverage describes the source's coverage.
	Coverage string
	// KnownLimitations states what the source cannot show.
	KnownLimitations string
	// Reviewer is the human who authorized the exception.
	Reviewer string
	// PolicyVersion pins the governing policy (Article 7).
	PolicyVersion string
	// ReviewTick is when the exception must be revisited. An exception
	// without an expiry would quietly become permanent.
	ReviewTick uint64
}

// Validate checks the exception is complete.
func (e SingleSourceException) Validate() error {
	var missing []string
	for name, v := range map[string]string{
		"ClaimID": e.ClaimID, "SourceID": e.SourceID,
		"WhyNecessary": e.WhyNecessary, "WhyAlternativesUnavailable": e.WhyAlternativesUnavailable,
		"SourceAssurance": e.SourceAssurance, "Coverage": e.Coverage,
		"KnownLimitations": e.KnownLimitations, "Reviewer": e.Reviewer,
		"PolicyVersion": e.PolicyVersion,
	} {
		if strings.TrimSpace(v) == "" {
			missing = append(missing, name)
		}
	}
	if e.ReviewTick == 0 {
		missing = append(missing, "ReviewTick")
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("%w: %s", ErrExceptionIncomplete, strings.Join(missing, ", "))
	}
	return nil
}

// Apply produces the qualification state a valid single-source
// exception yields. It returns SupportedBySingleHighAssuranceSource
// and nothing else -- there is no argument that makes it return
// Supported or a corroborated equivalent.
//
// effectiveSources is the number of genuinely distinct sources, as
// computed by pkg/qualification/independence.EffectiveSourceCount --
// not the raw count. Two feeds sharing a root are one effective
// source, and passing 2 here when the effective count is 1 would be
// the very confusion Article 3 forbids.
func Apply(e SingleSourceException, effectiveSources int, nowTick uint64) (State, error) {
	if err := e.Validate(); err != nil {
		return "", err
	}
	if effectiveSources != 1 {
		return "", fmt.Errorf("%w: effective source count is %d", ErrNotSingleSource, effectiveSources)
	}
	if nowTick > e.ReviewTick {
		return "", fmt.Errorf("%w: review tick %d, now %d", ErrExceptionExpired, e.ReviewTick, nowTick)
	}
	return SupportedBySingleHighAssuranceSource, nil
}

// Qualification is a recorded outcome for one claim.
type Qualification struct {
	ClaimID       string `json:"claim_id"`
	State         State  `json:"state"`
	PolicyVersion string `json:"policy_version"`
	Rationale     string `json:"rationale"`
	// MaterialDissent carries unresolved dissent into the outcome.
	// Article 11 forbids it disappearing.
	MaterialDissent []string `json:"material_dissent,omitempty"`
	Tick            uint64   `json:"tick"`
}

// New builds a Qualification, enforcing the dissent-visibility rule:
// if material dissent exists, the state must advertise it. A caller
// asking for Supported while carrying material dissent is corrected to
// QualifiedWithDissent rather than silently granted the stronger
// state.
func New(claimID string, s State, policyVersion, rationale string, materialDissent []string, tick uint64) (Qualification, error) {
	if strings.TrimSpace(claimID) == "" {
		return Qualification{}, errors.New("state: ClaimID must be non-empty")
	}
	if strings.TrimSpace(policyVersion) == "" {
		return Qualification{}, errors.New("state: PolicyVersion must be non-empty (Article 7)")
	}
	if _, err := Parse(string(s)); err != nil {
		return Qualification{}, err
	}
	if len(materialDissent) > 0 && s != QualifiedWithDissent && s != Contradicted {
		s = QualifiedWithDissent
	}
	d := append([]string(nil), materialDissent...)
	sort.Strings(d)
	return Qualification{
		ClaimID: claimID, State: s, PolicyVersion: policyVersion,
		Rationale: rationale, MaterialDissent: d, Tick: tick,
	}, nil
}
