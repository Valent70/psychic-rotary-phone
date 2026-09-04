// Package plan is the VERIQO Qualification Evidence Plan.
//
// # The paradigm change this encodes
//
// The review asked for the question to change. Not:
//
//	How do we close all the gaps?
//
// but:
//
//	What evidence is required to legitimately promote each control to
//	the next assurance level?
//
// Twenty-two ASSURANCE_GAP and six EXTERNAL_QUALIFICATION rows are not
// a backlog of things to fix. Most of them cannot be fixed by writing
// code at all: they are waiting on somebody who is not VERIQO. Treating
// them as a backlog produces exactly the pressure that closes gaps
// dishonestly.
//
// So every open control gets five things instead of a status:
//
//	PROOF OBLIGATION   what must be true for the level to be earned
//	TEST METHOD        how it would be demonstrated
//	EVIDENCE ARTEFACT  what the demonstration leaves behind
//	PASS/FAIL CRITERIA what counts as having met it
//	VALIDATOR          who must independently confirm it
//
// The VALIDATOR field is the one that does the work. A plan where
// VERIQO validates everything is a plan to remain self-tested forever
// while producing documents that read like qualification.
package plan

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"veriqo/pkg/qualification/ledger"
)

var (
	ErrNoObligation = errors.New("plan: an item must state its proof obligation")
	ErrNoMethod     = errors.New("plan: an item must state a test method")
	ErrNoArtefact   = errors.New("plan: an item must name the evidence artefact the method leaves behind")
	ErrNoCriteria   = errors.New("plan: an item must state what counts as passing")
	ErrNoValidator  = errors.New("plan: an item must name who validates it")
	ErrSelfValidate = errors.New("plan: VERIQO cannot validate its own promotion to a level that requires an outside party")
)

// Validator is who confirms an obligation was met.
type Validator string

const (
	// VeriqoEngineering is honest for promotions up to the internal
	// ceiling and dishonest for anything above it.
	VeriqoEngineering Validator = "VERIQO engineering"
	// IndependentAssessor is a named third party engaged to examine
	// procedure. None is engaged.
	IndependentAssessor Validator = "an independent assessor (none engaged)"
	// AdversarialLab is a party engaged to attempt falsification --
	// stronger than examination.
	AdversarialLab Validator = "an adversarial recovery lab (none engaged)"
	// ExternalTSA is a timestamping authority independent of the matter.
	ExternalTSA Validator = "an external timestamping authority (none engaged)"
	// DataPartner is a counterparty supplying real commercial data
	// under an agreement.
	DataPartner Validator = "a data partner under a real data agreement (none engaged)"
	// LegalCounsel is required where the question is legal effect
	// rather than technical behaviour.
	LegalCounsel Validator = "qualified legal counsel in the relevant jurisdiction (none engaged)"
	// Accreditor is a body qualifying against a published standard.
	Accreditor Validator = "an accrediting body against a published standard (none engaged)"
)

// IsExternal reports whether the validator is somebody other than
// VERIQO.
func (v Validator) IsExternal() bool { return v != VeriqoEngineering }

// Item is one control's route to its next level.
type Item struct {
	Article int
	Control string
	// From is the level the control holds today.
	From ledger.Level
	// To is the next level, and the one this item plans for.
	To ledger.Level
	// Obligation states what must be true. Not what must be built --
	// what must be TRUE, which is a different and usually larger thing.
	Obligation string
	// Method is how it would be demonstrated.
	Method string
	// Artefact is what the demonstration leaves behind for somebody
	// else to check.
	Artefact string
	// Criteria states what counts as having met it, in terms that can
	// fail.
	Criteria string
	// Validator is who confirms it.
	Validator Validator
	// Blocker names what stands in the way today, or is empty when the
	// item is actionable now.
	Blocker string
}

// Actionable reports whether VERIQO could do this today.
func (i Item) Actionable() bool { return strings.TrimSpace(i.Blocker) == "" }

// Validate refuses an item that could not be acted on or checked.
func (i Item) Validate() error {
	for _, f := range []struct {
		v   string
		err error
	}{
		{i.Obligation, ErrNoObligation}, {i.Method, ErrNoMethod},
		{i.Artefact, ErrNoArtefact}, {i.Criteria, ErrNoCriteria},
	} {
		if strings.TrimSpace(f.v) == "" {
			return fmt.Errorf("%w: article %d", f.err, i.Article)
		}
	}
	if strings.TrimSpace(string(i.Validator)) == "" {
		return fmt.Errorf("%w: article %d", ErrNoValidator, i.Article)
	}
	// The rule that makes the plan honest: a promotion to a level
	// requiring an outside party may not be validated by VERIQO.
	if i.To.RequiresOutsideParty() && !i.Validator.IsExternal() {
		return fmt.Errorf("%w: article %d plans %s with validator %q",
			ErrSelfValidate, i.Article, i.To, i.Validator)
	}
	if i.To <= i.From {
		return fmt.Errorf("plan: article %d plans a move from %s to %s, which is not a promotion",
			i.Article, i.From, i.To)
	}
	return nil
}

// Summary counts a plan.
type Summary struct {
	Total int
	// Actionable is the number VERIQO could act on today. This is the
	// only number that is a to-do list.
	Actionable int
	// BlockedExternal is the number waiting on somebody else.
	BlockedExternal int
	// ByValidator groups by who must confirm.
	ByValidator map[Validator]int
}

// Headline states the plan in a form that cannot be quoted as progress.
func (s Summary) Headline() string {
	return fmt.Sprintf("%d open controls: %d actionable by VERIQO today, %d waiting on an outside party. "+
		"The second number does not shrink by working harder.",
		s.Total, s.Actionable, s.BlockedExternal)
}

// Summarize counts the plan.
func Summarize(items []Item) Summary {
	s := Summary{Total: len(items), ByValidator: map[Validator]int{}}
	for _, i := range items {
		s.ByValidator[i.Validator]++
		if i.Actionable() {
			s.Actionable++
		} else {
			s.BlockedExternal++
		}
	}
	return s
}

// Validate checks the whole plan.
func Validate(items []Item) error {
	seen := map[int]bool{}
	for _, i := range items {
		if err := i.Validate(); err != nil {
			return err
		}
		if seen[i.Article] {
			return fmt.Errorf("plan: article %d appears twice", i.Article)
		}
		seen[i.Article] = true
	}
	return nil
}

// Report renders the plan.
func Report(items []Item) string {
	var b strings.Builder
	b.WriteString("VERIQO Qualification Evidence Plan\n")
	b.WriteString("What evidence would legitimately promote each open control to its next level.\n")
	b.WriteString("This is NOT a backlog: most items are waiting on somebody who is not VERIQO,\n")
	b.WriteString("and no amount of engineering shortens that queue.\n\n")

	sorted := append([]Item(nil), items...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Actionable() != sorted[j].Actionable() {
			return sorted[i].Actionable()
		}
		return sorted[i].Article < sorted[j].Article
	})

	section := ""
	for _, i := range sorted {
		want := "BLOCKED ON AN OUTSIDE PARTY"
		if i.Actionable() {
			want = "ACTIONABLE BY VERIQO TODAY"
		}
		if want != section {
			fmt.Fprintf(&b, "== %s ==\n\n", want)
			section = want
		}
		fmt.Fprintf(&b, "Article %d  %s -> %s\n    %s\n", i.Article, i.From, i.To, i.Control)
		fmt.Fprintf(&b, "    obligation: %s\n", i.Obligation)
		fmt.Fprintf(&b, "    method:     %s\n", i.Method)
		fmt.Fprintf(&b, "    artefact:   %s\n", i.Artefact)
		fmt.Fprintf(&b, "    criteria:   %s\n", i.Criteria)
		fmt.Fprintf(&b, "    validator:  %s\n", i.Validator)
		if !i.Actionable() {
			fmt.Fprintf(&b, "    blocked by: %s\n", i.Blocker)
		}
		b.WriteString("\n")
	}

	s := Summarize(items)
	b.WriteString(s.Headline())
	b.WriteString("\n\nBy validator:\n")
	var vs []string
	for v := range s.ByValidator {
		vs = append(vs, string(v))
	}
	sort.Strings(vs)
	for _, v := range vs {
		fmt.Fprintf(&b, "    %-58s %d\n", v, s.ByValidator[Validator(v)])
	}
	return b.String()
}
