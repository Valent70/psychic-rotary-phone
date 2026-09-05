// Package invariant makes Law 11 a property of the SYSTEM rather than
// of one package.
//
// # The hole this closes
//
// pkg/assurance/state refuses a self-certifying promotion. That is
// necessary and it is not sufficient, because it constrains one code
// path. A system has many surfaces that can utter an assurance state:
//
//	the release authority       the passport generator
//	the API                     the CLI
//	every report                the qualification ledger
//	the auditor capsule         any export
//	automation and CI           a UI nobody has written yet
//
// If any one of them can produce the string PRODUCTION_QUALIFIED
// without going through the ladder, Law 11 is bypassed -- and the
// bypass will not look like a bypass. It will look like a field being
// set, in a package whose author had no idea Law 11 existed.
//
// # The invariant
//
//	No system surface may emit an assurance state higher than the
//	state derived from the evidence.
//
// This package is the one chokepoint through which an assurance state
// reaches the outside world. Emit() takes the state a surface WANTS to
// publish and the evidence actually held, derives what that evidence
// supports, and returns whichever is lower -- with a finding attached
// when they differ.
//
// It never returns an error for an over-claim. That is deliberate: a
// surface that got an error would have to decide what to do, and some
// of them would log it and publish the claim anyway. Instead the
// over-claim is silently impossible: the caller receives the derived
// state, whatever it asked for, and a record saying what it asked for.
//
// # CLAIMED != DERIVED
//
// The distinction is frozen here as a type. A claimed state is what a
// party asserts. A derived state is what the evidence supports. They
// are different kinds of thing, and a system that stores only one of
// them cannot report the difference -- which is the single most
// valuable thing it can report.
package invariant

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"veriqo/pkg/assurance/state"
	"veriqo/pkg/contract"
)

var (
	ErrNoSurface    = errors.New("assurance/invariant: an emission names no surface")
	ErrNoSubject    = errors.New("assurance/invariant: an emission names no subject")
	ErrUnknownState = errors.New("assurance/invariant: unknown state")
)

// Surface is a place an assurance state can reach the outside world.
//
// They are enumerated rather than free-form so that a new surface is a
// deliberate addition to this list, reviewed alongside the invariant,
// rather than a string somebody typed.
type Surface string

const (
	ReleaseAuthority      Surface = "RELEASE_AUTHORITY"
	PassportIssuer        Surface = "PASSPORT_ISSUER"
	API                   Surface = "API"
	CLI                   Surface = "CLI"
	Report                Surface = "REPORT"
	QualificationLedger   Surface = "QUALIFICATION_LEDGER"
	AuditorCapsule        Surface = "AUDITOR_CAPSULE"
	Export                Surface = "EXPORT"
	Automation            Surface = "AUTOMATION"
	ContinuousIntegration Surface = "CI"
	UserInterface         Surface = "UI"
)

// Surfaces returns every surface the invariant governs.
func Surfaces() []Surface {
	return []Surface{ReleaseAuthority, PassportIssuer, API, CLI, Report,
		QualificationLedger, AuditorCapsule, Export, Automation,
		ContinuousIntegration, UserInterface}
}

func (s Surface) Valid() bool {
	for _, k := range Surfaces() {
		if k == s {
			return true
		}
	}
	return false
}

// Verdict is what happened to a claim when it met the evidence.
type Verdict string

const (
	// Consistent: the claim equals the derived state.
	Consistent Verdict = "CONSISTENT"
	// Understated: the claim is BELOW the derived state. This is not
	// an error and is never corrected upward -- a party is entitled to
	// claim less than it could, and silently promoting them would
	// make this package an over-claimer.
	Understated Verdict = "UNDERSTATED"
	// ClaimInvalid: the claim is ABOVE the derived state. The emitted
	// value is the derived one and the claim is recorded as a finding.
	ClaimInvalid Verdict = "QUALIFICATION_CLAIM_INVALID"
	// Unassessable: no evidence was supplied at all, so nothing can be
	// derived. Distinct from ClaimInvalid: "we cannot tell" is not
	// "you are wrong", and conflating them would let a surface with a
	// missing evidence file look like a liar.
	Unassessable Verdict = "UNASSESSABLE"
)

// Emission is one attempt by a surface to publish an assurance state.
type Emission struct {
	Surface Surface `json:"surface"`
	// Subject is what the state is about.
	Subject string `json:"subject"`
	// Claimed is what the surface wants to publish.
	Claimed state.State `json:"claimed"`
	// At is when.
	At time.Time `json:"at"`
	// By names the principal or component responsible.
	By contract.ID `json:"by,omitempty"`
}

func (e Emission) Validate() error {
	if !e.Surface.Valid() {
		return fmt.Errorf("%w: %q. A new surface must be added to Surfaces() and reviewed "+
			"alongside the invariant, not typed at a call site", ErrNoSurface, e.Surface)
	}
	if strings.TrimSpace(e.Subject) == "" {
		return ErrNoSubject
	}
	if !e.Claimed.Valid() {
		return fmt.Errorf("%w: %v", ErrUnknownState, e.Claimed)
	}
	return nil
}

// Result is what a surface is permitted to publish.
type Result struct {
	Emission Emission    `json:"emission"`
	Derived  state.State `json:"derived"`
	// Emitted is what the surface MUST publish. It is never above
	// Derived.
	Emitted state.State `json:"emitted"`
	Verdict Verdict     `json:"verdict"`
	// Reason explains the verdict in a sentence a reader can act on.
	Reason string `json:"reason"`
	// Evidence is what the derivation rested on, so the answer can be
	// checked rather than believed.
	Evidence []string `json:"evidence,omitempty"`
}

// Sound reports whether the surface's claim survived contact with the
// evidence. A false answer is a finding, not a crash.
func (r Result) Sound() bool { return r.Verdict == Consistent || r.Verdict == Understated }

// String renders the result for a log or a report.
func (r Result) String() string {
	if r.Verdict == ClaimInvalid {
		return fmt.Sprintf("%s: %s claimed %s; the evidence supports %s. Emitting %s. %s",
			r.Verdict, r.Emission.Surface, r.Emission.Claimed, r.Derived, r.Emitted, r.Reason)
	}
	return fmt.Sprintf("%s: %s emits %s for %s. %s",
		r.Verdict, r.Emission.Surface, r.Emitted, r.Emission.Subject, r.Reason)
}

// Derive computes the state a body of evidence supports.
//
// It is the whole of the invariant's judgement and is deliberately
// small enough to read in one sitting. Two rules:
//
//  1. the highest rung whose required evidence class is present;
//  2. capped at INTERNALLY_ASSURED unless at least one piece of
//     evidence has a validator independent of the implementer.
//
// The cap is Law 11 restated from the evidence's side. A surface
// cannot reach past it by holding more internal evidence, because
// internal evidence of any quantity is still internal.
func Derive(implementer contract.ID, ev []state.Evidence) (state.State, []string, error) {
	var cited []string
	byClass := map[state.Class]bool{}
	independent := false

	for _, e := range ev {
		if err := e.Validate(); err != nil {
			return state.Undefined, nil, err
		}
		byClass[e.Class] = true
		cited = append(cited, fmt.Sprintf("%s [%s] %s", e.ID, e.Class, e.Validator.Name))
		if e.Validator.IndependentOf(implementer) {
			independent = true
		}
	}
	sort.Strings(cited)

	if len(ev) == 0 {
		return state.Undefined, nil, nil
	}

	// Walk up while each rung's requirement is met.
	best := state.Undefined
	for _, s := range state.States() {
		if s == state.Undefined {
			continue
		}
		if !byClass[s.RequiredEvidence()] {
			break
		}
		best = s
	}

	// The cap. Everything above INTERNALLY_ASSURED is defined in terms
	// of somebody else's work.
	if !independent && best > state.InternallyAssured {
		best = state.InternallyAssured
	}
	return best, cited, nil
}

// Emit applies the invariant.
//
// Every surface that publishes an assurance state calls this and
// publishes what it returns. There is no second path, and the absence
// of a second path is the property -- not the correctness of any one
// caller.
func Emit(e Emission, implementer contract.ID, ev []state.Evidence) (Result, error) {
	if err := e.Validate(); err != nil {
		return Result{}, err
	}
	derived, cited, err := Derive(implementer, ev)
	if err != nil {
		return Result{}, err
	}
	r := Result{Emission: e, Derived: derived, Evidence: cited}

	switch {
	case len(ev) == 0:
		r.Emitted = state.Undefined
		r.Verdict = Unassessable
		r.Reason = "no evidence was supplied, so nothing can be derived. This is not a " +
			"finding against the claim: 'we cannot tell' and 'you are wrong' are " +
			"different answers"
	case e.Claimed > derived:
		r.Emitted = derived
		r.Verdict = ClaimInvalid
		r.Reason = fmt.Sprintf(
			"the evidence supports %s. %s is not reachable from it, so the claim is "+
				"recorded and the derived state is what is published", derived, e.Claimed)
	case e.Claimed < derived:
		// Deliberately NOT promoted. A party is entitled to claim less
		// than it could, and quietly upgrading them would make this
		// package the thing it exists to prevent.
		r.Emitted = e.Claimed
		r.Verdict = Understated
		r.Reason = fmt.Sprintf("the evidence would support %s; the claim of %s stands "+
			"because a party may claim less than it could", derived, e.Claimed)
	default:
		r.Emitted = e.Claimed
		r.Verdict = Consistent
		r.Reason = "the claim and the evidence agree"
	}
	return r, nil
}

// Guard is the assertion form, for surfaces that would rather fail
// than publish a corrected value.
//
// CI is the intended caller: a build should break on an over-claim
// rather than quietly publish a lower one, because in CI there is
// nobody reading the output who would notice the correction.
func Guard(e Emission, implementer contract.ID, ev []state.Evidence) error {
	r, err := Emit(e, implementer, ev)
	if err != nil {
		return err
	}
	if r.Verdict == ClaimInvalid {
		return fmt.Errorf("%s: %s", ClaimInvalid, r.Reason)
	}
	return nil
}

// Ledger records every emission, so that over-claims are visible as a
// pattern rather than one at a time.
//
// One surface over-claiming once is a bug. The same surface
// over-claiming every release is a process that has decided the
// invariant is an obstacle, and only a record over time shows the
// difference.
type Ledger struct {
	results []Result
}

func (l *Ledger) Record(r Result) { l.results = append(l.results, r) }

// Record applies the invariant and records the outcome in one call.
func (l *Ledger) Emit(e Emission, implementer contract.ID, ev []state.Evidence) (Result, error) {
	r, err := Emit(e, implementer, ev)
	if err != nil {
		return Result{}, err
	}
	l.Record(r)
	return r, nil
}

// All returns every recorded emission.
func (l *Ledger) All() []Result { return append([]Result(nil), l.results...) }

// Invalid returns the emissions whose claim exceeded the evidence.
func (l *Ledger) Invalid() []Result {
	var out []Result
	for _, r := range l.results {
		if r.Verdict == ClaimInvalid {
			out = append(out, r)
		}
	}
	return out
}

// BySurface counts invalid claims per surface, which is the number
// that identifies a process problem rather than a bug.
func (l *Ledger) BySurface() map[Surface]int {
	out := map[Surface]int{}
	for _, r := range l.Invalid() {
		out[r.Emission.Surface]++
	}
	return out
}

// Report renders the ledger.
func (l *Ledger) Report() string {
	var b strings.Builder
	b.WriteString("ASSURANCE EMISSION LEDGER\n")
	b.WriteString("  No system surface may emit an assurance state higher than the state\n")
	b.WriteString("  derived from the evidence. Every emission below passed through that\n")
	b.WriteString("  check; the ones marked QUALIFICATION_CLAIM_INVALID were corrected\n")
	b.WriteString("  downward before publication.\n\n")
	if len(l.results) == 0 {
		b.WriteString("  no emissions recorded\n")
		return b.String()
	}
	for _, r := range l.results {
		fmt.Fprintf(&b, "  %s\n", r)
	}
	inv := l.Invalid()
	fmt.Fprintf(&b, "\n  %d emission(s), %d invalid claim(s)\n", len(l.results), len(inv))
	if len(inv) > 0 {
		b.WriteString("  by surface:\n")
		var keys []Surface
		for s := range l.BySurface() {
			keys = append(keys, s)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		for _, s := range keys {
			fmt.Fprintf(&b, "    %-22s %d\n", s, l.BySurface()[s])
		}
		b.WriteString("  One surface over-claiming once is a bug. The same surface\n")
		b.WriteString("  over-claiming repeatedly is a process that has decided the\n")
		b.WriteString("  invariant is an obstacle.\n")
	}
	return b.String()
}
