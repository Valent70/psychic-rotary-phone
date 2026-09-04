// Package reverseproof is the VERIQO reverse proof engine.
//
// # The inversion
//
// Ordinary analysis asks: what does this evidence show? Reverse proof
// asks the question backwards:
//
//	if this claim were TRUE, what would have to be true, and what
//	would we therefore expect to observe?
//
// The difference is not stylistic. Forward analysis is bounded by the
// evidence somebody happened to collect, so it cannot distinguish
// "we looked and it was there" from "we never looked". Reverse proof
// derives the expectation FIRST, from the claim, and then reports the
// gap. That gap is the most valuable output VERIQO produces: it is the
// difference between "we do not know" and "we do not know X, and this
// is the evidence that would settle it".
//
// # The chain
//
//	Claim -> required conditions -> expected observations
//	      -> available sources -> acquisition feasibility
//	      -> observed -> missing -> contradicted
//	      -> alternative hypotheses -> qualification
//
// # A condition is necessary, not sufficient
//
// Every Condition here is something that MUST hold for the claim to
// hold. That is what makes the logic sound in the direction it is
// used: one refuted necessary condition refutes the claim, whereas any
// number of satisfied ones do not establish it. The type enforces the
// asymmetry -- Verdict() can conclude REFUTED and never CONFIRMED.
package reverseproof

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"veriqo/pkg/claim"
	"veriqo/pkg/contract"
)

var (
	ErrNoConditions     = errors.New("reverseproof: a claim with no necessary conditions has not been decomposed")
	ErrNoExpectation    = errors.New("reverseproof: a condition must say what would be observed if it held")
	ErrNoClaim          = errors.New("reverseproof: no claim")
	ErrDuplicateID      = errors.New("reverseproof: duplicate condition id")
	ErrUnknownCondition = errors.New("reverseproof: unknown condition")
)

// State is what the evidence says about one condition.
type State string

const (
	// Unexamined is the zero value: nobody has looked for the
	// expected observation.
	Unexamined State = ""
	// Satisfied: the expected observation was found.
	Satisfied State = "SATISFIED"
	// Refuted: the opposite was observed. One of these refutes the
	// claim, because the condition is necessary.
	Refuted State = "REFUTED"
	// NotObserved: it was looked for and not found. Law 5 -- this is
	// NOT refutation, and keeping them apart is the point.
	NotObserved State = "NOT_OBSERVED"
	// Unobtainable: no source can supply it. Distinct from
	// NOT_OBSERVED because acquiring more evidence will not help.
	Unobtainable State = "UNOBTAINABLE"
	// Assumed: taken as holding without evidence. Permitted, and
	// counted, because a decomposition with hidden assumptions is
	// worse than one with stated ones.
	Assumed State = "ASSUMED"
)

func States() []State {
	return []State{Satisfied, Refuted, NotObserved, Unobtainable, Assumed, Unexamined}
}

func (s State) Valid() bool {
	for _, x := range States() {
		if x == s {
			return true
		}
	}
	return false
}

func (s State) String() string {
	if s == Unexamined {
		return "UNEXAMINED"
	}
	return string(s)
}

// Examined reports whether anybody looked. NOT_OBSERVED means they
// did; UNEXAMINED means they did not; and the two must never be
// counted together.
func (s State) Examined() bool { return s != Unexamined }

// Condition is one thing that must hold for the claim to hold.
type Condition struct {
	ID   string `json:"id"`
	Must string `json:"must"`
	// Expected is what would be observed if it held. A condition
	// without one cannot be looked for, so it cannot be assessed and
	// cannot be part of a proof.
	Expected string `json:"expected"`

	// Sources names where the expected observation could come from.
	// An empty list is what makes a condition UNOBTAINABLE, and the
	// planner reads it.
	Sources []string `json:"sources,omitempty"`

	// Diagnosticity is how much settling this condition would settle
	// the claim, 0..1. A condition every hypothesis predicts equally
	// is worth little however cheap it is to obtain.
	Diagnosticity float64 `json:"diagnosticity"`
	// AcquisitionCost is a coarse relative cost, 0..1.
	AcquisitionCost float64 `json:"acquisition_cost"`
	// LegallyAccessible marks evidence VERIQO may lawfully obtain.
	LegallyAccessible bool `json:"legally_accessible"`

	State        State    `json:"state"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
	Note         string   `json:"note,omitempty"`
}

func (c Condition) Validate() error {
	if strings.TrimSpace(c.ID) == "" {
		return errors.New("reverseproof: a condition has no id")
	}
	if strings.TrimSpace(c.Must) == "" {
		return fmt.Errorf("reverseproof: condition %s states nothing", c.ID)
	}
	if strings.TrimSpace(c.Expected) == "" {
		return fmt.Errorf("%w: %s", ErrNoExpectation, c.ID)
	}
	if !c.State.Valid() {
		return fmt.Errorf("reverseproof: condition %s has unknown state %q", c.ID, c.State)
	}
	if c.Diagnosticity < 0 || c.Diagnosticity > 1 {
		return fmt.Errorf("reverseproof: condition %s has diagnosticity %v outside 0..1",
			c.ID, c.Diagnosticity)
	}
	if c.State == Satisfied && len(c.EvidenceRefs) == 0 {
		return fmt.Errorf("reverseproof: condition %s is SATISFIED and cites no evidence", c.ID)
	}
	if c.State == Refuted && len(c.EvidenceRefs) == 0 {
		return fmt.Errorf("reverseproof: condition %s is REFUTED and cites no evidence; "+
			"a refutation with no evidence is an opinion", c.ID)
	}
	if c.State == Assumed && strings.TrimSpace(c.Note) == "" {
		return fmt.Errorf("reverseproof: condition %s is ASSUMED and does not say why; "+
			"a stated assumption is the only acceptable kind", c.ID)
	}
	return nil
}

// Verdict is what the decomposition concludes.
type Verdict string

const (
	// Refuted: at least one necessary condition was refuted.
	VerdictRefuted Verdict = "REFUTED"
	// Consistent: every condition examined so far is satisfied, and
	// some remain unexamined or unobtainable.
	//
	// It is deliberately NOT called "confirmed". Satisfying necessary
	// conditions does not establish a claim, and a word that suggested
	// it would let a proof structure be read as a proof.
	VerdictConsistent Verdict = "CONSISTENT_SO_FAR"
	// FullyChecked: every condition is satisfied and none is
	// outstanding. Still not "proved": the decomposition may be
	// incomplete, which is why Incompleteness() is reported alongside.
	VerdictFullyChecked Verdict = "ALL_CONDITIONS_SATISFIED"
	// Incomplete: conditions remain unexamined.
	VerdictIncomplete Verdict = "INCOMPLETE"
	// Undecidable: the outstanding conditions are unobtainable, so
	// more work will not settle it.
	VerdictUndecidable Verdict = "UNDECIDABLE_ON_AVAILABLE_EVIDENCE"
)

// Proof is a claim decomposed into its necessary conditions.
type Proof struct {
	ID       contract.ID `json:"id"`
	ClaimID  contract.ID `json:"claim_id"`
	TenantID string      `json:"tenant_id"`

	Statement  string      `json:"statement"`
	Conditions []Condition `json:"conditions"`

	// Alternatives are the other explanations for the same
	// observations. A decomposition with none has not asked what else
	// would produce this evidence.
	Alternatives []string `json:"alternatives,omitempty"`

	Versions contract.VersionSet `json:"versions"`
}

// New builds a proof from a claim and its decomposition.
func New(c claim.Claim, id contract.ID, conditions []Condition, alternatives []string) (*Proof, error) {
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoClaim, err)
	}
	if len(conditions) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoConditions, c.ID)
	}
	seen := map[string]bool{}
	for _, cond := range conditions {
		if err := cond.Validate(); err != nil {
			return nil, err
		}
		if seen[cond.ID] {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateID, cond.ID)
		}
		seen[cond.ID] = true
	}
	return &Proof{
		ID: id, ClaimID: c.ID, TenantID: c.TenantID,
		Statement: c.Statement, Conditions: append([]Condition(nil), conditions...),
		Alternatives: append([]string(nil), alternatives...),
		Versions:     c.Versions,
	}, nil
}

// Verdict reads the conditions.
//
// The order is the argument. REFUTED is checked first: one refuted
// necessary condition refutes the claim, and no amount of satisfaction
// elsewhere changes that. There is no branch that returns "proved",
// because satisfying necessary conditions does not establish a claim.
func (p *Proof) Verdict() Verdict {
	var refuted, unexamined, unobtainable, satisfied, notObserved int
	for _, c := range p.Conditions {
		switch c.State {
		case Refuted:
			refuted++
		case Unexamined:
			unexamined++
		case Unobtainable:
			unobtainable++
		case Satisfied, Assumed:
			satisfied++
		case NotObserved:
			notObserved++
		}
	}
	switch {
	case refuted > 0:
		return VerdictRefuted
	case unexamined > 0:
		return VerdictIncomplete
	case satisfied == len(p.Conditions):
		return VerdictFullyChecked
	case unobtainable > 0 && notObserved+unobtainable > 0:
		return VerdictUndecidable
	default:
		return VerdictConsistent
	}
}

// Missing returns the conditions that were looked for and not found,
// plus the ones nobody looked at, kept apart.
func (p *Proof) Missing() (notObserved, unexamined []Condition) {
	for _, c := range p.Conditions {
		switch c.State {
		case NotObserved:
			notObserved = append(notObserved, c)
		case Unexamined:
			unexamined = append(unexamined, c)
		}
	}
	return
}

// Refutations returns the conditions that were refuted, with the
// evidence that refuted them.
func (p *Proof) Refutations() []Condition {
	var out []Condition
	for _, c := range p.Conditions {
		if c.State == Refuted {
			out = append(out, c)
		}
	}
	return out
}

// Assumptions returns the conditions taken as holding without
// evidence. A decomposition with many of these is a chain of
// assumptions wearing the shape of a proof.
func (p *Proof) Assumptions() []Condition {
	var out []Condition
	for _, c := range p.Conditions {
		if c.State == Assumed {
			out = append(out, c)
		}
	}
	return out
}

// Completeness is the share of conditions that were examined at all.
//
// It is reported alongside every verdict, because ALL_CONDITIONS_
// SATISFIED over a decomposition that only listed the easy conditions
// is a strong-looking result about a weak question.
func (p *Proof) Completeness() float64 {
	if len(p.Conditions) == 0 {
		return 0
	}
	n := 0
	for _, c := range p.Conditions {
		if c.State.Examined() {
			n++
		}
	}
	return float64(n) / float64(len(p.Conditions))
}

// Priority is one item of the acquisition plan.
type Priority struct {
	Condition Condition
	// Value is diagnosticity x accessibility / cost. It ranks what to
	// obtain next, and it is deliberately simple: a sophisticated
	// formula over three coarse inputs would imply a precision the
	// inputs do not have.
	Value  float64
	Reason string
}

// Plan turns the gap into an acquisition plan.
//
// This is the output that turns "we do not know" into "we do not know
// X, and this is the most valuable evidence that would settle it".
// Conditions that are UNOBTAINABLE or not legally accessible are
// excluded with the reason stated, rather than silently dropped --
// a plan that omits them looks achievable and is not.
func (p *Proof) Plan() (actionable []Priority, blocked []Priority) {
	for _, c := range p.Conditions {
		if c.State == Satisfied || c.State == Refuted {
			continue
		}
		switch {
		case len(c.Sources) == 0 || c.State == Unobtainable:
			blocked = append(blocked, Priority{Condition: c, Value: 0,
				Reason: "no source can supply the expected observation; acquiring more " +
					"evidence will not settle this condition"})
		case !c.LegallyAccessible:
			blocked = append(blocked, Priority{Condition: c, Value: 0,
				Reason: "the evidence exists and VERIQO may not lawfully obtain it"})
		default:
			cost := c.AcquisitionCost
			if cost <= 0 {
				cost = 0.01
			}
			actionable = append(actionable, Priority{Condition: c,
				Value: c.Diagnosticity / cost,
				Reason: fmt.Sprintf("diagnosticity %.2f at cost %.2f from %s",
					c.Diagnosticity, c.AcquisitionCost, strings.Join(c.Sources, ", "))})
		}
	}
	sort.Slice(actionable, func(i, j int) bool {
		if actionable[i].Value != actionable[j].Value {
			return actionable[i].Value > actionable[j].Value
		}
		return actionable[i].Condition.ID < actionable[j].Condition.ID
	})
	sort.Slice(blocked, func(i, j int) bool {
		return blocked[i].Condition.ID < blocked[j].Condition.ID
	})
	return
}

// Set records a condition's state.
func (p *Proof) Set(id string, state State, evidenceRefs []string, note string) error {
	for i, c := range p.Conditions {
		if c.ID != id {
			continue
		}
		next := c
		next.State = state
		next.EvidenceRefs = evidenceRefs
		next.Note = note
		if err := next.Validate(); err != nil {
			return err
		}
		p.Conditions[i] = next
		return nil
	}
	return fmt.Errorf("%w: %s", ErrUnknownCondition, id)
}

// ApplyTo updates a claim from the proof.
//
// A refuted necessary condition demotes the claim to CONTRADICTED --
// automatically, because the failure mode is that somebody records the
// refutation and leaves the conclusion standing.
func (p *Proof) ApplyTo(c claim.Claim) (claim.Claim, error) {
	if c.ID != p.ClaimID {
		return claim.Claim{}, fmt.Errorf("reverseproof: %s decomposes %s, not %s",
			p.ID, p.ClaimID, c.ID)
	}
	out := c
	out.ReverseProofRef = string(p.ID)
	out.AlternativeHypotheses = mergeStrings(out.AlternativeHypotheses, p.Alternatives)

	// Everything the decomposition expected becomes the claim's
	// expected evidence, which is what makes the gap visible on the
	// claim itself.
	for _, cond := range p.Conditions {
		out.ExpectedEvidence = appendUnique(out.ExpectedEvidence, cond.ID+": "+cond.Expected)
	}
	notObserved, _ := p.Missing()
	for _, cond := range notObserved {
		out.MissingEvidence = appendUnique(out.MissingEvidence, cond.ID+": "+cond.Expected)
	}

	if refs := p.Refutations(); len(refs) > 0 {
		for _, cond := range refs {
			for _, ev := range cond.EvidenceRefs {
				var err error
				out, err = out.RecordContradiction(ev,
					fmt.Sprintf("necessary condition %s (%s) was refuted", cond.ID, cond.Must))
				if err != nil {
					return claim.Claim{}, err
				}
			}
		}
		if out.Status != claim.Contradicted {
			out.Status = claim.Contradicted
		}
	}
	return out, nil
}

// Report renders the decomposition.
func (p *Proof) Report() string {
	var b strings.Builder
	fmt.Fprintf(&b, "REVERSE PROOF %s\n  claim: %s\n", p.ID, p.Statement)
	fmt.Fprintf(&b, "  verdict: %s (completeness %.0f%%)\n", p.Verdict(), p.Completeness()*100)
	for _, c := range p.Conditions {
		fmt.Fprintf(&b, "    %-6s %-14s diag=%.2f  %s\n", c.ID, c.State, c.Diagnosticity, c.Must)
		fmt.Fprintf(&b, "           expected: %s\n", c.Expected)
		if c.Note != "" {
			fmt.Fprintf(&b, "           note: %s\n", c.Note)
		}
	}
	actionable, blocked := p.Plan()
	if len(actionable) > 0 {
		b.WriteString("  most valuable evidence to obtain next:\n")
		for _, pr := range actionable {
			fmt.Fprintf(&b, "    %-6s value=%.2f  %s\n", pr.Condition.ID, pr.Value, pr.Reason)
		}
	}
	if len(blocked) > 0 {
		b.WriteString("  cannot be settled by acquiring evidence:\n")
		for _, pr := range blocked {
			fmt.Fprintf(&b, "    %-6s %s\n", pr.Condition.ID, pr.Reason)
		}
	}
	if len(p.Alternatives) > 0 {
		fmt.Fprintf(&b, "  alternatives: %s\n", strings.Join(p.Alternatives, "; "))
	}
	b.WriteString("  NOTE: satisfying necessary conditions does not establish the claim. " +
		"One refuted condition refutes it; any number of satisfied ones do not prove it.\n")
	return b.String()
}

func appendUnique(xs []string, v string) []string {
	for _, x := range xs {
		if x == v {
			return xs
		}
	}
	return append(append([]string(nil), xs...), v)
}

func mergeStrings(a, b []string) []string {
	out := append([]string(nil), a...)
	for _, v := range b {
		out = appendUnique(out, v)
	}
	sort.Strings(out)
	return out
}
