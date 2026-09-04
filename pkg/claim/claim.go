// Package claim is the VERIQO claim engine.
//
// # Every material conclusion is a Claim object
//
// Not a string, not a boolean, not a confidence score. A claim carries
// what it asserts, what supports it, what CONTRADICTS it, what is
// MISSING, and what it does not cover -- and the type refuses to exist
// without the last three.
//
// The reason is Law 4 and it is not a formality. A conclusion that
// records only its supporting evidence is indistinguishable, on the
// page, from one where nobody looked for the opposite. The reader
// cannot tell, the analyst six months later cannot tell, and the
// opposing expert can.
//
// # The seven statuses, and why INCONCLUSIVE is not a failure
//
//	SUPPORTED            evidence establishes it
//	QUALIFIED            establishes it, within stated limits
//	PARTIALLY_SUPPORTED  part of it is established, part is not
//	INCONCLUSIVE         the evidence was examined and does not decide
//	CONTRADICTED         evidence argues against it
//	UNRESOLVED           the work has not been done
//	NOT_DETERMINED       the zero value: nobody has looked at all
//
// INCONCLUSIVE and UNRESOLVED are different and the difference is the
// whole discipline: one is a finding after work, the other is an
// absence of work. A system with one word for both cannot tell a
// customer whether acquiring more evidence would help.
//
// # Absence is not negative evidence (Law 5)
//
//	not observed  !=  observed absent  !=  contradicted
//
// MissingEvidence is where "we did not find it" lives. Contradicting
// is where "we found the opposite" lives. Putting a missing item in
// the contradicting list is the most common way an argument is
// overstated, and Validate refuses an item that appears in both.
package claim

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"veriqo/pkg/canonical/jcs"
	"veriqo/pkg/contract"
)

var (
	ErrNoStatement        = errors.New("claim: a claim must state what it asserts")
	ErrNoScope            = errors.New("claim: a claim must state what it covers")
	ErrNoEvidence         = errors.New("claim: a material claim must cite evidence")
	ErrNoDisproofPath     = errors.New("claim: a claim must name what would make it false")
	ErrOverlappingSets    = errors.New("claim: evidence appears as both supporting and contradicting")
	ErrMissingIsNotContra = errors.New("claim: an item appears as both missing and contradicting; " +
		"not finding something is not finding the opposite")
	ErrUnstatedLimits   = errors.New("claim: QUALIFIED requires the limits it is qualified by")
	ErrContradictedHeld = errors.New("claim: contradicting evidence was recorded and the claim was not demoted")
	ErrUnknownStatus    = errors.New("claim: unknown status")
)

// Status is the claim's standing.
type Status string

const (
	// NotDetermined is the zero value. Nobody has looked.
	NotDetermined      Status = ""
	Supported          Status = "SUPPORTED"
	Qualified          Status = "QUALIFIED"
	PartiallySupported Status = "PARTIALLY_SUPPORTED"
	Inconclusive       Status = "INCONCLUSIVE"
	Contradicted       Status = "CONTRADICTED"
	Unresolved         Status = "UNRESOLVED"
)

func Statuses() []Status {
	return []Status{Supported, Qualified, PartiallySupported, Inconclusive,
		Contradicted, Unresolved, NotDetermined}
}

func (s Status) Valid() bool {
	for _, x := range Statuses() {
		if x == s {
			return true
		}
	}
	return false
}

func (s Status) String() string {
	if s == NotDetermined {
		return "NOT_DETERMINED"
	}
	return string(s)
}

// Establishes reports whether the status may found a material
// conclusion. QUALIFIED does -- within its limits, which is why the
// limits are mandatory.
func (s Status) Establishes() bool {
	return s == Supported || s == Qualified
}

// RestsOnWork reports whether the status was reached by examining
// evidence. UNRESOLVED and NOT_DETERMINED were not, and telling a
// customer which they are facing is the difference between "more
// evidence would help" and "we have not started".
func (s Status) RestsOnWork() bool {
	return s != Unresolved && s != NotDetermined
}

// Scope states what a claim covers, so that "the cargo was short" is
// not read as a statement about every voyage.
type Scope struct {
	Subject string            `json:"subject"`
	Period  contract.Interval `json:"period"`
	// Aspect narrows further: quantity, quality, timing, authority.
	Aspect string `json:"aspect,omitempty"`
}

func (s Scope) Validate() error {
	if strings.TrimSpace(s.Subject) == "" {
		return fmt.Errorf("%w: no subject", ErrNoScope)
	}
	if !s.Period.Valid() {
		return fmt.Errorf("%w: %s has no valid period", ErrNoScope, s.Subject)
	}
	return nil
}

func (s Scope) String() string {
	out := s.Subject
	if s.Aspect != "" {
		out += " (" + s.Aspect + ")"
	}
	if s.Period.To != nil {
		out += fmt.Sprintf(" [%s..%s]", s.Period.From.Format("2006-01-02"),
			s.Period.To.Format("2006-01-02"))
	} else {
		out += fmt.Sprintf(" [%s..open]", s.Period.From.Format("2006-01-02"))
	}
	return out
}

// Claim is a material assertion with everything that bears on it.
type Claim struct {
	ID       contract.ID `json:"id"`
	TenantID string      `json:"tenant_id"`
	CaseID   string      `json:"case_id"`

	Statement string `json:"statement"`
	Scope     Scope  `json:"scope"`

	SupportingEvidence    []string `json:"supporting_evidence"`
	ContradictingEvidence []string `json:"contradicting_evidence"`
	// MissingEvidence is what WOULD bear on the claim and was not
	// found. Law 5 lives here: this is not negative evidence.
	MissingEvidence []string `json:"missing_evidence"`
	// ExpectedEvidence is what a true claim implies should exist. The
	// gap between expected and supporting is what the acquisition
	// planner works on.
	ExpectedEvidence []string `json:"expected_evidence,omitempty"`

	HypothesisRefs   []string `json:"hypothesis_refs,omitempty"`
	ReverseProofRef  string   `json:"reverse_proof_ref,omitempty"`
	IndependenceRef  string   `json:"independence_ref,omitempty"`
	TrustRef         string   `json:"trust_ref,omitempty"`
	QualificationRef string   `json:"qualification_ref,omitempty"`

	// DisproofPath names what would make this claim FALSE. Law 4: a
	// claim with no disproof path has not been thought about, only
	// argued for.
	DisproofPath string `json:"disproof_path"`

	// AlternativeHypotheses names the explanations that were
	// considered and not adopted. An empty list on an established
	// claim means nobody looked for another explanation.
	AlternativeHypotheses []string `json:"alternative_hypotheses,omitempty"`

	Limitations []string `json:"limitations"`
	Status      Status   `json:"status"`

	Versions contract.VersionSet `json:"versions"`
}

// Validate enforces Laws 1, 4 and 5.
func (c Claim) Validate() error {
	if c.ID == "" {
		return fmt.Errorf("%w: claim has no id", contract.ErrMalformedID)
	}
	if err := c.ID.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(c.TenantID) == "" {
		return errors.New("claim: not anchored to a tenant")
	}
	if strings.TrimSpace(c.Statement) == "" {
		return ErrNoStatement
	}
	if err := c.Scope.Validate(); err != nil {
		return err
	}
	if !c.Status.Valid() {
		return fmt.Errorf("%w: %q", ErrUnknownStatus, c.Status)
	}

	// Law 4. Every claim, not only the established ones: a claim that
	// nobody could falsify is not a weaker claim, it is a different
	// kind of statement.
	if strings.TrimSpace(c.DisproofPath) == "" {
		return fmt.Errorf("%w: %s", ErrNoDisproofPath, c.ID)
	}

	// Law 5: the same item cannot be both absent and contrary.
	if overlap := intersect(c.MissingEvidence, c.ContradictingEvidence); len(overlap) > 0 {
		return fmt.Errorf("%w: %s", ErrMissingIsNotContra, strings.Join(overlap, ", "))
	}
	if overlap := intersect(c.SupportingEvidence, c.ContradictingEvidence); len(overlap) > 0 {
		return fmt.Errorf("%w: %s", ErrOverlappingSets, strings.Join(overlap, ", "))
	}

	// Law 1: a material conclusion needs evidence references.
	if c.Status.Establishes() && len(c.SupportingEvidence) == 0 {
		return fmt.Errorf("%w: %s is %s with no supporting evidence",
			ErrNoEvidence, c.ID, c.Status)
	}
	// The half most systems leave out: contradicting evidence exists
	// and the claim still says SUPPORTED.
	if c.Status == Supported && len(c.ContradictingEvidence) > 0 {
		return fmt.Errorf("%w: %s records %d contradicting item(s) and is SUPPORTED; "+
			"the status is at most PARTIALLY_SUPPORTED or CONTRADICTED",
			ErrContradictedHeld, c.ID, len(c.ContradictingEvidence))
	}
	if c.Status == Qualified && len(c.Limitations) == 0 {
		return fmt.Errorf("%w: %s", ErrUnstatedLimits, c.ID)
	}
	// An established claim that considered no alternative has not been
	// tested against the world, only assembled from it.
	if c.Status.Establishes() && len(c.AlternativeHypotheses) == 0 {
		return fmt.Errorf("claim: %s is %s and names no alternative hypothesis; "+
			"a conclusion nobody tried to explain differently has been assembled, not tested",
			c.ID, c.Status)
	}
	if !c.Versions.Complete() {
		return fmt.Errorf("%w: %v", contract.ErrUnversioned, c.Versions.Missing())
	}
	return nil
}

// Digest is the claim's hash, for the ledger and the passport.
func (c Claim) Digest() (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	return jcs.Hash(c)
}

// Demote lowers a claim's status because a counterexample was found.
//
// There is no Promote. A claim rises by being re-established through
// the qualification ladder, not by a status assignment -- which is the
// same asymmetry the temporal package enforces, for the same reason.
func (c Claim) Demote(to Status, reason string) (Claim, error) {
	if !to.Valid() {
		return Claim{}, fmt.Errorf("%w: %q", ErrUnknownStatus, to)
	}
	if to.Establishes() && !c.Status.Establishes() {
		return Claim{}, fmt.Errorf("claim: %s -> %s is a promotion; a claim is re-established "+
			"through qualification, not by relabelling", c.Status, to)
	}
	out := c
	out.Status = to
	out.Limitations = append(append([]string(nil), c.Limitations...),
		fmt.Sprintf("demoted from %s: %s", c.Status, reason))
	return out, nil
}

// RecordContradiction adds counter-evidence AND applies the demotion
// it requires.
//
// The two happen together deliberately. A method that only recorded
// the contradiction would leave the caller to remember the demotion,
// and the whole failure mode is that nobody remembers.
func (c Claim) RecordContradiction(evidenceRef, reason string) (Claim, error) {
	if strings.TrimSpace(evidenceRef) == "" {
		return Claim{}, errors.New("claim: a contradiction must name the evidence")
	}
	for _, m := range c.MissingEvidence {
		if m == evidenceRef {
			return Claim{}, fmt.Errorf("%w: %s", ErrMissingIsNotContra, evidenceRef)
		}
	}
	out := c
	out.ContradictingEvidence = appendUnique(out.ContradictingEvidence, evidenceRef)
	// Remove it from supporting if it was there: the same item cannot
	// do both jobs.
	out.SupportingEvidence = remove(out.SupportingEvidence, evidenceRef)

	switch {
	case len(out.SupportingEvidence) == 0:
		out.Status = Contradicted
	case out.Status.Establishes():
		out.Status = PartiallySupported
	}
	out.Limitations = append(append([]string(nil), c.Limitations...),
		fmt.Sprintf("contradicted by %s: %s", evidenceRef, reason))
	return out, nil
}

// RecordMissing adds an item that would bear on the claim and was not
// found. It never changes the status: an absence is not a finding.
func (c Claim) RecordMissing(what string) Claim {
	out := c
	out.MissingEvidence = appendUnique(out.MissingEvidence, what)
	return out
}

// WhatWouldChangeOurMind renders the claim's own falsification
// conditions, which is the signature output of the specification's
// section 58.
func (c Claim) WhatWouldChangeOurMind() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Current: %s -- %s\n", c.Status, c.Statement)
	fmt.Fprintf(&b, "Scope:   %s\n", c.Scope)
	fmt.Fprintf(&b, "Would change this conclusion: %s\n", c.DisproofPath)
	if len(c.ContradictingEvidence) > 0 {
		fmt.Fprintf(&b, "Known contradictions: %s\n",
			strings.Join(sorted(c.ContradictingEvidence), ", "))
	}
	if len(c.MissingEvidence) > 0 {
		fmt.Fprintf(&b, "Not found (which is not the same as absent): %s\n",
			strings.Join(sorted(c.MissingEvidence), ", "))
	}
	if len(c.AlternativeHypotheses) > 0 {
		fmt.Fprintf(&b, "Alternatives considered: %s\n",
			strings.Join(sorted(c.AlternativeHypotheses), ", "))
	}
	if len(c.Limitations) > 0 {
		fmt.Fprintf(&b, "Limitations: %s\n", strings.Join(c.Limitations, "; "))
	}
	return b.String()
}

// WhyNot answers the negative questions of specification section 57.
func (c Claim) WhyNot() map[string]string {
	out := map[string]string{}
	if !c.Status.Establishes() {
		switch c.Status {
		case Inconclusive:
			out["why not supported"] = "the evidence was examined and does not decide the question"
		case Contradicted:
			out["why not supported"] = "contradicting evidence was found: " +
				strings.Join(sorted(c.ContradictingEvidence), ", ")
		case Unresolved, NotDetermined:
			out["why not supported"] = "the work has not been done; this is an absence of " +
				"assessment, not a finding"
		case PartiallySupported:
			out["why not supported"] = "part of the scope is established and part is not"
		}
	}
	if len(c.MissingEvidence) > 0 {
		out["why unresolved"] = "these would bear on it and were not found: " +
			strings.Join(sorted(c.MissingEvidence), ", ")
	}
	if len(c.AlternativeHypotheses) > 0 {
		out["why not the alternatives"] = "considered and not adopted: " +
			strings.Join(sorted(c.AlternativeHypotheses), ", ")
	}
	return out
}

func intersect(a, b []string) []string {
	set := map[string]bool{}
	for _, x := range a {
		set[x] = true
	}
	var out []string
	for _, y := range b {
		if set[y] {
			out = append(out, y)
		}
	}
	sort.Strings(out)
	return out
}

func appendUnique(xs []string, v string) []string {
	for _, x := range xs {
		if x == v {
			return xs
		}
	}
	return append(append([]string(nil), xs...), v)
}

func remove(xs []string, v string) []string {
	var out []string
	for _, x := range xs {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

func sorted(xs []string) []string {
	out := append([]string(nil), xs...)
	sort.Strings(out)
	return out
}
