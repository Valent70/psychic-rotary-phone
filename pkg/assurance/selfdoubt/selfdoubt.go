// Package selfdoubt implements the VERIQO Self-Doubt Principle.
//
// # The principle
//
//	Setiap claim yang dibuat VERIQO harus mempunyai mekanisme untuk
//	mencoba membuktikan claim tersebut salah.
//
//	             CLAIM
//	       ┌───────┴────────┐
//	   PROOF PATH       DISPROOF PATH
//	       │                │
//	   Evidence          Counterexample
//	       │                │
//	  PASS/ASSURE       FAIL/DEMOTE
//	       └───────┬────────┘
//	          QUALIFICATION
//
// # Why a proof path alone is not enough
//
// A claim with only a proof path can be established and never
// questioned. Every test in this repository is a proof path: it
// demonstrates the control behaving. None of them is designed to make
// the claim FALSE, so a claim can be true of the cases somebody thought
// of and false in general -- which is the overfitting the review named,
// stated as an architecture rather than as a worry.
//
// A disproof path is not a negative test. A negative test is part of
// the proof path: it demonstrates the control refusing, which is the
// control working. A disproof path attempts to produce a
// COUNTEREXAMPLE to the claim itself, and succeeds when the claim is
// wrong.
//
// # What this package does
//
// It refuses to record a claim that has no disproof path, and it makes
// the outcome of running that path change the claim's standing:
// a surviving counterexample DEMOTES, which is the half of the diagram
// most systems leave out.
package selfdoubt

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"veriqo/pkg/qualification/ledger"
)

var (
	ErrNoClaim        = errors.New("selfdoubt: a claim must state what it asserts")
	ErrNoProofPath    = errors.New("selfdoubt: a claim must name how it would be established")
	ErrNoDisproofPath = errors.New("selfdoubt: a claim must name how somebody would try to make it false")
	ErrPathsIdentical = errors.New("selfdoubt: the disproof path repeats the proof path, so nothing attacks the claim")
	ErrDemotedButHeld = errors.New("selfdoubt: a counterexample was found and the claim was not demoted")
)

// Outcome is what running both paths produced.
type Outcome string

const (
	// NotRun: neither path has been executed.
	NotRun Outcome = ""
	// Established: the proof path succeeded and the disproof path
	// found no counterexample.
	Established Outcome = "ESTABLISHED"
	// Refuted: the disproof path produced a counterexample. The claim
	// is false and must be demoted or withdrawn.
	Refuted Outcome = "REFUTED"
	// Unsettled: the proof path succeeded but the disproof path was
	// not run, or could not be run. The claim is NOT established --
	// it is untested against attack.
	Unsettled Outcome = "UNSETTLED"
)

// Claim is one assertion VERIQO makes, with both paths.
type Claim struct {
	// ID is stable and citable.
	ID string
	// Assertion is what is claimed, in a form that could be false.
	Assertion string
	// ProofPath is how it would be established.
	ProofPath string
	// DisproofPath is how somebody would try to make it false. Not a
	// negative test: an attempt to produce a counterexample.
	DisproofPath string
	// Counterexample is what the disproof path found, if anything.
	// It is OPEN: a claim carrying one is refuted and demoted.
	Counterexample string
	// ClosedCounterexample is a counterexample the disproof path DID
	// produce and that has since been fixed, with FixedBy naming the
	// fix.
	//
	// It exists because "we attacked this and found nothing" and "we
	// attacked this, found a defect, and closed it" are different
	// pieces of evidence, and the second is much stronger. A claim
	// that has never yielded anything to attack may mean the control
	// is sound or may mean the attack was weak; a claim whose attack
	// once succeeded is a claim whose attack is known to be capable of
	// succeeding. Collapsing the two -- by deleting the history once
	// the fix lands -- throws away the only evidence that the disproof
	// path is real.
	ClosedCounterexample string
	// FixedBy cites what closed the ClosedCounterexample: the change,
	// and the test that now fails without it.
	FixedBy string
	// Outcome is the result of running both.
	Outcome Outcome
	// Level is the assurance level the claim is recorded at. A refuted
	// claim may not hold one above Implemented.
	Level ledger.Level
	// DisproofRunner names who ran the disproof path. When it is
	// VERIQO, the claim is self-tested however hard the attack was.
	DisproofRunner string
}

// Validate refuses a claim that cannot be attacked.
func (c Claim) Validate() error {
	if strings.TrimSpace(c.Assertion) == "" {
		return ErrNoClaim
	}
	if strings.TrimSpace(c.ProofPath) == "" {
		return fmt.Errorf("%w: %s", ErrNoProofPath, c.ID)
	}
	if strings.TrimSpace(c.DisproofPath) == "" {
		return fmt.Errorf("%w: %s", ErrNoDisproofPath, c.ID)
	}
	if normalise(c.ProofPath) == normalise(c.DisproofPath) {
		return fmt.Errorf("%w: %s", ErrPathsIdentical, c.ID)
	}
	// A counterexample that did not demote the claim is the failure
	// this package exists to prevent: the disproof path ran, found
	// something, and the claim stood anyway.
	if strings.TrimSpace(c.Counterexample) != "" {
		if c.Outcome != Refuted {
			return fmt.Errorf("%w: %s found %q but records outcome %s",
				ErrDemotedButHeld, c.ID, c.Counterexample, c.Outcome)
		}
		if c.Level > ledger.Implemented {
			return fmt.Errorf("%w: %s is refuted and still recorded at %s",
				ErrDemotedButHeld, c.ID, c.Level)
		}
	}
	if c.Outcome == Refuted && strings.TrimSpace(c.Counterexample) == "" {
		return fmt.Errorf("selfdoubt: %s is REFUTED but names no counterexample", c.ID)
	}
	// A closed counterexample with nothing cited as closing it is an
	// assertion that a defect went away.
	if strings.TrimSpace(c.ClosedCounterexample) != "" {
		if strings.TrimSpace(c.FixedBy) == "" {
			return fmt.Errorf("%w: %s records a closed counterexample and cites nothing "+
				"that closed it", ErrNoDisproofPath, c.ID)
		}
		if strings.TrimSpace(c.Counterexample) != "" {
			return fmt.Errorf("%w: %s carries an open and a closed counterexample at once",
				ErrDemotedButHeld, c.ID)
		}
	}
	return nil
}

// Yielded reports whether attacking this claim has ever produced a
// defect -- open or closed. It is the question a reader should ask
// before believing an ESTABLISHED verdict, because a disproof path
// that has never found anything has not been shown to be capable of
// finding anything.
func (c Claim) Yielded() bool {
	return strings.TrimSpace(c.Counterexample) != "" ||
		strings.TrimSpace(c.ClosedCounterexample) != ""
}

// Established reports whether the claim survived attack. An Unsettled
// claim is deliberately not established: a claim nobody tried to break
// has not been shown to hold, only shown to work.
func (c Claim) IsEstablished() bool {
	return c.Validate() == nil && c.Outcome == Established
}

// SelfAttacked reports whether the only party that tried to break the
// claim was VERIQO. True for everything in this repository, and worth
// stating on every claim rather than once in a preface.
func (c Claim) SelfAttacked() bool {
	r := strings.ToLower(strings.TrimSpace(c.DisproofRunner))
	if r == "" {
		return true
	}
	for _, s := range []string{"veriqo", "internal", "ourselves", "self"} {
		if strings.Contains(r, s) {
			return true
		}
	}
	return false
}

// Describe renders a claim with both paths.
func (c Claim) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s [%s at %s]\n", c.ID, c.Outcome, c.Level)
	fmt.Fprintf(&b, "    claim:    %s\n", c.Assertion)
	fmt.Fprintf(&b, "    proof:    %s\n", c.ProofPath)
	fmt.Fprintf(&b, "    disproof: %s\n", c.DisproofPath)
	if c.Counterexample != "" {
		fmt.Fprintf(&b, "    COUNTEREXAMPLE: %s\n", c.Counterexample)
	}
	if c.ClosedCounterexample != "" {
		fmt.Fprintf(&b, "    counterexample found and closed: %s\n", c.ClosedCounterexample)
		fmt.Fprintf(&b, "    closed by: %s\n", c.FixedBy)
	}
	if c.SelfAttacked() {
		b.WriteString("    attacked by: VERIQO only -- the claim has not been attacked from outside\n")
	} else {
		fmt.Fprintf(&b, "    attacked by: %s\n", c.DisproofRunner)
	}
	return b.String()
}

// Register is the set of claims VERIQO makes.
type Register struct {
	claims []Claim
}

// NewRegister builds a validated register.
func NewRegister(claims ...Claim) (*Register, error) {
	r := &Register{}
	seen := map[string]bool{}
	for _, c := range claims {
		if err := c.Validate(); err != nil {
			return nil, err
		}
		if seen[c.ID] {
			return nil, fmt.Errorf("selfdoubt: claim %s appears twice", c.ID)
		}
		seen[c.ID] = true
		r.claims = append(r.claims, c)
	}
	return r, nil
}

// All returns a copy.
func (r *Register) All() []Claim { return append([]Claim(nil), r.claims...) }

// Unsettled returns the claims nobody has tried to break. A non-empty
// result is the finding.
func (r *Register) Unsettled() []string {
	var out []string
	for _, c := range r.claims {
		if c.Outcome == Unsettled || c.Outcome == NotRun {
			out = append(out, c.ID)
		}
	}
	sort.Strings(out)
	return out
}

// Refuted returns the claims a counterexample defeated.
func (r *Register) Refuted() []string {
	var out []string
	for _, c := range r.claims {
		if c.Outcome == Refuted {
			out = append(out, c.ID)
		}
	}
	sort.Strings(out)
	return out
}

// Report renders the register.
func (r *Register) Report() string {
	var b strings.Builder
	b.WriteString("VERIQO Self-Doubt Register\n")
	b.WriteString("Every claim carries a DISPROOF path as well as a proof path.\n")
	b.WriteString("A claim nobody tried to break is UNSETTLED, not established: it has been\n")
	b.WriteString("shown to work, which is a different thing from having been shown to hold.\n\n")
	for _, c := range r.claims {
		b.WriteString(c.Describe())
		b.WriteString("\n")
	}
	selfOnly, established, establishedSelfOnly := 0, 0, 0
	for _, c := range r.claims {
		self := c.SelfAttacked()
		if self {
			selfOnly++
		}
		if c.Outcome == Established {
			established++
			if self {
				establishedSelfOnly++
			}
		}
	}
	fmt.Fprintf(&b, "%d claim(s). %d unsettled, %d refuted. %d of %d were attacked by VERIQO only.\n",
		len(r.claims), len(r.Unsettled()), len(r.Refuted()), selfOnly, len(r.claims))
	// The disclosure is about what VERIQO says it has ESTABLISHED. An
	// UNSETTLED claim whose disproof path names an outside party is not a
	// counterexample to it -- it is the register admitting the party has
	// not been engaged, which is the same admission stated differently.
	if established > 0 && establishedSelfOnly == established {
		fmt.Fprintf(&b, "All %d established claim(s) were attacked by VERIQO alone; no outside\n", established)
		b.WriteString("party has tried to break any of them. Surviving one's own attack\n")
		b.WriteString("is the weakest form of survival available.\n")
	}
	return b.String()
}

func normalise(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}
