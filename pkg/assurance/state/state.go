// Package state is the assurance state machine and Law 11.
//
// # The problem it exists to solve
//
// A control that has been written, tested by its author, and marked
// "done" on a spreadsheet is indistinguishable, in every artefact the
// organisation produces, from a control that an outside party has
// attacked and confirmed. Both appear as a green row. The difference
// between them is the entire difference between an assurance claim and
// a wish, and no amount of internal diligence closes it -- because the
// missing thing is not effort, it is INDEPENDENCE.
//
// So assurance is modelled here as a state machine in which the
// transitions that require an outside party CANNOT BE TAKEN by an
// inside party, structurally, whatever they intend and whatever the
// configuration says.
//
// # Law 11 -- No Self-Certification
//
//	An entity may implement and test a control, but may not
//	unilaterally promote that control to an assurance level whose
//	definition requires independent evidence.
//
// This is the assurance-layer twin of Law 7. Law 7 says an AI cannot
// upgrade evidence; Law 11 says an author cannot upgrade the assurance
// of their own control. Both are enforced the same way: the promotion
// is not merely discouraged, it is unrepresentable -- a Promotion that
// lacks the required evidence class does not construct.
//
// # No state jumps
//
// The ladder is walked one rung at a time. A jump from
// INTERNALLY_ASSURED to PRODUCTION_QUALIFIED is refused, because every
// rung it skipped is a question nobody answered: was it tested by
// somebody else, did that testing find anything, was what it found
// fixed, was the fix retested, has it run in production, for how long,
// under what load, with what incidents.
//
// A demotion is different. It may skip any distance downward and needs
// no independent evidence, because withdrawing a claim is not a claim.
package state

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"veriqo/pkg/contract"
	"veriqo/pkg/identity"
)

var (
	ErrUnknownState   = errors.New("assurance/state: unknown state")
	ErrStateJump      = errors.New("assurance/state: a promotion may not skip a state")
	ErrSelfCertified  = errors.New("assurance/state: Law 11 -- this promotion requires evidence from a party that is not the implementer")
	ErrNoEvidence     = errors.New("assurance/state: the promotion cites no evidence")
	ErrWrongClass     = errors.New("assurance/state: the cited evidence is of the wrong class for this promotion")
	ErrNotIndependent = errors.New("assurance/state: the validator is not independent of the implementer")
	ErrNoValidator    = errors.New("assurance/state: evidence that requires a validator names none")
	ErrBackwards      = errors.New("assurance/state: a promotion may not move downward; use Demote")
)

// State is one rung of the assurance ladder.
//
// The ordering is total and the zero value is Undefined, so a struct
// that was never populated cannot masquerade as assured.
type State int

const (
	// Undefined: nobody has said what this control is. The zero value.
	Undefined State = iota
	// Specified: the control has a written definition -- what it must
	// do, and what would count as it failing.
	Specified
	// Implemented: the code exists and builds.
	Implemented
	// InternallyTested: the implementer's own tests exercise it,
	// including its refusal paths.
	InternallyTested
	// InternallyAssured: the implementer has ATTACKED it -- attempted
	// to produce a counterexample, not merely to confirm it works.
	// This is the highest rung reachable without an outside party, and
	// it is where an honest self-assessment stops.
	InternallyAssured
	// ReadyForExternalTest: the artefacts an outside party needs exist
	// and are reproducible: a build, a manifest, test vectors, a
	// replay bundle, a verifier they can run themselves.
	ReadyForExternalTest
	// ExternallyTested: a party that is not VERIQO has run tests
	// against it. Note that this rung says nothing about the RESULT --
	// being tested and passing are different facts.
	ExternallyTested
	// ExternallyValidated: that party has stated, in a signed report
	// with a named scope, that the control does what it claims.
	ExternallyValidated
	// OperationallyProven: it has run in production, under real load,
	// for a stated period, with its incidents recorded.
	OperationallyProven
	// ProductionQualified: everything above, plus the release
	// authority's decision. The top rung, and the only one that
	// permits an unqualified public claim.
	ProductionQualified
)

var stateNames = map[State]string{
	Undefined: "UNDEFINED", Specified: "SPECIFIED", Implemented: "IMPLEMENTED",
	InternallyTested: "INTERNALLY_TESTED", InternallyAssured: "INTERNALLY_ASSURED",
	ReadyForExternalTest: "READY_FOR_EXTERNAL_TEST", ExternallyTested: "EXTERNALLY_TESTED",
	ExternallyValidated: "EXTERNALLY_VALIDATED", OperationallyProven: "OPERATIONALLY_PROVEN",
	ProductionQualified: "PRODUCTION_QUALIFIED",
}

func (s State) String() string {
	if n, ok := stateNames[s]; ok {
		return n
	}
	return fmt.Sprintf("State(%d)", int(s))
}

func (s State) MarshalJSON() ([]byte, error) { return []byte(`"` + s.String() + `"`), nil }

func (s State) Valid() bool { _, ok := stateNames[s]; return ok }

// States returns every state in ladder order.
func States() []State {
	out := make([]State, 0, len(stateNames))
	for s := range stateNames {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Parse resolves a state name.
func Parse(s string) (State, error) {
	for k, v := range stateNames {
		if strings.EqualFold(v, strings.TrimSpace(s)) {
			return k, nil
		}
	}
	return Undefined, fmt.Errorf("%w: %q", ErrUnknownState, s)
}

// SelfReachable reports whether the implementer can reach this state
// alone. It is the boundary Law 11 defends.
//
// INTERNALLY_ASSURED is the last self-reachable rung. Everything above
// it is defined in terms of somebody else's work, so an inside party
// cannot produce the evidence by definition rather than by policy.
func (s State) SelfReachable() bool { return s <= InternallyAssured }

// RequiredEvidence is the class of evidence a promotion INTO this
// state must cite.
func (s State) RequiredEvidence() Class {
	switch s {
	case Undefined, Specified, Implemented, InternallyTested, InternallyAssured:
		return Internal
	case ReadyForExternalTest:
		// Reproducibility is checkable from inside -- the claim is
		// that the capsule builds and verifies, not that anybody has
		// used it.
		return Internal
	case ExternallyTested, ExternallyValidated:
		return ExternalRequired
	case OperationallyProven:
		return ProductionRequired
	case ProductionQualified:
		return ReleaseAuthorityRequired
	}
	return Internal
}

// Class is the kind of evidence a promotion needs.
//
// The classes are not severities. They name WHO can produce the
// evidence, which is what makes them enforceable: no amount of
// internal work produces external evidence.
type Class string

const (
	// Internal: the implementer's own tests, attacks and reasoning.
	Internal Class = "ASSURANCE_INTERNAL"
	// ExternalRequired: a signed report from a validator who is not
	// the implementer, with a named scope.
	ExternalRequired Class = "EXTERNAL_REQUIRED"
	// ProductionRequired: operational evidence from a real deployment
	// -- a period, a load, an incident record.
	ProductionRequired Class = "PRODUCTION_REQUIRED"
	// LegalRequired: an opinion from counsel. Distinct from external
	// because a security assessor cannot answer a lawfulness question
	// and a lawyer cannot answer a cryptographic one.
	LegalRequired Class = "LEGAL_REQUIRED"
	// ReleaseAuthorityRequired: the named authority's decision, which
	// rests on everything below it.
	ReleaseAuthorityRequired Class = "RELEASE_AUTHORITY_REQUIRED"
)

func (c Class) Valid() bool {
	switch c {
	case Internal, ExternalRequired, ProductionRequired, LegalRequired, ReleaseAuthorityRequired:
		return true
	}
	return false
}

// NeedsIndependentParty reports whether this class can be produced
// from inside.
func (c Class) NeedsIndependentParty() bool { return c != Internal }

// Classes returns every class in a fixed order.
func Classes() []Class {
	return []Class{Internal, ExternalRequired, ProductionRequired, LegalRequired,
		ReleaseAuthorityRequired}
}

// Validator is whoever produced a piece of assurance evidence.
type Validator struct {
	// ID is the party. For internal evidence it is a VERIQO principal;
	// for external evidence it is the outside organisation.
	ID contract.ID `json:"id"`
	// Name is the human-readable party, which is what appears in a
	// report a customer reads.
	Name string `json:"name"`
	// External marks a party outside VERIQO. Setting it does not make
	// it true -- Independence() also requires that the party is not
	// the implementer and that somebody other than the party itself
	// attested to the relationship.
	External bool `json:"external"`
	// AttestedBy names who confirmed this party's independence. A
	// party attesting to its own independence is the oldest trick in
	// assurance and is refused.
	AttestedBy contract.ID `json:"attested_by,omitempty"`
	// Accreditation names the standard or body the validator works
	// under, when one applies. Empty is honest and common.
	Accreditation string `json:"accreditation,omitempty"`
}

func (v Validator) Validate() error {
	if strings.TrimSpace(string(v.ID)) == "" {
		return fmt.Errorf("%w: a validator has no id", ErrNoValidator)
	}
	if strings.TrimSpace(v.Name) == "" {
		return fmt.Errorf("%w: validator %s has no name", ErrNoValidator, v.ID)
	}
	if v.External {
		if strings.TrimSpace(string(v.AttestedBy)) == "" {
			return fmt.Errorf("%w: %s claims to be external and nobody attested to it",
				ErrNotIndependent, v.ID)
		}
		if v.AttestedBy == v.ID {
			return fmt.Errorf("%w: %s attested to its own independence",
				ErrNotIndependent, v.ID)
		}
	}
	return nil
}

// IndependentOf reports whether this validator is independent of a
// named implementer.
func (v Validator) IndependentOf(implementer contract.ID) bool {
	return v.Validate() == nil && v.External && v.ID != implementer
}

// Evidence is one piece of assurance evidence.
type Evidence struct {
	ID    contract.ID `json:"id"`
	Class Class       `json:"class"`
	// Summary is what the evidence says, in a sentence a reader who
	// was not there can assess.
	Summary string `json:"summary"`
	// Scope is what it covers -- and therefore what it does not. An
	// unscoped external report is read as covering everything.
	Scope string `json:"scope"`
	// Validator produced it.
	Validator Validator `json:"validator"`
	// At is when. An assurance statement with no date is a statement
	// about an unknown version of an unknown system.
	At time.Time `json:"at"`
	// ArtefactHash ties the evidence to what was examined, so a report
	// cannot silently be reused for a later version.
	ArtefactHash string `json:"artefact_hash"`
	// Exceptions are what the validator explicitly did NOT cover or
	// could not conclude. A report with no exceptions is either
	// extraordinary or unread.
	Exceptions []string `json:"exceptions,omitempty"`
	// Period, for operational evidence: how long it ran.
	Period string `json:"period,omitempty"`
}

func (e Evidence) Validate() error {
	if strings.TrimSpace(string(e.ID)) == "" {
		return fmt.Errorf("%w: evidence has no id", ErrNoEvidence)
	}
	if !e.Class.Valid() {
		return fmt.Errorf("%w: %q", ErrWrongClass, e.Class)
	}
	if strings.TrimSpace(e.Summary) == "" {
		return fmt.Errorf("%w: %s says nothing", ErrNoEvidence, e.ID)
	}
	if strings.TrimSpace(e.Scope) == "" {
		return fmt.Errorf("%w: %s states no scope; an unscoped assurance statement is read "+
			"as covering everything", ErrNoEvidence, e.ID)
	}
	if e.At.IsZero() {
		return fmt.Errorf("%w: %s carries no date", ErrNoEvidence, e.ID)
	}
	if strings.TrimSpace(e.ArtefactHash) == "" {
		return fmt.Errorf("%w: %s names no artefact, so it cannot be tied to a version",
			ErrNoEvidence, e.ID)
	}
	if err := e.Validator.Validate(); err != nil {
		return err
	}
	if e.Class.NeedsIndependentParty() && !e.Validator.External {
		return fmt.Errorf("%w: %s is class %s and its validator %s is internal",
			ErrSelfCertified, e.ID, e.Class, e.Validator.ID)
	}
	if e.Class == ProductionRequired && strings.TrimSpace(e.Period) == "" {
		return fmt.Errorf("%w: %s is operational evidence and states no period; "+
			"'it ran' without 'for how long' is not operational evidence",
			ErrNoEvidence, e.ID)
	}
	return nil
}

// Transition is one recorded movement along the ladder.
type Transition struct {
	From State       `json:"from"`
	To   State       `json:"to"`
	By   contract.ID `json:"by"`
	At   time.Time   `json:"at"`
	// Reason is required in both directions. A demotion without a
	// reason is indistinguishable from an accident.
	Reason string `json:"reason"`
	// Evidence backs a promotion. A demotion cites none.
	Evidence []Evidence `json:"evidence,omitempty"`
}

// Machine is one control's position on the ladder, with its history.
type Machine struct {
	// Subject is the control this machine tracks.
	Subject string `json:"subject"`
	// Implementer is the party that built it. Law 11 is expressed as a
	// comparison against this field, so it is required.
	Implementer contract.ID `json:"implementer"`

	current State
	history []Transition
}

// New starts a machine at UNDEFINED.
func New(subject string, implementer contract.ID) (*Machine, error) {
	if strings.TrimSpace(subject) == "" {
		return nil, errors.New("assurance/state: a machine must name its subject")
	}
	if strings.TrimSpace(string(implementer)) == "" {
		return nil, errors.New("assurance/state: a machine must name its implementer; " +
			"Law 11 is a comparison against that party and cannot be evaluated without it")
	}
	return &Machine{Subject: subject, Implementer: implementer}, nil
}

// State returns the current rung.
func (m *Machine) State() State { return m.current }

// History returns a copy of every transition, in order.
func (m *Machine) History() []Transition { return append([]Transition(nil), m.history...) }

// Promote moves up exactly one rung.
//
// The checks, in the order they are applied:
//
//  1. exactly one rung -- no jumps, in either direction;
//  2. the promoter is active at the instant;
//  3. the target's required evidence class is cited by at least one
//     piece of valid evidence;
//  4. Law 11 -- where that class needs an independent party, the
//     evidence's validator must be external, attested, and not the
//     implementer.
func (m *Machine) Promote(to State, by identity.Principal, at time.Time, reason string,
	ev ...Evidence) error {

	if !to.Valid() {
		return fmt.Errorf("%w: %v", ErrUnknownState, to)
	}
	if to <= m.current {
		return fmt.Errorf("%w: %s is at %s and the promotion targets %s",
			ErrBackwards, m.Subject, m.current, to)
	}
	if to != m.current+1 {
		return fmt.Errorf("%w: %s is at %s and the promotion targets %s, skipping %s. "+
			"Every skipped rung is a question nobody answered",
			ErrStateJump, m.Subject, m.current, to, skipped(m.current, to))
	}
	if at.IsZero() {
		return errors.New("assurance/state: a promotion carries no instant")
	}
	if err := by.Validate(); err != nil {
		return err
	}
	if err := by.Active(at); err != nil {
		return err
	}
	if strings.TrimSpace(reason) == "" {
		return errors.New("assurance/state: a promotion states no reason")
	}

	want := to.RequiredEvidence()
	var matching []Evidence
	for _, e := range ev {
		if err := e.Validate(); err != nil {
			return err
		}
		if e.Class == want {
			matching = append(matching, e)
		}
	}
	if len(matching) == 0 {
		return fmt.Errorf("%w: %s requires evidence of class %s and none was cited",
			ErrWrongClass, to, want)
	}

	// Law 11. The check is on the EVIDENCE, not on the promoter,
	// because a promoter is always internal -- somebody at VERIQO
	// records the promotion. What must come from outside is the thing
	// being recorded.
	if want.NeedsIndependentParty() {
		ok := false
		for _, e := range matching {
			if e.Validator.IndependentOf(m.Implementer) {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("%w: %s -> %s requires %s, and no cited evidence has a "+
				"validator independent of the implementer %s",
				ErrSelfCertified, m.current, to, want, m.Implementer)
		}
	}

	m.history = append(m.history, Transition{From: m.current, To: to, By: by.ID,
		At: at, Reason: reason, Evidence: matching})
	m.current = to
	return nil
}

// Demote moves down any distance.
//
// It needs a reason and no evidence, and it may skip rungs, because
// withdrawing a claim is not a claim. Making demotion as hard as
// promotion is how systems end up holding stale assurance: the honest
// move becomes the expensive one.
func (m *Machine) Demote(to State, by contract.ID, at time.Time, reason string) error {
	if !to.Valid() {
		return fmt.Errorf("%w: %v", ErrUnknownState, to)
	}
	if to >= m.current {
		return fmt.Errorf("assurance/state: %s is at %s; %s is not a demotion",
			m.Subject, m.current, to)
	}
	if strings.TrimSpace(reason) == "" {
		return errors.New("assurance/state: a demotion states no reason; an unexplained " +
			"demotion is indistinguishable from an accident")
	}
	if at.IsZero() {
		return errors.New("assurance/state: a demotion carries no instant")
	}
	m.history = append(m.history, Transition{From: m.current, To: to, By: by, At: at,
		Reason: reason})
	m.current = to
	return nil
}

// skipped names the rungs a jump would have passed over.
func skipped(from, to State) string {
	var names []string
	for s := from + 1; s < to; s++ {
		names = append(names, s.String())
	}
	return strings.Join(names, ", ")
}

// SelfCertified reports whether this machine has reached a state that
// requires independence WITHOUT any independent evidence in its
// history.
//
// It should always be false. It exists so that the property can be
// asserted over a whole register rather than trusted per promotion.
func (m *Machine) SelfCertified() bool {
	for _, t := range m.history {
		if !t.To.RequiredEvidence().NeedsIndependentParty() {
			continue
		}
		ok := false
		for _, e := range t.Evidence {
			if e.Validator.IndependentOf(m.Implementer) {
				ok = true
				break
			}
		}
		if !ok {
			return true
		}
	}
	return false
}

// Describe renders the machine for a reader.
func (m *Machine) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n  state:       %s\n  implementer: %s\n", m.Subject, m.current, m.Implementer)
	if m.current.SelfReachable() {
		b.WriteString("  independence: none required at this state, and none claimed\n")
	}
	for _, t := range m.history {
		fmt.Fprintf(&b, "  %s -> %s by %s: %s\n", t.From, t.To, t.By, t.Reason)
		for _, e := range t.Evidence {
			who := "internal"
			if e.Validator.External {
				who = e.Validator.Name + " (external, attested by " + string(e.Validator.AttestedBy) + ")"
			}
			fmt.Fprintf(&b, "      %s [%s] %s -- scope: %s\n", e.ID, who, e.Summary, e.Scope)
			for _, x := range e.Exceptions {
				fmt.Fprintf(&b, "        exception: %s\n", x)
			}
		}
	}
	return b.String()
}
