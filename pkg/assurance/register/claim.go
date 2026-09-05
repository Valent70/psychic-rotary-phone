// Package register makes assurance a first-class object rather than a
// spreadsheet.
//
// # Why this is not project management
//
// Assurance normally lives in a tracker: a row, an owner, a status, a
// date. That representation has one fatal property -- every row looks
// the same. "Implemented and tested by the author" and "attacked by an
// accredited third party under a signed scope" occupy the same green
// cell, and by the time the information reaches an investor, a
// customer or a regulator, the difference has been erased.
//
// Here an assurance claim is an object with the same discipline VERIQO
// applies to intelligence claims: it states what it asserts, what
// would disprove it, what evidence supports it, who produced that
// evidence, whether that party was independent, what the evidence does
// NOT cover, and what has already been found against it. It carries a
// required level and a current level, and the distance between them is
// the honest measure of how far there is to go.
//
// # The three registers are one graph
//
// A gate, a control and an assurance claim are not three lists. A gate
// is satisfied by controls; a control is asserted by claims; a claim
// is supported by evidence; evidence is produced by a validator; the
// validator's independence decides what level the claim may reach; and
// the release decision reads the whole chain. Graph walks that chain,
// and refuses a release whose support does not actually reach.
package register

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
	ErrNoAssertion    = errors.New("assurance/register: a claim asserts nothing")
	ErrNoDisproof     = errors.New("assurance/register: a claim states no disproof path")
	ErrPathsIdentical = errors.New("assurance/register: the disproof path restates the assertion")
	ErrOverclaim      = errors.New("assurance/register: the claim's current level outruns its evidence")
	ErrExpired        = errors.New("assurance/register: the claim's evidence has expired")
	ErrUnmetRequired  = errors.New("assurance/register: the claim has not reached its required level")
	ErrOpenCounter    = errors.New("assurance/register: a claim with an open counterexample may not hold an assured level")
)

// Claim is one assertion about a control, with everything that
// qualifies it.
//
// The field set is deliberately the same shape as an intelligence
// claim: assertion, disproof path, evidence, limitations,
// counterexamples. A system that applies one standard to what it says
// about the world and a weaker one to what it says about itself is
// not a proof platform.
type Claim struct {
	ID      contract.ID `json:"claim_id"`
	Subject string      `json:"subject"`
	// Assertion is what is claimed, in a form that could be false.
	Assertion string `json:"assertion"`

	// RequiredLevel is the rung this claim MUST reach before the thing
	// it supports may be released. It is set from the risk, not from
	// what happens to be achievable.
	RequiredLevel state.State `json:"required_level"`
	// CurrentLevel is where it actually is.
	CurrentLevel state.State `json:"current_level"`

	// Evidence supports the current level.
	Evidence []state.Evidence `json:"evidence,omitempty"`
	// DisproofPath is how somebody would try to make this claim false.
	// Not a negative test -- an attempt to produce a counterexample.
	DisproofPath string `json:"disproof_path"`

	// Environment is where the evidence was produced. "A developer
	// laptop" and "a production-equivalent cluster" support very
	// different claims.
	Environment string `json:"environment"`
	// Scope is what the claim covers, and therefore what it does not.
	Scope string `json:"scope"`

	// At is when the claim was last assessed; Expiry is when that
	// assessment stops being current. An assurance claim with no
	// expiry is a claim that the world stopped changing.
	At     time.Time  `json:"date"`
	Expiry *time.Time `json:"expiry,omitempty"`

	// Counterexamples are OPEN findings against this claim. A claim
	// carrying one may not hold a level above IMPLEMENTED.
	Counterexamples []string `json:"counterexamples,omitempty"`
	// ClosedCounterexamples were found and fixed. They are kept
	// because a disproof path that has produced something is known to
	// be capable of producing something.
	ClosedCounterexamples []string `json:"closed_counterexamples,omitempty"`
	// Limitations are what the claim does not establish even where it
	// holds.
	Limitations []string `json:"limitations,omitempty"`

	// Controls names the implementation this claim is about.
	Controls []string `json:"controls,omitempty"`
	// Gates names the production gates this claim contributes to.
	Gates []string `json:"gates,omitempty"`
	// Debts names the evidence debts blocking it.
	Debts []contract.ID `json:"debts,omitempty"`

	// Implementer built the control. Law 11 compares against it.
	Implementer contract.ID `json:"implementer"`
	// PromotionHistory is every movement of CurrentLevel.
	PromotionHistory []state.Transition `json:"promotion_history,omitempty"`
}

// Validator returns the party behind the strongest evidence, and
// whether that party was independent of the implementer.
//
// It returns both because either alone misleads: a named external firm
// that turns out to be the implementer's own subsidiary, or an
// independence flag with nobody attached to it.
func (c Claim) Validator() (state.Validator, bool) {
	var best state.Validator
	var found, independent bool
	for _, e := range c.Evidence {
		if !found || e.Class.NeedsIndependentParty() {
			best, found = e.Validator, true
		}
		if e.Validator.IndependentOf(c.Implementer) {
			best, independent = e.Validator, true
			break
		}
	}
	return best, independent
}

// Validate refuses a claim that cannot be assessed or that outruns its
// support.
func (c Claim) Validate() error {
	if strings.TrimSpace(string(c.ID)) == "" {
		return fmt.Errorf("%w: a claim has no id", contract.ErrMalformedID)
	}
	if strings.TrimSpace(c.Assertion) == "" {
		return fmt.Errorf("%w: %s", ErrNoAssertion, c.ID)
	}
	if strings.TrimSpace(c.DisproofPath) == "" {
		return fmt.Errorf("%w: %s", ErrNoDisproof, c.ID)
	}
	if normalise(c.Assertion) == normalise(c.DisproofPath) {
		return fmt.Errorf("%w: %s", ErrPathsIdentical, c.ID)
	}
	if strings.TrimSpace(c.Scope) == "" {
		return fmt.Errorf("%w: %s states no scope", ErrNoAssertion, c.ID)
	}
	if strings.TrimSpace(c.Environment) == "" {
		return fmt.Errorf("%w: %s names no environment; evidence from a laptop and evidence "+
			"from a production cluster support different claims", ErrNoAssertion, c.ID)
	}
	if !c.RequiredLevel.Valid() || !c.CurrentLevel.Valid() {
		return fmt.Errorf("assurance/register: %s has an unknown level", c.ID)
	}
	if strings.TrimSpace(string(c.Implementer)) == "" {
		return fmt.Errorf("assurance/register: %s names no implementer", c.ID)
	}
	if c.At.IsZero() {
		return fmt.Errorf("assurance/register: %s carries no assessment date", c.ID)
	}

	// An open counterexample caps the level, exactly as it does in the
	// self-doubt register.
	if len(c.Counterexamples) > 0 && c.CurrentLevel > state.Implemented {
		return fmt.Errorf("%w: %s holds %s with %d open counterexample(s)",
			ErrOpenCounter, c.ID, c.CurrentLevel, len(c.Counterexamples))
	}

	// The current level must actually be supported by evidence of the
	// class that level requires -- and where that class needs an
	// independent party, by evidence from one.
	if c.CurrentLevel > state.Undefined {
		want := c.CurrentLevel.RequiredEvidence()
		supported, independent := false, !want.NeedsIndependentParty()
		for _, e := range c.Evidence {
			if err := e.Validate(); err != nil {
				return fmt.Errorf("assurance/register: %s: %w", c.ID, err)
			}
			if e.Class == want {
				supported = true
				if e.Validator.IndependentOf(c.Implementer) {
					independent = true
				}
			}
		}
		// SPECIFIED and IMPLEMENTED are readable from the repository
		// itself; requiring a separate evidence record for them would
		// make the register clerical rather than informative.
		if c.CurrentLevel > state.Implemented {
			if !supported {
				return fmt.Errorf("%w: %s holds %s and cites no %s evidence",
					ErrOverclaim, c.ID, c.CurrentLevel, want)
			}
			if !independent {
				return fmt.Errorf("%w: %s holds %s, which requires %s, and no cited evidence "+
					"has a validator independent of %s",
					state.ErrSelfCertified, c.ID, c.CurrentLevel, want, c.Implementer)
			}
		}
	}
	return nil
}

// Current reports whether the claim's evidence has not expired as of
// an instant.
func (c Claim) Current(at time.Time) bool {
	return c.Expiry == nil || at.Before(*c.Expiry)
}

// Met reports whether the claim has reached the level its risk
// requires, as of an instant.
func (c Claim) Met(at time.Time) bool {
	return c.Validate() == nil && c.CurrentLevel >= c.RequiredLevel && c.Current(at)
}

// Shortfall says what is missing, in words, when Met is false.
func (c Claim) Shortfall(at time.Time) string {
	if err := c.Validate(); err != nil {
		return err.Error()
	}
	if !c.Current(at) {
		return fmt.Sprintf("%s: the evidence expired on %s and has not been renewed",
			c.ID, c.Expiry.Format("2006-01-02"))
	}
	if c.CurrentLevel < c.RequiredLevel {
		need := c.CurrentLevel + 1
		return fmt.Sprintf("%s: at %s, needs %s. The next rung is %s, which requires %s",
			c.ID, c.CurrentLevel, c.RequiredLevel, need, need.RequiredEvidence())
	}
	return ""
}

// Yielded reports whether attacking this claim has ever produced
// anything -- open or closed.
func (c Claim) Yielded() bool {
	return len(c.Counterexamples) > 0 || len(c.ClosedCounterexamples) > 0
}

// Describe renders a claim the way a customer asking "prove it" should
// see it.
func (c Claim) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", c.ID)
	fmt.Fprintf(&b, "  claim:        %s\n", c.Assertion)
	fmt.Fprintf(&b, "  subject:      %s\n", c.Subject)
	fmt.Fprintf(&b, "  level:        %s (required: %s)\n", c.CurrentLevel, c.RequiredLevel)
	fmt.Fprintf(&b, "  scope:        %s\n", c.Scope)
	fmt.Fprintf(&b, "  environment:  %s\n", c.Environment)
	fmt.Fprintf(&b, "  disproof:     %s\n", c.DisproofPath)
	if v, independent := c.Validator(); strings.TrimSpace(string(v.ID)) != "" {
		who := "internal -- no independent party has examined this"
		if independent {
			who = v.Name + " (independent, attested by " + string(v.AttestedBy) + ")"
		}
		fmt.Fprintf(&b, "  validator:    %s\n", who)
	} else {
		b.WriteString("  validator:    none -- no evidence record names a party\n")
	}
	for _, e := range c.Evidence {
		fmt.Fprintf(&b, "    evidence %s [%s] %s\n", e.ID, e.Class, e.Summary)
		for _, x := range e.Exceptions {
			fmt.Fprintf(&b, "        the validator did not cover: %s\n", x)
		}
	}
	for _, x := range c.Counterexamples {
		fmt.Fprintf(&b, "  OPEN COUNTEREXAMPLE: %s\n", x)
	}
	for _, x := range c.ClosedCounterexamples {
		fmt.Fprintf(&b, "  counterexample found and closed: %s\n", x)
	}
	for _, l := range c.Limitations {
		fmt.Fprintf(&b, "  limitation:   %s\n", l)
	}
	for _, d := range c.Debts {
		fmt.Fprintf(&b, "  blocked by:   %s\n", d)
	}
	if !c.Yielded() {
		b.WriteString("  note:         this disproof path has never produced a counterexample, " +
			"which is not the same as there being none to find\n")
	}
	return b.String()
}

func normalise(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// SortClaims orders claims by id, so a report is stable between runs.
func SortClaims(cs []Claim) {
	sort.Slice(cs, func(i, j int) bool { return cs[i].ID < cs[j].ID })
}
