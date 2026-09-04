// Package casefile is the VERIQO case room and the sequencing gate.
//
// # FC-007 SEQUENCING_BYPASS
//
// The finding was "resolve first, prove later": a case could be
// resolved and the proof attached afterwards, which produces a record
// where every event is present and the reasoning ran backwards.
//
// The fix has two halves, and the second is the one that is usually
// missing:
//
//  1. Resolve() refuses while any material claim is unproven.
//  2. The case records the GATE DECISION, not merely the order of
//     events. A ledger whose entries happen to be in the right order
//     proves nothing about whether anything was checked -- somebody
//     could append them in that order without a gate existing at all.
//
// So Resolve writes a Gate record naming what it checked, and
// Verify() requires that record to be present, to have been made
// BEFORE the resolution, and to cover every claim the case actually
// holds. A perfectly ordered case with no gate record fails.
//
// # What a case is
//
// One investigation: its evidence, entities, claims, hypotheses,
// contradictions, findings, decisions, limitations and replay handle.
// It is the single conceptual interface the specification asks for --
// not twenty dashboards.
package casefile

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"veriqo/pkg/canonical/jcs"
	"veriqo/pkg/claim"
	"veriqo/pkg/contract"
	"veriqo/pkg/findings"
	"veriqo/pkg/reverseproof"
)

var (
	ErrNoCase              = errors.New("casefile: no case id")
	ErrClosed              = errors.New("casefile: the case is closed")
	ErrUnprovenClaim       = errors.New("casefile: a material claim has no reverse proof")
	ErrClaimNotEstablished = errors.New("casefile: a material claim is not established")
	ErrNoGate              = errors.New("casefile: the case was resolved without a recorded gate decision")
	ErrGateAfterResolution = errors.New("casefile: the gate decision post-dates the resolution")
	ErrGateIncomplete      = errors.New("casefile: the gate decision does not cover every material claim")
	ErrAlreadyResolved     = errors.New("casefile: the case is already resolved")
	ErrUnknownClaim        = errors.New("casefile: unknown claim")
	ErrNoFindings          = errors.New("casefile: a resolved case must produce at least one finding")
)

// Status is where the case is.
type Status string

const (
	Open     Status = "OPEN"
	Resolved Status = "RESOLVED"
	Closed   Status = "CLOSED"
	// Abandoned: the case ended without a determination. It is a real
	// outcome and it is not a failure; a system with no word for it
	// pushes analysts towards manufacturing a conclusion.
	Abandoned Status = "ABANDONED"
)

// Material marks a claim the case's conclusion depends on.
//
// Not every claim is material. A background claim about a vessel's
// flag may be interesting and not load-bearing. The gate applies to
// the material ones, and the distinction is recorded rather than
// inferred, so nobody can quietly reclassify a claim to get past the
// gate.
type Entry struct {
	Claim    claim.Claim
	Proof    *reverseproof.Proof
	Material bool
	// MaterialityReason is required when Material is true. "Why does
	// the conclusion depend on this" is the question a reviewer asks
	// first.
	MaterialityReason string
}

// Gate is the record of a sequencing check.
//
// It exists so that "the gate ran" is a fact in the case rather than
// an inference from event order.
type Gate struct {
	At            time.Time `json:"at"`
	ClaimsChecked []string  `json:"claims_checked"`
	Passed        bool      `json:"passed"`
	Reason        string    `json:"reason"`
	// Digest binds the gate to the exact set of claims and proofs it
	// saw, so a gate record cannot be reused after the case changed.
	Digest string `json:"digest"`
}

// Case is one investigation.
type Case struct {
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
	Title    string `json:"title"`

	Status Status `json:"status"`

	entries  map[contract.ID]Entry
	findings []findings.Finding

	gate       *Gate
	resolvedAt time.Time

	Limitations []string `json:"limitations"`
	// Contradictions collects everything the case found that argues
	// against its own conclusion. It is on the case, not buried in the
	// claims, because a reader has to see it without walking the tree.
	Contradictions []string `json:"contradictions"`

	Versions contract.VersionSet `json:"versions"`
}

// New opens a case.
func New(id, tenantID, title string, versions contract.VersionSet) (*Case, error) {
	if strings.TrimSpace(id) == "" {
		return nil, ErrNoCase
	}
	if strings.TrimSpace(tenantID) == "" {
		return nil, errors.New("casefile: not anchored to a tenant")
	}
	if !versions.Complete() {
		return nil, fmt.Errorf("%w: %v", contract.ErrUnversioned, versions.Missing())
	}
	return &Case{ID: id, TenantID: tenantID, Title: title, Status: Open,
		entries: map[contract.ID]Entry{}, Versions: versions}, nil
}

// AddClaim records a claim, with whether the conclusion depends on it.
func (c *Case) AddClaim(cl claim.Claim, material bool, reason string) error {
	if c.Status != Open {
		return fmt.Errorf("%w: %s", ErrClosed, c.Status)
	}
	if err := cl.Validate(); err != nil {
		return err
	}
	if cl.TenantID != c.TenantID {
		return fmt.Errorf("%w: %s", contract.ErrCrossTenant, cl.ID)
	}
	if cl.CaseID != c.ID {
		return fmt.Errorf("casefile: %s belongs to case %s", cl.ID, cl.CaseID)
	}
	if material && strings.TrimSpace(reason) == "" {
		return errors.New("casefile: a material claim must say why the conclusion depends on it")
	}
	c.entries[cl.ID] = Entry{Claim: cl, Material: material, MaterialityReason: reason}
	for _, ce := range cl.ContradictingEvidence {
		c.Contradictions = appendUnique(c.Contradictions,
			fmt.Sprintf("%s: %s", cl.ID, ce))
	}
	return nil
}

// AttachProof records a claim's decomposition.
func (c *Case) AttachProof(claimID contract.ID, p *reverseproof.Proof) error {
	if c.Status != Open {
		return fmt.Errorf("%w: %s", ErrClosed, c.Status)
	}
	e, ok := c.entries[claimID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownClaim, claimID)
	}
	if p == nil {
		return ErrUnprovenClaim
	}
	if p.ClaimID != claimID {
		return fmt.Errorf("casefile: %s decomposes %s, not %s", p.ID, p.ClaimID, claimID)
	}
	e.Proof = p
	c.entries[claimID] = e

	// A refuted decomposition demotes the claim in the case, at the
	// moment the proof is attached rather than at resolution -- so the
	// case never holds a proven-false claim marked SUPPORTED.
	if p.Verdict() == reverseproof.VerdictRefuted {
		updated, err := p.ApplyTo(e.Claim)
		if err != nil {
			return err
		}
		e.Claim = updated
		c.entries[claimID] = e
	}
	return nil
}

// Materials returns the claims the conclusion depends on, sorted.
func (c *Case) Materials() []Entry {
	var out []Entry
	for _, e := range c.entries {
		if e.Material {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Claim.ID < out[j].Claim.ID })
	return out
}

// Entries returns every claim, sorted.
func (c *Case) Entries() []Entry {
	var out []Entry
	for _, e := range c.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Claim.ID < out[j].Claim.ID })
	return out
}

// gateDigest binds a gate to the exact claims and proofs it saw.
func (c *Case) gateDigest() (string, error) {
	type view struct {
		ClaimID  string `json:"claim_id"`
		Status   string `json:"status"`
		ProofID  string `json:"proof_id"`
		Verdict  string `json:"verdict"`
		Material bool   `json:"material"`
	}
	var vs []view
	for _, e := range c.Entries() {
		v := view{ClaimID: string(e.Claim.ID), Status: string(e.Claim.Status),
			Material: e.Material}
		if e.Proof != nil {
			v.ProofID = string(e.Proof.ID)
			v.Verdict = string(e.Proof.Verdict())
		}
		vs = append(vs, v)
	}
	return jcs.Hash(vs)
}

// RunGate performs the sequencing check and RECORDS it.
//
// It is separate from Resolve deliberately: the gate is a thing that
// happened at a time, and a case must be able to show that it happened
// before the resolution rather than as part of it.
func (c *Case) RunGate(at time.Time) (Gate, error) {
	if at.IsZero() {
		return Gate{}, errors.New("casefile: a gate decision needs an instant")
	}
	digest, err := c.gateDigest()
	if err != nil {
		return Gate{}, err
	}
	g := Gate{At: at, Digest: digest, Passed: true}

	materials := c.Materials()
	if len(materials) == 0 {
		g.Passed = false
		g.Reason = "the case names no material claim; a conclusion that depends on nothing " +
			"is not a conclusion"
		c.gate = &g
		return g, errors.New(g.Reason)
	}

	var problems []string
	for _, e := range materials {
		g.ClaimsChecked = append(g.ClaimsChecked, string(e.Claim.ID))
		if e.Proof == nil {
			problems = append(problems, fmt.Sprintf("%s has no reverse proof", e.Claim.ID))
			continue
		}
		switch e.Proof.Verdict() {
		case reverseproof.VerdictRefuted:
			problems = append(problems, fmt.Sprintf("%s is refuted by its own decomposition",
				e.Claim.ID))
		case reverseproof.VerdictIncomplete:
			problems = append(problems, fmt.Sprintf(
				"%s has %d unexamined necessary condition(s)",
				e.Claim.ID, countUnexamined(e.Proof)))
		}
		if !e.Claim.Status.Establishes() {
			problems = append(problems, fmt.Sprintf("%s is %s", e.Claim.ID, e.Claim.Status))
		}
	}
	sort.Strings(g.ClaimsChecked)

	if len(problems) > 0 {
		g.Passed = false
		g.Reason = strings.Join(problems, "; ")
		c.gate = &g
		return g, fmt.Errorf("%w: %s", ErrUnprovenClaim, g.Reason)
	}
	g.Reason = fmt.Sprintf("every material claim (%s) carries a decomposition that is not "+
		"refuted and has no unexamined necessary condition",
		strings.Join(g.ClaimsChecked, ", "))
	c.gate = &g
	return g, nil
}

func countUnexamined(p *reverseproof.Proof) int {
	_, un := p.Missing()
	return len(un)
}

// Resolve concludes the case.
//
// It re-runs the gate rather than trusting a previous run: a gate
// decision made before the last claim was added would be a gate that
// checked a different case.
func (c *Case) Resolve(fs []findings.Finding, at time.Time) error {
	if c.Status == Resolved || c.Status == Closed {
		return fmt.Errorf("%w: %s", ErrAlreadyResolved, c.Status)
	}
	if at.IsZero() {
		return errors.New("casefile: a resolution needs an instant")
	}
	if len(fs) == 0 {
		return ErrNoFindings
	}
	for _, f := range fs {
		if err := findings.Require(f); err != nil {
			return err
		}
		if f.CaseID != c.ID {
			return fmt.Errorf("casefile: finding %s belongs to case %s", f.ID, f.CaseID)
		}
		if f.TenantID != c.TenantID {
			return fmt.Errorf("%w: finding %s", contract.ErrCrossTenant, f.ID)
		}
	}

	g, err := c.RunGate(at)
	if err != nil {
		return err
	}
	if !g.Passed {
		return fmt.Errorf("%w: %s", ErrUnprovenClaim, g.Reason)
	}

	c.findings = append([]findings.Finding(nil), fs...)
	c.Status = Resolved
	c.resolvedAt = at
	for _, f := range fs {
		for _, l := range f.Limitations {
			c.Limitations = appendUnique(c.Limitations, l)
		}
	}
	sort.Strings(c.Limitations)
	return nil
}

// Abandon ends a case without a determination.
func (c *Case) Abandon(reason string, at time.Time) error {
	if c.Status != Open {
		return fmt.Errorf("%w: %s", ErrClosed, c.Status)
	}
	if strings.TrimSpace(reason) == "" {
		return errors.New("casefile: abandoning a case requires a stated reason")
	}
	c.Status = Abandoned
	c.resolvedAt = at
	c.Limitations = appendUnique(c.Limitations, "the case was abandoned: "+reason)
	return nil
}

// Gate returns the recorded gate decision.
func (c *Case) Gate() (Gate, bool) {
	if c.gate == nil {
		return Gate{}, false
	}
	return *c.gate, true
}

// Findings returns the case's findings.
func (c *Case) Findings() []findings.Finding {
	return append([]findings.Finding(nil), c.findings...)
}

// ResolvedAt returns when.
func (c *Case) ResolvedAt() time.Time { return c.resolvedAt }

// Verify checks a resolved case's integrity.
//
// This is the half FC-007 is really about. It does NOT check event
// order: it checks that a gate decision EXISTS, that it passed, that
// it was made no later than the resolution, and that it saw the case
// as it now stands. A perfectly ordered case with no gate record fails
// here, which is the point -- order is not evidence that anything was
// checked.
func (c *Case) Verify() error {
	if c.Status != Resolved {
		return nil
	}
	g, ok := c.Gate()
	if !ok {
		return fmt.Errorf("%w: %s", ErrNoGate, c.ID)
	}
	if !g.Passed {
		return fmt.Errorf("casefile: %s is resolved and its gate decision did not pass: %s",
			c.ID, g.Reason)
	}
	if g.At.After(c.resolvedAt) {
		return fmt.Errorf("%w: gate at %s, resolved at %s",
			ErrGateAfterResolution, g.At.Format(time.RFC3339), c.resolvedAt.Format(time.RFC3339))
	}
	// The gate must have seen this case, not an earlier version of it.
	digest, err := c.gateDigest()
	if err != nil {
		return err
	}
	if digest != g.Digest {
		return fmt.Errorf("%w: the case changed after the gate decision was recorded", ErrGateIncomplete)
	}
	// And it must have covered every material claim.
	checked := map[string]bool{}
	for _, id := range g.ClaimsChecked {
		checked[id] = true
	}
	for _, e := range c.Materials() {
		if !checked[string(e.Claim.ID)] {
			return fmt.Errorf("%w: %s was not checked", ErrGateIncomplete, e.Claim.ID)
		}
	}
	if len(c.findings) == 0 {
		return ErrNoFindings
	}
	return nil
}

// Report renders the case room.
func (c *Case) Report() string {
	var b strings.Builder
	fmt.Fprintf(&b, "CASE %s -- %s [%s]\n", c.ID, c.Title, c.Status)
	b.WriteString("  claims:\n")
	for _, e := range c.Entries() {
		mark := " "
		if e.Material {
			mark = "*"
		}
		proof := "NO PROOF"
		if e.Proof != nil {
			proof = string(e.Proof.Verdict())
		}
		fmt.Fprintf(&b, "   %s %-12s %-20s %s\n", mark, e.Claim.ID, e.Claim.Status, proof)
		if e.Material {
			fmt.Fprintf(&b, "       material because: %s\n", e.MaterialityReason)
		}
	}
	if g, ok := c.Gate(); ok {
		verdict := "PASSED"
		if !g.Passed {
			verdict = "DID NOT PASS"
		}
		fmt.Fprintf(&b, "  sequencing gate at %s: %s -- %s\n",
			g.At.UTC().Format(time.RFC3339), verdict, g.Reason)
	} else {
		b.WriteString("  sequencing gate: NOT RUN\n")
	}
	if len(c.Contradictions) > 0 {
		b.WriteString("  contradictions found:\n")
		for _, x := range c.Contradictions {
			fmt.Fprintf(&b, "    - %s\n", x)
		}
	}
	for _, f := range c.findings {
		fmt.Fprintf(&b, "  finding %s: %s\n", f.ID, f.Statement)
	}
	if len(c.Limitations) > 0 {
		b.WriteString("  limitations:\n")
		for _, l := range c.Limitations {
			fmt.Fprintf(&b, "    - %s\n", l)
		}
	}
	return b.String()
}

func appendUnique(xs []string, v string) []string {
	for _, x := range xs {
		if x == v {
			return xs
		}
	}
	return append(xs, v)
}
